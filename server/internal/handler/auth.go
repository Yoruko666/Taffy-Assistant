package handler

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"system/internal/config"
	"system/internal/middleware"
	"system/internal/service"
)

// AuthHandler 认证相关 HTTP 处理器。
type AuthHandler struct {
	userSvc *service.UserService
	jwtMW   *middleware.JWTMiddleware
	cfg     *config.AppConfig
}

// NewAuthHandler 创建 AuthHandler。
func NewAuthHandler(userSvc *service.UserService, jwtMW *middleware.JWTMiddleware, cfg *config.AppConfig) *AuthHandler {
	return &AuthHandler{userSvc: userSvc, jwtMW: jwtMW, cfg: cfg}
}

// RegisterRequest 注册请求体。
type RegisterRequest struct {
	Phone    string `json:"phone"`
	Email    string `json:"email"`
	Password string `json:"password"`
	Nickname string `json:"nickname"`
}

// LoginRequest 登录请求体。
type LoginRequest struct {
	Phone    string `json:"phone"`
	Password string `json:"password"`
}

// TokenResponse 令牌响应。
type TokenResponse struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	ExpiresIn   int64  `json:"expires_in"` // 秒
}

// Register POST /api/v1/auth/register
func (h *AuthHandler) Register(w http.ResponseWriter, r *http.Request) {
	var req RegisterRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Phone == "" && req.Email == "" {
		writeJSONError(w, http.StatusBadRequest, "phone or email is required")
		return
	}
	if req.Password == "" || len(req.Password) < 6 {
		writeJSONError(w, http.StatusBadRequest, "password must be at least 6 characters")
		return
	}

	id, err := h.userSvc.Register(r.Context(), req.Phone, req.Email, req.Password, req.Nickname)
	if err != nil {
		if errors.Is(err, service.ErrDuplicatePhone) {
			writeJSONError(w, http.StatusConflict, "phone already registered")
			return
		}
		if errors.Is(err, service.ErrDuplicateEmail) {
			writeJSONError(w, http.StatusConflict, "email already registered")
			return
		}
		slog.Error("register failed", "err", err)
		writeJSONError(w, http.StatusInternalServerError, "internal error")
		return
	}

	accessToken, err := h.jwtMW.GenerateAccessToken(id, h.cfg.JWT.AccessTTLDuration())
	if err != nil {
		slog.Error("generate token failed", "err", err)
		writeJSONError(w, http.StatusInternalServerError, "internal error")
		return
	}

	resp := TokenResponse{
		AccessToken: accessToken,
		TokenType:   "Bearer",
		ExpiresIn:   int64(h.cfg.JWT.AccessTTLDuration().Seconds()),
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(resp)
}

// Login POST /api/v1/auth/login
func (h *AuthHandler) Login(w http.ResponseWriter, r *http.Request) {
	var req LoginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Phone == "" {
		writeJSONError(w, http.StatusBadRequest, "phone is required")
		return
	}

	user, err := h.userSvc.Login(r.Context(), req.Phone, req.Password)
	if err != nil {
		if errors.Is(err, service.ErrUserNotFound) || errors.Is(err, service.ErrInvalidPassword) {
			writeJSONError(w, http.StatusUnauthorized, "invalid phone or password")
			return
		}
		slog.Error("login failed", "err", err)
		writeJSONError(w, http.StatusInternalServerError, "internal error")
		return
	}

	accessToken, err := h.jwtMW.GenerateAccessToken(user.UserID, h.cfg.JWT.AccessTTLDuration())
	if err != nil {
		slog.Error("generate token failed", "err", err)
		writeJSONError(w, http.StatusInternalServerError, "internal error")
		return
	}

	resp := TokenResponse{
		AccessToken: accessToken,
		TokenType:   "Bearer",
		ExpiresIn:   int64(h.cfg.JWT.AccessTTLDuration().Seconds()),
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// writeJSONError 写入 JSON 错误响应。
func writeJSONError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
