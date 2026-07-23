package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/SKAIBlue/zzam-tiger/internal/provider"
	"github.com/SKAIBlue/zzam-tiger/internal/worktree"
)

func TestCodeHighlighterDetectsPathsAndFallsBack(t *testing.T) {
	for _, path := range []string{"main.go", "Dockerfile"} {
		highlighter := newCodeHighlighter(path)
		if !highlighter.available() {
			t.Fatalf("expected lexer for %q", path)
		}
	}
	if highlighter := newCodeHighlighter("notes.unknown-format"); highlighter.available() {
		t.Fatal("unknown extension unexpectedly selected a lexer")
	}
}

func TestWorkspaceCodePreviewHighlightsAndKeepsVisibleWidth(t *testing.T) {
	file := worktree.File{Path: "main.go", Data: []byte("package main\n\nfunc main() { println(\"안녕\") }\n")}
	rendered := renderWorkspaceFile(file, 24, 10)
	if !strings.Contains(rendered, "\x1b[38;2;") {
		t.Fatalf("recognized source preview has no token colors: %q", rendered)
	}
	if !strings.Contains(ansi.Strip(rendered), "package main") {
		t.Fatalf("highlighting changed source text: %q", rendered)
	}
	for _, line := range strings.Split(rendered, "\n") {
		if width := lipgloss.Width(line); width > 24 {
			t.Fatalf("highlighted preview width = %d, want <= 24: %q", width, line)
		}
	}
}

func TestUnknownWorkspacePreviewRemainsPlain(t *testing.T) {
	highlighter := newCodeHighlighter("notes.unknown-format")
	const content = "plain text"
	if got := highlighter.line(content); got != content {
		t.Fatalf("plain fallback = %q, want %q", got, content)
	}
}

func TestHighlightedDiffsPreserveMarkersSelectionAndSanitization(t *testing.T) {
	workspace := renderWorkspaceDiff(worktree.Diff{
		Path:  "main.go",
		Patch: "--- a/main.go\n+++ b/main.go\n@@ -1 +1 @@\n-package old\n+package new\x1b[2J\n",
	}, 60)
	plain := ansi.Strip(workspace)
	if !strings.Contains(plain, "--- a/main.go") || !strings.Contains(plain, "+++ b/main.go") || !strings.Contains(plain, "-package old") || !strings.Contains(plain, "+package new[2J") {
		t.Fatalf("highlighted unified diff lost syntax, markers, or sanitized text: %q", workspace)
	}
	if !strings.Contains(workspace, "\x1b[48;2;72;43;49m") || !strings.Contains(workspace, "\x1b[48;2;32;60;47m") || !strings.Contains(workspace, "\x1b[38;2;") {
		t.Fatalf("changed lines did not combine diff backgrounds with syntax foregrounds: %q", workspace)
	}

	remote := renderDiffFileState([]provider.DiffFile{{
		NewPath: "main.go",
		Lines:   []provider.DiffLine{{NewLine: 1, Text: "+package main\x1b[2J"}},
	}}, 0, 0, -1, -1, 120)
	if strings.Contains(remote, "\x1b[2J") || !strings.Contains(ansi.Strip(remote), "+ package main[2J") {
		t.Fatalf("remote diff did not preserve sanitized selected content: %q", remote)
	}
	if !strings.Contains(remote, "\x1b[48;2;49;95;133m") || !strings.Contains(remote, "\x1b[38;2;") {
		t.Fatalf("selected code did not combine high-contrast background with syntax foregrounds: %q", remote)
	}
	for _, line := range strings.Split(remote, "\n") {
		if width := lipgloss.Width(line); width > 120 {
			t.Fatalf("highlighted split diff width = %d, want <= 120: %q", width, line)
		}
	}
}

func TestDiffBackgroundStopsAtTextAndLongTextCanWrap(t *testing.T) {
	short := renderUnifiedWorkspaceDiff(worktree.Diff{Path: "main.go", Patch: "+package main"}, 20)
	shortLine := strings.Split(short, "\n")[1]
	if width := lipgloss.Width(shortLine); width != len("+package main") {
		t.Fatalf("short background row width = %d, want text width: %q", width, shortLine)
	}

	longText := "+package main // this text is intentionally wider than the viewport"
	long := renderUnifiedWorkspaceDiff(worktree.Diff{Path: "main.go", Patch: longText}, 20)
	longLine := strings.Split(long, "\n")[1]
	if width := lipgloss.Width(longLine); width <= 20 {
		t.Fatalf("long code was truncated instead of being left to wrap: width=%d row=%q", width, longLine)
	}
}
