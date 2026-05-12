# Server（Go 业务服务器）

> SHVA 业务后端，使用 Go 实现。**整个系统的中枢**，但**不再**承担大模型编排——AI 编排已从 v0.3 起拆到 [`worker/`](../worker)。
>
> **职责**：
>
> - 客户端鉴权、用户/设备管理、对话历史落库（CRUD，M3 接入 MySQL/Redis）
> - 家具端音频 WebSocket 接入与转发到 Worker
> - 通过 MQTT 下发设备控制指令、接收设备状态（M3）
> - 客户端 App 的 REST + WS 状态推送

---

## 架构总览

```
[家具端]                 [Go Server :8080]              [Go Worker :8090]            [模型/外部服务]
  麦克风 ──WS音频──▶  /v1/voice  ──WS透传──▶  /v1/orchestrate  ──WS音频──▶ ASR :9100
  扬声器 ◀──事件流──     │  ▲                       │  ▲                  ──HTTPS──▶ 云端 LLM
                         │  │ asr_partial/asr_final │  │                   ──HTTP───▶ TTS :9200
                         │  │ llm_result/llm_error  │  │
                         │  └───────────────────────┴──┘
                         │
                         ├──MQTT───▶ EMQX ──▶ 其他家具（灯/空调/窗帘）  (M3)
                         └──MySQL/Redis──▶ 用户/设备/会话历史            (M3)


[客户端 App] ──HTTPS / WSS──▶ Go Server 的 REST + 状态推送（不走音频）
```

> Server **不直接**调 ASR / LLM / TTS。所有 AI 能力都通过 Worker 间接获取。

---

## 模块清单

| 模块 ID | 名称 | 职责 |
|---|---|---|
| **M-GW** | API 网关 | 客户端 / 家具端 接入、JWT 鉴权、限流 |
| **M-AU** | 用户与认证 | 用户注册、登录、密码哈希、JWT 签发 |
| **M-DV** | 设备管理 | 设备绑定、属性、能力、在线状态、MQTT 收发 |
| **M-CH** | 会话与转发 | 接收家具端音频流 → 透传到 Worker → 把 Worker 结果回送 → 落库对话历史 |
| **M-SC** | 场景服务 | 场景定义、触发与执行 |
| **M-WK** | 大模型编排 | 已搬到 [`worker/`](../worker) |

---

## 端点全景

| 端点 | 协议 | 调用方 | 用途 |
|---|---|---|---|
| `/v1/voice` | **WS** | 家具端 | **核心**：音频上行 / 识别文本与回复下行（透传到 Worker） |
| `/api/v1/auth/*` | HTTPS | 客户端 App | 注册 / 登录 / 登出 |
| `/api/v1/devices/*` | HTTPS | 客户端 App | 设备绑定 / 列表 / 状态 |
| `/api/v1/scenes/*` | HTTPS | 客户端 App | 场景管理 |
| `/api/v1/conversations/*` | HTTPS | 客户端 App | 对话历史 |
| `/ws` | WSS | 客户端 App | 设备状态实时推送（device.status / chat.message） |
| `/v1/health` | HTTP | 运维 / 客户端 | 健康检查 |

---

## 核心：`/v1/voice` 家具端音频 WebSocket

### 协议

详见 [`furniture/README.md`](../furniture/README.md) "协议：家具端 ↔ Go Server WebSocket" 章节。

**单 WS 多轮对话**：家具端在一条连接内可以循环发 N 组 `start / [PCM...] / end`，每组触发一次 `asr_final + eos`，连接不断开，直到家具端主动关闭。

| 方向 | 帧类型 | 内容 |
|---|---|---|
| 家具→Server | text | `{"type":"start","sample_rate":16000,"format":"pcm_s16le","channels":1}` |
| 家具→Server | binary | 16-bit LE PCM mono 字节流，每 ~600ms 一包 |
| 家具→Server | text | `{"type":"end"}`（端侧 VAD 触发，每轮 1 次） |
| 家具→Server | text | `{"type":"ping"}` |
| Server→家具 | text | `{"type":"pong"}` |
| Server→家具 | text | `{"type":"asr_partial","text":"..."}` |
| Server→家具 | text | `{"type":"asr_final","text":"..."}` |
| Server→家具 | text | `{"type":"eos"}` |
| Server→家具 | text | `{"type":"llm_result","text":"..."}` |
| Server→家具 | text | `{"type":"llm_error","message":"..."}` |
| Server→家具 | text | `{"type":"error","message":"..."}` |

### 处理逻辑（拆分后）

Server 的 `/v1/voice` 不再做事件翻译、不再调 LLM，**改成纯 WS 透传 + 会话日志**：

1. 家具端连上 → Server 校验 `device_id` / `token`（M3 启用）
2. Server 拨号到 Worker `/v1/orchestrate`，把 `device_id` 通过 query 透传过去
3. 双向纯字节透传：家具端的 text/binary 原样转发给 Worker；Worker 的回包原样回家具端
4. 关键事件（`asr_final` / `llm_result` / `error`）在 Server 侧打一行 INFO 日志，便于观测
5. 任意一边断开 → 关闭另一边 → 写对话历史（M3）

事件翻译（`partial → asr_partial`）和 LLM 调用都在 Worker 内部完成，Server 不感知。

---

## 文件结构

```
server/
├── config.yaml                          # Worker 接入地址（worker.ws_url）
├── cmd/server/main.go                   # 入口：加载配置 → 路由注册 → 优雅退出
└── internal/handler/
    ├── config.go                        # AppConfig / WorkerConfig 解析（支持 $env:WORKER_WS_URL）
    ├── health.go                        # /v1/health（worker / mqtt / db 状态）
    └── voice.go                         # /v1/voice（家具 ↔ worker 双向透传 + 会话日志）
```

### 依赖

```bash
go get github.com/gorilla/websocket
go get gopkg.in/yaml.v3
```

### 配置方式

| 字段 | YAML 配置项 | 环境变量（更高优先级） |
|---|---|---|
| Worker WS 地址 | `worker.ws_url` | `WORKER_WS_URL`（默认 `ws://127.0.0.1:8090/v1/orchestrate`） |
| HTTP 端口 | — | `PORT`（默认 `8080`） |
| 配置路径 | — | `CONFIG_PATH`（默认 `config.yaml`） |

> LLM / ASR / TTS 的所有配置（API Key、URL、模型名等）已搬到 [`../worker/config.yaml`](../worker/config.yaml)。Server 不再持有 `LLM_API_KEY`。

---

## 联调：五层验证法（拆分后）

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
   $env:PORT = "8080"
   $env:WORKER_WS_URL = "ws://127.0.0.1:8090/v1/orchestrate"
   go run ./cmd/server
   curl http://127.0.0.1:8080/v1/health
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
- **Server**：`client connected` → `worker connected worker=ws://127.0.0.1:8090/...` → `event type=asr_final text=...` → `event type=llm_result len=...` → `session done`
- **Worker**：`upstream connected` → `asr connected` → `llm ok cost_ms=...` → `session done`
- **ASR Model**：`ws connected` → `segment #1 done: text='把客厅的灯打开'`

### 分层定位口诀

- 第 4 层直连 worker 通、第 5 层经 Server 挂 → 问题在 Server ↔ Worker 透传层（worker URL 错误 / worker 没起）。
- 第 4 层直连 worker 就不通 → 问题在 worker / LLM Key / ASR 模型，与 Server 无关。

---

## 设计要点

### 1. 双向纯透传，不动一个字节

家具端发什么、Server 就转什么给 Worker；Worker 回什么、Server 就回什么给家具端。
text 帧只用于打日志，**不修改**。这样：

- 协议演进时只需要改 worker，不必动 server
- 增减事件类型（`tts_audio` / `device_intent` 等）零成本
- Server 永远不会成为协议瓶颈

### 2. Server ↔ Worker 复用单 WS

每条家具会话对应**一条** server↔worker WS。worker 拿到 server 透传过来的 `device_id` / `session_id`，把日志和家具端的会话 ID 关联，便于跨进程排障。

### 3. 写家具端 WS 加锁

虽然拆分后只有"worker → 家具端"一个写者 goroutine，但仍保留 `sync.Mutex`，为后续要在 Server 侧主动推送 `device.status` 等事件留出扩展空间。

### 4. 设备鉴权

家具端 WS 不像 HTTP 那样方便加 Header，使用 query 参数：

```
ws://server:8080/v1/voice?device_id=<id>&token=<device-token>
```

`device-token` 是家具端配网阶段烧录 / 写入的长期凭证，与客户端用户的 JWT 不同。

---

## 后续工作

- [x] 与 ASR Model 的 WS 协议契约（已迁移至 worker）
- [x] 家具端 ↔ Go Server 协议契约
- [x] 联调脚本（`furniture/mock_furniture.py`）
- [x] 搭基础脚手架
- [x] 实现 `/v1/voice` 透传（M1）
- [x] 接入云端 LLM（M2，已迁移至 worker）
- [x] **拆出 worker，server 改为纯转发**（v0.3）
- [ ] 实现客户端 JWT 鉴权与登录
- [ ] 实现设备 Token 鉴权
- [ ] 接入 MQTT（推荐 `eclipse/paho.mqtt.golang`）
- [ ] 对话历史落库（MySQL + Redis）
- [ ] 健康巡检：定时打 worker `/v1/health`
