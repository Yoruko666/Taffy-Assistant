# Worker（Go 大模型编排进程）

> Taffy 系统的"AI 编排层"：独占 ASR / LLM / TTS 模型能力的调度，对外通过 WS 暴露 `/v1/orchestrate`。
> 支持 OpenAI Function Calling（Tool Call），LLM 可按需调用 `control_device` / `activate_scene` 等工具函数。
>
> 职责：
>
> - 接受 Server 的 WS 连接（`/v1/orchestrate`）
> - 把上游送来的语音流（`start / [PCM] / end`）转发到本地 ASR
> - ASR 的 `partial / final` 翻译为 `asr_partial / asr_final` 回送
> - `asr_final` 触发云端 LLM 调用（携带设备上下文 + Tool Schema）
> - LLM 返回 `tool_calls` 时封装为 `device_command` 下发给 Server 执行
> - 收到 Server 的 `device_command_result` 后将结果回传 LLM 进行二次调用，生成最终自然语言回复
> - 最终回复以 `llm_result` 回送，并异步合成 `tts_audio`（wav）下发家具端播放
>
> 不负责：家具端 / 客户端的接入与鉴权，对话历史落库，MQTT 设备控制，数据库操作（这些在 [`../server`](../server)）。

---

## 架构定位

```
家具端 ──WS──▶ Server :8080 ──WS──▶ Worker :8090 ──WS──▶ ASR Model :9100
   ▲                  │                    │
   │                  │                    ├──HTTPS──▶ 云端 LLM API (Tool Call)
   └──── llm_result ──┴────── 透传 ────────┘
                                            └──HTTP───▶ TTS Model :9200

设备控制消息流：
Server ──device_info──▶ Worker            （会话开始时推送设备上下文）
Worker ──device_command──▶ Server         （LLM tool_calls → 设备控制指令）
Server ──device_command_result──▶ Worker  （控制结果回传 → 二次 LLM 调用）
```

- Server ↔ Worker 协议与"家具端 ↔ Server"基本相同，Server 只做透传 + 鉴权 + `device_command` 拦截 + 落库。
- Worker 是无状态的，可以水平扩多份（会话级状态都在 [`internal/handler/session.go`](internal/handler/session.go) 的 `Session` 对象里，随 WS 生命周期消亡）。

---

## 端点

| 端点 | 协议 | 调用方 | 用途 |
|---|---|---|---|
| `/v1/orchestrate` | **WS** | Server | 核心：语音流入 + 大模型结果流出 |
| `/v1/health` | HTTP | 运维 / Server | 健康检查（含 ASR / LLM / TTS 配置状态） |

### `/v1/orchestrate` 协议

> 事件 `type` 字段值与对应 Go 结构体集中定义在 [`pkg/protocol/events.go`](../pkg/protocol/events.go)。Worker 通过 `internal/handler/tools.go` 的 type alias 引用，**字段以 protocol 包为准**。

| 方向 | 帧类型 | 内容 |
|---|---|---|
| Server→Worker | text | `{"type":"start","sample_rate":16000,"format":"pcm_s16le","channels":1}` |
| Server→Worker | binary | 16-bit LE PCM mono 字节流 |
| Server→Worker | text | `{"type":"end"}` |
| Server→Worker | text | `{"type":"ping"}`（保活） |
| Server→Worker | text | `{"type":"device_info","devices":[...]}` — 会话建立后推送用户设备列表 + 状态 |
| Server→Worker | text | `{"type":"device_command_result","tool_id":"...","success":true,"message":"..."}` — Server 执行设备控制后回传结果 |
| Worker→Server | text | `{"type":"pong"}` |
| Worker→Server | text | `{"type":"asr_partial","text":"..."}` |
| Worker→Server | text | `{"type":"asr_final","text":"..."}` |
| Worker→Server | text | `{"type":"eos"}` |
| Worker→Server | text | `{"type":"llm_result","text":"..."}` |
| Worker→Server | text | `{"type":"llm_error","message":"..."}` |
| Worker→Server | text | `{"type":"tts_audio","format":"wav","data":"<base64>"}`（异步） |
| Worker→Server | text | `{"type":"error","message":"..."}` |
| Worker→Server | text | `{"type":"device_command","tool_id":"...","function":"control_device","params":{...}}` — LLM 返回 tool_calls 时下发 |

Server 在拨号 worker 时会带上 query 参数 `device_id` / `session_id`，便于 worker 日志与上游会话关联。

---

## 文件结构

```
worker/
├── config.yaml                          # ASR / LLM / TTS 三段配置
├── cmd/worker/main.go                   # 入口：监听 :8090，装配 health + orchestrate
└── internal/handler/                    # 单包多文件，关注点分离
    ├── config.go                        # ASRConfig / LLMConfig / TTSConfig + LoadConfig
    ├── health.go                        # /v1/health
    ├── orchestrate.go                   # /v1/orchestrate WS 主流程 + 两条 pump goroutine
    ├── session.go                       # 会话级状态（上游 conn + 写锁 + 设备上下文 + pendingToolCall）
    ├── llm.go                           # callLLMWithTools / callLLMSecondRound + 公共 postLLM
    ├── asr.go                           # translateASREvent（partial → asr_partial）
    ├── tts.go                           # 异步 callTTS + 下发 tts_audio
    ├── tools.go                         # OpenAI Function Calling Tool Schema + LLM 响应结构体
    └── prompt.go                        # BuildSystemPrompt（注入设备列表 + 控制规则）
```

> WebSocket 公共胶水（`Upgrader / Dialer / WriteJSON / IsNormalClose`）与 HTTP 工具（`Getenv / AccessLog`）已下沉到仓库根 [`pkg/wsutil`](../pkg/wsutil/README.md) 与 [`pkg/httpx`](../pkg/httpx/README.md)，与 server 共用，避免实现漂移。

### 依赖

```bash
go get github.com/gorilla/websocket
go get gopkg.in/yaml.v3
```

> 本模块通过仓库根 [`go.work`](../go.work) 引用本地 module `taffy.local/pkg/{protocol,wsutil,httpx}`（`go.mod` 中以 `replace` 兜底），不发布到任何模块代理。

---

## 配置

`config.yaml`：

```yaml
asr:
  ws_url: "ws://127.0.0.1:9100/v1/asr/stream"

llm:
  url: "https://dashscope.aliyuncs.com/compatible-mode/v1/chat/completions"
  api_key: "sk-your-api-key-here"   # 也可用 $env:LLM_API_KEY 覆盖
  model: "qwen-turbo"               # 阿里百炼 qwen-turbo / qwen-plus / qwen-max
  system_prompt: "你是「小菲」，一个智能家居语音助手。请用中文简短回答用户的问题。"
  timeout: 30

tts:
  url: "http://127.0.0.1:9200/v1/tts/synthesize"
```

> `system_prompt` 会在运行时由 Worker 自动追加当前用户的设备列表 + Tool 调用规则，无需手动维护。
> 实现见 [`internal/handler/prompt.go`](internal/handler/prompt.go) 的 `BuildSystemPrompt`。

| 字段 | YAML | 环境变量（优先） |
|---|---|---|
| ASR WS 地址 | `asr.ws_url` | `ASR_WS_URL` |
| LLM API Key | `llm.api_key` | `LLM_API_KEY` |
| LLM API 地址 | `llm.url` | `LLM_URL` |
| LLM 模型名 | `llm.model` | `LLM_MODEL` |
| TTS HTTP 地址 | `tts.url` | `TTS_URL` |
| Worker 监听端口 | — | `WORKER_PORT`（默认 `8090`） |
| 配置文件路径 | — | `CONFIG_PATH`（默认 `config.yaml`） |

### 切换 LLM / 模型

无需改 `config.yaml`，启动时设环境变量即可。例如从默认的 qwen-turbo 切换到另一个 Key + 模型：

```powershell
$env:LLM_API_KEY = "sk-新key"
$env:LLM_URL = "https://新端点/compatible-mode/v1/chat/completions"
$env:LLM_MODEL = "qwen3.6-plus"
.\worker.exe
```

未配置 LLM 时：`asr_final` 不会触发 LLM，worker 只完成 ASR 透传。
未配置 ASR 时：`/v1/orchestrate` 直接回 `error`。
未配置 TTS 时：跳过 `tts_audio` 下发，仅文本回复。

---

## 启动

```powershell
cd worker
$env:LLM_API_KEY = "sk-your-key"
go run ./cmd/worker          # 监听 :8090
```

健康检查：

```powershell
curl http://127.0.0.1:8090/v1/health
```

一键起全栈见 [`scripts/start-all.ps1`](../scripts/start-all.ps1)。

---

## 设计要点

### 1. 与 Server 的边界

Worker 完全不感知家具端 / 客户端 / 数据库 / MQTT，只接受 Server 通过 WS 推送的标准化语音会话，是个纯 AI 工作流容器。这样：

- Worker 可以独立部署到 GPU 机；Server 留在 CPU/IO 机。
- Worker 可以替换实现（Python / Rust / 其他模型栈），只要协议兼容。
- LLM API Key 等敏感配置只存在于 Worker，Server 不接触。

### 2. Session 统一收口写操作

`Session.WriteJSON` / `Session.WriteRaw` 内部持锁，所有需要往上游 WS 写的位置（ASR 回传 goroutine、LLM 异步 goroutine、TTS 异步 goroutine）都走 Session，避免触发 gorilla/websocket 的并发写 panic。

`Session` 同时持有：

- `devices / scenes` — 来自 server 的 `device_info`，二轮调用 LLM 时 `SnapshotDeviceContext()` 取快照；
- `toolCalls` — 已下发但还在等 result 的 tool_call，被 `RememberToolCall` 写入、`PopToolCall` 取出。

### 3. 异步 LLM 调用 + Tool Call 两轮机制

`asr_final` 触发后，LLM 调用在独立 goroutine 中进行，不阻塞下一段 ASR。家具端可以"边说边等回复"，多轮对话之间无需等待上一轮 LLM 结束。

Tool Call 流程：

1. Worker 收到 Server 推送的 `device_info`，缓存设备上下文
2. LLM 调用时将设备上下文注入 system prompt，并携带 `tools` 参数（`control_device`）
3. 若 LLM 返回 `tool_calls`：
   - Worker 将每个 tool call 封装为 `device_command` 下发给 Server
   - 等待 Server 回复 `device_command_result`
   - 将 tool 执行结果作为 `tool` role 消息回传 LLM 进行二次调用
   - LLM 根据执行结果生成自然语言回复（如"好的，已为您打开客厅灯"）
4. 若 LLM 直接返回文本（无需调工具），按原有逻辑回传 `llm_result`

两次 LLM 调用复用同一个 [`postLLM`](internal/handler/llm.go) 公共函数，避免代码重复。

### 4. 空文本短路

ASR 回 `asr_final` 但文本为空（环境噪音误触）时，worker 直接回 `llm_result text=""`，不调用 LLM，避免家具端 VAD 卡住。

### 5. TTS 异步、失败不阻塞回复

`synthesizeAndSendTTS` 在 goroutine 中运行；合成失败仅打 `warn` 日志，不发 `error` 事件给端侧——避免家具端把 TTS 异常当业务异常处理。

---

## 后续工作

- [x] Server↔Worker WS 透传 + LLM 调用
- [x] OpenAI Function Calling（Tool Call）两轮调用
- [x] TTS 异步合成 + `tts_audio` 下发
- [ ] LLM 流式输出（`stream=true`，逐 token 推 `llm_partial`）
- [ ] 多轮对话上下文（在 worker 内维护 `session_id` → 历史消息，目前每条 `asr_final` 是独立轮）
- [ ] 健康巡检：定时打 ASR / TTS `/v1/health`，掉线自动 `error: model_unavailable`
