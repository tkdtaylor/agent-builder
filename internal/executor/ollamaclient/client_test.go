package ollamaclient

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TC-102-01: Chat sends correct JSON body and parses structured tool_calls response
func TestChatWithToolCalls(t *testing.T) {
	// Start a stub server that validates the request and returns a tool_calls response
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Assert the request method and path
		if r.Method != "POST" {
			t.Fatalf("expected method POST, got %s", r.Method)
		}
		if r.URL.Path != "/api/chat" {
			t.Fatalf("expected path /api/chat, got %s", r.URL.Path)
		}

		// Decode the request body
		var req ChatRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("failed to decode request body: %v", err)
		}

		// Assert the request contents
		if req.Model != "qwen3:8b" {
			t.Fatalf("expected model qwen3:8b, got %s", req.Model)
		}
		if req.Stream {
			t.Fatalf("expected Stream=false, got true")
		}
		if len(req.Messages) != 2 {
			t.Fatalf("expected 2 messages, got %d", len(req.Messages))
		}
		if len(req.Tools) != 1 {
			t.Fatalf("expected 1 tool, got %d", len(req.Tools))
		}

		// Write a response with tool_calls
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		resp := ChatResponse{
			Message: Message{
				Role: "assistant",
				ToolCalls: []ToolCall{
					{
						Function: ToolCallFunction{
							Name:      "write_file",
							Arguments: json.RawMessage(`{"path":"hello.txt","content":"hello"}`),
						},
					},
				},
			},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	// Create a client with the stub server URL
	client, err := NewClient(server.URL)
	if err != nil {
		t.Fatalf("NewClient failed: %v", err)
	}

	// Build the request
	req := ChatRequest{
		Model: "qwen3:8b",
		Messages: []Message{
			{Role: "system", Content: "You are a helpful assistant."},
			{Role: "user", Content: "Write hello.txt with content hello."},
		},
		Tools: []Tool{
			{
				Type: "function",
				Function: ToolFunction{
					Name:        "write_file",
					Description: "Write content to a file",
					Parameters:  map[string]any{"type": "object"},
				},
			},
		},
		Stream: false,
	}

	// Call Chat
	resp, err := client.Chat(context.Background(), req)
	if err != nil {
		t.Fatalf("Chat failed: %v", err)
	}

	// Assert the response
	if resp.Message.Role != "assistant" {
		t.Fatalf("expected role assistant, got %s", resp.Message.Role)
	}
	if len(resp.Message.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(resp.Message.ToolCalls))
	}
	if resp.Message.ToolCalls[0].Function.Name != "write_file" {
		t.Fatalf("expected tool name write_file, got %s", resp.Message.ToolCalls[0].Function.Name)
	}
	if string(resp.Message.ToolCalls[0].Function.Arguments) != `{"path":"hello.txt","content":"hello"}` {
		t.Fatalf("expected arguments, got %s", resp.Message.ToolCalls[0].Function.Arguments)
	}
	if resp.Message.Content != "" {
		t.Fatalf("expected empty Content, got %s", resp.Message.Content)
	}
}

// TC-102-02: Chat parses a plain-text content response (no tool_calls)
func TestChatWithContent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		resp := ChatResponse{
			Message: Message{
				Role:    "assistant",
				Content: "Task complete. Branch: task/102-test",
			},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client, err := NewClient(server.URL)
	if err != nil {
		t.Fatalf("NewClient failed: %v", err)
	}

	req := ChatRequest{
		Model:    "qwen3:8b",
		Messages: []Message{{Role: "user", Content: "What is 2+2?"}},
		Stream:   false,
	}

	resp, err := client.Chat(context.Background(), req)
	if err != nil {
		t.Fatalf("Chat failed: %v", err)
	}

	if resp.Message.Content != "Task complete. Branch: task/102-test" {
		t.Fatalf("expected content, got %s", resp.Message.Content)
	}
	if len(resp.Message.ToolCalls) != 0 {
		t.Fatalf("expected 0 tool calls, got %d", len(resp.Message.ToolCalls))
	}
}

// TC-102-03: Chat returns a descriptive error on non-200 HTTP response
func TestChatNon200Response(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		body       string
	}{
		{"500 Internal Server Error", http.StatusInternalServerError, `{"error":"model not found"}`},
		{"404 Not Found", http.StatusNotFound, "unknown endpoint"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tt.statusCode)
				_, _ = w.Write([]byte(tt.body))
			}))
			defer server.Close()

			client, err := NewClient(server.URL)
			if err != nil {
				t.Fatalf("NewClient failed: %v", err)
			}

			req := ChatRequest{
				Model:    "qwen3:8b",
				Messages: []Message{{Role: "user", Content: "test"}},
				Stream:   false,
			}

			resp, err := client.Chat(context.Background(), req)
			if err == nil {
				t.Fatalf("expected error, got nil")
			}

			// Check that the status code is in the error message
			if !contains(err.Error(), fmt.Sprintf("%d", tt.statusCode)) {
				t.Fatalf("expected status code in error message, got: %v", err)
			}

			// Check that the response is the zero value
			if resp.Message.Role != "" || resp.Message.Content != "" {
				t.Fatalf("expected zero value response, got: %v", resp)
			}
		})
	}
}

// TC-102-04: Chat returns a descriptive error on malformed JSON response
func TestChatMalformedJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{not valid json`))
	}))
	defer server.Close()

	client, err := NewClient(server.URL)
	if err != nil {
		t.Fatalf("NewClient failed: %v", err)
	}

	req := ChatRequest{
		Model:    "qwen3:8b",
		Messages: []Message{{Role: "user", Content: "test"}},
		Stream:   false,
	}

	resp, err := client.Chat(context.Background(), req)
	if err == nil {
		t.Fatalf("expected error, got nil")
	}

	// Check that the error mentions decode or unmarshal (from json.Decoder.Decode)
	errStr := err.Error()
	if !contains(errStr, "decode") && !contains(errStr, "unmarshal") && !contains(errStr, "invalid") {
		t.Fatalf("expected decode/unmarshal/invalid in error message, got: %v", err)
	}

	// Check that the response is the zero value
	if resp.Message.Role != "" || resp.Message.Content != "" {
		t.Fatalf("expected zero value response, got: %v", resp)
	}
}

// TC-102-05: Chat returns a descriptive error when the context is cancelled
func TestChatContextCancelled(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Should never reach here because context is already cancelled
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client, err := NewClient(server.URL)
	if err != nil {
		t.Fatalf("NewClient failed: %v", err)
	}

	// Create an already-cancelled context
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	req := ChatRequest{
		Model:    "qwen3:8b",
		Messages: []Message{{Role: "user", Content: "test"}},
		Stream:   false,
	}

	resp, err := client.Chat(ctx, req)
	if err == nil {
		t.Fatalf("expected error, got nil")
	}

	// Check that the error is context.Canceled
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got: %v", err)
	}

	// Check that the response is the zero value
	if resp.Message.Role != "" || resp.Message.Content != "" {
		t.Fatalf("expected zero value response, got: %v", resp)
	}
}

// TC-102-06: NewClient rejects a blank base URL
func TestNewClientBlankURL(t *testing.T) {
	tests := []string{
		"",
		"  ",
		"\t",
		"\n",
	}

	for _, url := range tests {
		t.Run("blank:"+url, func(t *testing.T) {
			client, err := NewClient(url)
			if err == nil {
				t.Fatalf("expected error for blank URL, got nil")
			}
			if client != nil {
				t.Fatalf("expected nil client, got: %v", client)
			}

			// Check that the error message is descriptive
			if !contains(err.Error(), "blank") && !contains(err.Error(), "base URL") {
				t.Fatalf("expected 'blank' or 'base URL' in error message, got: %v", err)
			}
		})
	}
}

// Helper function to check if a string contains a substring
func contains(s, substr string) bool {
	for i := 0; i < len(s); i++ {
		if len(s)-i < len(substr) {
			return false
		}
		match := true
		for j := 0; j < len(substr); j++ {
			if s[i+j] != substr[j] {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}

// TestTC10601_ObjectArgsDecode (REQ-106-01) verifies the client decodes Ollama's
// native object-form tool-call arguments and that the captured json.RawMessage
// unmarshals to the exact field values (not merely non-empty).
func TestTC10601_ObjectArgsDecode(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Ollama native /api/chat returns arguments as a JSON OBJECT, not a string.
		_, _ = w.Write([]byte(`{"message":{"role":"assistant","tool_calls":[` +
			`{"function":{"name":"write_file","arguments":{"path":"product.go","content":"package mathx"}}}]}}`))
	}))
	defer srv.Close()

	c, err := NewClient(srv.URL)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	resp, err := c.Chat(context.Background(), ChatRequest{Model: "m"})
	if err != nil {
		t.Fatalf("Chat returned error on object-form arguments: %v", err)
	}
	if len(resp.Message.ToolCalls) != 1 {
		t.Fatalf("tool_calls = %d, want 1", len(resp.Message.ToolCalls))
	}
	fn := resp.Message.ToolCalls[0].Function
	if fn.Name != "write_file" {
		t.Fatalf("Name = %q, want write_file", fn.Name)
	}
	// Decoded-value assertion (not raw-string): unmarshal the RawMessage to a map.
	var args map[string]string
	if err := json.Unmarshal(fn.Arguments, &args); err != nil {
		t.Fatalf("arguments not valid object JSON: %v (raw=%s)", err, fn.Arguments)
	}
	if args["path"] != "product.go" {
		t.Errorf("args[path] = %q, want product.go", args["path"])
	}
	if args["content"] != "package mathx" {
		t.Errorf("args[content] = %q, want \"package mathx\"", args["content"])
	}
}
