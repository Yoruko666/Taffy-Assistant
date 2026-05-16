package handler

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"time"

	"github.com/redis/go-redis/v9"

	"system/internal/config"
)

// HealthWithConfig 是 server /v1/health 的处理器。
func HealthWithConfig(w http.ResponseWriter, r *http.Request, cfg *config.AppConfig) {
	HealthWithDB(w, r, cfg, nil, nil)
}

// HealthWithDB 带数据库连接的健康检查。
func HealthWithDB(w http.ResponseWriter, r *http.Request, cfg *config.AppConfig, db *sql.DB, rdb *redis.Client) {
	workerStatus := "unknown"
	if cfg != nil && cfg.Worker.WSURL != "" {
		workerStatus = "configured"
	}

	dbStatus := "not_configured"
	if db != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := db.PingContext(ctx); err != nil {
			dbStatus = "error"
		} else {
			dbStatus = "ok"
		}
	}

	redisStatus := "not_configured"
	if rdb != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := rdb.Ping(ctx).Err(); err != nil {
			redisStatus = "error"
		} else {
			redisStatus = "ok"
		}
	}

	resp := map[string]any{
		"status":  "ok",
		"service": "shva-server",
		"version": "0.4.0-db",
		"time":    time.Now().Format(time.RFC3339),
		"components": map[string]string{
			"worker": workerStatus,
			"mqtt":   "unknown",
			"db":     dbStatus,
			"redis":  redisStatus,
		},
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

// Health 是无配置版本的 /v1/health 处理器。
func Health(w http.ResponseWriter, r *http.Request) {
	HealthWithDB(w, r, nil, nil, nil)
}
