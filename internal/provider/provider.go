// Package provider abstracts LLM backends behind one tool-use interface.
//
// Two wire dialects cover effectively every hosted and local model:
//   - "anthropic": the Anthropic Messages API (Claude)
//   - "openai":    OpenAI-compatible chat completions with tool calling,
//     which is also how Kimi/Moonshot, OpenRouter, Groq, Ollama,
//     vLLM, llama.cpp and most open-source servers are exposed.
package provider

import (
	"context"
	"encoding/json"
)

// Block is one content block in a message. Exactly one of the shapes is used,
// selected by Type: "text", "tool_use", or "tool_result".
type Block struct {
	Type string

	// text
	Text string

	// tool_use
	ID    string
	Name  string
	Input json.RawMessage

	// tool_result
	ToolUseID string
	Content   string
	IsError   bool
	// Images attached to a tool_result — how the model sees its own work
	// (frames from view_frames). Adapters translate to each wire dialect.
	Images []Image
}

// Image is a base64-encoded image riding on a tool result.
type Image struct {
	MediaType string // e.g. "image/jpeg"
	Data      string // base64, no data: prefix
}

func TextBlock(s string) Block { return Block{Type: "text", Text: s} }

func ToolResultBlock(toolUseID, content string, isErr bool) Block {
	return Block{Type: "tool_result", ToolUseID: toolUseID, Content: content, IsError: isErr}
}

type Message struct {
	Role   string // "user" | "assistant"
	Blocks []Block
}

func UserText(s string) Message {
	return Message{Role: "user", Blocks: []Block{TextBlock(s)}}
}

// ToolDef is a tool advertised to the model. Schema is a JSON Schema object.
type ToolDef struct {
	Name        string
	Description string
	Schema      map[string]any
}

type Request struct {
	Model     string
	System    string
	Messages  []Message
	Tools     []ToolDef
	MaxTokens int
}

type Response struct {
	Blocks       []Block
	StopReason   string // "end_turn" | "tool_use" | "max_tokens" | ...
	InputTokens  int
	OutputTokens int
}

// Text concatenates all text blocks of a response.
func (r *Response) Text() string {
	out := ""
	for _, b := range r.Blocks {
		if b.Type == "text" {
			out += b.Text
		}
	}
	return out
}

// ToolUses returns the tool_use blocks of a response.
func (r *Response) ToolUses() []Block {
	var uses []Block
	for _, b := range r.Blocks {
		if b.Type == "tool_use" {
			uses = append(uses, b)
		}
	}
	return uses
}

// Provider is a synchronous completion backend.
type Provider interface {
	Complete(ctx context.Context, req Request) (*Response, error)
	Name() string
}
