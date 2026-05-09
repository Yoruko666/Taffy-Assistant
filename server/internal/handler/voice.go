package handler

import (
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
// M1 行为：纯透传到 ASR 模型 WS（默认 ws://127.0.0.1:9100/v1/asr/stream），
// 把 ASR 的事件类型加上 asr_ 前缀（partial -> asr_partial, final -> asr_final）后回家具。
//
//   - 不做鉴权：device_id / token 仅记录日志；
//   - 不做 LLM / TTS / MQTT：那些事件在 M2~M3 落地。
type VoiceHandler struct {
	asrWSURL string
	upgrader websocket.Upgrader
	dialer   *websocket.Dialer
}

// NewVoiceHandler 构造一个透传 handler。
func NewVoiceHandler(asrWSURL string) *VoiceHandler {
	return &VoiceHandler{
		asrWSURL: asrWSURL,
		upgrader: websocket.Upgrader{
			ReadBufferSize:  64 * 1024,
			WriteBufferSize: 64 * 1024,
			// 联调阶段允许任意来源
			CheckOrigin: func(*http.Request) bool { return true },
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

	// 3) 双向转发
	//   client -> asr：start / end / ping / 二进制 PCM 原样发
	//   asr -> client：把 ready/partial/final/eos/error 翻译成 asr_* 后回客户端
	stop := make(chan struct{})
	var once sync.Once
	closeStop := func() { once.Do(func() { close(stop) }) }

	wg := &sync.WaitGroup{}
	wg.Add(2)

	// client -> asr
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
				// 通知 ASR 我们结束了
				_ = asrConn.WriteControl(
					websocket.CloseMessage,
					websocket.FormatCloseMessage(websocket.CloseNormalClosure, "client gone"),
					time.Now().Add(time.Second),
				)
				return
			}

			// 拦截 ping，自己回 pong，不转发给 ASR（ASR 协议没定义 ping）
			if mt == websocket.TextMessage && isPing(data) {
				_ = writeJSON(clientConn, map[string]any{"type": "pong"})
				continue
			}

			if err := asrConn.WriteMessage(mt, data); err != nil {
				log.Warn("asr write err", "err", err)
				return
			}
		}
	}()

	// asr -> client
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

			// 二进制理论上 ASR 不会回，保险起见原样转
			if mt != websocket.TextMessage {
				if err := clientConn.WriteMessage(mt, data); err != nil {
					log.Warn("client write(bin) err", "err", err)
					return
				}
				continue
			}

			out := translateASREvent(data)
			if err := clientConn.WriteMessage(websocket.TextMessage, out); err != nil {
				log.Warn("client write err", "err", err)
				return
			}
		}
	}()

	// 等任意一边结束
	<-stop
	// 主动关闭两端，让另一方的 ReadMessage 返回
	_ = clientConn.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
	_ = asrConn.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
	wg.Wait()
	log.Info("session done")
}

// translateASREvent 把 ASR 的事件 type 映射成家具协议（partial -> asr_partial, final -> asr_final）。
// 解析失败 / 不认识的事件原样透传，不破坏未来扩展。
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
