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
