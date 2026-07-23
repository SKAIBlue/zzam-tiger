package tui

import (
	"context"
	"encoding/base64"
	"fmt"
	"image"
	"os/exec"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/SKAIBlue/zzam-tiger/internal/provider"
	"github.com/SKAIBlue/zzam-tiger/internal/worktree"
)

type fakeWorkspace struct {
	entries      []worktree.Entry
	entriesByDir map[string][]worktree.Entry
	entryDirs    []string
	files        map[string]worktree.File
	status       worktree.Status
	diffs        map[string]worktree.Diff
	staged       []string
	unstaged     []string
	stageAll     int
	unstageAll   int
	commits      []string
	history      []worktree.Commit
}

func (w *fakeWorkspace) Root() string { return "/repo" }
func (w *fakeWorkspace) Entries(_ context.Context, dir string) ([]worktree.Entry, error) {
	w.entryDirs = append(w.entryDirs, dir)
	if w.entriesByDir != nil {
		return w.entriesByDir[dir], nil
	}
	return w.entries, nil
}
func (w *fakeWorkspace) Read(_ context.Context, path string) (worktree.File, error) {
	return w.files[path], nil
}
func (w *fakeWorkspace) Status(context.Context) (worktree.Status, error) { return w.status, nil }
func (w *fakeWorkspace) Stage(_ context.Context, path string) error {
	w.staged = append(w.staged, path)
	return nil
}
func (w *fakeWorkspace) StageAll(context.Context) error { w.stageAll++; return nil }
func (w *fakeWorkspace) Unstage(_ context.Context, path string) error {
	w.unstaged = append(w.unstaged, path)
	return nil
}
func (w *fakeWorkspace) UnstageAll(context.Context) error { w.unstageAll++; return nil }
func (w *fakeWorkspace) Commit(_ context.Context, message string) error {
	w.commits = append(w.commits, message)
	return nil
}
func (w *fakeWorkspace) Diff(_ context.Context, path string, _ bool) (worktree.Diff, error) {
	return w.diffs[path], nil
}
func (w *fakeWorkspace) History(context.Context, int) ([]worktree.Commit, error) {
	return w.history, nil
}

func TestWorkspaceTabsLeadAndCommitsAreGraph(t *testing.T) {
	m := newWithWorkspace(fakeProvider{}, time.Second, &fakeWorkspace{})
	want := []string{"Commit", "Files", "Graph", "Branches", "PRs", "Issues", "Milestones", "CI Runs"}
	got := m.tabLabels()
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("tabs = %#v, want %#v", got, want)
	}
	if m.active != workspaceCommitTab || !m.localTab() {
		t.Fatalf("initial tab = %d, local=%v; want Commit", m.active, m.localTab())
	}
}

func TestWorkspaceThirdTabLoadsCommitGraph(t *testing.T) {
	m := newWithWorkspace(fakeProvider{}, 0, &fakeWorkspace{})
	m.active = 2
	if got := m.kind(); got != provider.Commits {
		t.Fatalf("third tab kind = %v, want Commits", got)
	}
}

func TestWorkspaceGraphLoadsLocalAndRemoteRefs(t *testing.T) {
	workspace := &fakeWorkspace{history: []worktree.Commit{{
		SHA: "abcdef123456", Subject: "merge feature", Author: "Ada", Parents: []string{"parent-a", "parent-b"},
		Refs: []worktree.Ref{{Name: "main", Head: true}, {Name: "origin/main", Remote: true}, {Name: "v0.0.3", Tag: true}},
	}}}
	m := newWithWorkspace(fakeProvider{}, 0, workspace)
	m.active = 2
	result := m.fetchListCmd(m.listRequest, provider.Commits, m.filter())().(listResultMsg)
	if result.err != nil || len(result.items) != 1 {
		t.Fatalf("graph result = %#v, err=%v", result.items, result.err)
	}
	item := result.items[0]
	if item.ID != "abcdef123456" || item.Meta != "abcdef1" || len(item.Parents) != 2 || len(item.Refs) != 3 {
		t.Fatalf("graph item = %#v", item)
	}
	if !item.Refs[0].Head || item.Refs[1].Name != "origin/main" || !item.Refs[1].Remote {
		t.Fatalf("graph refs = %#v", item.Refs)
	}
	if item.Refs[2].Name != "v0.0.3" || !item.Refs[2].Tag {
		t.Fatalf("graph tag ref = %#v", item.Refs[2])
	}
}

func TestGraphViewShowsForkMergeAndBranchTips(t *testing.T) {
	m := New(fakeProvider{}, 0)
	m.active = 4 // Graph in the remote-only tab order.
	m.width, m.height = 120, 12
	m.loadingList = false
	m.items[provider.Commits] = []provider.Item{
		{ID: "merge", Title: "merge feature", Parents: []string{"main", "feature"}, Refs: []provider.CommitRef{{Name: "main", Head: true}, {Name: "origin/main", Remote: true}, {Name: "v0.0.3", Tag: true}}},
		{ID: "main", Title: "main work", Parents: []string{"base"}},
		{ID: "feature", Title: "feature work", Parents: []string{"base"}, Refs: []provider.CommitRef{{Name: "feature"}}},
		{ID: "base", Title: "common base"},
	}
	view := ansi.Strip(m.View())
	for _, want := range []string{"●─┬", "● │", "│ ●", "[HEAD→main]", "[origin/main]", "[tag:v0.0.3]", "[feature]"} {
		if !strings.Contains(view, want) {
			t.Fatalf("graph view missing %q:\n%s", want, view)
		}
	}
}

func TestGraphViewStaysWithinNarrowTerminal(t *testing.T) {
	m := New(fakeProvider{}, 0)
	m.active = 4
	m.width, m.height = 24, 10
	m.loadingList = false
	m.items[provider.Commits] = []provider.Item{{
		ID: "tip", Title: "a deliberately long commit subject", Parents: []string{"base"},
		Refs: []provider.CommitRef{{Name: "feature/long-name", Head: true}, {Name: "origin/feature/long-name", Remote: true}},
	}, {ID: "base", Title: "base"}}
	for _, line := range strings.Split(m.View(), "\n") {
		if width := lipgloss.Width(line); width > m.width {
			t.Fatalf("narrow graph row width = %d, want <= %d: %q", width, m.width, ansi.Strip(line))
		}
	}
}

func TestTabBarKeepsActiveTabVisibleAtNarrowWidths(t *testing.T) {
	m := newWithWorkspace(fakeProvider{}, 0, &fakeWorkspace{})
	m.width = 60
	for _, active := range []int{0, 6, 7} {
		m.active = active
		bar := m.tabsView()
		if lipgloss.Width(bar) > m.width {
			t.Fatalf("active %d tab bar width = %d: %q", active, lipgloss.Width(bar), bar)
		}
		if !strings.Contains(ansi.Strip(bar), fmt.Sprintf(" %d %s ", active+1, m.tabLabels()[active])) {
			t.Fatalf("active %d missing from tab bar %q", active, bar)
		}
	}
}

func TestWorkspaceFilterKeepsParentDirectories(t *testing.T) {
	m := newWithWorkspace(fakeProvider{}, 0, &fakeWorkspace{})
	m.workspaceEntries = []worktree.Entry{
		{Path: "internal", Name: "internal", IsDir: true},
		{Path: "internal/tui", Name: "tui", IsDir: true},
		{Path: "internal/tui/model.go", Name: "model.go"},
		{Path: "README.md", Name: "README.md"},
	}
	m.workspaceExpanded["internal"] = true
	m.workspaceExpanded["internal/tui"] = true
	m.fileFilter.SetValue("model")
	entries := m.filteredWorkspaceEntries()
	if len(entries) != 3 || entries[0].Path != "internal" || entries[1].Path != "internal/tui" || entries[2].Path != "internal/tui/model.go" {
		t.Fatalf("filtered entries = %#v", entries)
	}
}

func TestWorkspaceFilesLoadDirectoriesLazily(t *testing.T) {
	workspace := &fakeWorkspace{entriesByDir: map[string][]worktree.Entry{
		"": {
			{Path: "docs", Name: "docs", IsDir: true},
			{Path: "z.txt", Name: "z.txt"},
		},
		"docs": {
			{Path: "docs/api", Name: "api", IsDir: true},
			{Path: "docs/guide.md", Name: "guide.md"},
		},
	}}
	m := newWithWorkspace(fakeProvider{}, 0, workspace)
	m.active = workspaceFilesTab

	updated, preview := m.Update(m.fetchWorkspaceCmd(m.workspaceEntryRequest)())
	m = updated.(Model)
	if preview != nil {
		t.Fatal("directory selection unexpectedly started a file preview")
	}
	if got := strings.Join(workspace.entryDirs, "|"); got != "" {
		t.Fatalf("initial directory requests = %q, want root only", got)
	}
	if entries := m.visibleWorkspaceEntries(); len(entries) != 2 {
		t.Fatalf("root entries = %#v", entries)
	}

	m.workspaceCursor = 0
	expanded, load := m.toggleWorkspaceDirectory()
	m = expanded
	if load == nil {
		t.Fatal("expanding an unloaded directory did not return an async command")
	}
	if len(workspace.entryDirs) != 1 {
		t.Fatalf("directory was read before the command ran: %#v", workspace.entryDirs)
	}
	updatedModel, _ := m.Update(load())
	m = updatedModel.(Model)
	if got := strings.Join(workspace.entryDirs, "|"); got != "|docs" {
		t.Fatalf("directory requests = %q, want root then docs", got)
	}
	if entries := m.visibleWorkspaceEntries(); len(entries) != 4 || entries[1].Path != "docs/api" || entries[2].Path != "docs/guide.md" {
		t.Fatalf("expanded entries = %#v", entries)
	}

	m.workspaceCursor = 0
	expanded, load = m.toggleWorkspaceDirectory()
	m = expanded
	if load != nil || len(m.visibleWorkspaceEntries()) != 2 {
		t.Fatalf("collapse reloaded the directory or left children visible: cmd=%v entries=%#v", load != nil, m.visibleWorkspaceEntries())
	}
}

func TestWorkspaceStalePreviewDoesNotReplaceDirectorySelection(t *testing.T) {
	workspace := &fakeWorkspace{
		entriesByDir: map[string][]worktree.Entry{
			"": {
				{Path: "a.txt", Name: "a.txt"},
				{Path: "z-dir", Name: "z-dir", IsDir: true},
			},
		},
		files: map[string]worktree.File{"a.txt": {Path: "a.txt", Data: []byte("old preview")}},
	}
	m := newWithWorkspace(fakeProvider{}, 0, workspace)
	m.active = workspaceFilesTab
	updated, preview := m.Update(m.fetchWorkspaceCmd(m.workspaceEntryRequest)())
	m = updated.(Model)
	if preview == nil {
		t.Fatal("initial file did not start a preview command")
	}

	m.workspaceCursor = 1
	m, _ = m.loadSelectedWorkspaceItem()
	updated, _ = m.Update(preview())
	m = updated.(Model)
	if m.workspaceFile.Path != "" || m.workspacePreviewLoading {
		t.Fatalf("stale preview replaced directory selection: file=%#v loading=%t", m.workspaceFile, m.workspacePreviewLoading)
	}
}

func TestWorkspaceDirectoryResultPreservesSelectionByPath(t *testing.T) {
	workspace := &fakeWorkspace{
		entriesByDir: map[string][]worktree.Entry{
			"": {
				{Path: "docs", Name: "docs", IsDir: true},
				{Path: "z.txt", Name: "z.txt"},
			},
			"docs": {{Path: "docs/guide.md", Name: "guide.md"}},
		},
		files: map[string]worktree.File{"z.txt": {Path: "z.txt", Data: []byte("selected")}},
	}
	m := newWithWorkspace(fakeProvider{}, 0, workspace)
	m.active = workspaceFilesTab
	updated, _ := m.Update(m.fetchWorkspaceCmd(m.workspaceEntryRequest)())
	m = updated.(Model)

	m.workspaceCursor = 0
	m, loadDirectory := m.toggleWorkspaceDirectory()
	if loadDirectory == nil {
		t.Fatal("directory expansion did not start")
	}
	m, _ = m.moveWorkspaceCursor(1)
	updated, _ = m.Update(loadDirectory())
	m = updated.(Model)
	entries := m.filteredWorkspaceEntries()
	if m.workspaceCursor >= len(entries) || entries[m.workspaceCursor].Path != "z.txt" {
		t.Fatalf("async expansion moved selection: cursor=%d entries=%#v", m.workspaceCursor, entries)
	}
}

func TestWorkspaceRefreshReadsOnlyRootAndExpandedDirectories(t *testing.T) {
	workspace := &fakeWorkspace{entriesByDir: map[string][]worktree.Entry{
		"":     {{Path: "docs", Name: "docs", IsDir: true}},
		"docs": {{Path: "docs/guide.md", Name: "guide.md"}},
	}}
	m := newWithWorkspace(fakeProvider{}, 0, workspace)
	m.active = workspaceFilesTab
	m.workspaceLoading = false
	m.workspaceExpanded["docs"] = true
	m.workspaceLoaded["docs"] = true
	m.workspaceEntries = append(m.workspaceEntries, workspace.entriesByDir[""]...)
	m.workspaceEntries = append(m.workspaceEntries, workspace.entriesByDir["docs"]...)

	updated, refresh := m.startWorkspaceLoad()
	m = updated
	message := refresh()
	batch, ok := message.(tea.BatchMsg)
	if !ok || len(batch) != 2 {
		t.Fatalf("refresh command = %T len=%d, want two-directory batch", message, len(batch))
	}
	for _, command := range batch {
		updatedModel, _ := m.Update(command())
		m = updatedModel.(Model)
	}
	if got := strings.Join(workspace.entryDirs, "|"); got != "|docs" {
		t.Fatalf("refresh directories = %q, want root and expanded docs", got)
	}
	if m.workspaceLoading || m.workspaceEntryPending != 0 {
		t.Fatalf("refresh stayed pending: loading=%t pending=%d", m.workspaceLoading, m.workspaceEntryPending)
	}
}

func TestWorkspaceRefreshRejectsLateChildrenOfRemovedDirectory(t *testing.T) {
	m := newWithWorkspace(fakeProvider{}, 0, &fakeWorkspace{})
	m.active = workspaceFilesTab
	m.workspaceEntries = []worktree.Entry{
		{Path: "docs", Name: "docs", IsDir: true},
		{Path: "docs/old.md", Name: "old.md"},
	}
	m.workspaceExpanded["docs"] = true
	m.workspaceLoaded["docs"] = true
	m.workspaceEntryRequest = 2
	m.workspaceEntryPending = 2
	m.workspaceLoading = true

	updated, _ := m.Update(workspaceResultMsg{request: 2, op: "entries", dir: "", entries: nil})
	m = updated.(Model)
	updated, _ = m.Update(workspaceResultMsg{
		request: 2,
		op:      "entries",
		dir:     "docs",
		entries: []worktree.Entry{{Path: "docs/late.md", Name: "late.md"}},
	})
	m = updated.(Model)
	if len(m.workspaceEntries) != 0 || m.workspaceLoaded["docs"] || m.workspaceExpanded["docs"] {
		t.Fatalf("late child result revived removed directory: entries=%#v loaded=%t expanded=%t", m.workspaceEntries, m.workspaceLoaded["docs"], m.workspaceExpanded["docs"])
	}
	if m.workspaceLoading || m.workspaceEntryPending != 0 {
		t.Fatalf("rejected child stayed pending: loading=%t pending=%d", m.workspaceLoading, m.workspaceEntryPending)
	}
}

func TestWorkspaceResizeDoesNotCancelPendingPreview(t *testing.T) {
	m := newWithWorkspace(fakeProvider{}, 0, &fakeWorkspace{})
	m.width, m.height = 100, 20
	m.workspaceFile = worktree.File{Path: "old.png", Image: true}
	m.workspacePreviewLoading = true
	m.workspacePreviewRequest = 9

	updated, command := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = updated.(Model)
	if command != nil || m.workspacePreviewRequest != 9 {
		t.Fatalf("resize invalidated pending preview: command=%v request=%d", command != nil, m.workspacePreviewRequest)
	}
}

func TestWorkspaceCommitOrderMatchesRenderedGroups(t *testing.T) {
	m := newWithWorkspace(fakeProvider{}, 0, &fakeWorkspace{})
	m.active = workspaceCommitTab
	m.height = 20
	m.workspaceStatus = worktree.Status{
		Staged:    []worktree.Change{{Path: "z.go", Code: 'M'}, {Path: "a.go", Code: 'A'}},
		Unstaged:  []worktree.Change{{Path: "d.go", Code: 'M'}},
		Untracked: []worktree.Change{{Path: "b.go", Code: '?'}},
	}
	changes := m.filteredWorkspaceChanges()
	want := []string{"a.go", "z.go", "b.go", "d.go"}
	for index, path := range want {
		if changes[index].change.Path != path {
			t.Fatalf("change %d = %q, want %q", index, changes[index].change.Path, path)
		}
	}
	rows := m.workspaceChangeRows()
	var rendered []string
	for _, row := range rows {
		if row.index >= 0 {
			rendered = append(rendered, row.item.change.Path)
		}
	}
	if strings.Join(rendered, "|") != strings.Join(want, "|") {
		t.Fatalf("rendered order = %#v, want %#v", rendered, want)
	}
}

func TestWorkspaceCommitMouseSkipsGroupHeaders(t *testing.T) {
	workspace := &fakeWorkspace{diffs: map[string]worktree.Diff{
		"a.go": {Path: "a.go"}, "b.go": {Path: "b.go"},
	}}
	m := newWithWorkspace(fakeProvider{}, 0, workspace)
	m.active, m.width, m.height = workspaceCommitTab, 120, 20
	m.workspaceStatus = worktree.Status{
		Staged:   []worktree.Change{{Path: "a.go", Code: 'M'}},
		Unstaged: []worktree.Change{{Path: "b.go", Code: 'M'}},
	}
	m.workspaceLoading = false

	updated, cmd := m.Update(tea.MouseMsg{X: 2, Y: 7, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
	m = updated.(Model)
	if cmd != nil || m.workspaceCursor != 0 { // row 2 is the Changes group header
		t.Fatalf("group header selected cursor %d with cmd=%v", m.workspaceCursor, cmd != nil)
	}
	updated, cmd = m.Update(tea.MouseMsg{X: 2, Y: 8, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
	m = updated.(Model)
	if cmd == nil || m.workspaceCursor != 1 {
		t.Fatalf("change click selected cursor %d with cmd=%v", m.workspaceCursor, cmd != nil)
	}
}

func TestWorkspaceCommitMessageSubmitsWithEnter(t *testing.T) {
	workspace := &fakeWorkspace{}
	m := newWithWorkspace(fakeProvider{}, 0, workspace)
	m.active, m.width, m.height = workspaceCommitTab, 120, 20
	m.workspaceStatus.Staged = []worktree.Change{{Path: "main.go", Code: 'M'}}
	m.commitMessage.SetValue("Describe the change")
	m.commitMessage.Focus()

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)
	if cmd == nil || !m.actionBusy {
		t.Fatalf("Enter did not start commit: command=%v busy=%t", cmd != nil, m.actionBusy)
	}
	updated, refresh := m.Update(cmd())
	m = updated.(Model)
	if len(workspace.commits) != 1 || workspace.commits[0] != "Describe the change" {
		t.Fatalf("commits = %#v", workspace.commits)
	}
	if m.commitMessage.Value() != "" || m.commitMessage.Focused() || m.actionBusy || refresh == nil {
		t.Fatalf("successful commit state: value=%q focused=%t busy=%t refresh=%v", m.commitMessage.Value(), m.commitMessage.Focused(), m.actionBusy, refresh != nil)
	}
}

func TestWorkspaceCommitViewShowsMessageAndButton(t *testing.T) {
	m := newWithWorkspace(fakeProvider{}, 0, &fakeWorkspace{})
	m.active, m.width, m.height = workspaceCommitTab, 120, 20
	m.workspaceLoading = false
	view := ansi.Strip(m.View())
	if !strings.Contains(view, "Commit message") || !strings.Contains(view, "Commit") {
		t.Fatalf("commit composer missing from view: %q", view)
	}
}

func TestWorkspaceCommitButtonSubmitsMessage(t *testing.T) {
	workspace := &fakeWorkspace{}
	m := newWithWorkspace(fakeProvider{}, 0, workspace)
	m.active, m.width, m.height = workspaceCommitTab, 120, 20
	m.workspaceStatus.Staged = []worktree.Change{{Path: "main.go", Code: 'M'}}
	m.commitMessage.SetValue("Commit from button")
	buttonWidth := lipgloss.Width(commitButtonStyle.Render("Commit"))

	updated, cmd := m.Update(tea.MouseMsg{X: m.width - buttonWidth - 1, Y: 4, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
	m = updated.(Model)
	if cmd == nil || !m.actionBusy {
		t.Fatalf("Commit button did not start commit: command=%v busy=%t", cmd != nil, m.actionBusy)
	}
	m.Update(cmd())
	if len(workspace.commits) != 1 || workspace.commits[0] != "Commit from button" {
		t.Fatalf("commits = %#v", workspace.commits)
	}
}

func TestWorkspaceCommitRequiresMessageAndStagedChanges(t *testing.T) {
	m := newWithWorkspace(fakeProvider{}, 0, &fakeWorkspace{})
	m.active = workspaceCommitTab
	m.commitMessage.Focus()

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)
	if m.actionBusy || len(m.workspace.(*fakeWorkspace).commits) != 0 || m.status != "enter a commit message" {
		t.Fatalf("empty message: busy=%t status=%q", m.actionBusy, m.status)
	}
	m.commitMessage.SetValue("Nothing staged")
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)
	if m.actionBusy || len(m.workspace.(*fakeWorkspace).commits) != 0 || m.status != "stage changes before committing" {
		t.Fatalf("unstaged commit: busy=%t status=%q", m.actionBusy, m.status)
	}
}

func TestAlignDiffLinesKeepsInsertionAligned(t *testing.T) {
	pairs := alignDiffLines([]string{"a", "b", "c"}, []string{"a", "inserted", "b", "c"})
	want := []alignedDiffLine{
		{old: "a", new: "a", hasOld: true, hasNew: true},
		{new: "inserted", hasNew: true},
		{old: "b", new: "b", hasOld: true, hasNew: true},
		{old: "c", new: "c", hasOld: true, hasNew: true},
	}
	if len(pairs) != len(want) {
		t.Fatalf("pairs = %#v", pairs)
	}
	for index := range want {
		if pairs[index] != want[index] {
			t.Fatalf("pair %d = %#v, want %#v", index, pairs[index], want[index])
		}
	}
}

func TestAlignDiffLinesRendersReplacementAsRemovalThenAddition(t *testing.T) {
	pairs := alignDiffLines([]string{"public value"}, []string{"private value"})
	want := []alignedDiffLine{{old: "public value", hasOld: true}, {new: "private value", hasNew: true}}
	if len(pairs) != len(want) || pairs[0] != want[0] || pairs[1] != want[1] {
		t.Fatalf("replacement pairs = %#v, want %#v", pairs, want)
	}
}

func TestWorkspaceSideBySideDiffDistinguishesBlankLinesFromGaps(t *testing.T) {
	diff := worktree.Diff{Path: "main.go", Old: []byte("value\n"), New: []byte("value\n\n")}
	rendered := renderWorkspaceDiff(diff, 100)
	plain := ansi.Strip(rendered)
	if !strings.Contains(plain, "+ ") {
		t.Fatalf("added blank line has no plus marker: %q", plain)
	}
	column := (100 - 3) / 2
	if !strings.Contains(rendered, diffGapStyle.Render(strings.Repeat(" ", column))) {
		t.Fatalf("added blank line has no shaded old-side gap: %q", rendered)
	}
}

func TestWorkspaceSideBySideDiffUsesSeparateSignedRowsAndShadedGaps(t *testing.T) {
	diff := worktree.Diff{Path: "main.go", Old: []byte("public value"), New: []byte("private value")}
	rendered := renderWorkspaceDiff(diff, 100)
	plain := strings.Split(ansi.Strip(rendered), "\n")
	var removal, addition int = -1, -1
	for index, line := range plain {
		if strings.Contains(line, "- public value") {
			removal = index
		}
		if strings.Contains(line, "+ private value") {
			addition = index
		}
	}
	if removal < 0 || addition != removal+1 {
		t.Fatalf("replacement rows were not split removal-first: %q", ansi.Strip(rendered))
	}
	column := (100 - 3) / 2
	gap := diffGapStyle.Render(strings.Repeat(" ", column))
	if strings.Count(rendered, gap) != 2 {
		t.Fatalf("side-by-side replacement has %d shaded gaps, want 2: %q", strings.Count(rendered, gap), rendered)
	}
}

func TestWorkspaceDiffSwitchesLayoutAtWidthBoundary(t *testing.T) {
	diff := worktree.Diff{
		Path:  "main.go",
		Old:   []byte("old\n"),
		New:   []byte("new\n"),
		Patch: "@@ -1 +1 @@\n-old\n+new\n",
	}
	if rendered := renderWorkspaceDiff(diff, 99); !strings.Contains(rendered, "unified") || strings.Contains(rendered, "side by side") {
		t.Fatalf("narrow layout = %q", rendered)
	}
	if rendered := renderWorkspaceDiff(diff, 100); !strings.Contains(rendered, "side by side") || strings.Contains(rendered, "unified") {
		t.Fatalf("wide layout = %q", rendered)
	}
}

func TestWorkspaceDiffFallsBackForLargeOneSidedChange(t *testing.T) {
	diff := worktree.Diff{
		Path:  "generated.txt",
		New:   []byte(strings.Repeat("line\n", 5_001)),
		Patch: "@@ -0,0 +1 @@\n+large change\n",
	}
	rendered := renderWorkspaceDiff(diff, 120)
	if !strings.Contains(rendered, "unified") || strings.Contains(rendered, "side by side") {
		t.Fatalf("large one-sided diff did not use bounded layout: %q", ansi.Strip(rendered))
	}
}

func TestWorkspacePreviewCanScrollWithoutChangingSelection(t *testing.T) {
	m := newWithWorkspace(fakeProvider{}, 0, &fakeWorkspace{})
	m.active = workspaceFilesTab
	m.width, m.height = 120, 12
	m.workspaceEntries = []worktree.Entry{{Path: "long.txt", Name: "long.txt"}}
	m.workspaceFile = worktree.File{Path: "long.txt", Data: []byte("one\ntwo\nthree\nfour\nfive\nsix\nseven\neight\nnine\nten\n")}

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyPgDown})
	m = updated.(Model)
	if m.workspaceCursor != 0 || m.workspacePreviewOffset == 0 {
		t.Fatalf("preview scroll changed cursor or did not move: cursor=%d offset=%d", m.workspaceCursor, m.workspacePreviewOffset)
	}
	preview := renderWorkspaceFileAt(m.workspaceFile, 60, m.workspaceListHeight(), m.workspacePreviewOffset)
	if strings.Contains(preview, "\none\n") || !strings.Contains(preview, "five") {
		t.Fatalf("scrolled preview = %q", preview)
	}
}

func TestWorkspacePaneWidthsStayWithinTerminal(t *testing.T) {
	for _, width := range []int{12, 30, 63, 120} {
		left, right := workspacePaneWidths(width)
		if left < 1 || right < 1 || left+3+right > width {
			t.Fatalf("width %d => left=%d right=%d", width, left, right)
		}
	}
}

func TestWorkspacePreviewStripsTerminalControlSequences(t *testing.T) {
	t.Setenv("KITTY_WINDOW_ID", "")
	t.Setenv("TERM", "xterm-256color")
	t.Setenv("TMUX", "")
	file := worktree.File{Path: "unsafe\x1b[31m\nname.txt", Data: []byte("safe\x1b[2J\nnext\tcolumn")}
	rendered := renderWorkspaceFile(file, 80, 10)
	if strings.Contains(rendered, "\x1b[2J") || strings.Contains(rendered, "\x1b[31m.txt") {
		t.Fatalf("preview leaked terminal control sequence: %q", rendered)
	}
	if !strings.Contains(rendered, "safe[2J") || !strings.Contains(rendered, "next\tcolumn") {
		t.Fatalf("preview lost safe content: %q", rendered)
	}
	if strings.Contains(rendered, "\nname.txt") {
		t.Fatalf("preview allowed a path to inject a row: %q", rendered)
	}

	diff := worktree.Diff{Path: "unsafe\x1b[31m.txt", Patch: "@@ -1 +1 @@\n-old\x1b[2J\n+new\n"}
	rendered = renderWorkspaceDiff(diff, 80)
	if strings.Contains(rendered, "\x1b[2J") || strings.Contains(rendered, "\x1b[31m.txt") {
		t.Fatalf("diff leaked terminal control sequence: %q", rendered)
	}

	m := newWithWorkspace(fakeProvider{}, 0, &fakeWorkspace{})
	m.width = 80
	m.err = fmt.Errorf("read unsafe\x1b]52;c;payload\a\npath")
	rendered = m.statusLine()
	if strings.Contains(rendered, "\x1b]52") || strings.Contains(rendered, "\n") {
		t.Fatalf("status leaked terminal control sequence: %q", rendered)
	}
}

func TestWorkspaceMarkdownPreviewUsesRichRenderer(t *testing.T) {
	t.Setenv("KITTY_WINDOW_ID", "")
	file := worktree.File{Path: "README.md", MIME: "text/markdown", Data: []byte("# Guide\n\n[Docs](https://example.com)\n\n```go\nfmt.Println(\"hi\")\n```\n\n```mermaid\ngraph TD; A-->B\n```\n\n![Logo](logo.png)\n")}
	rendered := renderWorkspaceFile(file, 80, 30)
	plain := ansi.Strip(rendered)
	for _, want := range []string{"Guide", "Docs", "https://example.com", "fmt.Println", "graph TD", "Logo", "logo.png"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("Markdown preview missing %q: %q", want, plain)
		}
	}
}

func TestMarkdownPreviewLoadsFirstLocalImage(t *testing.T) {
	workspace := &fakeWorkspace{files: map[string]worktree.File{
		"docs/guide.md":        {Path: "docs/guide.md", Data: []byte("![Diagram](images/flow.png)\n")},
		"docs/images/flow.png": {Path: "docs/images/flow.png", Image: true, Binary: true, MIME: "image/png"},
	}}
	m := newWithWorkspace(fakeProvider{}, 0, workspace)
	m.width, m.height = 120, 30
	result := m.fetchWorkspaceFileCmd(1, "docs/guide.md")().(workspaceResultMsg)
	if result.err != nil || result.file.Path != "docs/guide.md" {
		t.Fatalf("Markdown preview result = %#v, err=%v", result.file, result.err)
	}
	if got := firstLocalMarkdownImage(result.file.Path, result.file.Data); got != "docs/images/flow.png" {
		t.Fatalf("resolved Markdown image = %q", got)
	}
}

func TestKittyImageKeepsNaturalSizeWhenItFitsPreviewPane(t *testing.T) {
	t.Setenv("KITTY_WINDOW_ID", "1")
	t.Setenv("TERM", "xterm-kitty")
	t.Setenv("TMUX", "")
	data, err := base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII=")
	if err != nil {
		t.Fatal(err)
	}
	rendered, ok := kittyImage(data, 40, 12)
	if !ok {
		t.Fatal("Kitty PNG was not rendered")
	}
	for _, want := range []string{"a=d,d=i,i=31,q=2", "a=T,f=100,q=2,i=31,C=1,m="} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("Kitty output missing %q: %q", want, rendered)
		}
	}
	if strings.Contains(rendered, ",c=") || strings.Contains(rendered, ",r=") {
		t.Fatalf("fitting image was resized: %q", rendered)
	}
}

func TestKittyImagePlacementContainsOnlyOversizedImages(t *testing.T) {
	for _, test := range []struct {
		name   string
		config image.Config
		want   string
	}{
		{name: "natural size", config: image.Config{Width: 100, Height: 100}, want: ""},
		{name: "width constrained", config: image.Config{Width: 800, Height: 100}, want: ",c=40"},
		{name: "height constrained", config: image.Config{Width: 100, Height: 800}, want: ",r=12"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := kittyImagePlacement(test.config, 40, 12, 10, 20); got != test.want {
				t.Fatalf("kittyImagePlacement() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestWorkspaceImageEncodingRunsInAsyncPreviewCommand(t *testing.T) {
	t.Setenv("KITTY_WINDOW_ID", "1")
	t.Setenv("TERM", "xterm-kitty")
	t.Setenv("TMUX", "")
	data, err := base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII=")
	if err != nil {
		t.Fatal(err)
	}
	workspace := &fakeWorkspace{files: map[string]worktree.File{
		"icon.png": {Path: "icon.png", Data: data, Image: true, Binary: true, MIME: "image/png"},
	}}
	m := newWithWorkspace(fakeProvider{}, 0, workspace)
	m.width, m.height = 120, 20
	result := m.fetchWorkspaceFileCmd(m.workspacePreviewRequest, "icon.png")().(workspaceResultMsg)
	if result.image == "" || !strings.Contains(result.image, "a=T,f=100") {
		t.Fatalf("async preview command did not prepare Kitty image: %q", result.image)
	}
	view := renderWorkspaceFileWithImageAt(result.file, result.image, result.width, result.height+1, 0)
	if !strings.Contains(view, result.image) {
		t.Fatal("workspace view did not use the cached image payload")
	}
}

func TestKittyImageRasterizesSVGWhenConverterIsAvailable(t *testing.T) {
	if _, err := exec.LookPath("rsvg-convert"); err != nil {
		if _, magickErr := exec.LookPath("magick"); magickErr != nil {
			t.Skip("no SVG rasterizer installed")
		}
	}
	t.Setenv("KITTY_WINDOW_ID", "1")
	t.Setenv("TERM", "xterm-kitty")
	t.Setenv("TMUX", "")
	svg := []byte(`<svg xmlns="http://www.w3.org/2000/svg" width="16" height="16"><rect width="16" height="16" fill="red"/></svg>`)
	if rendered, ok := kittyImage(svg, 20, 8); !ok || !strings.Contains(rendered, "a=T,f=100") {
		t.Fatalf("SVG Kitty output = ok:%v %q", ok, rendered)
	}
}

func TestWorkspaceIgnoresRemoteResult(t *testing.T) {
	m := newWithWorkspace(fakeProvider{}, 0, &fakeWorkspace{})
	m.width, m.height = 100, 30
	updated, _ := m.Update(listResultMsg{request: m.listRequest, kind: m.kind(), filter: m.filter().Value})
	m = updated.(Model)
	if m.loadingList || len(m.items[m.kind()]) != 0 {
		t.Fatal("stale remote result changed local-tab state")
	}
}

func TestWorkspaceStageAndUnstageShortcuts(t *testing.T) {
	workspace := &fakeWorkspace{}
	for _, test := range []struct {
		name     string
		change   workspaceChange
		key      rune
		wantCall string
	}{
		{name: "stage", change: workspaceChange{change: worktree.Change{Path: "new.go", Code: '?'}, staged: false}, key: 's', wantCall: "stage"},
		{name: "unstage", change: workspaceChange{change: worktree.Change{Path: "ready.go", Code: 'M'}, staged: true}, key: 'u', wantCall: "unstage"},
	} {
		t.Run(test.name, func(t *testing.T) {
			workspace.staged, workspace.unstaged = nil, nil
			m := newWithWorkspace(fakeProvider{}, 0, workspace)
			m.active = workspaceCommitTab
			if test.change.staged {
				m.workspaceStatus.Staged = []worktree.Change{test.change.change}
			} else {
				m.workspaceStatus.Untracked = []worktree.Change{test.change.change}
			}
			m.workspaceLoading = false
			updated, cmd := m.Update(key(test.key))
			m = updated.(Model)
			if cmd == nil || !m.actionBusy {
				t.Fatal("shortcut did not start an action")
			}
			result := cmd()
			if test.wantCall == "stage" && (len(workspace.staged) != 1 || workspace.staged[0] != test.change.change.Path) {
				t.Fatalf("stage calls = %#v", workspace.staged)
			}
			if test.wantCall == "unstage" && (len(workspace.unstaged) != 1 || workspace.unstaged[0] != test.change.change.Path) {
				t.Fatalf("unstage calls = %#v", workspace.unstaged)
			}
			if result.(workspaceActionResultMsg).err != nil {
				t.Fatalf("action result = %#v", result)
			}
		})
	}
}

func TestWorkspaceStageAndUnstageAllShortcuts(t *testing.T) {
	workspace := &fakeWorkspace{}
	for _, test := range []struct {
		name string
		key  rune
	}{
		{name: "stage all", key: 'S'},
		{name: "unstage all", key: 'U'},
	} {
		t.Run(test.name, func(t *testing.T) {
			workspace.stageAll, workspace.unstageAll = 0, 0
			m := newWithWorkspace(fakeProvider{}, 0, workspace)
			m.active = workspaceCommitTab
			m.workspaceLoading = false
			updated, cmd := m.Update(key(test.key))
			m = updated.(Model)
			if cmd == nil || !m.actionBusy {
				t.Fatal("shortcut did not start an action")
			}
			result := cmd().(workspaceActionResultMsg)
			if test.key == 'S' && (workspace.stageAll != 1 || workspace.unstageAll != 0) {
				t.Fatalf("bulk calls = stage %d, unstage %d", workspace.stageAll, workspace.unstageAll)
			}
			if test.key == 'U' && (workspace.stageAll != 0 || workspace.unstageAll != 1) {
				t.Fatalf("bulk calls = stage %d, unstage %d", workspace.stageAll, workspace.unstageAll)
			}
			if result.err != nil {
				t.Fatalf("action result = %#v", result)
			}
		})
	}
}

func TestWorkspaceStageKeepsSamePathSelectedAfterRegrouping(t *testing.T) {
	workspace := &fakeWorkspace{status: worktree.Status{Unstaged: []worktree.Change{{Path: "z.go", Code: 'M'}}}}
	m := newWithWorkspace(fakeProvider{}, 0, workspace)
	m.active = workspaceCommitTab
	m.workspaceStatus = workspace.status
	m.workspaceLoading = false

	updated, action := m.Update(key('s'))
	m = updated.(Model)
	result := action()
	workspace.status = worktree.Status{Staged: []worktree.Change{{Path: "a.go", Code: 'A'}, {Path: "z.go", Code: 'M'}}}
	updated, refresh := m.Update(result)
	m = updated.(Model)
	if refresh == nil {
		t.Fatal("successful stage did not request status refresh")
	}
	updated, loadSelected := m.Update(refresh())
	m = updated.(Model)
	if got := m.filteredWorkspaceChanges()[m.workspaceCursor].change.Path; got != "z.go" {
		t.Fatalf("selected path after regrouping = %q", got)
	}
	if loadSelected == nil {
		t.Fatal("regrouping did not reload selected diff")
	}
}
