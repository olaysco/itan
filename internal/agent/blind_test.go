package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/olaysco/itan/internal/provider"
)

// refusesImages answers exactly as OpenRouter did on a real run: a 404 when
// the request carries images, normal service when it does not.
type refusesImages struct {
	after     []*provider.Response
	calls     int
	withImage int
	requests  []provider.Request
}

func (r *refusesImages) Name() string { return "refuses-images" }

func (r *refusesImages) Complete(_ context.Context, req provider.Request) (*provider.Response, error) {
	r.requests = append(r.requests, req)
	r.calls++
	for _, m := range req.Messages {
		if hasImages(m) {
			r.withImage++
			return nil, &provider.HTTPError{
				Provider: "openrouter", Status: 404,
				Body: `{"error":{"message":"No endpoints found that support image input","code":404}}`,
			}
		}
	}
	if len(r.after) == 0 {
		return &provider.Response{Blocks: []provider.Block{provider.TextBlock("done")}, StopReason: "end_turn"}, nil
	}
	resp := r.after[0]
	r.after = r.after[1:]
	return resp, nil
}

// TestTextOnlyModelSurvivesFrames is the regression for a run that composed
// four scenes over eleven minutes and then died on the view_strip that was
// meant to check the work: the frames went to a text-only model, the 404
// ended the run, and the reply was lost.
func TestTextOnlyModelSurvivesFrames(t *testing.T) {
	fake := &refusesImages{after: []*provider.Response{
		{Blocks: []provider.Block{toolUse("t1", "view_frames", `{"times":[0.5]}`)}, StopReason: "tool_use"},
		{Blocks: []provider.Block{provider.TextBlock("Could not see the frames; the cut is 3s at 320x240.")}, StopReason: "end_turn"},
	}}
	a, proj := newTestAgent(t, fake)
	clip := makeClip(t, proj.Dir)
	if _, err := proj.AddAsset(context.Background(), clip); err != nil {
		t.Fatal(err)
	}

	var events []Event
	reply, err := a.Run(context.Background(), "check the frame", func(e Event) { events = append(events, e) })
	if err != nil {
		t.Fatalf("a text-only model must not end the run: %v", err)
	}
	if reply == "" {
		t.Fatal("the run produced no reply")
	}
	if fake.withImage != 1 {
		t.Errorf("images were sent %d times; it should learn after the first refusal", fake.withImage)
	}

	// The user has to be told why, and what to do about it.
	var note string
	for _, e := range events {
		if e.Kind == "text" && strings.Contains(e.Text, "cannot accept images") {
			note = e.Text
		}
	}
	if note == "" {
		t.Fatal("no note explaining that the frames were dropped")
	}
	if !strings.Contains(note, "model.vision") {
		t.Errorf("the note does not say how to fix it: %s", note)
	}

	// The model must be told it did NOT see the frames — a vaguer note
	// invites it to review pictures it never received.
	var sawWarning bool
	for _, req := range fake.requests {
		for _, m := range req.Messages {
			for _, b := range m.Blocks {
				if strings.Contains(b.Content, "could NOT be shown") &&
					strings.Contains(b.Content, "You have not seen them") {
					sawWarning = true
				}
			}
		}
	}
	if !sawWarning {
		t.Error("history does not tell the model the frames were never shown")
	}
}

// Once it has learned, later frame-producing tools must not re-attach images
// and pay for the same failure again.
func TestBlindModelStopsAttachingFrames(t *testing.T) {
	fake := &refusesImages{after: []*provider.Response{
		{Blocks: []provider.Block{toolUse("t1", "view_frames", `{"times":[0.5]}`)}, StopReason: "tool_use"},
		{Blocks: []provider.Block{toolUse("t2", "view_frames", `{"times":[1.5]}`)}, StopReason: "tool_use"},
		{Blocks: []provider.Block{provider.TextBlock("ok")}, StopReason: "end_turn"},
	}}
	a, proj := newTestAgent(t, fake)
	clip := makeClip(t, proj.Dir)
	if _, err := proj.AddAsset(context.Background(), clip); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Run(context.Background(), "look twice", nil); err != nil {
		t.Fatal(err)
	}
	if fake.withImage != 1 {
		t.Errorf("hit the image refusal %d times; it should latch after the first", fake.withImage)
	}
	if !a.blindModel {
		t.Error("the agent did not remember that this model cannot see")
	}
}

// A model that CAN see must be unaffected: frames still get through.
func TestSightedModelStillGetsFrames(t *testing.T) {
	fake := &scripted{responses: []*provider.Response{
		{Blocks: []provider.Block{toolUse("t1", "view_frames", `{"times":[0.5]}`)}, StopReason: "tool_use"},
		{Blocks: []provider.Block{provider.TextBlock("The frame looks right.")}, StopReason: "end_turn"},
	}}
	a, proj := newTestAgent(t, fake)
	clip := makeClip(t, proj.Dir)
	if _, err := proj.AddAsset(context.Background(), clip); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Run(context.Background(), "check it", nil); err != nil {
		t.Fatal(err)
	}
	if a.blindModel {
		t.Fatal("a working model was marked blind")
	}
	last := fake.requests[len(fake.requests)-1]
	if !hasImages(last.Messages[len(last.Messages)-1]) {
		t.Fatal("frames never reached a model that accepts them")
	}
}
