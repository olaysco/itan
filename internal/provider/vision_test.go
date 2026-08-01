package provider

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func imageResultMsg() Message {
	b := ToolResultBlock("t1", "showing 1 frame at 2.0s", false)
	b.Images = []Image{{MediaType: "image/jpeg", Data: "ZmFrZWpwZw=="}}
	return Message{Role: "user", Blocks: []Block{b}}
}

// Tool results carrying frames must reach Claude as image source blocks
// inside the tool_result content array.
func TestAnthropicToolResultImages(t *testing.T) {
	var body string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		body = string(raw)
		_, _ = w.Write([]byte(`{"content":[{"type":"text","text":"looks right"}],"stop_reason":"end_turn","usage":{}}`))
	}))
	defer srv.Close()

	p := NewAnthropic(srv.URL, "k")
	_, err := p.Complete(context.Background(), Request{
		Model: "m", MaxTokens: 10,
		Messages: []Message{
			UserText("check it"),
			{Role: "assistant", Blocks: []Block{{Type: "tool_use", ID: "t1", Name: "view_frames", Input: []byte(`{}`)}}},
			imageResultMsg(),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	var req struct {
		Messages []struct {
			Content []struct {
				Type    string          `json:"type"`
				Content json.RawMessage `json:"content"`
			} `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal([]byte(body), &req); err != nil {
		t.Fatal(err)
	}
	last := req.Messages[len(req.Messages)-1].Content[0]
	if last.Type != "tool_result" {
		t.Fatalf("last block type = %s", last.Type)
	}
	payload := string(last.Content)
	for _, want := range []string{`"type":"image"`, `"media_type":"image/jpeg"`, `"data":"ZmFrZWpwZw=="`, "showing 1 frame"} {
		if !strings.Contains(payload, want) {
			t.Errorf("tool_result content missing %s in %s", want, payload)
		}
	}
}

// Multimodal hosts may return assistant content as an array of parts; the
// text inside must survive parsing — dropping it loses the whole reply.
func TestOpenAIArrayContent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":[{"type":"text","text":"part one. "},{"type":"text","text":"part two."}]},"finish_reason":"stop"}],"usage":{}}`))
	}))
	defer srv.Close()

	p := NewOpenAI(srv.URL, "k", "test")
	resp, err := p.Complete(context.Background(), Request{Model: "m", Messages: []Message{UserText("hi")}})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Text() != "part one. part two." {
		t.Fatalf("array content lost: %q", resp.Text())
	}
}

// Same for streaming: delta content may arrive in array form.
func TestOpenAIStreamArrayContent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(
			"data: {\"choices\":[{\"delta\":{\"content\":[{\"type\":\"text\",\"text\":\"hello \"}]}}]}\n\n" +
				"data: {\"choices\":[{\"delta\":{\"content\":\"world\"},\"finish_reason\":\"stop\"}]}\n\n" +
				"data: [DONE]\n\n"))
	}))
	defer srv.Close()

	p := NewOpenAI(srv.URL, "k", "test")
	resp, err := p.CompleteStream(context.Background(), Request{Model: "m", Messages: []Message{UserText("hi")}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Text() != "hello world" {
		t.Fatalf("stream array content lost: %q", resp.Text())
	}
}

// Reasoning models put chain-of-thought in reasoning_content; it must be
// captured (a reasoning-only response is a stall the agent must detect) and
// streamed as Thinking deltas so the UI shows activity.
func TestOpenAIReasoningContent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"","reasoning_content":"thinking hard about frames"},"finish_reason":"length"}],"usage":{}}`))
	}))
	defer srv.Close()

	p := NewOpenAI(srv.URL, "k", "test")
	resp, err := p.Complete(context.Background(), Request{Model: "m", Messages: []Message{UserText("hi")}})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Reasoning != "thinking hard about frames" {
		t.Fatalf("reasoning lost: %q", resp.Reasoning)
	}
	if len(resp.Blocks) != 0 || resp.StopReason != "max_tokens" {
		t.Fatalf("reasoning-only response misparsed: %+v", resp)
	}
}

func TestOpenAIStreamReasoning(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(
			"data: {\"choices\":[{\"delta\":{\"reasoning_content\":\"hmm \"}}]}\n\n" +
				"data: {\"choices\":[{\"delta\":{\"reasoning_content\":\"okay\"}}]}\n\n" +
				"data: {\"choices\":[{\"delta\":{\"content\":\"done\"},\"finish_reason\":\"stop\"}]}\n\n" +
				"data: [DONE]\n\n"))
	}))
	defer srv.Close()

	var thinking string
	p := NewOpenAI(srv.URL, "k", "test")
	resp, err := p.CompleteStream(context.Background(), Request{Model: "m", Messages: []Message{UserText("hi")}},
		func(d Delta) { thinking += d.Thinking })
	if err != nil {
		t.Fatal(err)
	}
	if thinking != "hmm okay" || resp.Reasoning != "hmm okay" {
		t.Fatalf("thinking deltas lost: %q / %q", thinking, resp.Reasoning)
	}
	if resp.Text() != "done" {
		t.Fatalf("text = %q", resp.Text())
	}
}

// The reasoning budget cap applies only where the parameter is known-safe.
func TestCapReasoning(t *testing.T) {
	or := &OpenAI{BaseURL: "https://openrouter.ai/api/v1"}
	body := map[string]any{}
	or.capReasoning(body, 8192)
	r, ok := body["reasoning"].(map[string]any)
	if !ok || r["max_tokens"] != 4096 {
		t.Fatalf("openrouter reasoning cap missing: %v", body)
	}
	direct := &OpenAI{BaseURL: "https://api.moonshot.ai/v1"}
	body = map[string]any{}
	direct.capReasoning(body, 8192)
	if _, ok := body["reasoning"]; ok {
		t.Fatal("reasoning param must not be sent to unknown hosts")
	}
}

// The OpenAI dialect can't put images on the tool role, so frames ride a
// follow-up user message as data URIs.
func TestOpenAIToolResultImages(t *testing.T) {
	var body string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		body = string(raw)
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"ok"},"finish_reason":"stop"}],"usage":{}}`))
	}))
	defer srv.Close()

	p := NewOpenAI(srv.URL, "k", "test")
	_, err := p.Complete(context.Background(), Request{
		Model: "m",
		Messages: []Message{
			UserText("check it"),
			{Role: "assistant", Blocks: []Block{{Type: "tool_use", ID: "t1", Name: "view_frames", Input: []byte(`{}`)}}},
			imageResultMsg(),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(body, `"url":"data:image/jpeg;base64,ZmFrZWpwZw=="`) {
		t.Fatalf("data-URI image missing from request: %s", body)
	}
	// Ordering: the tool message stays a plain string; the image user
	// message follows it.
	toolIdx := strings.Index(body, `"role":"tool"`)
	imgIdx := strings.Index(body, `image_url`)
	if toolIdx < 0 || imgIdx < toolIdx {
		t.Fatal("image user message must follow the tool message")
	}
}
