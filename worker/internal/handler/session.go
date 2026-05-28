package handler

import (
	"encoding/json"
	"log/slog"
	"sync"

	"github.com/gorilla/websocket"

	"taffy.local/pkg/wsutil"
)

// Session 一条 /v1/orchestrate WS 会话的可变状态容器。
// 写上游必须经过 WriteJSON / WriteRaw（内部加锁），保证多 goroutine 并发安全。
type Session struct {
	upConn *websocket.Conn

	// writeMu 保护 upConn 的写：ASR 回传 / LLM 异步 / TTS 异步 共用。
	writeMu sync.Mutex

	// 会话级设备上下文，由 server 通过 device_info 推送，LLM 注入 system prompt。
	devCtxMu sync.Mutex
	devices  []DeviceContext
	scenes   []SceneContext

	// 会话级 tool_call 缓存：tool_id → 首轮下发的 name/arguments，
	// 二轮 LLM 调用时还原 history，避免模型幻觉。
	toolCallMu sync.Mutex
	toolCalls  map[string]pendingToolCall

	log *slog.Logger
}

// NewSession 构造一个会话上下文。
func NewSession(upConn *websocket.Conn, log *slog.Logger) *Session {
	return &Session{
		upConn:    upConn,
		toolCalls: make(map[string]pendingToolCall),
		log:       log,
	}
}

func (s *Session) Log() *slog.Logger { return s.log }

// WriteJSON 串行化 v 并以 TextMessage 发到上游，并发安全。
func (s *Session) WriteJSON(v any) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return wsutil.WriteJSON(s.upConn, v)
}

// WriteRaw 透传一帧（含 binary）到上游，并发安全。
func (s *Session) WriteRaw(messageType int, data []byte) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return s.upConn.WriteMessage(messageType, data)
}

// SetDeviceContext 接收 server 推送的 device_info 整帧，缓存设备 / 场景。
func (s *Session) SetDeviceContext(raw json.RawMessage) {
	var info DeviceInfoEvent
	if err := json.Unmarshal(raw, &info); err != nil {
		s.log.Warn("device_info parse failed", "err", err)
		return
	}
	s.devCtxMu.Lock()
	s.devices = info.Devices
	s.scenes = info.Scenes
	s.devCtxMu.Unlock()
	s.log.Info("device context received", "devices", len(info.Devices), "scenes", len(info.Scenes))
}

// SnapshotDeviceContext 拷贝当前设备 / 场景列表，避免长持锁。
func (s *Session) SnapshotDeviceContext() ([]DeviceContext, []SceneContext) {
	s.devCtxMu.Lock()
	defer s.devCtxMu.Unlock()
	devices := make([]DeviceContext, len(s.devices))
	copy(devices, s.devices)
	scenes := make([]SceneContext, len(s.scenes))
	copy(scenes, s.scenes)
	return devices, scenes
}

// RememberToolCall 记录一次刚下发给 server 的 tool_call。
func (s *Session) RememberToolCall(toolID, name, arguments string) {
	s.toolCallMu.Lock()
	defer s.toolCallMu.Unlock()
	s.toolCalls[toolID] = pendingToolCall{Name: name, Arguments: arguments}
}

// PopToolCall 取出并删除指定 tool_id 的 pending 缓存。
// ok=false 表示无缓存（脏数据 / 重复回执）。
func (s *Session) PopToolCall(toolID string) (pendingToolCall, bool) {
	s.toolCallMu.Lock()
	defer s.toolCallMu.Unlock()
	p, ok := s.toolCalls[toolID]
	if ok {
		delete(s.toolCalls, toolID)
	}
	return p, ok
}

// pendingToolCall 已下发但还在等 result 的 tool call，
// 二轮 LLM 调用时回填真实的 function.name 与 arguments。
type pendingToolCall struct {
	Name      string
	Arguments string // 原始 JSON 字符串
}
