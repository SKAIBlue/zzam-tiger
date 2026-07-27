package tui

import "testing"

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
