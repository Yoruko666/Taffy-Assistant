# Furniture（家具端）

> SHVA 系统的**音频输入端**：带麦克风与扬声器的智能家居设备（类似小度音箱、天猫精灵）。
> 本目录同时提供：
>
> 1. **协议契约**：家具端 ↔ Go Server 的 WebSocket 协议，供硬件 / 模拟器对照实现；
> 2. **PC 端模拟器** `mock_furniture.py`：用 wav 当虚拟麦克风，**完整复刻"小度"端侧链路**
>    （KWS 唤醒词 → VAD 端点检测 → WS 长连接上行 → 滚屏展示 asr_partial/asr_final）。

---

## 角色与定位

```
[家具端]                   [Go Server]               [ASR Model]
  麦克风 ─PCM─> WS音频流 ─透传─> WS音频流 ─> 流式识别
  扬声器 <─wav─ TTS音频  <───── TTS 合成 <─ 云端 LLM
```

家具端职责（与商用智能音箱一致）：

| 项 | 说明 |
|---|---|
| 唤醒 / 触发 | **KWS**（唤醒词，可替换：AlwaysOn / openWakeWord / Porcupine）或物理按键 |
| 端点检测 | **webrtcvad** 在端侧做 VAD，检测到 ≥ 800ms 静音自动发 `end` |
| 录音 | 16kHz / 16-bit / 单声道 PCM，**强约束** |
| 上行 | **一条 WebSocket 长连接，承载多轮对话**（每轮一组 start / [PCM...] / end） |
| 下行 | 接收 Go Server 推送的 ASR 识别文本、LLM 回复、TTS 音频 |
| 播放 | 解码并播放 TTS wav |
| 控制 | 接收 MQTT 控制指令（灯 / 空调 / 窗帘等设备控制，与音频是不同通道） |

---

## PC 端模拟器 `mock_furniture.py`（"伪小度"）

脚本定位：**不需要真实麦克风**，用若干 wav 文件拼成"虚拟麦克风流"——文件之间塞静音模拟用户停顿。完整跑一条
`wav 流 → KWS → VAD → WS 上行 → asr_partial/asr_final 滚屏` 的端侧链路。

### 端侧流水线

```
[虚拟麦克风 wav 流]
        │
        ▼
[KWS 唤醒词检测]   ← 抽象层；M2 默认 AlwaysOn（始终视为已唤醒）
        │          ← M3 可换成 openWakeWord / Porcupine，仅需新增子类
        ▼
[VAD 端点检测]     ← webrtcvad，检测到说话开始/结束 → 自动发 start/end
        │
        ▼
[WebSocket 上行]   ── 16k PCM 600ms/包 ──▶  Go Server (/v1/voice) ──▶  ASR Model
        │                                       │
        └───────────── asr_partial / asr_final ◀┘
```

### 安装依赖

```powershell
pip install -r furniture/requirements.txt
```

依赖清单（`requirements.txt`）：

- `websockets` —— WS 客户端
- `webrtcvad` —— 端侧 VAD，自动断句
- `soundfile`、`numpy` —— wav 解析与格式转换
- `scipy` —— **抗混叠多相重采样**（`resample_poly`）。喂入非 16k 的 wav 时必须有，否则会严重混叠（典型表现：`把客厅的灯打开` → `马听德德灯打`）
- 预留 `openwakeword` / `pvporcupine` —— M3 唤醒词

> 真实硬件建议麦克风直接按 16 kHz 采样，**不要**在家具端做重采样。`mock_furniture.py` 的重采样只是为了 PC 上能直接用现成 wav 联调。

### 用法

```powershell
# 1) 单句：经 server 透传到 ASR
python furniture/mock_furniture.py `
    --server ws://127.0.0.1:8080/v1/voice `
    --device-id dev1 --token t1 `
    --wav some.wav

# 2) 多句连说：脚本把多个 wav 拼成连续麦克风流，每句之间插 1.5s 静音
#    VAD 会自动为每句发 start/end，同一条 WS 拿到多个 asr_final
python furniture/mock_furniture.py `
    --server ws://127.0.0.1:8080/v1/voice `
    --wav a.wav b.wav c.wav --gap-ms 1500

# 3) PTT 模式：关闭 VAD，整段一次性 start/PCM/end（兼容老 test_stream.py 行为）
python furniture/mock_furniture.py --mode ptt --wav a.wav

# 4) 直连 ASR 排障（跳过 server）
python furniture/mock_furniture.py `
    --server ws://127.0.0.1:9100/v1/asr/stream --wav a.wav
```

### 关键参数

| 参数 | 默认 | 说明 |
|---|---|---|
| `--mode` | `vad` | `vad`=端侧自动断句（像小度）；`ptt`=按住说话整段送 |
| `--silence-ms` | 800 | VAD 判定"一句话结束"所需的静音时长 |
| `--min-speech-ms` | 90 | VAD 判定"一句话开始"所需的语音时长（过滤偶发噪声，调小可以让句首爆破音更容易触发 start） |
| `--lookback-ms` | 300 | VAD `start` 时额外把前这么多毫秒的音频也送给 ASR，避免句首被吞（如 `把客厅的灯打开` → `客厅的灯打开`） |
| `--vad-level` | 2 | webrtcvad 灵敏度 0~3，越大越严 |
| `--gap-ms` | 1200 | 多 wav 之间插入的静音（模拟用户两句之间的停顿） |
| `--lead-ms` | 500 | 流开头静音（给 VAD 一段稳定的非语音基线） |
| `--realtime` | off | 按 30ms 帧实时节奏发送（更真实但更慢） |
| `--hold` | 15 | 发完后等服务端响应的最大秒数 |

### 识别质量小贴士

| 症状 | 调整 |
|---|---|
| 识别成乱串（整段都不对） | 确认已 `pip install scipy`，或把 wav 先转成 16k mono |
| 句首第一个字被吞 | 加大 `--lookback-ms 500`，或把 `--min-speech-ms` 调到 60 |
| 多句连说只出 1 个 final | 加大 `--gap-ms 2000`，或 `--silence-ms` 调小到 600 |
| 一直出不来 final（只有 partial） | `--mode ptt` 或把 `--silence-ms` 调到 500；也可能是 wav 尾部太短 |
| `的` 被识别成 `德` 这类同音字错 | 这是流式模型本身的限制，后续 M2 的 LLM 语义归一化会纠正，不影响 demo

### 预期输出

```
[mock-furniture] loading 2 wav file(s)…
[mock-furniture] virtual mic stream: ...
[mock-furniture] connecting ws://127.0.0.1:8080/v1/voice?device_id=dev1&token=t1
[mock-furniture] connected (mode=vad)
   ↳ ready (ASR 就绪)
[mock-furniture] ▶ VAD: segment #1 START (+300ms lookback)
   ↳ asr_partial: 把客厅
   ↳ asr_partial: 把客厅的灯
[mock-furniture] ■ VAD: segment #1 END (waiting asr_final…)
   ↳ asr_final: 把客厅的灯打开  ✅
   ↳ eos
[mock-furniture] ▶ VAD: segment #2 START (+300ms lookback)
   ↳ asr_partial: 把空调
   ↳ asr_final: 把空调调到 26 度  ✅
[mock-furniture] done
```

### KWS 替换点（M3）

```python
# 新增一个子类即可，主流程不需要改
class OpenWakeWordEngine(WakeWord):
    def __init__(self, model_path): ...
    def feed(self, frame_pcm16: bytes) -> bool: ...     # ONNX 推理，命中返回 True
    def is_active_window(self) -> bool: ...             # 命中后开 N 秒会话窗口
```

> 硬件（ESP32 / 树莓派）可以直接复用这里的协议与参数（600ms/包、16k mono PCM、VAD 800ms 静默），
> 实现语言换掉即可。

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
| 家具→Server | `text` | `{"type":"start","sample_rate":16000,"format":"pcm_s16le","channels":1}` | 一段话开始（每轮对话 1 次） |
| 家具→Server | `binary` | 16-bit LE PCM mono 字节流 | 段内每 ~600ms 一包 |
| 家具→Server | `text` | `{"type":"end"}` | 端侧 VAD 检测到静音 / 物理松键 / 达到最长时长 |
| 家具→Server | `text` | `{"type":"ping"}` | 保活（30s 一次） |
| Server→家具 | `text` | `{"type":"asr_partial","text":"..."}` | 段内实时识别中间结果 |
| Server→家具 | `text` | `{"type":"asr_final","text":"..."}` | 本段识别完成 |
| Server→家具 | `text` | `{"type":"eos"}` | 本段结束标记，**连接保持，可继续下一段** |
| Server→家具 | `text` | `{"type":"reply","text":"..."}` | LLM 文本回复 |
| Server→家具 | `text` | `{"type":"device_done","device":"...","action":"..."}` | 设备执行结果（用于本地反馈） |
| Server→家具 | `text` | `{"type":"tts_audio","format":"wav","data":"<base64>"}` | TTS 语音回复，**家具端立即播放** |
| Server→家具 | `text` | `{"type":"error","message":"..."}` | 错误（识别失败 / LLM 失败等） |
| Server→家具 | `text` | `{"type":"pong"}` | 保活回包 |

> 注：帧类型语义与 ASR 模型原生一致，但事件名加上 `asr_` 前缀以区分下游 LLM / 设备 / TTS 事件，方便家具端按 `type` 字段分发。

### 单 WS 多轮对话时序

```
家具端                                       Go Server
  │  WS connect ?device_id=...&token=...       │
  │ ─────────────────────────────────────────> │  → 拨号 ASR WS
  │                                              │
  │  ── 第 1 轮：打开客厅灯 ──                   │
  │  {"type":"start", ...}                      │ → 透传
  │  [PCM 600ms] × N                            │ → 透传
  │                            {"type":"asr_partial","text":"打开客厅"}
  │ <───────────────────────────────────────── │
  │  {"type":"end"}        (VAD 触发)           │ → 透传
  │                            {"type":"asr_final","text":"打开客厅灯"}
  │                            {"type":"eos"}
  │ <───────────────────────────────────────── │
  │                            {"type":"reply","text":"好的，已为您打开客厅灯"}
  │                            {"type":"device_done","device":"light-001","action":"turn_on"}
  │                            {"type":"tts_audio","format":"wav","data":"..."}
  │ <───────────────────────────────────────── │ (家具端播放)
  │                                              │
  │  ── 第 2 轮：把空调调到 26 度 ──（**同一条 WS**） │
  │  {"type":"start", ...}                      │
  │  [PCM 600ms] × M                            │
  │  {"type":"end"}                             │
  │                            {"type":"asr_final","text":"把空调调到 26 度"}
  │                            {"type":"eos"}
  │ <───────────────────────────────────────── │
  │                            ...（LLM / 设备 / TTS 同上）
  │                                              │
  │  用户结束使用 → 主动关 WS                    │
  │ ─────────────────────────────────────────> │
```

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

## 硬件实现建议（非强制）

### ESP32-S3（C/C++）

- 麦克风：I²S 接 INMP441 / SPH0645
- WS 客户端：`esp_websocket_client`
- 编码：直接 16-bit PCM，**不要**自己做 MP3 / Opus（增加延迟，且 ASR 不要求压缩）
- VAD：可以跑 `esp-sr` 自带的 VAD，或把端点检测下放给云端（退化为 PTT 模式）
- KWS：`esp-sr` 自带的 WakeNet，或 Picovoice Porcupine for MCU

### 树莓派 / Linux 板子（Python）

- 直接把 `mock_furniture.py` 改成从 `sounddevice.InputStream` 读真实麦克风，整条 VAD / WS 管线复用

---

## 后续工作

- [x] 协议：家具端 ↔ server WebSocket 契约
- [x] PC 模拟器：`mock_furniture.py`（KWS 抽象 + VAD 自动断句 + WS 长连接多轮）
- [x] 单 WS 多段对话语义（ASR / Server / 家具 三方对齐）
- [ ] M3 唤醒词：openWakeWord 模型 + `OpenWakeWordEngine` 子类
- [ ] 真实麦克风 backend：`sounddevice` 版本（树莓派即插即用）
- [ ] ESP32-S3 参考实现
- [ ] 接入 MQTT 控制通道（共用 `furniture/` 目录或拆 `furniture/audio` `furniture/control`）

> 服务端配套实现见 [`server/README.md`](../server/README.md)。
> ASR 模型协议见 [`model/README.md`](../model/README.md)（家具端**不直连** ASR，只通过 Go Server）。
