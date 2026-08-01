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

// OpenAI speaks OpenAI-compatible chat completions with tool calling.
// This one adapter covers OpenAI, Kimi/Moonshot, OpenRouter, Groq, Ollama,
// vLLM, llama.cpp server, and most other hosts.
type OpenAI struct {
	BaseURL string
	APIKey  string
	Label   string
	HTTP    *http.Client
}

func NewOpenAI(baseURL, apiKey, label string) *OpenAI {
	if label == "" {
		label = "openai-compatible"
	}
	return &OpenAI{BaseURL: baseURL, APIKey: apiKey, Label: label, HTTP: &http.Client{Timeout: 5 * time.Minute}}
}

func (o *OpenAI) Name() string { return o.Label }

type oaToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

type oaMessage struct {
	Role       string       `json:"role"`
	Content    string       `json:"content,omitempty"`
	ToolCalls  []oaToolCall `json:"tool_calls,omitempty"`
	ToolCallID string       `json:"tool_call_id,omitempty"`
}

func (o *OpenAI) Complete(ctx context.Context, req Request) (*Response, error) {
	msgs := []oaMessage{}
	if req.System != "" {
		msgs = append(msgs, oaMessage{Role: "system", Content: req.System})
	}
	msgs = append(msgs, toOAMessages(req.Messages)...)

	body := map[string]any{
		"model":      req.Model,
		"messages":   msgs,
		"max_tokens": req.MaxTokens,
	}
	if len(req.Tools) > 0 {
		tools := make([]map[string]any, len(req.Tools))
		for i, t := range req.Tools {
			tools[i] = map[string]any{
				"type": "function",
				"function": map[string]any{
					"name":        t.Name,
					"description": t.Description,
					"parameters":  t.Schema,
				},
			}
		}
		body["tools"] = tools
	}

	raw, _ := json.Marshal(body)
	httpReq, err := http.NewRequestWithContext(ctx, "POST", o.BaseURL+"/chat/completions", bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if o.APIKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+o.APIKey)
	}

	resp, err := o.HTTP.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	payload, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return nil, &HTTPError{
			Provider: o.Label, Status: resp.StatusCode,
			Body: truncate(string(payload), 400), RetryAfter: retryAfter(resp.Header),
		}
	}

	var out struct {
		Choices []struct {
			Message      oaMessage `json:"message"`
			FinishReason string    `json:"finish_reason"`
		} `json:"choices"`
		Usage struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(payload, &out); err != nil {
		return nil, fmt.Errorf("%s: decode: %w", o.Label, err)
	}
	if len(out.Choices) == 0 {
		return nil, fmt.Errorf("%s: empty choices", o.Label)
	}

	choice := out.Choices[0]
	res := &Response{
		InputTokens:  out.Usage.PromptTokens,
		OutputTokens: out.Usage.CompletionTokens,
	}
	if choice.Message.Content != "" {
		res.Blocks = append(res.Blocks, TextBlock(choice.Message.Content))
	}
	for _, tc := range choice.Message.ToolCalls {
		args := tc.Function.Arguments
		if args == "" {
			args = "{}"
		}
		res.Blocks = append(res.Blocks, Block{
			Type: "tool_use", ID: tc.ID, Name: tc.Function.Name, Input: json.RawMessage(args),
		})
	}
	// Normalize stop reason onto the Anthropic vocabulary the agent loop uses.
	switch choice.FinishReason {
	case "tool_calls":
		res.StopReason = "tool_use"
	case "length":
		res.StopReason = "max_tokens"
	default:
		res.StopReason = "end_turn"
	}
	if len(res.ToolUses()) > 0 {
		res.StopReason = "tool_use"
	}
	return res, nil
}

// CompleteStream implements Streamer over OpenAI-style chunked completions.
// Tool calls arrive as fragments keyed by index (id/name first, then argument
// string pieces) and are reassembled into complete tool_use blocks.
func (o *OpenAI) CompleteStream(ctx context.Context, req Request, onDelta func(Delta)) (*Response, error) {
	msgs := []oaMessage{}
	if req.System != "" {
		msgs = append(msgs, oaMessage{Role: "system", Content: req.System})
	}
	msgs = append(msgs, toOAMessages(req.Messages)...)

	body := map[string]any{
		"model":          req.Model,
		"messages":       msgs,
		"max_tokens":     req.MaxTokens,
		"stream":         true,
		"stream_options": map[string]any{"include_usage": true},
	}
	if len(req.Tools) > 0 {
		tools := make([]map[string]any, len(req.Tools))
		for i, t := range req.Tools {
			tools[i] = map[string]any{
				"type": "function",
				"function": map[string]any{
					"name":        t.Name,
					"description": t.Description,
					"parameters":  t.Schema,
				},
			}
		}
		body["tools"] = tools
	}

	raw, _ := json.Marshal(body)
	httpReq, err := http.NewRequestWithContext(ctx, "POST", o.BaseURL+"/chat/completions", bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if o.APIKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+o.APIKey)
	}

	resp, err := o.HTTP.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		payload, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, &HTTPError{
			Provider: o.Label, Status: resp.StatusCode,
			Body: truncate(string(payload), 400), RetryAfter: retryAfter(resp.Header),
		}
	}

	type callBuild struct {
		id, name string
		args     strings.Builder
	}
	var (
		text         strings.Builder
		calls        = map[int]*callBuild{}
		callOrder    []int
		finishReason string
		res          = &Response{}
	)

	err = readSSE(ctx, resp.Body, func(_, data string) error {
		if data == "[DONE]" {
			return io.EOF
		}
		var chunk struct {
			Choices []struct {
				Delta struct {
					Content   string `json:"content"`
					ToolCalls []struct {
						Index    int    `json:"index"`
						ID       string `json:"id"`
						Function struct {
							Name      string `json:"name"`
							Arguments string `json:"arguments"`
						} `json:"function"`
					} `json:"tool_calls"`
				} `json:"delta"`
				FinishReason string `json:"finish_reason"`
			} `json:"choices"`
			Usage *struct {
				PromptTokens     int `json:"prompt_tokens"`
				CompletionTokens int `json:"completion_tokens"`
			} `json:"usage"`
		}
		if uerr := json.Unmarshal([]byte(data), &chunk); uerr != nil {
			return nil // tolerate unknown frames
		}
		if chunk.Usage != nil {
			res.InputTokens = chunk.Usage.PromptTokens
			res.OutputTokens = chunk.Usage.CompletionTokens
		}
		if len(chunk.Choices) == 0 {
			return nil
		}
		choice := chunk.Choices[0]
		if choice.FinishReason != "" {
			finishReason = choice.FinishReason
		}
		if choice.Delta.Content != "" {
			text.WriteString(choice.Delta.Content)
			if onDelta != nil {
				onDelta(Delta{Text: choice.Delta.Content})
			}
		}
		for _, tc := range choice.Delta.ToolCalls {
			cb := calls[tc.Index]
			if cb == nil {
				cb = &callBuild{}
				calls[tc.Index] = cb
				callOrder = append(callOrder, tc.Index)
			}
			if tc.ID != "" {
				cb.id = tc.ID
			}
			if tc.Function.Name != "" {
				cb.name = tc.Function.Name
			}
			cb.args.WriteString(tc.Function.Arguments)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	if text.Len() > 0 {
		res.Blocks = append(res.Blocks, TextBlock(text.String()))
	}
	for _, idx := range callOrder {
		cb := calls[idx]
		args := cb.args.String()
		if args == "" {
			args = "{}"
		}
		res.Blocks = append(res.Blocks, Block{Type: "tool_use", ID: cb.id, Name: cb.name, Input: json.RawMessage(args)})
	}
	switch finishReason {
	case "tool_calls":
		res.StopReason = "tool_use"
	case "length":
		res.StopReason = "max_tokens"
	default:
		res.StopReason = "end_turn"
	}
	if len(res.ToolUses()) > 0 {
		res.StopReason = "tool_use"
	}
	return res, nil
}

func toOAMessages(msgs []Message) []oaMessage {
	var out []oaMessage
	for _, m := range msgs {
		switch m.Role {
		case "assistant":
			am := oaMessage{Role: "assistant"}
			for _, b := range m.Blocks {
				switch b.Type {
				case "text":
					am.Content += b.Text
				case "tool_use":
					call := oaToolCall{ID: b.ID, Type: "function"}
					call.Function.Name = b.Name
					call.Function.Arguments = string(b.Input)
					if call.Function.Arguments == "" {
						call.Function.Arguments = "{}"
					}
					am.ToolCalls = append(am.ToolCalls, call)
				}
			}
			out = append(out, am)
		default: // user turns: split tool results into role:"tool" messages
			var text string
			var toolMsgs []oaMessage
			for _, b := range m.Blocks {
				switch b.Type {
				case "text":
					text += b.Text
				case "tool_result":
					toolMsgs = append(toolMsgs, oaMessage{
						Role: "tool", ToolCallID: b.ToolUseID, Content: b.Content,
					})
				}
			}
			out = append(out, toolMsgs...)
			if text != "" {
				out = append(out, oaMessage{Role: "user", Content: text})
			}
		}
	}
	return out
}
