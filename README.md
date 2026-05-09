# 智能家居语音交互助手系统

> 《软件工程》课程实践项目 · 2026 春季学期
> 仓库地址：<https://github.com/Yoruko666/System>

## 项目简介

本项目为一套面向全屋智能场景的语音交互系统，由 **家具端、客户端、服务器、大模型工作流** 四部分组成。家具助手名为 **小菲**——一台带麦克风与扬声器的智能家居设备。用户可通过 Android 客户端以文本方式、或对着"小菲"以语音方式与大模型交互，系统自动将自然语言指令解析为对家居设备的控制命令，由服务器分发到家具端执行并反馈状态。

整体形态对齐主流智能音箱的端云分工（端侧 KWS+VAD，云端 ASR/LLM/TTS）：

- **家具端（小菲） = 本地硬件**：跑在音箱 / PC 模拟器上，负责 **录音 → KWS 唤醒词 → VAD 端点检测 → WS 上行 → 播放 TTS**。**断句在端侧做**，不靠云端。
- **Go Server = 中枢**：一条 WS 长连接里承载 N 轮对话；家具端每个 `start / [PCM...] / end` 都原样转发给 ASR，ASR 的 `asr_partial / asr_final` 回推家具；`asr_final` 触发 LLM → MQTT 设备控制 → 本地 TTS 合成回包。
- **Python 模型工作流**：本地 ASR（FunASR paraformer-zh-streaming）+ 本地 TTS（Piper）+ 云端 LLM API。

```
   ┌──────────────────┐                              ┌────────────────────┐
   │  Android 客户端   │   HTTP / WebSocket           │   Go 服务器（中枢）  │   HTTP / WebSocket   ┌────────────────────────┐
   │   (client/)      │ ◀───────────────────────────▶│     (server/)      │ ◀──────────────────▶│  Python 模型工作流       │
   └──────────────────┘  登录/对话/状态/场景           └─────┬──────┬───────┘   ASR · LLM · TTS    │   (model/)              │
                                                            │      │                              │   ASR :9100             │
                                                            │      │                              │   TTS :9200             │
                                                  WebSocket │      │ MQTT                         │   云端 LLM API          │
                                                  音频上下行 │      │ 设备控制                      └────────────────────────┘
                                                            ▼      ▼
                                                  ┌────────────────────────┐
                                                  │  家具端（模拟器/硬件）    │
                                                  │   (furniture/)         │
                                                  │  麦克风 + KWS + VAD +   │
                                                  │  扬声器 + 设备          │
                                                  └────────────────────────┘
```

### 端侧（小菲）的职责链（重要）

家具端内部严格按下列流水线工作，**和主流商用智能音箱完全一致**：

```
[麦克风/虚拟音频流] → [KWS 唤醒词检测] → [VAD 端点检测] → [WS 上行 PCM] → [接收 asr_partial/asr_final] → [播放 tts_audio]
                         ↑ 抽象层               ↑ webrtcvad
                         M2 默认 AlwaysOn      （端侧自动断句，不依赖云端 VAD）
                         M3 可切 openWakeWord /
                             Porcupine
```

核心设计：

- **WS 长连接复用**：一次握手、多轮对话。家具端在同一条 `/v1/voice` 上按需 `start → PCM → end → start → PCM → end → ...`，每个 `end` 触发一次 `asr_final`。
- **断句归端侧**：`webrtcvad` 监听持续静音 ≥ 800ms 自动发 `end`，不靠 ASR 去切句，避免云端延迟放大。
- **KWS 抽象**：`WakeWord` 基类预留接口；M2 用 `AlwaysOnWakeWord` 默认一直活跃，M3 替换为 openWakeWord / Porcupine 即可获得"嗨家具"式唤醒，**无需动主流程**。
- **无真实麦克风也能演示**：`furniture/mock_furniture.py` 把若干 wav 文件按"多句连说"拼成虚拟麦克风流，PC 上直接跑通全链路。

### 一次"打开客厅灯"的完整链路

1. 家具端：KWS 判定处于活跃会话窗口 → VAD 检测到用户开始说话 → 向 Server 发 `start` + 16k PCM 帧（600ms / 包）；
2. Server 把 start / PCM 透传给 model 的 `ws://127.0.0.1:9100/v1/asr/stream`，ASR 的 `partial` 事件加 `asr_` 前缀回推家具；
3. 用户说完停顿 ≥ 800ms，家具端 VAD 自动发 `end` → ASR 回 `final`（Server 转成 `asr_final`）+ `eos`；
4. Server 拿到 `asr_final` 文本后调用云端 LLM，得到结构化指令与回复文案；
5. Server 调用 model 的 TTS（`http://127.0.0.1:9200/v1/tts/synthesize`），把 wav base64 后通过**同一条 WS** 推回家具端播放；
6. Server 同时通过 MQTT 把控制指令下发给"客厅灯"那台家具，并把执行结果通过 WebSocket 推送给客户端做状态展示；
7. 用户继续说下一句 → 家具端复用同一条 WS 再次 `start` → 进入下一轮对话。

## 技术栈

| 模块 | 技术选型 | 与外部的关系 |
|---|---|---|
| 家具端 `furniture/` | PC 端：Python + `webrtcvad`（默认）；硬件可选 ESP32 / 树莓派；唤醒词可选 openWakeWord / Porcupine | 音频走 WebSocket 到 server；设备控制走 MQTT 到 server。**不直连 model** |
| 客户端 `client/` | Android（Kotlin + Jetpack Compose） | HTTP / WebSocket 到 server；只对 server 一个端点 |
| 服务器 `server/` | Go 1.22 标准库 `net/http` + `gorilla/websocket`（后续按需接 MQTT 客户端、MySQL、Redis） | 中枢：聚合 model（ASR/TTS）、云端 LLM、MQTT broker、客户端、家具端 |
| 模型工作流 `model/` | Python（FastAPI） | 以本地 HTTP / WebSocket 暴露给 server：<br>• **流式 ASR**（FunASR paraformer-zh-streaming，`:9100`，WS `/v1/asr/stream` + REST `/v1/asr/transcribe`）<br>• **本地 TTS**（Piper huayan-medium，`:9200`，REST `/v1/tts/synthesize`）<br>• **云端 LLM API**（通义/豆包/DeepSeek 任选，由 server 直接调用） |
| 数据库 | MySQL + Redis（M3 接入） | 由 server 持有连接，客户端不直接访问 |
| 消息中间件 | MQTT broker（Mosquitto / EMQX，外部部署） | server 既是 publisher（下发控制）也是 subscriber（接收设备状态） |
| 版本管理 | Git + GitHub | 仓库：<https://github.com/Yoruko666/System> |

## 目录结构

```
System/
├── README.md                                       # 本文件，项目总览
├── 《软件工程》课程实践考核要求说明.pdf                # 课程官方要求
├── .gitignore                                      # 忽略模型权重 / wheel / 构建产物 / 生成音频
│
├── docs/                                           # 项目文档
│   ├── 01-项目策划文档.md                           # 对应考核 2.4
│   ├── 02-需求分析文档.md                           # 对应考核 2.1
│   └── 03-软件设计文档.md                           # 对应考核 2.2
│
├── client/                                         # Android 客户端（Kotlin）
│   └── README.md
│
├── server/                                         # Go 服务器（中枢：透传/LLM/TTS/MQTT/CRUD）
│   ├── README.md
│   ├── go.mod / go.sum
│   ├── cmd/server/main.go                          # 入口：路由注册 + 优雅退出
│   └── internal/handler/                           # HTTP / WebSocket 处理器
│       ├── health.go                               # /v1/health
│       └── voice.go                                # /v1/voice 家具端音频上下行（单 WS 多轮透传 ASR）
│
├── furniture/                                      # 家具端"小菲"（KWS + VAD + WS 长连接）
│   ├── README.md                                   # 家具 ↔ server WS 协议契约
│   ├── requirements.txt                            # websockets / webrtcvad / soundfile / numpy
│   └── mock_furniture.py                           # PC 端模拟器：wav 当虚拟麦克风，端侧自动断句
│
└── model/                                          # 大模型工作流（Python）
    ├── README.md                                   # 模块详细说明
    ├── setup_gguf.py                               # 一键下载推理依赖到 gguf_pkg/
    ├── download_model.py                           # 一键下载 ASR / TTS 模型
    ├── start_all.ps1                               # 一键启动 ASR (:9100) + TTS (:9200)
    ├── test_stream_e2e.py                          # TTS → 流式 ASR 端到端联调
    ├── asr_server/                                 # FunASR ASR 服务（流式 + 整段，支持单 WS 多轮）
    │   └── scripts/test_stream.py                  # 老版单段 PTT 调试脚本（仍可用）
    └── tts_server/                                 # Piper TTS 服务
```

> 模型权重（`*.onnx` / `*.gguf` / `*.pt` 等）、wheel 包、生成音频已通过 `.gitignore` 排除，**不入库**。

## 快速开始

### 1. 克隆仓库

```bash
git clone https://github.com/Yoruko666/System.git
cd System
```

### 2. 启动模型工作流（语音能力，**必须先起**）

详见 [`model/README.md`](./model/README.md)，简要：

```powershell
cd model
python setup_gguf.py                          # 下载推理依赖
pip install -r asr_server/requirements.txt
pip install -r tts_server/requirements.txt
python download_model.py                      # 下载 ASR / TTS 模型
.\start_all.ps1                               # 启动 ASR(:9100) + TTS(:9200)
```

### 3. 启动 Go 服务器（中枢，转发音频到 model）

详见 [`server/README.md`](./server/README.md)。最小启动：

```powershell
cd server
go run ./cmd/server                            # 默认监听 :8080，转发到 ws://127.0.0.1:9100/v1/asr/stream
# 或自定义：
# $env:PORT="8080"; $env:ASR_WS_URL="ws://127.0.0.1:9100/v1/asr/stream"; go run ./cmd/server
```

启动后健康检查：

```powershell
curl http://127.0.0.1:8080/v1/health
```

### 4. 联调家具端"小菲"音频链路

用 `furniture/mock_furniture.py` 扮演"带麦克风的小菲"，用 wav 文件作为虚拟麦克风。脚本内置 KWS 抽象层（M2 默认 AlwaysOn）+ webrtcvad 端点检测，能像真机一样自动断句、一条 WS 多句连说。

```powershell
# 安装家具端运行依赖
pip install -r furniture/requirements.txt

# 单句：经 server 透传到 ASR，期望打印 asr_partial / asr_final
python furniture/mock_furniture.py `
    --server ws://127.0.0.1:8080/v1/voice `
    --device-id dev1 --token t1 `
    --wav model\audio_output\<某次运行>\01_把客厅的灯打开.wav

# 多句连说：脚本把多个 wav 拼成虚拟麦克风流，每句之间塞 1.2s 静音
# VAD 会自动给每一句发 start/end，同一条 WS 拿到 N 个 asr_final
python furniture/mock_furniture.py `
    --server ws://127.0.0.1:8080/v1/voice `
    --wav a.wav b.wav c.wav --gap-ms 1500

# PTT 模式（整段一次性，兼容老脚本行为）
python furniture/mock_furniture.py --mode ptt --wav a.wav

# 直连 ASR 分段排障（跳过 server）
python furniture/mock_furniture.py `
    --server ws://127.0.0.1:9100/v1/asr/stream --wav a.wav
```

预期看到类似：

```
[mock-furniture] ▶ VAD: segment #1 START
   ↳ asr_partial: 把客厅
   ↳ asr_partial: 把客厅的灯
[mock-furniture] ■ VAD: segment #1 END (waiting asr_final…)
   ↳ asr_final: 把客厅的灯打开  ✅
[mock-furniture] ▶ VAD: segment #2 START
   ...
```

### 5. 运行 Android 客户端 / 家具端

详见 [`client/README.md`](./client/README.md)、[`furniture/README.md`](./furniture/README.md)（开发中）。

## 联调测试：四层验证法

家具端 ↔ Server ↔ Model 有多处可能出错（端口、采样率、VAD、透传），一层层往上打就能精准定位。按顺序跑，哪层红了就停在那层。

```
第 1 层  ASR 进程是否起来        → curl /v1/health
第 2 层  ASR 整段识别是否正确     → curl -F audio=@xxx.wav /v1/asr/transcribe
第 3 层  ASR 流式 WS 是否出字     → mock_furniture.py 直连 :9100
第 4 层  Server↔Model 透传是否通  → mock_furniture.py 走 :8080
```

### 准备：开 3 个 PowerShell 窗口

**窗口 A — ASR 模型服务**

```powershell
cd C:\Users\yorukoguan\Desktop\System\model
python -m uvicorn asr_server.app:app --host 0.0.0.0 --port 9100
```

等到日志出现 `ASR 流式模型加载完成` 和 `Uvicorn running on http://0.0.0.0:9100`。

**窗口 B — Go Server**

```powershell
cd C:\Users\yorukoguan\Desktop\System\server
$env:PORT="8080"
$env:ASR_WS_URL="ws://127.0.0.1:9100/v1/asr/stream"
go run ./cmd/server
```

**窗口 C — 测试客户端**（下面所有命令都在这里跑）

```powershell
cd C:\Users\yorukoguan\Desktop\System
pip install -r furniture/requirements.txt     # 首次；建议连带安装 scipy 以获得正确的重采样
```

### 第 1 层：ASR 健康检查

```powershell
curl http://127.0.0.1:9100/v1/health
```

期望 `stream_loaded: true`。挂了 → 看窗口 A，通常是模型没下载：`python model/download_model.py --only asr`。

### 第 2 层：ASR 整段识别（HTTP）

```powershell
$wav = "model\audio_output\20260509_203301\01_把客厅的灯打开.wav"
curl.exe -X POST -F "audio=@$wav" http://127.0.0.1:9100/v1/asr/transcribe
```

期望：`{"text":"把客厅的灯打开","duration_ms":...}`。这一层通说明模型本身没问题，往下一层走。

### 第 3 层：直连 ASR 的流式 WS（跳过 Server，只验模型侧）

```powershell
python furniture\mock_furniture.py --server ws://127.0.0.1:9100/v1/asr/stream --wav model\audio_output\20260509_203301\01_把客厅的灯打开.wav
```

期望：`ready` → 若干 `partial` → `final: 把客厅的灯打开` → `eos`。这层通了表示**模型流式链路 OK**，再挂就一定在 Go Server。

### 第 4 层：经 Server 透传（端到端）

**单句**：

```powershell
python furniture\mock_furniture.py --server ws://127.0.0.1:8080/v1/voice --device-id dev1 --token t1 --wav model\audio_output\20260509_203301\01_把客厅的灯打开.wav
```

期望三端日志：

- 窗口 C：`ready` → `asr_partial` × N → `asr_final` → `eos`
- 窗口 B：`client connected` → `asr connected asr=ws://127.0.0.1:9100/...` → `session done`
- 窗口 A：`ws connected` → `segment #1 done: text='把客厅的灯打开'` → `ws closed segments=1`

**多句连说**（验证单 WS 多轮对话 + VAD 自动断句）：

```powershell
python furniture\mock_furniture.py --server ws://127.0.0.1:8080/v1/voice --wav model\audio_output\20260509_203301\01_把客厅的灯打开.wav model\audio_output\20260509_203301\02_把空调调到26度.wav --gap-ms 1500
```

关键期望：

- 窗口 C 收到 **2 个** `asr_final`，顺序与 wav 对应；
- 窗口 A 有 `segment #1 done ...` 和 `segment #2 done ...`，**中间没有 ws closed**；
- 窗口 B 只有一次 `client connected` / `session done`。

**PTT 模式**（旁路 VAD，排查 VAD 灵敏度用）：

```powershell
python furniture\mock_furniture.py --mode ptt --server ws://127.0.0.1:8080/v1/voice --wav model\audio_output\20260509_203301\01_把客厅的灯打开.wav
```

### 快速故障定位表

| 症状 | 最可能原因 | 先看哪里 |
|---|---|---|
| 客户端收到 `{"type":"error","message":"asr backend unreachable"}` | ASR 没起 / `ASR_WS_URL` 写错 | 窗口 A 状态；`curl :9100/v1/health` |
| `ready` 收到但没有 `asr_partial` | 音频没送到 / 采样率不对 | 加 `--mode ptt`；确认 wav 是 16k mono |
| 识别出乱串（如 `马听德德灯打`） | 客户端重采样混叠（wav 不是 16k） | `pip install scipy`；或把 wav 转成 16k mono |
| 有 `asr_partial` 但没 `asr_final` | 客户端没发 `end` / VAD 静音阈值太大 | 调小 `--silence-ms 500` 或 `--mode ptt` |
| 多句只出 1 个 `asr_final` | 两句 gap 太短，VAD 没断开 | 加大 `--gap-ms 2000`、`--silence-ms 600` |
| 句首被吞（如 `把客厅的灯打开` → `客厅的灯打开`） | VAD 起点切早了 | 加大 `--lookback-ms 500`、`--min-speech-ms 60` |
| Server 日志 `asr read err` 并立刻结束 | ASR 崩溃 / OOM | 看窗口 A 的 Python 异常栈 |

### 采样率约束（重要）

ASR 只认 **16 kHz / 16-bit / 单声道 PCM**。`mock_furniture.py` 会把任意 wav 重采样到 16k：

- **强烈建议** `pip install scipy`：使用多相滤波（`resample_poly`），自带抗混叠低通；
- 也可以用 `pip install soxr`；
- 什么都不装时会回退线性插值，22.05k/44.1k → 16k 会严重混叠，表现为 ASR 输出中文乱串。

真实硬件（ESP32 / 树莓派）麦克风应直接按 16k 采样，**不要**让家具端做重采样。

## 文档索引

| 文档 | 内容 | 对应考核 |
|---|---|---|
| [`docs/01-项目策划文档.md`](./docs/01-项目策划文档.md) | 项目管理、人员分工、进度安排、风险管理 | 2.4 项目管理 |
| [`docs/02-需求分析文档.md`](./docs/02-需求分析文档.md) | 用例图、功能需求、非功能需求 | 2.1 需求分析 |
| [`docs/03-软件设计文档.md`](./docs/03-软件设计文档.md) | 体系结构、模块设计、接口契约 | 2.2 软件设计 |
| `docs/04-测试计划与用例报告.md` | 测试计划、用例、缺陷记录（待补） | 2.3 系统测试 |

## 小组成员

| 角色 | 姓名 | 主要职责 |
|---|---|---|
| 项目经理 / 队长 | 关梓浩 | 统筹管理、进度控制、风险管理、对外沟通 |
| 服务器开发 | 林帅 | Go 服务端（设备接入 / 指令分发 / 用户服务） |
| 客户端开发 | 马正朗 | Android 客户端 UI 与业务逻辑 |
| 大模型工作流 | 吴承凯 | Python 语音/LLM 工作流、Prompt 设计 |
| 家具端开发 | 刘智冲 | 家具端模拟或硬件端、协议实现 |
| 测试 / UI 设计 | 容嘉 | 测试计划、用例设计、界面原型 |

> 关梓浩除管理外也参与编码工作。详细分工见《项目策划文档》。

## 关键里程碑

| 阶段 | 时间 | 交付物 |
|---|---|---|
| 需求与设计 | 2026-05-08 ~ 05-10 | 需求分析文档、软件设计文档 |
| 编码实现 | 2026-05-10 ~ 06-21 | 各模块可运行代码 |
| 集成与测试 | 2026-06-22 ~ 07-05 | 测试报告、修复完成的系统 |
| 演示与交付 | 2026-07-06 ~ 07-10 | PPT、演示视频、最终交付包 |

## 协作约定

- 默认分支为 `main`，**不直接向 `main` 推代码**，统一走 Pull Request；
- 分支命名：`feature/<模块>-<简述>`、`fix/<简述>`、`docs/<简述>`；
- 提交信息建议遵循 Conventional Commits：`feat: 新增 ASR 接口`、`fix: 修正 TTS 端口`、`docs: 更新 README`；
- 模型权重、`*.whl`、构建产物、私密配置（`.env`）一律不入库，参见 `.gitignore`。
