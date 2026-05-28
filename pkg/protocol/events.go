// Package protocol 定义 Server ↔ Worker（以及部分 Server ↔ 家具端）共享的
// JSON 协议结构体与事件类型常量。
//
// 设计原则：
//   - 仅放"双方都要解析"的纯数据结构，不放业务逻辑、不依赖任何具体框架。
//   - 字段命名严格对齐 OpenAI Function Calling / 现有线上协议，禁止任意改动。
//   - 任意修改都会同时影响 server 与 worker，PR 审查时务必两端一起看。
//
// 仓库结构：本包通过 go.work（仓库根）被 server / worker 引用，
// module path 故意取本地占位 "taffy.local/..."（不会发布到任何 proxy）。
package protocol

import "encoding/json"

// ======== 事件类型常量 ========
//
// 用于在 worker / server 处理 ws 文本帧时做 type 字段判断，
// 避免散落在各处的 "device_command" / "device_info" 字面量。

const (
	// EventTypeDeviceInfo Server → Worker：会话建立后推送当前用户设备列表。
	EventTypeDeviceInfo = "device_info"

	// EventTypeDeviceCommand Worker → Server：LLM tool call 触发的设备控制请求。
	EventTypeDeviceCommand = "device_command"

	// EventTypeDeviceCommandResult Server → Worker：上一条 device_command 的执行结果。
	EventTypeDeviceCommandResult = "device_command_result"
)

// ======== Tool 名称常量 ========

const (
	FunctionControlDevice = "control_device"
	FunctionActivateScene = "activate_scene"
)

// ======== 设备上下文 ========

// DeviceContext 是 Server 推给 Worker 的"用户当前设备快照"。
//
// 用途：worker 拿到后注入 system prompt，让 LLM 知道有哪些 device_id 可控、
// 当前开关 / 亮度 / 温度 / 模式 / 开合度，从而生成正确的 tool call。
//
// 与服务端的 DB 模型 model.Device + model.DeviceState 区别：
//   - 这是一个聚合快照，按"LLM 阅读"优化，字段命名贴近 prompt 习惯
//   - 可选字段（亮度等）用 omitempty + 指针，未赋值不会出现在 JSON 中
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
	Type    string          `json:"type"` // 必为 EventTypeDeviceInfo
	Devices []DeviceContext `json:"devices"`
	Scenes  []SceneContext  `json:"scenes,omitempty"`
}

// ======== 设备指令 ========

// ControlDeviceParams control_device 函数的参数。
//
// 字段平铺在顶层（不嵌 params 子对象），
// 与 Tool Schema 以及 server.ExecuteControlDevice 的解析结构保持一致。
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
//
// Worker → Server 方向：Worker 在 LLM 返回 tool_calls 后，
// 把 tool 的 arguments 原样塞到 Params。
type DeviceCommandEvent struct {
	Type     string          `json:"type"`     // 必为 EventTypeDeviceCommand
	ToolID   string          `json:"tool_id"`  // tool_call ID，用于关联结果
	Function string          `json:"function"` // FunctionControlDevice / FunctionActivateScene
	Params   json.RawMessage `json:"params"`   // 原始参数 JSON，由 server 自行解析为具体类型
}

// DeviceCommandResultEvent type=device_command_result 整帧。
//
// Server → Worker 方向，承载执行结果，给 LLM 二次调用作为 tool message。
type DeviceCommandResultEvent struct {
	Type    string `json:"type"`              // 必为 EventTypeDeviceCommandResult
	ToolID  string `json:"tool_id"`           // 对应的 tool_call ID
	Success bool   `json:"success"`           // 是否执行成功
	Message string `json:"message,omitempty"` // 执行结果描述（成功或失败原因）
}
