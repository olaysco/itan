package provider

import (
	"bufio"
	"context"
	"io"
	"strings"
)

// Delta is one streamed increment of assistant text.
type Delta struct {
	Text string
}

// Streamer is implemented by providers that support server-sent-event
// streaming. CompleteStream returns the same fully-assembled Response as
// Complete — tool calls included — while emitting text deltas as they arrive.
type Streamer interface {
	Provider
	CompleteStream(ctx context.Context, req Request, onDelta func(Delta)) (*Response, error)
}

// readSSE walks "event:/data:" frames, calling handle for each data payload
// (event name may be empty — OpenAI omits it). handle returning io.EOF ends
// the stream cleanly; any other error aborts.
func readSSE(ctx context.Context, body io.Reader, handle func(event, data string) error) error {
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	event := ""
	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return err
		}
		line := scanner.Text()
		switch {
		case line == "":
			event = "" // frame boundary
		case strings.HasPrefix(line, "event:"):
			event = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		case strings.HasPrefix(line, "data:"):
			data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			if err := handle(event, data); err != nil {
				if err == io.EOF {
					return nil
				}
				return err
			}
		}
	}
	return scanner.Err()
}
