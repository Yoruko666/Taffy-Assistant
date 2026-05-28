// Package mqtt 实现 Server 与物理 / 模拟设备之间的 MQTT 桥接。
//
// 主题契约（与 furniture/mock_devices.py 一致）：
//
//	taffy/device/{id}/cmd        Server → Device   下发控制指令
//	taffy/device/{id}/status     Device → Server   设备主动汇报状态
//	taffy/device/{id}/result     Device → Server   指令执行结果
//	taffy/device/{id}/heartbeat  Device → Server   定时心跳
//	taffy/device/{id}/register   Device → Server   设备上线注册
//
// Bridge 仅负责 publish 控制指令 + subscribe 上行帧并调用回调；
// 业务逻辑由调用方在回调里实现，本包不依赖 service / repository。
// broker 未配置时 NewBridge 返回 nil，所有方法对 nil 安全。
package mqtt

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync/atomic"
	"time"

	mqttClient "github.com/eclipse/paho.mqtt.golang"

	"taffy-server/internal/config"
)

// 主题模板。
const (
	topicCmdFmt       = "taffy/device/%s/cmd"
	topicStatusFilter = "taffy/device/+/status"
	topicResultFilter = "taffy/device/+/result"
	topicHbFilter     = "taffy/device/+/heartbeat"
)

// CommandPayload Server → Device 的控制 JSON（与 mock_devices.py 解析的 action/params 对齐）。
type CommandPayload struct {
	ToolID string         `json:"tool_id,omitempty"`
	Action string         `json:"action"`
	Params map[string]any `json:"params,omitempty"`
}

// StatusPayload Device → Server 上行的状态 JSON。
type StatusPayload struct {
	DeviceID    string `json:"device_id"`
	Type        string `json:"type,omitempty"`
	Power       *bool  `json:"power,omitempty"`
	Brightness  *int   `json:"brightness,omitempty"`
	Temperature *int   `json:"temperature,omitempty"`
	Mode        string `json:"mode,omitempty"`
	Position    *int   `json:"position,omitempty"`
	UpdatedAt   int64  `json:"updated_at,omitempty"`
}

// ResultPayload Device → Server 上行的指令执行结果 JSON。
type ResultPayload struct {
	DeviceID string `json:"device_id"`
	Action   string `json:"action"`
	Result   string `json:"result"` // ok / error 等
	ToolID   string `json:"tool_id,omitempty"`
}

// Handlers 业务侧注入的回调，对应字段可单独留 nil 表示忽略该上行类型。
type Handlers struct {
	OnStatus    func(StatusPayload)
	OnResult    func(ResultPayload)
	OnHeartbeat func(deviceID string)
}

// Bridge 封装 paho mqtt 客户端，并发安全（Publish 由 paho 加锁，connected 用 atomic）。
type Bridge struct {
	cfg       *config.MQTTConfig
	client    mqttClient.Client
	connected atomic.Bool
	handlers  Handlers
}

// NewBridge 按配置连接 MQTT broker。
//
//	cfg.Broker == ""  → 返回 (nil, nil)，调用方按 disabled 处理
//	连接失败          → 返回错误
//	连接成功          → 自动订阅 status / result / heartbeat 三个 wildcard 主题
//
// 关闭时调用 Bridge.Close。
func NewBridge(cfg *config.MQTTConfig, handlers Handlers) (*Bridge, error) {
	if cfg == nil || cfg.Broker == "" {
		return nil, nil
	}
	b := &Bridge{cfg: cfg, handlers: handlers}

	opts := mqttClient.NewClientOptions().
		AddBroker(cfg.Broker).
		SetClientID(cfg.ClientID).
		SetUsername(cfg.Username).
		SetPassword(cfg.Password).
		SetAutoReconnect(true).
		SetConnectRetry(true).
		SetConnectRetryInterval(5 * time.Second).
		SetConnectTimeout(5 * time.Second).
		SetOnConnectHandler(b.onConnect).
		SetConnectionLostHandler(b.onConnectionLost)

	b.client = mqttClient.NewClient(opts)

	tok := b.client.Connect()
	if !tok.WaitTimeout(5 * time.Second) {
		return nil, fmt.Errorf("mqtt connect timeout: %s", cfg.Broker)
	}
	if err := tok.Error(); err != nil {
		return nil, fmt.Errorf("mqtt connect: %w", err)
	}
	return b, nil
}

func (b *Bridge) onConnect(_ mqttClient.Client) {
	b.connected.Store(true)
	slog.Info("mqtt connected", "broker", b.cfg.Broker)
	b.subscribeAll()
}

func (b *Bridge) onConnectionLost(_ mqttClient.Client, err error) {
	b.connected.Store(false)
	slog.Warn("mqtt connection lost", "err", err)
}

func (b *Bridge) subscribeAll() {
	subs := map[string]mqttClient.MessageHandler{
		topicStatusFilter: b.handleStatus,
		topicResultFilter: b.handleResult,
		topicHbFilter:     b.handleHeartbeat,
	}
	for topic, handler := range subs {
		tok := b.client.Subscribe(topic, b.cfg.QoS, handler)
		if tok.WaitTimeout(3 * time.Second); tok.Error() != nil {
			slog.Warn("mqtt subscribe failed", "topic", topic, "err", tok.Error())
		}
	}
}

func (b *Bridge) handleStatus(_ mqttClient.Client, msg mqttClient.Message) {
	if b.handlers.OnStatus == nil {
		return
	}
	var p StatusPayload
	if err := json.Unmarshal(msg.Payload(), &p); err != nil {
		slog.Warn("mqtt bad status", "topic", msg.Topic(), "err", err)
		return
	}
	if p.DeviceID == "" {
		// 设备只在 topic 中带 id 时的兜底
		p.DeviceID = extractDeviceID(msg.Topic())
	}
	b.handlers.OnStatus(p)
}

func (b *Bridge) handleResult(_ mqttClient.Client, msg mqttClient.Message) {
	if b.handlers.OnResult == nil {
		return
	}
	var p ResultPayload
	if err := json.Unmarshal(msg.Payload(), &p); err != nil {
		slog.Warn("mqtt bad result", "topic", msg.Topic(), "err", err)
		return
	}
	if p.DeviceID == "" {
		p.DeviceID = extractDeviceID(msg.Topic())
	}
	b.handlers.OnResult(p)
}

func (b *Bridge) handleHeartbeat(_ mqttClient.Client, msg mqttClient.Message) {
	if b.handlers.OnHeartbeat == nil {
		return
	}
	id := extractDeviceID(msg.Topic())
	if id == "" {
		return
	}
	b.handlers.OnHeartbeat(id)
}

// extractDeviceID 从 "taffy/device/{id}/{kind}" 主题中提取 device_id。
func extractDeviceID(topic string) string {
	const prefix = "taffy/device/"
	if len(topic) <= len(prefix) {
		return ""
	}
	rest := topic[len(prefix):]
	for i := 0; i < len(rest); i++ {
		if rest[i] == '/' {
			return rest[:i]
		}
	}
	return rest
}

// PublishCommand 把控制指令发布到 taffy/device/{id}/cmd。
// fire-and-forget：内部用 ctx 控制 ack 等待时间，失败仅返回错误，不重试。
func (b *Bridge) PublishCommand(ctx context.Context, deviceID string, payload CommandPayload) error {
	if b == nil || !b.connected.Load() {
		return fmt.Errorf("mqtt not connected")
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal cmd: %w", err)
	}
	topic := fmt.Sprintf(topicCmdFmt, deviceID)
	tok := b.client.Publish(topic, b.cfg.QoS, false, body)

	deadline, ok := ctx.Deadline()
	wait := 3 * time.Second
	if ok {
		if d := time.Until(deadline); d > 0 && d < wait {
			wait = d
		}
	}
	if !tok.WaitTimeout(wait) {
		return fmt.Errorf("mqtt publish timeout: %s", topic)
	}
	return tok.Error()
}

// PublishGenericCommand 是 PublishCommand 的适配器，让外部包不必依赖 CommandPayload 类型。
func (b *Bridge) PublishGenericCommand(ctx context.Context, deviceID string, toolID, action string, params map[string]any) error {
	return b.PublishCommand(ctx, deviceID, CommandPayload{
		ToolID: toolID,
		Action: action,
		Params: params,
	})
}

// Close 优雅断开连接，多次调用安全。
func (b *Bridge) Close() {
	if b == nil || b.client == nil {
		return
	}
	b.client.Disconnect(500)
	b.connected.Store(false)
}

// Status 返回健康状态："ok" / "error" / "not_configured"。
func (b *Bridge) Status() string {
	if b == nil {
		return "not_configured"
	}
	if b.connected.Load() {
		return "ok"
	}
	return "error"
}
