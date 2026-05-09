# Server（Go 业务服务器）

> SHVA 业务后端，使用 Go 实现。**整个系统的中枢**，吃掉了原设计中独立的"Python LLM Worker"角色。
>
> **职责**：
> - 客户端鉴权、设备管理、对话编排（REST + WSS）
> - 家具端音频 WebSocket 透传到本地 ASR
> - ASR 文本拿到后调用云端 LLM、解析结构化指令
> - 通过 MQTT 下发设备控制指令
> - 调本地 TTS 合成语音回播给家具端
>
> 当前是占位目录，本文档给出**架构、协议契约、骨架代码、联调步骤**。

---

## 架构总览

```
[家具端]                 [Go Server :8080]               [ASR Model :9100]
  麦克风 ──WS音频──>   /v1/voice   ──WS音频透传──>   /v1/asr/stream
  扬声器 <──TTS wav──    │  ▲                            │
                         │  │ partial / final            │
                         │  └────────────────────────────┘
                         │
                         ├──HTTPS──> [云端 LLM API]  ←  config.yaml + $env:LLM_API_KEY
                         │             返回 llm_result
                         │
                         ├──MQTT───> [EMQX] ──> [其他家具：灯/空调/窗帘]  (M3)
                         │
                         └──HTTP───> [TTS Model :9200]   返回 wav 字节  (M3)


[客户端 App] ──HTTPS / WSS──> Go Server 的 REST + 状态推送（不走音频）
```

---

## 模块清单（对应设计文档 §3.1）

| 模块 ID | 名称 | 职责 |
|---|---|---|
| **M-GW** | API 网关 | 客户端 / 家具端 接入、JWT 鉴权、限流 |
| **M-AU** | 用户与认证 | 用户注册、登录、密码哈希、JWT 签发 |
| **M-DV** | 设备管理 | 设备绑定、属性、能力、在线状态、MQTT 收发 |
| **M-CH** | 会话与编排 | 接收家具端音频流 → 透传 ASR → 调 LLM → 派发 MQTT 指令 → 调 TTS |
| **M-SC** | 场景服务 | 场景定义、触发与执行 |

> 原 `M-LM 大模型工作流（Python）` 模块已**合并进 M-CH**，不再单独部署。

---

## 端点全景

| 端点 | 协议 | 调用方 | 用途 |
|---|---|---|---|
| `/v1/voice` | **WS** | 家具端 | **核心**：音频上行 / 识别文本与回复下行 |
| `/api/v1/auth/*` | HTTPS | 客户端 App | 注册 / 登录 / 登出 |
| `/api/v1/devices/*` | HTTPS | 客户端 App | 设备绑定 / 列表 / 状态 |
| `/api/v1/scenes/*` | HTTPS | 客户端 App | 场景管理 |
| `/api/v1/conversations/*` | HTTPS | 客户端 App | 对话历史 |
| `/ws` | WSS | 客户端 App | 设备状态实时推送（device.status / chat.message） |
| `/v1/health` | HTTP | 运维 / 客户端 | 健康检查 |

> 客户端 App **不**直接调 ASR / TTS；家具端 **不**直接调 ASR / TTS。所有 AI 能力都经 Go Server 编排。

---

## 核心：`/v1/voice` 家具端音频 WebSocket

### 协议

详见 [`furniture/README.md`](../furniture/README.md) "协议：家具端 ↔ Go Server WebSocket" 章节。

**单 WS 多轮对话**：家具端在一条连接内可以循环发 N 组 `start / [PCM...] / end`，每组触发一次 `asr_final + eos`，连接不断开，直到家具端主动关闭。

协议摘要（M2 完整版）：

| 方向 | 帧类型 | 内容 |
|---|---|---|
| 家具→Server | text | `{"type":"start","sample_rate":16000,"format":"pcm_s16le","channels":1}` |
| 家具→Server | binary | 16-bit LE PCM mono 字节流，每 ~600ms 一包 |
| 家具→Server | text | `{"type":"end"}`（端侧 VAD 触发，每轮 1 次） |
| 家具→Server | text | `{"type":"ping"}`（保活） |
| Server→家具 | text | `{"type":"pong"}` |
| Server→家具 | text | `{"type":"asr_partial","text":"..."}` |
| Server→家具 | text | `{"type":"asr_final","text":"..."}` |
| Server→家具 | text | `{"type":"eos"}`（本段结束；**连接保持，可开始下一段**） |
| Server→家具 | text | `{"type":"llm_result","text":"..."}`（M2：大模型回答） |
| Server→家具 | text | `{"type":"llm_error","message":"..."}`（M2：大模型调用失败） |
| Server→家具 | text | `{"type":"reply","text":"..."}`（M3） |
| Server→家具 | text | `{"type":"device_done","device":"...","action":"..."}`（M3） |
| Server→家具 | text | `{"type":"tts_audio","format":"wav","data":"<base64>"}`（M3） |
| Server→家具 | text | `{"type":"error","message":"..."}` |

### 转换说明（Go Server 做的事）

ASR Model 原生事件类型为 `ready / partial / final / eos / error`，Go Server 逐帧转发并改写类型：

| ASR 原生 | Go Server 转发给家具 |
|---|---|
| `ready` | 不转发（吞掉） |
| `partial` | `asr_partial` |
| `final` | `asr_final`，**并异步触发 LLM 调用**（不阻塞下一段 ASR 回传） |
| `eos` | `eos`（透传） |
| `error` | `error` |

**M2 核心逻辑**：`asr_final` 触发 Server 启动一个 goroutine，用 `config.yaml` 中配置的 URL + `$env:LLM_API_KEY` 调用兼容 OpenAI 格式的大模型 API，等待返回后将结果通过 `llm_result` 事件送回家具端。家具端收到后恢复 VAD 语音检测，进入下一轮对话。

> 若 ASR 返回空文本（如环境噪音误触发），Server 直接发空 `llm_result` 回家具端，不调用 LLM，VAD 正常恢复。避免卡死。

---

## 实现源码参考（M2 已落地）

> 骨架设计阶段的伪代码已替换为真实实现。核心文件结构如下：

```
server/
├── config.yaml                          # LLM API 配置（url / model / system_prompt）
├── cmd/server/main.go                   # 入口：加载配置 → 路由注册 → 优雅退出
└── internal/handler/
    ├── config.go                        # AppConfig / LLMConfig 结构体 + YAML 解析 + 环境变量覆盖
    ├── health.go                        # /v1/health（含 LLM 状态：configured / disabled）
    └── voice.go                         # /v1/voice（核心）：
                                         #   · 家具端 ↔ ASR 双向透传
                                         #   · ASR 事件翻译（partial → asr_partial 等）
                                         #   · asr_final 触发异步 LLM 调用
                                         #   · 写客户端 WS 线程安全（sync.Mutex）
```

### 依赖

```bash
go get github.com/gorilla/websocket
go get gopkg.in/yaml.v3
```

### 配置方式

| 字段 | YAML 配置项 | 环境变量（更高优先级） |
|---|---|---|
| API 地址 | `llm.url` | — |
| API Key | `llm.api_key` | `LLM_API_KEY` |
| 模型名 | `llm.model`（默认 `gpt-3.5-turbo`） | — |
| System Prompt | `llm.system_prompt` | — |
| 超时 | `llm.timeout`（默认 30s） | — |

配置示例见 [`config.yaml`](./config.yaml)。未配置 LLM 时，健康检查显示 `llm: disabled`，`/v1/voice` 退化为纯 ASR 透传模式。

### 核心流程（`voice.go`）

```go
// VoiceHandler.ServeHTTP(w, r)
//   1. 升级家具端 WS 连接
//   2. 拨号到 ASR WS（ws://127.0.0.1:9100/v1/asr/stream）
//   goroutine A: 家具端 → ASR（透传 start / PCM / end / ping）
//   goroutine B: ASR → 家具端（翻译事件类型 + 转发）
//     · 收到 asr_final → 启动 goroutine C 异步调用 LLM
//   goroutine C: 调 LLM API → 写 llm_result / llm_error 回家具端
//   写 clientConn 用 sync.Mutex 串行化（goroutine B + C 都可能写）
```

---

## 联调：四层验证法

家具端还没上硬件，但用 `furniture/mock_furniture.py` 当**虚拟音箱**就能把"家具端 → Go Server → ASR"端到端跑通。为便于定位问题，按四层从底到顶逐层验证——哪层红了就停在那层查，不要越级。

```
第 1 层  ASR 进程是否起来        → curl /v1/health
第 2 层  ASR 整段识别是否正确     → curl -F audio=@xxx.wav /v1/asr/transcribe
第 3 层  ASR 流式 WS 是否出字     → mock_furniture.py 直连 :9100
第 4 层  Server↔Model 透传是否通  → mock_furniture.py 走 :8080
```

### 步骤

1. 启动 ASR 模型服务（`model/`）：

   ```powershell
   cd model
   .\start_all.ps1
   # 或单跑 ASR：
   # python -m uvicorn asr_server.app:app --host 0.0.0.0 --port 9100
   ```

   第 1 层验证：

   ```powershell
   curl http://127.0.0.1:9100/v1/health     # 期望 stream_loaded: true
   ```

2. 第 2 层（可选，确认模型本身识别 OK）：

   ```powershell
   $wav = "model\audio_output\20260509_203301\01_把客厅的灯打开.wav"
   curl.exe -X POST -F "audio=@$wav" http://127.0.0.1:9100/v1/asr/transcribe
   # 期望：{"text":"把客厅的灯打开", ...}
   ```

3. 启动 Go Server：

   ```powershell
   cd server
   $env:PORT="8080"
   $env:ASR_WS_URL="ws://127.0.0.1:9100/v1/asr/stream"
   go run ./cmd/server
   ```

4. 第 3 层：**直连 ASR 的流式 WS**（跳过 Server，只验模型侧流式链路）：

   ```powershell
   pip install -r furniture/requirements.txt    # 首次；建议同时 pip install scipy
   python furniture/mock_furniture.py `
     --server ws://127.0.0.1:9100/v1/asr/stream `
     --wav model\audio_output\20260509_203301\01_把客厅的灯打开.wav
   ```

   期望：`ready` → 若干 `partial` → `final: 把客厅的灯打开` → `eos`。这层通了再往第 4 层走。

5. 第 4 层：**经 Go Server 透传**（端到端）：

   ```powershell
   # 单句
   python furniture/mock_furniture.py `
     --server ws://127.0.0.1:8080/v1/voice `
     --device-id dev1 --token t1 `
     --wav model\audio_output\20260509_203301\01_把客厅的灯打开.wav

   # 多句连说（单 WS 多轮对话）
   python furniture/mock_furniture.py `
     --server ws://127.0.0.1:8080/v1/voice `
     --wav a.wav b.wav c.wav --gap-ms 1500
   ```

### 期望三端日志

- **家具端**：`ready` → `asr_partial` × N → `asr_final: 把客厅的灯打开` → `eos`
- **Go Server**：`client connected` → `asr connected asr=ws://127.0.0.1:9100/...` → `session done`
- **ASR Model**：`ws connected` → `segment #1 done: text='把客厅的灯打开'` → `ws closed segments=1`

多句连说时，Server / ASR 日志中**不应该**出现中途 `ws closed` / 新的 `client connected`——整个多轮会话共用同一条连接。

### 分层定位口诀

- 第 3 层直连 ASR 通、第 4 层经 Server 挂 → 问题在 Go Server 透传层。
- 第 3 层直连 ASR 就不通 → 问题在 ASR 模型（采样率、VAD、模型未下载等），与 Server 无关。

更多故障定位（错误提示 → 修法）见顶层 [`README.md`](../README.md) "联调测试：四层验证法" 章节。

---

## 设计要点

### 1. 双向透传，不动音频字节

家具端发的二进制 PCM 帧**原样转发**给 ASR，Go Server 不做解码 / 重采样，节省 CPU 也避免引入额外延迟。控制帧（`start` / `end` / `ping`）也直接透传，因为 ASR 协议与家具端协议完全一致。

### 2. 事件类型重写

ASR 返回的是 `partial / final / eos`，Go Server 转换为带语义前缀的 `asr_partial / asr_final`，方便家具端按 `type` 字段分发，避免和 `reply / device_done / tts_audio` 等下游事件混淆。

### 3. 写家具端 WS 必须加锁

多个 goroutine（ASR 转发、LLM 回复、设备结果、TTS）都会写同一个家具端 WS 连接，**必须用 `sync.Mutex` 串行化写操作**，否则会触发 gorilla/websocket 的并发写 panic。

### 4. TTS 与设备控制并行

LLM 返回后，**TTS 合成**（耗时 ~300~800ms）和 **MQTT 派发**（~50ms）应并行启动，让用户先听到回复，设备同时执行。

### 5. 超时与降级

- ASR 60s 内没 final → 返回 `error: asr_timeout`
- LLM 失败 → 返回 `error: llm_failed`，本轮交互终止
- TTS 失败 → **静默降级**，只回 `reply` 文本，不回 `tts_audio`（家具端如有 TTS 兜底则本地播放，否则只显示文字 / 跳过）
- MQTT 失败 → 单条指令失败不影响其他指令，记录日志

### 6. 设备鉴权

家具端 WS 不像 HTTP 那样方便加 Header，使用 query 参数：

```
ws://server:8080/v1/voice?device_id=<id>&token=<device-token>
```

`device-token` 是家具端配网阶段烧录 / 写入的长期凭证，与客户端用户的 JWT 不同。

---

## 模块拆分建议（编码前）

```
server/
├── cmd/server/main.go          # 入口
├── internal/
│   ├── handler/
│   │   ├── voice.go            # /v1/voice （家具端音频 WS）★ 本文档骨架
│   │   ├── auth.go             # /api/v1/auth/* （客户端登录注册）
│   │   ├── device.go           # /api/v1/devices/*
│   │   ├── scene.go            # /api/v1/scenes/*
│   │   ├── conversation.go     # /api/v1/conversations/*
│   │   └── client_ws.go        # /ws （客户端状态推送）
│   ├── asr/                    # ASR WS 客户端封装
│   ├── tts/                    # TTS HTTP 客户端
│   ├── llm/                    # 云端 LLM 客户端 (含 prompt 模板与 JSON Schema 校验)
│   ├── mqtt/                   # MQTT 发布订阅
│   ├── auth/                   # JWT、密码哈希 (bcrypt)、设备 token
│   ├── store/                  # MySQL/Redis 持久化
│   └── model/                  # User/Device/Scene/Conversation 等领域模型
├── go.mod
└── README.md
```

---

## 后续工作

- [x] 与 ASR Model 的 WS 协议契约
- [x] 家具端 ↔ Go Server 协议契约
- [x] 联调脚本（`furniture/mock_furniture.py`，含 VAD 自动断句 / 单 WS 多轮 / 直连 ASR 分层排障）
- [x] 搭基础脚手架（`cmd/server` + `internal/handler`，标准库 `net/http`）
- [x] 实现 `/v1/voice` 透传（M1：双向透传 + 事件改名，不含 LLM/TTS/MQTT）
- [x] 用模拟脚本跑通"家具端 → Go Server → ASR"（四层验证法全绿）
- [x] **接入云端 LLM 客户端（config.yaml + $env:LLM_API_KEY）**（M2）
- [ ] 实现客户端 JWT 鉴权与登录
- [ ] 实现设备 Token 鉴权
- [ ] 接入 MQTT（推荐 `eclipse/paho.mqtt.golang`）
- [ ] 接入 TTS HTTP 客户端
- [ ] 健康巡检：定时打 ASR/TTS `/v1/health`，掉线后家具端 fallback
