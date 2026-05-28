package handler

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/url"
	"strings"
	"time"

	"github.com/gorilla/websocket"

	"taffy.local/pkg/protocol"
)

// 本文件汇集 /v1/voice WebSocket 透传链路上的协议级工具函数：
// 仅做编码 / 解码 / 控制流判定，不依赖具体业务。

// parseAuthFlag 解析 TAFFY_VOICE_AUTH 环境变量。
// 显式 "off"/"0"/"false"/"no" 关闭鉴权；其他（含未设置）开启。
func parseAuthFlag(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "off", "0", "false", "no", "disable", "disabled":
		return false
	default:
		return true
	}
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

// isDeviceCommand 判断 worker → server 方向的某条文本帧是否为
// "请 server 执行设备控制"指令。
func isDeviceCommand(data []byte) bool {
	var ev map[string]any
	if err := json.Unmarshal(data, &ev); err != nil {
		return false
	}
	t, _ := ev["type"].(string)
	return t == protocol.EventTypeDeviceCommand
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

// writeJSON 将 v 序列化为 JSON 后以 TextMessage 发送。
func writeJSON(c *websocket.Conn, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return c.WriteMessage(websocket.TextMessage, b)
}

// isNormalClose 判断 ReadMessage 返回的错误是否属于"对端正常关闭"。
func isNormalClose(err error) bool {
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
