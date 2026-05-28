# Furniture（家具端）

> Taffy 系统的**音频输入端 + 设备执行端**：家具助手"**小菲**"以及灯/空调/窗帘等普通设备的本地模拟器。

家具端包含两类设备：

- **带麦家具（小菲音箱）**：走 WebSocket `/v1/voice` 上传音频，接收 ASR/LLM/TTS 结果。对应 `mock_furniture.py`。
- **普通家具（灯/空调/窗帘）**：走 MQTT 收指令、上报状态/心跳。对应 `mock_devices.py`。

---

## 1. PC 端音箱模拟器 `mock_furniture.py`

### 1.1 安装依赖

```powershell
pip install -r furniture/requirements.txt
```

### 1.2 用法

```powershell
# 实时麦克风：对着麦克风说话，VAD 自动断句
python furniture/mock_furniture.py --server ws://127.0.0.1:8080/v1/voice?device_id=dev1&token=t1

# 列出可用音频设备
python furniture/mock_furniture.py --list-devices
```

### 1.3 端侧行为：VAD 暂停 / 恢复

每次 VAD 检测到用户说完（静音 ≥ 800ms → 自动发 `end`）后：

1. **暂停语音检测**，不再采集下一段音频；
2. 等待 Server 经过 ASR + LLM 链路后返回结果；
3. 收到 `llm_result`（或 `llm_error`）事件后，**恢复语音检测**，继续下一轮对话。

这样避免 LLM 处理期间用户的新语音和旧结果混淆，形成自然的**用户说 → 机器答 → 用户再说**对话节奏。

> 实现细节：`mock_furniture.py` 内部用 `asyncio.Event` 同步：`stream_live`（VAD + 上行）发完 `end` 后等待 `llm_done` 事件，`receiver` 收到 `llm_result` 后设置该事件，恢复 VAD。

### 1.4 Tool Call 流程（v0.5+）

当用户指令涉及设备控制（如"打开客厅灯"）时，LLM 会通过 Function Calling 机制调用 `control_device` 工具：

1. ASR 识别文本 → Worker 调用 LLM（携带设备上下文 + Tool Schema）
2. LLM 返回 `tool_calls`（如 `control_device`）→ Worker 下发 `device_command` 给 Server
3. Server 执行设备控制（更新 DB）→ 回复 `device_command_result` 给 Worker
4. Worker 二次调用 LLM（携带 tool 执行结果）→ LLM 生成自然语言回复
5. Worker 回传 `llm_result` → 家具端显示回复

家具端感知上与之前完全一致——只收到 `llm_result`，中间的 tool call 过程对家具端透明。

---

## 2. 普通设备模拟器 `mock_devices.py`

用于模拟灯/空调/窗帘等**仅 MQTT 控制**的普通设备。

### 2.1 用法

```powershell
# 连接 MQTT Broker（默认 localhost:1883）
python furniture/mock_devices.py --devices light_living,aircon_bedroom,curtain_living

# 无 MQTT Broker 时的降级模式
python furniture/mock_devices.py --standalone --devices light_living,aircon_bedroom
```

### 2.2 设备命名

设备命名格式：`<type>_<room>`，如 `light_living` / `aircon_bedroom` / `curtain_living`。

---

## 3. 通信协议（对接 Server / Worker 必读）

### 3.1 家具端音频 WebSocket（带麦家具）

**端点**：`wss://<host>:8080/v1/voice?device_id=<id>&token=<device-token>`

| 方向 | 帧类型 | 内容 |
|---|---|---|
| 家具→Server | text | `{"type":"start","sample_rate":16000,"format":"pcm_s16le","channels":1}` |
| 家具→Server | binary | 16-bit LE PCM mono 字节流，每 ~600ms 一包 |
| 家具→Server | text | `{"type":"end"}`（端侧 VAD 触发） |
| 家具→Server | text | `{"type":"ping"}`（保活） |
| Server→家具 | text | `{"type":"pong"}` |
| Server→家具 | text | `{"type":"asr_partial","text":"..."}`（ASR 中间结果） |
| Server→家具 | text | `{"type":"asr_final","text":"..."}`（ASR 最终结果 → 触发 LLM） |
| Server→家具 | text | `{"type":"eos"}`（本段结束，连接保持） |
| Server→家具 | text | `{"type":"llm_result","text":"..."}`（M2：大模型回答） |
| Server→家具 | text | `{"type":"llm_error","message":"..."}`（M2：大模型调用失败） |
| Server→家具 | text | `{"type":"reply","text":"..."}`（M3：大模型回答） |
| Server→家具 | text | `{"type":"device_done","device":"...","action":"..."}`（M3：设备执行完成） |
| Server→家具 | text | `{"type":"tts_audio","format":"wav","data":"<base64>"}`（M3：语音回播） |
| Server→家具 | text | `{"type":"error","message":"..."}`（其他错误） |

> `tts_audio` 由家具端保存或播放（`mock_furniture.py` 当前保存到 `furniture/output/`）。

### 3.2 MQTT 设备通信（普通家具）

**Topic 规范**（与设计文档 4.4 对齐）：

| Topic | 方向 | 说明 | QoS |
|---|---|---|---|
| `taffy/device/{id}/register` | Dev→Server | 上线注册 | 1 |
| `taffy/device/{id}/cmd` | Server→Dev | 下发控制指令 | 1 |
| `taffy/device/{id}/status` | Dev→Server | 状态上报 | 1 |
| `taffy/device/{id}/result` | Dev→Server | 指令执行结果 | 1 |
| `taffy/device/{id}/heartbeat` | Dev→Server | 心跳（30s） | 0 |

#### 3.2.1 注册报文（Dev → Server）

```json
{
  "device_id": "light_living",
  "type": "light",
  "name": "light_living",
  "room": "living"
}
```

#### 3.2.2 控制指令（Server → Dev）

```json
{
  "action": "turn_on",
  "params": {
    "brightness": 80
  }
}
```

#### 3.2.3 状态上报（Dev → Server）

```json
{
  "device_id": "light_living",
  "type": "light",
  "name": "light_living",
  "room": "living",
  "power": true,
  "brightness": 80,
  "updated_at": 1737010234
}
```

#### 3.2.4 指令结果（Dev → Server）

```json
{
  "device_id": "light_living",
  "action": "turn_on",
  "result": "ok"
}
```

#### 3.2.5 心跳（Dev → Server）

```json
{
  "device_id": "light_living",
  "ts": 1737010234
}
```

---

## 4. 联调建议顺序

1. 先跑通 WS 音频链路（`asr_final` / `llm_result`）
2. 再跑 MQTT 设备链路（`cmd` / `status` / `result`）
3. 最后做全链路语音控制设备联调（语音 → tool call → MQTT → 状态回流）
