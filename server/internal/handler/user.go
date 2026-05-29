package handler

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"taffy-server/internal/middleware"
	"taffy-server/internal/service"
)

// UserHandler 用户相关 HTTP 处理器。
type UserHandler struct {
	userSvc *service.UserService
}

func NewUserHandler(userSvc *service.UserService) *UserHandler {
	return &UserHandler{userSvc: userSvc}
}

// GetProfile GET /api/v1/users/me
func (h *UserHandler) GetProfile(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.UserIDFromContext(r.Context())
	if !ok {
		writeAPIError(w, http.StatusUnauthorized, codeUnauthorized, "unauthorized")
		return
	}

	u, err := h.userSvc.GetUser(r.Context(), userID)
	if err != nil {
		slog.Error("get user failed", "err", err)
		writeAPIError(w, http.StatusInternalServerError, codeInternal, "internal error")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(u)
}

// updateUserReq PATCH /api/v1/users/me 请求体。
type updateUserReq struct {
	Nickname  string `json:"nickname"`
	Email     string `json:"email"`
	AvatarURL string `json:"avatar_url"`
}

// UpdateProfile PATCH /api/v1/users/me
func (h *UserHandler) UpdateProfile(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.UserIDFromContext(r.Context())
	if !ok {
		writeAPIError(w, http.StatusUnauthorized, codeUnauthorized, "unauthorized")
		return
	}

	var req updateUserReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_request_body", "invalid request body")
		return
	}

	if err := h.userSvc.UpdateUser(r.Context(), userID, req.Nickname, req.Email, req.AvatarURL); err != nil {
		slog.Error("update user failed", "err", err)
		writeAPIError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}

	u, _ := h.userSvc.GetUser(r.Context(), userID)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(u)
}

// changePasswordReq POST /api/v1/users/me/password 请求体。
type changePasswordReq struct {
	OldPassword string `json:"old_password"`
	NewPassword string `json:"new_password"`
}

// ChangePassword POST /api/v1/users/me/password
func (h *UserHandler) ChangePassword(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.UserIDFromContext(r.Context())
	if !ok {
		writeAPIError(w, http.StatusUnauthorized, codeUnauthorized, "unauthorized")
		return
	}

	var req changePasswordReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_request_body", "invalid request body")
		return
	}

	if err := h.userSvc.ChangePassword(r.Context(), userID, req.OldPassword, req.NewPassword); err != nil {
		if errors.Is(err, service.ErrInvalidPassword) {
			writeAPIError(w, http.StatusUnauthorized, "invalid_password", "invalid password")
			return
		}
		if errors.Is(err, service.ErrWeakPassword) {
			writeAPIError(w, http.StatusBadRequest, "weak_password", err.Error())
			return
		}
		slog.Error("change password failed", "err", err)
		writeAPIError(w, http.StatusInternalServerError, codeInternal, "internal error")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

// deleteProfileReq DELETE /api/v1/users/me 请求体。
type deleteProfileReq struct {
	Password string `json:"password"`
}

// DeleteProfile DELETE /api/v1/users/me
func (h *UserHandler) DeleteProfile(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.UserIDFromContext(r.Context())
	if !ok {
		writeAPIError(w, http.StatusUnauthorized, codeUnauthorized, "unauthorized")
		return
	}

	var req deleteProfileReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_request_body", "invalid request body")
		return
	}

	if _, err := h.userSvc.VerifyPassword(r.Context(), userID, req.Password); err != nil {
		writeAPIError(w, http.StatusUnauthorized, "invalid_password", "invalid password")
		return
	}

	if err := h.userSvc.DeleteUser(r.Context(), userID); err != nil {
		slog.Error("delete user failed", "err", err)
		writeAPIError(w, http.StatusInternalServerError, codeInternal, "internal error")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}
