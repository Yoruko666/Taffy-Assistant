# pkg/httpx

Server / Worker 共享的 HTTP 通用工具：环境变量读取 + 访问日志中间件。**仅依赖标准库**。

## 为什么独立成 module

- server 的 `cmd/server/main.go` 与 worker 的 `cmd/worker/main.go` 都需要读 `PORT` / `CONFIG_PATH` 等环境变量，并在最外层裹一层访问日志中间件。
- 抽出来后只在一处维护，server / worker 任一改动自动同步。
- 通过仓库根 [`go.work`](../../go.work) 拼接，无需发布到任何模块代理。

## 提供的 API

```go
import "taffy.local/pkg/httpx"

// 取环境变量，未设置或为空时回落到默认值
port := httpx.Getenv("PORT", "8080")

// 访问日志中间件：每个请求一行 INFO（method/path/remote/dur_ms）
// WebSocket Upgrade 请求也会经过这里
srv := &http.Server{
    Addr:    ":" + port,
    Handler: httpx.AccessLog(mux),
}
```

## 与其他 pkg 的关系

- [`pkg/protocol`](../protocol/README.md) — Server ↔ Worker 共享 JSON 协议结构体
- [`pkg/wsutil`](../wsutil/README.md) — Server ↔ Worker 共享 WebSocket 胶水
- `pkg/httpx`（本包） — Server / Worker 共享 HTTP 工具

三个包都仅在仓库内通过 `go.work` 引用，不发布到任何模块代理。
