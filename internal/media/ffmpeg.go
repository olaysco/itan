// Package media wraps ffmpeg/ffprobe and owns the project edit ledger.
package media

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// RenderTimeout bounds every ffmpeg invocation so a bad filtergraph can never
// hang the harness.
var RenderTimeout = 10 * time.Minute

type Info struct {
	Width    int     `json:"w"`
	Height   int     `json:"h"`
	Duration float64 `json:"dur"`
	FPS      float64 `json:"fps"`
	HasAudio bool    `json:"audio"`
	Codec    string  `json:"codec,omitempty"`
}

func (i Info) Aspect() float64 {
	if i.Height == 0 {
		return 0
	}
	return float64(i.Width) / float64(i.Height)
}

// Compact renders metadata in the terse form used by the ledger and tool
// results, e.g. "640x360 25fps 4.0s audio:yes".
func (i Info) Compact() string {
	audio := "no"
	if i.HasAudio {
		audio = "yes"
	}
	return fmt.Sprintf("%dx%d %.4gfps %.1fs audio:%s", i.Width, i.Height, i.FPS, i.Duration, audio)
}

func Available() bool {
	_, err1 := exec.LookPath("ffmpeg")
	_, err2 := exec.LookPath("ffprobe")
	return err1 == nil && err2 == nil
}

// Run executes ffmpeg with the given args under the render timeout.
func Run(ctx context.Context, args ...string) error {
	ctx, cancel := context.WithTimeout(ctx, RenderTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "ffmpeg", append([]string{"-hide_banner", "-y"}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return fmt.Errorf("ffmpeg timed out after %s", RenderTimeout)
		}
		return fmt.Errorf("ffmpeg: %s", tail(string(out), 500))
	}
	return nil
}

// Probe returns metadata for a media file.
func Probe(ctx context.Context, path string) (Info, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "ffprobe",
		"-v", "quiet", "-print_format", "json", "-show_streams", "-show_format", path)
	out, err := cmd.Output()
	if err != nil {
		return Info{}, fmt.Errorf("ffprobe %s: %w", path, err)
	}

	var data struct {
		Streams []struct {
			CodecType  string `json:"codec_type"`
			CodecName  string `json:"codec_name"`
			Width      int    `json:"width"`
			Height     int    `json:"height"`
			RFrameRate string `json:"r_frame_rate"`
		} `json:"streams"`
		Format struct {
			Duration string `json:"duration"`
		} `json:"format"`
	}
	if err := json.Unmarshal(out, &data); err != nil {
		return Info{}, err
	}

	info := Info{}
	info.Duration, _ = strconv.ParseFloat(data.Format.Duration, 64)
	for _, s := range data.Streams {
		switch s.CodecType {
		case "video":
			info.Width, info.Height, info.Codec = s.Width, s.Height, s.CodecName
			info.FPS = parseRate(s.RFrameRate)
		case "audio":
			info.HasAudio = true
		}
	}
	if info.Width == 0 && !info.HasAudio {
		return Info{}, fmt.Errorf("no media streams in %s", path)
	}
	return info, nil
}

func parseRate(r string) float64 {
	num, den, ok := strings.Cut(r, "/")
	n, _ := strconv.ParseFloat(num, 64)
	if !ok {
		return n
	}
	d, _ := strconv.ParseFloat(den, 64)
	if d == 0 {
		return n
	}
	return n / d
}

// EvenDims rounds dimensions up to even numbers (required by yuv420p encoders).
func EvenDims(w, h int) (int, int) {
	return w + w%2, h + h%2
}

func tail(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return "…" + s[len(s)-n:]
}
