# pkg/wsutil

Server ↔ Worker 共享的 WebSocket 胶水代码（Upgrader / Dialer / WriteJSON / IsNormalClose）。

## 为什么独立成 module

- server 的 `/v1/voice` 与 worker 的 `/v1/orchestrate` 都需要"升级 → 拨号 → 双向 pump"的样板代码；
- 抽出来后只在一处维护，server / worker 任一改动自动同步；
- 通过仓库根 [`go.work`](../../go.work) 拼接，无需发布到任何模块代理。

## 提供的 API

```go
import "taffy.local/pkg/wsutil"

upgrader := wsutil.DefaultUpgrader()        // server 端
dialer   := wsutil.DefaultDialer()          // 客户端
_ = wsutil.WriteJSON(conn, payload)
if wsutil.IsNormalClose(err) { ... }
```

依赖：`github.com/gorilla/websocket v1.5.3`（与 server / worker 已锁定的版本一致）。
