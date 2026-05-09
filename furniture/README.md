# Furniture（家具端）

> SHVA 系统的**音频输入端**：家具助手"**小菲**"——带麦克风与扬声器的智能家居设备。

---

## PC 端模拟器 `mock_furniture.py`

### 安装依赖

```powershell
pip install -r furniture/requirements.txt
```

### 用法

```powershell
# 实时麦克风：对着麦克风说话，VAD 自动断句
python furniture/mock_furniture.py --server ws://127.0.0.1:8080/v1/voice?device_id=dev1&token=t1

# 列出可用音频设备
python furniture/mock_furniture.py --list-devices
```

### M2 行为：VAD 暂停 / 恢复

每次 VAD 检测到用户说完（静音 ≥ 800ms → 自动发 `end`）后：

1. **暂停语音检测**，不再采集下一段音频；
2. 等待 Server 经过 ASR + LLM 链路后返回结果；
3. 收到 `llm_result`（或 `llm_error`）事件后，**恢复语音检测**，继续下一轮对话。

这样避免 LLM 处理期间用户的新语音和旧结果混淆，形成自然的**用户说 → 机器答 → 用户再说**对话节奏。

### 协议：家具端 ↔ Go Server WebSocket

**端点**：`wss://server:8080/v1/voice?device_id=<id>&token=<token>`

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
| Server→家具 | text | `{"type":"llm_result","text":"..."}`（**M2 新增**：大模型回答） |
| Server→家具 | text | `{"type":"llm_error","message":"..."}`（**M2 新增**：大模型调用失败） |
| Server→家具 | text | `{"type":"error","message":"..."}`（其他错误） |

> `mock_furniture.py` 内部用 `asyncio.Event` 同步：`stream_live`（VAD + 上行）发完 `end` 后等待 `llm_done` 事件，`receiver` 收到 `llm_result` 后设置该事件，恢复 VAD。
