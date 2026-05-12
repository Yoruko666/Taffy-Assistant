package handler

import (
	"encoding/json"
	"net/http"
	"time"
)

// HealthWithConfig 是 server /v1/health 的处理器。
// 拆分后 Server 不再直接持有 LLM/ASR/TTS 状态，这些都集中在 Worker 的健康检查里。
func HealthWithConfig(w http.ResponseWriter, r *http.Request, cfg *AppConfig) {
	workerStatus := "unknown"
	if cfg != nil && cfg.Worker.WSURL != "" {
		workerStatus = "configured"
	}
	resp := map[string]any{
		"status":  "ok",
		"service": "shva-server",
		"version": "0.3.0-split",
		"time":    time.Now().Format(time.RFC3339),
		"components": map[string]string{
			"worker": workerStatus,
			"mqtt":   "unknown",
			"db":     "unknown",
		},
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

// Health 是无配置版本的 /v1/health 处理器。
func Health(w http.ResponseWriter, r *http.Request) {
	HealthWithConfig(w, r, nil)
}
