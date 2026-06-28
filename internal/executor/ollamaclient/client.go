// Package ollamaclient provides a minimal HTTP client for Ollama's /api/chat endpoint.
// It is stdlib-only (net/http, encoding/json, context, fmt, strings) and a leaf
// package with no internal imports.
package ollamaclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// Tool describes one tool the model may call.
type Tool struct {
	Type     string       `json:"type"`     // "function"
	Function ToolFunction `json:"function"`
}

// ToolFunction describes the function definition of a tool.
type ToolFunction struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Parameters  any    `json:"parameters"` // JSON Schema object
}

// Message is one turn in a conversation.
type Message struct {
	Role      string     `json:"role"`                // "system","user","assistant","tool"
	Content   string     `json:"content,omitempty"`
	ToolCalls []ToolCall `json:"tool_calls,omitempty"`
	Name      string     `json:"name,omitempty"` // tool name for role:"tool" messages
}

// ToolCall represents a tool call made by the model.
type ToolCall struct {
	Function ToolCallFunction `json:"function"`
}

// ToolCallFunction is the function part of a tool call.
type ToolCallFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"` // raw JSON string
}

// ChatRequest is the payload sent to /api/chat.
type ChatRequest struct {
	Model    string    `json:"model"`
	Messages []Message `json:"messages"`
	Tools    []Tool    `json:"tools,omitempty"`
	Stream   bool      `json:"stream"`
}

// ChatResponse is what /api/chat returns (non-streaming).
type ChatResponse struct {
	Message Message `json:"message"`
}

// Client is a thin HTTP client for the Ollama /api/chat endpoint.
type Client struct {
	baseURL    string
	httpClient *http.Client
}

// NewClient creates a new Ollama client with the given base URL.
// It returns (nil, error) if the base URL is blank.
func NewClient(baseURL string) (*Client, error) {
	if strings.TrimSpace(baseURL) == "" {
		return nil, fmt.Errorf("blank base URL: baseURL must not be empty")
	}
	return &Client{
		baseURL:    baseURL,
		httpClient: &http.Client{},
	}, nil
}

// Chat sends a chat request to Ollama's /api/chat endpoint and returns the response.
// It returns a non-nil error on any non-200 HTTP status code, malformed JSON response,
// or context cancellation.
func (c *Client) Chat(ctx context.Context, req ChatRequest) (ChatResponse, error) {
	// Encode the request body
	reqBody, err := json.Marshal(req)
	if err != nil {
		return ChatResponse{}, fmt.Errorf("marshal request: %w", err)
	}

	// Create the HTTP request
	httpReq, err := http.NewRequestWithContext(ctx, "POST", c.baseURL+"/api/chat", bytes.NewReader(reqBody))
	if err != nil {
		return ChatResponse{}, fmt.Errorf("create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	// Execute the request
	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		// Check if the error is context cancellation
		if ctx.Err() != nil {
			return ChatResponse{}, ctx.Err()
		}
		return ChatResponse{}, fmt.Errorf("send request: %w", err)
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	// Check the HTTP status code
	if resp.StatusCode != http.StatusOK {
		// Read the body to include in the error message
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return ChatResponse{}, fmt.Errorf("HTTP %d: failed to read error body", resp.StatusCode)
		}
		// Truncate to 256 bytes for the error message
		bodyStr := string(body)
		if len(bodyStr) > 256 {
			bodyStr = bodyStr[:256] + "..."
		}
		return ChatResponse{}, fmt.Errorf("HTTP %d: %s", resp.StatusCode, bodyStr)
	}

	// Decode the response
	var chatResp ChatResponse
	decoder := json.NewDecoder(resp.Body)
	if err := decoder.Decode(&chatResp); err != nil {
		return ChatResponse{}, fmt.Errorf("decode response: %w", err)
	}

	return chatResp, nil
}
