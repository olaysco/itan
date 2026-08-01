package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
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

// CompleteStream implements Streamer over the Messages API SSE protocol:
// content blocks are assembled from content_block_start / *_delta / *_stop
// events; text deltas are surfaced as they arrive.
func (a *Anthropic) CompleteStream(ctx context.Context, req Request, onDelta func(Delta)) (*Response, error) {
	body := map[string]any{
		"model":      req.Model,
		"max_tokens": req.MaxTokens,
		"messages":   toAnthMessages(req.Messages),
		"stream":     true,
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
	if resp.StatusCode >= 400 {
		payload, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, &HTTPError{
			Provider: "anthropic", Status: resp.StatusCode,
			Body: truncate(string(payload), 400), RetryAfter: retryAfter(resp.Header),
		}
	}

	res := &Response{}
	blocks := map[int]*Block{}            // index → block under construction
	partial := map[int]*strings.Builder{} // index → accumulated tool-input JSON
	var order []int

	err = readSSE(ctx, resp.Body, func(event, data string) error {
		switch event {
		case "message_start":
			var ev struct {
				Message struct {
					Usage struct {
						InputTokens int `json:"input_tokens"`
					} `json:"usage"`
				} `json:"message"`
			}
			_ = json.Unmarshal([]byte(data), &ev)
			res.InputTokens = ev.Message.Usage.InputTokens
		case "content_block_start":
			var ev struct {
				Index        int `json:"index"`
				ContentBlock struct {
					Type string `json:"type"`
					ID   string `json:"id"`
					Name string `json:"name"`
				} `json:"content_block"`
			}
			if uerr := json.Unmarshal([]byte(data), &ev); uerr != nil {
				return nil
			}
			b := &Block{Type: ev.ContentBlock.Type, ID: ev.ContentBlock.ID, Name: ev.ContentBlock.Name}
			blocks[ev.Index] = b
			partial[ev.Index] = &strings.Builder{}
			order = append(order, ev.Index)
		case "content_block_delta":
			var ev struct {
				Index int `json:"index"`
				Delta struct {
					Type        string `json:"type"`
					Text        string `json:"text"`
					PartialJSON string `json:"partial_json"`
				} `json:"delta"`
			}
			if uerr := json.Unmarshal([]byte(data), &ev); uerr != nil {
				return nil
			}
			b := blocks[ev.Index]
			if b == nil {
				return nil
			}
			switch ev.Delta.Type {
			case "text_delta":
				b.Text += ev.Delta.Text
				if onDelta != nil {
					onDelta(Delta{Text: ev.Delta.Text})
				}
			case "input_json_delta":
				partial[ev.Index].WriteString(ev.Delta.PartialJSON)
			}
		case "message_delta":
			var ev struct {
				Delta struct {
					StopReason string `json:"stop_reason"`
				} `json:"delta"`
				Usage struct {
					OutputTokens int `json:"output_tokens"`
				} `json:"usage"`
			}
			_ = json.Unmarshal([]byte(data), &ev)
			if ev.Delta.StopReason != "" {
				res.StopReason = ev.Delta.StopReason
			}
			if ev.Usage.OutputTokens > 0 {
				res.OutputTokens = ev.Usage.OutputTokens
			}
		case "message_stop":
			return io.EOF
		case "error":
			return fmt.Errorf("anthropic stream error: %s", truncate(data, 300))
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	for _, idx := range order {
		b := blocks[idx]
		if b.Type == "tool_use" {
			input := partial[idx].String()
			if input == "" {
				input = "{}"
			}
			b.Input = json.RawMessage(input)
		}
		res.Blocks = append(res.Blocks, *b)
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
