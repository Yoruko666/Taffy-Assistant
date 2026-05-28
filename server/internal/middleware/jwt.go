package middleware

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/redis/go-redis/v9"

	"taffy-server/internal/config"
)

// Claims 自定义 JWT Claims。
type Claims struct {
	UserID int64 `json:"user_id"`
	jwt.RegisteredClaims
}

// JWTMiddleware JWT 鉴权中间件。
type JWTMiddleware struct {
	secret []byte
	rdb    *redis.Client
}

// NewJWTMiddleware 创建中间件实例。
func NewJWTMiddleware(cfg *config.JWTConfig, rdb *redis.Client) *JWTMiddleware {
	return &JWTMiddleware{
		secret: []byte(cfg.Secret),
		rdb:    rdb,
	}
}

// GenerateAccessToken 生成 Access Token。
func (m *JWTMiddleware) GenerateAccessToken(userID int64, ttl time.Duration) (string, error) {
	now := time.Now()
	claims := Claims{
		UserID: userID,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(now.Add(ttl)),
			IssuedAt:  jwt.NewNumericDate(now),
			Subject:   fmt.Sprintf("%d", userID),
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString(m.secret)
}

// ParseToken 解析并验证 JWT，返回 Claims。
func (m *JWTMiddleware) ParseToken(tokenStr string) (*Claims, error) {
	token, err := jwt.ParseWithClaims(tokenStr, &Claims{}, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
		}
		return m.secret, nil
	})
	if err != nil {
		return nil, err
	}
	claims, ok := token.Claims.(*Claims)
	if !ok || !token.Valid {
		return nil, errors.New("invalid token")
	}

	// 检查黑名单
	if m.rdb != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		key := fmt.Sprintf("token:blacklist:%s", token.Raw)
		exists, _ := m.rdb.Exists(ctx, key).Result()
		if exists > 0 {
			return nil, errors.New("token revoked")
		}
	}

	return claims, nil
}

// RevokeToken 将 token 加入黑名单（注销时调用）。
func (m *JWTMiddleware) RevokeToken(tokenStr string, ttl time.Duration) error {
	if m.rdb == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	key := fmt.Sprintf("token:blacklist:%s", tokenStr)
	return m.rdb.Set(ctx, key, "1", ttl).Err()
}

// RequireAuth 返回一个 HTTP 中间件，校验 Authorization: Bearer <token>。
func (m *JWTMiddleware) RequireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		authHeader := r.Header.Get("Authorization")
		if authHeader == "" {
			http.Error(w, `{"error":"missing authorization header"}`, http.StatusUnauthorized)
			return
		}
		parts := strings.SplitN(authHeader, " ", 2)
		if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
			http.Error(w, `{"error":"invalid authorization format"}`, http.StatusUnauthorized)
			return
		}
		claims, err := m.ParseToken(parts[1])
		if err != nil {
			http.Error(w, `{"error":"invalid or expired token"}`, http.StatusUnauthorized)
			return
		}
		ctx := context.WithValue(r.Context(), CtxKeyUserID, claims.UserID)
		next.ServeHTTP(w, r.WithContext(ctx))
	}
}

// CtxKeyUserID 上下文中 user_id 的 key 实例。
type ctxKeyUserID struct{}

var CtxKeyUserID = ctxKeyUserID{}

// UserIDFromContext 从上下文提取 user_id。
func UserIDFromContext(ctx context.Context) (int64, bool) {
	id, ok := ctx.Value(CtxKeyUserID).(int64)
	return id, ok
}
