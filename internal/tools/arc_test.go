package tools

import (
	"context"
	"fmt"
	"math"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/olaysco/itan/internal/media"
)

// fakeTTS synthesizes a tone whose length is proportional to the text, which
// is the property that matters here: voice_scenes must take its timing from
// the audio it actually produced, not from anything it was told.
type fakeTTS struct{ secPerChar float64 }

func (f *fakeTTS) Describe() string { return "fake tts" }
func (f *fakeTTS) Speak(ctx context.Context, text, outPath string) error {
	d := float64(len(text)) * f.secPerChar
	if d < 0.2 {
		d = 0.2
	}
	return exec.CommandContext(ctx, "ffmpeg", "-y", "-loglevel", "error",
		"-f", "lavfi", "-i", fmt.Sprintf("sine=frequency=300:duration=%.3f", d),
		outPath).Run()
}

// makeSilentClip is a scene render with no audio of its own, so what is
// heard in the assembled piece is only the narration — which is what the
// offset test needs to measure.
func makeSilentClip(t *testing.T, dir, name string, dur int) string {
	t.Helper()
	out := filepath.Join(dir, name)
	cmd := exec.Command("ffmpeg", "-y", "-loglevel", "error",
		"-f", "lavfi", "-i", fmt.Sprintf("testsrc=duration=%d:size=320x240:rate=10", dur),
		"-c:v", "libx264", "-pix_fmt", "yuv420p", out)
	if raw, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("clip: %v\n%s", err, raw)
	}
	return out
}

func arcCtx(t *testing.T) *Ctx {
	t.Helper()
	c := composeCtx(t)
	c.TTS = &fakeTTS{secPerChar: 0.06}
	return c
}

// TestVoiceScenesRetimesToTheRealRead is the reason this stage exists: a
// planned duration is a guess, and composing to a guess is what makes
// narration overrun its picture.
func TestVoiceScenesRetimesToTheRealRead(t *testing.T) {
	c := arcCtx(t)
	r := NewRegistry()

	res := r.Execute(c, "storyboard", []byte(`{"scenes":[
	  {"n":1,"intent":"hook","say":"Short line.","visual":"logo","duration":4},
	  {"n":2,"intent":"body","say":"A considerably longer line that plainly takes more time to read aloud than the first one does.","visual":"ui","duration":4},
	  {"n":3,"intent":"end","visual":"end card","duration":2}]}`))
	if res.Err != nil {
		t.Fatalf("storyboard: %v", res.Err)
	}

	if res = r.Execute(c, "voice_scenes", []byte(`{"pad":0.5}`)); res.Err != nil {
		t.Fatalf("voice_scenes: %v", res.Err)
	}
	s := c.Project.Scenes
	if s[0].Voice == "" || s[1].Voice == "" {
		t.Fatal("narrated scenes did not get voice tracks")
	}
	if s[2].Voice != "" {
		t.Error("a scene with no `say` must not be voiced")
	}
	if s[0].Duration == 4 || s[1].Duration == 4 {
		t.Errorf("durations still the planned guess: %.2f, %.2f", s[0].Duration, s[1].Duration)
	}
	if s[1].Duration <= s[0].Duration {
		t.Errorf("the longer line did not get the longer scene: %.2f vs %.2f", s[1].Duration, s[0].Duration)
	}
	if s[2].Duration != 2 {
		t.Errorf("a silent scene's duration was changed: %.2f", s[2].Duration)
	}
	// Duration must be the measured read plus the pad, not an estimate.
	info, err := media.Probe(context.Background(), s[0].Voice)
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(s[0].Duration-(info.Duration+0.5)) > 0.05 {
		t.Errorf("scene 1 duration %.3f is not the read (%.3f) plus pad", s[0].Duration, info.Duration)
	}
}

// Re-running must not re-synthesize unchanged lines, and rewriting a line
// must drop that scene's stale voice track — otherwise new words play over
// old audio.
func TestVoiceScenesReusesAndInvalidates(t *testing.T) {
	c := arcCtx(t)
	r := NewRegistry()
	r.Execute(c, "storyboard", []byte(`{"scenes":[{"n":1,"intent":"a","say":"First take.","duration":3}]}`))
	r.Execute(c, "voice_scenes", []byte(`{}`))
	first := c.Project.Scenes[0].Voice
	if first == "" {
		t.Fatal("no voice track")
	}

	res := r.Execute(c, "voice_scenes", []byte(`{}`))
	if c.Project.Scenes[0].Voice != first {
		t.Error("unchanged line was re-synthesized")
	}
	if !strings.Contains(res.Summary, "1 reused") {
		t.Errorf("summary should say the track was reused: %s", res.Summary)
	}

	r.Execute(c, "storyboard", []byte(`{"scenes":[{"n":1,"intent":"a","say":"Second take, different words.","duration":3}]}`))
	if c.Project.Scenes[0].Voice != "" {
		t.Error("rewriting the line kept the old voice track — new words would play over old audio")
	}
	r.Execute(c, "voice_scenes", []byte(`{}`))
	if c.Project.Scenes[0].Voice == first {
		t.Error("rewritten line reused the old take")
	}
}

// A scene is addressable by content, the way people ask for changes.
func TestSceneAddressing(t *testing.T) {
	c := arcCtx(t)
	r := NewRegistry()
	clip := makeTierClip(t, c.Project.Dir, "s1.mp4", 2)
	if _, err := c.Project.AddAsset(context.Background(), clip); err != nil {
		t.Fatal(err)
	}
	r.Execute(c, "storyboard", []byte(`{"scenes":[{"n":1,"intent":"hook","duration":2},{"n":2,"intent":"body","duration":2}]}`))
	r.Execute(c, "storyboard", []byte(`{"mark_rendered":{"n":1,"output":"a1"}}`))

	for _, ref := range []string{"scene 1", "scene1", "s1"} {
		got, err := resolveInput(c, Args{"input": ref})
		if err != nil {
			t.Fatalf("%q: %v", ref, err)
		}
		if got != clip {
			t.Errorf("%q resolved to %s, want %s", ref, got, clip)
		}
	}
	if _, err := resolveInput(c, Args{"input": "scene 2"}); err == nil {
		t.Error("an unrendered scene must not resolve to something")
	} else if !strings.Contains(err.Error(), "not rendered") {
		t.Errorf("unhelpful error for an unrendered scene: %v", err)
	}
	if _, err := resolveInput(c, Args{"input": "scene 9"}); err == nil {
		t.Error("a scene that does not exist must not resolve")
	}
}

// TestAssembleAlignsNarrationToTheCut: the whole point of assemble is that
// offsets come from the rendered scenes, so narration stays on its picture
// even when a scene was composed shorter or longer than planned.
func TestAssembleAlignsNarrationToTheCut(t *testing.T) {
	c := arcCtx(t)
	r := NewRegistry()

	res := r.Execute(c, "storyboard", []byte(`{"scenes":[
	  {"n":1,"intent":"hook","say":"One.","duration":9},
	  {"n":2,"intent":"body","say":"Two.","duration":9}]}`))
	if res.Err != nil {
		t.Fatal(res.Err)
	}
	if res = r.Execute(c, "voice_scenes", []byte(`{}`)); res.Err != nil {
		t.Fatal(res.Err)
	}

	// Deliberately compose scenes that do NOT match the planned durations.
	for i, dur := range []int{2, 3} {
		clip := makeTierClip(t, c.Project.Dir, fmt.Sprintf("scene%d.mp4", i+1), dur)
		if _, err := c.Project.AddAsset(context.Background(), clip); err != nil {
			t.Fatal(err)
		}
		res = r.Execute(c, "storyboard", []byte(fmt.Sprintf(`{"mark_rendered":{"n":%d,"output":"%s"}}`, i+1, clip)))
		if res.Err != nil {
			t.Fatal(res.Err)
		}
	}

	res = r.Execute(c, "assemble", []byte(`{}`))
	if res.Err != nil {
		t.Fatalf("assemble: %v", res.Err)
	}
	info, err := media.Probe(context.Background(), c.Project.Current)
	if err != nil {
		t.Fatal(err)
	}
	// 2 + 3 from the real renders, not 9 + 9 from the plan.
	if info.Duration < 4.6 || info.Duration > 5.6 {
		t.Fatalf("assembled duration %.2fs — offsets came from the plan, not the cut", info.Duration)
	}
	if !info.HasAudio {
		t.Fatal("assembled piece has no audio — narration was dropped")
	}
	if !strings.Contains(res.Summary, "2 narration tracks") {
		t.Errorf("summary does not account for the narration: %s", res.Summary)
	}
}

func TestAssembleRefusesUnrenderedScenes(t *testing.T) {
	c := arcCtx(t)
	r := NewRegistry()
	r.Execute(c, "storyboard", []byte(`{"scenes":[{"n":1,"intent":"a","duration":2},{"n":2,"intent":"b","duration":2}]}`))
	res := r.Execute(c, "assemble", []byte(`{}`))
	if res.Err == nil {
		t.Fatal("assembling an unrendered storyboard must fail")
	}
	if !strings.Contains(res.Err.Error(), "1, 2") {
		t.Errorf("error should name the missing scenes: %v", res.Err)
	}
}

// TestAssemblePlacesNarrationAtTheRealOffset checks the claim directly in
// the audio: scene 2's line must start where scene 2's picture starts on the
// finished cut, not where the storyboard guessed it would.
func TestAssemblePlacesNarrationAtTheRealOffset(t *testing.T) {
	c := arcCtx(t)
	r := NewRegistry()

	// ~2.4s and ~1.2s of speech, against planned durations that are wildly
	// wrong — if offsets came from the plan, scene 2 would land at 9s.
	res := r.Execute(c, "storyboard", []byte(`{"scenes":[
	  {"n":1,"intent":"a","say":"0123456789012345678901234567890123456789","duration":9},
	  {"n":2,"intent":"b","say":"01234567890123456789","duration":9}]}`))
	if res.Err != nil {
		t.Fatal(res.Err)
	}
	if res = r.Execute(c, "voice_scenes", []byte(`{}`)); res.Err != nil {
		t.Fatal(res.Err)
	}
	for i, dur := range []int{4, 3} {
		clip := makeSilentClip(t, c.Project.Dir, fmt.Sprintf("sc%d.mp4", i+1), dur)
		if res = r.Execute(c, "storyboard", []byte(fmt.Sprintf(`{"mark_rendered":{"n":%d,"output":"%s"}}`, i+1, clip))); res.Err != nil {
			t.Fatal(res.Err)
		}
	}
	if res = r.Execute(c, "assemble", []byte(`{}`)); res.Err != nil {
		t.Fatalf("assemble: %v", res.Err)
	}

	// The gap between the two lines is silence; where it ENDS is where
	// scene 2's narration begins.
	out, err := exec.Command("ffmpeg", "-i", c.Project.Current,
		"-af", "silencedetect=n=-45dB:d=0.4", "-f", "null", "-").CombinedOutput()
	if err != nil {
		t.Fatalf("silencedetect: %v\n%s", err, out)
	}
	var ends []float64
	for _, line := range strings.Split(string(out), "\n") {
		i := strings.Index(line, "silence_end: ")
		if i < 0 {
			continue
		}
		var v float64
		if _, err := fmt.Sscanf(strings.Fields(line[i+len("silence_end: "):])[0], "%f", &v); err == nil {
			ends = append(ends, v)
		}
	}
	if len(ends) == 0 {
		t.Fatalf("no silence boundary found — the two lines were not laid out separately:\n%s", out)
	}
	// Scene 1's render is 4s long, so scene 2's line starts at 4s.
	var found bool
	for _, e := range ends {
		if math.Abs(e-4.0) < 0.35 {
			found = true
		}
	}
	if !found {
		t.Fatalf("scene 2's narration does not start at the 4.0s cut point (silence ended at %v)", ends)
	}
}
