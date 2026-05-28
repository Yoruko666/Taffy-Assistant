# 语音模型工作流（Model 模块）

> 本模块对应设计文档 §3.3 中的 **M-AS / M-TS**，负责本地 ASR / TTS 模型的部署与推理。
> LLM 走云端 API，无需本地下载。
>
> **生产路径**：**家具端**音频经 Go Server 透传 → ASR 走 **WebSocket 流式识别**；TTS 走 HTTP 整段合成，音频回传给**家具端**播放。
> 客户端 App **不**参与音频链路（仅做控制面板），详见 [`client/README.md`](../client/README.md)。

## 目录结构

```
model/
├── README.md                   # 本文件
├── setup_gguf.py               # 一键下载推理依赖包到 gguf_pkg/
├── download_model.py           # 一键下载 ASR(流式+VAD) / TTS 模型
├── gguf_pkg/                   # 离线依赖包仓库（pip download 产物，已 gitignore）
├── start_all.ps1               # 一键启动 ASR (:9100) + TTS (:9200)
├── test_stream_e2e.py          # TTS → 流式 ASR 端到端测试（⭐ 主力）
├── audio_output/               # test_stream_e2e.py 输出目录（按时间戳分子目录，已 gitignore）
├── asr_server/
│   ├── app.py                  # FastAPI ASR 服务（流式 WS + 兼容 HTTP）
│   ├── requirements.txt
│   ├── scripts/
│   │   └── test_stream.py      # 流式 ASR 底座联调脚本
│   └── models/                 # ASR 模型目录（已 gitignore）
│       ├── paraformer-zh-streaming/   # 流式 ASR
│       └── fsmn-vad/                  # 端点检测 VAD
└── tts_server/
    ├── app.py                  # FastAPI TTS 服务（Piper huayan-medium）
    ├── requirements.txt
    └── models/                 # TTS 模型目录（已 gitignore）
```

---

## 安装步骤（Windows）

> 以下命令均在 `model/` 目录下，使用 PowerShell 执行。

### 1. 进入 model 目录

```powershell
cd model
```

### 2. 下载推理依赖包到 `gguf_pkg/`

```powershell
python setup_gguf.py
```

### 3. 安装服务运行依赖

```powershell
# ASR 依赖（FunASR + Torch，体积较大）
pip install -r asr_server/requirements.txt

# TTS 依赖（Piper，体积小）
pip install -r tts_server/requirements.txt
```

### 4. 下载 ASR / TTS 模型

```powershell
# 一次下完 ASR(流式 + VAD) + TTS（默认走 hf-mirror 镜像）
python download_model.py

# 也可以只下某一类
python download_model.py --only asr
python download_model.py --only tts

# 可选：额外下载非流式 paraformer-zh，用于 HTTP /transcribe 调试
python download_model.py --with-offline-asr
```

下载产物分别落地到：

- ASR 流式：`asr_server/models/paraformer-zh-streaming/`
- ASR VAD ：`asr_server/models/fsmn-vad/`
- ASR 非流式（可选）：`asr_server/models/paraformer-zh/`
- TTS：`tts_server/models/zh/zh_CN/huayan/medium/zh_CN-huayan-medium.onnx(.json)`

---

## 启动 ASR / TTS 服务（Windows）

一条命令同时拉起两个服务，分别监听 `:9100` 和 `:9200`。

```powershell
.\start_all.ps1
```

> 会在两个新 PowerShell 窗口里分别跑 ASR / TTS，关闭窗口即停止对应服务。

端口可通过环境变量覆盖：

```powershell
$env:ASR_PORT=9101; $env:TTS_PORT=9201; .\start_all.ps1
```

启动成功后可做健康检查：

```powershell
curl http://127.0.0.1:9100/v1/health
curl http://127.0.0.1:9200/v1/health
```

---

## 对外接口

### ASR：`WS  ws://127.0.0.1:9100/v1/asr/stream` ⭐ 主接口（流式）

**协议**

| 方向 | 帧类型 | 内容 |
|---|---|---|
| C → S | `text` | `{"type":"start","sample_rate":16000,"format":"pcm_s16le","channels":1}` |
| C → S | `binary` | 16-bit little-endian PCM 原始字节，单声道 16kHz；建议每 ~600ms 发一包 |
| C → S | `text` | `{"type":"end"}`（音频结束，要求服务端出最终结果） |
| C → S | `text` | `{"type":"ping"}`（保活） |
| S → C | `text` | `{"type":"ready"}`（模型就绪，可以开始送音频）|
| S → C | `text` | `{"type":"partial","text":"..."}`（中间结果，会反复修正）|
| S → C | `text` | `{"type":"final","text":"..."}`（一段话识别完成；VAD 端点 / end）|
| S → C | `text` | `{"type":"eos"}`（end 后服务端处理完毕，连接将关闭）|
| S → C | `text` | `{"type":"error","message":"..."}`（错误，连接随后关闭）|
| S → C | `text` | `{"type":"pong"}`（保活回包）|

**性能预期**（CPU，paraformer-zh-streaming）

| 指标 | 值 |
|---|---|
| 单块解码窗口 | ~600 ms（`chunk_size=[0,10,5]`） |
| 中间结果延迟 | 400~800 ms |
| 最终结果延迟（用户停说后） | 200~700 ms |
| 端到端 P95（5 s 语音） | ≤ 1.2 s |

### ASR：`POST http://127.0.0.1:9100/v1/asr/transcribe` （兼容 / 调试）

- 请求：`multipart/form-data`，字段 `audio`（音频文件，支持 wav/mp3/m4a/webm 等）
- 响应：`{ "text": "把客厅灯打开", "duration_ms": 1840 }`
- 用途：冒烟测试、curl 调试。**不建议生产链路使用**。

curl 示例：

```powershell
curl -X POST -F "audio=@./test.wav" http://127.0.0.1:9100/v1/asr/transcribe
```

### TTS：`POST http://127.0.0.1:9200/v1/tts/synthesize`

- 请求：`application/json`

```json
{ "text": "好的，已为您打开客厅灯", "voice": "zh_CN-huayan-medium", "format": "wav" }
```

- 响应：二进制 `audio/wav` 流，可直接落盘或转发给客户端播放。

curl 示例：

```powershell
curl -X POST -H "Content-Type: application/json" `
     -d '{"text":"你好"}' `
     http://127.0.0.1:9200/v1/tts/synthesize `
     --output reply.wav
```

---

## Go Worker 调用片段

### ASR：透传客户端 WS 到 ASR WS（推荐路径）

完整骨架见 [`worker/README.md`](../worker/README.md) 的"核心"章节。核心思路：

```go
// 家具端 WS  <----->  Go Server WS  <----->  Go Worker WS  <----->  ASR WS（本机）
//          双向 goroutine 透传，Go Worker 顺手把 final 文本喂给 LLM（含 Tool Call）
```

### TTS：HTTP 文本 → 音频字节

```go
func CallTTS(text string) ([]byte, error) {
    payload, _ := json.Marshal(map[string]string{"text": text})
    resp, err := http.Post(
        "http://127.0.0.1:9200/v1/tts/synthesize",
        "application/json", bytes.NewReader(payload),
    )
    if err != nil { return nil, err }
    defer resp.Body.Close()
    return io.ReadAll(resp.Body)
}
```

### ASR：HTTP 整段（仅冒烟测试）

```go
func CallASRTranscribe(audio []byte, filename string) (string, error) {
    body := &bytes.Buffer{}
    w := multipart.NewWriter(body)
    fw, _ := w.CreateFormFile("audio", filename)
    fw.Write(audio)
    w.Close()

    req, _ := http.NewRequest("POST", "http://127.0.0.1:9100/v1/asr/transcribe", body)
    req.Header.Set("Content-Type", w.FormDataContentType())
    resp, err := http.DefaultClient.Do(req)
    if err != nil { return "", err }
    defer resp.Body.Close()

    var r struct{ Text string `json:"text"` }
    json.NewDecoder(resp.Body).Decode(&r)
    return r.Text, nil
}
```

---

## 环境变量

| 变量 | 默认 | 说明 |
|---|---|---|
| `ASR_PORT` | `9100` | ASR 监听端口 |
| `ASR_MODEL_DIR` | `./asr_server/models/paraformer-zh-streaming` | 流式 ASR 模型目录 |
| `ASR_VAD_MODEL_DIR` | `./asr_server/models/fsmn-vad` | VAD 模型目录（缺失时自动降级，仅依赖客户端 `end` 信号断句）|
| `ASR_OFFLINE_MODEL_DIR` | 同上 / `paraformer-zh` 优先 | HTTP 整段识别用模型 |
| `ASR_DEVICE` | `cuda`（由 `start_all.ps1` 注入，可覆盖为 `cpu`）| 运行设备；独立运行 `app.py` 时默认 `cpu` |
| `TTS_PORT` | `9200` | TTS 监听端口 |
| `TTS_DEVICE` | `cpu` | Piper 使用 CPU 足够超实时，不建议占用显存 |

---

## 联调脚本

### 1. `asr_server/scripts/test_stream.py`：流式 ASR 底座验证

**模拟家具端**行为，把 wav 文件按 600ms 切帧通过 WebSocket 送入 ASR（或 Go Server 透传端点），打印 `partial / final` 识别结果。不需要真实家具端硬件即可验证整条链路。

```powershell
# 依赖
pip install websockets soundfile numpy

# 直连 ASR 验证模型层
python asr_server/scripts/test_stream.py --url ws://127.0.0.1:9100/v1/asr/stream --wav test.wav

# 经 Go Server 验证透传层
python asr_server/scripts/test_stream.py --url "ws://127.0.0.1:8080/v1/voice?device_id=dev1&token=t1" --wav test.wav
```

详细参数见脚本顶部 docstring。

### 2. `test_stream_e2e.py`：TTS → 流式 ASR 端到端 ⭐

**不落盘中间文件**，把 TTS 合成的音频直接切帧喂给流式 ASR，打印每条 partial、final、首个 partial 延迟、总耗时。用来：

- 验证流式链路（partial 行数 > 1 才算真流式）
- 批量刷识别准确率
- 量化"首字延迟 / 总延迟"看 GPU 是否吃满

默认会把 TTS 产物按时间戳存到 `model/audio_output/<timestamp>/`，文件名形如 `01_把客厅的灯打开.wav`，方便事后听感回归。

```powershell
# 依赖
pip install requests websockets soundfile numpy

# 单条（默认文本 "把客厅的灯打开"）
python test_stream_e2e.py

# 指定文本
python test_stream_e2e.py --text "把空调调到26度"

# 批量跑预置 6 条指令，输出 PASS/DIFF 汇总
python test_stream_e2e.py --batch

# 按真实节奏发帧（每帧间 sleep 600ms），测真实首字延迟
python test_stream_e2e.py --batch --realtime

# 自定义保存目录
python test_stream_e2e.py --batch --save-dir D:\tts_samples

# 不保存 wav（纯压测时省磁盘）
python test_stream_e2e.py --batch --no-save
```

**输出示例**：

```
>>> [把客厅的灯打开]
  [TTS] 45210 bytes, 350 ms
  [save] ...\audio_output\20260509_201530\01_把客厅的灯打开.wav
  [pcm] 50000 bytes ≈ 1562 ms
  [partial] 把
  [partial] 把客厅
  [partial] 把客厅的灯打开
  [final]   把客厅的灯打开
  [timing]  first_partial=180 ms, total=850 ms
  [verdict] PASS

=== Summary ===
PASS: 6/6
  [PASS] '把客厅的灯打开'           first_partial=180ms  total=850ms  got='把客厅的灯打开'
  ...

[wav] 保存目录：...\audio_output\20260509_201530
[wav] 播放全部：Get-ChildItem '...' *.wav | ForEach-Object { Start-Process $_.FullName; Start-Sleep 2 }
[wav] 打开目录：explorer '...'
```

**关键指标预期**（ASR 走 `cuda`，Piper CPU）：

| 指标 | 健康值 | 异常诊断 |
|---|---|---|
| partial 行数 | 2~5 行 | 0 行说明流式没生效；1 行说明帧切太大 |
| first_partial | 150~400 ms | >1 s → GPU 没用上或 chunk_stride 偏大 |
| total（非 `--realtime`） | 500~1500 ms | 首次会略慢；第二次起 >3 s 需排查 |
| PASS 率 | 批量 ≥ 5/6 | 偶发 DIFF 多为 TTS 发音差异，非 ASR bug |

---

## 后续工作（TODO）

- [x] 流式 ASR WebSocket 接口（主接口）与联调脚本
- [x] Go Worker 完成 `/v1/orchestrate` WS 透传层（见 [`worker/README.md`](../worker/README.md)）
- [x] Go Worker 接入云端 LLM + Function Calling（config.yaml + `$env:LLM_API_KEY`）
- [x] Go Worker 接入 TTS（异步合成 → `tts_audio` 下发家具端播放）
- [x] Go Server 接入 MQTT 设备控制（cmd 下发 + status / heartbeat / result 订阅）
- [ ] ASR / TTS 接入 Go Server 的健康巡检（`/v1/health`）
- [x] 家具端协议落地（见 [`furniture/README.md`](../furniture/README.md)）

> 详细架构与契约见 [`docs/03-软件设计文档.md`](../docs/03-软件设计文档.md) §3.3 与 §4.6。
