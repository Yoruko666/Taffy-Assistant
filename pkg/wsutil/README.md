# pkg/wsutil

Server ↔ Worker 共享的 WebSocket 胶水代码（Upgrader / Dialer / WriteJSON / IsNormalClose）。

## 为什么独立成 module

- server 的 `/v1/voice` 与 worker 的 `/v1/orchestrate` 都需要"升级 → 拨号 → 双向 pump"的样板代码；
- 抽出来后只在一处维护，server / worker 任一改动自动同步；
- 通过仓库根 [`go.work`](../../go.work) 拼接，无需发布到任何模块代理。

## 提供的 API

```go
import "taffy.local/pkg/wsutil"

upgrader := wsutil.DefaultUpgrader()        // 同源 + 空 Origin
policy   := wsutil.AllowOrigins("https://app.example")
upgrader = wsutil.DefaultUpgrader(policy)  // 显式 Origin 白名单

dialer := wsutil.DefaultDialer()           // 客户端
_ = wsutil.WriteJSON(conn, payload)
if wsutil.IsNormalClose(err) { ... }
```

`DefaultUpgrader` 的 policy 参数是可选的，保持原有无参数调用方可编译。未传 policy 时，`DefaultOriginPolicy` 允许同源 Origin 和空 Origin，拒绝跨站 Origin。传入 `AllowOrigins` 或自定义 `OriginPolicy` 可显式配置浏览器允许的 Origin。

空 Origin 常见于家具端等非浏览器客户端，只表示请求没有浏览器 Origin；它不能替代设备鉴权。服务端仍必须独立校验设备身份、凭据和权限，不能因为接受空 Origin 就放行未认证设备。

Origin policy 只解决浏览器跨站请求防护，不是认证或授权机制。不要用 `CheckOrigin` 代替设备鉴权。

依赖：`github.com/gorilla/websocket v1.5.3`（与 server / worker 已锁定的版本一致）。
