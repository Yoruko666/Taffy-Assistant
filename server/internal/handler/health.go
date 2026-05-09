package handler

import (
	"encoding/json"
	"net/http"
	"time"
)

// Health 是 /v1/health 的处理器。
//
// 当前 M1 阶段只汇报自己的状态，后续接入 ASR / TTS / LLM / MQTT 时再扩展子项。
func Health(w http.ResponseWriter, r *http.Request) {
	resp := map[string]any{
		"status":     "ok",
		"service":    "shva-server",
		"version":    "0.1.0-m1",
		"time":       time.Now().Format(time.RFC3339),
		"components": map[string]string{
			// 占位，M3+ 时改成真实探活
			"asr":  "unknown",
			"tts":  "unknown",
			"llm":  "unknown",
			"mqtt": "unknown",
		},
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}
