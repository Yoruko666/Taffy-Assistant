# pkg/protocol

Server ↔ Worker 共享的 JSON 协议结构体与事件类型常量。

## 为什么独立成 module

- `server` 与 `worker` 在 WS 上来回传同一组 JSON 事件（`device_info` / `device_command` / `device_command_result`）。
- 早期两侧各自定义结构体，字段曾发生过漂移；抽到独立 module 后**只有一处定义**，编译期保证一致。
- 通过仓库根的 [`go.work`](../../go.work) 拼接，无需发布到任何模块代理。

## 修改流程

1. 改 `events.go` 的字段或新增事件类型。
2. **同时**修改 server / worker 引用方。
3. `cd server && go build ./... && go vet ./...`
4. `cd worker && go build ./... && go vet ./...`

## 引用方式

```go
import "taffy.local/pkg/protocol"

ev := protocol.DeviceCommandEvent{
    Type:     protocol.EventTypeDeviceCommand,
    ToolID:   id,
    Function: protocol.FunctionControlDevice,
    Params:   raw,
}
```
