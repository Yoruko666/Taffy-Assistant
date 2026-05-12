package handler

import (
	"encoding/json"
	"net/http"
	"time"
)

// HealthWithConfig 是 worker /v1/health 的处理器。
func HealthWithConfig(w http.ResponseWriter, r *http.Request, cfg *AppConfig) {
	llmStatus := "disabled"
	asrStatus := "unknown"
	ttsStatus := "disabled"
	if cfg != nil {
		if cfg.LLM.URL != "" {
			llmStatus = "configured"
		}
		if cfg.ASR.WSURL != "" {
			asrStatus = "configured"
		}
		if cfg.TTS.URL != "" {
			ttsStatus = "configured"
		}
	}
	resp := map[string]any{
		"status":  "ok",
		"service": "shva-worker",
		"version": "0.3.0-split",
		"time":    time.Now().Format(time.RFC3339),
		"components": map[string]string{
			"asr": asrStatus,
			"llm": llmStatus,
			"tts": ttsStatus,
		},
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}
