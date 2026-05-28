package repository

import (
	"context"
	"database/sql"
	"fmt"

	"taffy-server/internal/model"
)

// ConversationRepo 对话会话数据访问。
type ConversationRepo struct {
	db *sql.DB
}

// NewConversationRepo 创建 ConversationRepo。
func NewConversationRepo(db *sql.DB) *ConversationRepo {
	return &ConversationRepo{db: db}
}

// Create 插入一条对话会话，返回自增 conversation_id。
func (r *ConversationRepo) Create(ctx context.Context, c *model.Conversation) (int64, error) {
	result, err := r.db.ExecContext(ctx,
		`INSERT INTO conversations (user_id, device_id) VALUES (?, ?)`,
		c.UserID, c.DeviceID,
	)
	if err != nil {
		return 0, fmt.Errorf("insert conversation: %w", err)
	}
	id, _ := result.LastInsertId()
	return id, nil
}

// EndByID 结束会话（设置 ended_at）。
func (r *ConversationRepo) EndByID(ctx context.Context, id int64) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE conversations SET ended_at = NOW() WHERE conversation_id = ?`, id,
	)
	if err != nil {
		return fmt.Errorf("end conversation: %w", err)
	}
	return nil
}

// MessageRepo 消息数据访问。
type MessageRepo struct {
	db *sql.DB
}

// NewMessageRepo 创建 MessageRepo。
func NewMessageRepo(db *sql.DB) *MessageRepo {
	return &MessageRepo{db: db}
}

// Create 插入一条消息，返回自增 message_id。
func (r *MessageRepo) Create(ctx context.Context, m *model.Message) (int64, error) {
	result, err := r.db.ExecContext(ctx,
		`INSERT INTO messages (conversation_id, role, content, audio_url) VALUES (?, ?, ?, ?)`,
		m.ConversationID, m.Role, m.Content, m.AudioURL,
	)
	if err != nil {
		return 0, fmt.Errorf("insert message: %w", err)
	}
	id, _ := result.LastInsertId()
	return id, nil
}

// ListByConversation 查询某会话的所有消息。
func (r *MessageRepo) ListByConversation(ctx context.Context, convID int64) ([]*model.Message, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT message_id, conversation_id, role, content, audio_url, created_at
		 FROM messages WHERE conversation_id = ? ORDER BY created_at`, convID,
	)
	if err != nil {
		return nil, fmt.Errorf("list messages: %w", err)
	}
	defer rows.Close()

	var msgs []*model.Message
	for rows.Next() {
		m := &model.Message{}
		if err := rows.Scan(&m.MessageID, &m.ConversationID, &m.Role, &m.Content, &m.AudioURL, &m.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan message: %w", err)
		}
		msgs = append(msgs, m)
	}
	return msgs, rows.Err()
}

// CommandRepo 指令数据访问。
type CommandRepo struct {
	db *sql.DB
}

// NewCommandRepo 创建 CommandRepo。
func NewCommandRepo(db *sql.DB) *CommandRepo {
	return &CommandRepo{db: db}
}

// Create 插入一条指令记录。
func (r *CommandRepo) Create(ctx context.Context, c *model.Command) (int64, error) {
	result, err := r.db.ExecContext(ctx,
		`INSERT INTO commands (message_id, device_id, action, params, result) VALUES (?, ?, ?, ?, ?)`,
		c.MessageID, c.DeviceID, c.Action, c.Params, c.Result,
	)
	if err != nil {
		return 0, fmt.Errorf("insert command: %w", err)
	}
	id, _ := result.LastInsertId()
	return id, nil
}

// UpdateResult 更新指令执行结果。
func (r *CommandRepo) UpdateResult(ctx context.Context, id int64, result model.CommandResult) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE commands SET result = ?, executed_at = NOW() WHERE command_id = ?`,
		result, id,
	)
	if err != nil {
		return fmt.Errorf("update command result: %w", err)
	}
	return nil
}

// SceneRepo 场景数据访问。
type SceneRepo struct {
	db *sql.DB
}

// NewSceneRepo 创建 SceneRepo。
func NewSceneRepo(db *sql.DB) *SceneRepo {
	return &SceneRepo{db: db}
}

// Create 插入一个场景。
func (r *SceneRepo) Create(ctx context.Context, s *model.Scene) (int64, error) {
	result, err := r.db.ExecContext(ctx,
		`INSERT INTO scenes (user_id, name, command_list) VALUES (?, ?, ?)`,
		s.UserID, s.Name, s.CommandList,
	)
	if err != nil {
		return 0, fmt.Errorf("insert scene: %w", err)
	}
	id, _ := result.LastInsertId()
	return id, nil
}

// GetByID 按 ID 查询场景。
func (r *SceneRepo) GetByID(ctx context.Context, id int64) (*model.Scene, error) {
	s := &model.Scene{}
	err := r.db.QueryRowContext(ctx,
		`SELECT scene_id, user_id, name, command_list, created_at, updated_at
		 FROM scenes WHERE scene_id = ?`, id,
	).Scan(&s.SceneID, &s.UserID, &s.Name, &s.CommandList, &s.CreatedAt, &s.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("get scene: %w", err)
	}
	return s, nil
}

// ListByUser 查询某用户的所有场景。
func (r *SceneRepo) ListByUser(ctx context.Context, userID int64) ([]*model.Scene, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT scene_id, user_id, name, command_list, created_at, updated_at
		 FROM scenes WHERE user_id = ? ORDER BY created_at`, userID,
	)
	if err != nil {
		return nil, fmt.Errorf("list scenes: %w", err)
	}
	defer rows.Close()

	var scenes []*model.Scene
	for rows.Next() {
		s := &model.Scene{}
		if err := rows.Scan(&s.SceneID, &s.UserID, &s.Name, &s.CommandList, &s.CreatedAt, &s.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan scene: %w", err)
		}
		scenes = append(scenes, s)
	}
	return scenes, rows.Err()
}

// Delete 删除场景。
func (r *SceneRepo) Delete(ctx context.Context, id int64) error {
	_, err := r.db.ExecContext(ctx,
		`DELETE FROM scenes WHERE scene_id = ?`, id,
	)
	if err != nil {
		return fmt.Errorf("delete scene: %w", err)
	}
	return nil
}
