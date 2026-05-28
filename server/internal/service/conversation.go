package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"taffy-server/internal/model"
	"taffy-server/internal/repository"
)

// ConversationService 把 conversations / messages / commands 三张表的写入路径串到一起。
// 所有写入使用独立 3s 超时 context；落库失败由调用方以 warn 处理，不阻塞语音回复链路。
type ConversationService struct {
	convRepo *repository.ConversationRepo
	msgRepo  *repository.MessageRepo
	cmdRepo  *repository.CommandRepo
}

// NewConversationService 创建 ConversationService。任一 repo 为 nil 视为该层未启用。
func NewConversationService(
	convRepo *repository.ConversationRepo,
	msgRepo *repository.MessageRepo,
	cmdRepo *repository.CommandRepo,
) *ConversationService {
	return &ConversationService{convRepo: convRepo, msgRepo: msgRepo, cmdRepo: cmdRepo}
}

// ErrConversationDisabled 表示对话历史落库未启用（DB 未初始化）。
var ErrConversationDisabled = errors.New("conversation persistence disabled")

func (s *ConversationService) Enabled() bool {
	return s != nil && s.convRepo != nil && s.msgRepo != nil && s.cmdRepo != nil
}

// StartConversation 写入一条 conversations 记录并返回 ID。
func (s *ConversationService) StartConversation(ctx context.Context, userID int64, deviceID string) (int64, error) {
	if !s.Enabled() {
		return 0, ErrConversationDisabled
	}
	cctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	c := &model.Conversation{UserID: userID}
	if deviceID != "" {
		c.DeviceID = &deviceID
	}
	id, err := s.convRepo.Create(cctx, c)
	if err != nil {
		return 0, fmt.Errorf("start conversation: %w", err)
	}
	return id, nil
}

// EndConversation 标记会话结束（写入 ended_at）。
func (s *ConversationService) EndConversation(ctx context.Context, conversationID int64) error {
	if !s.Enabled() || conversationID == 0 {
		return nil
	}
	cctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	return s.convRepo.EndByID(cctx, conversationID)
}

// AppendUserMessage 写入一条 user 角色消息，返回 message_id。
func (s *ConversationService) AppendUserMessage(ctx context.Context, conversationID int64, content string) (int64, error) {
	return s.appendMessage(ctx, conversationID, model.RoleUser, content)
}

// AppendAssistantMessage 写入一条 assistant 角色消息，返回 message_id。
func (s *ConversationService) AppendAssistantMessage(ctx context.Context, conversationID int64, content string) (int64, error) {
	return s.appendMessage(ctx, conversationID, model.RoleAssistant, content)
}

func (s *ConversationService) appendMessage(ctx context.Context, conversationID int64, role model.MessageRole, content string) (int64, error) {
	if !s.Enabled() || conversationID == 0 {
		return 0, ErrConversationDisabled
	}
	cctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	return s.msgRepo.Create(cctx, &model.Message{
		ConversationID: conversationID,
		Role:           role,
		Content:        content,
	})
}

// AppendCommand 在某条 assistant 消息下记录一条设备控制指令。
// rawParams 是原始 JSON（可能为空）。
func (s *ConversationService) AppendCommand(ctx context.Context, messageID int64, deviceID, action string, rawParams []byte) (int64, error) {
	if !s.Enabled() || messageID == 0 {
		return 0, ErrConversationDisabled
	}
	cctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	cmd := &model.Command{
		MessageID: messageID,
		DeviceID:  deviceID,
		Action:    action,
		Result:    model.ResultPending,
	}
	if len(rawParams) > 0 {
		j := model.JSON(rawParams)
		cmd.Params = &j
	}
	return s.cmdRepo.Create(cctx, cmd)
}

// MarkCommandResult 把指令置为 success / failure。
func (s *ConversationService) MarkCommandResult(ctx context.Context, commandID int64, success bool) error {
	if !s.Enabled() || commandID == 0 {
		return nil
	}
	cctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	res := model.ResultSuccess
	if !success {
		res = model.ResultFailure
	}
	return s.cmdRepo.UpdateResult(cctx, commandID, res)
}
