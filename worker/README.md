# Worker（Go 大模型编排进程）

> Taffy 系统的"AI 编排层"。从 v0.3 起从 Server 中拆出，独占 ASR / LLM / TTS 等模型能力的调度。
> 从 v0.5 起支持 **OpenAI Function Calling（Tool Call）**，LLM 可按需调用 `control_device` / `activate_scene` 等工具函数控制家居设备。
>
> **职责**：
>
> - 接受 Server 的 WS 连接（`/v1/orchestrate`）
> - 把上游送来的语音流（`start / [PCM] / end`）转发到本地 ASR
> - ASR 的 `partial / final` 翻译为 `asr_partial / asr_final` 回送
> - `asr_final` 触发云端 LLM 调用（**携带设备上下文 + Tool Schema**）
> - LLM 返回 `tool_calls` 时，封装为 `device_command` 下发给 Server 执行
> - 收到 Server 的 `device_command_result` 后，将结果回传 LLM 进行**二次调用**，生成最终自然语言回复
> - 最终回复以 `llm_result / llm_error` 回送
> - 未来：并行 TTS 合成、与场景服务对接
>
> **不负责**：家具端 / 客户端的接入与鉴权，对话历史落库，MQTT 设备控制，数据库操作。这些在 [`../server`](../server)。

---

## 架构定位

```
家具端 ──WS──▶ Server :8080 ──WS──▶ Worker :8090 ──WS──▶ ASR Model :9100
   ▲                  │                    │
   │                  │                    ├──HTTPS──▶ 云端 LLM API (Tool Call)
   └──── llm_result ──┴────── 透传 ────────┘
                                            └──HTTP───▶ TTS Model :9200 (M3)

v0.5 新增消息流：
Server ──device_info──▶ Worker   （会话开始时推送设备上下文）
Worker ──device_command──▶ Server （LLM tool_calls → 设备控制指令）
Server ──device_command_result──▶ Worker （控制结果回传 → 二次 LLM 调用）
```

- Server ↔ Worker 的协议**与"家具端 ↔ Server"基本相同**，Server 透传 + 鉴权 + `device_command` 拦截。
- Worker 是无状态的，可以水平扩多份，由 Server 通过配置切换 / 负载均衡。

---

## 端点

| 端点 | 协议 | 调用方 | 用途 |
|---|---|---|---|
| `/v1/orchestrate` | **WS** | Server | 核心：语音流入 + 大模型结果流出 |
| `/v1/health` | HTTP | 运维 / Server | 健康检查（含 ASR / LLM / TTS 配置状态） |

### `/v1/orchestrate` 协议

| 方向 | 帧类型 | 内容 |
|---|---|---|
| Server→Worker | text | `{"type":"start","sample_rate":16000,"format":"pcm_s16le","channels":1}` |
| Server→Worker | binary | 16-bit LE PCM mono 字节流 |
| Server→Worker | text | `{"type":"end"}` |
| Server→Worker | text | `{"type":"ping"}`（保活） |
| Worker→Server | text | `{"type":"pong"}` |
| Worker→Server | text | `{"type":"asr_partial","text":"..."}` |
| Worker→Server | text | `{"type":"asr_final","text":"..."}` |
| Worker→Server | text | `{"type":"eos"}` |
| Worker→Server | text | `{"type":"llm_result","text":"..."}` |
| Worker→Server | text | `{"type":"llm_error","message":"..."}` |
| Worker→Server | text | `{"type":"error","message":"..."}` |
| **Server→Worker** | text | **`{"type":"device_info","devices":[...]}`** — **v0.5 新增**：会话建立后推送用户设备列表+状态，供 LLM 上下文注入 |
| **Worker→Server** | text | **`{"type":"device_command","tool_id":"...","function":{"name":"control_device","arguments":"{...}"}}`** — **v0.5 新增**：LLM 返回 tool_calls 时下发 |
| **Server→Worker** | text | **`{"type":"device_command_result","tool_id":"...","success":true,"message":"..."}`** — **v0.5 新增**：Server 执行设备控制后回传结果 |

Server 在拨号 worker 时会带上 query 参数 `device_id` / `session_id`，便于 worker 日志与上游会话关联。

---

## 配置

`config.yaml`：

```yaml
asr:
  ws_url: "ws://127.0.0.1:9100/v1/asr/stream"

llm:
  url: "https://open.bigmodel.cn/api/paas/v4/chat/completions"
  api_key: "sk-your-api-key-here"   # 也可用 $env:LLM_API_KEY 覆盖
  model: "GLM-4-Flash-250414"
  system_prompt: "你是智能家居助手小菲，负责控制用户家中的智能设备。"
  timeout: 30

tts:
  url: "http://127.0.0.1:9200/v1/tts/synthesize"
```

> `system_prompt` 会在运行时由 Worker 自动追加上当前用户的设备列表信息，无需手动维护。

| 字段 | YAML | 环境变量（优先） |
|---|---|---|
| ASR WS 地址 | `asr.ws_url` | `ASR_WS_URL` |
| LLM API Key | `llm.api_key` | `LLM_API_KEY` |
| TTS HTTP 地址 | `tts.url` | `TTS_URL` |
| Worker 监听端口 | — | `WORKER_PORT`（默认 `8090`） |
| 配置文件路径 | — | `CONFIG_PATH`（默认 `config.yaml`） |

未配置 LLM 时：`asr_final` 不会触发 LLM，worker 只完成 ASR 透传。
未配置 ASR 时：`/v1/orchestrate` 直接回 `error`。

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

Worker **完全不感知** 家具端 / 客户端 / 数据库 / MQTT。它只接受 Server 通过 WS 推送的"标准化语音会话"，是个纯 AI 工作流容器。这样：

- Worker 可以独立部署到 GPU 机；Server 留在 CPU/IO 机。
- Worker 可以替换实现（Python / Rust / 其他模型栈），只要协议兼容。
- LLM API Key 等敏感配置只存在于 Worker，Server 不接触。

### 2. 写上游 WS 必须加锁

ASR 回传 goroutine 与 LLM 回复 goroutine 都会写同一个 WS 连接，必须用 `sync.Mutex` 串行化，否则会触发 gorilla/websocket 的并发写 panic。

### 3. 异步 LLM 调用 + Tool Call 两轮机制

`asr_final` 触发后，LLM 调用在独立 goroutine 中进行，**不阻塞下一段 ASR**。家具端可以"边说边等回复"，多轮对话之间无需等待上一轮 LLM 结束。

**v0.5 Tool Call 流程**：

1. Worker 收到 Server 推送的 `device_info`，缓存设备上下文
2. LLM 调用时，将设备上下文注入 system prompt，并携带 `tools` 参数（`control_device` / `activate_scene`）
3. 若 LLM 返回 `tool_calls`：
   - Worker 将每个 tool call 封装为 `device_command` 下发给 Server
   - 等待 Server 回复 `device_command_result`
   - 将 tool 执行结果作为 `tool` role 消息回传 LLM，进行**二次调用**
   - LLM 根据执行结果生成自然语言回复（如"好的，已为您打开客厅灯"）
4. 若 LLM 直接返回文本（无需调工具），按原有逻辑回传 `llm_result`

### 4. 空文本短路

ASR 回 `asr_final` 但文本为空（环境噪音误触）时，worker 直接回 `llm_result text=""`，不调用 LLM，避免家具端 VAD 卡住。

---

## 后续工作

- [x] **LLM Tool Call（Function Calling）支持**（v0.5：control_device / activate_scene 工具函数 + 二轮 LLM 调用）
- [x] **设备上下文动态注入**（v0.5：接收 device_info → 注入 system prompt）
- [x] **device_command 下发与结果回传**（v0.5：通过 WS 协议与 Server 交互）
- [ ] LLM 流式输出（`stream=true`，逐 token 推 `llm_partial`）
- [ ] 接入 TTS HTTP，把 wav 字节通过 `tts_audio` 帧回送
- [ ] 多轮对话上下文（在 worker 内维护 `session_id` → 历史消息）
- [ ] 健康巡检：定时打 ASR / TTS `/v1/health`，掉线自动 `error: model_unavailable`
