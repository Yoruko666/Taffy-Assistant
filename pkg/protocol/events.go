// Package protocol 定义 Server ↔ Worker 共享的 JSON 协议结构体与事件类型常量。
//
// 设计原则：
//   - 仅放双方都要解析的纯数据结构，不放业务逻辑、不依赖任何具体框架。
//   - 字段命名严格对齐 OpenAI Function Calling / 现有线上协议，禁止任意改动。
//   - 任意修改都会同时影响 server 与 worker，PR 审查时务必两端一起看。
//
// 通过仓库根 go.work 被 server / worker 引用；module path 取本地占位
// "taffy.local/..."，不会发布到任何 module proxy。
package protocol

import "encoding/json"

// 事件 type 字段常量。

const (
	EventTypeDeviceInfo          = "device_info"           // Server → Worker：会话建立后推送当前用户设备列表
	EventTypeDeviceCommand       = "device_command"        // Worker → Server：LLM tool call 触发的设备控制请求
	EventTypeDeviceCommandResult = "device_command_result" // Server → Worker：上一条 device_command 的执行结果
)

// LLM 工具函数名常量。

const (
	FunctionControlDevice = "control_device"
	FunctionActivateScene = "activate_scene"
)

// DeviceContext 是 Server 推给 Worker 的"用户当前设备快照"。
// Worker 拿到后注入 system prompt，让 LLM 知道有哪些 device_id 可控。
// 可选字段（亮度等）使用指针 + omitempty，未赋值不会出现在 JSON。
type DeviceContext struct {
	DeviceID    string `json:"device_id"`
	Name        string `json:"name"`
	Type        string `json:"type"`
	Room        string `json:"room"`
	Power       bool   `json:"power"`
	Brightness  *int   `json:"brightness,omitempty"`
	Temperature *int   `json:"temperature,omitempty"`
	Mode        string `json:"mode,omitempty"`
	Position    *int   `json:"position,omitempty"`
}

// SceneContext Server 推给 Worker 的预设场景信息。
type SceneContext struct {
	SceneID int64  `json:"scene_id"`
	Name    string `json:"name"`
}

// DeviceInfoEvent type=device_info 整帧。
type DeviceInfoEvent struct {
	Type    string          `json:"type"`
	Devices []DeviceContext `json:"devices"`
	Scenes  []SceneContext  `json:"scenes,omitempty"`
}

// ControlDeviceParams control_device 函数的参数。
// 所有动作参数平铺在顶层（不嵌 params 子对象），与 Tool Schema 一致。
type ControlDeviceParams struct {
	DeviceID    string `json:"device_id"`
	Action      string `json:"action"`
	Brightness  *int   `json:"brightness,omitempty"`
	Temperature *int   `json:"temperature,omitempty"`
	Mode        string `json:"mode,omitempty"`
	Position    *int   `json:"position,omitempty"`
}

// ActivateSceneParams activate_scene 函数的参数。
type ActivateSceneParams struct {
	SceneID int64 `json:"scene_id"`
}

// DeviceCommandEvent type=device_command 整帧。
// Worker → Server 方向：Worker 在 LLM 返回 tool_calls 后，把 arguments 原样塞到 Params。
type DeviceCommandEvent struct {
	Type     string          `json:"type"`
	ToolID   string          `json:"tool_id"`
	Function string          `json:"function"`
	Params   json.RawMessage `json:"params"`
}

// DeviceCommandResultEvent type=device_command_result 整帧。
// Server → Worker 方向，承载执行结果，给 LLM 二次调用作为 tool message。
type DeviceCommandResultEvent struct {
	Type    string `json:"type"`
	ToolID  string `json:"tool_id"`
	Success bool   `json:"success"`
	Message string `json:"message,omitempty"`
}
