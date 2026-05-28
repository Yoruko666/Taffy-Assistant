// Package wsutil 提供 server / worker 共用的 WebSocket 胶水代码：
// DefaultUpgrader / DefaultDialer / WriteJSON / IsNormalClose。
// 不掺入业务逻辑。
package wsutil

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/gorilla/websocket"
)

// 默认 64KB 读写缓冲，对 16k PCM × 600ms 的单帧足够。
const defaultBufSize = 64 * 1024

// DefaultUpgrader 返回 server 端的 WebSocket Upgrader。
// 默认放行所有 Origin，仅供内网部署 / 联调；公网请额外加 Origin 白名单。
func DefaultUpgrader() websocket.Upgrader {
	return websocket.Upgrader{
		ReadBufferSize:  defaultBufSize,
		WriteBufferSize: defaultBufSize,
		CheckOrigin:     func(*http.Request) bool { return true },
	}
}

// DefaultDialer 返回客户端拨号器，握手超时 10s，缓冲 64KB。
func DefaultDialer() *websocket.Dialer {
	return &websocket.Dialer{
		HandshakeTimeout: 10 * time.Second,
		ReadBufferSize:   defaultBufSize,
		WriteBufferSize:  defaultBufSize,
	}
}

// WriteJSON 将 v 序列化为 JSON 后以 TextMessage 发出。
// 不做并发保护；上层有并发写入需求时请自行加锁。
func WriteJSON(c *websocket.Conn, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return c.WriteMessage(websocket.TextMessage, b)
}

// IsNormalClose 判断 ReadMessage 错误是否属于"对端正常关闭"：
// websocket.ErrCloseSent / CloseNormalClosure / CloseGoingAway / CloseNoStatusReceived。
func IsNormalClose(err error) bool {
	if errors.Is(err, websocket.ErrCloseSent) {
		return true
	}
	var ce *websocket.CloseError
	if errors.As(err, &ce) {
		switch ce.Code {
		case websocket.CloseNormalClosure,
			websocket.CloseGoingAway,
			websocket.CloseNoStatusReceived:
			return true
		}
	}
	return false
}
