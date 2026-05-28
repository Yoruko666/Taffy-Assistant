package handler

import (
	"strconv"

	"taffy.local/pkg/protocol"
)

// BuildTools 构造发送给 LLM 的 tools 参数（OpenAI Function Calling 格式）。
//
// control_device 的所有动作参数（brightness / temperature / mode / position）平铺在顶层，
// 与 server 端 [protocol.ControlDeviceParams] 字段一一对应，请勿改成嵌套 params 子对象。
//
// activate_scene 服务端尚未实现，暂不暴露给 LLM，避免模型误触发后回复"暂未实现"。
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
							"enum":        []string{"turn_on", "turn_off", "set_brightness", "set_temperature", "set_mode", "set_position"},
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
							"enum":        []string{"cool", "heat", "auto", "fan", "dry"},
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

// 以下结构体作为 protocol 包的本地别名，保持 worker 内部命名习惯不变。
// 字段定义在 [protocol]，请勿在此处再加字段。

type (
	ControlDeviceParams      = protocol.ControlDeviceParams
	ActivateSceneParams      = protocol.ActivateSceneParams
	DeviceCommandEvent       = protocol.DeviceCommandEvent
	DeviceCommandResultEvent = protocol.DeviceCommandResultEvent
	DeviceContext            = protocol.DeviceContext
	SceneContext             = protocol.SceneContext
	DeviceInfoEvent          = protocol.DeviceInfoEvent
)

// BuildSystemPrompt 根据设备上下文动态构建 system prompt。
func BuildSystemPrompt(basePrompt string, devices []DeviceContext, scenes []SceneContext) string {
	prompt := basePrompt
	if prompt == "" {
		prompt = "你是「小菲」，一个智能家居语音助手。请用中文简短回答用户的问题。"
	}

	prompt += "\n\n## 当前用户的设备列表\n"
	if len(devices) == 0 {
		prompt += "（暂无设备）\n"
	} else {
		for _, d := range devices {
			status := "关闭"
			if d.Power {
				status = "开启"
			}
			prompt += "- " + d.DeviceID + " | " + d.Room + " " + d.Name + " (" + d.Type + ") 状态:" + status
			if d.Brightness != nil {
				prompt += " 亮度:" + strconv.Itoa(*d.Brightness) + "%"
			}
			if d.Temperature != nil {
				prompt += " 温度:" + strconv.Itoa(*d.Temperature) + "°C"
			}
			if d.Mode != "" {
				prompt += " 模式:" + d.Mode
			}
			if d.Position != nil {
				prompt += " 开合度:" + strconv.Itoa(*d.Position) + "%"
			}
			prompt += "\n"
		}
	}

	if len(scenes) > 0 {
		prompt += "\n## 用户预设场景\n"
		for _, s := range scenes {
			prompt += "- 场景ID:" + strconv.FormatInt(s.SceneID, 10) + " " + s.Name + "\n"
		}
	}

	prompt += "\n## 重要规则\n"
	prompt += "1. 当用户要求控制设备时，你必须调用 control_device 函数，不要在文本回复中描述操作。\n"
	prompt += "2. device_id 必须从上面的设备列表中选择，不能自行编造。\n"
	prompt += "3. 调用函数后，等待执行结果再回复用户。如果执行成功，简短确认；如果失败，告知用户。\n"
	prompt += "4. 如果用户的意图不是控制设备，直接文字回复即可，不需要调用任何函数。\n"
	prompt += "5. 回复要简洁自然，像一个语音助手在说话。\n"

	return prompt
}
