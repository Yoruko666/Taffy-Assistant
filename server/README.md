# Server（Go 业务服务器）

> Taffy 业务后端，使用 Go 实现。系统中枢，负责接入与转发，**不直接**承担大模型编排（已下沉到 [`worker/`](../worker)）。
>
> 职责：
>
> - 客户端 JWT 鉴权、用户/设备管理、对话历史落库（MySQL + Redis）
> - 家具端音频 WebSocket 接入与转发到 Worker
> - 设备状态 CRUD（开/关、温度、亮度、开合度等）
> - 拦截 Worker 的 `device_command` 事件执行设备控制（更新 DB + 转发），结果回传 Worker
> - 会话建立时推送用户设备列表与状态（`device_info`）给 Worker，供 LLM 上下文注入
> - 通过 MQTT 下发设备控制指令、接收设备状态（计划中）
> - 客户端 App 的 REST + WS 状态推送（计划中）

---

## 架构总览

```
[家具端]                 [Go Server :8080]              [Go Worker :8090]            [模型/外部服务]
  麦克风 ──WS音频──▶  /v1/voice  ──WS透传──▶  /v1/orchestrate  ──WS音频──▶ ASR :9100
  扬声器 ◀──事件流──     │  ▲                       │  ▲                  ──HTTPS──▶ 云端 LLM
                         │  │ asr_partial/asr_final │  │  (含 Tool Call)   ──HTTP───▶ TTS :9200
                         │  │ llm_result/llm_error  │  │
                         │  │ device_command ────────┼──┘
                         │  │ device_command_result ─┘
                         │  │ device_info ──────────┘
                         │
                         ├──MQTT───▶ EMQX / Mosquitto ──▶ 灯 / 空调 / 窗帘 / mock_devices.py
                         │       ◀── status / result / heartbeat
                         └──MySQL/Redis──▶ 用户 / 设备 / 对话 / 消息 / 指令 / 场景
```

> Server **不直接**调 ASR / LLM / TTS，所有 AI 能力都通过 Worker 间接获取。
> MQTT 桥接（`internal/mqtt/bridge.go`）默认禁用——`config.yaml > mqtt.broker` 为空时整个 bridge 不构造，仅 server↔worker 链路工作。

---

## 模块清单

| 模块 ID | 名称 | 职责 |
|---|---|---|
| **M-GW** | API 网关 | 客户端 / 家具端 接入、JWT 鉴权 |
| **M-AU** | 用户与认证 | 用户注册、登录、bcrypt 密码哈希、JWT 签发与黑名单（Redis） |
| **M-DV** | 设备管理 | 设备绑定、属性、状态、Token 鉴权、MQTT 收发 |
| **M-CH** | 会话与转发 | 接收家具端音频流 → 透传到 Worker → 回送 Worker 结果 → 拦截 `device_command` 执行 → 推送 `device_info` 上下文 → 落库对话历史（conversations / messages / commands） |
| **M-MQ** | MQTT 桥接 | 按 `taffy/device/{id}/{cmd,status,result,heartbeat}` 主题契约接入 broker；接收设备上行状态同步进 DB |
| **M-SC** | 场景服务 | 场景定义、触发与执行（计划中） |
| **M-WK** | 大模型编排 | 见 [`worker/`](../worker) |

---

## 端点全景

| 端点 | 协议 | 调用方 | 用途 |
|---|---|---|---|
| `/v1/voice` | **WS** | 家具端 | 核心：音频上行 / 识别文本与回复下行（透传到 Worker）/ `device_command` 拦截执行 / `device_info` 上下文推送 / 对话历史落库 |
| `/api/v1/auth/register` | HTTPS | 客户端 App | 用户注册，返回 JWT |
| `/api/v1/auth/login` | HTTPS | 客户端 App | 用户登录，返回 JWT |
| `/api/v1/devices` | HTTPS | 客户端 App | 获取当前用户所有设备（JWT 鉴权） |
| `/api/v1/devices/states` | HTTPS | 客户端 App | 获取当前用户所有设备状态（JWT 鉴权） |
| `/api/v1/devices/state` | HTTPS | 客户端 App | 更新设备状态（PUT，JWT 鉴权） |
| `/api/v1/devices/{id}/state` | HTTPS | 客户端 App | 获取单个设备状态（JWT 鉴权） |
| `/api/v1/scenes/*` | HTTPS | 客户端 App | 场景管理（计划中） |
| `/api/v1/conversations/*` | HTTPS | 客户端 App | 对话历史查询（计划中；写入已完成） |
| `/ws` | WSS | 客户端 App | 设备状态实时推送（计划中） |
| `/v1/health` | HTTP | 运维 / 客户端 | 健康检查（含 worker / db / redis / mqtt 四项状态） |

---

## 核心：`/v1/voice` 家具端音频 WebSocket

### 协议

家具端 ↔ Server 协议详见 [`furniture/README.md`](../furniture/README.md) §3.1。

> 单 WS 多轮对话：家具端在一条连接内可以循环发 N 组 `start / [PCM...] / end`，每组触发一次 `asr_final + eos`，连接不断开。

### Server ↔ Worker 内部协议

> 事件 `type` 字段值与对应 Go 结构体集中定义在 [`pkg/protocol/events.go`](../pkg/protocol/events.go)，server 与 worker 引用同一份。新增 / 重命名事件类型时，请先改 protocol 包再同时修改两端。

| 方向 | 帧类型 | 内容 |
|---|---|---|
| Server→Worker | text | `{"type":"device_info","devices":[...]}` — 会话建立后推送用户设备列表+状态 |
| Worker→Server | text | `{"type":"device_command","tool_id":"...","function":{"name":"control_device","arguments":"{...}"}}` |
| Server→Worker | text | `{"type":"device_command_result","tool_id":"...","success":true,"message":"..."}` |

### 处理逻辑

Server 的 `/v1/voice` 只做透传 + device_command 拦截执行 + 设备上下文推送 + 会话日志 + 对话历史落库：

1. 家具端连上 → Server 校验 `device_id` / `token`（可通过 `TAFFY_VOICE_AUTH=off` 跳过）
2. Server 拨号到 Worker `/v1/orchestrate`，把 `device_id` / `session_id` 通过 query 透传
3. Server 查询该用户的所有设备 + 状态，通过 `device_info` 事件推送给 Worker
4. 鉴权通过后向 `conversations` 表插入一条新会话（[`internal/handler/voice_session.go`](internal/handler/voice_session.go)）
5. 双向透传：家具端的 text/binary 原样转发给 Worker；Worker 的回包原样回家具端
6. `asr_final` → 写 `messages(role=user)`；`llm_result` → 写 `messages(role=assistant)`（异步、失败仅 warn）
7. Worker 下发 `device_command` 时，Server 拦截：
   - 在 `commands(result=pending)` 表写一行（挂在最近一条 user message 下）
   - 调用 `DeviceService.ExecuteControlDevice` 更新设备状态表
   - 若 MQTT bridge 已启用，best-effort publish 到 `taffy/device/{id}/cmd` 触发物理设备执行
   - 回复 `device_command_result` 给 Worker
   - 更新 `commands.result` 为 success / failure
8. 任意一边断开 → 关闭另一边 → 写 `conversations.ended_at`

事件翻译与 LLM 调用都在 Worker 内部完成，Server 不感知。

---

## 文件结构

```
server/
├── config.yaml                          # Worker + MySQL + Redis + JWT + MQTT 配置
├── migrations/
│   ├── 001_init.sql                     # 建库建表（7 张表）
│   └── 002_seed.sql                     # 测试种子数据
├── cmd/server/main.go                   # 入口：依赖装配（buildDeps）+ 路由装配（buildRouter）+ 优雅退出
└── internal/
    ├── config/config.go                  # AppConfig / MySQL / Redis / JWT / MQTT
    ├── model/model.go                    # 数据模型（User/Device/DeviceState/Conversation/Message/Command/Scene）
    ├── database/
    │   ├── mysql.go                      # MySQL 连接初始化
    │   └── redis.go                      # Redis 连接初始化
    ├── repository/
    │   ├── user.go                       # 用户 CRUD
    │   ├── device.go                     # 设备 CRUD（含 UpdateLastSeen）
    │   ├── device_state.go               # 设备状态 Upsert + 联表查询
    │   └── conversation.go               # 对话/消息/指令/场景 CRUD
    ├── service/
    │   ├── user.go                       # 注册/登录/密码校验（bcrypt）
    │   ├── device.go                     # 设备创建/状态更新/权限校验 + AttachMQTT
    │   ├── conversation.go               # 对话/消息/指令落库 facade（Start/Append/MarkResult/End）
    │   ├── voice_context.go              # /v1/voice 设备上下文构建（BuildDeviceContext，推送给 Worker）
    │   ├── voice_command.go              # /v1/voice device_command 执行（含 MQTT publish best-effort）
    │   └── mqtt_handlers.go              # 把 mqtt.Bridge 的上行回调翻译成 service 方法调用
    ├── mqtt/bridge.go                    # MQTT broker 接入：publish cmd + subscribe status/result/heartbeat
    ├── middleware/jwt.go                 # JWT 生成/解析/黑名单/RequireAuth
    └── handler/
        ├── health.go                     # /v1/health（worker/db/redis/mqtt 状态）
        ├── voice.go                      # /v1/voice WS 接入（鉴权 + 双向透传 + 调用 service/voice_*）
        ├── voice_session.go              # 会话级对话历史落库（asr_final / llm_result / commands）
        ├── voice_proto.go                # /v1/voice 协议工具（鉴权环境变量 / 事件分类 / device_command 检测）
        ├── auth.go                       # /api/v1/auth/register + /login（标准 error code）
        └── device.go                     # /api/v1/devices/* 设备与状态 API
```

> 协议结构体（`device_info` / `device_command` / `device_command_result` / action / mode 常量）通过仓库根 `go.work` 引用 [`pkg/protocol`](../pkg/protocol/README.md)；WebSocket 公共胶水代码（Upgrader / Dialer / WriteJSON / IsNormalClose）引用 [`pkg/wsutil`](../pkg/wsutil/README.md)；HTTP 小工具引用 `pkg/httpx`。三个 pkg 都不发布到任何 module proxy。

### 依赖

```bash
go get github.com/gorilla/websocket
go get gopkg.in/yaml.v3
go get github.com/go-sql-driver/mysql
go get github.com/redis/go-redis/v9
go get github.com/golang-jwt/jwt/v5
go get github.com/eclipse/paho.mqtt.golang
go get golang.org/x/crypto
```

> 另：本模块通过仓库根 [`go.work`](../go.work) 引用本地 module `taffy.local/pkg/{protocol,wsutil,httpx}`（`go.mod` 中以 `replace` 兜底），不发布到任何 module proxy。

### 配置方式

| 字段 | YAML 配置项 | 环境变量（更高优先级） |
|---|---|---|
| Worker WS 地址 | `worker.ws_url` | `WORKER_WS_URL`（默认 `ws://127.0.0.1:8090/v1/orchestrate`） |
| MySQL 主机 | `mysql.host` | `MYSQL_HOST`（默认 `127.0.0.1`） |
| MySQL 密码 | `mysql.password` | `MYSQL_PASSWORD` |
| Redis 地址 | `redis.addr` | `REDIS_ADDR`（默认 `127.0.0.1:6379`） |
| JWT 密钥 | `jwt.secret` | `JWT_SECRET`（默认 `change-me-in-production`，**生产必改**） |
| MQTT broker | `mqtt.broker` | `MQTT_BROKER`（留空即禁用整个 bridge） |
| MQTT 用户名 | `mqtt.username` | `MQTT_USERNAME` |
| MQTT 密码 | `mqtt.password` | `MQTT_PASSWORD` |
| HTTP 端口 | — | `PORT`（默认 `8080`） |
| 配置路径 | — | `CONFIG_PATH`（默认 `config.yaml`） |
| 语音鉴权开关 | — | `TAFFY_VOICE_AUTH=off` 临时关闭 `/v1/voice` 设备鉴权（仅本地开发） |

### REST 错误响应契约

所有 `/api/v1/*` 4xx/5xx 响应都遵循固定 JSON：

```json
{
  "error":   "<machine_code>",
  "message": "<human readable>"
}
```

- `error` 是稳定的业务错误码（见 [`internal/handler/auth.go`](internal/handler/auth.go) 顶部 `code*` 常量），客户端按 code 翻译本地化文案；
- `message` 仅做兜底显示 / 日志，**禁止用做匹配判定**。

当前已声明的 code（持续追加）：

| code | 出现位置 | HTTP |
|---|---|---|
| `invalid_request_body` | auth, device | 400 |
| `phone_or_email_required` | auth/register | 400 |
| `phone_required` | auth/login | 400 |
| `password_too_short` | auth/register | 400 |
| `phone_already_registered` | auth/register | 409 |
| `email_already_registered` | auth/register | 409 |
| `invalid_phone_or_password` | auth/login | 401 |
| `internal_error` | 所有 5xx | 500 |

### 数据库初始化

```powershell
# 首次运行：建库建表
Get-Content migrations/001_init.sql | mysql -u root -p

# 可选：插入测试数据
Get-Content migrations/002_seed.sql | mysql -u root -p
```

> LLM / ASR / TTS 配置（API Key、URL、模型名等）见 [`../worker/config.yaml`](../worker/config.yaml)，Server 不持有 `LLM_API_KEY`。

---

## 联调：五层验证法

```
第 1 层  ASR 进程是否起来              → curl http://127.0.0.1:9100/v1/health
第 2 层  ASR 整段识别是否正确           → curl -F audio=@xxx.wav http://127.0.0.1:9100/v1/asr/transcribe
第 3 层  ASR 流式 WS 是否出字           → mock_furniture.py 直连 :9100/v1/asr/stream
第 4 层  Worker↔ASR 透传 + LLM 是否通   → mock_furniture.py 直连 :8090/v1/orchestrate
第 5 层  Server↔Worker 透传是否通       → mock_furniture.py 走 :8080/v1/voice
```

### 步骤

1. 启动 ASR / TTS 模型服务（`model/start_all.ps1`）；
2. 启动 Worker：

   ```powershell
   cd worker
   $env:LLM_API_KEY = "sk-your-key"
   go run ./cmd/worker
   curl http://127.0.0.1:8090/v1/health     # 期望 worker 返回 ok
   ```

3. 启动 Server：

   ```powershell
   cd server
   # 首次运行先初始化数据库
   Get-Content migrations/001_init.sql | mysql -u root -p
   # 修改 config.yaml 中的 mysql.password
   $env:PORT = "8080"
   $env:WORKER_WS_URL = "ws://127.0.0.1:8090/v1/orchestrate"
   go run ./cmd/server
   curl http://127.0.0.1:8080/v1/health   # 期望 components.db=ok, components.redis=ok（mqtt 按配置）
   ```

4. 第 4 层（直连 worker，跳过 server，验证 AI 工作流）：

   ```powershell
   python furniture/mock_furniture.py `
     --server ws://127.0.0.1:8090/v1/orchestrate `
     --wav model\audio_output\20260509_203301\01_把客厅的灯打开.wav
   ```

5. 第 5 层（端到端，经 server）：

   ```powershell
   python furniture/mock_furniture.py `
     --server ws://127.0.0.1:8080/v1/voice `
     --device-id dev1 --token t1 `
     --wav model\audio_output\20260509_203301\01_把客厅的灯打开.wav
   ```

### 期望日志

- **家具端**：`asr_partial` × N → `asr_final: 把客厅的灯打开` → `eos` → `llm_result`
- **Server**：`client connected` → `worker connected worker=ws://127.0.0.1:8090/...` → `pushed device_info N devices` → `event type=asr_final text=...` → `device_command tool_id=... action=control_device` → `device_command_result tool_id=... success=true` → `event type=llm_result len=...` → `session done`
- **Worker**：`upstream connected` → `asr connected` → `llm ok cost_ms=...` → `session done`
- **ASR Model**：`ws connected` → `segment #1 done: text='把客厅的灯打开'`

### 分层定位口诀

- 第 4 层直连 worker 通、第 5 层经 Server 挂 → 问题在 Server ↔ Worker 透传层（worker URL 错误 / worker 没起）。
- 第 4 层直连 worker 就不通 → 问题在 worker / LLM Key / ASR 模型，与 Server 无关。

---

## 设计要点

### 1. 透传 + device_command 拦截

家具端发什么、Server 就转什么给 Worker；Worker 回什么、Server 就回什么给家具端——**除了 `device_command`**。

- `device_command` 被 Server 拦截，调用 DeviceService 执行设备控制后，回复 `device_command_result`
- 其他 text 帧只用于打日志，**不修改**
- 协议演进时只需要改 worker，不必动 server
- Server 永远不会成为协议瓶颈

### 2. Server ↔ Worker 复用单 WS

每条家具会话对应**一条** server↔worker WS。worker 拿到 server 透传过来的 `device_id` / `session_id`，把日志和家具端的会话 ID 关联，便于跨进程排障。

### 3. 写家具端 WS 加锁

虽然只有"worker → 家具端"一个写者 goroutine，但仍保留 `sync.Mutex`，为后续要在 Server 侧主动推送 `device.status` 等事件留出扩展空间。

### 4. 设备鉴权

家具端 WS 不像 HTTP 那样方便加 Header，使用 query 参数：

```
ws://server:8080/v1/voice?device_id=<id>&token=<device-token>
```

`device-token` 是家具端配网阶段烧录 / 写入的长期凭证，与客户端用户的 JWT 不同。

- 默认开启（`devices` 表中的 `token` 列必须非空且与传入完全相等，且 `status != unregistered`）；
- 本地开发期可设 `TAFFY_VOICE_AUTH=off` 跳过校验（启动会打 WARN）；
- 演示账号：`device_id=dev1, token=t1`（来自 `migrations/002_seed.sql`）。

### 5. MQTT 桥接（可选）

MQTT bridge 实现在 [`internal/mqtt/bridge.go`](internal/mqtt/bridge.go)：

- 启动时按 `config.yaml > mqtt.broker` 决定是否构造：
  - 空 → 整个 bridge 不创建，所有 publish 变成 no-op；
  - 非空 → 连接 broker，自动订阅 `taffy/device/+/{status,result,heartbeat}` 三个 wildcard 主题。
- 业务回调通过 `service.NewMQTT{Status,Result,Heartbeat}Handler` 装配，**mqtt 包不反向依赖 service**：
  - `status` → `DeviceService.SyncStateFromDevice`（合并字段到 `device_states`）
  - `heartbeat` → `DeviceService.MarkOnline`（刷新 `devices.last_seen` 并置 `status=online`）
  - `result` → 当前仅记录日志，预留与 `commands` 表 tool_id 关联
- 上游下发用 `DeviceService.publishCommandBestEffort`：在 `ExecuteControlDevice` 内 best-effort 发到 `taffy/device/{id}/cmd`，失败不阻塞 LLM 二轮调用。
- 协议契约（topic + JSON 字段）与 [`furniture/mock_devices.py`](../furniture/mock_devices.py) 完全对齐，详见 [`furniture/README.md`](../furniture/README.md) §3.2。

健康检查 `/v1/health` 的 `components.mqtt` 字段取值：

| 值 | 含义 |
|---|---|
| `not_configured` | broker 未配置或 bridge 未创建 |
| `ok` | 当前与 broker 处于已连接状态 |
| `error` | 配置了 broker 但当前未连接（含断线重连期间） |

---

## 后续工作

- [x] 设备 Token 鉴权
- [x] 接入 MQTT（`eclipse/paho.mqtt.golang`），与 mock_devices 协议对齐
- [x] 对话历史落库（conversations / messages / commands 写入路径完整）
- [x] 标准化 REST 错误码 `{error, message}`
- [ ] 对话历史查询 API（`/api/v1/conversations/*`）
- [ ] 客户端 WebSocket 状态推送 Hub（`/ws` 通道）
- [ ] 健康巡检：定时打 worker `/v1/health`，掉线自动降级
- [ ] 场景管理（`/api/v1/scenes/*` + `activate_scene` tool 落地）
- [ ] 限流 / 审计日志
