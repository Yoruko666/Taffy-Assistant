package handler

import (
	"log/slog"
	"net/http"

	"taffy.local/pkg/wsutil"

	"taffy-server/internal/hub"
	"taffy-server/internal/middleware"
)

// RealtimeHandler 客户端实时推送 WS 端点（UC-10）。
//
// 鉴权方式：query string `?token=<jwt>`——浏览器 / OkHttp WebSocket 不能带自定义头部，
// 所以走 query 而不是 Authorization 头。token 校验仍走 [middleware.JWTMiddleware]，
// 与 REST API 共用同一套黑名单 / 过期判定。
//
// 协议：
//   - 客户端无需上行任何业务帧（hub 只负责读心跳 / 监测断连）；
//   - 服务端下行事件由业务侧通过 [hub.Publisher.BroadcastToUser] 触发，
//     当前实现仅有 `device_state_changed`（语音 / 客户端控制后均推送）。
type RealtimeHandler struct {
	jwtMW *middleware.JWTMiddleware
	hub   *hub.Hub
}

// NewRealtimeHandler 构造 RealtimeHandler。
func NewRealtimeHandler(jwtMW *middleware.JWTMiddleware, h *hub.Hub) *RealtimeHandler {
	return &RealtimeHandler{jwtMW: jwtMW, hub: h}
}

func (h *RealtimeHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	log := slog.With("path", "/ws", "remote", r.RemoteAddr)

	token := r.URL.Query().Get("token")
	if token == "" {
		log.Warn("realtime auth rejected: missing token")
		http.Error(w, "missing token", http.StatusUnauthorized)
		return
	}
	claims, err := h.jwtMW.ParseToken(token)
	if err != nil {
		log.Warn("realtime auth rejected", "err", err)
		http.Error(w, "invalid or expired token", http.StatusUnauthorized)
		return
	}

	upgrader := wsutil.DefaultUpgrader()
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Error("upgrade failed", "err", err)
		return
	}

	c := h.hub.Register(claims.UserID, conn)
	c.Run()
}
