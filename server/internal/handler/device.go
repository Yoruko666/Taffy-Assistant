package handler

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"taffy-server/internal/middleware"
	"taffy-server/internal/model"
	"taffy-server/internal/service"
)

// DeviceHandler 设备相关 HTTP 处理器。
type DeviceHandler struct {
	deviceSvc *service.DeviceService
}

// NewDeviceHandler 创建 DeviceHandler。
func NewDeviceHandler(deviceSvc *service.DeviceService) *DeviceHandler {
	return &DeviceHandler{deviceSvc: deviceSvc}
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
