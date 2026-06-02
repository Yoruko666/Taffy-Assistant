package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"taffy.local/pkg/protocol"
)

// llmHTTPClient 是 worker 内 LLM 调用共用的 HTTP 客户端。
// 不设 Timeout，由每次调用自己的 ctx 控制（见 LLMConfig.Timeout）。
var llmHTTPClient = &http.Client{}

// llmRequest 抽出 callLLMWithTools / callLLMSecondRound 的公共字段。
type llmRequest struct {
	Model    string
	Messages []map[string]any
	Tools    []map[string]any // nil 时不带 tools 字段
}

// postLLM 向指定 provider 发起一次 OpenAI 兼容的 Chat Completions 调用。
func (h *OrchestrateHandler) postLLM(provider LLMProvider, req llmRequest) (*LLMResponse, error) {
	cfg := &h.cfg.LLM

	body := map[string]any{
		"model":    req.Model,
		"messages": req.Messages,
		"stream":   false,
	}
	if len(req.Tools) > 0 {
		body["tools"] = req.Tools
	}

	bodyJSON, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal llm req: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(cfg.Timeout)*time.Second)
	defer cancel()

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, provider.URL, bytes.NewReader(bodyJSON))
	if err != nil {
		return nil, fmt.Errorf("build llm req: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+provider.APIKey)

	resp, err := llmHTTPClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("llm http: %w", err)
	}
	defer resp.Body.Close()

	var result LLMResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode llm resp: %w", err)
	}
	if result.Error != nil && result.Error.Message != "" {
		return nil, errors.New("llm api error: " + result.Error.Message)
	}
	return &result, nil
}

// postLLMWithFallback 依次尝试所有 provider，返回第一个成功的结果。
func (h *OrchestrateHandler) postLLMWithFallback(req llmRequest) (*LLMResponse, error) {
	cfg := &h.cfg.LLM
	if len(cfg.Providers) == 0 {
		return nil, errors.New("no llm provider configured")
	}

	var lastErr error
	for i, p := range cfg.Providers {
		// 使用 provider 的 model 覆盖 req.Model（如果 req 未指定则以 provider 为准）
		reqCopy := req
		if reqCopy.Model == "" {
			reqCopy.Model = p.Model
		}

		resp, err := h.postLLM(p, reqCopy)
		if err == nil {
			if i > 0 {
				slog.Info("llm fallback succeeded", "provider_index", i, "model", p.Model)
			}
			return resp, nil
		}
		slog.Warn("llm provider failed, trying next", "index", i, "model", p.Model, "err", err)
		lastErr = err
	}
	return nil, fmt.Errorf("all llm providers failed: %w", lastErr)
}

// callLLMWithTools 首次调用 LLM，带 tools 参数，让 LLM 决定是否触发 control_device。
// 自动按 providers 顺序尝试主备切换。
func (h *OrchestrateHandler) callLLMWithTools(text string, devices []DeviceContext, scenes []SceneContext) (*LLMResponse, error) {
	cfg := &h.cfg.LLM
	systemPrompt := BuildSystemPrompt(cfg.SystemPrompt, devices, scenes)
	return h.postLLMWithFallback(llmRequest{
		Messages: []map[string]any{
			{"role": "system", "content": systemPrompt},
			{"role": "user", "content": text},
		},
		Tools: BuildTools(),
	})
}

// callLLMSecondRound 二次调用 LLM，将 tool result 喂回，获取最终文本回复。
// pending 必须如实回放首轮 tool call 的 name/arguments，否则 history 与执行结果不自洽。
// 自动按 providers 顺序尝试主备切换。
func (h *OrchestrateHandler) callLLMSecondRound(toolResult, toolID string, pending pendingToolCall, devices []DeviceContext, scenes []SceneContext) (string, error) {
	cfg := &h.cfg.LLM
	systemPrompt := BuildSystemPrompt(cfg.SystemPrompt, devices, scenes)

	// arguments 必须是合法 JSON 字符串；首轮异常未拿到时兜底为 "{}"。
	args := pending.Arguments
	if args == "" {
		args = "{}"
	}
	name := pending.Name
	if name == "" {
		name = protocol.FunctionControlDevice
	}

	// 多轮顺序：system → user(占位) → assistant(tool_call) → tool(result)。
	// OpenAI 协议要求 assistant(tool_calls) 之前必须有 user 消息；
	// 模型在二轮主要看 tool result，因此用中性占位代替原始用户文本。
	messages := []map[string]any{
		{"role": "system", "content": systemPrompt},
		{"role": "user", "content": "（接上轮设备控制请求）"},
		{
			"role":    "assistant",
			"content": "",
			"tool_calls": []map[string]any{
				{
					"id":   toolID,
					"type": "function",
					"function": map[string]any{
						"name":      name,
						"arguments": args,
					},
				},
			},
		},
		{
			"role":         "tool",
			"tool_call_id": toolID,
			"content":      toolResult,
		},
	}

	resp, err := h.postLLMWithFallback(llmRequest{
		Messages: messages,
	})
	if err != nil {
		return "", err
	}
	if len(resp.Choices) == 0 {
		return "", errors.New("llm response has no choices")
	}
	return resp.Choices[0].Message.Content, nil
}
