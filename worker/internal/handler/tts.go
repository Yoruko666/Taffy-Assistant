package handler

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// TTS 合成超时（秒）。Piper huayan-medium 一句中文大约 0.5~1.5s，
// 留 15s 上限足够覆盖长句。
const ttsHTTPTimeout = 15 * time.Second

// synthesizeAndSendTTS 调用本地 TTS 服务合成 wav，并通过 WS 下发 tts_audio 事件。
//
// 该函数应在 llm_result 已经发送之后调用，**不阻塞**主回复链路：
//   - 即便 TTS 失败，用户也已经看到/收到了文本回复；
//   - 失败仅打 warn 日志，不发 error 事件给端侧（避免端侧把 TTS 异常当业务异常处理）。
//
// 协议（家具端 receiver 已支持）：
//
//	{"type":"tts_audio","format":"wav","data":"<base64 wav>"}
func (h *OrchestrateHandler) synthesizeAndSendTTS(
	conn *websocket.Conn,
	text string,
	writeMu *sync.Mutex,
	log *slog.Logger,
) {
	if h.cfg == nil || h.cfg.TTS.URL == "" {
		// TTS 未配置，跳过（保持纯文本链路可用）
		return
	}
	if text == "" {
		return
	}

	t0 := time.Now()
	wavBytes, err := callTTS(h.cfg.TTS.URL, text)
	cost := time.Since(t0).Milliseconds()
	if err != nil {
		log.Warn("tts synthesize failed", "err", err, "cost_ms", cost, "text_len", len(text))
		return
	}
	log.Info("tts synthesize ok", "cost_ms", cost, "text_len", len(text), "wav_bytes", len(wavBytes))

	encoded := base64.StdEncoding.EncodeToString(wavBytes)
	writeMu.Lock()
	werr := writeJSON(conn, map[string]any{
		"type":   "tts_audio",
		"format": "wav",
		"data":   encoded,
	})
	writeMu.Unlock()
	if werr != nil {
		log.Warn("tts send failed", "err", werr)
	}
}

// callTTS 向本地 TTS 服务发起合成请求，返回 wav 字节流。
func callTTS(url string, text string) ([]byte, error) {
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

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("tts http: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		// 失败时 TTS 服务返回的是 JSON {"error":"...","message":"..."}
		errBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, errors.New("tts status " + resp.Status + ": " + string(errBody))
	}

	// 防御：限制最大读取，避免 TTS 异常返回巨大流。
	// 30s 22.05kHz 16bit 单声道 ≈ 1.3MB，给 8MB 上限足够。
	const maxWavBytes = 8 * 1024 * 1024
	wav, err := io.ReadAll(io.LimitReader(resp.Body, maxWavBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read tts body: %w", err)
	}
	if len(wav) > maxWavBytes {
		return nil, fmt.Errorf("tts response too large: > %d bytes", maxWavBytes)
	}
	if len(wav) < 44 {
		// 一个合法 wav 至少要有 44 字节 RIFF header
		return nil, errors.New("tts response too short, not a valid wav")
	}
	return wav, nil
}
