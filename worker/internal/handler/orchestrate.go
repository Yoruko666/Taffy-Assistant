package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// OrchestrateHandler 实现 Server ↔ Worker 之间的 WS 端点（/v1/orchestrate）。
//
// 协议与"家具端 ↔ Server"完全一致，Server 只在中间做透传：
//
//	上行（server → worker）：
//	  text   {"type":"start", ...}
//	  binary <16-bit LE PCM mono 字节流>
//	  text   {"type":"end"}
//	  text   {"type":"ping"}
//
//	下行（worker → server）：
//	  text   {"type":"asr_partial","text":"..."}
//	  text   {"type":"asr_final","text":"..."}
//	  text   {"type":"eos"}
//	  text   {"type":"llm_result","text":"..."}
//	  text   {"type":"llm_error","message":"..."}
//	  text   {"type":"error","message":"..."}
//	  text   {"type":"pong"}
//
// Worker 在内部做的事：
//   - 把上游的 start / PCM / end 透传给本地 ASR（FunASR :9100 流式 WS）
//   - 把 ASR 的 partial/final/eos 翻译为 asr_partial/asr_final/eos 返回上游
//   - asr_final 触发异步调用云端 LLM，返回 llm_result / llm_error
//   - 未来：asr_final 后并行调用 TTS / 解析设备指令
type OrchestrateHandler struct {
	cfg      *AppConfig
	upgrader websocket.Upgrader
	dialer   *websocket.Dialer
}

// NewOrchestrateHandler 构造 worker orchestrate handler。
func NewOrchestrateHandler(cfg *AppConfig) *OrchestrateHandler {
	return &OrchestrateHandler{
		cfg: cfg,
		upgrader: websocket.Upgrader{
			ReadBufferSize:  64 * 1024,
			WriteBufferSize: 64 * 1024,
			CheckOrigin:     func(*http.Request) bool { return true },
		},
		dialer: &websocket.Dialer{
			HandshakeTimeout: 10 * time.Second,
			ReadBufferSize:   64 * 1024,
			WriteBufferSize:  64 * 1024,
		},
	}
}

func (h *OrchestrateHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	deviceID := r.URL.Query().Get("device_id")
	sessionID := r.URL.Query().Get("session_id")
	log := slog.With("path", "/v1/orchestrate", "device_id", deviceID, "session_id", sessionID, "remote", r.RemoteAddr)

	// 1) 升级上游（server）连接
	upConn, err := h.upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Error("upgrade failed", "err", err)
		return
	}
	defer upConn.Close()
	log.Info("upstream connected")

	asrURL := ""
	if h.cfg != nil {
		asrURL = h.cfg.ASR.WSURL
	}
	if asrURL == "" {
		log.Error("asr ws url not configured")
		_ = writeJSON(upConn, map[string]any{
			"type":    "error",
			"message": "asr backend not configured",
		})
		return
	}

	// 2) 拨号到 ASR
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	asrConn, _, err := h.dialer.DialContext(ctx, asrURL, nil)
	if err != nil {
		log.Error("dial asr failed", "asr", asrURL, "err", err)
		_ = writeJSON(upConn, map[string]any{
			"type":    "error",
			"message": "asr backend unreachable",
		})
		return
	}
	defer asrConn.Close()
	log.Info("asr connected", "asr", asrURL)

	// 3) 双向转发 + LLM 处理
	stop := make(chan struct{})
	var once sync.Once
	closeStop := func() { once.Do(func() { close(stop) }) }

	// 保护 upConn 写入（ASR 回传 goroutine + LLM goroutine 可能同时写）
	var writeMu sync.Mutex
	safeWriteUp := func(v any) error {
		writeMu.Lock()
		defer writeMu.Unlock()
		b, err := json.Marshal(v)
		if err != nil {
			return err
		}
		return upConn.WriteMessage(websocket.TextMessage, b)
	}

	wg := &sync.WaitGroup{}
	wg.Add(2)

	// ---------- upstream -> asr ----------
	go func() {
		defer wg.Done()
		defer closeStop()
		for {
			mt, data, err := upConn.ReadMessage()
			if err != nil {
				if isNormalClose(err) {
					log.Info("upstream closed")
				} else {
					log.Warn("upstream read err", "err", err)
				}
				_ = asrConn.WriteControl(
					websocket.CloseMessage,
					websocket.FormatCloseMessage(websocket.CloseNormalClosure, "upstream gone"),
					time.Now().Add(time.Second),
				)
				return
			}

			// 拦截 ping，自己回 pong（Server 不感知 worker 的保活细节）
			if mt == websocket.TextMessage && isPing(data) {
				_ = safeWriteUp(map[string]any{"type": "pong"})
				continue
			}

			if err := asrConn.WriteMessage(mt, data); err != nil {
				log.Warn("asr write err", "err", err)
				return
			}
		}
	}()

	// ---------- asr -> upstream ----------
	go func() {
		defer wg.Done()
		defer closeStop()
		for {
			mt, data, err := asrConn.ReadMessage()
			if err != nil {
				if isNormalClose(err) {
					log.Info("asr closed")
				} else {
					log.Warn("asr read err", "err", err)
				}
				return
			}

			// 二进制直接透传
			if mt != websocket.TextMessage {
				writeMu.Lock()
				werr := upConn.WriteMessage(mt, data)
				writeMu.Unlock()
				if werr != nil {
					log.Warn("upstream write(bin) err", "err", werr)
					return
				}
				continue
			}

			// 翻译 ASR 事件并发送
			out := translateASREvent(data)
			writeMu.Lock()
			werr := upConn.WriteMessage(websocket.TextMessage, out)
			writeMu.Unlock()
			if werr != nil {
				log.Warn("upstream write err", "err", werr)
				return
			}

			// 收到 asr_final → 异步调用大模型（空文本则直接放行）
			if h.cfg != nil && h.cfg.LLM.URL != "" {
				var ev map[string]any
				if err := json.Unmarshal(out, &ev); err == nil {
					if t, _ := ev["type"].(string); t == "asr_final" {
						if text, ok := ev["text"].(string); ok && text != "" {
							go h.handleLLMResult(upConn, text, &writeMu, log)
						} else {
							log.Info("asr_final empty text, skip llm")
							writeMu.Lock()
							_ = writeJSON(upConn, map[string]any{
								"type": "llm_result",
								"text": "",
							})
							writeMu.Unlock()
						}
					}
				}
			}
		}
	}()

	// 等任意一边结束
	<-stop
	_ = upConn.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
	_ = asrConn.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
	wg.Wait()
	log.Info("session done")
}

// handleLLMResult 调用大模型 API，将结果通过 ws 送回上游。
func (h *OrchestrateHandler) handleLLMResult(conn *websocket.Conn, text string, writeMu *sync.Mutex, log *slog.Logger) {
	t0 := time.Now()
	reply, err := h.callLLM(text)
	cost := time.Since(t0).Milliseconds()

	if err != nil {
		log.Warn("llm call failed", "err", err, "cost_ms", cost)
		writeMu.Lock()
		_ = writeJSON(conn, map[string]any{
			"type":    "llm_error",
			"message": err.Error(),
		})
		writeMu.Unlock()
		return
	}

	log.Info("llm ok", "cost_ms", cost, "reply_len", len(reply))
	writeMu.Lock()
	_ = writeJSON(conn, map[string]any{
		"type": "llm_result",
		"text": reply,
	})
	writeMu.Unlock()
}

// callLLM 调用兼容 OpenAI Chat Completions 格式的大模型 API。
func (h *OrchestrateHandler) callLLM(text string) (string, error) {
	cfg := &h.cfg.LLM

	messages := []map[string]string{}
	if cfg.SystemPrompt != "" {
		messages = append(messages, map[string]string{"role": "system", "content": cfg.SystemPrompt})
	}
	messages = append(messages, map[string]string{"role": "user", "content": text})

	body := map[string]any{
		"model":    cfg.Model,
		"messages": messages,
		"stream":   false,
	}
	bodyJSON, err := json.Marshal(body)
	if err != nil {
		return "", err
	}

	req, err := http.NewRequest("POST", cfg.URL, bytes.NewReader(bodyJSON))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+cfg.APIKey)

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(cfg.Timeout)*time.Second)
	defer cancel()
	req = req.WithContext(ctx)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var result struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error,omitempty"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", err
	}

	if result.Error != nil && result.Error.Message != "" {
		return "", errors.New("llm api error: " + result.Error.Message)
	}

	if len(result.Choices) > 0 {
		return result.Choices[0].Message.Content, nil
	}
	return "", errors.New("llm response has no choices")
}

// translateASREvent 把 ASR 的事件 type 映射为家具协议（partial -> asr_partial, final -> asr_final）。
func translateASREvent(raw []byte) []byte {
	var ev map[string]any
	if err := json.Unmarshal(raw, &ev); err != nil {
		return raw
	}
	t, _ := ev["type"].(string)
	switch t {
	case "partial":
		ev["type"] = "asr_partial"
	case "final":
		ev["type"] = "asr_final"
	}
	out, err := json.Marshal(ev)
	if err != nil {
		return raw
	}
	return out
}

// isPing 判断是否是上游的保活帧 {"type":"ping"}。
func isPing(data []byte) bool {
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		return false
	}
	t, _ := m["type"].(string)
	return t == "ping"
}

func writeJSON(c *websocket.Conn, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return c.WriteMessage(websocket.TextMessage, b)
}

func isNormalClose(err error) bool {
	if errors.Is(err, websocket.ErrCloseSent) {
		return true
	}
	if ce, ok := err.(*websocket.CloseError); ok {
		switch ce.Code {
		case websocket.CloseNormalClosure,
			websocket.CloseGoingAway,
			websocket.CloseNoStatusReceived:
			return true
		}
	}
	return false
}
