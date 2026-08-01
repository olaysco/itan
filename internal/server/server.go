// Package server hosts the desktop editing screen: an embedded single-page
// UI over the same agent/session used by the CLI. `heydit ui` starts it and
// opens the browser; packaging it into a native shell (Wails/Tauri) reuses
// this server unchanged.
package server

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sync"

	"github.com/olaysco/heydit/internal/agent"
	"github.com/olaysco/heydit/internal/cli"
	"github.com/olaysco/heydit/internal/media"
)

//go:embed ui/index.html
var uiFS embed.FS

type Server struct {
	Session *cli.Session
	mu      sync.Mutex // one agent run at a time; edits are sequential by nature
}

func New(session *cli.Session) *Server {
	// Headless: there is no blocking terminal prompt in the browser flow, so
	// "ask" degrades to deny-with-feedback and the model explains itself.
	if session.Agent != nil {
		session.Agent.Gate.SetAsker(nil)
	}
	return &Server{Session: session}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /", s.handleIndex)
	mux.HandleFunc("GET /api/state", s.handleState)
	mux.HandleFunc("POST /api/chat", s.handleChat)
	mux.HandleFunc("POST /api/upload", s.handleUpload)
	mux.HandleFunc("POST /api/undo", s.handleUndo)
	mux.HandleFunc("GET /media", s.handleMedia)
	return mux
}

func (s *Server) Listen(addr string) error {
	fmt.Printf("Heydit UI on http://%s\n", addr)
	return http.ListenAndServe(addr, s.Handler())
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	page, _ := uiFS.ReadFile("ui/index.html")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(page)
}

type stateView struct {
	Model   string    `json:"model"`
	Ffmpeg  bool      `json:"ffmpeg"`
	Assets  []fileRef `json:"assets"`
	Ops     []opView  `json:"ops"`
	Current string    `json:"current,omitempty"` // media URL
	Cost    string    `json:"cost,omitempty"`
}

type fileRef struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	URL  string `json:"url"`
	Info string `json:"info"`
}

type opView struct {
	Seq     int    `json:"seq"`
	Tool    string `json:"tool"`
	Summary string `json:"summary"`
	URL     string `json:"url,omitempty"`
}

func mediaURL(path string) string {
	if path == "" {
		return ""
	}
	return "/media?path=" + path
}

func (s *Server) state() stateView {
	p := s.Session.Project
	v := stateView{
		Model:   s.Session.Cfg.Model.Provider + "/" + s.Session.Cfg.Model.ID,
		Ffmpeg:  media.Available(),
		Current: mediaURL(p.Current),
		Assets:  []fileRef{},
		Ops:     []opView{},
	}
	if s.Session.Agent != nil {
		v.Cost = s.Session.Agent.CostLine()
	}
	for _, a := range p.Assets {
		v.Assets = append(v.Assets, fileRef{ID: a.ID, Name: filepath.Base(a.Path), URL: mediaURL(a.Path), Info: a.Info.Compact()})
	}
	for _, op := range p.Ops {
		v.Ops = append(v.Ops, opView{Seq: op.Seq, Tool: op.Tool, Summary: op.Summary, URL: mediaURL(op.Output)})
	}
	return v
}

func (s *Server) handleState(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, s.state())
}

func (s *Server) handleChat(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Message string `json:"message"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Message == "" {
		httpErr(w, 400, "message required")
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	var events []agent.Event
	reply, err := runAsk(r.Context(), s.Session, req.Message, func(e agent.Event) {
		if e.Kind == "text_delta" { // per-token noise; the reply carries the text
			return
		}
		events = append(events, e)
	})
	if err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	_ = s.Session.Agent.SaveSession()
	writeJSON(w, map[string]any{"reply": reply, "events": events, "state": s.state()})
}

func runAsk(ctx context.Context, session *cli.Session, msg string, onEvent func(agent.Event)) (string, error) {
	if session.Agent == nil {
		return "", fmt.Errorf("no usable model configured — set an API key and restart, or switch models via CLI /model")
	}
	return session.Agent.Run(ctx, msg, onEvent)
}

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

	dir := filepath.Join(s.Session.Project.Dir, ".heydit", "uploads")
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

func (s *Server) handleUndo(w http.ResponseWriter, _ *http.Request) {
	if _, err := s.Session.Project.Undo(); err != nil {
		httpErr(w, 400, err.Error())
		return
	}
	writeJSON(w, s.state())
}

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
