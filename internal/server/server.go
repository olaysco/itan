// Package server hosts the desktop editing screen: an embedded single-page
// UI over the same agent/session used by the CLI. `itan ui` starts it and
// opens the browser; packaging it into a native shell (Wails/Tauri) reuses
// this server unchanged.
//
// The chat endpoint streams NDJSON events (text deltas, tool trace, retries,
// permission requests) so the UI renders the agent's work live, and the
// permission bridge lets the browser answer allow / always / deny-with-
// feedback while the agent blocks mid-run.
package server

import (
	"context"
	"crypto/rand"
	"embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/olaysco/itan/internal/agent"
	"github.com/olaysco/itan/internal/cli"
	"github.com/olaysco/itan/internal/config"
	"github.com/olaysco/itan/internal/media"
	"github.com/olaysco/itan/internal/permission"
	"github.com/olaysco/itan/internal/skills"
	"github.com/olaysco/itan/internal/tools"
	"github.com/olaysco/itan/internal/voice"
)

//go:embed ui/index.html
var uiFS embed.FS

type Server struct {
	Session *cli.Session
	mu      sync.Mutex // one agent run at a time; edits are sequential by nature

	permMu  sync.Mutex
	pending *pendingPerm
	emit    func(map[string]any) // active stream writer (valid during a run)
	runCtx  context.Context      // ctx of the in-flight run
}

type pendingPerm struct {
	ID string
	ch chan permission.Decision
}

func New(session *cli.Session) *Server {
	s := &Server{Session: session}
	s.attachAsker()
	config.RememberProject(session.Project.Dir)
	return s
}

// attachAsker points the agent's permission gate at the web bridge. Called on
// startup and again after model switches (which rebuild the agent).
func (s *Server) attachAsker() {
	if s.Session.Agent != nil {
		s.Session.Agent.Gate.SetAsker(s.webAsker)
	}
}

// webAsker blocks the running agent while the browser shows the permission
// dialog. No stream attached, timeout, or closed tab → deny with guidance.
func (s *Server) webAsker(req permission.Request) permission.Decision {
	s.permMu.Lock()
	emit, runCtx := s.emit, s.runCtx
	if emit == nil {
		s.permMu.Unlock()
		return permission.Decision{Action: permission.Deny, Feedback: "no interactive approver attached; explain what you wanted to do"}
	}
	idBytes := make([]byte, 8)
	_, _ = rand.Read(idBytes)
	p := &pendingPerm{ID: hex.EncodeToString(idBytes), ch: make(chan permission.Decision, 1)}
	s.pending = p
	s.permMu.Unlock()

	args, _ := json.Marshal(req.Args)
	emit(map[string]any{
		"type": "permission_request", "id": p.ID,
		"tool": req.Tool, "args": string(args),
		"mutating": req.Mutating, "safety": req.Safety,
	})

	defer func() {
		s.permMu.Lock()
		s.pending = nil
		s.permMu.Unlock()
	}()
	if runCtx == nil {
		runCtx = context.Background()
	}
	select {
	case d := <-p.ch:
		return d
	case <-runCtx.Done():
		return permission.Decision{Action: permission.Deny, Feedback: "the user closed the session before answering"}
	case <-time.After(5 * time.Minute):
		return permission.Decision{Action: permission.Deny, Feedback: "permission request timed out with no answer"}
	}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /", s.handleIndex)
	mux.HandleFunc("GET /api/state", s.handleState)
	mux.HandleFunc("GET /api/history", s.handleHistory)
	mux.HandleFunc("POST /api/chat", s.handleChatStream)
	mux.HandleFunc("POST /api/permission", s.handlePermission)
	mux.HandleFunc("POST /api/upload", s.handleUpload)
	mux.HandleFunc("POST /api/asset/remove", s.handleAssetRemove)
	mux.HandleFunc("POST /api/asset/replace", s.handleAssetReplace)
	mux.HandleFunc("POST /api/reveal", s.handleReveal)
	mux.HandleFunc("POST /api/step/edit", s.handleStepEdit)
	mux.HandleFunc("POST /api/step/revert", s.handleStepRevert)
	mux.HandleFunc("GET /api/assets", s.handleAssets)
	mux.HandleFunc("GET /api/projects", s.handleProjects)
	mux.HandleFunc("POST /api/project", s.handleProjectSwitch)
	mux.HandleFunc("POST /api/undo", s.handleUndo)
	mux.HandleFunc("POST /api/revert", s.handleRevert)
	mux.HandleFunc("POST /api/model", s.handleModel)
	mux.HandleFunc("POST /api/mode", s.handleMode)
	mux.HandleFunc("POST /api/demo", s.handleDemo)
	mux.HandleFunc("POST /api/tool", s.handleTool)
	mux.HandleFunc("POST /api/voice/transcribe", s.handleTranscribe)
	mux.HandleFunc("POST /api/voice/speak", s.handleSpeak)
	mux.HandleFunc("GET /media", s.handleMedia)
	return mux
}

// Listen serves the UI until the listener fails or ctx is canceled (Ctrl+C).
// Shutdown saves the session first — closing the terminal must never cost the
// user their conversation.
func (s *Server) Listen(ctx context.Context, addr string) error {
	srv := &http.Server{Addr: addr, Handler: s.Handler()}
	errCh := make(chan error, 1)
	go func() { errCh <- srv.ListenAndServe() }()
	fmt.Printf("Itan UI on http://%s (ctrl+c to quit)\n", addr)
	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		fmt.Println("\nshutting down — session saved")
		if s.Session.Agent != nil {
			_ = s.Session.Agent.SaveSession()
		}
		shutCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		return srv.Shutdown(shutCtx)
	}
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	page, _ := uiFS.ReadFile("ui/index.html")
	// $ITAN_UI serves the page from disk instead of the embedded copy, so a
	// CSS or markup change is a browser refresh rather than a rebuild. The
	// shipped binary is unaffected — the variable is a development switch.
	if dev := os.Getenv("ITAN_UI"); dev != "" {
		if live, err := os.ReadFile(dev); err == nil {
			page = live
			w.Header().Set("Cache-Control", "no-store")
		}
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(page)
}

// --- state -----------------------------------------------------------------

type fileRef struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	URL  string `json:"url"`
	Info string `json:"info"`
}

type opView struct {
	Seq     int            `json:"seq"`
	Tool    string         `json:"tool"`
	Summary string         `json:"summary"`
	URL     string         `json:"url,omitempty"`
	Args    map[string]any `json:"args,omitempty"`
}

type checkpointView struct {
	Num   int    `json:"num"`
	Label string `json:"label"`
	When  string `json:"when"`
	Edits int    `json:"edits"`
}

type skillView struct {
	Name   string `json:"name"`
	Desc   string `json:"desc"`
	Source string `json:"source"`
	Active bool   `json:"active"`
}

type modelView struct {
	Spec   string `json:"spec"`
	Name   string `json:"name"`
	Via    string `json:"via"`
	Local  bool   `json:"local"`
	Active bool   `json:"active"`
}

type stateView struct {
	Model       string           `json:"model"`
	Project     string           `json:"project"`
	ProjectDir  string           `json:"project_dir"`
	Mode        string           `json:"mode"`
	Ffmpeg      bool             `json:"ffmpeg"`
	Assets      []fileRef        `json:"assets"`
	Ops         []opView         `json:"ops"`
	Current     string           `json:"current,omitempty"`
	CurrentInfo string           `json:"current_info,omitempty"`
	Original    string           `json:"original,omitempty"`
	Checkpoints []checkpointView `json:"checkpoints"`
	Skills      []skillView      `json:"skills"`
	Models      []modelView      `json:"models"`
	TokensIn    int              `json:"tokens_in"`
	TokensOut   int              `json:"tokens_out"`
	Cost        string           `json:"cost,omitempty"` // "≈$0.31" when the model's rates are known
	TTS         string           `json:"tts"`
	STT         string           `json:"stt"`
}

// modelRates maps model-id substrings to $/1M token (in, out) for the header
// cost estimate. Approximate by design — the readout says ≈.
var modelRates = []struct {
	match   string
	in, out float64
}{
	{"opus", 15, 75}, {"sonnet", 3, 15}, {"haiku", 1, 5},
	{"kimi-k3", 3, 15}, {"kimi-k2.5", 0.6, 3}, {"kimi", 0.6, 3},
	{"glm", 1.4, 4.4}, {"deepseek-v4-flash", 0.14, 0.28}, {"deepseek", 0.435, 0.87},
}

func estimateCost(modelID string, in, out int) string {
	id := strings.ToLower(modelID)
	for _, r := range modelRates {
		if strings.Contains(id, r.match) {
			usd := float64(in)/1e6*r.in + float64(out)/1e6*r.out
			if usd < 0.005 {
				return ""
			}
			return fmt.Sprintf("≈$%.2f", usd)
		}
	}
	return ""
}

func mediaURL(path string) string {
	if path == "" {
		return ""
	}
	return "/media?path=" + path
}

func (s *Server) state() stateView {
	p := s.Session.Project
	cfg := s.Session.Cfg
	v := stateView{
		Model:      cfg.Model.Provider + "/" + cfg.Model.ID,
		Project:    filepath.Base(p.Dir),
		ProjectDir: p.Dir,
		Mode:       string(permission.ModeAuto),
		Ffmpeg:     media.Available(),
		Current:    mediaURL(p.Current),
		Assets:     []fileRef{}, Ops: []opView{}, Checkpoints: []checkpointView{},
		Skills: []skillView{}, Models: []modelView{},
		TTS: cfg.Audio.TTS.Provider + " · " + cfg.Audio.TTS.Voice,
		STT: cfg.Audio.STT.Provider,
	}
	if len(p.Assets) > 0 {
		v.Original = mediaURL(p.Assets[0].Path)
	}
	if p.Current != "" {
		if info, err := media.Probe(context.Background(), p.Current); err == nil {
			v.CurrentInfo = info.Compact()
		}
	}
	for _, a := range p.Assets {
		v.Assets = append(v.Assets, fileRef{ID: a.ID, Name: filepath.Base(a.Path), URL: mediaURL(a.Path), Info: a.Info.Compact()})
	}
	for _, op := range p.Ops {
		v.Ops = append(v.Ops, opView{Seq: op.Seq, Tool: op.Tool, Summary: op.Summary, URL: mediaURL(op.Output), Args: op.Args})
	}

	active := map[string]bool{}
	if s.Session.Agent != nil {
		v.Mode = string(s.Session.Agent.Gate.Mode())
		v.TokensIn, v.TokensOut = s.Session.Agent.InputTokens, s.Session.Agent.OutputTokens
		v.Cost = estimateCost(cfg.Model.ID, v.TokensIn, v.TokensOut)
		cps := s.Session.Agent.Checkpoints()
		for i := len(cps) - 1; i >= 0; i-- { // newest first
			cp := cps[i]
			v.Checkpoints = append(v.Checkpoints, checkpointView{
				Num: i + 1, Label: cp.Label, When: cp.At.Local().Format("15:04"), Edits: len(cp.Ops),
			})
		}
		for _, n := range s.Session.Agent.ActiveSkills() {
			active[n] = true
		}
	}
	for _, sk := range skills.Load(cfg, p.Dir).All() {
		v.Skills = append(v.Skills, skillView{Name: sk.Name, Desc: sk.Description, Source: sk.Source, Active: active[sk.Name]})
	}
	for _, name := range config.PresetNames() {
		preset := config.Presets[name]
		v.Models = append(v.Models, modelView{
			Spec: name, Name: preset.DefaultModel, Via: preset.Note,
			Local:  preset.KeyEnv == "",
			Active: cfg.Model.Provider == name,
		})
	}
	return v
}

func (s *Server) handleState(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, s.state())
}

// --- conversation history --------------------------------------------------

type chatMsg struct {
	Role string `json:"role"` // "user" | "assistant"
	Text string `json:"text"`
}

// handleHistory returns the displayable transcript of the saved conversation
// so the chat panel survives reloads and project switches. Tool results,
// harness reminders, and synthetic nudges are model-facing plumbing — they
// are stripped, not shown.
func (s *Server) handleHistory(w http.ResponseWriter, _ *http.Request) {
	msgs := []chatMsg{}
	if s.Session.Agent != nil {
		for _, m := range s.Session.Agent.History {
			var text string
			for _, b := range m.Blocks {
				if b.Type == "text" {
					text += b.Text
				}
			}
			text = strings.TrimSpace(stripReminders(text))
			switch {
			case text == "" || text == "(no output)":
			case m.Role == "user" && strings.HasPrefix(text, "(You ended your turn"):
			case m.Role == "user" && strings.HasPrefix(text, "[frames attached"):
			case m.Role == "user" || m.Role == "assistant":
				msgs = append(msgs, chatMsg{Role: m.Role, Text: text})
			}
		}
	}
	writeJSON(w, map[string]any{"messages": msgs})
}

// stripReminders cuts the harness-appended reminder blocks off a user
// message; reminders are always appended after the user's own text.
func stripReminders(text string) string {
	cut := len(text)
	for _, marker := range []string{"\n<skill-playbook", "\n<project-state>", "\n<plan-mode>"} {
		if i := strings.Index(text, marker); i >= 0 && i < cut {
			cut = i
		}
	}
	return text[:cut]
}

// --- streaming chat --------------------------------------------------------

func (s *Server) handleChatStream(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Message string `json:"message"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Message == "" {
		httpErr(w, 400, "message required")
		return
	}
	if s.Session.Agent == nil {
		httpErr(w, 503, "no usable model configured — set an API key and restart, or switch models")
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	w.Header().Set("Content-Type", "application/x-ndjson")
	w.Header().Set("Cache-Control", "no-cache")
	flusher, canFlush := w.(http.Flusher)
	var writeMu sync.Mutex
	send := func(obj map[string]any) {
		writeMu.Lock()
		defer writeMu.Unlock()
		line, err := json.Marshal(obj)
		if err != nil {
			return
		}
		_, _ = w.Write(append(line, '\n'))
		if canFlush {
			flusher.Flush()
		}
	}

	s.permMu.Lock()
	s.emit, s.runCtx = send, r.Context()
	s.permMu.Unlock()
	defer func() {
		s.permMu.Lock()
		s.emit, s.runCtx = nil, nil
		s.permMu.Unlock()
	}()

	reply, err := s.Session.Agent.Run(r.Context(), req.Message, func(e agent.Event) {
		obj := map[string]any{"type": e.Kind}
		switch e.Kind {
		case "text_delta", "text", "thinking":
			obj["text"] = e.Text
		case "tool_start":
			obj["tool"], obj["args"] = e.Tool, e.Args
		case "tool_end":
			obj["tool"], obj["summary"], obj["output"] = e.Tool, e.Summary, mediaURL(e.Output)
			obj["err"], obj["ms"] = e.Err, e.Duration.Milliseconds()
		case "retry":
			obj["text"], obj["err"] = e.Text, e.Err
		case "permission":
			obj["tool"], obj["err"] = e.Tool, e.Err
		}
		send(obj)
	})
	if err != nil {
		send(map[string]any{"type": "error", "error": err.Error()})
		return
	}
	_ = s.Session.Agent.SaveSession()
	send(map[string]any{"type": "done", "reply": reply, "state": s.state()})
}

func (s *Server) handlePermission(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID       string `json:"id"`
		Action   string `json:"action"` // allow | deny
		Always   bool   `json:"always"`
		Feedback string `json:"feedback"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpErr(w, 400, "bad request")
		return
	}
	s.permMu.Lock()
	p := s.pending
	s.permMu.Unlock()
	if p == nil || p.ID != req.ID {
		httpErr(w, 404, "no matching pending permission request")
		return
	}
	d := permission.Decision{Action: permission.Deny, Feedback: req.Feedback}
	if req.Action == "allow" {
		d = permission.Decision{Action: permission.Allow, AlwaysAllow: req.Always}
	}
	select {
	case p.ch <- d:
		writeJSON(w, map[string]string{"status": "resolved"})
	default:
		httpErr(w, 409, "request already resolved")
	}
}

// --- project mutations -----------------------------------------------------

func (s *Server) handleUpload(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseMultipartForm(512 << 20); err != nil {
		httpErr(w, 400, "bad upload: "+err.Error())
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		httpErr(w, 400, "no file field")
		return
	}
	defer file.Close()

	dir := filepath.Join(s.Session.Project.Dir, ".itan", "uploads")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	dest := filepath.Join(dir, filepath.Base(header.Filename))
	out, err := os.Create(dest)
	if err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	if _, err := io.Copy(out, file); err != nil {
		out.Close()
		httpErr(w, 500, err.Error())
		return
	}
	out.Close()

	if _, err := s.Session.Project.AddAsset(r.Context(), dest); err != nil {
		httpErr(w, 400, err.Error())
		return
	}
	writeJSON(w, s.state())
}

func (s *Server) handleDemo(w http.ResponseWriter, r *http.Request) {
	if !media.Available() {
		httpErr(w, 400, "ffmpeg is required to generate a demo clip")
		return
	}
	dir := filepath.Join(s.Session.Project.Dir, ".itan", "uploads")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	dest := filepath.Join(dir, "demo.mp4")
	err := media.Run(r.Context(),
		"-f", "lavfi", "-i", "testsrc=duration=4:size=640x360:rate=25",
		"-f", "lavfi", "-i", "sine=frequency=440:duration=4",
		"-c:v", "libx264", "-pix_fmt", "yuv420p", "-c:a", "aac", "-shortest", dest)
	if err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	if _, err := s.Session.Project.AddAsset(r.Context(), dest); err != nil {
		httpErr(w, 400, err.Error())
		return
	}
	writeJSON(w, s.state())
}

// handleAssetRemove unregisters a source clip. The file on disk is kept —
// only the project stops referencing it.
func (s *Server) handleAssetRemove(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ID == "" {
		httpErr(w, 400, "id required")
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.Session.Project.RemoveAsset(req.ID); err != nil {
		httpErr(w, 400, err.Error())
		return
	}
	writeJSON(w, s.state())
}

// handleAssetReplace swaps the file behind an asset id with an uploaded one —
// the "use MY screenshot instead" move. The id stays stable; rendered steps
// are untouched.
func (s *Server) handleAssetReplace(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseMultipartForm(512 << 20); err != nil {
		httpErr(w, 400, "bad upload: "+err.Error())
		return
	}
	id := r.FormValue("id")
	file, header, err := r.FormFile("file")
	if err != nil || id == "" {
		httpErr(w, 400, "id and file required")
		return
	}
	defer file.Close()

	dir := filepath.Join(s.Session.Project.Dir, ".itan", "uploads")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	dest := filepath.Join(dir, fmt.Sprintf("replaced-%d-%s", time.Now().Unix(), filepath.Base(header.Filename)))
	out, err := os.Create(dest)
	if err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	if _, err := io.Copy(out, file); err != nil {
		out.Close()
		httpErr(w, 500, err.Error())
		return
	}
	out.Close()

	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.Session.Project.ReplaceAsset(r.Context(), id, dest); err != nil {
		httpErr(w, 400, err.Error())
		return
	}
	writeJSON(w, s.state())
}

// handleReveal opens the OS file manager at a project file — the user's
// files are real files; hand them over.
func (s *Server) handleReveal(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Path string `json:"path"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Path == "" {
		httpErr(w, 400, "path required")
		return
	}
	if !s.allowed(req.Path) {
		httpErr(w, 403, "not a project file")
		return
	}
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", "-R", req.Path)
	case "windows":
		cmd = exec.Command("explorer", "/select,", req.Path)
	default:
		cmd = exec.Command("xdg-open", filepath.Dir(req.Path))
	}
	if err := cmd.Start(); err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	writeJSON(w, map[string]string{"status": "ok"})
}

// --- recipe replay ---------------------------------------------------------

// handleStepEdit re-runs a past step with edited args and replays every later
// step from its recorded recipe — non-destructive editing born from the
// ledger. A checkpoint is pushed first so /revert undoes the whole replay.
func (s *Server) handleStepEdit(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Seq  int            `json:"seq"`
		Args map[string]any `json:"args"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Seq <= 0 {
		httpErr(w, 400, "seq required")
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.replayFrom(r.Context(), req.Seq, req.Args); err != nil {
		httpErr(w, 400, err.Error())
		return
	}
	if s.Session.Agent != nil {
		_ = s.Session.Agent.SaveSession()
	}
	writeJSON(w, s.state())
}

// replayFrom is the recipe engine: every op recorded its tool + args + input,
// so the ledger is a replayable program, not just history. Steps whose input
// was CURRENT re-resolve naturally as the replay advances; explicit paths are
// rewired along the old→new output chain. Old renders stay on disk.
func (s *Server) replayFrom(ctx context.Context, seq int, newArgs map[string]any) error {
	p := s.Session.Project
	idx := -1
	for i, op := range p.Ops {
		if op.Seq == seq {
			idx = i
			break
		}
	}
	if idx < 0 {
		return fmt.Errorf("no step %d", seq)
	}
	if s.Session.Agent != nil {
		s.Session.Agent.CheckpointNow(fmt.Sprintf("edit step %d (%s)", seq, p.Ops[idx].Tool))
	}

	recipe := append([]media.EditOp{}, p.Ops[idx:]...)
	p.Ops = p.Ops[:idx]
	p.Current = ""
	for i := len(p.Ops) - 1; i >= 0; i-- {
		if p.Ops[i].Output != "" {
			p.Current = p.Ops[i].Output
			break
		}
	}
	if p.Current == "" && len(p.Assets) > 0 {
		p.Current = p.Assets[0].Path
	}
	if err := p.Save(); err != nil {
		return err
	}

	reg := tools.NewRegistry()
	gate := permission.NewGate(permission.Mode(s.Session.Cfg.Mode), s.Session.Cfg.Permissions, nil)
	if s.Session.Agent != nil {
		gate = s.Session.Agent.Gate
	}
	tctx := &tools.Ctx{
		Context: ctx, Project: p, Config: s.Session.Cfg,
		TTS: voice.TTSFromConfig(s.Session.Cfg), STT: voice.STTFromConfig(s.Session.Cfg),
	}

	rewire := map[string]string{} // old output path → new output path
	for i, op := range recipe {
		args := map[string]any{}
		for k, v := range op.Args {
			args[k] = v
		}
		if i == 0 {
			for k, v := range newArgs {
				args[k] = v
			}
		}
		if in, ok := args["input"].(string); ok && in != "" {
			if nw, hit := rewire[in]; hit {
				args["input"] = nw
			}
		} else if op.Input != "" {
			if nw, hit := rewire[op.Input]; hit {
				args["input"] = nw
			}
		}

		tool, ok := reg.Get(op.Tool)
		if !ok {
			return fmt.Errorf("step %d: unknown tool %s", op.Seq, op.Tool)
		}
		if dec := gate.Check(permission.Request{Tool: op.Tool, Args: args, Mutating: tool.Mutating}); dec.Action != permission.Allow {
			return fmt.Errorf("step %d (%s) denied: %s", op.Seq, op.Tool, dec.Feedback)
		}
		raw, _ := json.Marshal(args)
		res := reg.Execute(tctx, op.Tool, raw)
		if res.Err != nil {
			return fmt.Errorf("replay of %s (step %d): %v", op.Tool, op.Seq, res.Err)
		}
		if op.Output != "" && res.Output != "" {
			rewire[op.Output] = res.Output
		}
	}
	return nil
}

// --- assets inventory ------------------------------------------------------

type assetView struct {
	ID      string `json:"id,omitempty"` // sources only
	Seq     int    `json:"seq,omitempty"`
	Kind    string `json:"kind"` // "source" | "render" | "audio"
	Name    string `json:"name"`
	URL     string `json:"url"`
	Meta    string `json:"meta"`
	Dur     string `json:"dur,omitempty"`
	Missing bool   `json:"missing,omitempty"`
	Current bool   `json:"current,omitempty"`
}

// handleAssets returns everything the project references, with honest issue
// flags: a moved/deleted file says so instead of failing later mid-edit.
// assetKind classifies a file for the drawer. Audio and images each need
// their own preview element, so guessing from a couple of extensions is not
// enough — a rendered frame is an image, not an audio track.
func assetKind(path, fallback string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".wav", ".mp3", ".m4a", ".aac", ".flac", ".ogg", ".opus":
		return "audio"
	case ".png", ".jpg", ".jpeg", ".gif", ".webp", ".svg", ".bmp", ".avif":
		return "image"
	}
	return fallback
}

func (s *Server) handleAssets(w http.ResponseWriter, _ *http.Request) {
	p := s.Session.Project
	out := []assetView{}
	add := func(v assetView, path string) {
		if _, err := os.Stat(path); err != nil {
			v.Missing = true
			v.Meta = "file moved or deleted"
		}
		v.Current = path == p.Current
		out = append(out, v)
	}
	for _, a := range p.Assets {
		v := assetView{
			ID: a.ID, Kind: assetKind(a.Path, "source"), Name: filepath.Base(a.Path),
			URL: mediaURL(a.Path), Meta: a.Info.Compact(),
		}
		// A still has no duration; showing "0.0s" reads as a broken clip.
		if a.Info.Duration > 0 {
			v.Dur = fmt.Sprintf("%.1fs", a.Info.Duration)
		}
		add(v, a.Path)
	}
	for _, op := range p.Ops {
		if op.Output == "" {
			continue
		}
		add(assetView{
			Seq: op.Seq, Kind: assetKind(op.Output, "render"), Name: filepath.Base(op.Output),
			URL: mediaURL(op.Output), Meta: fmt.Sprintf("step %03d · %s", op.Seq, op.Tool),
		}, op.Output)
	}
	writeJSON(w, map[string]any{"assets": out})
}

// handleStepRevert rewinds the ledger to a selected step: later steps drop
// off (their render files stay on disk) and CURRENT becomes that step's
// output. Checkpointed, so /revert can undo the rewind itself.
func (s *Server) handleStepRevert(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Seq int `json:"seq"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Seq <= 0 {
		httpErr(w, 400, "seq required")
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.Session.Project
	idx := -1
	for i, op := range p.Ops {
		if op.Seq == req.Seq {
			idx = i
			break
		}
	}
	if idx < 0 {
		httpErr(w, 400, fmt.Sprintf("no step %d", req.Seq))
		return
	}
	if s.Session.Agent != nil {
		s.Session.Agent.CheckpointNow(fmt.Sprintf("revert to step %d", req.Seq))
	}
	p.Ops = p.Ops[:idx+1]
	if p.Ops[idx].Output != "" {
		p.Current = p.Ops[idx].Output
	}
	if err := p.Save(); err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	if s.Session.Agent != nil {
		_ = s.Session.Agent.SaveSession()
	}
	writeJSON(w, s.state())
}

// --- projects --------------------------------------------------------------

// A project is a directory; its .itan/ subfolder carries the ledger,
// conversation, and checkpoints — so switching projects switches the whole
// session, and switching back resumes where it left off.

type projectRef struct {
	Dir    string `json:"dir"`
	Name   string `json:"name"`
	Active bool   `json:"active"`
	Meta   string `json:"meta,omitempty"` // "4 edits · today 14:12"
}

func projectMeta(dir string) string {
	path := filepath.Join(dir, ".itan", "project.json")
	st, err := os.Stat(path)
	if err != nil {
		return ""
	}
	var p struct {
		Ops []any `json:"ops"`
	}
	if data, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(data, &p)
	}
	when := st.ModTime().Format("Jan 2 15:04")
	if time.Since(st.ModTime()) < 24*time.Hour {
		when = "today " + st.ModTime().Format("15:04")
	}
	return fmt.Sprintf("%d edits · %s", len(p.Ops), when)
}

func (s *Server) handleProjects(w http.ResponseWriter, _ *http.Request) {
	cur := s.Session.Project.Dir
	refs := []projectRef{}
	seen := false
	for _, d := range config.RecentProjects() {
		if d == cur {
			seen = true
		}
		refs = append(refs, projectRef{Dir: d, Name: filepath.Base(d), Active: d == cur, Meta: projectMeta(d)})
	}
	if !seen {
		refs = append([]projectRef{{Dir: cur, Name: filepath.Base(cur), Active: true, Meta: projectMeta(cur)}}, refs...)
	}
	writeJSON(w, map[string]any{"projects": refs})
}

// resolveProjectDir turns what a person types into a predictable absolute
// path: `~` expands, and bare names land under the home directory — never
// silently under the server's working directory (which created literal "~"
// folders and projects in surprising places).
func resolveProjectDir(input string) (string, error) {
	dir := strings.TrimSpace(input)
	if dir == "" {
		return "", fmt.Errorf("dir required")
	}
	if dir == "~" || strings.HasPrefix(dir, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		dir = filepath.Join(home, strings.TrimPrefix(dir, "~"))
	}
	if !filepath.IsAbs(dir) {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		dir = filepath.Join(home, dir)
	}
	return filepath.Abs(dir)
}

// handleProjectSwitch opens (or creates) the project at dir and swaps the
// whole session to it, restoring that project's saved conversation.
func (s *Server) handleProjectSwitch(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Dir string `json:"dir"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpErr(w, 400, "dir required")
		return
	}
	abs, err := resolveProjectDir(req.Dir)
	if err != nil {
		httpErr(w, 400, err.Error())
		return
	}
	if err := os.MkdirAll(abs, 0o755); err != nil {
		httpErr(w, 400, "cannot open "+abs+": "+err.Error())
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, err := cli.NewSession(abs, true)
	if err != nil {
		httpErr(w, 400, err.Error())
		return
	}
	if s.Session.Agent != nil {
		_ = s.Session.Agent.SaveSession() // don't lose the outgoing conversation
	}
	s.Session = sess
	s.attachAsker()
	config.RememberProject(abs)
	writeJSON(w, s.state())
}

func (s *Server) handleUndo(w http.ResponseWriter, _ *http.Request) {
	if _, err := s.Session.Project.Undo(); err != nil {
		httpErr(w, 400, err.Error())
		return
	}
	writeJSON(w, s.state())
}

func (s *Server) handleRevert(w http.ResponseWriter, r *http.Request) {
	var req struct {
		N int `json:"n"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	if s.Session.Agent == nil {
		httpErr(w, 503, "no agent")
		return
	}
	if _, err := s.Session.Agent.Revert(req.N); err != nil {
		httpErr(w, 400, err.Error())
		return
	}
	_ = s.Session.Agent.SaveSession()
	writeJSON(w, s.state())
}

func (s *Server) handleModel(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Spec string `json:"spec"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Spec == "" {
		httpErr(w, 400, "spec required")
		return
	}
	if err := s.Session.SwitchModel(req.Spec); err != nil {
		httpErr(w, 400, err.Error())
		return
	}
	s.attachAsker() // rebuild installed the terminal asker; take it back
	writeJSON(w, s.state())
}

func (s *Server) handleMode(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Mode string `json:"mode"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpErr(w, 400, "bad request")
		return
	}
	m := permission.Mode(req.Mode)
	if m != permission.ModeAuto && m != permission.ModeAsk && m != permission.ModePlan {
		httpErr(w, 400, "modes: auto, ask, plan")
		return
	}
	if s.Session.Agent == nil {
		httpErr(w, 503, "no agent")
		return
	}
	s.Session.Agent.Gate.SetMode(m)
	writeJSON(w, s.state())
}

// --- timeline gestures -----------------------------------------------------

// gestureTools are the direct-manipulation gestures the timeline may issue
// without the model in the loop. Each maps 1:1 onto a registry tool and lands
// as a normal ledger op — undoable, checkpointed, permission-gated. The
// whitelist keeps chat as the control surface for everything else.
var gestureTools = map[string]bool{"trim": true, "cut_range": true, "concat": true, "export": true}

// handleTool executes one whitelisted tool call directly (no LLM): instant,
// token-free, and honest — plan mode and deny rules still block it.
func (s *Server) handleTool(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name string         `json:"name"`
		Args map[string]any `json:"args"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Name == "" {
		httpErr(w, 400, "name required")
		return
	}
	if !gestureTools[req.Name] {
		httpErr(w, 400, req.Name+" is not a gesture tool — ask for it in chat instead")
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	reg := tools.NewRegistry()
	tool, _ := reg.Get(req.Name)
	gate := permission.NewGate(permission.Mode(s.Session.Cfg.Mode), s.Session.Cfg.Permissions, nil)
	if s.Session.Agent != nil {
		gate = s.Session.Agent.Gate
	}
	if dec := gate.Check(permission.Request{Tool: req.Name, Args: req.Args, Mutating: tool.Mutating}); dec.Action != permission.Allow {
		httpErr(w, 403, dec.Feedback)
		return
	}

	raw, _ := json.Marshal(req.Args)
	tctx := &tools.Ctx{
		Context: r.Context(), Project: s.Session.Project, Config: s.Session.Cfg,
		TTS: voice.TTSFromConfig(s.Session.Cfg), STT: voice.STTFromConfig(s.Session.Cfg),
	}
	res := reg.Execute(tctx, req.Name, raw)
	if res.Err != nil {
		httpErr(w, 500, res.Err.Error())
		return
	}
	if s.Session.Agent != nil {
		_ = s.Session.Agent.SaveSession()
	}
	writeJSON(w, map[string]any{"summary": res.Summary, "state": s.state()})
}

// --- voice -----------------------------------------------------------------

// handleTranscribe accepts browser-recorded audio (webm/ogg/wav), converts it
// with ffmpeg, and returns the Whisper transcript.
func (s *Server) handleTranscribe(w http.ResponseWriter, r *http.Request) {
	raw, err := io.ReadAll(io.LimitReader(r.Body, 32<<20))
	if err != nil || len(raw) == 0 {
		httpErr(w, 400, "no audio body")
		return
	}
	tmpDir, err := os.MkdirTemp("", "itan-voice-*")
	if err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	defer os.RemoveAll(tmpDir)
	src := filepath.Join(tmpDir, "in.webm")
	wav := filepath.Join(tmpDir, "in.wav")
	if err := os.WriteFile(src, raw, 0o600); err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	if err := media.Run(r.Context(), "-i", src, "-vn", "-acodec", "pcm_s16le", "-ar", "16000", "-ac", "1", wav); err != nil {
		httpErr(w, 500, "audio convert failed: "+err.Error())
		return
	}
	stt := voice.STTFromConfig(s.Session.Cfg)
	text, err := stt.Transcribe(r.Context(), wav)
	if err != nil {
		// 502 → the UI shows the mic-error state with the fix-it hint.
		httpErr(w, 502, err.Error())
		return
	}
	writeJSON(w, map[string]string{"text": text})
}

// handleSpeak synthesizes the reply with the configured TTS and returns wav.
func (s *Server) handleSpeak(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Text string `json:"text"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Text == "" {
		httpErr(w, 400, "text required")
		return
	}
	tmpDir, err := os.MkdirTemp("", "itan-voice-*")
	if err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	defer os.RemoveAll(tmpDir)
	out := filepath.Join(tmpDir, "out.wav")
	tts := voice.TTSFromConfig(s.Session.Cfg)
	if err := tts.Speak(r.Context(), req.Text, out); err != nil {
		httpErr(w, 502, err.Error())
		return
	}
	data, err := os.ReadFile(out)
	if err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	w.Header().Set("Content-Type", "audio/wav")
	_, _ = w.Write(data)
}

// --- media -----------------------------------------------------------------

// handleMedia serves only files the project actually references — assets,
// op outputs, current — never arbitrary paths.
func (s *Server) handleMedia(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Query().Get("path")
	if !s.allowed(path) {
		httpErr(w, 403, "not a project file")
		return
	}
	http.ServeFile(w, r, path)
}

func (s *Server) allowed(path string) bool {
	p := s.Session.Project
	if path == "" {
		return false
	}
	if path == p.Current {
		return true
	}
	for _, a := range p.Assets {
		if a.Path == path {
			return true
		}
	}
	for _, op := range p.Ops {
		if op.Output == path {
			return true
		}
	}
	return false
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func httpErr(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
