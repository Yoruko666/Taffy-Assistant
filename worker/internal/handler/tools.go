package handler

import (
	"taffy.local/pkg/protocol"
)

// BuildTools 构造发送给 LLM 的 tools 参数（OpenAI Function Calling 格式）。
//
// control_device 的所有动作参数（brightness / temperature / mode / position）平铺在顶层，
// 与 protocol.ControlDeviceParams 字段一一对应，请勿改成嵌套 params 子对象。
//
// activate_scene 服务端尚未实现，暂不暴露给 LLM。
func BuildTools() []map[string]any {
	return []map[string]any{
		{
			"type": "function",
			"function": map[string]any{
				"name":        protocol.FunctionControlDevice,
				"description": "控制用户的智能家居设备。当用户要求打开/关闭/调节设备时调用此函数。例如：开灯、关空调、调高温度、拉上窗帘等。",
				"parameters": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"device_id": map[string]any{
							"type":        "string",
							"description": "要控制的设备ID，必须从用户拥有的设备列表中选择",
						},
						"action": map[string]any{
							"type":        "string",
							"description": "控制动作",
							"enum":        protocol.AllControlActions(),
						},
						"brightness": map[string]any{
							"type":        "integer",
							"description": "亮度 0~100，仅 action=set_brightness 时提供",
							"minimum":     0,
							"maximum":     100,
						},
						"temperature": map[string]any{
							"type":        "integer",
							"description": "温度 16~30°C，仅 action=set_temperature 时提供",
							"minimum":     16,
							"maximum":     30,
						},
						"mode": map[string]any{
							"type":        "string",
							"description": "空调模式，仅 action=set_mode 时提供",
							"enum":        protocol.AllAirconModes(),
						},
						"position": map[string]any{
							"type":        "integer",
							"description": "窗帘开合度 0~100，0=完全关闭，100=完全打开，仅 action=set_position 时提供",
							"minimum":     0,
							"maximum":     100,
						},
					},
					"required": []string{"device_id", "action"},
				},
			},
		},
	}
}

// LLMResponse 表示 LLM Chat Completions 响应结构。
type LLMResponse struct {
	ID      string `json:"id"`
	Choices []struct {
		Index        int        `json:"index"`
		Message      LLMMessage `json:"message"`
		FinishReason string     `json:"finish_reason"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

// LLMMessage 表示 LLM 返回的消息（可能含 tool_calls）。
type LLMMessage struct {
	Role      string     `json:"role"`
	Content   string     `json:"content"`
	ToolCalls []ToolCall `json:"tool_calls,omitempty"`
}

// ToolCall 表示一次工具调用。
type ToolCall struct {
	ID       string       `json:"id"`
	Type     string       `json:"type"`
	Function FunctionCall `json:"function"`
}

// FunctionCall 表示函数调用的详情。
type FunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"` // JSON string
}

// 以下为 protocol 包的本地别名，保持 worker 内部命名一致。字段以 protocol 为准。
type (
	ControlDeviceParams      = protocol.ControlDeviceParams
	ActivateSceneParams      = protocol.ActivateSceneParams
	DeviceCommandEvent       = protocol.DeviceCommandEvent
	DeviceCommandResultEvent = protocol.DeviceCommandResultEvent
	DeviceContext            = protocol.DeviceContext
	SceneContext             = protocol.SceneContext
	DeviceInfoEvent          = protocol.DeviceInfoEvent
)

