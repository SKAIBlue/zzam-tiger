package tui

import (
	"testing"

	"github.com/charmbracelet/lipgloss"
)

func TestWorkspaceFileIcon(t *testing.T) {
	tests := map[string]string{
		"main.go":            "",
		"Application.java":   "",
		"component.tsx":      "",
		"README.md":          "",
		"Dockerfile":         "",
		"docker-compose.yml": "",
		"go.mod":             "",
		"photo.png":          "",
		"unknown.custom":     fileIconDefault,
	}
	for name, want := range tests {
		if got := workspaceFileIcon(name); got != want {
			t.Errorf("workspaceFileIcon(%q) = %q, want %q", name, got, want)
		}
	}
}

func TestWorkspaceFileIconColor(t *testing.T) {
	tests := map[string]lipgloss.Color{
		".gitignore": lipgloss.Color("#F05032"),
		"main.go":    lipgloss.Color("#00ADD8"),
		"app.js":     lipgloss.Color("#F7DF1E"),
		"app.ts":     lipgloss.Color("#3178C6"),
		"README.md":  lipgloss.Color("#519ABA"),
	}
	for name, want := range tests {
		if got := workspaceFileIconSpec(name).color; got != want {
			t.Errorf("workspaceFileIconSpec(%q).color = %q, want %q", name, got, want)
		}
	}
}
