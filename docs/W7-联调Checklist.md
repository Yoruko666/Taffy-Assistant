# W7 端到端集成联调 Checklist

> **联调窗口**：2026-06-01（周一）~ 2026-06-04（周四）
> **目标**：在 W7 末（06-07 前）跑通 UC-01 / UC-02 / UC-03 三条端到端用例，作为 M3 原型 Demo 验收依据。
> **协调人**：关梓浩
> **使用方式**：每条 Link 由对应负责人执行；遇到问题先在群里贴日志，再决定是否升级为 Bug。

---

## 联调前置条件（W6 末必须就绪）

- [ ] MySQL 8 / Redis 7 在本机或同网段可连，已执行 `001_init.sql` + `002_seed.sql`
- [ ] 模型权重已下载（`model/asr_server/models/`、`model/tts_server/models/zh_CN-huayan-medium.onnx`）
- [ ] Worker `config.yaml` 或 `LLM_API_KEY` 环境变量已配大模型 Key
- [ ] 各端口未被占用：`9100 / 9200 / 8090 / 8080 / 3306 / 6379`
- [ ] **代码冻结**：联调期间禁止往 `main` 推非 fix/test 提交；新功能进 `feature/*` 分支

---

## Link ① 模型服务自检（吴承凯）

**启动**

```powershell
cd model
.\start_all.ps1   # 起 ASR(:9100) + TTS(:9200)
```

**验收点**

| 检查项 | 命令 / 操作 | 绿灯标准 |
|---|---|---|
| ① ASR 健康 | `curl http://127.0.0.1:9100/v1/health` | 返回 `{"status":"ok"}` 或 `loading→ok`（≤ 30s） |
| ② TTS 健康 | `curl http://127.0.0.1:9200/v1/health` | `loaded_voices` 含 `zh_CN-huayan-medium` |
| ③ TTS 单测 | `python model/test_stream_e2e.py` 或：`Invoke-RestMethod -Uri http://127.0.0.1:9200/v1/tts/synthesize -Method POST -ContentType 'application/json' -Body '{"text":"测试一下"}' -OutFile test.wav` | `test.wav` 可播放，时长 1~2s |
| ④ ASR 流式单测 | `python model/asr_server/scripts/test_stream.py` | 终端打印 partial→final 文本 |

**典型故障与排查**

- TTS 启动 `503 piper-tts not installed` → `pip install piper-tts`
- ASR 启动慢 → 第一次加载 paraformer 需要预热 ~30s，等就行
- 9100/9200 端口被占 → `netstat -ano | findstr :9100` 找进程杀掉

---

## Link ② Worker ↔ 模型服务（吴承凯）

**启动**

```powershell
$env:LLM_API_KEY = "sk-..."
cd worker
go run ./cmd/worker     # :8090
```

**验收点**

| 检查项 | 操作 | 绿灯标准 |
|---|---|---|
| ① Worker 健康 | `curl http://127.0.0.1:8090/v1/health` | 返回 ok，`asr_url` 等字段非空 |
| ② Worker → ASR 拨号 | 看 worker 启动日志 | 无 `dial asr failed` |
| ③ LLM 配置生效 | 看启动日志 | `model=GLM-4-Flash-250414`、`api_key_len > 0` |
| ④ 直连 worker 跑 ASR | `python furniture/mock_furniture.py --server "ws://127.0.0.1:8090/v1/orchestrate?device_id=dev1"` 然后说话 | 终端打印 `用户：xxx` 和 `小菲：xxx`，且 `output/tts_*.wav` 落盘 |

**典型故障**

- LLM 401：检查 `LLM_API_KEY` 是否带 `sk-` 前缀，URL 是否写错（智谱、通义不同前缀）
- LLM 超时：把 `worker/config.yaml` 的 `timeout` 从 30 提到 60
- TTS 失败但 LLM 文本回复正常：说明 P1-6 接入的"异步不阻塞"策略生效，问题缩到 TTS 单段

---

## Link ③ Server ↔ Worker（林帅 + 吴承凯）

**启动**

```powershell
cd server
go run ./cmd/server     # :8080
```

**验收点**

| 检查项 | 操作 | 绿灯标准 |
|---|---|---|
| ① Server 健康 | `curl http://127.0.0.1:8080/v1/health` | `worker=ok / mysql=ok / redis=ok` 三项全亮 |
| ② Server 拨号 worker | 起 `mock_furniture.py --server ws://127.0.0.1:8080/...` | server 日志 `worker connected`，无 `dial worker failed` |
| ③ device_info 同步推送 | server 日志 | `device context pushed devices=N`，且**先于** `event type=asr_final` 出现 |
| ④ device_command 拦截 | 说"打开客厅灯"，看 server 日志 | `device command received function=control_device` → `device command result success=true` |
| ⑤ DB 状态写入 | 执行后 `SELECT * FROM device_states WHERE device_id='...'` | `power=1`、`updated_at` 是刚才的时间 |
| ⑥ 二轮 LLM history 正确 | worker 日志 | `second llm ok reply_len>0`，且端侧 `小菲：` 句子合理（不出现"我不知道你在说什么"） |
| ⑦ TTS 链路 | 端侧 `output/` 目录 | 出现 `tts_*.wav`，时长合理（不是 0 字节） |

**Bug 典型场景**

- 设备列表为空 → 检查 `002_seed.sql` 是否执行、`device.owner_id` 是否与请求的 `device_id` 关联
- LLM 不调 tool（直接文字回复）→ 检查 system prompt 里设备列表是否为空（说明 ③ 没到）
- 多轮对话第二次失败 → 检查 worker 是否对每个会话独立维护 `sessionToolCalls`，**禁止共享**
- **鉴权拒绝**（HTTP 401，连不上 WS）→ 见下方"鉴权专项"

### 鉴权专项（新增 P1-4）

`/v1/voice` 在 WS Upgrade 之前会校验 `?device_id=...&token=...` 与 `devices` 表是否匹配。

| 检查项 | 操作 | 绿灯标准 |
|---|---|---|
| ① 合法设备直连 | `mock_furniture.py --server "ws://127.0.0.1:8080/v1/voice?device_id=dev1&token=t1"` | server 日志 `voice auth ok`，握手成功 |
| ② 错误 token | URL 改成 `token=wrong` | server 日志 `voice auth rejected: invalid credential`，HTTP 401 |
| ③ 缺失 token | URL 改成 `?device_id=dev1` | HTTP 401 + 日志 `missing device_id or token` |
| ④ 不存在的 device | `device_id=ghost&token=any` | HTTP 401 |
| ⑤ 开发期降级 | `$env:TAFFY_VOICE_AUTH="off"; go run ./cmd/server` | 启动时 `voice auth DISABLED ... do NOT use in production`，任何 device_id 都能进 |

---

## Link ④ 家具端全链路（刘智冲 + 全员）

> 这是 **UC-02** 的主战场，演示视频的 90% 内容来自这里。

**测试用例**

| 用例 ID | 说话内容 | 期望端侧打印 | 期望 DB 状态变化 |
|---|---|---|---|
| UC-02-01 | "打开客厅灯" | `用户：打开客厅灯` → `小菲：好的，已为您打开客厅灯` + 播放 wav | `device_states` 客厅灯 `power=1` |
| UC-02-02 | "把空调调到 26 度" | `小菲：已将空调温度设为 26 度` | `temperature=26 power=1` |
| UC-02-03 | "卧室窗帘拉到一半" | `小菲：卧室窗帘开合度已设为 50%` | `position=50` |
| UC-02-04 | "今天天气怎么样" | `小菲：<闲聊回复>`（不调 tool） | DB 无变化 |
| UC-02-05 | "把不存在的灯打开" | `小菲：抱歉，您的设备列表里没有 xxx` | DB 无变化 |
| UC-02-06 | 连续说 3 句不同指令 | 每句 ≤ 3s 内得到回复 | 3 次状态分别正确写入 |

**绿灯标准**：6 个用例至少 5 个一次过。

**性能基线**（不达标也不阻塞 M3，但记录到测试报告）

- 端到端首字延迟（说完到 `用户：xxx`）：**< 1.5s**
- LLM tool call → DB 写入：**< 1s**
- TTS 合成 1 句中文：**< 2s**

---

## Link ⑤ Android ↔ Server（马正朗 + 林帅）

> **风险最高的一段**——客户端目前只有 WS 监听骨架。
> W7 内必须达到的最小可演示状态：

**用例**

| 用例 ID | 操作 | 绿灯标准 |
|---|---|---|
| UC-01-01 | 启动 App → 注册新用户 | 进入主界面，本地存了 JWT |
| UC-01-02 | 登录已有用户 | 同上 |
| UC-01-03 | 查看设备列表 | 看到种子数据里的 N 台设备 |
| UC-03-01 | 设备列表上点开/关 | 卡片 UI 即时翻转，DB 实际更新（用 mysql 客户端验证） |
| UC-03-02 | 查看设备状态详情 | 显示 `power / brightness / temperature / mode` |
| UC-02-Android | App 内对小菲说话（**Stretch goal**，做不完不阻塞 M3） | 走 WS 上行 → 收 `llm_result` 显示 |

**接口对照表**（已就绪，等客户端接入）

| 用途 | 方法 | 路径 | 鉴权 |
|---|---|---|---|
| 注册 | `POST` | `/api/v1/auth/register` | 无 |
| 登录 | `POST` | `/api/v1/auth/login` | 无 |
| 设备列表 | `GET` | `/api/v1/devices` | JWT |
| 状态列表 | `GET` | `/api/v1/devices/states` | JWT |
| 更新状态 | `PUT` | `/api/v1/devices/state` | JWT |

---

## 联调日志规范

> 每天联调结束前，关梓浩在 `docs/attachments/W7-联调日志.md` 追加一段。

模板：

```markdown
## 2026-06-XX

### 跑通的链路
- Link ①②③④ ✅

### 阻塞问题
1. [BUG-001] worker 偶发 panic：xxx（吴承凯，已开 issue）
2. ...

### 明日计划
- 林帅：补 device_id/token 鉴权
- 马正朗：登录页接入
```

---

## 退出条件（M3 验收）

W7 末（2026-06-07）必须满足：

- [ ] Link ①②③④ 全部绿灯，UC-02 至少 5/6 通过
- [ ] Link ⑤ 的 UC-01-01/02/03 + UC-03-01 全绿（设备控制可点击改 DB）
- [ ] 演示视频 V0（5 分钟内）录制完成，能清晰展示一次"打开客厅灯"的完整链路
- [ ] 关键 Bug（P0/P1）数量为 0
- [ ] 测试用例集（容嘉负责）已经把 UC-01/02/03 的步骤记录在 `docs/04-测试计划与用例报告.md`

---

## 一键启动顺序备忘

任何成员要拉起完整环境，按这个顺序：

```
1. 起 MySQL 8 + Redis 7（本地服务或 docker）
2. cd model && .\start_all.ps1            (ASR :9100 + TTS :9200)
3. cd worker && go run ./cmd/worker        (:8090)
4. cd server && go run ./cmd/server        (:8080)
5. python furniture/mock_furniture.py --server "ws://127.0.0.1:8080/v1/voice?device_id=dev1&token=t1"
   或在 Android Studio 跑 client/ 模块
```

或直接在项目根目录：

```powershell
.\start_all.ps1
```
