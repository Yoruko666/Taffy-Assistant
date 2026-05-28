package handler

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"time"

	"github.com/redis/go-redis/v9"

	"taffy-server/internal/config"
)

// HealthWithDB 是 /v1/health 处理器。
// status 字段恒为 ok；分量在 components 里以 configured / ok / error / not_configured 体现。
func HealthWithDB(
	w http.ResponseWriter,
	_ *http.Request,
	cfg *config.AppConfig,
	db *sql.DB,
	rdb *redis.Client,
	mqttStatus func() string,
) {
	workerStatus := "unknown"
	if cfg != nil && cfg.Worker.WSURL != "" {
		workerStatus = "configured"
	}

	resp := map[string]any{
		"status":  "ok",
		"service": "taffy-server",
		"version": "0.5.0-mqtt",
		"time":    time.Now().Format(time.RFC3339),
		"components": map[string]string{
			"worker": workerStatus,
			"db":     pingDB(db),
			"redis":  pingRedis(rdb),
			"mqtt":   resolveMQTTStatus(mqttStatus),
		},
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

func pingDB(db *sql.DB) string {
	if db == nil {
		return "not_configured"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		return "error"
	}
	return "ok"
}

func pingRedis(rdb *redis.Client) string {
	if rdb == nil {
		return "not_configured"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := rdb.Ping(ctx).Err(); err != nil {
		return "error"
	}
	return "ok"
}

func resolveMQTTStatus(probe func() string) string {
	if probe == nil {
		return "not_configured"
	}
	return probe()
}

