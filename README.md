# 智能家居语音交互助手系统

> 《软件工程》课程实践项目 · 2026 春季学期
> 仓库地址：<https://github.com/Yoruko666/System>

## 项目简介

本项目为一套面向全屋智能场景的语音交互系统，由 **家具端、客户端、服务器、Worker、模型服务** 五部分组成。家具助手名为 **小菲**——一台带麦克风与扬声器的智能家居设备。用户可通过 Android 客户端以文本方式、或对着"小菲"以语音方式与大模型交互，系统自动将自然语言指令解析为对家居设备的控制命令，由服务器分发到家具端执行并反馈状态。

整体形态对齐主流智能音箱的端云分工（端侧 KWS+VAD，云端 ASR/LLM/TTS）：

- **家具端（小菲） = 本地硬件**：跑在音箱 / PC 模拟器上，负责 **录音 → KWS 唤醒词 → VAD 端点检测 → WS 上行 → 播放 TTS**。**断句在端侧做**，不靠云端。
- **Go Server = 中枢 / 信息接收与转发 + 设备控制执行**：接受家具端 / 客户端的接入，做 JWT 鉴权、用户/设备 CRUD、对话历史落库（MySQL + Redis）、MQTT 设备控制；**不直接对接 AI**，把语音 WS 整段转发给 Worker；同时拦截 Worker 的 `device_command` 事件，执行设备控制（更新 DB + 转发控制指令），将结果回传 Worker 供 LLM 生成最终回复。
- **Go Worker = 大模型编排 + Tool Call**：独占 ASR / LLM / TTS 调度，并支持 **OpenAI Function Calling**。一条 WS 长连接里承载 N 轮对话；家具端每个 `start / [PCM...] / end` 由 Server 原样转发到 Worker，Worker 转 ASR、`asr_final` 触发 LLM；当 LLM 返回 `tool_calls`（如 `control_device`）时，Worker 通过 WS 下发 `device_command` 给 Server 执行，收到执行结果后二次调用 LLM 生成自然语言回复，最终回传 `llm_result`。
- **模型服务**：仅提供本地 ASR（FunASR paraformer-zh-streaming）和 TTS（Piper）推理能力，由 Worker 按需调用。

```
                ┌──────────────────────────────┐        ┌─────────────────────────────────┐
                │   Go Server :8080（中枢）     │        │   Go Worker :8090（AI 编排）     │
                │       (server/)              │        │        (worker/)                │
                │                              │        │                                 │
 ┌────────────┐ │ ┌──────┐ ┌──────────────┐    │  WS    │ ┌─────────────┐  ┌────────────┐ │
 │ Android    │ │ │ REST │ │ /v1/voice    │────┼────────┼▶│/v1/orchestr.│─▶│ ASR :9100  │ │
 │ 客户端     │◀┼─│ /WS  │ │ 家具入口+鉴权 │◀───┼────────┼─│ 大模型工作流 │  └────────────┘ │
 │ (client/)  │ │ └──────┘ │ 转发 / 落库   │    │        │ │             │  ┌────────────┐ │
 └────────────┘ │          └──────────────┘    │        │ │             │─▶│ 云端 LLM   │ │
                │                              │        │ │             │  └────────────┘ │
                │ ┌──────────────┐             │        │ │             │  ┌────────────┐ │
                │ │ MQTT Bridge  │─▶ EMQX (M3) │        │ │             │─▶│ TTS :9200  │ │
                │ └──────────────┘             │        │ └─────────────┘  └────────────┘ │
                │ ┌──────────────┐             │        │                                 │
                │ │ MySQL/Redis  │ (已接入)     │        │                                 │
                │ └──────────────┘             │        │                                 │
                └──────────────┬───────────────┘        └─────────────────────────────────┘
                               │ WS /v1/voice  （音频上行 + asr_*/llm_* 下行）
                               ▼
                  ┌──────────────────────────────┐
                  │      家具端（小菲）           │
                  │      (furniture/)            │
                  │ 麦克风 → KWS → VAD → WS 上行  │
                  │ VAD 发 end → 暂停 → 收回复 →  │
                  │ 恢复；扬声器(TTS)/MQTT 控制   │
                  └──────────────────────────────┘
```

### 端侧（小菲）的职责链

家具端内部严格按下列流水线工作，**和主流商用智能音箱完全一致**：

```
[麦克风/虚拟音频流] → [KWS 唤醒词检测] → [VAD 端点检测] → [WS 上行 PCM] → [接收 asr_partial/asr_final] → [接收 llm_result] → [恢复 VAD]
                         ↑ 抽象层               ↑ webrtcvad
                         M2 默认 AlwaysOn      （端侧自动断句，不依赖云端 VAD）
                         M3 可切 openWakeWord /
                             Porcupine
```

核心设计：

- **WS 长连接复用**：一次握手、多轮对话。家具端在同一条 `/v1/voice` 上按需 `start → PCM → end → start → PCM → end → ...`，每个 `end` 触发一次 `asr_final`。
- **断句归端侧**：`webrtcvad` 监听持续静音 ≥ 800ms 自动发 `end`，不靠 ASR 去切句，避免云端延迟放大。
- **KWS 抽象**：`WakeWord` 基类预留接口；M2 用 `AlwaysOnWakeWord` 默认一直活跃，M3 替换为 openWakeWord / Porcupine 即可获得"嗨家具"式唤醒，**无需动主流程**。

### 一次"打开客厅灯"的完整链路（v0.5 Tool Call）

1. 家具端：KWS 判定处于活跃会话窗口 → VAD 检测到用户开始说话 → 向 Server 发 `start` + 16k PCM 帧（600ms / 包）；
2. Server 校验 `device_id / token` 后，把帧**整段透传**给 Worker 的 `ws://127.0.0.1:8090/v1/orchestrate`；Worker 再透传给 ASR `ws://127.0.0.1:9100/v1/asr/stream`；ASR 的 `partial` → Worker 改名为 `asr_partial` → Server 再透传 → 家具端；
3. 用户说完停顿 ≥ 800ms，家具端 VAD 自动发 `end` → **家具端暂停语音检测** → ASR 回 `final`（Worker 改名 `asr_final`）+ `eos`，Server 透传给家具端；
4. Worker 拿到 `asr_final` 文本后，异步调用云端 LLM API（**携带设备上下文 + Tool Schema**），若 LLM 判断需要控制设备则返回 `tool_calls`（如 `control_device`）；
5. Worker 将 `tool_calls` 封装为 `device_command` 事件，通过 WS 发送给 Server；
6. Server 拦截 `device_command`，调用 DeviceService 更新数据库设备状态 → 回复 `device_command_result`（成功/失败）给 Worker；
7. Worker 将 tool 执行结果回传 LLM 进行**二次调用**，LLM 生成自然语言回复（如"好的，已为您打开客厅灯"），通过 `llm_result` 推回 Server，Server 透传给家具端；同时 Worker 异步调用 TTS 将文本合成为 wav，下发 `tts_audio`（base64 wav）给家具端播放；
8. 家具端收到 `llm_result` / `tts_audio` 后打印回答 / 播放语音，**恢复语音检测**，等待下一轮对话。

## 技术栈

| 模块 | 技术选型 | 与外部的关系 |
|---|---|---|
| 家具端 `furniture/` | PC 端：Python + `webrtcvad`（默认）；硬件可选 ESP32 / 树莓派；唤醒词可选 openWakeWord / Porcupine | 音频走 WebSocket 到 server；设备控制走 MQTT 到 server。**不直连 worker / model** |
| 客户端 `client/` | Android（Kotlin + Jetpack Compose） | HTTP / WebSocket 到 server；只对 server 一个端点 |
| 服务器 `server/` | Go 1.22 标准库 `net/http` + `gorilla/websocket` + `go-sql-driver/mysql` + `go-redis/v9` + `golang-jwt/v5` + `golang.org/x/crypto` | 中枢：接入家具端 / 客户端，转发到 worker，落库与 MQTT 设备控制 |
| Worker `worker/` | Go 1.22 + `gorilla/websocket` + `gopkg.in/yaml.v3` | 大模型编排：对接 ASR / LLM / TTS，**支持 OpenAI Function Calling（Tool Call）**，被 server 通过 WS 调用 |
| 模型服务 `model/` | Python（FastAPI） | 仅提供模型推理服务，不参与业务逻辑：<br>• **流式 ASR**（FunASR paraformer-zh-streaming，`:9100`，WS `/v1/asr/stream`）<br>• **本地 TTS**（Piper huayan-medium，`:9200`，REST `/v1/tts/synthesize`）<br>由 worker 调用 |
| 数据库 | MySQL 8 + Redis 7（已接入） | 由 server 持有连接，客户端不直接访问 |
| 消息中间件 | MQTT broker（Mosquitto / EMQX，外部部署） | server 既是 publisher（下发控制）也是 subscriber（接收设备状态） |
| 版本管理 | Git + GitHub | 仓库：<https://github.com/Yoruko666/System> |

## 目录结构

```
System/
├── README.md                                       # 本文件，项目总览
├── 《软件工程》课程实践考核要求说明.pdf                # 课程官方要求
├── .gitignore                                      # 忽略模型权重 / wheel / 构建产物 / 生成音频
├── .gitattributes                                  # 跨平台换行符统一配置
│
├── docs/                                           # 项目文档
│   ├── 01-项目策划文档.md                           # 对应考核 2.4
│   ├── 02-需求分析文档.md                           # 对应考核 2.1
│   └── 03-软件设计文档.md                           # 对应考核 2.2
│
├── client/                                         # Android 客户端（Kotlin + Compose）
│   ├── README.md
│   ├── settings.gradle.kts                         # Gradle 项目设置
│   ├── build.gradle.kts                            # 根构建脚本
│   ├── gradle.properties                           # Gradle 属性
│   ├── gradle/wrapper/gradle-wrapper.properties    # Gradle 8.7
│   └── app/                                        # 📱 主应用模块
│       ├── build.gradle.kts                        # 构建配置（Compose + OkHttp）
│       ├── proguard-rules.pro
│       └── src/main/
│           ├── AndroidManifest.xml
│           ├── res/values/{strings,themes}.xml
│           └── java/com/shva/client/
│               ├── ShvaApplication.kt              # Application 入口
│               ├── MainActivity.kt                 # 主界面（消息列表）
│               ├── ui/theme/Theme.kt               # Material 3 主题
│               └── websocket/ServerWebSocket.kt    # WS 连接管理（自动重连）
│
├── server/                                         # Go 服务器（中枢：家具/客户端入口、转发到 worker、MQTT/CRUD）
│   ├── README.md
│   ├── go.mod / go.sum
│   ├── config.yaml                                 # Worker + MySQL + Redis + JWT 配置
│   ├── migrations/                                  # 数据库迁移脚本
│   │   ├── 001_init.sql                            # 建库建表（7 张表）
│   │   └── 002_seed.sql                            # 测试种子数据
│   ├── cmd/server/main.go                          # 入口：加载配置 + 路由注册 + 优雅退出
│   └── internal/
│       ├── config/config.go                         # AppConfig / MySQLConfig / RedisConfig / JWTConfig
│       ├── model/model.go                           # 数据模型（User/Device/DeviceState/...）
│       ├── database/
│       │   ├── mysql.go                             # MySQL 连接初始化 + 健康检查
│       │   └── redis.go                            # Redis 连接初始化 + 健康检查
│       ├── repository/
│       │   ├── user.go                             # 用户 CRUD
│       │   ├── device.go                           # 设备 CRUD
│       │   ├── device_state.go                     # 设备状态 Upsert
│       │   └── conversation.go                     # 对话/消息/指令/场景 CRUD
│       ├── service/
│       │   ├── user.go                             # 注册/登录/密码校验（bcrypt）
│       │   └── device.go                          # 设备创建/状态更新/权限校验
│       ├── middleware/jwt.go                       # JWT 生成/解析/黑名单/RequireAuth
│       └── handler/
│           ├── config_compat.go                    # LoadConfig/DefaultConfig 向后兼容
│           ├── health.go                           # /v1/health（worker/db/redis 状态）
│           ├── voice.go                            # /v1/voice（家具 ↔ worker 透传 + device_command 拦截 + 设备上下文推送）
│           ├── auth.go                             # /api/v1/auth/register + /login
│           └── device.go                           # /api/v1/devices/* 设备与状态 API
│
├── worker/                                         # Go Worker（大模型编排 + Tool Call：ASR / LLM / TTS）
│   ├── README.md
│   ├── go.mod / go.sum
│   ├── config.yaml                                 # ASR WS / LLM API / TTS HTTP 配置
│   ├── cmd/worker/main.go                          # 入口：监听 :8090
│   └── internal/handler/
│       ├── config.go                               # ASR/LLM/TTS 三段配置
│       ├── health.go                               # /v1/health
│       ├── orchestrate.go                          # /v1/orchestrate（核心 AI 工作流 + Tool Call）
│       └── tools.go                                # Tool Schema 定义 + 设备上下文 + LLM 响应解析
│
├── furniture/                                      # 家具端"小菲"（KWS + VAD + WS 长连接）
│   ├── README.md                                   # 家具 ↔ server WS 协议契约
│   ├── requirements.txt                            # websockets / webrtcvad / numpy
│   └── mock_furniture.py                           # PC 端模拟器：VAD 断句 + LLM 多轮对话
│
└── model/                                          # 模型服务（ASR + TTS 推理）
    ├── README.md
    ├── setup_gguf.py                               # 一键下载推理依赖
    ├── download_model.py                           # 一键下载 ASR / TTS 模型
    ├── start_all.ps1                               # 一键启动 ASR (:9100) + TTS (:9200)
    ├── test_stream_e2e.py                          # TTS → 流式 ASR 端到端联调
    ├── asr_server/                                 # FunASR ASR 服务（流式 WS + HTTP）
    │   ├── app.py
    │   ├── requirements.txt
    │   └── scripts/test_stream.py
    └── tts_server/                                 # Piper TTS 服务（HTTP）
        ├── app.py
        └── requirements.txt
```

> 模型权重（`*.onnx` / `*.gguf` / `*.pt` 等）、wheel 包、生成音频已通过 `.gitignore` 排除，**不入库**。

## 快速开始

### 0. 首次启动 — 安装依赖与下载模型

```powershell
# Python 依赖（ASR + TTS + 家具端）
pip install funasr
pip install -r model\asr_server\requirements.txt
pip install -r model\tts_server\requirements.txt
pip install -r furniture\requirements.txt

# 下载 ASR / TTS 模型权重
cd model
python setup_gguf.py
python download_model.py
cd ..

# Go（安装后即可，无需额外包管理）
# 下载：https://go.dev/dl/
```

### 1. 一键启动所有服务

在项目根目录执行（会自动打开 4 个新窗口：ASR / TTS / Worker / Server）：

```powershell
# 编辑 worker/config.yaml 填入大模型 URL，或通过环境变量设置 API Key
$env:LLM_API_KEY = "sk-你的key" #单次设置
[Environment]::SetEnvironmentVariable("LLM_API_KEY", "sk-你的key", "User") #永久设置
.\start_all.ps1
```

等 4 个窗口中日志都稳定后，即可进行测试。关闭对应窗口即停止服务。

### 2. 分步启动（或自定义配置）

#### 2.1 启动模型服务（ASR + TTS）

详见 [`model/README.md`](./model/README.md)：

```powershell
cd model
.\start_all.ps1          # 启动 ASR(:9100) + TTS(:9200)
```

#### 2.2 配置大模型并启动 Worker

```powershell
cd worker
# 编辑 config.yaml 填入大模型 URL，或通过环境变量设置 API Key
$env:LLM_API_KEY = "sk-你的key"
go run ./cmd/worker      # 监听 :8090，对接 ws://127.0.0.1:9100/v1/asr/stream
```

健康检查：

```powershell
curl http://127.0.0.1:8090/v1/health
```

#### 2.3 启动 Go 服务器

```powershell
cd server

# 初始化数据库（首次运行）
# 1. 先启动 MySQL，然后执行迁移脚本：
Get-Content migrations/001_init.sql | mysql -u root -p   # 建库建表
Get-Content migrations/002_seed.sql | mysql -u root -p   # 可选：插入测试数据

# 2. 修改 config.yaml 中的 mysql.password 和 jwt.secret

# 3. 启动 Server
$env:WORKER_WS_URL = "ws://127.0.0.1:8090/v1/orchestrate"   # 可选，默认即此
go run ./cmd/server      # 监听 :8080，转发到 worker
```

健康检查：

```powershell
curl http://127.0.0.1:8080/v1/health
```

也可不配大模型（worker 自动降级为纯 ASR 透传），仅测试 ASR 链路的启动方式不变。

### 3. WS 协议消息类型一览

#### 家具端 ↔ Server ↔ Worker 通用消息

| 方向 | 类型 | 说明 |
|---|---|---|
| 家具→Server | `start` / binary / `end` / `ping` | 音频上行 |
| Server→家具 | `pong` / `asr_partial` / `asr_final` / `eos` / `llm_result` / `tts_audio` / `llm_error` / `error` | 识别结果、文本回复、TTS 语音回复 |

#### Tool Call 扩展消息（v0.5 新增）

| 方向 | 类型 | 说明 |
|---|---|---|
| Server→Worker | `device_info` | 会话开始时推送用户设备列表+状态（注入 LLM 上下文） |
| Worker→Server | `device_command` | LLM 返回 tool_calls 时下发设备控制指令 |
| Server→Worker | `device_command_result` | Server 执行设备控制后回传结果（成功/失败+消息） |

### 4. REST API 端点一览

| 方法 | 路径 | 鉴权 | 说明 |
|---|---|---|---|
| `POST` | `/api/v1/auth/register` | 否 | 用户注册，返回 JWT |
| `POST` | `/api/v1/auth/login` | 否 | 用户登录，返回 JWT |
| `GET` | `/api/v1/devices` | JWT | 获取当前用户所有设备 |
| `GET` | `/api/v1/devices/states` | JWT | 获取当前用户所有设备状态 |
| `PUT` | `/api/v1/devices/state` | JWT | 更新设备状态（power/温度/亮度/开合度） |
| `GET` | `/api/v1/devices/{id}/state` | JWT | 获取单个设备状态 |

> 鉴权方式：在请求头添加 `Authorization: Bearer <jwt_token>`
>
> **WS `/v1/voice` 鉴权**：必须传 `?device_id=...&token=...`，server 会查 `devices` 表校验 token 是否匹配；
> 默认要求设备已存在且 `status != unregistered`。开发期可设环境变量 `SHVA_VOICE_AUTH=off` 关闭校验（启动时会打 WARN）。
> 演示账号：`device_id=dev1, token=t1`（属于用户 `13800000001`）。

### 4. 家具端测试（实时麦克风，含 LLM 多轮对话 + Tool Call）

```powershell
# 实时麦克风：说话 → VAD 断句 → ASR 识别 → LLM 回复 → 恢复检测
python furniture/mock_furniture.py --server "ws://127.0.0.1:8080/v1/voice?device_id=dev1&token=t1"
```

### 5. 编译运行 Android 客户端

**前置条件**：安装 [Android Studio](https://developer.android.com/studio)（Hedgehog 2023.1+），模拟器推荐 `Pixel 6 API 33+`。

**打开项目**：用 Android Studio 打开 `client/` 目录，首次打开自动下载 Gradle 8.7 + 依赖（需联网）。

**编译 APK**：

```powershell
cd client
./gradlew assembleDebug
```

APK 输出：`app/build/outputs/apk/debug/app-debug.apk`

**安装到模拟器 / 真机**：

```powershell
# 确保模拟器已启动或真机已 USB 连接
./gradlew installDebug
```

连接后顶部状态栏显示 **🟢 已连接**，底部列表中会显示服务端推送的消息。

**服务端地址配置**：编辑 `client/app/build.gradle.kts`，修改 `SERVER_HOST` 和 `SERVER_PORT` 字段后重新编译：

| 运行方式 | `SERVER_HOST` 值 |
|---|---|
| Android 模拟器 → 本机 | `10.0.2.2`（默认，自动映射宿主机 localhost） |
| 真机 → 局域网服务器 | 填写局域网 IP，如 `"192.168.1.100"` |

更多细节详见 [`client/README.md`](./client/README.md)。

## 文档索引

| 文档 | 内容 | 对应考核 |
|---|---|---|
| [`docs/01-项目策划文档.md`](./docs/01-项目策划文档.md) | 项目管理、人员分工、进度安排、风险管理 | 2.4 项目管理 |
| [`docs/02-需求分析文档.md`](./docs/02-需求分析文档.md) | 用例图、功能需求、非功能需求 | 2.1 需求分析 |
| [`docs/03-软件设计文档.md`](./docs/03-软件设计文档.md) | 体系结构、模块设计、接口契约 | 2.2 软件设计 |
| [`docs/04-测试计划与用例报告.md`](./docs/04-测试计划与用例报告.md) | 测试计划、用例库（76 条）、缺陷登记 | 2.3 系统测试 |

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
