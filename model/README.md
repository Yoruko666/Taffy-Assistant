# 语音模型工作流（Model 模块）

> 本模块对应设计文档 §3.3 中的 **M-AS / M-TS**，负责本地 ASR / TTS 模型的部署与推理。
> LLM 走云端 API，无需本地下载。

## 目录结构

```
model/
├── README.md                   # 本文件
├── setup_gguf.py               # 一键下载推理依赖包到 gguf_pkg/
├── download_model.py           # 一键下载 ASR / TTS 模型
├── gguf_pkg/                   # 离线依赖包仓库（pip download 产物，已 gitignore）
├── start_all.ps1               # 一键启动 ASR (:9100) + TTS (:9200)
├── asr_server/
│   ├── app.py                  # FastAPI ASR 服务（FunASR paraformer-zh）
│   ├── requirements.txt
│   └── models/                 # ASR 模型目录（已 gitignore）
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
# 一次下完 ASR + TTS（默认走 hf-mirror 镜像）
python download_model.py

# 也可以只下某一类
python download_model.py --only asr
python download_model.py --only tts
```

下载产物分别落地到：

- ASR：`asr_server/models/paraformer-zh/`
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

## 对外接口（供 Go Server / 工作流调用）

### ASR：`POST http://127.0.0.1:9100/v1/asr/transcribe`

- 请求：`multipart/form-data`，字段 `audio`（音频文件，支持 wav/mp3/m4a/webm 等）
- 响应：

```json
{ "text": "把客厅灯打开", "duration_ms": 1840 }
```

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

### Go Server 调用片段

```go
// ASR：上传音频 → 拿文本
func CallASR(audio []byte, filename string) (string, error) {
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

// TTS：文本 → 音频字节
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

---

## 后续工作（TODO）

- [ ] 工作流编排：`POST /v1/voice` → ASR → 云端 LLM → TTS → 回调 Go 服务器下发设备指令；
- [ ] ASR / TTS 接入 Go Server 的健康巡检（`/v1/health`）。

> 详细架构与契约见 [`docs/03-软件设计文档.md`](../docs/03-软件设计文档.md) §3.3 与 §4.5。
