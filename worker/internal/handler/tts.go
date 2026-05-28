package handler

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

// TTS 合成超时（秒）。Piper huayan-medium 单句 0.5~1.5s，15s 上限足够覆盖长句。
const ttsHTTPTimeout = 15 * time.Second

// maxWavBytes 防御异常 TTS 流。30s 22.05kHz 16bit 单声道 ≈ 1.3MB。
const maxWavBytes = 8 * 1024 * 1024

// ttsHTTPClient 是 worker 内 TTS 调用共用的 HTTP 客户端。
var ttsHTTPClient = &http.Client{}

// synthesizeAndSendTTS 调用本地 TTS 合成 wav，并通过 WS 下发 tts_audio 事件。
//
// 应在 llm_result 已发送之后调用，不阻塞主回复链路：
// 失败仅 warn 日志，不发 error 事件（避免端侧把 TTS 异常当业务异常处理）。
//
// 协议：{"type":"tts_audio","format":"wav","data":"<base64 wav>"}
func (h *OrchestrateHandler) synthesizeAndSendTTS(s *Session, text string) {
	if h.cfg == nil || h.cfg.TTS.URL == "" {
		return
	}
	if text == "" {
		return
	}

	log := s.Log()
	t0 := time.Now()
	wavBytes, err := callTTS(h.cfg.TTS.URL, text)
	cost := time.Since(t0).Milliseconds()
	if err != nil {
		log.Warn("tts synthesize failed", "err", err, "cost_ms", cost, "text_len", len(text))
		return
	}
	log.Info("tts synthesize ok", "cost_ms", cost, "text_len", len(text), "wav_bytes", len(wavBytes))

	encoded := base64.StdEncoding.EncodeToString(wavBytes)
	if err := s.WriteJSON(map[string]any{
		"type":   "tts_audio",
		"format": "wav",
		"data":   encoded,
	}); err != nil {
		log.Warn("tts send failed", "err", err)
	}
}

// callTTS 向本地 TTS 服务发起合成请求，返回 wav 字节流。
func callTTS(url, text string) ([]byte, error) {
	body, err := json.Marshal(map[string]any{
		"text":   text,
		"format": "wav",
	})
	if err != nil {
		return nil, fmt.Errorf("marshal tts req: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), ttsHTTPTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("build tts req: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "audio/wav")

	resp, err := ttsHTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("tts http: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		// 失败时 TTS 服务返回 JSON {"error":"...","message":"..."}
		errBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, errors.New("tts status " + resp.Status + ": " + string(errBody))
	}

	wav, err := io.ReadAll(io.LimitReader(resp.Body, maxWavBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read tts body: %w", err)
	}
	if len(wav) > maxWavBytes {
		return nil, fmt.Errorf("tts response too large: > %d bytes", maxWavBytes)
	}
	if len(wav) < 44 {
		// 合法 wav 至少 44 字节 RIFF header
		return nil, errors.New("tts response too short, not a valid wav")
	}
	return wav, nil
}
