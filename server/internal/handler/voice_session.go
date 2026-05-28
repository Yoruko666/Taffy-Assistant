package handler

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync"
	"time"

	"taffy.local/pkg/protocol"

	"taffy-server/internal/service"
)

// voiceSession 收敛一条 /v1/voice 会话需要落库的中间状态：
//
//   - conversationID：StartConversation 返回的自增 id；0 表示落库未启用；
//   - lastUserMessageID：最近一条 asr_final 写入 messages 后的 id，用于把
//     后续的 device_command（assistant tool_call）挂到对应消息下；
//   - cmdByToolID：tool_id → command_id，等 device_command_result 回来时更新结果。
//
// 字段并发安全（worker→client / device_command 两条 goroutine 可能同时写）。
type voiceSession struct {
	conv   *service.ConversationService
	logger *slog.Logger

	mu                sync.Mutex
	conversationID    int64
	lastUserMessageID int64
	cmdByToolID       map[string]int64
}

// newVoiceSession 创建一个会话落库上下文。
// conv 为 nil 或未启用时，所有方法变成 no-op，调用方无需额外判空。
func newVoiceSession(conv *service.ConversationService, log *slog.Logger) *voiceSession {
	return &voiceSession{
		conv:        conv,
		logger:      log,
		cmdByToolID: make(map[string]int64),
	}
}

func (s *voiceSession) enabled() bool {
	return s != nil && s.conv != nil && s.conv.Enabled()
}

// start 在会话建立时插入一条 conversations 记录。
// userID 为 0（device 未鉴权 / DB 未初始化）时跳过。
func (s *voiceSession) start(ctx context.Context, userID int64, deviceID string) {
	if !s.enabled() || userID == 0 {
		return
	}
	id, err := s.conv.StartConversation(ctx, userID, deviceID)
	if err != nil {
		s.logger.Warn("conv start failed", "err", err)
		return
	}
	s.mu.Lock()
	s.conversationID = id
	s.mu.Unlock()
	s.logger.Info("conversation started", "conversation_id", id)
}

// end 在会话结束时标记 ended_at。
func (s *voiceSession) end() {
	s.mu.Lock()
	cid := s.conversationID
	s.mu.Unlock()
	if cid == 0 || !s.enabled() {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := s.conv.EndConversation(ctx, cid); err != nil {
		s.logger.Warn("conv end failed", "err", err)
	}
}

// recordWorkerEvent 解析 worker → client 的文本帧，按 type 异步落库：
//
//	asr_final  → messages(role=user)
//	llm_result → messages(role=assistant)
func (s *voiceSession) recordWorkerEvent(data []byte) {
	if !s.enabled() {
		return
	}
	var ev map[string]any
	if err := json.Unmarshal(data, &ev); err != nil {
		return
	}
	t, _ := ev["type"].(string)
	text, _ := ev["text"].(string)
	if text == "" {
		return
	}

	s.mu.Lock()
	cid := s.conversationID
	s.mu.Unlock()
	if cid == 0 {
		return
	}

	switch t {
	case "asr_final":
		go s.appendUser(cid, text)
	case "llm_result":
		go s.appendAssistant(cid, text)
	}
}

func (s *voiceSession) appendUser(cid int64, text string) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	id, err := s.conv.AppendUserMessage(ctx, cid, text)
	if err != nil {
		s.logger.Warn("append user msg failed", "err", err)
		return
	}
	s.mu.Lock()
	s.lastUserMessageID = id
	s.mu.Unlock()
}

func (s *voiceSession) appendAssistant(cid int64, text string) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if _, err := s.conv.AppendAssistantMessage(ctx, cid, text); err != nil {
		s.logger.Warn("append assistant msg failed", "err", err)
	}
}

// recordCommand 在 worker 下发 device_command 时插入一条 commands 记录。
// 返回 command_id 供后续 result 回填。0 表示未启用或暂无关联 user 消息。
func (s *voiceSession) recordCommand(cmd protocol.DeviceCommandEvent) int64 {
	if !s.enabled() {
		return 0
	}
	s.mu.Lock()
	msgID := s.lastUserMessageID
	s.mu.Unlock()
	if msgID == 0 {
		return 0
	}

	var p protocol.ControlDeviceParams
	_ = json.Unmarshal(cmd.Params, &p)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	id, err := s.conv.AppendCommand(ctx, msgID, p.DeviceID, p.Action, cmd.Params)
	if err != nil {
		s.logger.Warn("append command failed", "err", err)
		return 0
	}
	s.mu.Lock()
	s.cmdByToolID[cmd.ToolID] = id
	s.mu.Unlock()
	return id
}

// markCommandResult 在 device_command 执行完成后更新 commands.result。
func (s *voiceSession) markCommandResult(toolID string, success bool) {
	if !s.enabled() {
		return
	}
	s.mu.Lock()
	id := s.cmdByToolID[toolID]
	delete(s.cmdByToolID, toolID)
	s.mu.Unlock()
	if id == 0 {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := s.conv.MarkCommandResult(ctx, id, success); err != nil {
		s.logger.Warn("mark command result failed", "err", err)
	}
}
