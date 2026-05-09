# 智能家居语音交互助手系统

> 《软件工程》课程实践项目 · 2026 春季学期
> 仓库地址：<https://github.com/Yoruko666/System>

## 项目简介

本项目为一套面向全屋智能场景的语音交互系统，由 **家具端、客户端、服务器、大模型工作流** 四部分组成。用户通过 Android 客户端以语音或文本方式与大模型交互，系统自动将自然语言指令解析为对家居设备的控制命令，经服务器分发后由家具端执行并反馈状态。

```
       ┌──────────────┐     语音/文本     ┌────────────────────┐
       │  Android 客户端  │ ───────────────▶ │  Go 服务器（网关）   │
       │   (client/)   │ ◀─────────────── │     (server/)      │
       └──────────────┘    回包/状态推送    └─────────┬──────────┘
                                                    │ HTTP
                                          ┌─────────▼──────────┐
                                          │  Python 模型工作流   │
                                          │   (model/)         │
                                          │   ASR / LLM / TTS  │
                                          └─────────┬──────────┘
                                                    │ MQTT/WebSocket
                                          ┌─────────▼──────────┐
                                          │  家具端（模拟/硬件） │
                                          │   (furniture/)     │
                                          └────────────────────┘
```

## 技术栈

| 模块 | 技术选型 |
|---|---|
| 家具端 `furniture/` | 可选方案（ESP32 / 树莓派 / 纯软件模拟），通过 MQTT/WebSocket 接入服务器 |
| 客户端 `client/` | Android（Kotlin + Jetpack Compose） |
| 服务器 `server/` | Go（Gin + WebSocket + MQTT Broker） |
| 模型工作流 `model/` | Python（FastAPI），以 HTTP 串联三类能力：<br>• **本地 ASR 服务**（FunASR paraformer-zh，端口 9100）<br>• **本地 TTS 服务**（Piper huayan-medium，端口 9200）<br>• **云端 LLM API**（通义/豆包/DeepSeek 任选） |
| 数据库 | MySQL + Redis |
| 版本管理 | Git + GitHub |

## 目录结构

```
System/
├── README.md                                       # 本文件，项目总览
├── 《软件工程》课程实践考核要求说明.pdf                # 课程官方要求
├── .gitignore                                      # 忽略模型权重 / wheel / 构建产物
│
├── docs/                                           # 项目文档
│   ├── 01-项目策划文档.md                           # 对应考核 2.4
│   ├── 02-需求分析文档.md                           # 对应考核 2.1
│   └── 03-软件设计文档.md                           # 对应考核 2.2
│
├── client/                                         # Android 客户端（Kotlin）
├── server/                                         # Go 服务器（设备接入 / 指令分发 / 用户服务）
├── furniture/                                      # 家具端（模拟器或硬件代码）
│
└── model/                                          # 大模型工作流（Python）
    ├── README.md                                   # 模块详细说明
    ├── setup_gguf.py                               # 一键下载推理依赖到 gguf_pkg/
    ├── download_model.py                           # 一键下载 ASR / TTS 模型
    ├── start_all.ps1                               # 一键启动 ASR (:9100) + TTS (:9200)
    ├── test_e2e.py                                 # 端到端联调脚本
    ├── asr_server/                                 # FunASR ASR 服务
    └── tts_server/                                 # Piper TTS 服务
```

> 模型权重（`*.onnx` / `*.gguf` / `*.pt` 等）、wheel 包、生成音频已通过 `.gitignore` 排除，**不入库**。

## 快速开始

### 1. 克隆仓库

```bash
git clone https://github.com/Yoruko666/System.git
cd System
```

### 2. 启动模型工作流（语音能力）

详见 [`model/README.md`](./model/README.md)，简要：

```powershell
cd model
python setup_gguf.py                          # 下载推理依赖
pip install -r asr_server/requirements.txt
pip install -r tts_server/requirements.txt
python download_model.py                      # 下载 ASR / TTS 模型
.\start_all.ps1                               # 启动 ASR(:9100) + TTS(:9200)
```

### 3. 启动 Go 服务器

详见 [`server/README.md`](./server/README.md)（开发中）。

### 4. 运行 Android 客户端

详见 [`client/README.md`](./client/README.md)（开发中）。

### 5. 启动家具端模拟器

详见 [`furniture/README.md`](./furniture/README.md)（开发中）。

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
