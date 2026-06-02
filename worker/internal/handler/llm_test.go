package handler

import (
	"testing"
)

func TestPostLLMWithFallback(t *testing.T) {
	// 从 config.yaml 加载配置
	cfg, err := LoadConfig("../../config.yaml")
	if err != nil {
		t.Fatalf("load config: %v", err)
	}

	if len(cfg.LLM.Providers) == 0 {
		t.Fatal("no llm providers configured")
	}
	t.Logf("providers count: %d", len(cfg.LLM.Providers))
	for i, p := range cfg.LLM.Providers {
		t.Logf("  [%d] model=%s url=%s", i, p.Model, p.URL)
	}

	h := &OrchestrateHandler{cfg: cfg}

	// 简单对话测试
	resp, err := h.postLLMWithFallback(llmRequest{
		Messages: []map[string]any{
			{"role": "user", "content": "你好，请用一句话介绍你自己"},
		},
	})
	if err != nil {
		t.Fatalf("llm call failed: %v", err)
	}

	if len(resp.Choices) == 0 {
		t.Fatal("llm response has no choices")
	}
	content := resp.Choices[0].Message.Content
	t.Logf("LLM response: %s", content)

	if content == "" {
		t.Error("llm returned empty content")
	}
}

func TestPostLLMWithTools(t *testing.T) {
	cfg, err := LoadConfig("../../config.yaml")
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if len(cfg.LLM.Providers) == 0 {
		t.Fatal("no llm providers configured")
	}

	h := &OrchestrateHandler{cfg: cfg}

	// 测试带 tools 的调用
	resp, err := h.callLLMWithTools("打开客厅的灯", nil, nil)
	if err != nil {
		t.Fatalf("llm with tools failed: %v", err)
	}
	if len(resp.Choices) == 0 {
		t.Fatal("llm response has no choices")
	}
	msg := resp.Choices[0].Message
	t.Logf("LLM content: %s", msg.Content)
	if len(msg.ToolCalls) > 0 {
		t.Logf("Tool calls: %d", len(msg.ToolCalls))
		for _, tc := range msg.ToolCalls {
			t.Logf("  tool: %s args: %s", tc.Function.Name, tc.Function.Arguments)
		}
	}
}
