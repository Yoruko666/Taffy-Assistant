package handler

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"taffy.local/pkg/protocol"
	"taffy.local/pkg/wsutil"
)

// OrchestrateHandler 实现 Server ↔ Worker 之间的 WS 端点 /v1/orchestrate。
//
// 上行（server → worker）：
//
//	text   {"type":"start", ...}
//	binary <16-bit LE PCM mono>
//	text   {"type":"end" | "ping" | "device_info" | "device_command_result"}
//
// 下行（worker → server）：
//
//	text   {"type":"asr_partial" | "asr_final" | "eos"}
//	text   {"type":"llm_result" | "llm_error"}
//	text   {"type":"device_command" | "tts_audio" | "error" | "pong"}
//
// 协议结构体见 pkg/protocol。
type OrchestrateHandler struct {
	cfg      *AppConfig
	upgrader websocket.Upgrader
	dialer   *websocket.Dialer
}

// NewOrchestrateHandler 构造 worker orchestrate handler。
func NewOrchestrateHandler(cfg *AppConfig) *OrchestrateHandler {
	return &OrchestrateHandler{
		cfg:      cfg,
		upgrader: wsutil.DefaultUpgrader(),
		dialer:   wsutil.DefaultDialer(),
	}
}

func (h *OrchestrateHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	deviceID := r.URL.Query().Get("device_id")
	sessionID := r.URL.Query().Get("session_id")
	log := slog.With("path", "/v1/orchestrate", "device_id", deviceID, "session_id", sessionID, "remote", r.RemoteAddr)

	upConn, err := h.upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Error("upgrade failed", "err", err)
		return
	}
	defer upConn.Close()
	log.Info("upstream connected")

	asrURL := ""
	if h.cfg != nil {
		asrURL = h.cfg.ASR.WSURL
	}
	if asrURL == "" {
		log.Error("asr ws url not configured")
		_ = wsutil.WriteJSON(upConn, map[string]any{"type": "error", "message": "asr backend not configured"})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	asrConn, _, err := h.dialer.DialContext(ctx, asrURL, nil)
	if err != nil {
		log.Error("dial asr failed", "asr", asrURL, "err", err)
		_ = wsutil.WriteJSON(upConn, map[string]any{"type": "error", "message": "asr backend unreachable"})
		return
	}
	defer asrConn.Close()
	log.Info("asr connected", "asr", asrURL)

	session := NewSession(upConn, log)
	h.pumpSession(session, asrConn)
	log.Info("session done")
}

// pumpSession 启动两条 goroutine：
//
//	upstream → asr：拦截 ping / device_info / device_command_result，其余透传给 ASR；
//	asr → upstream：翻译 ASR 事件，asr_final 时异步触发 LLM。
//
// 任一侧关闭即返回；wg.Wait 之前用 100ms 读 deadline 唤醒另一条 goroutine。
func (h *OrchestrateHandler) pumpSession(s *Session, asrConn *websocket.Conn) {
	stop := make(chan struct{})
	var once sync.Once
	closeStop := func() { once.Do(func() { close(stop) }) }

	wg := &sync.WaitGroup{}
	wg.Add(2)

	go func() {
		defer wg.Done()
		defer closeStop()
		h.pumpUpstreamToASR(s, asrConn)
	}()
	go func() {
		defer wg.Done()
		defer closeStop()
		h.pumpASRToUpstream(s, asrConn)
	}()

	<-stop
	_ = s.upConn.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
	_ = asrConn.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
	wg.Wait()
}

// pumpUpstreamToASR：server → worker → ASR。
// 拦截 ping / device_info / device_command_result，其余直接透传到 ASR。
func (h *OrchestrateHandler) pumpUpstreamToASR(s *Session, asrConn *websocket.Conn) {
	log := s.Log()
	for {
		mt, data, err := s.upConn.ReadMessage()
		if err != nil {
			if wsutil.IsNormalClose(err) {
				log.Info("upstream closed")
			} else {
				log.Warn("upstream read err", "err", err)
			}
			_ = asrConn.WriteControl(
				websocket.CloseMessage,
				websocket.FormatCloseMessage(websocket.CloseNormalClosure, "upstream gone"),
				time.Now().Add(time.Second),
			)
			return
		}

		// 拦截非音频消息
		if mt == websocket.TextMessage && h.handleUpstreamControl(s, data) {
			continue
		}

		// 其他消息（start / end / binary PCM）透传给 ASR
		if err := asrConn.WriteMessage(mt, data); err != nil {
			log.Warn("asr write err", "err", err)
			return
		}
	}
}

// handleUpstreamControl 处理来自 server 的控制类文本帧，返回 true 表示已被消费、不再透传给 ASR。
func (h *OrchestrateHandler) handleUpstreamControl(s *Session, data []byte) bool {
	var ev map[string]any
	if err := json.Unmarshal(data, &ev); err != nil {
		return false
	}
	t, _ := ev["type"].(string)

	switch t {
	case "ping":
		_ = s.WriteJSON(map[string]any{"type": "pong"})
		return true

	case protocol.EventTypeDeviceInfo:
		raw, _ := json.Marshal(ev)
		s.SetDeviceContext(raw)
		return true

	case protocol.EventTypeDeviceCommandResult:
		var result DeviceCommandResultEvent
		raw, _ := json.Marshal(ev)
		if err := json.Unmarshal(raw, &result); err == nil {
			go h.handleCommandResult(s, result)
		}
		return true
	}
	return false
}

// pumpASRToUpstream：ASR → worker → server。
// 翻译 ASR 事件命名（partial→asr_partial / final→asr_final），并在 asr_final 时异步触发 LLM。
func (h *OrchestrateHandler) pumpASRToUpstream(s *Session, asrConn *websocket.Conn) {
	log := s.Log()
	for {
		mt, data, err := asrConn.ReadMessage()
		if err != nil {
			if wsutil.IsNormalClose(err) {
				log.Info("asr closed")
			} else {
				log.Warn("asr read err", "err", err)
			}
			return
		}

		// 二进制直接透传
		if mt != websocket.TextMessage {
			if err := s.WriteRaw(mt, data); err != nil {
				log.Warn("upstream write(bin) err", "err", err)
				return
			}
			continue
		}

		// 翻译 ASR 事件并发送
		out := translateASREvent(data)
		if err := s.WriteRaw(websocket.TextMessage, out); err != nil {
			log.Warn("upstream write err", "err", err)
			return
		}

		// asr_final → 异步调用大模型（带 Tool Calling）
		if h.cfg != nil && h.cfg.LLM.URL != "" {
			h.maybeTriggerLLM(s, out)
		}
	}
}

// maybeTriggerLLM 检查 out 是否为 asr_final，若是则异步调用 LLM 主链路。
func (h *OrchestrateHandler) maybeTriggerLLM(s *Session, out []byte) {
	var ev map[string]any
	if err := json.Unmarshal(out, &ev); err != nil {
		return
	}
	if t, _ := ev["type"].(string); t != "asr_final" {
		return
	}
	text, _ := ev["text"].(string)
	if text == "" {
		s.Log().Info("asr_final empty text, skip llm")
		_ = s.WriteJSON(map[string]any{"type": "llm_result", "text": ""})
		return
	}
	devices, scenes := s.SnapshotDeviceContext()
	go h.handleLLMWithTools(s, text, devices, scenes)
}

// handleLLMWithTools 调用大模型（带 Tool Calling），处理 tool_calls 结果。
func (h *OrchestrateHandler) handleLLMWithTools(s *Session, userText string, devices []DeviceContext, scenes []SceneContext) {
	log := s.Log()
	t0 := time.Now()
	resp, err := h.callLLMWithTools(userText, devices, scenes)
	cost := time.Since(t0).Milliseconds()

	if err != nil {
		log.Warn("llm call failed", "err", err, "cost_ms", cost)
		_ = s.WriteJSON(map[string]any{"type": "llm_error", "message": err.Error()})
		return
	}

	if len(resp.Choices) == 0 {
		log.Warn("llm response has no choices", "cost_ms", cost)
		_ = s.WriteJSON(map[string]any{"type": "llm_result", "text": ""})
		return
	}

	choice := resp.Choices[0]
	log.Info("llm ok", "cost_ms", cost, "finish_reason", choice.FinishReason, "has_tool_calls", len(choice.Message.ToolCalls) > 0)

	if len(choice.Message.ToolCalls) > 0 {
		for _, tc := range choice.Message.ToolCalls {
			log.Info("tool call requested", "function", tc.Function.Name, "args", tc.Function.Arguments, "tool_id", tc.ID)
			s.RememberToolCall(tc.ID, tc.Function.Name, tc.Function.Arguments)
			_ = s.WriteJSON(DeviceCommandEvent{
				Type:     protocol.EventTypeDeviceCommand,
				ToolID:   tc.ID,
				Function: tc.Function.Name,
				Params:   json.RawMessage(tc.Function.Arguments),
			})
		}
		// 等待 device_command_result 回来后再做二次调用，不立即发 llm_result。
		return
	}

	replyText := choice.Message.Content
	_ = s.WriteJSON(map[string]any{"type": "llm_result", "text": replyText})

	go h.synthesizeAndSendTTS(s, replyText)
}

// handleCommandResult 处理 Server 返回的指令执行结果，触发二次 LLM 调用生成最终回复。
func (h *OrchestrateHandler) handleCommandResult(s *Session, result DeviceCommandResultEvent) {
	log := s.Log()
	log.Info("command result received", "tool_id", result.ToolID, "success", result.Success, "message", result.Message)

	pending, ok := s.PopToolCall(result.ToolID)
	if !ok {
		// 无缓存通常是脏数据 / 重复回执，用安全默认值兜底。
		log.Warn("no pending tool call found for result", "tool_id", result.ToolID)
		pending = pendingToolCall{Name: protocol.FunctionControlDevice, Arguments: "{}"}
	}

	devices, scenes := s.SnapshotDeviceContext()

	resultContent := "执行成功"
	if !result.Success {
		resultContent = "执行失败: " + result.Message
	} else if result.Message != "" {
		resultContent = result.Message
	}

	t0 := time.Now()
	reply, err := h.callLLMSecondRound(resultContent, result.ToolID, pending, devices, scenes)
	cost := time.Since(t0).Milliseconds()

	if err != nil {
		log.Warn("second llm call failed", "err", err, "cost_ms", cost)
		_ = s.WriteJSON(map[string]any{"type": "llm_error", "message": err.Error()})
		return
	}

	log.Info("second llm ok", "cost_ms", cost, "reply_len", len(reply))
	_ = s.WriteJSON(map[string]any{"type": "llm_result", "text": reply})

	go h.synthesizeAndSendTTS(s, reply)
}
