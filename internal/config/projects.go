package config

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// Recent projects: every project the UI has opened, newest first, so the
// project switcher can offer them. A project is just a directory — its
// .itan/ subfolder carries the ledger, conversation, and checkpoints, which
// is what makes each project its own resumable session.

const maxRecentProjects = 12

func recentProjectsPath() string { return filepath.Join(GlobalDir(), "recent-projects.json") }

// RecentProjects returns remembered project dirs that still exist.
func RecentProjects() []string {
	var dirs []string
	if data, err := os.ReadFile(recentProjectsPath()); err == nil {
		_ = json.Unmarshal(data, &dirs)
	}
	out := dirs[:0]
	for _, d := range dirs {
		if st, err := os.Stat(d); err == nil && st.IsDir() {
			out = append(out, d)
		}
	}
	return out
}

// RememberProject moves dir to the front of the recent list.
func RememberProject(dir string) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return
	}
	dirs := []string{abs}
	for _, d := range RecentProjects() {
		if d != abs {
			dirs = append(dirs, d)
		}
	}
	if len(dirs) > maxRecentProjects {
		dirs = dirs[:maxRecentProjects]
	}
	if err := os.MkdirAll(GlobalDir(), 0o755); err != nil {
		return
	}
	data, _ := json.MarshalIndent(dirs, "", "  ")
	_ = os.WriteFile(recentProjectsPath(), data, 0o644)
}
