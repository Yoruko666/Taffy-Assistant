package handler

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"taffy-server/internal/config"
	"taffy-server/internal/middleware"
	"taffy-server/internal/service"
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

// 业务错误码常量，客户端按 code 翻译本地化文案。
const (
	codeInvalidBody       = "invalid_request_body"
	codePhoneOrEmailReq   = "phone_or_email_required"
	codePhoneRequired     = "phone_required"
	codePasswordTooShort  = "password_too_short"
	codeDuplicatePhone    = "phone_already_registered"
	codeDuplicateEmail    = "email_already_registered"
	codeInvalidCredential = "invalid_phone_or_password"
	codeInternal          = "internal_error"
)

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
		writeAPIError(w, http.StatusBadRequest, codeInvalidBody, "invalid request body")
		return
	}
	if req.Phone == "" && req.Email == "" {
		writeAPIError(w, http.StatusBadRequest, codePhoneOrEmailReq, "phone or email is required")
		return
	}
	if req.Password == "" || len(req.Password) < 6 {
		writeAPIError(w, http.StatusBadRequest, codePasswordTooShort, "password must be at least 6 characters")
		return
	}

	id, err := h.userSvc.Register(r.Context(), req.Phone, req.Email, req.Password, req.Nickname)
	if err != nil {
		if errors.Is(err, service.ErrDuplicatePhone) {
			writeAPIError(w, http.StatusConflict, codeDuplicatePhone, "phone already registered")
			return
		}
		if errors.Is(err, service.ErrDuplicateEmail) {
			writeAPIError(w, http.StatusConflict, codeDuplicateEmail, "email already registered")
			return
		}
		slog.Error("register failed", "err", err)
		writeAPIError(w, http.StatusInternalServerError, codeInternal, "internal error")
		return
	}

	accessToken, err := h.jwtMW.GenerateAccessToken(id, h.cfg.JWT.AccessTTLDuration())
	if err != nil {
		slog.Error("generate token failed", "err", err)
		writeAPIError(w, http.StatusInternalServerError, codeInternal, "internal error")
		return
	}

	resp := TokenResponse{
		AccessToken: accessToken,
		TokenType:   "Bearer",
		ExpiresIn:   int64(h.cfg.JWT.AccessTTLDuration().Seconds()),
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(resp)
}

// Login POST /api/v1/auth/login
func (h *AuthHandler) Login(w http.ResponseWriter, r *http.Request) {
	var req LoginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeAPIError(w, http.StatusBadRequest, codeInvalidBody, "invalid request body")
		return
	}
	if req.Phone == "" {
		writeAPIError(w, http.StatusBadRequest, codePhoneRequired, "phone is required")
		return
	}

	user, err := h.userSvc.Login(r.Context(), req.Phone, req.Password)
	if err != nil {
		if errors.Is(err, service.ErrUserNotFound) || errors.Is(err, service.ErrInvalidPassword) {
			writeAPIError(w, http.StatusUnauthorized, codeInvalidCredential, "invalid phone or password")
			return
		}
		slog.Error("login failed", "err", err)
		writeAPIError(w, http.StatusInternalServerError, codeInternal, "internal error")
		return
	}

	accessToken, err := h.jwtMW.GenerateAccessToken(user.UserID, h.cfg.JWT.AccessTTLDuration())
	if err != nil {
		slog.Error("generate token failed", "err", err)
		writeAPIError(w, http.StatusInternalServerError, codeInternal, "internal error")
		return
	}

	resp := TokenResponse{
		AccessToken: accessToken,
		TokenType:   "Bearer",
		ExpiresIn:   int64(h.cfg.JWT.AccessTTLDuration().Seconds()),
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

// writeAPIError 写入标准 JSON 错误响应：{"error":"<code>","message":"<text>"}。
func writeAPIError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{
		"error":   code,
		"message": message,
	})
}

// writeJSONError 写入仅含 error 字段的简化错误响应。
// Deprecated: 新代码请用 writeAPIError(w, status, code, message)。
func writeJSONError(w http.ResponseWriter, status int, msg string) {
	writeAPIError(w, status, msg, msg)
}
