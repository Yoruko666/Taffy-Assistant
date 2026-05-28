package handler

import (
	"encoding/json"
	"log/slog"
	"net/url"
	"strings"
	"time"

	"taffy.local/pkg/protocol"
)

// 本文件汇集 /v1/voice WebSocket 透传链路上的协议级工具函数。
// 通用 WS 胶水（WriteJSON / IsNormalClose / Upgrader / Dialer）见 pkg/wsutil。

// parseAuthFlag 解析 TAFFY_VOICE_AUTH 环境变量。
// "off"/"0"/"false"/"no"/"disable"/"disabled" 关闭鉴权；其他（含未设置）开启。
func parseAuthFlag(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "off", "0", "false", "no", "disable", "disabled":
		return false
	default:
		return true
	}
}

// buildWorkerURL 在 worker WS URL 上附加 device_id / session_id 参数，
// 便于 worker 日志关联同一会话。
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

// isDeviceCommand 判断文本帧是否为 worker → server 的设备控制指令。
func isDeviceCommand(data []byte) bool {
	var ev map[string]any
	if err := json.Unmarshal(data, &ev); err != nil {
		return false
	}
	t, _ := ev["type"].(string)
	return t == protocol.EventTypeDeviceCommand
}

// logSessionEvent 把 worker → client 的关键事件打一行精简日志。
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


