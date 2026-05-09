# Furniture（家具端）

> SHVA 系统的**音频输入端**：带麦克风与扬声器的智能家居设备（类似小度音箱、天猫精灵）。
> 当前**不实现具体硬件 / 模拟器**，本文档只定义**家具端 ↔ Go Server 的 WebSocket 协议契约**，供后续硬件 / 模拟器开发对照实现。

---

## 角色与定位

```
[家具端]                   [Go Server]               [ASR Model]
  麦克风 ─PCM─> WS音频流 ─透传─> WS音频流 ─> 流式识别
  扬声器 <─wav─ TTS音频  <───── TTS 合成 <─ 云端 LLM
```

家具端职责：

| 项 | 说明 |
|---|---|
| 唤醒 / 触发 | 物理按键、唤醒词检测（KWS）或定时录音（具体实现自由） |
| 录音 | 16kHz / 16-bit / 单声道 PCM，**强约束** |
| 上行 | 通过 WebSocket 发送音频帧到 Go Server |
| 下行 | 接收 Go Server 推送的 ASR 识别文本、LLM 回复、TTS 音频 |
| 播放 | 解码并播放 TTS wav |
| 控制 | 接收 MQTT 控制指令（灯 / 空调 / 窗帘等设备控制，与音频是不同通道） |

> **本仓库现阶段不实现家具端代码**，仅定义协议。硬件可选 ESP32-S3 / 树莓派 / Linux 板子，软件模拟器可用 Python + sounddevice 在 PC 上跑（演示足够）。

---

## 协议：家具端 ↔ Go Server WebSocket

### 端点

```
ws://<server-host>:8080/v1/voice
```

> 生产环境 `wss://`。鉴权方式：URL 参数 `?device_id=<id>&token=<device-token>`，由家具端在出厂 / 配网阶段烧录的设备凭证生成。

### 录音参数（强约束）

| 项 | 值 |
|---|---|
| 采样率 | **16000 Hz** |
| 位深 | **16-bit signed PCM** |
| 通道 | **单声道（mono）** |
| 字节序 | **little-endian** |
| 帧长建议 | 每 ~600ms 发一包（19200 字节，与 ASR 模型 chunk 对齐） |

> 与 ASR 模型 (`paraformer-zh-streaming`) 的 `chunk_size=[0,10,5]` 对齐，能拿到最佳延迟与识别率。

### 帧定义

| 方向 | 帧类型 | 内容 | 说明 |
|---|---|---|---|
| 家具→Server | `text` | `{"type":"start","sample_rate":16000,"format":"pcm_s16le","channels":1}` | 录音开始首帧 |
| 家具→Server | `binary` | 16-bit LE PCM mono 字节流 | 每 ~600ms 一包 |
| 家具→Server | `text` | `{"type":"end"}` | 用户停止说话 / 静音超时 / 物理松键 |
| 家具→Server | `text` | `{"type":"ping"}` | 保活（30s 一次） |
| Server→家具 | `text` | `{"type":"asr_partial","text":"..."}` | 实时识别中间结果（可显示在屏幕，无屏可丢弃） |
| Server→家具 | `text` | `{"type":"asr_final","text":"..."}` | 一段话识别完成 |
| Server→家具 | `text` | `{"type":"reply","text":"..."}` | LLM 文本回复 |
| Server→家具 | `text` | `{"type":"device_done","device":"...","action":"..."}` | 设备执行结果（用于本地反馈，如声光提示） |
| Server→家具 | `text` | `{"type":"tts_audio","format":"wav","data":"<base64>"}` | TTS 语音回复，**家具端立即播放** |
| Server→家具 | `text` | `{"type":"error","message":"..."}` | 错误（识别失败 / LLM 失败等） |
| Server→家具 | `text` | `{"type":"pong"}` | 保活回包 |

> 注：帧类型语义与 ASR 模型原生一致，但事件名加上 `asr_` 前缀以区分下游 LLM / 设备 / TTS 事件，方便家具端按 `type` 字段分发。

### 一次完整交互的时序

```
家具端                                       Go Server
  │  WS connect ?device_id=...&token=...       │
  │ ─────────────────────────────────────────> │
  │                                              │ → 拨号 ASR WS
  │  {"type":"start", ...}                      │
  │ ─────────────────────────────────────────> │ → 透传给 ASR
  │  [PCM 600ms]                                │
  │ ─────────────────────────────────────────> │ → 透传给 ASR
  │                            {"type":"asr_partial","text":"打开"}
  │ <───────────────────────────────────────── │
  │  [PCM 600ms]                                │
  │ ─────────────────────────────────────────> │
  │                            {"type":"asr_partial","text":"打开客厅"}
  │ <───────────────────────────────────────── │
  │  {"type":"end"}                             │
  │ ─────────────────────────────────────────> │ → ASR 出 final
  │                            {"type":"asr_final","text":"打开客厅灯"}
  │ <───────────────────────────────────────── │
  │                                              │ → 调云端 LLM
  │                            {"type":"reply","text":"好的，已为您打开客厅灯"}
  │ <───────────────────────────────────────── │
  │                                              │ → MQTT 控制设备
  │                            {"type":"device_done","device":"light-001","action":"turn_on"}
  │ <───────────────────────────────────────── │
  │                                              │ → 调本地 TTS
  │                            {"type":"tts_audio","format":"wav","data":"..."}
  │ <───────────────────────────────────────── │  ← 家具端播放
```

---

## 实现建议（参考，非强制）

### Linux / 树莓派（Python 模拟器）

```python
# 伪代码示意，实际不在本仓库实现
import sounddevice as sd
import websockets, json, asyncio

async def run():
    ws = await websockets.connect("ws://server:8080/v1/voice?device_id=...&token=...")
    await ws.send(json.dumps({"type":"start","sample_rate":16000,"format":"pcm_s16le","channels":1}))
    # 后台线程录音、按 600ms 切帧、二进制发送
    # 主协程读 ws 消息：asr_partial 显示，tts_audio 播放
```

### ESP32-S3（C/C++）

- 麦克风：I²S 接 INMP441 / SPH0645
- WS 客户端：`esp_websocket_client`
- 编码：直接 16-bit PCM，**不要**自己做 MP3 / Opus（增加延迟，且 ASR 不要求压缩）

---

## 与 MQTT 控制通道的关系

家具端有**两条网络通道**，不要混淆：

| 通道 | 协议 | 用途 |
|---|---|---|
| **音频通道** | WebSocket (`/v1/voice`) | 仅"会说话"的家具（音箱）使用，承载语音交互 |
| **控制通道** | MQTT (`shva/device/{id}/...`) | 所有家具都用，接收开关 / 亮度 / 温度等控制指令 |

举例：
- 客厅音箱：**两条**都连（既要听用户说话，也可能被远程控制开关机）
- 卧室灯：**只连 MQTT**（不需要听用户说话）
- 用户说"打开卧室灯" → 客厅音箱通过 WS 上传 → Server 通过 MQTT 控制卧室灯 → MQTT 回报状态

---

## 后续工作

- [ ] 选定家具端形态：纯软件模拟器 / ESP32 硬件 / 树莓派 / 多者并存
- [ ] 实现唤醒方案（按键 / 唤醒词 KWS / 持续录音 + VAD）
- [ ] 实现 WebSocket 客户端 + 双向流处理
- [ ] 接入 MQTT 控制通道（共用 `furniture/` 目录或拆 `furniture/audio` `furniture/control`）

> 服务端配套实现见 [`server/README.md`](../server/README.md)。
> ASR 模型协议见 [`model/README.md`](../model/README.md)（家具端**不直连** ASR，只通过 Go Server）。
