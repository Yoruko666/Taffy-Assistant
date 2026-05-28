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

	"taffy-server/internal/config"
	"taffy-server/internal/service"
)

// VoiceHandler 实现家具端 ↔ Server WebSocket（/v1/voice）：
//   - HTTP 入口 + 设备鉴权（device_id / token，可由 TAFFY_VOICE_AUTH=off 关闭）
//   - 双向透传家具端 ↔ Worker 的 WS 帧
//   - 拦截 Worker 的 device_command，委托 service 层执行后回传结果
//   - 会话建立时同步推送设备上下文给 Worker
type VoiceHandler struct {
	workerWSURL string
	deviceSvc   *service.DeviceService
	upgrader    websocket.Upgrader
	dialer      *websocket.Dialer

	// authEnabled 控制是否对家具端做 device_id/token 校验。
	//   - 默认 true：必须凭据通过才放行；
	//   - 通过环境变量 TAFFY_VOICE_AUTH=off / 0 / false 显式关闭，仅供本地开发期；
	//   - deviceSvc 为 nil（DB 未配置）时鉴权也会自动跳过，启动时打 WARN。
	authEnabled bool
}

// NewVoiceHandler 构造 voice handler，内部拨号到 worker。
func NewVoiceHandler(cfg *config.AppConfig, deviceSvc *service.DeviceService) *VoiceHandler {
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

func (h *VoiceHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	deviceID := r.URL.Query().Get("device_id")
	token := r.URL.Query().Get("token")
	log := slog.With("path", "/v1/voice", "device_id", deviceID, "remote", r.RemoteAddr)
	if token != "" {
		log = log.With("token_len", len(token))
	}

	// 必须在 WS Upgrade 之前完成鉴权，否则客户端拿不到 401 状态码。
	if err := h.authenticateDevice(r.Context(), deviceID, token, log); err != nil {
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
		_ = writeJSON(clientConn, map[string]any{"type": "error", "message": "worker not configured"})
		return
	}

	workerURL, err := buildWorkerURL(h.workerWSURL, deviceID)
	if err != nil {
		log.Error("build worker url failed", "err", err)
		_ = writeJSON(clientConn, map[string]any{"type": "error", "message": "worker url invalid"})
		return
	}

	dialCtx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	workerConn, _, err := h.dialer.DialContext(dialCtx, workerURL, nil)
	if err != nil {
		log.Error("dial worker failed", "worker", workerURL, "err", err)
		_ = writeJSON(clientConn, map[string]any{"type": "error", "message": "worker backend unreachable"})
		return
	}
	defer workerConn.Close()
	log.Info("worker connected", "worker", workerURL)

	// 同步推送，确保 LLM 第一轮调用时已能看到设备列表。
	if h.deviceSvc != nil && deviceID != "" {
		h.pushDeviceContext(workerConn, deviceID, log)
	}

	h.pump(clientConn, workerConn, log)
	log.Info("session done")
}

// authenticateDevice 校验家具端 WS 上行的 (device_id, token) 凭据。
//
// 决策矩阵：
//
//	authEnabled=false → 跳过；
//	deviceSvc==nil    → 跳过（DB 不可用），打 WARN；
//	凭据有效          → 通过；
//	凭据无效          → ErrInvalidDeviceToken；
//	DB 异常           → 原样返回（fail-close）。
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

// pushDeviceContext 把当前用户的设备列表 + 状态打包推给 Worker。
// 业务字段构造在 service.BuildDeviceContext。
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

	if err := writeJSON(workerConn, protocol.DeviceInfoEvent{
		Type:    protocol.EventTypeDeviceInfo,
		Devices: devices,
	}); err != nil {
		log.Warn("push device context: write failed", "err", err)
		return
	}
	log.Info("device context pushed", "devices", len(devices))
}

// pump 双向透传家具端 ↔ Worker，直到任一侧关闭。
// worker → client 方向会拦截 device_command 交给 service 处理。
func (h *VoiceHandler) pump(clientConn, workerConn *websocket.Conn, log *slog.Logger) {
	stop := make(chan struct{})
	var once sync.Once
	closeStop := func() { once.Do(func() { close(stop) }) }

	// 客户端写入需要并发安全：正常转发 + device_command_result 都会写。
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

	// worker → client
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

			if mt == websocket.TextMessage && isDeviceCommand(data) {
				go h.handleDeviceCommand(workerConn, data, log)
				continue
			}

			if err := writeClient(mt, data); err != nil {
				log.Warn("client write err", "err", err)
				return
			}

			if mt == websocket.TextMessage {
				logSessionEvent(log, data)
			}
		}
	}()

	<-stop
	_ = clientConn.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
	_ = workerConn.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
	wg.Wait()
}

// handleDeviceCommand 解析 worker 下发的 device_command，
// 委托给 [service.DeviceService] 执行后把结果回传给 worker。
func (h *VoiceHandler) handleDeviceCommand(workerConn *websocket.Conn, data []byte, log *slog.Logger) {
	var cmd protocol.DeviceCommandEvent
	if err := json.Unmarshal(data, &cmd); err != nil {
		log.Warn("handle device command: parse failed", "err", err)
		h.sendCommandResult(workerConn, "", false, "指令解析失败", log)
		return
	}

	log.Info("device command received",
		"function", cmd.Function, "tool_id", cmd.ToolID, "params", string(cmd.Params))

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

	// TODO: 通过 MQTT 或专用 WS 频道把控制指令同步下发给物理设备
}

// sendCommandResult 向 Worker 回传指令执行结果。
func (h *VoiceHandler) sendCommandResult(workerConn *websocket.Conn, toolID string, success bool, message string, log *slog.Logger) {
	if err := writeJSON(workerConn, protocol.DeviceCommandResultEvent{
		Type:    protocol.EventTypeDeviceCommandResult,
		ToolID:  toolID,
		Success: success,
		Message: message,
	}); err != nil {
		log.Warn("send command result: write failed", "err", err)
	}
}
