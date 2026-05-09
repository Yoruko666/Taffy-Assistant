# Client（控制 / 管理面板）

> SHVA 客户端定位为**控制与管理面板**，**不**承担语音输入。
> 计划技术栈：Android（Kotlin + Jetpack Compose）；Web 演示版可选。
> 当前为占位目录，等服务端核心链路（Server ↔ ASR）打通后再启动客户端开发。

---

## 重要：客户端 ≠ 音频输入端

本系统的语音输入由**家具端**（带麦克风的智能音箱式硬件，类似小度音箱）承担：

```
家具端 ──WS音频流──> Go Server ──WS音频流──> ASR Model
                         │
                         └─> 云端 LLM ─> MQTT 下发设备 / TTS 回播家具端
```

客户端**只**做下面这些事，**不**录音、**不**上传音频：

| 功能 | 说明 |
|---|---|
| 用户认证 | 登录 / 注册 / 登出（HTTPS） |
| 设备管理 | 绑定 / 解绑 / 改名 / 分组（HTTPS） |
| 状态查看 | 设备开关 / 亮度 / 温度 / 在线状态（HTTP + WebSocket 推送） |
| 场景管理 | 场景定义、一键触发（HTTPS） |
| 对话历史 | 浏览家具端语音交互的历史记录（HTTPS） |
| 个人中心 | 资料、改密码、注销 |

---

## 与服务端的连接

客户端**只认 Go Server 一个地址**：

```
Client ──HTTPS──> Go Server (REST，登录/设备/场景/对话历史)
       ──WSS────> Go Server (实时状态推送：device.status / chat.message)
```

**没有**任何与 ASR / TTS / LLM Worker 的直连——所有 AI 能力都由 Go Server 在内部编排。

### 主要 REST 端点

详见 `docs/03-软件设计文档.md` §4.2。客户端常用：

| 模块 | 端点 |
|---|---|
| 认证 | `POST /api/v1/auth/register`、`POST /api/v1/auth/login` |
| 设备 | `GET /api/v1/devices`、`POST /api/v1/devices/bind`、`GET /api/v1/devices/{id}/status` |
| 会话 | `GET /api/v1/conversations`、`GET /api/v1/conversations/{id}/messages` |
| 场景 | `GET /api/v1/scenes`、`POST /api/v1/scenes/{id}/trigger` |
| 用户 | `GET /api/v1/users/me`、`PATCH /api/v1/users/me` |

### WebSocket 推送（接收为主）

```
wss://<host>/ws?token=<jwt>
```

事件类型：

| `type` | 说明 |
|---|---|
| `device.status` | 家具端状态变更（亮度/温度/开关等） |
| `device.online` / `device.offline` | 设备上下线 |
| `chat.message` | 家具端语音交互产生的新消息（让 App 跨端同步看到） |

---

## 技术选型（建议）

### Android 端（首选）

- **语言 / UI**：Kotlin + Jetpack Compose（Material 3）
- **网络**：Retrofit + OkHttp（含 WebSocket）
- **架构**：MVVM + Repository
- **本地存储**：DataStore（Token 等）
- **不需要**：`AudioRecord`、`MediaRecorder`、PCM 处理 — 客户端不录音

### Web 演示版（可选）

仅做调试 / 演示用：

- 框架：Vue 3 / React 任意，简单 SPA 即可
- 主要展示：设备卡片网格、状态实时刷新、对话历史
- **不需要**：`getUserMedia`、`AudioWorklet` — 客户端不录音

---

## 后续工作

- [ ] 选定 Android 单端还是 Android + Web 双端
- [ ] 搭脚手架（Compose 项目模板）
- [ ] UI 落地：登录 / 主页 / 设备详情 / 对话历史 / 场景 / 个人中心
- [ ] 接入 Go Server REST + WebSocket（鉴权、状态推送）
- [ ] 错误恢复：WS 断线自动重连、Token 过期刷新

> 与家具端的语音链路设计见：
> - 家具端协议：[`furniture/README.md`](../furniture/README.md)
> - 服务端 WS 透传：[`server/README.md`](../server/README.md)
> - ASR 模型接口：[`model/README.md`](../model/README.md)
