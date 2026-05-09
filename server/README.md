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
                         ├──HTTPS──> [云端 LLM API]   返回 {reply, commands[]}
                         │
                         ├──MQTT───> [EMQX] ──> [其他家具：灯/空调/窗帘]
                         │
                         └──HTTP───> [TTS Model :9200]   返回 wav 字节


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

详见 [`furniture/README.md`](../furniture/README.md) "协议：家具端 ↔ Go Server WebSocket" 章节。摘要：

| 方向 | 帧类型 | 内容 |
|---|---|---|
| 家具→Server | text | `{"type":"start","sample_rate":16000,"format":"pcm_s16le","channels":1}` |
| 家具→Server | binary | 16-bit LE PCM mono 字节流，每 ~600ms 一包 |
| 家具→Server | text | `{"type":"end"}` |
| Server→家具 | text | `{"type":"asr_partial","text":"..."}` |
| Server→家具 | text | `{"type":"asr_final","text":"..."}` |
| Server→家具 | text | `{"type":"reply","text":"..."}` |
| Server→家具 | text | `{"type":"device_done","device":"...","action":"..."}` |
| Server→家具 | text | `{"type":"tts_audio","format":"wav","data":"<base64>"}` |
| Server→家具 | text | `{"type":"error","message":"..."}` |

### 转换说明（Go Server 做的事）

ASR Model 原生事件类型为 `ready / partial / final / eos / error`，Go Server 包装：

| ASR 原生 | Go Server 转发给家具 |
|---|---|
| `ready` | 不转发（吞掉） |
| `partial` | `asr_partial` |
| `final` | `asr_final`，并触发 LLM 流程 |
| `eos` | 不转发（吞掉） |
| `error` | `error` |

---

## 骨架代码：WS 透传 + LLM 编排

> **本轮目标：先打通"家具端 ↔ Go Server ↔ ASR"这一段**。LLM / TTS / MQTT 留 TODO。

### 依赖

```bash
go get github.com/gorilla/websocket
go get github.com/golang-jwt/jwt/v5
# 后续接 MQTT：
# go get github.com/eclipse/paho.mqtt.golang
```

### `voice_handler.go`（核心）

```go
package handler

import (
    "context"
    "encoding/base64"
    "encoding/json"
    "log"
    "net/http"
    "sync"
    "time"

    "github.com/gorilla/websocket"
)

// === 配置 ===
const (
    asrWSURL = "ws://127.0.0.1:9100/v1/asr/stream"
    ttsURL   = "http://127.0.0.1:9200/v1/tts/synthesize"
)

var upgrader = websocket.Upgrader{
    ReadBufferSize:  32 * 1024,
    WriteBufferSize: 32 * 1024,
    CheckOrigin:     func(r *http.Request) bool { return true }, // 生产收紧
}

// === 协议事件 ===
type evt struct {
    Type    string `json:"type"`
    Text    string `json:"text,omitempty"`
    Format  string `json:"format,omitempty"`
    Data    string `json:"data,omitempty"`     // base64
    Device  string `json:"device,omitempty"`
    Action  string `json:"action,omitempty"`
    Message string `json:"message,omitempty"`
}

// === 入口：家具端连这里 ===
func HandleVoice(w http.ResponseWriter, r *http.Request) {
    // 0. 设备鉴权（query 参数：?device_id=&token=）
    deviceID, err := authDeviceFromQuery(r)
    if err != nil {
        http.Error(w, "unauthorized", http.StatusUnauthorized)
        return
    }

    cli, err := upgrader.Upgrade(w, r, nil)
    if err != nil {
        return
    }
    defer cli.Close()

    ctx, cancel := context.WithCancel(r.Context())
    defer cancel()

    // 1. 拨号到 ASR
    asr, _, err := websocket.DefaultDialer.DialContext(ctx, asrWSURL, nil)
    if err != nil {
        sendEvt(cli, evt{Type: "error", Message: "asr_unreachable"})
        return
    }
    defer asr.Close()

    // 2. 写家具端 WS 加锁（多 goroutine 都会写）
    var sendMu sync.Mutex
    sendToFurniture := func(e evt) {
        sendMu.Lock()
        defer sendMu.Unlock()
        _ = cli.WriteJSON(e)
    }

    var finalText string
    finalCh := make(chan string, 1)

    // 2a. ASR -> 家具端（事件改写）
    go func() {
        defer cancel()
        for {
            mt, data, err := asr.ReadMessage()
            if err != nil {
                return
            }
            if mt != websocket.TextMessage {
                continue
            }
            var e evt
            if err := json.Unmarshal(data, &e); err != nil {
                continue
            }
            switch e.Type {
            case "ready":
                // 吞掉
            case "partial":
                sendToFurniture(evt{Type: "asr_partial", Text: e.Text})
            case "final":
                finalText = e.Text
                sendToFurniture(evt{Type: "asr_final", Text: e.Text})
            case "eos":
                select {
                case finalCh <- finalText:
                default:
                }
                return
            case "error":
                sendToFurniture(evt{Type: "error", Message: e.Message})
                return
            }
        }
    }()

    // 2b. 家具端 -> ASR（直接透传，含 text 控制帧与 binary 音频帧）
    go func() {
        defer cancel()
        for {
            mt, data, err := cli.ReadMessage()
            if err != nil {
                return
            }
            if err := asr.WriteMessage(mt, data); err != nil {
                return
            }
        }
    }()

    // 3. 等 final 或断开
    var text string
    select {
    case text = <-finalCh:
    case <-ctx.Done():
        return
    case <-time.After(60 * time.Second):
        sendToFurniture(evt{Type: "error", Message: "asr_timeout"})
        return
    }
    if text == "" {
        sendToFurniture(evt{Type: "error", Message: "empty_text"})
        return
    }

    // 4. 跑 LLM → 设备指令 → TTS（TODO：本轮先打桩）
    go runDialogPipeline(ctx, deviceID, text, sendToFurniture)

    // 5. 让 pipeline 把事件推完再退出
    <-ctx.Done()
}

// === LLM + 设备 + TTS（TODO：占位）===
func runDialogPipeline(
    ctx context.Context, deviceID string, text string,
    send func(evt),
) {
    // TODO 4.1 调云端 LLM
    reply, commands, err := callCloudLLM(ctx, deviceID, text)
    if err != nil {
        send(evt{Type: "error", Message: "llm_failed: " + err.Error()})
        return
    }
    if reply != "" {
        send(evt{Type: "reply", Text: reply})
    }

    // TODO 4.2 派发设备指令到 MQTT
    for _, cmd := range commands {
        if err := publishMQTT(cmd); err != nil {
            log.Printf("mqtt publish err: %v", err)
            continue
        }
        send(evt{Type: "device_done", Device: cmd.Device, Action: cmd.Action})
    }

    // TODO 4.3 TTS
    if reply != "" {
        wav, err := callTTS(ctx, reply)
        if err == nil {
            send(evt{
                Type:   "tts_audio",
                Format: "wav",
                Data:   base64.StdEncoding.EncodeToString(wav),
            })
        }
    }
}

// ---- helpers (TODO) ----
func authDeviceFromQuery(r *http.Request) (string, error) {
    // 从 ?device_id=&token= 解析并校验设备凭证
    return r.URL.Query().Get("device_id"), nil
}
func sendEvt(c *websocket.Conn, e evt) { _ = c.WriteJSON(e) }

type DeviceCommand struct{ Device, Action string }

func callCloudLLM(ctx context.Context, deviceID, text string) (string, []DeviceCommand, error) {
    // TODO: 调通义/豆包/DeepSeek，按 JSON Schema 解析 reply + commands
    return "", nil, nil
}
func publishMQTT(cmd DeviceCommand) error                       { return nil }
func callTTS(ctx context.Context, text string) ([]byte, error)  { return nil, nil }
```

### `main.go`

```go
package main

import (
    "log"
    "net/http"

    "your/module/handler"
)

func main() {
    http.HandleFunc("/v1/voice", handler.HandleVoice)
    // 其他路由：登录、设备、场景、客户端 WS ...
    log.Println("server listening on :8080")
    log.Fatal(http.ListenAndServe(":8080", nil))
}
```

---

## 联调：不需要家具端，先把"Server ↔ ASR"打通

家具端还没开发，但可以用 Python 脚本**模拟家具端**，验证整条链路。

### 步骤

1. 启动 ASR 模型服务（在 `model/` 目录）：

   ```powershell
   cd model
   .\start_all.ps1
   # 或单跑 ASR：
   # python -m uvicorn asr_server.app:app --host 0.0.0.0 --port 9100
   ```

   验证 ASR 自身：

   ```powershell
   curl http://127.0.0.1:9100/v1/health
   ```

2. 启动 Go Server（占位 LLM/TTS/MQTT 也没关系，至少透传跑通）：

   ```powershell
   cd server
   go run ./cmd/server
   ```

3. 用模拟脚本扮演家具端，喂一个 wav 文件给 Go Server，期望打印出 `asr_partial` / `asr_final`：

   ```powershell
   python model/asr_server/scripts/test_stream.py `
     --url ws://127.0.0.1:8080/v1/voice `
     --wav path/to/test_16k_mono.wav
   ```

   > 该脚本同样支持直连 ASR（`--url ws://127.0.0.1:9100/v1/asr/stream`），可分两段验证：
   > - 直连 ASR 通 → 模型层 OK
   > - 经 Go Server 通 → 透传层 OK

### 期望输出

```
[mock-furniture] connected ws://127.0.0.1:8080/v1/voice
[mock-furniture] sent start frame
[mock-furniture] sent 600ms PCM frame (19200 bytes)
...
<- asr_partial: 打开
<- asr_partial: 打开客厅
<- asr_partial: 打开客厅灯
[mock-furniture] sent end frame
<- asr_final: 打开客厅灯
[mock-furniture] done
```

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
- [x] 联调脚本（`model/asr_server/scripts/test_stream.py`）
- [ ] 搭基础脚手架（gin / 标准库 net/http；建议先 net/http 轻量起步）
- [ ] 实现 `/v1/voice` 透传（按本文档骨架，不含 LLM/TTS/MQTT）
- [ ] 用模拟脚本跑通"家具端 → Go Server → ASR"
- [ ] 实现客户端 JWT 鉴权与登录
- [ ] 实现设备 Token 鉴权
- [ ] 接入云端 LLM 客户端（Prompt 模板 + JSON Schema 校验）
- [ ] 接入 MQTT（推荐 `eclipse/paho.mqtt.golang`）
- [ ] 接入 TTS HTTP 客户端
- [ ] 健康巡检：定时打 ASR/TTS `/v1/health`，掉线后家具端 fallback
