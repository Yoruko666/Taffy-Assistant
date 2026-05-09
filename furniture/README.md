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
