package provider

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// Both adapters are tested against local fake servers: we assert the exact
// wire shape sent and that responses normalize onto the shared Block model.

func TestAnthropicWireFormat(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/messages" {
			t.Errorf("path = %s", r.URL.Path)
		}
		if r.Header.Get("x-api-key") != "k" {
			t.Error("missing api key header")
		}
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &got)
		_, _ = w.Write([]byte(`{
			"content":[{"type":"text","text":"hi"},{"type":"tool_use","id":"tu1","name":"trim","input":{"start":1}}],
			"stop_reason":"tool_use","usage":{"input_tokens":10,"output_tokens":5}}`))
	}))
	defer srv.Close()

	p := NewAnthropic(srv.URL, "k")
	resp, err := p.Complete(context.Background(), Request{
		Model:     "claude-x",
		System:    "sys",
		MaxTokens: 100,
		Messages: []Message{
			UserText("hello"),
			{Role: "assistant", Blocks: []Block{{Type: "tool_use", ID: "a", Name: "probe", Input: []byte(`{}`)}}},
			{Role: "user", Blocks: []Block{ToolResultBlock("a", "640x360", false)}},
		},
		Tools: []ToolDef{{Name: "trim", Description: "d", Schema: map[string]any{"type": "object"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got["system"] != "sys" || got["model"] != "claude-x" {
		t.Errorf("request body: %v", got)
	}
	if _, ok := got["tools"]; !ok {
		t.Error("tools not sent")
	}
	if resp.StopReason != "tool_use" || len(resp.ToolUses()) != 1 || resp.Text() != "hi" {
		t.Errorf("response mapping: %+v", resp)
	}
	if resp.InputTokens != 10 || resp.OutputTokens != 5 {
		t.Error("usage not mapped")
	}
}

func TestOpenAIWireFormatAndToolRoundTrip(t *testing.T) {
	var got struct {
		Messages []map[string]any `json:"messages"`
		Tools    []map[string]any `json:"tools"`
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Errorf("path = %s", r.URL.Path)
		}
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &got)
		_, _ = w.Write([]byte(`{
			"choices":[{"message":{"content":"","tool_calls":[
				{"id":"c1","type":"function","function":{"name":"trim","arguments":"{\"start\":2}"}}]},
				"finish_reason":"tool_calls"}],
			"usage":{"prompt_tokens":7,"completion_tokens":3}}`))
	}))
	defer srv.Close()

	p := NewOpenAI(srv.URL, "key", "kimi")
	resp, err := p.Complete(context.Background(), Request{
		Model:  "kimi-k3",
		System: "sys",
		Messages: []Message{
			UserText("hello"),
			{Role: "assistant", Blocks: []Block{{Type: "tool_use", ID: "c0", Name: "probe", Input: []byte(`{}`)}}},
			{Role: "user", Blocks: []Block{ToolResultBlock("c0", "640x360", false)}},
		},
		Tools: []ToolDef{{Name: "trim", Description: "d", Schema: map[string]any{"type": "object"}}},
	})
	if err != nil {
		t.Fatal(err)
	}

	// system message first; assistant tool call and role:"tool" result present
	if got.Messages[0]["role"] != "system" {
		t.Errorf("first message = %v", got.Messages[0])
	}
	roles := []string{}
	for _, m := range got.Messages {
		roles = append(roles, m["role"].(string))
	}
	wantRoles := []string{"system", "user", "assistant", "tool"}
	for i, r := range wantRoles {
		if roles[i] != r {
			t.Fatalf("roles = %v, want prefix %v", roles, wantRoles)
		}
	}
	if len(got.Tools) != 1 {
		t.Error("tools not sent")
	}

	if resp.StopReason != "tool_use" {
		t.Errorf("stop = %s", resp.StopReason)
	}
	uses := resp.ToolUses()
	if len(uses) != 1 || uses[0].Name != "trim" || string(uses[0].Input) != `{"start":2}` {
		t.Errorf("tool use mapping: %+v", uses)
	}
}
