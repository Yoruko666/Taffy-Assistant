# scripts

仓库级启动脚本集中存放在此处。

| 脚本 | 作用 | 用法（仓库根） |
|---|---|---|
| `start-all.ps1` | 一键起全栈：ASR(:9100) + TTS(:9200) + Worker(:8090) + Server(:8080) | `.\scripts\start-all.ps1` |

模块自带的脚本不在此处（保持就近原则）：

- `model/start_all.ps1`：仅启动 ASR + TTS（模型层独立调试用）
- `model/setup_gguf.py` / `model/download_model.py`：模型依赖与权重下载

## 启动顺序

`start-all.ps1` 内部顺序：

1. 启动 ASR（FastAPI on `:9100`），等待 `/v1/health` 返回 `stream_loaded`（最长 120s）；
2. 启动 TTS（FastAPI on `:9200`），等待 `/v1/health` 通过（最长 60s，超时仅 warn）；
3. 启动 Worker（`go run ./cmd/worker`），等待 `/v1/health` 返回 `taffy-worker`（最长 60s）；
4. 启动 Server（`go run ./cmd/server`），不等待。

每个服务会单独开一个 PowerShell 窗口，关闭对应窗口即停止该服务。

## 环境变量覆盖

`start-all.ps1` 支持以下环境变量在启动前覆盖默认值：

| 变量 | 默认 | 影响 |
|---|---|---|
| `ASR_PORT` | `9100` | ASR HTTP/WS 端口 |
| `TTS_PORT` | `9200` | TTS HTTP 端口 |
| `WORKER_PORT` | `8090` | Worker HTTP/WS 端口 |
| `PORT` | `8080` | Server HTTP/WS 端口 |
| `LLM_API_KEY` | （需自行设置）| 云端 LLM 鉴权，未设置时 worker 仅完成 ASR 透传 |

各组件还会读取自身的环境变量（在 `start-all.ps1` 之外、之前 export 也有效）：

| 变量 | 默认 | 用途 | 读取方 |
|---|---|---|---|
| `TAFFY_VOICE_AUTH` | （未设）| 设为 `off`/`0`/`false` 关闭 `/v1/voice` 设备 token 校验（仅本地开发）| server |
| `WORKER_WS_URL` | `ws://127.0.0.1:8090/v1/orchestrate` | server → worker 的拨号地址 | server |
| `MYSQL_HOST` / `MYSQL_PASSWORD` | yaml 默认 | 覆盖 server `config.yaml > mysql.*` | server |
| `REDIS_ADDR` | yaml 默认 | 覆盖 server `config.yaml > redis.addr` | server |
| `JWT_SECRET` | yaml 默认（不安全）| 覆盖 server `config.yaml > jwt.secret`，生产**必须设置** | server |
| `MQTT_BROKER` | （未设）| 设为如 `tcp://127.0.0.1:1883` 启用 MQTT bridge，留空则禁用 | server |
| `MQTT_USERNAME` / `MQTT_PASSWORD` | （未设）| broker 鉴权（若需要）| server |
| `ASR_WS_URL` | yaml 默认 | 覆盖 worker `config.yaml > asr.ws_url` | worker |
| `TTS_URL` | yaml 默认 | 覆盖 worker `config.yaml > tts.url` | worker |
| `CONFIG_PATH` | `config.yaml` | server / worker 各自读取自己工作目录下的配置 | server / worker |

> 完整配置项与默认值见 [`server/README.md`](../server/README.md) 与 [`worker/README.md`](../worker/README.md) 的"配置"章节。
