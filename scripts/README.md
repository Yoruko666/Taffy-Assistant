# scripts

仓库级启动脚本集中存放在此处。

| 脚本 | 作用 | 用法（仓库根） |
|---|---|---|
| `start-all.ps1` | 一键起全栈：ASR(:9100) + TTS(:9200) + Worker(:8090) + Server(:8080) | `.\scripts\start-all.ps1` |

模块自带的脚本不在此处（保持就近原则）：

- `model/start_all.ps1`：仅启动 ASR + TTS（模型层独立调试用）

## 环境变量覆盖

| 变量 | 默认 | 说明 |
|---|---|---|
| `ASR_PORT` | 9100 | ASR HTTP 端口 |
| `TTS_PORT` | 9200 | TTS HTTP 端口 |
| `WORKER_PORT` | 8090 | Worker HTTP/WS 端口 |
| `PORT` | 8080 | Server HTTP/WS 端口 |
| `TAFFY_VOICE_AUTH` | （未设）| 设为 `off`/`0`/`false` 可关闭 `/v1/voice` 鉴权（仅本地开发） |
