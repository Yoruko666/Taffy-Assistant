package handler

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// VoiceHandler 实现家具端 ↔ Server WebSocket（/v1/voice）。
//
// 拆分后职责：
//   - 接受家具端连接，做设备鉴权（device_id / token，M2 暂未启用）
//   - 把家具端整条 WS 会话原样转发给 Worker（/v1/orchestrate）
//   - 把 Worker 的响应原样回传给家具端
//   - 记录会话日志（未来：写入 MySQL/Redis 的对话历史、设备状态等）
//
// Server 不再做以下事情（已迁移到 Worker）：
//   - 直接对接 ASR
//   - 翻译 ASR 事件类型
//   - 调用云端 LLM
//   - 合成 TTS
type VoiceHandler struct {
	workerWSURL string
	upgrader    websocket.Upgrader
	dialer      *websocket.Dialer
}

// NewVoiceHandler 构造 voice handler，内部拨号到 worker。
func NewVoiceHandler(cfg *AppConfig) *VoiceHandler {
	workerURL := ""
	if cfg != nil {
		workerURL = cfg.Worker.WSURL
	}
	return &VoiceHandler{
		workerWSURL: workerURL,
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

	// TODO(M3)：在此处校验 device_id / token，未通过直接 401 拒绝。

	// 1) 升级家具端连接
	clientConn, err := h.upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Error("upgrade failed", "err", err)
		return
	}
	defer clientConn.Close()
	log.Info("client connected")

	if h.workerWSURL == "" {
		log.Error("worker ws url not configured")
		_ = writeJSON(clientConn, map[string]any{
			"type":    "error",
			"message": "worker not configured",
		})
		return
	}

	// 2) 拨号到 worker，把家具端的 device_id / session_id 通过 query 透传过去
	workerURL, err := buildWorkerURL(h.workerWSURL, deviceID)
	if err != nil {
		log.Error("build worker url failed", "err", err)
		_ = writeJSON(clientConn, map[string]any{
			"type":    "error",
			"message": "worker url invalid",
		})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	workerConn, _, err := h.dialer.DialContext(ctx, workerURL, nil)
	if err != nil {
		log.Error("dial worker failed", "worker", workerURL, "err", err)
		_ = writeJSON(clientConn, map[string]any{
			"type":    "error",
			"message": "worker backend unreachable",
		})
		return
	}
	defer workerConn.Close()
	log.Info("worker connected", "worker", workerURL)

	// 3) 双向纯透传
	stop := make(chan struct{})
	var once sync.Once
	closeStop := func() { once.Do(func() { close(stop) }) }

	// 写 clientConn 用 Mutex 保护（虽然当前只有一个 goroutine 写，
	// 但 buildWorkerURL/连接错误等路径也可能并发写，沿用稳妥实现）。
	var clientWriteMu sync.Mutex

	wg := &sync.WaitGroup{}
	wg.Add(2)

	// ---------- client -> worker ----------
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
				_ = workerConn.WriteControl(
					websocket.CloseMessage,
					websocket.FormatCloseMessage(websocket.CloseNormalClosure, "client gone"),
					time.Now().Add(time.Second),
				)
				return
			}
			if err := workerConn.WriteMessage(mt, data); err != nil {
				log.Warn("worker write err", "err", err)
				return
			}
		}
	}()

	// ---------- worker -> client ----------
	go func() {
		defer wg.Done()
		defer closeStop()
		for {
			mt, data, err := workerConn.ReadMessage()
			if err != nil {
				if isNormalClose(err) {
					log.Info("worker closed")
				} else {
					log.Warn("worker read err", "err", err)
				}
				return
			}

			clientWriteMu.Lock()
			werr := clientConn.WriteMessage(mt, data)
			clientWriteMu.Unlock()
			if werr != nil {
				log.Warn("client write err", "err", werr)
				return
			}

			// 会话观测：把关键事件打一行日志（不阻塞转发）
			if mt == websocket.TextMessage {
				logSessionEvent(log, data)
			}
		}
	}()

	<-stop
	_ = clientConn.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
	_ = workerConn.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
	wg.Wait()
	log.Info("session done")
}

// buildWorkerURL 在 worker 的 WS URL 上附加 device_id / session_id 等
// 透传参数，便于 worker 日志关联同一会话。
func buildWorkerURL(base, deviceID string) (string, error) {
	if deviceID == "" {
		return base, nil
	}
	u, err := url.Parse(base)
	if err != nil {
		return "", err
	}
	q := u.Query()
	q.Set("device_id", deviceID)
	q.Set("session_id", time.Now().UTC().Format("20060102T150405.000000000Z"))
	u.RawQuery = q.Encode()
	return u.String(), nil
}

// logSessionEvent 把 worker → client 的关键事件打一行精简日志，
// 便于在 server 侧观测会话进度（不影响转发性能）。
func logSessionEvent(log *slog.Logger, data []byte) {
	var ev map[string]any
	if err := json.Unmarshal(data, &ev); err != nil {
		return
	}
	t, _ := ev["type"].(string)
	switch t {
	case "asr_final":
		text, _ := ev["text"].(string)
		log.Info("event", "type", t, "text", text)
	case "llm_result":
		text, _ := ev["text"].(string)
		log.Info("event", "type", t, "len", len(text))
	case "llm_error", "error":
		msg, _ := ev["message"].(string)
		log.Warn("event", "type", t, "message", msg)
	}
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
