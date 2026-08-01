package server

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/olaysco/itan/internal/agent"
	"github.com/olaysco/itan/internal/cli"
	"github.com/olaysco/itan/internal/config"
	"github.com/olaysco/itan/internal/media"
	"github.com/olaysco/itan/internal/provider"
	"github.com/olaysco/itan/internal/skills"
)

type nullProvider struct{}

func (nullProvider) Name() string { return "null" }
func (nullProvider) Complete(context.Context, provider.Request) (*provider.Response, error) {
	return &provider.Response{StopReason: "end_turn"}, nil
}

// The chat panel restores from /api/history: user and assistant text only,
// with harness reminders, tool results, and synthetic nudges stripped.
func TestHistoryEndpoint(t *testing.T) {
	dir := t.TempDir()
	proj, err := media.LoadProject(dir)
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	ag := agent.New(cfg, nullProvider{}, proj, skills.Load(cfg, dir))
	ag.History = []provider.Message{
		provider.UserText("make it a tiktok\n<skill-playbook name=\"tiktok\">\nsecret playbook body\n</skill-playbook name=\"tiktok\">\n<project-state>\nledger\n</project-state>"),
		{Role: "assistant", Blocks: []provider.Block{
			{Type: "tool_use", ID: "t1", Name: "trim", Input: []byte(`{}`)},
		}},
		{Role: "user", Blocks: []provider.Block{provider.ToolResultBlock("t1", "trimmed", false)}},
		{Role: "assistant", Blocks: []provider.Block{provider.TextBlock("Trimmed it.")}},
		provider.UserText("(You ended your turn without any reply. If the task is unfinished, continue it now with tool calls; otherwise summarize what changed in one short paragraph.)"),
		{Role: "assistant", Blocks: []provider.Block{provider.TextBlock("(no output)")}},
	}
	s := New(&cli.Session{Cfg: cfg, Project: proj, Agent: ag})

	rec := httptest.NewRecorder()
	s.handleHistory(rec, nil)
	var out struct {
		Messages []chatMsg `json:"messages"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Messages) != 2 {
		t.Fatalf("messages = %+v, want 2", out.Messages)
	}
	if out.Messages[0].Role != "user" || out.Messages[0].Text != "make it a tiktok" {
		t.Fatalf("user message not stripped of reminders: %+v", out.Messages[0])
	}
	if out.Messages[1].Role != "assistant" || out.Messages[1].Text != "Trimmed it." {
		t.Fatalf("assistant message wrong: %+v", out.Messages[1])
	}
}
