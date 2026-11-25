package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestPostLLMWithFallback(t *testing.T) {
	var requests int
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.Method != http.MethodPost {
			t.Errorf("request method = %s, want POST", r.Method)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"error":{"message":"primary unavailable"}}`))
	}))
	defer primary.Close()

	fallback := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"mock-response","choices":[{"message":{"role":"assistant","content":"mock reply"}}]}`))
	}))
	defer fallback.Close()

	h := &OrchestrateHandler{cfg: &AppConfig{LLM: LLMConfig{
		Providers: []LLMProvider{
			{URL: primary.URL, Model: "primary-model"},
			{URL: fallback.URL, Model: "fallback-model"},
		},
		Timeout: 1,
	}}}

	resp, err := h.postLLMWithFallback(llmRequest{
		Messages: []map[string]any{{"role": "user", "content": "hello"}},
	})
	if err != nil {
		t.Fatalf("mock LLM call failed: %v", err)
	}
	if len(resp.Choices) != 1 || resp.Choices[0].Message.Content != "mock reply" {
		t.Fatalf("unexpected response: %+v", resp)
	}
	if requests != 2 {
		t.Fatalf("mock request count = %d, want 2", requests)
	}
}

func TestPostLLMWithTools(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Model    string           `json:"model"`
			Messages []map[string]any `json:"messages"`
			Tools    []map[string]any `json:"tools"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode request: %v", err)
		}
		if body.Model != "mock-model" {
			t.Errorf("model = %q, want mock-model", body.Model)
		}
		if len(body.Messages) != 2 {
			t.Errorf("message count = %d, want 2", len(body.Messages))
		}
		if len(body.Tools) != 1 {
			t.Errorf("tool count = %d, want 1", len(body.Tools))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"","tool_calls":[{"id":"call-1","type":"function","function":{"name":"control_device","arguments":"{}"}}]}}]}`))
	}))
	defer server.Close()

	h := &OrchestrateHandler{cfg: &AppConfig{LLM: LLMConfig{
		Providers: []LLMProvider{{URL: server.URL, Model: "mock-model"}},
		Timeout:   1,
	}}}

	resp, err := h.callLLMWithTools("打开客厅的灯", nil, nil)
	if err != nil {
		t.Fatalf("mock LLM with tools failed: %v", err)
	}
	if len(resp.Choices) != 1 || len(resp.Choices[0].Message.ToolCalls) != 1 {
		t.Fatalf("unexpected tool response: %+v", resp)
	}
}
