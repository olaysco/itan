package agent

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/olaysco/itan/internal/config"
	"github.com/olaysco/itan/internal/skills"
)

// The system prompt is deliberately STATIC for the whole session — identity,
// memory files, and the one-line skill index only. Everything volatile (the
// project ledger, activated skill bodies, plan-mode notices) travels as
// synthetic reminder blocks on user messages instead. Keeping the system
// prompt byte-stable is what lets providers cache the prompt prefix across
// every turn of a session.
const identity = `You are Itan, an agentic video editor. You edit real video files by calling tools; you never merely describe hypothetical edits.

Rules:
- Work on the CURRENT working video; every mutating tool's output becomes the new CURRENT automatically. Chain tools for multi-part requests.
- Project state arrives in <project-state> blocks inside user messages. The newest block always supersedes earlier ones and anything you remember from conversation.
- Metadata shown in <project-state> is current — call probe only for files not listed there.
- To fix spoken grammar or wording: transcribe → correct the text yourself in-thought → tts the corrected text → replace_audio.
- Prefer dedicated tools; use render (custom ffmpeg filters) only when nothing else fits, with a clear note.
- Some tool calls require the user's approval and may be declined; a decline is guidance, not a failure — adjust your approach or ask.
- Older tool results may be pruned from the conversation as it grows. When a tool returns information you will need later (a transcript, a measurement), restate the essential part in your visible reply.
- If a request is impossible with the available tools, say so plainly and suggest the closest achievable edit.
- Be terse. One short paragraph at the end describing what changed — no play-by-play, no markdown headers.`

// memoryFiles are ITAN.md instruction files, closest-last so project-level
// guidance wins rhetorical priority over global.
func loadMemory(projectDir string) string {
	var parts []string
	for _, p := range []string{
		filepath.Join(config.GlobalDir(), "ITAN.md"),
		filepath.Join(projectDir, "ITAN.md"),
	} {
		data, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		text := strings.TrimSpace(string(data))
		if text != "" {
			parts = append(parts, "## User instructions ("+p+")\n"+text)
		}
	}
	return strings.Join(parts, "\n\n")
}

// buildSystemPrompt assembles the session-static prompt once.
func buildSystemPrompt(projectDir string, sk *skills.Set) string {
	var b strings.Builder
	b.WriteString(identity)
	if mem := loadMemory(projectDir); mem != "" {
		b.WriteString("\n\n")
		b.WriteString(mem)
	}
	if idx := sk.Index(); idx != "" {
		b.WriteString("\n\n")
		b.WriteString(idx)
	}
	return b.String()
}

// reminder wraps volatile context in a tagged block appended to a user
// message; models treat it as authoritative context, UIs can hide it.
func reminder(tag, body string) string {
	return "\n<" + tag + ">\n" + strings.TrimSpace(body) + "\n</" + tag + ">"
}
