package handler

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"taffy.local/pkg/protocol"
	"taffy.local/pkg/wsutil"

	"taffy-server/internal/config"
	"taffy-server/internal/model"
	"taffy-server/internal/service"
)

// VoiceHandler 实现家具端 ↔ Server 的 /v1/voice WebSocket：
//   - 设备 device_id / token 鉴权（可由 TAFFY_VOICE_AUTH=off 关闭）
//   - 与 Worker 之间双向透传
//   - 拦截 Worker 的 device_command，委托 service 层执行后回传结果
//   - 会话建立时推送设备上下文给 Worker
//   - 把 asr_final / llm_result / device_command 异步落库
type VoiceHandler struct {
	workerWSURL string
	deviceSvc   *service.DeviceService
	convSvc     *service.ConversationService
	upgrader    websocket.Upgrader
	dialer      *websocket.Dialer

	// authEnabled=false 时跳过 device_id/token 校验，仅本地开发用。
	authEnabled bool
}

// NewVoiceHandler 构造 voice handler。convSvc 为 nil 时跳过对话历史落库。
func NewVoiceHandler(cfg *config.AppConfig, deviceSvc *service.DeviceService, convSvc *service.ConversationService) *VoiceHandler {
	workerURL := ""
	if cfg != nil {
		workerURL = cfg.Worker.WSURL
	}
	authEnabled := parseAuthFlag(os.Getenv("TAFFY_VOICE_AUTH"))
	if !authEnabled {
		slog.Warn("voice auth DISABLED via TAFFY_VOICE_AUTH env, do NOT use in production")
	}
	if deviceSvc == nil {
		slog.Warn("voice handler created without DeviceService, device credential check will be skipped")
	}
	return &VoiceHandler{
		workerWSURL: workerURL,
		deviceSvc:   deviceSvc,
		convSvc:     convSvc,
		authEnabled: authEnabled,
		upgrader:    wsutil.DefaultUpgrader(),
		dialer:      wsutil.DefaultDialer(),
	}
}

func (h *VoiceHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	deviceID := r.URL.Query().Get("device_id")
	token := r.URL.Query().Get("token")
	log := slog.With("path", "/v1/voice", "device_id", deviceID, "remote", r.RemoteAddr)
	if token != "" {
		log = log.With("token_len", len(token))
	}

	// 鉴权必须在 WS Upgrade 之前完成，否则客户端拿不到 401 状态码。
	device, err := h.authenticateDevice(r.Context(), deviceID, token, log)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"error":   "unauthorized",
			"message": "invalid device_id or token",
		})
		return
	}

	clientConn, err := h.upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Error("upgrade failed", "err", err)
		return
	}
	defer clientConn.Close()
	log.Info("client connected")

	if h.workerWSURL == "" {
		log.Error("worker ws url not configured")
		_ = wsutil.WriteJSON(clientConn, map[string]any{"type": "error", "message": "worker not configured"})
		return
	}

	workerURL, err := buildWorkerURL(h.workerWSURL, deviceID)
	if err != nil {
		log.Error("build worker url failed", "err", err)
		_ = wsutil.WriteJSON(clientConn, map[string]any{"type": "error", "message": "worker url invalid"})
		return
	}

	dialCtx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	workerConn, _, err := h.dialer.DialContext(dialCtx, workerURL, nil)
	if err != nil {
		log.Error("dial worker failed", "worker", workerURL, "err", err)
		_ = wsutil.WriteJSON(clientConn, map[string]any{"type": "error", "message": "worker backend unreachable"})
		return
	}
	defer workerConn.Close()
	log.Info("worker connected", "worker", workerURL)

	// 同步推送设备上下文，确保 LLM 第一轮调用时已能看到设备列表。
	if h.deviceSvc != nil && deviceID != "" {
		h.pushDeviceContext(workerConn, deviceID, log)
	}

	vs := newVoiceSession(h.convSvc, log)
	if device != nil && device.OwnerID != nil {
		startCtx, startCancel := context.WithTimeout(context.Background(), 3*time.Second)
		vs.start(startCtx, *device.OwnerID, deviceID)
		startCancel()
	}
	defer vs.end()

	h.pump(clientConn, workerConn, vs, log)
	log.Info("session done")
}

// authenticateDevice 校验家具端 (device_id, token) 凭据。
//
//	authEnabled=false → 跳过；尝试解析设备用于落库挂 user_id；
//	deviceSvc==nil    → 跳过（DB 不可用）；
//	凭据有效          → 返回 *model.Device；
//	凭据无效          → ErrInvalidDeviceToken；
//	DB 异常           → 原样返回。
func (h *VoiceHandler) authenticateDevice(parentCtx context.Context, deviceID, token string, log *slog.Logger) (*model.Device, error) {
	if !h.authEnabled {
		log.Debug("voice auth skipped (disabled by env)")
		if h.deviceSvc != nil && deviceID != "" {
			ctx, cancel := context.WithTimeout(parentCtx, 3*time.Second)
			defer cancel()
			if d, err := h.deviceSvc.GetDevice(ctx, deviceID); err == nil {
				return d, nil
			}
		}
		return nil, nil
	}
	if h.deviceSvc == nil {
		log.Warn("voice auth skipped (no DeviceService); set up MySQL or fix configuration before production")
		return nil, nil
	}
	if deviceID == "" || token == "" {
		log.Warn("voice auth rejected: missing device_id or token")
		return nil, service.ErrInvalidDeviceToken
	}

	ctx, cancel := context.WithTimeout(parentCtx, 3*time.Second)
	defer cancel()

	device, err := h.deviceSvc.ValidateDeviceCredential(ctx, deviceID, token)
	if err != nil {
		if errors.Is(err, service.ErrInvalidDeviceToken) {
			log.Warn("voice auth rejected: invalid credential")
		} else {
			log.Error("voice auth db error", "err", err)
		}
		return nil, err
	}
	log.Info("voice auth ok")
	return device, nil
}

// pushDeviceContext 把当前用户的设备列表 + 状态打包推给 Worker。
func (h *VoiceHandler) pushDeviceContext(workerConn *websocket.Conn, deviceID string, log *slog.Logger) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	devices, err := h.deviceSvc.BuildDeviceContext(ctx, deviceID)
	if err != nil {
		log.Warn("push device context failed", "device_id", deviceID, "err", err)
		return
	}
	if devices == nil {
		devices = []service.DeviceContextItem{}
	}

	if err := wsutil.WriteJSON(workerConn, protocol.DeviceInfoEvent{
		Type:    protocol.EventTypeDeviceInfo,
		Devices: devices,
	}); err != nil {
		log.Warn("push device context: write failed", "err", err)
		return
	}
	log.Info("device context pushed", "devices", len(devices))
}

// pump 双向透传家具端 ↔ Worker，直到任一侧关闭。
// worker → client 方向会拦截 device_command 交给 service 处理，
// 并把 asr_final / llm_result 异步写入对话历史表。
func (h *VoiceHandler) pump(clientConn, workerConn *websocket.Conn, vs *voiceSession, log *slog.Logger) {
	stop := make(chan struct{})
	var once sync.Once
	closeStop := func() { once.Do(func() { close(stop) }) }

	// 正常转发 + device_command_result 都会写客户端，加锁保证并发安全。
	var clientWriteMu sync.Mutex
	writeClient := func(mt int, data []byte) error {
		clientWriteMu.Lock()
		defer clientWriteMu.Unlock()
		return clientConn.WriteMessage(mt, data)
	}

	wg := &sync.WaitGroup{}
	wg.Add(2)

	// client → worker
	go func() {
		defer wg.Done()
		defer closeStop()
		for {
			mt, data, err := clientConn.ReadMessage()
			if err != nil {
				if wsutil.IsNormalClose(err) {
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

	// worker → client
	go func() {
		defer wg.Done()
		defer closeStop()
		for {
			mt, data, err := workerConn.ReadMessage()
			if err != nil {
				if wsutil.IsNormalClose(err) {
					log.Info("worker closed")
				} else {
					log.Warn("worker read err", "err", err)
				}
				return
			}

			if mt == websocket.TextMessage && isDeviceCommand(data) {
				go h.handleDeviceCommand(workerConn, data, vs, log)
				continue
			}

			if err := writeClient(mt, data); err != nil {
				log.Warn("client write err", "err", err)
				return
			}

			if mt == websocket.TextMessage {
				logSessionEvent(log, data)
				vs.recordWorkerEvent(data)
			}
		}
	}()

	<-stop
	_ = clientConn.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
	_ = workerConn.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
	wg.Wait()
}

// handleDeviceCommand 解析 worker 下发的 device_command，
// 委托给 service.DeviceService 执行后把结果回传给 worker，同时落库。
func (h *VoiceHandler) handleDeviceCommand(workerConn *websocket.Conn, data []byte, vs *voiceSession, log *slog.Logger) {
	var cmd protocol.DeviceCommandEvent
	if err := json.Unmarshal(data, &cmd); err != nil {
		log.Warn("handle device command: parse failed", "err", err)
		h.sendCommandResult(workerConn, "", false, "指令解析失败", log)
		return
	}

	log.Info("device command received",
		"function", cmd.Function, "tool_id", cmd.ToolID, "params", string(cmd.Params))

	// 先插入 commands(result=pending)，便于异步路径回填结果。
	vs.recordCommand(cmd)

	var result service.DeviceCommandResult
	switch cmd.Function {
	case protocol.FunctionControlDevice:
		if h.deviceSvc == nil {
			result = service.DeviceCommandResult{Success: false, Message: "数据库服务不可用"}
		} else {
			result = h.deviceSvc.ExecuteControlDevice(context.Background(), cmd.Params)
		}
	case protocol.FunctionActivateScene:
		if h.deviceSvc == nil {
			result = service.DeviceCommandResult{Success: false, Message: "数据库服务不可用"}
		} else {
			result = h.deviceSvc.ExecuteActivateScene(context.Background(), cmd.Params)
		}
	default:
		result = service.DeviceCommandResult{Success: false, Message: "未知的控制函数: " + cmd.Function}
	}

	log.Info("device command result",
		"tool_id", cmd.ToolID, "success", result.Success, "message", result.Message)
	h.sendCommandResult(workerConn, cmd.ToolID, result.Success, result.Message, log)
	vs.markCommandResult(cmd.ToolID, result.Success)
}

// sendCommandResult 向 Worker 回传指令执行结果。
func (h *VoiceHandler) sendCommandResult(workerConn *websocket.Conn, toolID string, success bool, message string, log *slog.Logger) {
	if err := wsutil.WriteJSON(workerConn, protocol.DeviceCommandResultEvent{
		Type:    protocol.EventTypeDeviceCommandResult,
		ToolID:  toolID,
		Success: success,
		Message: message,
	}); err != nil {
		log.Warn("send command result: write failed", "err", err)
	}
}
