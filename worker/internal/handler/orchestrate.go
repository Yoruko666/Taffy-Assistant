package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// OrchestrateHandler 实现 Server ↔ Worker 之间的 WS 端点（/v1/orchestrate）。
//
// 协议与"家具端 ↔ Server"完全一致，Server 只在中间做透传：
//
//	上行（server → worker）：
//	  text   {"type":"start", ...}
//	  binary <16-bit LE PCM mono 字节流>
//	  text   {"type":"end"}
//	  text   {"type":"ping"}
//	  text   {"type":"device_info","devices":[...],"scenes":[...]}   ← 新增：设备上下文
//	  text   {"type":"device_command_result","tool_id":"...","success":true,"message":"..."}  ← 新增：指令执行结果
//
//	下行（worker → server）：
//	  text   {"type":"asr_partial","text":"..."}
//	  text   {"type":"asr_final","text":"..."}
//	  text   {"type":"eos"}
//	  text   {"type":"llm_result","text":"..."}
//	  text   {"type":"llm_error","message":"..."}
//	  text   {"type":"device_command","tool_id":"...","function":"control_device","params":{...}}  ← 新增：设备控制指令
//	  text   {"type":"error","message":"..."}
//	  text   {"type":"pong"}
type OrchestrateHandler struct {
	cfg      *AppConfig
	upgrader websocket.Upgrader
	dialer   *websocket.Dialer

	// 会话级设备上下文（由 Server 在会话开始时推送）
	mu      sync.Mutex
	devices []DeviceContext
	scenes  []SceneContext
}

// NewOrchestrateHandler 构造 worker orchestrate handler。
func NewOrchestrateHandler(cfg *AppConfig) *OrchestrateHandler {
	return &OrchestrateHandler{
		cfg: cfg,
		upgrader: websocket.Upgrader{
			ReadBufferSize:  64 * 1024,
			WriteBufferSize: 64 * 1024,
			CheckOrigin:     func(*http.Request) bool { return true },
		},
		dialer: &websocket.Dialer{
			HandshakeTimeout: 10 * time.Second,
			ReadBufferSize:   64 * 1024,
			WriteBufferSize:  64 * 1024,
		},
	}
}

func (h *OrchestrateHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	deviceID := r.URL.Query().Get("device_id")
	sessionID := r.URL.Query().Get("session_id")
	log := slog.With("path", "/v1/orchestrate", "device_id", deviceID, "session_id", sessionID, "remote", r.RemoteAddr)

	// 1) 升级上游（server）连接
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
		_ = writeJSON(upConn, map[string]any{
			"type":    "error",
			"message": "asr backend not configured",
		})
		return
	}

	// 2) 拨号到 ASR
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	asrConn, _, err := h.dialer.DialContext(ctx, asrURL, nil)
	if err != nil {
		log.Error("dial asr failed", "asr", asrURL, "err", err)
		_ = writeJSON(upConn, map[string]any{
			"type":    "error",
			"message": "asr backend unreachable",
		})
		return
	}
	defer asrConn.Close()
	log.Info("asr connected", "asr", asrURL)

	// 3) 双向转发 + LLM 处理
	stop := make(chan struct{})
	var once sync.Once
	closeStop := func() { once.Do(func() { close(stop) }) }

	// 保护 upConn 写入（ASR 回传 goroutine + LLM goroutine 可能同时写）
	var writeMu sync.Mutex
	safeWriteUp := func(v any) error {
		writeMu.Lock()
		defer writeMu.Unlock()
		b, err := json.Marshal(v)
		if err != nil {
			return err
		}
		return upConn.WriteMessage(websocket.TextMessage, b)
	}

	// 会话级设备上下文存储
	var sessionDevices []DeviceContext
	var sessionScenes []SceneContext
	var sessionMu sync.Mutex

	wg := &sync.WaitGroup{}
	wg.Add(2)

	// ---------- upstream -> asr ----------
	go func() {
		defer wg.Done()
		defer closeStop()
		for {
			mt, data, err := upConn.ReadMessage()
			if err != nil {
				if isNormalClose(err) {
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
			if mt == websocket.TextMessage {
				var ev map[string]any
				if err := json.Unmarshal(data, &ev); err == nil {
					t, _ := ev["type"].(string)

					// 保活
					if t == "ping" {
						_ = safeWriteUp(map[string]any{"type": "pong"})
						continue
					}

					// 拦截 device_info：保存设备上下文，不转发给 ASR
					if t == "device_info" {
						sessionMu.Lock()
						raw, _ := json.Marshal(ev)
						var info DeviceInfoEvent
						if err := json.Unmarshal(raw, &info); err == nil {
							sessionDevices = info.Devices
							sessionScenes = info.Scenes
							log.Info("device context received", "devices", len(sessionDevices), "scenes", len(sessionScenes))
						}
						sessionMu.Unlock()
						continue
					}

					// 拦截 device_command_result：处理指令执行结果，触发二次 LLM 调用
					if t == "device_command_result" {
						var result DeviceCommandResultEvent
						raw, _ := json.Marshal(ev)
						if err := json.Unmarshal(raw, &result); err == nil {
							go h.handleCommandResult(upConn, result, &writeMu, &sessionMu, &sessionDevices, &sessionScenes, log)
						}
						continue
					}
				}
			}

			// 其他消息（start / end / binary PCM）透传给 ASR
			if err := asrConn.WriteMessage(mt, data); err != nil {
				log.Warn("asr write err", "err", err)
				return
			}
		}
	}()

	// ---------- asr -> upstream ----------
	go func() {
		defer wg.Done()
		defer closeStop()
		for {
			mt, data, err := asrConn.ReadMessage()
			if err != nil {
				if isNormalClose(err) {
					log.Info("asr closed")
				} else {
					log.Warn("asr read err", "err", err)
				}
				return
			}

			// 二进制直接透传
			if mt != websocket.TextMessage {
				writeMu.Lock()
				werr := upConn.WriteMessage(mt, data)
				writeMu.Unlock()
				if werr != nil {
					log.Warn("upstream write(bin) err", "err", werr)
					return
				}
				continue
			}

			// 翻译 ASR 事件并发送
			out := translateASREvent(data)
			writeMu.Lock()
			werr := upConn.WriteMessage(websocket.TextMessage, out)
			writeMu.Unlock()
			if werr != nil {
				log.Warn("upstream write err", "err", werr)
				return
			}

			// 收到 asr_final → 异步调用大模型（带 Tool Calling）
			if h.cfg != nil && h.cfg.LLM.URL != "" {
				var ev map[string]any
				if err := json.Unmarshal(out, &ev); err == nil {
					if t, _ := ev["type"].(string); t == "asr_final" {
						if text, ok := ev["text"].(string); ok && text != "" {
							// 获取当前设备上下文快照
							sessionMu.Lock()
							devicesCopy := make([]DeviceContext, len(sessionDevices))
							copy(devicesCopy, sessionDevices)
							scenesCopy := make([]SceneContext, len(sessionScenes))
							copy(scenesCopy, sessionScenes)
							sessionMu.Unlock()

							go h.handleLLMWithTools(upConn, text, &writeMu, devicesCopy, scenesCopy, log)
						} else {
							log.Info("asr_final empty text, skip llm")
							writeMu.Lock()
							_ = writeJSON(upConn, map[string]any{
								"type": "llm_result",
								"text": "",
							})
							writeMu.Unlock()
						}
					}
				}
			}
		}
	}()

	// 等任意一边结束
	<-stop
	_ = upConn.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
	_ = asrConn.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
	wg.Wait()
	log.Info("session done")
}

// handleLLMWithTools 调用大模型（带 Tool Calling），处理 tool_calls 结果。
func (h *OrchestrateHandler) handleLLMWithTools(
	conn *websocket.Conn,
	userText string,
	writeMu *sync.Mutex,
	devices []DeviceContext,
	scenes []SceneContext,
	log *slog.Logger,
) {
	t0 := time.Now()
	resp, err := h.callLLMWithTools(userText, devices, scenes)
	cost := time.Since(t0).Milliseconds()

	if err != nil {
		log.Warn("llm call failed", "err", err, "cost_ms", cost)
		writeMu.Lock()
		_ = writeJSON(conn, map[string]any{
			"type":    "llm_error",
			"message": err.Error(),
		})
		writeMu.Unlock()
		return
	}

	if len(resp.Choices) == 0 {
		log.Warn("llm response has no choices", "cost_ms", cost)
		writeMu.Lock()
		_ = writeJSON(conn, map[string]any{
			"type": "llm_result",
			"text": "",
		})
		writeMu.Unlock()
		return
	}

	choice := resp.Choices[0]
	log.Info("llm ok", "cost_ms", cost, "finish_reason", choice.FinishReason, "has_tool_calls", len(choice.Message.ToolCalls) > 0)

	// 如果 LLM 请求调用工具（设备控制），下发 device_command 给 Server
	if len(choice.Message.ToolCalls) > 0 {
		for _, tc := range choice.Message.ToolCalls {
			log.Info("tool call requested", "function", tc.Function.Name, "args", tc.Function.Arguments, "tool_id", tc.ID)

			writeMu.Lock()
			_ = writeJSON(conn, DeviceCommandEvent{
				Type:     "device_command",
				ToolID:   tc.ID,
				Function: tc.Function.Name,
				Params:   json.RawMessage(tc.Function.Arguments),
			})
			writeMu.Unlock()
		}
		// 不立即发送 llm_result，等待 device_command_result 回来后再做二次调用
		return
	}

	// 纯文本回复
	writeMu.Lock()
	_ = writeJSON(conn, map[string]any{
		"type": "llm_result",
		"text": choice.Message.Content,
	})
	writeMu.Unlock()
}

// handleCommandResult 处理 Server 返回的指令执行结果，触发二次 LLM 调用生成最终回复。
func (h *OrchestrateHandler) handleCommandResult(
	conn *websocket.Conn,
	result DeviceCommandResultEvent,
	writeMu *sync.Mutex,
	sessionMu *sync.Mutex,
	sessionDevices *[]DeviceContext,
	sessionScenes *[]SceneContext,
	log *slog.Logger,
) {
	log.Info("command result received", "tool_id", result.ToolID, "success", result.Success, "message", result.Message)

	// 获取当前设备上下文快照
	sessionMu.Lock()
	devicesCopy := make([]DeviceContext, len(*sessionDevices))
	copy(devicesCopy, *sessionDevices)
	scenesCopy := make([]SceneContext, len(*sessionScenes))
	copy(scenesCopy, *sessionScenes)
	sessionMu.Unlock()

	// 构建二次调用消息，将 tool result 喂回 LLM
	resultContent := "执行成功"
	if !result.Success {
		resultContent = "执行失败: " + result.Message
	} else if result.Message != "" {
		resultContent = result.Message
	}

	t0 := time.Now()
	reply, err := h.callLLMSecondRound(resultContent, result.ToolID, devicesCopy, scenesCopy)
	cost := time.Since(t0).Milliseconds()

	if err != nil {
		log.Warn("second llm call failed", "err", err, "cost_ms", cost)
		writeMu.Lock()
		_ = writeJSON(conn, map[string]any{
			"type":    "llm_error",
			"message": err.Error(),
		})
		writeMu.Unlock()
		return
	}

	log.Info("second llm ok", "cost_ms", cost, "reply_len", len(reply))
	writeMu.Lock()
	_ = writeJSON(conn, map[string]any{
		"type": "llm_result",
		"text": reply,
	})
	writeMu.Unlock()
}

// callLLMWithTools 首次调用 LLM，带 tools 参数。
func (h *OrchestrateHandler) callLLMWithTools(text string, devices []DeviceContext, scenes []SceneContext) (*LLMResponse, error) {
	cfg := &h.cfg.LLM

	systemPrompt := BuildSystemPrompt(cfg.SystemPrompt, devices, scenes)

	messages := []map[string]any{}
	messages = append(messages, map[string]any{"role": "system", "content": systemPrompt})
	messages = append(messages, map[string]any{"role": "user", "content": text})

	body := map[string]any{
		"model":    cfg.Model,
		"messages": messages,
		"stream":   false,
		"tools":    BuildTools(),
	}
	bodyJSON, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequest("POST", cfg.URL, bytes.NewReader(bodyJSON))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+cfg.APIKey)

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(cfg.Timeout)*time.Second)
	defer cancel()
	req = req.WithContext(ctx)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var result LLMResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	if result.Error != nil && result.Error.Message != "" {
		return nil, errors.New("llm api error: " + result.Error.Message)
	}

	return &result, nil
}

// callLLMSecondRound 二次调用 LLM，将 tool result 喂回，获取最终文本回复。
func (h *OrchestrateHandler) callLLMSecondRound(toolResult string, toolID string, devices []DeviceContext, scenes []SceneContext) (string, error) {
	cfg := &h.cfg.LLM

	systemPrompt := BuildSystemPrompt(cfg.SystemPrompt, devices, scenes)

	// 构建多轮对话：system → assistant(tool_call) → tool(result)
	messages := []map[string]any{}
	messages = append(messages, map[string]any{"role": "system", "content": systemPrompt})

	// assistant 消息（含 tool_calls）
	messages = append(messages, map[string]any{
		"role": "assistant",
		"tool_calls": []map[string]any{
			{
				"id":   toolID,
				"type": "function",
				"function": map[string]any{
					"name":      "control_device",
					"arguments": "{}",
				},
			},
		},
	})

	// tool 消息（执行结果）
	messages = append(messages, map[string]any{
		"role":       "tool",
		"tool_call_id": toolID,
		"content":    toolResult,
	})

	body := map[string]any{
		"model":    cfg.Model,
		"messages": messages,
		"stream":   false,
	}
	bodyJSON, err := json.Marshal(body)
	if err != nil {
		return "", err
	}

	req, err := http.NewRequest("POST", cfg.URL, bytes.NewReader(bodyJSON))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+cfg.APIKey)

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(cfg.Timeout)*time.Second)
	defer cancel()
	req = req.WithContext(ctx)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var result LLMResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", err
	}

	if result.Error != nil && result.Error.Message != "" {
		return "", errors.New("llm api error: " + result.Error.Message)
	}

	if len(result.Choices) > 0 {
		return result.Choices[0].Message.Content, nil
	}
	return "", errors.New("llm response has no choices")
}

// callLLM 调用兼容 OpenAI Chat Completions 格式的大模型 API（保留兼容）。
func (h *OrchestrateHandler) callLLM(text string) (string, error) {
	cfg := &h.cfg.LLM

	messages := []map[string]string{}
	if cfg.SystemPrompt != "" {
		messages = append(messages, map[string]string{"role": "system", "content": cfg.SystemPrompt})
	}
	messages = append(messages, map[string]string{"role": "user", "content": text})

	body := map[string]any{
		"model":    cfg.Model,
		"messages": messages,
		"stream":   false,
	}
	bodyJSON, err := json.Marshal(body)
	if err != nil {
		return "", err
	}

	req, err := http.NewRequest("POST", cfg.URL, bytes.NewReader(bodyJSON))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+cfg.APIKey)

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(cfg.Timeout)*time.Second)
	defer cancel()
	req = req.WithContext(ctx)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var result struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error,omitempty"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", err
	}

	if result.Error != nil && result.Error.Message != "" {
		return "", errors.New("llm api error: " + result.Error.Message)
	}

	if len(result.Choices) > 0 {
		return result.Choices[0].Message.Content, nil
	}
	return "", errors.New("llm response has no choices")
}

// translateASREvent 把 ASR 的事件 type 映射为家具协议（partial -> asr_partial, final -> asr_final）。
func translateASREvent(raw []byte) []byte {
	var ev map[string]any
	if err := json.Unmarshal(raw, &ev); err != nil {
		return raw
	}
	t, _ := ev["type"].(string)
	switch t {
	case "partial":
		ev["type"] = "asr_partial"
	case "final":
		ev["type"] = "asr_final"
	}
	out, err := json.Marshal(ev)
	if err != nil {
		return raw
	}
	return out
}

// isPing 判断是否是上游的保活帧 {"type":"ping"}。
func isPing(data []byte) bool {
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		return false
	}
	t, _ := m["type"].(string)
	return t == "ping"
}

func writeJSON(c *websocket.Conn, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return c.WriteMessage(websocket.TextMessage, b)
}

func isNormalClose(err error) bool {
	if errors.Is(err, websocket.ErrCloseSent) {
		return true
	}
	if ce, ok := err.(*websocket.CloseError); ok {
		switch ce.Code {
		case websocket.CloseNormalClosure,
			websocket.CloseGoingAway,
			websocket.CloseNoStatusReceived:
			return true
		}
	}
	return false
}
