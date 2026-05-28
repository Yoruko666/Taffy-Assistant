package handler

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"system/internal/config"
	"system/internal/model"
	"system/internal/service"
)

// VoiceHandler 实现家具端 ↔ Server WebSocket（/v1/voice）。
//
// 拆分后职责：
//   - 接受家具端连接，做设备鉴权（device_id / token，可由 SHVA_VOICE_AUTH=off 关闭）
//   - 把家具端整条 WS 会话原样转发给 Worker（/v1/orchestrate）
//   - 把 Worker 的响应原样回传给家具端
//   - 拦截 Worker 下发的 device_command，调用 DeviceService 执行
//   - 在会话建立时，查询用户设备列表推送给 Worker 作为上下文
//
// Server 不再做以下事情（已迁移到 Worker）:
//   - 直接对接 ASR
//   - 翻译 ASR 事件类型
//   - 调用云端 LLM
//   - 合成 TTS
type VoiceHandler struct {
	workerWSURL string
	deviceSvc   *service.DeviceService
	upgrader    websocket.Upgrader
	dialer      *websocket.Dialer

	// authEnabled 控制是否对家具端做 device_id/token 校验。
	// - 默认 true：必须凭据通过才放行（生产/演示前置必备）。
	// - 通过环境变量 SHVA_VOICE_AUTH=off / 0 / false 显式关闭，仅供本地开发期。
	// - deviceSvc 为 nil（DB 未配置）时，鉴权也会自动跳过——日志会打 WARN。
	authEnabled bool
}

// NewVoiceHandler 构造 voice handler，内部拨号到 worker。
func NewVoiceHandler(cfg *config.AppConfig, deviceSvc *service.DeviceService) *VoiceHandler {
	workerURL := ""
	if cfg != nil {
		workerURL = cfg.Worker.WSURL
	}
	authEnabled := parseAuthFlag(os.Getenv("SHVA_VOICE_AUTH"))
	if !authEnabled {
		slog.Warn("voice auth DISABLED via SHVA_VOICE_AUTH env, do NOT use in production")
	}
	if deviceSvc == nil {
		slog.Warn("voice handler created without DeviceService, device credential check will be skipped")
	}
	return &VoiceHandler{
		workerWSURL: workerURL,
		deviceSvc:   deviceSvc,
		authEnabled: authEnabled,
		upgrader: websocket.Upgrader{
			ReadBufferSize:  64 * 1024,
			WriteBufferSize: 64 * 1024,
			CheckOrigin:     func(*http.Request) bool { return true },
		},
		dialer: &websocket.Dialer{
			HandshakeTimeout: 10 * time.Second,
			ReadBufferSize:   64 * 1024,
			WriteBufferSize:  64 * 1024,
		},
	}
}

// parseAuthFlag 解析 SHVA_VOICE_AUTH 环境变量。
// 空 / 未设置 → 默认开启鉴权（true）。
// 显式 "off" / "0" / "false" / "no" → 关闭鉴权（false）。
// 其他值 → 视为开启。
func parseAuthFlag(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "off", "0", "false", "no", "disable", "disabled":
		return false
	default:
		return true
	}
}

func (h *VoiceHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	deviceID := r.URL.Query().Get("device_id")
	token := r.URL.Query().Get("token")
	log := slog.With("path", "/v1/voice", "device_id", deviceID, "remote", r.RemoteAddr)
	if token != "" {
		log = log.With("token_len", len(token))
	}

	// 0) 设备鉴权 —— 必须在 WS Upgrade 之前完成，否则客户端拿到的不是 401 而是
	// "握手成功 + 一帧 error 后被关闭"，体感差且抓不到 HTTP 状态码。
	if err := h.authenticateDevice(r.Context(), deviceID, token, log); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"error":   "unauthorized",
			"message": "invalid device_id or token",
		})
		return
	}

	// 1) 升级家具端连接
	clientConn, err := h.upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Error("upgrade failed", "err", err)
		return
	}
	defer clientConn.Close()
	log.Info("client connected")

	if h.workerWSURL == "" {
		log.Error("worker ws url not configured")
		_ = writeJSON(clientConn, map[string]any{
			"type":    "error",
			"message": "worker not configured",
		})
		return
	}

	// 2) 拨号到 worker，把家具端的 device_id / session_id 通过 query 透传过去
	workerURL, err := buildWorkerURL(h.workerWSURL, deviceID)
	if err != nil {
		log.Error("build worker url failed", "err", err)
		_ = writeJSON(clientConn, map[string]any{
			"type":    "error",
			"message": "worker url invalid",
		})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	workerConn, _, err := h.dialer.DialContext(ctx, workerURL, nil)
	if err != nil {
		log.Error("dial worker failed", "worker", workerURL, "err", err)
		_ = writeJSON(clientConn, map[string]any{
			"type":    "error",
			"message": "worker backend unreachable",
		})
		return
	}
	defer workerConn.Close()
	log.Info("worker connected", "worker", workerURL)

	// 3) 推送设备上下文给 Worker（如果数据库可用）
	//
	// 必须**同步**推送：worker 的 system prompt 依赖此设备列表来生成 tool call
	// 的 device_id。如果异步推送，第一轮 asr_final 触发 LLM 时设备列表可能还没到，
	// 模型会因为"暂无设备"而拒绝控制。
	// 推送本身只发一条 ws 文本帧，最坏 5s（DB 超时）就返回，不会显著增加首字延迟。
	if h.deviceSvc != nil && deviceID != "" {
		h.pushDeviceContext(workerConn, deviceID, log)
	}

	// 4) 双向透传（worker → client 方向拦截 device_command）
	stop := make(chan struct{})
	var once sync.Once
	closeStop := func() { once.Do(func() { close(stop) }) }

	// 写 clientConn 用 Mutex 保护
	var clientWriteMu sync.Mutex

	wg := &sync.WaitGroup{}
	wg.Add(2)

	// ---------- client -> worker ----------
	go func() {
		defer wg.Done()
		defer closeStop()
		for {
			mt, data, err := clientConn.ReadMessage()
			if err != nil {
				if isNormalClose(err) {
					log.Info("client closed")
				} else {
					log.Warn("client read err", "err", err)
				}
				_ = workerConn.WriteControl(
					websocket.CloseMessage,
					websocket.FormatCloseMessage(websocket.CloseNormalClosure, "client gone"),
					time.Now().Add(time.Second),
				)
				return
			}
			if err := workerConn.WriteMessage(mt, data); err != nil {
				log.Warn("worker write err", "err", err)
				return
			}
		}
	}()

	// ---------- worker -> client ----------
	go func() {
		defer wg.Done()
		defer closeStop()
		for {
			mt, data, err := workerConn.ReadMessage()
			if err != nil {
				if isNormalClose(err) {
					log.Info("worker closed")
				} else {
					log.Warn("worker read err", "err", err)
				}
				return
			}

			// 拦截 device_command 事件
			if mt == websocket.TextMessage {
				if isDeviceCommand(data) {
					go h.handleDeviceCommand(workerConn, data, log)
					continue // 不转发给客户端
				}
			}

			clientWriteMu.Lock()
			werr := clientConn.WriteMessage(mt, data)
			clientWriteMu.Unlock()
			if werr != nil {
				log.Warn("client write err", "err", werr)
				return
			}

			// 会话观测：把关键事件打一行日志（不阻塞转发）
			if mt == websocket.TextMessage {
				logSessionEvent(log, data)
			}
		}
	}()

	<-stop
	_ = clientConn.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
	_ = workerConn.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
	wg.Wait()
	log.Info("session done")
}

// authenticateDevice 校验家具端 WS 上行的 (device_id, token) 凭据。
//
// 决策矩阵：
//
//	authEnabled=false → 跳过，仅在缺失关键字段时打 debug 日志；
//	deviceSvc==nil    → 跳过（DB 不可用），打 WARN；
//	凭据有效          → 通过，info 日志；
//	凭据无效          → 返回 ErrInvalidDeviceToken；
//	DB 异常           → 返回原始 err（fail-close，让上层 401 拒绝避免脏会话）。
func (h *VoiceHandler) authenticateDevice(parentCtx context.Context, deviceID, token string, log *slog.Logger) error {
	if !h.authEnabled {
		log.Debug("voice auth skipped (disabled by env)")
		return nil
	}
	if h.deviceSvc == nil {
		log.Warn("voice auth skipped (no DeviceService); set up MySQL or fix configuration before production")
		return nil
	}
	if deviceID == "" || token == "" {
		log.Warn("voice auth rejected: missing device_id or token")
		return service.ErrInvalidDeviceToken
	}

	ctx, cancel := context.WithTimeout(parentCtx, 3*time.Second)
	defer cancel()

	if _, err := h.deviceSvc.ValidateDeviceCredential(ctx, deviceID, token); err != nil {
		if errors.Is(err, service.ErrInvalidDeviceToken) {
			log.Warn("voice auth rejected: invalid credential")
		} else {
			log.Error("voice auth db error", "err", err)
		}
		return err
	}
	log.Info("voice auth ok")
	return nil
}


func (h *VoiceHandler) pushDeviceContext(workerConn *websocket.Conn, deviceID string, log *slog.Logger) {
	ctx, contextCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer contextCancel()

	// 查找当前设备信息，获取 owner_id
	device, err := h.deviceSvc.GetDevice(ctx, deviceID)
	if err != nil {
		log.Warn("push device context: get device failed", "device_id", deviceID, "err", err)
		return
	}

	// 查询用户所有设备
	devices, err := h.deviceSvc.ListDevicesByOwner(ctx, device.OwnerID)
	if err != nil {
		log.Warn("push device context: list devices failed", "err", err)
		return
	}

	// 查询用户所有设备状态
	states, err := h.deviceSvc.ListDeviceStatesByOwner(ctx, device.OwnerID)
	if err != nil {
		log.Warn("push device context: list states failed", "err", err)
		return
	}

	// 构建状态映射 device_id → DeviceState
	stateMap := make(map[string]*model.DeviceState)
	if states != nil {
		for _, s := range states {
			stateMap[s.DeviceID] = s
		}
	}

	// 构建设备上下文列表
	type deviceCtx struct {
		DeviceID    string `json:"device_id"`
		Name        string `json:"name"`
		Type        string `json:"type"`
		Room        string `json:"room"`
		Power       bool   `json:"power"`
		Brightness  *int   `json:"brightness,omitempty"`
		Temperature *int   `json:"temperature,omitempty"`
		Mode        string `json:"mode,omitempty"`
		Position    *int   `json:"position,omitempty"`
	}

	var deviceList []deviceCtx
	for _, d := range devices {
		dc := deviceCtx{
			DeviceID: d.DeviceID,
			Name:     d.Name,
			Type:     string(d.Type),
			Room:     d.Room,
			Power:    false,
		}
		if s, ok := stateMap[d.DeviceID]; ok {
			dc.Power = s.Power
			dc.Brightness = s.Brightness
			dc.Temperature = s.Temperature
			if s.Mode != nil {
				dc.Mode = string(*s.Mode)
			}
			dc.Position = s.Position
		}
		deviceList = append(deviceList, dc)
	}

	if deviceList == nil {
		deviceList = []deviceCtx{}
	}

	msg := map[string]any{
		"type":    "device_info",
		"devices": deviceList,
	}

	b, err := json.Marshal(msg)
	if err != nil {
		log.Warn("push device context: marshal failed", "err", err)
		return
	}

	if err := workerConn.WriteMessage(websocket.TextMessage, b); err != nil {
		log.Warn("push device context: write failed", "err", err)
		return
	}
	log.Info("device context pushed", "devices", len(deviceList))
}

// isDeviceCommand 判断是否是 Worker 下发的设备控制指令。
func isDeviceCommand(data []byte) bool {
	var ev map[string]any
	if err := json.Unmarshal(data, &ev); err != nil {
		return false
	}
	t, _ := ev["type"].(string)
	return t == "device_command"
}

// handleDeviceCommand 处理 Worker 下发的设备控制指令：
// 1. 解析指令
// 2. 调用 DeviceService 更新数据库
// 3. 转发控制指令给家具端（未来通过 MQTT/WS 实现）
// 4. 回传执行结果给 Worker
func (h *VoiceHandler) handleDeviceCommand(
	workerConn *websocket.Conn,
	data []byte,
	log *slog.Logger,
) {
	var cmd struct {
		Type     string          `json:"type"`
		ToolID   string          `json:"tool_id"`
		Function string          `json:"function"`
		Params   json.RawMessage `json:"params"`
	}
	if err := json.Unmarshal(data, &cmd); err != nil {
		log.Warn("handle device command: parse failed", "err", err)
		h.sendCommandResult(workerConn, "", false, "指令解析失败", log)
		return
	}

	log.Info("device command received", "function", cmd.Function, "tool_id", cmd.ToolID, "params", string(cmd.Params))

	var success bool
	var message string

	switch cmd.Function {
	case "control_device":
		success, message = h.executeControlDevice(cmd.Params, log)
	case "activate_scene":
		success, message = h.executeActivateScene(cmd.Params, log)
	default:
		success = false
		message = "未知的控制函数: " + cmd.Function
	}

	log.Info("device command result", "tool_id", cmd.ToolID, "success", success, "message", message)
	h.sendCommandResult(workerConn, cmd.ToolID, success, message, log)

	// 如果控制成功，将状态变更通知转发给家具端
	// TODO(M4): 通过 MQTT 或专用 WS 频道向物理设备下发控制指令
}

// executeControlDevice 执行 control_device 函数。
func (h *VoiceHandler) executeControlDevice(params json.RawMessage, log *slog.Logger) (bool, string) {
	if h.deviceSvc == nil {
		return false, "数据库服务不可用"
	}

	var p struct {
		DeviceID    string `json:"device_id"`
		Action      string `json:"action"`
		Brightness  *int   `json:"brightness,omitempty"`
		Temperature *int   `json:"temperature,omitempty"`
		Mode        string `json:"mode,omitempty"`
		Position    *int   `json:"position,omitempty"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return false, "参数解析失败: " + err.Error()
	}

	if p.DeviceID == "" {
		return false, "缺少 device_id 参数"
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// 获取设备信息，确认归属
	device, err := h.deviceSvc.GetDevice(ctx, p.DeviceID)
	if err != nil {
		return false, "设备不存在: " + p.DeviceID
	}

	// 获取当前状态
	current, err := h.deviceSvc.GetDeviceState(ctx, p.DeviceID)
	if err != nil {
		return false, "获取设备状态失败: " + err.Error()
	}

	// 根据 action 更新状态
	switch p.Action {
	case "turn_on":
		current.Power = true
	case "turn_off":
		current.Power = false
	case "set_brightness":
		if p.Brightness == nil {
			return false, "缺少 brightness 参数"
		}
		current.Power = true
		current.Brightness = p.Brightness
	case "set_temperature":
		if p.Temperature == nil {
			return false, "缺少 temperature 参数"
		}
		current.Power = true
		current.Temperature = p.Temperature
	case "set_mode":
		if p.Mode == "" {
			return false, "缺少 mode 参数"
		}
		current.Power = true
		m := model.AirconMode(p.Mode)
		current.Mode = &m
	case "set_position":
		if p.Position == nil {
			return false, "缺少 position 参数"
		}
		current.Power = true
		current.Position = p.Position
	default:
		return false, "未知的 action: " + p.Action
	}

	// 更新数据库
	if err := h.deviceSvc.UpdateDeviceState(ctx, device.OwnerID, current); err != nil {
		return false, "状态更新失败: " + err.Error()
	}

	// 生成成功消息
	return true, formatSuccessMessage(device, p.Action, current)
}

// executeActivateScene 执行 activate_scene 函数。
func (h *VoiceHandler) executeActivateScene(params json.RawMessage, log *slog.Logger) (bool, string) {
	// TODO: 实现场景激活逻辑
	return false, "场景激活功能暂未实现"
}

// formatSuccessMessage 格式化成功消息。
func formatSuccessMessage(device *model.Device, action string, state *model.DeviceState) string {
	room := device.Room
	name := device.Name
	if room != "" {
		room += "的"
	}

	switch action {
	case "turn_on":
		return "已打开" + room + name
	case "turn_off":
		return "已关闭" + room + name
	case "set_brightness":
		if state.Brightness != nil {
			return room + name + "亮度已设为" + intToStr(*state.Brightness) + "%"
		}
		return room + name + "亮度已调整"
	case "set_temperature":
		if state.Temperature != nil {
			return room + name + "温度已设为" + intToStr(*state.Temperature) + "度"
		}
		return room + name + "温度已调整"
	case "set_mode":
		modeMap := map[string]string{"cool": "制冷", "heat": "制热", "auto": "自动", "fan": "送风", "dry": "除湿"}
		modeName := "未知"
		if state.Mode != nil {
			if n, ok := modeMap[string(*state.Mode)]; ok {
				modeName = n
			}
		}
		return room + name + "已切换为" + modeName + "模式"
	case "set_position":
		if state.Position != nil {
			return room + name + "开合度已设为" + intToStr(*state.Position) + "%"
		}
		return room + name + "开合度已调整"
	default:
		return room + name + "操作完成"
	}
}

// sendCommandResult 向 Worker 回传指令执行结果。
func (h *VoiceHandler) sendCommandResult(workerConn *websocket.Conn, toolID string, success bool, message string, log *slog.Logger) {
	result := map[string]any{
		"type":    "device_command_result",
		"tool_id": toolID,
		"success": success,
		"message": message,
	}
	b, err := json.Marshal(result)
	if err != nil {
		log.Warn("send command result: marshal failed", "err", err)
		return
	}
	if err := workerConn.WriteMessage(websocket.TextMessage, b); err != nil {
		log.Warn("send command result: write failed", "err", err)
	}
}

func intToStr(n int) string {
	if n == 0 {
		return "0"
	}
	sign := ""
	if n < 0 {
		sign = "-"
		n = -n
	}
	digits := make([]byte, 0, 12)
	for n > 0 {
		digits = append(digits, byte('0'+n%10))
		n /= 10
	}
	for i, j := 0, len(digits)-1; i < j; i, j = i+1, j-1 {
		digits[i], digits[j] = digits[j], digits[i]
	}
	return sign + string(digits)
}

// buildWorkerURL 在 worker 的 WS URL 上附加 device_id / session_id 等
// 透传参数，便于 worker 日志关联同一会话。
func buildWorkerURL(base, deviceID string) (string, error) {
	if deviceID == "" {
		return base, nil
	}
	u, err := url.Parse(base)
	if err != nil {
		return "", err
	}
	q := u.Query()
	q.Set("device_id", deviceID)
	q.Set("session_id", time.Now().UTC().Format("20060102T150405.000000000Z"))
	u.RawQuery = q.Encode()
	return u.String(), nil
}

// logSessionEvent 把 worker → client 的关键事件打一行精简日志，
// 便于在 server 侧观测会话进度（不影响转发性能）。
func logSessionEvent(log *slog.Logger, data []byte) {
	var ev map[string]any
	if err := json.Unmarshal(data, &ev); err != nil {
		return
	}
	t, _ := ev["type"].(string)
	switch t {
	case "asr_final":
		text, _ := ev["text"].(string)
		log.Info("event", "type", t, "text", text)
	case "llm_result":
		text, _ := ev["text"].(string)
		log.Info("event", "type", t, "len", len(text))
	case "llm_error", "error":
		msg, _ := ev["message"].(string)
		log.Warn("event", "type", t, "message", msg)
	}
}

func writeJSON(c *websocket.Conn, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return c.WriteMessage(websocket.TextMessage, b)
}

func isNormalClose(err error) bool {
	if errors.Is(err, websocket.ErrCloseSent) {
		return true
	}
	if ce, ok := err.(*websocket.CloseError); ok {
		switch ce.Code {
		case websocket.CloseNormalClosure,
			websocket.CloseGoingAway,
			websocket.CloseNoStatusReceived:
			return true
		}
	}
	return false
}
