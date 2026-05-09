# Furniture（家具端）

> SHVA 系统的**音频输入端**：家具助手"**小菲**"——带麦克风与扬声器的智能家居设备。

---

## PC 端模拟器 `mock_furniture.py`（"虚拟小菲"）

支持两种模式：
- **单句模式**：用 wav 文件模拟单句语音输入；
- **实时麦克风模式**（`--live`）：从物理麦克风真实采集，实时上行测试。

### 安装依赖

```powershell
pip install -r furniture/requirements.txt
```

依赖清单（`requirements.txt`）：

- `websockets` —— WS 客户端
- `webrtcvad` —— 端侧 VAD，自动断句
- `soundfile`、`numpy` —— wav 解析与格式转换
- `scipy` —— **抗混叠多相重采样**（`resample_poly`）。喂入非 16k 的 wav 时必须有
- `sounddevice` —— **实时麦克风采集**（`--live` 模式需要）

### 用法

```powershell
# 1) 单句：经 server 透传到 ASR
python furniture/mock_furniture.py `
    --server ws://127.0.0.1:8080/v1/voice `
    --device-id dev1 --token t1 `
    --wav some.wav

# 2) 实时麦克风：对着麦克风说话，VAD 自动断句
python furniture/mock_furniture.py --server ws://127.0.0.1:8080/v1/voice `
    --device-id dev1 --token t1 --live
```
