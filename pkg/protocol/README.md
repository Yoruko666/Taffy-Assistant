# pkg/protocol

Server ↔ Worker 共享的 JSON 协议结构体、事件类型常量与 control_device 取值常量。

## 为什么独立成 module

- `server` 与 `worker` 在 WS 上来回传同一组 JSON 事件（`device_info` / `device_command` / `device_command_result`）。
- 两侧需要对 `control_device.action` 与空调 `mode` 取值保持一致，集中在一处避免字面量漂移。
- 通过仓库根的 [`go.work`](../../go.work) 拼接，无需发布到任何模块代理。

## 暴露的 API 一览

```go
import "taffy.local/pkg/protocol"
```

### 事件 type 常量

```go
protocol.EventTypeDeviceInfo           // "device_info"
protocol.EventTypeDeviceCommand        // "device_command"
protocol.EventTypeDeviceCommandResult  // "device_command_result"
```

### LLM 工具函数名常量

```go
protocol.FunctionControlDevice  // "control_device"
protocol.FunctionActivateScene  // "activate_scene"
```

### `control_device` 的 action 取值

```go
protocol.ActionTurnOn         // "turn_on"
protocol.ActionTurnOff        // "turn_off"
protocol.ActionSetBrightness  // "set_brightness"
protocol.ActionSetTemperature // "set_temperature"
protocol.ActionSetMode        // "set_mode"
protocol.ActionSetPosition    // "set_position"

protocol.AllControlActions()  // []string，按上述顺序，用于 LLM tool schema 的 enum
```

### 空调模式取值

```go
protocol.ModeCool  // "cool"
protocol.ModeHeat  // "heat"
protocol.ModeAuto  // "auto"
protocol.ModeFan   // "fan"
protocol.ModeDry   // "dry"

protocol.AllAirconModes()    // []string，用于 LLM tool schema 的 enum
protocol.AirconModeLabels()  // map[string]string，"cool"→"制冷" 等中文标签
```

### 结构体

```go
protocol.DeviceContext             // 推给 worker 的单台设备快照
protocol.SceneContext              // 推给 worker 的预设场景
protocol.DeviceInfoEvent           // type=device_info 整帧
protocol.ControlDeviceParams       // control_device.arguments 解码目标
protocol.ActivateSceneParams       // activate_scene.arguments 解码目标
protocol.DeviceCommandEvent        // type=device_command 整帧
protocol.DeviceCommandResultEvent  // type=device_command_result 整帧
```

> 字段命名严格对齐 OpenAI Function Calling / 现有线上协议，禁止任意改动。
> 任意修改都会同时影响 server 与 worker，PR 审查时务必两端一起看。

## 引用示例

Server 侧构造一条 device_command 回执：

```go
ev := protocol.DeviceCommandResultEvent{
    Type:    protocol.EventTypeDeviceCommandResult,
    ToolID:  toolID,
    Success: true,
    Message: "已为您打开客厅灯",
}
```

Worker 侧的 tool schema：

```go
"action": map[string]any{
    "type":        "string",
    "description": "控制动作",
    "enum":        protocol.AllControlActions(),
},
"mode": map[string]any{
    "type":        "string",
    "description": "空调模式，仅 action=set_mode 时提供",
    "enum":        protocol.AllAirconModes(),
},
```

Server 侧把 action 翻译成中文反馈：

```go
switch p.Action {
case protocol.ActionTurnOn:
    return "已打开" + room + name
case protocol.ActionSetMode:
    if name, ok := protocol.AirconModeLabels()[string(*state.Mode)]; ok {
        return room + device + "已切换为" + name + "模式"
    }
}
```

## 修改流程

1. 改 `events.go` 的字段或新增事件类型 / action / mode。
2. **同时**修改 server / worker 引用方。
3. `cd server && go build ./... && go vet ./...`
4. `cd worker && go build ./... && go vet ./...`
