package handler

import (
	"encoding/json"
	"net/http"
	"time"
)

// HealthWithConfig 是 /v1/health 的处理器（带 LLM 配置信息）。
func HealthWithConfig(w http.ResponseWriter, r *http.Request, cfg *AppConfig) {
	llmStatus := "disabled"
	if cfg != nil && cfg.LLM.URL != "" {
		llmStatus = "configured"
	}
	resp := map[string]any{
		"status":     "ok",
		"service":    "shva-server",
		"version":    "0.2.0-m2",
		"time":       time.Now().Format(time.RFC3339),
		"components": map[string]string{
			"asr":  "unknown",
			"tts":  "unknown",
			"llm":  llmStatus,
			"mqtt": "unknown",
		},
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

// Health 是旧版 /v1/health 处理器（无配置时使用）。
func Health(w http.ResponseWriter, r *http.Request) {
	HealthWithConfig(w, r, nil)
}
