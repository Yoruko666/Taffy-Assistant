# pkg/httpx

Server / Worker 共享的 HTTP 通用工具。

## 为什么独立成 module

- server 与 worker 的 `main.go` 都需要：带默认值的环境变量读取、给 mux 套一层访问日志中间件。
- 抽到独立 module 后只在一处维护，server / worker 任一改动自动同步。
- 通过仓库根 [`go.work`](../../go.work) 拼接，无需发布到任何模块代理。

## 提供的 API

```go
import "taffy.local/pkg/httpx"
```

| API | 用途 |
|---|---|
| `httpx.Getenv(key, def string) string` | 读取环境变量，未设置或为空时返回默认值 |
| `httpx.AccessLog(next http.Handler) http.Handler` | 给所有 HTTP 请求打一行 INFO 访问日志（`method / path / remote / dur_ms`），WebSocket Upgrade 握手前也会经过 |

## 使用示例

```go
package main

import (
    "net/http"

    "taffy.local/pkg/httpx"
)

func main() {
    port := httpx.Getenv("PORT", "8080")
    mux := http.NewServeMux()
    // ... 路由注册 ...

    srv := &http.Server{
        Addr:    ":" + port,
        Handler: httpx.AccessLog(mux),
    }
    _ = srv.ListenAndServe()
}
```

## 设计取舍

- 不引入第三方日志库，使用标准库 `log/slog`，由调用方在 `main` 里 `slog.SetDefault(...)` 决定格式。
- 不做请求体大小限制 / 限流 / 链路追踪——这些是上层 / 反向代理的职责。
- 不提供 router：标准库 `net/http` 在 Go 1.22 已支持 `{var}` 路径参数，够用。

> 依赖：仅 Go 标准库（`net/http`、`log/slog`、`os`、`time`）。
