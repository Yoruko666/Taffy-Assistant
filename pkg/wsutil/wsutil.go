// Package wsutil 提供 server / worker 共用的 WebSocket 胶水代码：
// DefaultUpgrader / DefaultDialer / WriteJSON / IsNormalClose。
// 不掺入业务逻辑。
package wsutil

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"time"

	"github.com/gorilla/websocket"
)

// 默认 64KB 读写缓冲，对 16k PCM × 600ms 的单帧足够。
const defaultBufSize = 64 * 1024

// OriginPolicy 决定 WebSocket 握手是否接受请求的 Origin。
//
// Origin 只用于浏览器跨站请求防护，不是设备身份认证。特别是家具端等
// 非浏览器客户端通常不发送 Origin；接受空 Origin 不能替代设备鉴权。
type OriginPolicy func(*http.Request) bool

// DefaultOriginPolicy 接受没有 Origin 的非浏览器请求，以及与请求 Host
// 相同的 Origin；拒绝跨站 Origin。
//
// 调用方如使用反向代理或需要跨域浏览器访问，应传入自己的 OriginPolicy，
// 而不是把 CheckOrigin 设为无条件放行。
func DefaultOriginPolicy(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}

	originURL, err := url.Parse(origin)
	if err != nil || originURL.Scheme == "" || originURL.Host == "" {
		return false
	}
	if originURL.Host != r.Host {
		return false
	}
	return r.URL.Scheme == "" || originURL.Scheme == r.URL.Scheme
}

// AllowOrigins returns an OriginPolicy for an explicit allowlist. Empty Origin
// is accepted for non-browser clients; callers must still enforce device
// authentication independently. Origins are matched exactly as serialized
// (for example, "https://app.example.com" and "https://app.example.com:443"
// are distinct entries).
func AllowOrigins(origins ...string) OriginPolicy {
	allowed := make(map[string]struct{}, len(origins))
	for _, origin := range origins {
		allowed[origin] = struct{}{}
	}
	return func(r *http.Request) bool {
		origin := r.Header.Get("Origin")
		if origin == "" {
			return true
		}
		_, ok := allowed[origin]
		return ok
	}
}

// DefaultUpgrader 返回 server 端的 WebSocket Upgrader。
// 可选 policy 用于配置允许的 Origin；未提供时仅允许同源或空 Origin，
// 不再默认放行所有跨站请求。空 Origin 仅表示客户端非浏览器，不表示已
// 通过家具/设备鉴权。
//
// 参数保持为可选以兼容现有调用方；公网或跨域部署应显式传入 AllowOrigins
// 返回的策略。
func DefaultUpgrader(policy ...OriginPolicy) websocket.Upgrader {
	checkOrigin := OriginPolicy(DefaultOriginPolicy)
	if len(policy) > 0 && policy[0] != nil {
		checkOrigin = policy[0]
	}
	return websocket.Upgrader{
		ReadBufferSize:  defaultBufSize,
		WriteBufferSize: defaultBufSize,
		CheckOrigin:     checkOrigin,
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
