package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Anthropic speaks the native Messages API.
type Anthropic struct {
	BaseURL string
	APIKey  string
	HTTP    *http.Client
}

func NewAnthropic(baseURL, apiKey string) *Anthropic {
	return &Anthropic{BaseURL: baseURL, APIKey: apiKey, HTTP: &http.Client{Timeout: 5 * time.Minute}}
}

func (a *Anthropic) Name() string { return "anthropic" }

type anthMsg struct {
	Role    string    `json:"role"`
	Content []anthBlk `json:"content"`
}

type anthBlk struct {
	Type      string          `json:"type"`
	Text      string          `json:"text,omitempty"`
	ID        string          `json:"id,omitempty"`
	Name      string          `json:"name,omitempty"`
	Input     json.RawMessage `json:"input,omitempty"`
	ToolUseID string          `json:"tool_use_id,omitempty"`
	Content   string          `json:"content,omitempty"`
	IsError   bool            `json:"is_error,omitempty"`
}

type anthTool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"input_schema"`
}

func (a *Anthropic) Complete(ctx context.Context, req Request) (*Response, error) {
	body := map[string]any{
		"model":      req.Model,
		"max_tokens": req.MaxTokens,
		"messages":   toAnthMessages(req.Messages),
	}
	if req.System != "" {
		body["system"] = req.System
	}
	if len(req.Tools) > 0 {
		tools := make([]anthTool, len(req.Tools))
		for i, t := range req.Tools {
			tools[i] = anthTool{Name: t.Name, Description: t.Description, InputSchema: t.Schema}
		}
		body["tools"] = tools
	}

	raw, _ := json.Marshal(body)
	httpReq, err := http.NewRequestWithContext(ctx, "POST", a.BaseURL+"/v1/messages", bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-api-key", a.APIKey)
	httpReq.Header.Set("anthropic-version", "2023-06-01")

	resp, err := a.HTTP.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	payload, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return nil, &HTTPError{
			Provider: "anthropic", Status: resp.StatusCode,
			Body: truncate(string(payload), 400), RetryAfter: retryAfter(resp.Header),
		}
	}

	var out struct {
		Content    []anthBlk `json:"content"`
		StopReason string    `json:"stop_reason"`
		Usage      struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(payload, &out); err != nil {
		return nil, fmt.Errorf("anthropic: decode: %w", err)
	}

	res := &Response{
		StopReason:   out.StopReason,
		InputTokens:  out.Usage.InputTokens,
		OutputTokens: out.Usage.OutputTokens,
	}
	for _, b := range out.Content {
		switch b.Type {
		case "text":
			res.Blocks = append(res.Blocks, TextBlock(b.Text))
		case "tool_use":
			res.Blocks = append(res.Blocks, Block{Type: "tool_use", ID: b.ID, Name: b.Name, Input: b.Input})
		}
	}
	return res, nil
}

func toAnthMessages(msgs []Message) []anthMsg {
	out := make([]anthMsg, 0, len(msgs))
	for _, m := range msgs {
		am := anthMsg{Role: m.Role}
		for _, b := range m.Blocks {
			switch b.Type {
			case "text":
				am.Content = append(am.Content, anthBlk{Type: "text", Text: b.Text})
			case "tool_use":
				input := b.Input
				if len(input) == 0 {
					input = json.RawMessage(`{}`)
				}
				am.Content = append(am.Content, anthBlk{Type: "tool_use", ID: b.ID, Name: b.Name, Input: input})
			case "tool_result":
				am.Content = append(am.Content, anthBlk{
					Type: "tool_result", ToolUseID: b.ToolUseID, Content: b.Content, IsError: b.IsError,
				})
			}
		}
		out = append(out, am)
	}
	return out
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
