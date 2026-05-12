# Worker（Go 大模型编排进程）

> SHVA 系统的"AI 编排层"。从 v0.3 起从 Server 中拆出，独占 ASR / LLM / TTS 等模型能力的调度。
>
> **职责**：
>
> - 接受 Server 的 WS 连接（`/v1/orchestrate`）
> - 把上游送来的语音流（`start / [PCM] / end`）转发到本地 ASR
> - ASR 的 `partial / final` 翻译为 `asr_partial / asr_final` 回送
> - `asr_final` 触发云端 LLM 调用，结果以 `llm_result / llm_error` 回送
> - 未来：并行 TTS 合成、设备指令解析、与场景服务对接
>
> **不负责**：家具端 / 客户端的接入与鉴权，对话历史落库，MQTT 设备控制。这些在 [`../server`](../server)。

---

## 架构定位

```
家具端 ──WS──▶ Server :8080 ──WS──▶ Worker :8090 ──WS──▶ ASR Model :9100
   ▲                  │                    │
   │                  │                    ├──HTTPS──▶ 云端 LLM API
   └──── llm_result ──┴────── 透传 ────────┘
                                            └──HTTP───▶ TTS Model :9200 (M3)
```

- Server ↔ Worker 的协议**与"家具端 ↔ Server"完全相同**，Server 只做透传 + 鉴权 + 会话日志。
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
  system_prompt: "你是一个智能家居助手，请用中文简短回答用户的问题。"
  timeout: 30

tts:
  url: "http://127.0.0.1:9200/v1/tts/synthesize"
```

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

一键起全栈见根目录 [`start_all.ps1`](../start_all.ps1)。

---

## 设计要点

### 1. 与 Server 的边界

Worker **完全不感知** 家具端 / 客户端 / 数据库 / MQTT。它只接受 Server 通过 WS 推送的"标准化语音会话"，是个纯 AI 工作流容器。这样：

- Worker 可以独立部署到 GPU 机；Server 留在 CPU/IO 机。
- Worker 可以替换实现（Python / Rust / 其他模型栈），只要协议兼容。
- LLM API Key 等敏感配置只存在于 Worker，Server 不接触。

### 2. 写上游 WS 必须加锁

ASR 回传 goroutine 与 LLM 回复 goroutine 都会写同一个 WS 连接，必须用 `sync.Mutex` 串行化，否则会触发 gorilla/websocket 的并发写 panic。

### 3. 异步 LLM 调用

`asr_final` 触发后，LLM 调用在独立 goroutine 中进行，**不阻塞下一段 ASR**。家具端可以"边说边等回复"，多轮对话之间无需等待上一轮 LLM 结束。

### 4. 空文本短路

ASR 回 `asr_final` 但文本为空（环境噪音误触）时，worker 直接回 `llm_result text=""`，不调用 LLM，避免家具端 VAD 卡住。

---

## 后续工作

- [ ] LLM 流式输出（`stream=true`，逐 token 推 `llm_partial`）
- [ ] 接入 TTS HTTP，把 wav 字节通过 `tts_audio` 帧回送
- [ ] 设备指令解析（结构化 JSON Schema 校验，下行 `device_intent`）
- [ ] 多轮对话上下文（在 worker 内维护 `session_id` → 历史消息）
- [ ] 健康巡检：定时打 ASR / TTS `/v1/health`，掉线自动 `error: model_unavailable`
