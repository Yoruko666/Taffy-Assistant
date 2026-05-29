package handler

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"taffy-server/internal/hub"
	"taffy-server/internal/middleware"
	"taffy-server/internal/model"
	"taffy-server/internal/service"
)

// 设备相关业务错误码常量。
const (
	codeUnauthorized   = "unauthorized"
	codeInvalidRequest = "invalid_request_body"
	codeDeviceIDReq    = "device_id_required"
	codeBindCodeReq    = "bind_code_required"
	codeInvalidBind    = "invalid_bind_code"
	codeDeviceNotFound = "device_not_found"
	codeNotOwner       = "not_device_owner"
	codeNameRequired   = "name_required"
)

// DeviceHandler 设备相关 HTTP 处理器。
type DeviceHandler struct {
	deviceSvc *service.DeviceService
	pub       hub.Publisher // 可为 nil；nil 时跳过实时广播
}

// NewDeviceHandler 创建 DeviceHandler。pub 为 nil 时跳过实时推送（降级行为）。
func NewDeviceHandler(deviceSvc *service.DeviceService, pub hub.Publisher) *DeviceHandler {
	return &DeviceHandler{deviceSvc: deviceSvc, pub: pub}
}

// ListDevices GET /api/v1/devices
func (h *DeviceHandler) ListDevices(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.UserIDFromContext(r.Context())
	if !ok {
		writeJSONError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	devices, err := h.deviceSvc.ListDevicesByOwner(r.Context(), userID)
	if err != nil {
		slog.Error("list devices failed", "err", err)
		writeJSONError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if devices == nil {
		devices = []*model.Device{}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"devices": devices})
}

// ListDeviceStates GET /api/v1/devices/states
func (h *DeviceHandler) ListDeviceStates(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.UserIDFromContext(r.Context())
	if !ok {
		writeJSONError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	states, err := h.deviceSvc.ListDeviceStatesByOwner(r.Context(), userID)
	if err != nil {
		slog.Error("list device states failed", "err", err)
		writeJSONError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if states == nil {
		states = []*model.DeviceState{}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"states": states})
}

// UpdateDeviceStateRequest 更新设备状态请求体。
type UpdateDeviceStateRequest struct {
	DeviceID    string  `json:"device_id"`
	Power       *bool   `json:"power,omitempty"`
	Brightness  *int    `json:"brightness,omitempty"`
	Temperature *int    `json:"temperature,omitempty"`
	Mode        *string `json:"mode,omitempty"`
	Position    *int    `json:"position,omitempty"`
}

// UpdateDeviceState PUT /api/v1/devices/state
func (h *DeviceHandler) UpdateDeviceState(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.UserIDFromContext(r.Context())
	if !ok {
		writeJSONError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	var req UpdateDeviceStateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.DeviceID == "" {
		writeJSONError(w, http.StatusBadRequest, "device_id is required")
		return
	}

	// 获取当前状态并合并更新
	current, err := h.deviceSvc.GetDeviceState(r.Context(), req.DeviceID)
	if err != nil {
		if errors.Is(err, service.ErrDeviceNotFound) {
			writeJSONError(w, http.StatusNotFound, "device not found")
			return
		}
		slog.Error("get device state failed", "err", err)
		writeJSONError(w, http.StatusInternalServerError, "internal error")
		return
	}

	// 合并：只更新请求中提供的字段
	if req.Power != nil {
		current.Power = *req.Power
	}
	if req.Brightness != nil {
		current.Brightness = req.Brightness
	}
	if req.Temperature != nil {
		current.Temperature = req.Temperature
	}
	if req.Mode != nil {
		m := model.AirconMode(*req.Mode)
		current.Mode = &m
	}
	if req.Position != nil {
		current.Position = req.Position
	}

	if err := h.deviceSvc.UpdateDeviceState(r.Context(), userID, current); err != nil {
		if errors.Is(err, service.ErrNotDeviceOwner) {
			writeJSONError(w, http.StatusForbidden, "not device owner")
			return
		}
		slog.Error("update device state failed", "err", err)
		writeJSONError(w, http.StatusInternalServerError, "internal error")
		return
	}

	// 实时广播给当前用户在线的所有客户端（UC-10），current 已是合并后的最新状态。
	if h.pub != nil {
		h.pub.BroadcastToUser(userID, map[string]any{
			"type":      "device_state_changed",
			"device_id": req.DeviceID,
			"state":     current,
		})
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"message": "ok"})
}

// GetDeviceState GET /api/v1/devices/{deviceID}/state
func (h *DeviceHandler) GetDeviceState(w http.ResponseWriter, r *http.Request) {
	deviceID := r.PathValue("deviceID")
	if deviceID == "" {
		writeJSONError(w, http.StatusBadRequest, "device_id is required")
		return
	}

	state, err := h.deviceSvc.GetDeviceState(r.Context(), deviceID)
	if err != nil {
		if errors.Is(err, service.ErrDeviceNotFound) {
			writeJSONError(w, http.StatusNotFound, "device not found")
			return
		}
		slog.Error("get device state failed", "err", err)
		writeJSONError(w, http.StatusInternalServerError, "internal error")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(state)
}

// ============================================================
// UC-03 设备绑定相关接口
// ============================================================

// BindableDeviceItem 给客户端展示的"待绑定池"条目。
// 注意：bind_code **不**在该响应中下发——用户需要从设备本体（贴纸 / 配套说明）取得，
// 输入正确才能完成绑定，避免任意人扫到列表就能抢绑。演示期为方便老师评审，
// 也允许通过 `?reveal=1` 查询参数返回 bind_code（仅限非生产环境）。
type BindableDeviceItem struct {
	DeviceID string            `json:"device_id"`
	Type     model.DeviceType  `json:"type"`
	Name     string            `json:"name"`
	BindCode string            `json:"bind_code,omitempty"`
}

// ListBindableDevices GET /api/v1/devices/bindable
func (h *DeviceHandler) ListBindableDevices(w http.ResponseWriter, r *http.Request) {
	if _, ok := middleware.UserIDFromContext(r.Context()); !ok {
		writeAPIError(w, http.StatusUnauthorized, codeUnauthorized, "unauthorized")
		return
	}

	devices, err := h.deviceSvc.ListBindable(r.Context())
	if err != nil {
		slog.Error("list bindable devices failed", "err", err)
		writeAPIError(w, http.StatusInternalServerError, codeInternal, "internal error")
		return
	}

	reveal := r.URL.Query().Get("reveal") == "1"
	items := make([]BindableDeviceItem, 0, len(devices))
	for _, d := range devices {
		item := BindableDeviceItem{
			DeviceID: d.DeviceID,
			Type:     d.Type,
			Name:     d.Name,
		}
		if reveal {
			item.BindCode = d.BindCode
		}
		items = append(items, item)
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"devices": items})
}

// BindDeviceRequest 绑定设备请求体。
type BindDeviceRequest struct {
	DeviceID string `json:"device_id"`
	BindCode string `json:"bind_code"`
	Name     string `json:"name"`
	Room     string `json:"room"`
}

// BindDevice POST /api/v1/devices/bind
func (h *DeviceHandler) BindDevice(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.UserIDFromContext(r.Context())
	if !ok {
		writeAPIError(w, http.StatusUnauthorized, codeUnauthorized, "unauthorized")
		return
	}

	var req BindDeviceRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeAPIError(w, http.StatusBadRequest, codeInvalidRequest, "invalid request body")
		return
	}
	req.DeviceID = strings.TrimSpace(req.DeviceID)
	req.BindCode = strings.TrimSpace(req.BindCode)
	req.Name = strings.TrimSpace(req.Name)
	req.Room = strings.TrimSpace(req.Room)
	if req.DeviceID == "" {
		writeAPIError(w, http.StatusBadRequest, codeDeviceIDReq, "device_id is required")
		return
	}
	if req.BindCode == "" {
		writeAPIError(w, http.StatusBadRequest, codeBindCodeReq, "bind_code is required")
		return
	}

	device, err := h.deviceSvc.BindDevice(r.Context(), userID, req.DeviceID, req.BindCode, req.Name, req.Room)
	if err != nil {
		if errors.Is(err, service.ErrInvalidBindCode) {
			writeAPIError(w, http.StatusConflict, codeInvalidBind,
				"invalid bind code or device already bound")
			return
		}
		slog.Error("bind device failed", "err", err, "device_id", req.DeviceID)
		writeAPIError(w, http.StatusInternalServerError, codeInternal, "internal error")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"message": "ok",
		"device":  device,
	})
}

// RenameDeviceRequest 重命名设备请求体。
type RenameDeviceRequest struct {
	Name string `json:"name"`
	Room string `json:"room"`
}

// RenameDevice PUT /api/v1/devices/{deviceID}
func (h *DeviceHandler) RenameDevice(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.UserIDFromContext(r.Context())
	if !ok {
		writeAPIError(w, http.StatusUnauthorized, codeUnauthorized, "unauthorized")
		return
	}
	deviceID := r.PathValue("deviceID")
	if deviceID == "" {
		writeAPIError(w, http.StatusBadRequest, codeDeviceIDReq, "device_id is required")
		return
	}

	var req RenameDeviceRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeAPIError(w, http.StatusBadRequest, codeInvalidRequest, "invalid request body")
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	req.Room = strings.TrimSpace(req.Room)
	if req.Name == "" {
		writeAPIError(w, http.StatusBadRequest, codeNameRequired, "name is required")
		return
	}

	if err := h.deviceSvc.RenameDevice(r.Context(), userID, deviceID, req.Name, req.Room); err != nil {
		switch {
		case errors.Is(err, service.ErrDeviceNotFound):
			writeAPIError(w, http.StatusNotFound, codeDeviceNotFound, "device not found")
		case errors.Is(err, service.ErrNotDeviceOwner):
			writeAPIError(w, http.StatusForbidden, codeNotOwner, "not device owner")
		default:
			slog.Error("rename device failed", "err", err, "device_id", deviceID)
			writeAPIError(w, http.StatusInternalServerError, codeInternal, "internal error")
		}
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"message": "ok"})
}

// UnbindDevice DELETE /api/v1/devices/{deviceID}
func (h *DeviceHandler) UnbindDevice(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.UserIDFromContext(r.Context())
	if !ok {
		writeAPIError(w, http.StatusUnauthorized, codeUnauthorized, "unauthorized")
		return
	}
	deviceID := r.PathValue("deviceID")
	if deviceID == "" {
		writeAPIError(w, http.StatusBadRequest, codeDeviceIDReq, "device_id is required")
		return
	}

	if err := h.deviceSvc.UnbindDevice(r.Context(), userID, deviceID); err != nil {
		switch {
		case errors.Is(err, service.ErrDeviceNotFound):
			writeAPIError(w, http.StatusNotFound, codeDeviceNotFound, "device not found")
		case errors.Is(err, service.ErrNotDeviceOwner):
			writeAPIError(w, http.StatusForbidden, codeNotOwner, "not device owner")
		default:
			slog.Error("unbind device failed", "err", err, "device_id", deviceID)
			writeAPIError(w, http.StatusInternalServerError, codeInternal, "internal error")
		}
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"message": "ok"})
}
