package handler

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"

	"taffy-server/internal/middleware"
	"taffy-server/internal/model"
	"taffy-server/internal/service"
)

// SceneHandler 场景管理 HTTP 处理器。
type SceneHandler struct {
	sceneSvc  *service.SceneService
	deviceSvc *service.DeviceService
}

func NewSceneHandler(sceneSvc *service.SceneService, deviceSvc *service.DeviceService) *SceneHandler {
	return &SceneHandler{sceneSvc: sceneSvc, deviceSvc: deviceSvc}
}

// ListScenes GET /api/v1/scenes
func (h *SceneHandler) ListScenes(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.UserIDFromContext(r.Context())
	if !ok {
		writeAPIError(w, http.StatusUnauthorized, codeUnauthorized, "unauthorized")
		return
	}

	scenes, err := h.sceneSvc.ListScenes(r.Context(), userID)
	if err != nil {
		slog.Error("list scenes failed", "err", err)
		writeAPIError(w, http.StatusInternalServerError, codeInternal, "internal error")
		return
	}
	if scenes == nil {
		scenes = []*model.Scene{}
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(scenes)
}

// createSceneReq POST /api/v1/scenes 请求体。
type createSceneReq struct {
	Name        string          `json:"name"`
	CommandList json.RawMessage `json:"command_list"`
}

// CreateScene POST /api/v1/scenes
func (h *SceneHandler) CreateScene(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.UserIDFromContext(r.Context())
	if !ok {
		writeAPIError(w, http.StatusUnauthorized, codeUnauthorized, "unauthorized")
		return
	}

	var req createSceneReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_request_body", "invalid request body")
		return
	}

	scene, err := h.sceneSvc.CreateScene(r.Context(), userID, req.Name, req.CommandList)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(scene)
}

// GetScene GET /api/v1/scenes/{id}
func (h *SceneHandler) GetScene(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.UserIDFromContext(r.Context())
	if !ok {
		writeAPIError(w, http.StatusUnauthorized, codeUnauthorized, "unauthorized")
		return
	}

	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeAPIError(w, http.StatusBadRequest, "invalid_scene_id", "invalid scene id")
		return
	}

	scene, err := h.sceneSvc.GetScene(r.Context(), id)
	if err != nil {
		writeAPIError(w, http.StatusNotFound, "scene_not_found", "scene not found")
		return
	}
	if scene.UserID != userID {
		writeAPIError(w, http.StatusForbidden, "not_owner", "not scene owner")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(scene)
}

// UpdateScene PUT /api/v1/scenes/{id}
func (h *SceneHandler) UpdateScene(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.UserIDFromContext(r.Context())
	if !ok {
		writeAPIError(w, http.StatusUnauthorized, codeUnauthorized, "unauthorized")
		return
	}

	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeAPIError(w, http.StatusBadRequest, "invalid_scene_id", "invalid scene id")
		return
	}

	var req createSceneReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_request_body", "invalid request body")
		return
	}

	if err := h.sceneSvc.UpdateScene(r.Context(), userID, id, req.Name, req.CommandList); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}

	scene, _ := h.sceneSvc.GetScene(r.Context(), id)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(scene)
}

// DeleteScene DELETE /api/v1/scenes/{id}
func (h *SceneHandler) DeleteScene(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.UserIDFromContext(r.Context())
	if !ok {
		writeAPIError(w, http.StatusUnauthorized, codeUnauthorized, "unauthorized")
		return
	}

	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeAPIError(w, http.StatusBadRequest, "invalid_scene_id", "invalid scene id")
		return
	}

	if err := h.sceneSvc.DeleteScene(r.Context(), userID, id); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

// TriggerScene POST /api/v1/scenes/{id}/trigger
func (h *SceneHandler) TriggerScene(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.UserIDFromContext(r.Context())
	if !ok {
		writeAPIError(w, http.StatusUnauthorized, codeUnauthorized, "unauthorized")
		return
	}

	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeAPIError(w, http.StatusBadRequest, "invalid_scene_id", "invalid scene id")
		return
	}

	scene, err := h.sceneSvc.GetScene(r.Context(), id)
	if err != nil {
		writeAPIError(w, http.StatusNotFound, "scene_not_found", "scene not found")
		return
	}
	if scene.UserID != userID {
		writeAPIError(w, http.StatusForbidden, "not_owner", "not scene owner")
		return
	}

	results, err := h.sceneSvc.Trigger(r.Context(), id, h.deviceSvc.ExecuteControlDevice)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "trigger_failed", err.Error())
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"status":  "ok",
		"results": results,
	})
}
