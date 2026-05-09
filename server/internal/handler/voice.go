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

// VoiceHandler 实现家具端 ↔ Go Server WebSocket（/v1/voice）。
//
// M2 行为：
//   - 家具端 PCM → ASR 流式识别（同 M1，透传）
//   - ASR partial/final → 翻译为 asr_partial/asr_final 回家具
//   - asr_final 触发 → 异步调用大模型 API → llm_result 回家具
//   - 家具端收到 llm_result 后可恢复语音检测（多轮对话）
//
// 协议扩展（M2 新增）：
//
//	服务端 → 客户端：
//	  {"type":"llm_result","text":"..."}   ← 大模型回答
//	  {"type":"llm_error","message":"..."} ← 大模型调用出错
type VoiceHandler struct {
	asrWSURL  string
	llmConfig *LLMConfig
	upgrader  websocket.Upgrader
	dialer    *websocket.Dialer
}

// NewVoiceHandler 构造 voice handler。
func NewVoiceHandler(asrWSURL string, cfg *AppConfig) *VoiceHandler {
	var llmCfg *LLMConfig
	if cfg != nil {
		llmCfg = &cfg.LLM
	}
	return &VoiceHandler{
		asrWSURL:  asrWSURL,
		llmConfig: llmCfg,
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

func (h *VoiceHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	deviceID := r.URL.Query().Get("device_id")
	token := r.URL.Query().Get("token")
	log := slog.With("path", "/v1/voice", "device_id", deviceID, "remote", r.RemoteAddr)
	if token != "" {
		log = log.With("token_len", len(token))
	}

	// 1) 升级家具端连接
	clientConn, err := h.upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Error("upgrade failed", "err", err)
		return
	}
	defer clientConn.Close()
	log.Info("client connected")

	// 2) 拨号到 ASR
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	asrConn, _, err := h.dialer.DialContext(ctx, h.asrWSURL, nil)
	if err != nil {
		log.Error("dial asr failed", "asr", h.asrWSURL, "err", err)
		_ = writeJSON(clientConn, map[string]any{
			"type":    "error",
			"message": "asr backend unreachable",
		})
		return
	}
	defer asrConn.Close()
	log.Info("asr connected", "asr", h.asrWSURL)

	// 3) 双向转发 + LLM 处理
	stop := make(chan struct{})
	var once sync.Once
	closeStop := func() { once.Do(func() { close(stop) }) }

	// 保护 clientConn 写入（ASR 回传 goroutine + LLM goroutine 可能同时写）
	var writeMu sync.Mutex
	safeWriteClient := func(v any) error {
		writeMu.Lock()
		defer writeMu.Unlock()
		b, err := json.Marshal(v)
		if err != nil {
			return err
		}
		return clientConn.WriteMessage(websocket.TextMessage, b)
	}

	wg := &sync.WaitGroup{}
	wg.Add(2)

	// ---------- client -> asr ----------
	go func() {
		defer wg.Done()
		defer closeStop()
		for {
			mt, data, err := clientConn.ReadMessage()
			if err != nil {
				if isNormalClose(err) {
					log.Info("client closed")
				} else {
					log.Warn("client read err", "err", err)
				}
				_ = asrConn.WriteControl(
					websocket.CloseMessage,
					websocket.FormatCloseMessage(websocket.CloseNormalClosure, "client gone"),
					time.Now().Add(time.Second),
				)
				return
			}

			// 拦截 ping，自己回 pong
			if mt == websocket.TextMessage && isPing(data) {
				_ = safeWriteClient(map[string]any{"type": "pong"})
				continue
			}

			if err := asrConn.WriteMessage(mt, data); err != nil {
				log.Warn("asr write err", "err", err)
				return
			}
		}
	}()

	// ---------- asr -> client ----------
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
				werr := clientConn.WriteMessage(mt, data)
				writeMu.Unlock()
				if werr != nil {
					log.Warn("client write(bin) err", "err", werr)
					return
				}
				continue
			}

			// 翻译 ASR 事件并发送
			out := translateASREvent(data)
			writeMu.Lock()
			werr := clientConn.WriteMessage(websocket.TextMessage, out)
			writeMu.Unlock()
			if werr != nil {
				log.Warn("client write err", "err", werr)
				return
			}

			// M2: 收到 asr_final → 异步调用大模型（空文本则直接放行）
			if h.llmConfig != nil && h.llmConfig.URL != "" {
				var ev map[string]any
				if err := json.Unmarshal(out, &ev); err == nil {
					if t, _ := ev["type"].(string); t == "asr_final" {
						if text, ok := ev["text"].(string); ok && text != "" {
							go h.handleLLMResult(clientConn, text, &writeMu, log)
						} else {
							log.Info("asr_final empty text, skip llm")
							writeMu.Lock()
							_ = writeJSON(clientConn, map[string]any{
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
	_ = clientConn.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
	_ = asrConn.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
	wg.Wait()
	log.Info("session done")
}

// handleLLMResult 调用大模型 API，将结果通过 ws 送回客户端。
func (h *VoiceHandler) handleLLMResult(conn *websocket.Conn, text string, writeMu *sync.Mutex, log *slog.Logger) {
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
func (h *VoiceHandler) callLLM(text string) (string, error) {
	cfg := h.llmConfig

	// 组装 messages
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

// translateASREvent 把 ASR 的事件 type 映射成家具协议（partial -> asr_partial, final -> asr_final）。
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

// isPing 判断是否是家具端的保活帧 {"type":"ping"}。
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
