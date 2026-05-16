// Package model 定义 SHVA 系统的数据模型，与数据库表一一对应。
package model

import "time"

// ──────────────────────────── 用户 ────────────────────────────

// User 对应 users 表。
type User struct {
	UserID       int64  `db:"user_id"       json:"user_id"`
	Phone        string `db:"phone"         json:"phone,omitempty"`
	Email        string `db:"email"         json:"email,omitempty"`
	PasswordHash string `db:"password_hash" json:"-"`
	Nickname     string `db:"nickname"      json:"nickname"`
	AvatarURL    string `db:"avatar_url"    json:"avatar_url,omitempty"`
	CreatedAt    time.Time `db:"created_at"  json:"created_at"`
	UpdatedAt    time.Time `db:"updated_at"  json:"updated_at"`
}

// ──────────────────────────── 设备 ────────────────────────────

// DeviceType 设备类型枚举。
type DeviceType string

const (
	DeviceSpeaker DeviceType = "SPEAKER"
	DeviceLight   DeviceType = "LIGHT"
	DeviceAircon  DeviceType = "AIRCON"
	DeviceCurtain DeviceType = "CURTAIN"
	DeviceSocket  DeviceType = "SOCKET"
)

// DeviceStatus 设备状态枚举。
type DeviceStatus string

const (
	StatusUnregistered DeviceStatus = "unregistered"
	StatusWaitingBind  DeviceStatus = "waiting_bind"
	StatusOnline       DeviceStatus = "online"
	StatusOffline      DeviceStatus = "offline"
	StatusError        DeviceStatus = "error"
)

// Device 对应 devices 表。
type Device struct {
	DeviceID  string       `db:"device_id"  json:"device_id"`
	OwnerID   int64        `db:"owner_id"   json:"owner_id"`
	Type      DeviceType   `db:"type"       json:"type"`
	Name      string       `db:"name"       json:"name"`
	Room      string       `db:"room"       json:"room"`
	Status    DeviceStatus `db:"status"     json:"status"`
	Token     string       `db:"token"      json:"-"`
	LastSeen  *time.Time   `db:"last_seen"  json:"last_seen,omitempty"`
	CreatedAt time.Time    `db:"created_at" json:"created_at"`
	UpdatedAt time.Time    `db:"updated_at" json:"updated_at"`
}

// ──────────────────────────── 设备状态 ────────────────────────────

// AirconMode 空调模式枚举。
type AirconMode string

const (
	ModeCool AirconMode = "cool"
	ModeHeat AirconMode = "heat"
	ModeAuto AirconMode = "auto"
	ModeFan  AirconMode = "fan"
	ModeDry  AirconMode = "dry"
)

// DeviceState 对应 device_states 表，保存各家具的使用情况。
type DeviceState struct {
	DeviceID    string     `db:"device_id"   json:"device_id"`
	Power       bool       `db:"power"       json:"power"`
	Brightness  *int       `db:"brightness"  json:"brightness,omitempty"`
	Temperature *int       `db:"temperature" json:"temperature,omitempty"`
	Mode        *AirconMode `db:"mode"       json:"mode,omitempty"`
	Position    *int       `db:"position"    json:"position,omitempty"`
	UpdatedAt   time.Time  `db:"updated_at"  json:"updated_at"`
}

// ──────────────────────────── 对话 ────────────────────────────

// Conversation 对应 conversations 表。
type Conversation struct {
	ConversationID int64      `db:"conversation_id" json:"conversation_id"`
	UserID         int64      `db:"user_id"         json:"user_id"`
	DeviceID       *string    `db:"device_id"       json:"device_id,omitempty"`
	StartedAt      time.Time  `db:"started_at"      json:"started_at"`
	EndedAt        *time.Time `db:"ended_at"        json:"ended_at,omitempty"`
}

// MessageRole 消息角色枚举。
type MessageRole string

const (
	RoleUser      MessageRole = "user"
	RoleAssistant MessageRole = "assistant"
	RoleSystem    MessageRole = "system"
)

// Message 对应 messages 表。
type Message struct {
	MessageID      int64       `db:"message_id"      json:"message_id"`
	ConversationID int64       `db:"conversation_id" json:"conversation_id"`
	Role           MessageRole `db:"role"            json:"role"`
	Content        string      `db:"content"         json:"content"`
	AudioURL       string      `db:"audio_url"       json:"audio_url,omitempty"`
	CreatedAt      time.Time   `db:"created_at"      json:"created_at"`
}

// ──────────────────────────── 指令 ────────────────────────────

// CommandResult 指令执行结果枚举。
type CommandResult string

const (
	ResultPending CommandResult = "pending"
	ResultSuccess CommandResult = "success"
	ResultFailure CommandResult = "failure"
	ResultTimeout CommandResult = "timeout"
)

// Command 对应 commands 表。
type Command struct {
	CommandID  int64         `db:"command_id"  json:"command_id"`
	MessageID  int64         `db:"message_id"  json:"message_id"`
	DeviceID   string        `db:"device_id"   json:"device_id"`
	Action     string        `db:"action"      json:"action"`
	Params     *JSON         `db:"params"      json:"params,omitempty"`
	Result     CommandResult `db:"result"      json:"result"`
	CreatedAt  time.Time     `db:"created_at"  json:"created_at"`
	ExecutedAt *time.Time    `db:"executed_at" json:"executed_at,omitempty"`
}

// JSON 是 []byte 的别名，方便自定义数据库扫描/值写入。
type JSON []byte

// ──────────────────────────── 场景 ────────────────────────────

// Scene 对应 scenes 表。
type Scene struct {
	SceneID     int64     `db:"scene_id"     json:"scene_id"`
	UserID      int64     `db:"user_id"      json:"user_id"`
	Name        string    `db:"name"         json:"name"`
	CommandList JSON      `db:"command_list" json:"command_list"`
	CreatedAt   time.Time `db:"created_at"   json:"created_at"`
	UpdatedAt   time.Time `db:"updated_at"   json:"updated_at"`
}
