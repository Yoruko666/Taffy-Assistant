# 智能家居语音交互助手系统

> 《软件工程》课程实践项目 · 2026 春季学期
> 仓库地址：<https://github.com/Yoruko666/System>

## 项目简介

本项目为一套面向全屋智能场景的语音交互系统，由 **家具端、客户端、服务器、大模型工作流** 四部分组成。用户通过 Android 客户端以文本方式、或通过家具端（带麦克风的智能音箱类设备）以语音方式与大模型交互，系统自动将自然语言指令解析为对家居设备的控制命令，由服务器分发到家具端执行并反馈状态。

**Go Server 是整套系统的中枢**：家具端的录音音频先发给 Server，Server 透传到 Python 模型工作流做流式 ASR / LLM / TTS，再把识别文本与 TTS 音频按帧推回家具端；家居设备控制则通过 MQTT 通道下发。客户端只与 Server 通信，不直接接触模型与家具。

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
                                                  │  麦克风 + 扬声器 + 设备  │
                                                  └────────────────────────┘
```

### 一次"打开客厅灯"的完整链路

1. 家具端按下唤醒键，开始录音 → 通过 `ws://server:8080/v1/voice` 把 16k PCM 帧发给 Server；
2. Server 把音频流透传给 model 的 `ws://127.0.0.1:9100/v1/asr/stream`，把 ASR 返回的 `partial`/`final` 加上 `asr_` 前缀回推家具；
3. Server 拿到 `asr_final` 文本后调用云端 LLM，得到结构化指令与回复文案；
4. Server 调用 model 的 TTS（`http://127.0.0.1:9200/v1/tts/synthesize`），把 wav base64 后通过同一条 WS 推回家具端播放；
5. Server 同时通过 MQTT 把控制指令下发给"客厅灯"那台家具，并把执行结果通过 WebSocket 推送给客户端做状态展示。

## 技术栈

| 模块 | 技术选型 | 与外部的关系 |
|---|---|---|
| 家具端 `furniture/` | 可选方案（ESP32 / 树莓派 / 纯软件模拟） | 音频走 WebSocket 到 server；设备控制走 MQTT 到 server。**不直连 model** |
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
│       └── voice.go                                # /v1/voice 家具端音频上下行（透传 ASR）
│
├── furniture/                                      # 家具端（模拟器或硬件代码）
│   └── README.md                                   # 家具 ↔ server WS 协议契约
│
└── model/                                          # 大模型工作流（Python）
    ├── README.md                                   # 模块详细说明
    ├── setup_gguf.py                               # 一键下载推理依赖到 gguf_pkg/
    ├── download_model.py                           # 一键下载 ASR / TTS 模型
    ├── start_all.ps1                               # 一键启动 ASR (:9100) + TTS (:9200)
    ├── test_stream_e2e.py                          # TTS → 流式 ASR 端到端联调
    ├── asr_server/                                 # FunASR ASR 服务（流式 + 整段）
    │   └── scripts/test_stream.py                  # 模拟家具端：直连 ASR / 经 server 透传都能跑
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

### 4. 联调家具端音频链路

可以直接复用 model 仓内的模拟家具端脚本，把目标 WS 指到 server，验证"家具 → server → model"全链路：

```powershell
cd model
python asr_server\scripts\test_stream.py `
    --url "ws://127.0.0.1:8080/v1/voice?device_id=dev1&token=t1" `
    --wav audio_output\<某次运行>\01_把客厅的灯打开.wav `
    --realtime
```

预期看到 `asr_partial` / `asr_final` 事件回流。

### 5. 运行 Android 客户端 / 家具端

详见 [`client/README.md`](./client/README.md)、[`furniture/README.md`](./furniture/README.md)（开发中）。

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
