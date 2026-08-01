package provider

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func sseWrite(w http.ResponseWriter, lines ...string) {
	for _, l := range lines {
		_, _ = w.Write([]byte(l + "\n"))
	}
	_, _ = w.Write([]byte("\n"))
}

func TestAnthropicStreamingAssemblesTextAndTools(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		sseWrite(w, "event: message_start", `data: {"message":{"usage":{"input_tokens":42}}}`)
		sseWrite(w, "event: content_block_start", `data: {"index":0,"content_block":{"type":"text"}}`)
		sseWrite(w, "event: content_block_delta", `data: {"index":0,"delta":{"type":"text_delta","text":"Trimming"}}`)
		sseWrite(w, "event: content_block_delta", `data: {"index":0,"delta":{"type":"text_delta","text":" now."}}`)
		sseWrite(w, "event: content_block_stop", `data: {"index":0}`)
		sseWrite(w, "event: content_block_start", `data: {"index":1,"content_block":{"type":"tool_use","id":"tu9","name":"trim"}}`)
		sseWrite(w, "event: content_block_delta", `data: {"index":1,"delta":{"type":"input_json_delta","partial_json":"{\"start\""}}`)
		sseWrite(w, "event: content_block_delta", `data: {"index":1,"delta":{"type":"input_json_delta","partial_json":":2}"}}`)
		sseWrite(w, "event: content_block_stop", `data: {"index":1}`)
		sseWrite(w, "event: message_delta", `data: {"delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":9}}`)
		sseWrite(w, "event: message_stop", `data: {}`)
	}))
	defer srv.Close()

	var deltas []string
	p := NewAnthropic(srv.URL, "k")
	resp, err := p.CompleteStream(context.Background(), Request{Model: "m", Messages: []Message{UserText("x")}},
		func(d Delta) { deltas = append(deltas, d.Text) })
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(deltas, ""); got != "Trimming now." {
		t.Fatalf("deltas = %q", got)
	}
	if resp.Text() != "Trimming now." || resp.StopReason != "tool_use" {
		t.Fatalf("assembled: text=%q stop=%s", resp.Text(), resp.StopReason)
	}
	uses := resp.ToolUses()
	if len(uses) != 1 || uses[0].Name != "trim" || string(uses[0].Input) != `{"start":2}` {
		t.Fatalf("tool reconstruction: %+v", uses)
	}
	if resp.InputTokens != 42 || resp.OutputTokens != 9 {
		t.Fatalf("usage: %d/%d", resp.InputTokens, resp.OutputTokens)
	}
}

func TestOpenAIStreamingAssemblesTextAndTools(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		sseWrite(w, `data: {"choices":[{"delta":{"content":"Cut"}}]}`)
		sseWrite(w, `data: {"choices":[{"delta":{"content":"ting."}}]}`)
		sseWrite(w, `data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"c7","function":{"name":"trim","arguments":"{\"sta"}}]}}]}`)
		sseWrite(w, `data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"rt\":3}"}}]}}]}`)
		sseWrite(w, `data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`)
		sseWrite(w, `data: {"choices":[],"usage":{"prompt_tokens":5,"completion_tokens":4}}`)
		sseWrite(w, `data: [DONE]`)
	}))
	defer srv.Close()

	var deltas []string
	p := NewOpenAI(srv.URL, "k", "kimi")
	resp, err := p.CompleteStream(context.Background(), Request{Model: "m", Messages: []Message{UserText("x")}},
		func(d Delta) { deltas = append(deltas, d.Text) })
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(deltas, "") != "Cutting." {
		t.Fatalf("deltas = %q", deltas)
	}
	uses := resp.ToolUses()
	if len(uses) != 1 || uses[0].ID != "c7" || string(uses[0].Input) != `{"start":3}` {
		t.Fatalf("tool reconstruction: %+v", uses)
	}
	if resp.StopReason != "tool_use" || resp.InputTokens != 5 || resp.OutputTokens != 4 {
		t.Fatalf("stop=%s usage=%d/%d", resp.StopReason, resp.InputTokens, resp.OutputTokens)
	}
}

// TestStreamRetryOnlyBeforeFirstDelta: a 500 before any bytes retries; once
// deltas have flowed, errors surface instead of replaying half a message.
func TestStreamRetryOnlyBeforeFirstDelta(t *testing.T) {
	attempts := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts == 1 {
			w.WriteHeader(500)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		sseWrite(w, `data: {"choices":[{"delta":{"content":"ok"},"finish_reason":"stop"}]}`)
		sseWrite(w, `data: [DONE]`)
	}))
	defer srv.Close()

	r := WithRetry(NewOpenAI(srv.URL, "k", "test"), nil)
	r.sleep = func(context.Context, time.Duration) error { return nil }
	resp, err := r.CompleteStream(context.Background(), Request{Model: "m", Messages: []Message{UserText("x")}}, nil)
	if err != nil || resp.Text() != "ok" || attempts != 2 {
		t.Fatalf("attempts=%d err=%v", attempts, err)
	}
}
