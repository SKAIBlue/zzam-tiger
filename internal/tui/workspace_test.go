package tui

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"image"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
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
	diffErrs     map[string]error
	staged       []string
	unstaged     []string
	stageAll     int
	unstageAll   int
	commits      []string
	history      []worktree.Commit
	branches     []worktree.Branch
	branchCalls  []string
	remote       worktree.RemoteState
	pulls        int
	pushes       int
}

type fakeWorkspaceWatcher struct {
	updates chan worktree.WatchUpdate
	closed  bool
}

func (w *fakeWorkspaceWatcher) Updates() <-chan worktree.WatchUpdate { return w.updates }
func (w *fakeWorkspaceWatcher) Close() error {
	if !w.closed {
		w.closed = true
		close(w.updates)
	}
	return nil
}

func TestWorkspaceWatchDebouncesAndSeparatesRemotePolling(t *testing.T) {
	m := newWithWorkspace(fakeProvider{}, time.Second, &fakeWorkspace{})
	m.workspaceLoading = false
	m.active = workspaceFilesTab

	updated, cmd := m.Update(workspaceWatchMsg{path: "/repo/file.txt"})
	m = updated.(Model)
	if cmd == nil || m.workspaceWatchGeneration != 1 || m.workspaceLoading {
		t.Fatalf("watch event state: cmd=%v generation=%d loading=%t", cmd != nil, m.workspaceWatchGeneration, m.workspaceLoading)
	}
	updated, _ = m.Update(workspaceWatchMsg{path: "/repo/file.txt"})
	m = updated.(Model)
	if m.workspaceWatchGeneration != 2 {
		t.Fatalf("second event generation = %d, want 2", m.workspaceWatchGeneration)
	}
	updated, stale := m.Update(workspaceDebounceMsg(1))
	m = updated.(Model)
	if stale != nil || m.workspaceLoading {
		t.Fatal("stale debounce started a workspace load")
	}
	updated, load := m.Update(workspaceDebounceMsg(2))
	m = updated.(Model)
	if load == nil || !m.workspaceLoading {
		t.Fatal("latest debounce did not start a workspace load")
	}

	m.workspaceLoading = false
	before := m.workspaceRequest
	updated, tick := m.Update(tickMsg(time.Now()))
	m = updated.(Model)
	if m.workspaceRequest != before || m.workspaceLoading || tick == nil {
		t.Fatalf("local polling tick changed workspace: request=%d before=%d loading=%t tick=%v", m.workspaceRequest, before, m.workspaceLoading, tick != nil)
	}

	m.active = 2
	m.loadingList = false
	updated, remote := m.Update(tickMsg(time.Now()))
	m = updated.(Model)
	if remote == nil || !m.loadingList {
		t.Fatal("remote tab did not retain periodic polling")
	}
}

func TestWorkspaceWatchCoalescesEventDuringLoad(t *testing.T) {
	m := newWithWorkspace(fakeProvider{}, 0, &fakeWorkspace{})
	m.active = workspaceCommitTab
	m.workspaceLoading = true
	m.workspaceRequest = 7

	updated, cmd := m.Update(workspaceDebounceMsg(m.workspaceWatchGeneration))
	m = updated.(Model)
	if cmd != nil || !m.workspaceWatchPending {
		t.Fatal("event during load was not marked pending")
	}
	updated, followup := m.Update(workspaceResultMsg{request: 7, op: "status"})
	m = updated.(Model)
	if followup == nil || !m.workspaceLoading || m.workspaceWatchPending || m.workspaceRequest != 8 {
		t.Fatalf("follow-up state: cmd=%v loading=%t pending=%t request=%d", followup != nil, m.workspaceLoading, m.workspaceWatchPending, m.workspaceRequest)
	}
}

func TestWorkspaceWatcherErrorsAndCloseRemainSafe(t *testing.T) {
	watcher := &fakeWorkspaceWatcher{updates: make(chan worktree.WatchUpdate, 1)}
	m := newWithWorkspace(fakeProvider{}, 0, &fakeWorkspace{})
	m.width = 120
	m.watcher = watcher
	updated, _ := m.Update(workspaceWatchMsg{err: context.DeadlineExceeded})
	m = updated.(Model)
	if m.workspaceWatcherErr == nil || !strings.Contains(m.statusLine(), "manual refresh") {
		t.Fatalf("watcher error not visible: %q", m.statusLine())
	}
	if err := m.Close(); err != nil || !watcher.closed {
		t.Fatalf("close state: err=%v closed=%t", err, watcher.closed)
	}
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
func (w *fakeWorkspace) RemoteState(context.Context) (worktree.RemoteState, error) {
	return w.remote, nil
}
func (w *fakeWorkspace) Pull(context.Context) error { w.pulls++; return nil }
func (w *fakeWorkspace) Push(context.Context) error { w.pushes++; return nil }
func (w *fakeWorkspace) Commit(_ context.Context, message string) error {
	w.commits = append(w.commits, message)
	return nil
}
func (w *fakeWorkspace) Diff(_ context.Context, path string, _ bool) (worktree.Diff, error) {
	if err := w.diffErrs[path]; err != nil {
		return worktree.Diff{}, err
	}
	return w.diffs[path], nil
}
func (w *fakeWorkspace) History(context.Context, int) ([]worktree.Commit, error) {
	return w.history, nil
}
func (*fakeWorkspace) CommitPaths(context.Context, string) ([]string, error) { return nil, nil }
func (w *fakeWorkspace) Branches(context.Context) ([]worktree.Branch, error) {
	return w.branches, nil
}
func (w *fakeWorkspace) CreateBranch(_ context.Context, name, start string) error {
	w.branchCalls = append(w.branchCalls, "create:"+name+":"+start)
	return nil
}
func (w *fakeWorkspace) CheckoutBranch(_ context.Context, name string) error {
	w.branchCalls = append(w.branchCalls, "checkout:"+name)
	return nil
}
func (w *fakeWorkspace) RenameBranch(_ context.Context, old, new string) error {
	w.branchCalls = append(w.branchCalls, "rename:"+old+":"+new)
	return nil
}
func (w *fakeWorkspace) DeleteBranch(_ context.Context, name string) error {
	w.branchCalls = append(w.branchCalls, "delete:"+name)
	return nil
}
func (w *fakeWorkspace) DeleteRemoteBranch(_ context.Context, remote, name string) error {
	w.branchCalls = append(w.branchCalls, "remote-delete:"+remote+":"+name)
	return nil
}

func TestBranchActionsRequireConfirmationAndRefresh(t *testing.T) {
	workspace := &fakeWorkspace{}
	m := newWithWorkspace(fakeProvider{}, 0, workspace)
	m.active = 3 // Branches tab in a workspace-enabled model.
	m.items[provider.Branches] = []provider.Item{{ID: "main", Title: "main", State: "local"}, {ID: "origin/topic", Title: "origin/topic", State: "remote"}}

	updated, cmd := m.Update(key('d'))
	m = updated.(Model)
	if cmd != nil || m.screen != branchScreen || len(workspace.branchCalls) != 0 {
		t.Fatal("delete ran before confirmation")
	}
	updated, cmd = m.Update(key('n'))
	m = updated.(Model)
	if cmd != nil || m.screen != listScreen || len(workspace.branchCalls) != 0 {
		t.Fatal("delete cancellation changed branches")
	}

	updated, _ = m.Update(key('d'))
	m = updated.(Model)
	updated, cmd = m.Update(key('y'))
	m = updated.(Model)
	if cmd == nil || !m.actionBusy {
		t.Fatal("confirmed deletion did not start")
	}
	updated, refresh := m.Update(cmd())
	m = updated.(Model)
	if refresh == nil || len(workspace.branchCalls) != 1 || workspace.branchCalls[0] != "delete:main" || !m.loadingList {
		t.Fatalf("local delete = %#v, refreshing=%t", workspace.branchCalls, m.loadingList)
	}

	m.loadingList = false
	m.cursor[provider.Branches] = 1
	updated, _ = m.Update(key('d'))
	m = updated.(Model)
	updated, cmd = m.Update(key('y'))
	m = updated.(Model)
	_ = cmd()
	if got := workspace.branchCalls[1]; got != "remote-delete:origin:topic" {
		t.Fatalf("remote deletion = %q", got)
	}
}

func TestRemoteBranchDeletionConfirmationShowsRemoteAndGitOperation(t *testing.T) {
	m := newWithWorkspace(fakeProvider{}, 0, &fakeWorkspace{})
	m.width, m.height = 100, 30
	m.active = 3
	m.screen = branchScreen
	m.branchAction = "delete"
	m.branchTarget = provider.Item{ID: "origin/topic", State: "remote"}
	view := m.View()
	for _, want := range []string{"Delete remote branch?", "Remote: origin", "Branch: topic", "git push origin --delete topic"} {
		if !strings.Contains(view, want) {
			t.Fatalf("confirmation missing %q:\n%s", want, view)
		}
	}
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

func TestUnavailableProviderShowsOnlyLocalGraphAndBranches(t *testing.T) {
	m := newWithWorkspace(nil, time.Second, &fakeWorkspace{branches: []worktree.Branch{{Name: "main", SHA: "abcdef012345", Head: true}}}).WithRemoteUnavailable(errors.New("gh missing"))
	want := []string{"Commit", "Files", "Graph", "Branches"}
	if got := m.tabLabels(); !reflect.DeepEqual(got, want) {
		t.Fatalf("unavailable-provider tabs = %#v, want %#v", got, want)
	}
	m.active = 3
	m.loadingList = false
	updated, cmd := m.startListLoad()
	m = updated
	if cmd == nil {
		t.Fatal("local Branches did not start a load")
	}
	result := cmd().(listResultMsg)
	if result.err != nil || len(result.items) != 1 || result.items[0].Title != "main" || result.items[0].Meta != "HEAD · abcdef0" {
		t.Fatalf("local Branches result = %#v", result)
	}
	m.items[provider.Branches] = result.items
	updatedModel, detail := m.openSelected()
	m = updatedModel.(Model)
	if detail != nil || m.screen != listScreen || !strings.Contains(m.status, "remote provider") {
		t.Fatalf("local Branches detail state: screen=%v cmd=%v status=%q", m.screen, detail != nil, m.status)
	}
}

func TestBranchesAlwaysUseLocalGitRefs(t *testing.T) {
	workspace := &fakeWorkspace{branches: []worktree.Branch{{Name: "main", Head: true}, {Name: "origin/main", Remote: true}}}
	m := newWithWorkspace(fakeProvider{}, 0, workspace)
	m.active = 3 // Branches
	updated, cmd := m.startListLoad()
	m = updated
	if cmd == nil {
		t.Fatal("Branches did not start a local Git load")
	}
	result, ok := cmd().(listResultMsg)
	if !ok {
		t.Fatalf("Branches command result = %T", cmd())
	}
	if len(result.items) != 2 || result.items[0].State != "local" || result.items[1].State != "remote" {
		t.Fatalf("branch items = %#v", result.items)
	}
}

func TestBranchFiltersScopeLocalAndRemoteRefs(t *testing.T) {
	workspace := &fakeWorkspace{branches: []worktree.Branch{{Name: "main"}, {Name: "origin/main", Remote: true}}}
	m := newWithWorkspace(fakeProvider{}, 0, workspace).WithRemoteUnavailable(errors.New("gh missing"))
	m.active = 3 // Branches when only local Git tabs are available.

	if got := m.filters(); len(got) != 3 || got[0].Value != "all" || got[1].Value != "local" || got[2].Value != "remote" {
		t.Fatalf("branch filters = %#v", got)
	}

	updated, cmd := m.changeFilter(1)
	m = updated.(Model)
	if cmd == nil || m.filter().Value != "local" {
		t.Fatalf("local filter change: cmd=%v filter=%#v", cmd != nil, m.filter())
	}
	result := cmd().(listResultMsg)
	if len(result.items) != 1 || result.items[0].ID != "main" || result.items[0].State != "local" {
		t.Fatalf("local branch items = %#v", result.items)
	}

	updated, cmd = m.changeFilter(1)
	m = updated.(Model)
	if cmd == nil || m.filter().Value != "remote" {
		t.Fatalf("remote filter change: cmd=%v filter=%#v", cmd != nil, m.filter())
	}
	result = cmd().(listResultMsg)
	if len(result.items) != 1 || result.items[0].ID != "origin/main" || result.items[0].State != "remote" {
		t.Fatalf("remote branch items = %#v", result.items)
	}
}

func TestUnavailableProviderRendersLocalGraphAndRemoteHeaderWarning(t *testing.T) {
	m := newWithWorkspace(nil, 0, &fakeWorkspace{}).WithRemoteUnavailable(errors.New("gh missing"))
	m.active = 2 // Graph after Commit and Files.
	m.width, m.height = 100, 20
	m.items[provider.Commits] = []provider.Item{{ID: "abcdef0", Title: "local change", State: "commit", Meta: "abcdef0"}}
	view := ansi.Strip(m.View())
	if !strings.Contains(view, "local change") {
		t.Fatalf("local Graph was hidden by remote error:\n%s", view)
	}
	if !strings.Contains(view, "remote unavailable: gh missing") {
		t.Fatalf("remote error was not visible in the header:\n%s", view)
	}
}

func TestFilesOnlyModeHasNoGitOrRemoteTabs(t *testing.T) {
	m := NewFilesOnly(worktree.NewFilesystem(t.TempDir()))
	defer m.Close()
	m.width = 100
	if !m.localTab() || !m.workspaceFilesActive() || m.workspaceCommitActive() {
		t.Fatalf("files-only state: local=%t files=%t commit=%t", m.localTab(), m.workspaceFilesActive(), m.workspaceCommitActive())
	}
	if got := m.tabLabels(); !reflect.DeepEqual(got, []string{"Files"}) {
		t.Fatalf("files-only tabs = %#v", got)
	}
	if m.tabCount() != 1 {
		t.Fatalf("files-only tab count = %d, want 1", m.tabCount())
	}
	if header := ansi.Strip(m.headerView("")); !strings.Contains(header, "Git repository not detected") {
		t.Fatalf("header did not explain files-only mode: %q", header)
	}
}

func TestFilesOnlyModeRefreshesAfterRealFilesystemWatchEvent(t *testing.T) {
	root := t.TempDir()
	m := NewFilesOnly(worktree.NewFilesystem(root))
	defer m.Close()
	m.workspaceLoading = false

	watch := waitWorkspaceWatchCmd(m.watcher)
	path := filepath.Join(root, "created.txt")
	if err := os.WriteFile(path, []byte("created"), 0o644); err != nil {
		t.Fatal(err)
	}
	message := watch()
	update, cmd := m.Update(message)
	m = update.(Model)
	if cmd == nil || m.workspaceWatchGeneration == 0 {
		t.Fatalf("watch event did not schedule reload: cmd=%v generation=%d", cmd != nil, m.workspaceWatchGeneration)
	}
	// The returned batch contains the next watch command and a debounced reload.
	batch, ok := cmd().(tea.BatchMsg)
	if !ok || len(batch) != 2 {
		t.Fatalf("watch command = %#v, want watch and debounce batch", cmd())
	}
	debounce := batch[1]()
	if _, ok := debounce.(workspaceDebounceMsg); !ok {
		t.Fatalf("watch batch second command = %T, want workspaceDebounceMsg", debounce)
	}
	update, load := m.Update(debounce)
	m = update.(Model)
	if load == nil {
		t.Fatal("debounce did not start Files-only reload")
	}
	result := load()
	if batch, ok := result.(tea.BatchMsg); ok {
		for _, candidate := range batch {
			update, _ = m.Update(candidate())
			m = update.(Model)
		}
	} else {
		update, _ = m.Update(result)
		m = update.(Model)
	}
	if len(m.workspaceEntries) != 1 || m.workspaceEntries[0].Path != "created.txt" {
		t.Fatalf("Files-only watcher reload entries = %#v", m.workspaceEntries)
	}
}

func TestWorkspaceThirdTabLoadsCommitGraph(t *testing.T) {
	m := newWithWorkspace(fakeProvider{}, 0, &fakeWorkspace{})
	m.active = 2
	if got := m.kind(); got != provider.Commits {
		t.Fatalf("third tab kind = %v, want Commits", got)
	}
}

func TestCommitAuthorScopeTabsRenderAndCanBeClicked(t *testing.T) {
	m := newWithWorkspace(fakeProvider{}, 0, &fakeWorkspace{})
	m.active = 2
	m.width, m.height = 100, 24
	m.resizeViewport()
	plain := ansi.Strip(m.filtersView())
	for _, label := range []string{"All", "Mine", "Others"} {
		if !strings.Contains(plain, label) {
			t.Fatalf("author scope tabs missing %q: %q", label, plain)
		}
	}
	updated, cmd := m.Update(tea.MouseMsg{X: 9, Y: 6, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
	m = updated.(Model)
	if m.graphAuthorScope != 1 || cmd == nil {
		t.Fatalf("Mine click selected scope=%d command=%v", m.graphAuthorScope, cmd != nil)
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
	m.width, m.height = 120, 16
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

func TestGraphKeyboardFileNavigationAndSearchHighlight(t *testing.T) {
	m := New(fakeProvider{}, 0)
	m.active = 4
	m.width, m.height, m.loadingList = 100, 20, false
	m.items[provider.Commits] = []provider.Item{
		{ID: "one", Title: "Fix CAFÉ cafe", Paths: []string{"docs/cafe.md", "main.go"}},
		{ID: "two", Title: "next", Paths: []string{"next.go"}},
	}
	m.graphFilter.SetValue("café")
	if view := ansi.Strip(m.View()); !strings.Contains(view, "Fix CAFÉ cafe") {
		t.Fatalf("graph search did not retain case-insensitive Unicode match: %q", view)
	}
	m.graphFilter.SetValue("")
	update, _ := m.Update(tea.KeyMsg{Type: tea.KeyRight})
	m = update.(Model)
	if m.graphDepth != graphCommitDepth || m.cursor[provider.Commits] != 0 {
		t.Fatalf("right moved Graph focus: depth=%v cursor=%d", m.graphDepth, m.cursor[provider.Commits])
	}
}

func TestGraphChangedFilesUseTreeBox(t *testing.T) {
	m := New(fakeProvider{}, 0)
	m.active = 4
	m.width, m.height, m.loadingList = 40, 24, false
	m.items[provider.Commits] = []provider.Item{{
		ID: "one", Title: "boxed files", Parents: []string{"base"}, Paths: []string{
			"internal/tui/file_icons.go", "internal/tui/file_icons_test.go", "internal/tui/model.go",
			"internal/tui/model_test.go", "internal/tui/view.go", "internal/tui/workspace.go", "internal/tui/workspace_test.go",
		},
	}}

	view := ansi.Strip(m.View())
	if !strings.Contains(view, "╭") || !strings.Contains(view, "╰") {
		t.Fatalf("changed files are not enclosed by a box:\n%s", view)
	}
	for _, want := range []string{"▾", "internal/tui", "file_icons.go", "view.go", "workspace_test.go"} {
		if !strings.Contains(view, want) {
			t.Fatalf("changed-files tree is missing %q:\n%s", want, view)
		}
	}
	if strings.Contains(view, "▾ internal ") || strings.Contains(view, "▾ tui ") {
		t.Fatalf("single-child directories were not compressed:\n%s", view)
	}
	foundCompressedDirectoryRow := false
	for _, line := range strings.Split(view, "\n") {
		if strings.Contains(line, "internal/tui") {
			foundCompressedDirectoryRow = strings.Contains(line, "▾") && strings.Contains(line, directoryOpenIcon)
			break
		}
	}
	if !foundCompressedDirectoryRow {
		t.Fatalf("compressed directory label wrapped away from its icon:\n%s", view)
	}
	boxedRows := m.graphFileRows(m.items[provider.Commits][0])
	wantTreeRows := graphTreeRows(graphFilePaths(m.items[provider.Commits][0]))
	wantRows := len(wantTreeRows) + 2
	if len(boxedRows) != wantRows {
		t.Fatalf("tree box reflowed rows: got=%d want=%d\n%s", len(boxedRows), wantRows, ansi.Strip(strings.Join(boxedRows, "\n")))
	}
	for index, row := range boxedRows {
		if width := lipgloss.Width(row); width != m.width {
			t.Fatalf("tree box row %d leaves outer margin: width=%d want=%d", index, width, m.width)
		}
	}
	for index, want := range wantTreeRows {
		line := ansi.Strip(boxedRows[index+1])
		left := strings.Index(line, "│")
		right := strings.LastIndex(line, "│")
		if left < 0 || right <= left {
			t.Fatalf("tree body row %d has invalid borders: %q", index, line)
		}
		body := line[left+len("│") : right]
		if len(body) == 0 || body[0] != ' ' {
			t.Fatalf("tree body row %d is missing left padding: %q", index, line)
		}
		if got := strings.TrimRight(body[1:], " "); got != want {
			t.Fatalf("tree body row %d reflowed: got=%q want=%q\n%s", index, got, want, ansi.Strip(strings.Join(boxedRows, "\n")))
		}
	}
	for _, line := range strings.Split(m.View(), "\n") {
		if width := lipgloss.Width(line); width > m.width {
			t.Fatalf("focused changed-files row width = %d, want <= %d: %q", width, m.width, ansi.Strip(line))
		}
	}
}

func TestGraphChangedFileTreeStopsCompressionAtBranches(t *testing.T) {
	rows := graphTreeRows([]string{"internal/provider/api.go", "internal/tui/model.go", "internal/tui/view.go"})
	view := strings.Join(rows, "\n")
	for _, want := range []string{"internal", "provider", "tui", "model.go", "view.go"} {
		if !strings.Contains(view, want) {
			t.Fatalf("branched tree is missing %q:\n%s", want, view)
		}
	}
	if strings.Contains(view, "internal/provider") || strings.Contains(view, "internal/tui") {
		t.Fatalf("tree compressed across a branching directory:\n%s", view)
	}
}

func TestGraphTreeConsecutiveFramesClearPreviousCommit(t *testing.T) {
	m := New(fakeProvider{}, 0)
	m.active, m.width, m.height, m.loadingList = 4, 60, 20, false
	m.items[provider.Commits] = []provider.Item{
		{ID: "one", Title: "large tree", Parents: []string{"two"}, Paths: []string{"internal/tui/file_icons.go", "internal/tui/workspace_test.go"}},
		{ID: "two", Title: "small tree", Paths: []string{"README.md"}},
	}
	_ = m.View()
	m.cursor[provider.Commits] = 1
	next := ansi.Strip(m.View())
	if strings.Contains(next, "file_icons.go") || strings.Contains(next, "workspace_test.go") {
		t.Fatalf("previous commit tree leaked into the next frame:\n%s", next)
	}
	if !strings.Contains(next, "README.md") {
		t.Fatalf("next frame is missing its selected commit file:\n%s", next)
	}
}

func TestWorkspaceSearchHighlightAppliesToFilesAndCommitTabs(t *testing.T) {
	workspace := &fakeWorkspace{}
	for _, tc := range []struct {
		name   string
		active int
		setup  func(*Model)
	}{
		{
			name:   "Files",
			active: workspaceFilesTab,
			setup: func(m *Model) {
				m.workspaceEntries = []worktree.Entry{{Path: "CAFÉ.md", Name: "CAFÉ.md"}}
			},
		},
		{
			name:   "Commit",
			active: workspaceCommitTab,
			setup: func(m *Model) {
				m.workspaceStatus = worktree.Status{Unstaged: []worktree.Change{{Path: "docs/CAFÉ.md", Code: 'M'}}}
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := newWithWorkspace(fakeProvider{}, 0, workspace)
			m.width, m.height, m.loadingList = 100, 20, false
			m.active = tc.active
			tc.setup(&m)
			m.fileFilter.SetValue("café")
			if view := m.View(); !strings.Contains(view, graphMatchStyle.Render("CAFÉ")) {
				t.Fatalf("workspace %s filter did not highlight its match: %q", tc.name, view)
			}
		})
	}
}

func TestWorkspaceListsRenderLanguageIcons(t *testing.T) {
	tests := []struct {
		name string
		path string
		icon string
	}{
		{name: "Go", path: "main.go", icon: ""},
		{name: "Java", path: "Application.java", icon: ""},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			m := newWithWorkspace(fakeProvider{}, 0, &fakeWorkspace{})
			m.width, m.height = 80, 20

			m.active = workspaceFilesTab
			m.workspaceEntries = []worktree.Entry{{Path: test.path, Name: test.path}}
			if view := m.workspaceList(80, 10); !strings.Contains(view, test.icon+" "+test.path) {
				t.Fatalf("Files view missing language icon: %q", view)
			}

			m.active = workspaceCommitTab
			m.workspaceStatus.Unstaged = []worktree.Change{{Path: test.path, Code: 'M'}}
			if view := m.workspaceList(80, 10); !strings.Contains(view, test.icon+" "+test.path) {
				t.Fatalf("Commit view missing language icon: %q", view)
			}
		})
	}
}

func TestArrowFocusFlowAcrossTabsGraphAndWorkspacePreview(t *testing.T) {
	t.Setenv("KITTY_WINDOW_ID", "1")
	workspace := &fakeWorkspace{}
	m := newWithWorkspace(fakeProvider{}, 0, workspace)
	m.width, m.height, m.loadingList = 100, 20, false
	m.active = 2 // Graph follows Commit and Files.
	m.items[provider.Commits] = []provider.Item{{ID: "one", Title: "first", Paths: []string{"one.go"}}}
	graphView := ansi.Strip(m.View())
	if !strings.Contains(graphView, "Zzam Tiger") || !strings.Contains(graphView, "Graph") || !strings.Contains(graphView, "All") || !strings.Contains(graphView, "Filter:") {
		t.Fatalf("Graph lost its title, scope status, or search row: %q", graphView)
	}
	if rawGraphView := m.View(); strings.Contains(rawGraphView, "\x1b_Ga=d") {
		t.Fatalf("Graph view must not emit Kitty image cleanup: %q", rawGraphView)
	}

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = updated.(Model)
	if m.focus != focusGraphFilters {
		t.Fatalf("tab down focus = %v, want graph filters", m.focus)
	}
	m.graphFilter.Focus()
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyUp})
	m = updated.(Model)
	if m.focus != focusTabs || m.graphFilter.Focused() {
		t.Fatalf("search up did not return to tabs: focus=%v input=%t", m.focus, m.graphFilter.Focused())
	}
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = updated.(Model)
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = updated.(Model)
	if m.focus != focusGraphFilters || m.graphFilter.Focused() {
		t.Fatalf("search down did not focus author filters: focus=%v input=%t", m.focus, m.graphFilter.Focused())
	}
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = updated.(Model)
	if m.focus != focusGraphCommits {
		t.Fatalf("filter down focus = %v, want graph commits", m.focus)
	}
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyUp})
	m = updated.(Model)
	if m.focus != focusGraphFilters || m.graphFilter.Focused() {
		t.Fatalf("first commit up did not focus Graph author filters: focus=%v input=%t", m.focus, m.graphFilter.Focused())
	}
	m.graphFilter.Blur()
	m.focus, m.graphDepth, m.graphFile = focusListItems, graphFileDepth, 0
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyUp})
	m = updated.(Model)
	if m.focus != focusGraphFilters || m.graphFilter.Focused() || m.graphDepth != graphCommitDepth {
		t.Fatalf("first Graph file up did not focus author filters: focus=%v input=%t depth=%v", m.focus, m.graphFilter.Focused(), m.graphDepth)
	}

	m.active = workspaceCommitTab
	m.focus = focusTabs
	m.workspaceStatus = worktree.Status{Unstaged: []worktree.Change{{Path: "one.go", Code: 'M'}}}
	m.workspaceDiffRows = strings.Split("header\n"+strings.Repeat("changed line\n", 30), "\n")
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = updated.(Model)
	if m.focus != focusFileFilter || !m.fileFilter.Focused() {
		t.Fatalf("commit tab down did not focus Filter: focus=%v input=%t", m.focus, m.fileFilter.Focused())
	}
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = updated.(Model)
	if m.focus != focusCommitMessage || !m.commitMessage.Focused() {
		t.Fatalf("Filter down did not focus message: focus=%v input=%t", m.focus, m.commitMessage.Focused())
	}
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = updated.(Model)
	if m.focus != focusWorkspaceList {
		t.Fatalf("message down focus = %v, want file list", m.focus)
	}
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRight})
	m = updated.(Model)
	if m.focus != focusWorkspacePreview {
		t.Fatalf("file right focus = %v, want preview", m.focus)
	}
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyLeft})
	m = updated.(Model)
	if m.focus != focusWorkspaceList {
		t.Fatalf("preview left focus = %v, want file list", m.focus)
	}
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRight})
	m = updated.(Model)
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = updated.(Model)
	if m.workspacePreviewOffset == 0 {
		t.Fatal("preview down did not scroll")
	}
	m.workspacePreviewOffset = 0
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyUp})
	m = updated.(Model)
	if m.focus != focusCommitMessage || !m.commitMessage.Focused() {
		t.Fatalf("preview-top up did not return to message: focus=%v input=%t", m.focus, m.commitMessage.Focused())
	}
}

func TestArrowTabSwitchingIsIsolatedToTabFocus(t *testing.T) {
	m := newWithWorkspace(fakeProvider{}, 0, &fakeWorkspace{})
	m.width, m.height = 100, 20
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRight})
	m = updated.(Model)
	if m.active != workspaceFilesTab || m.focus != focusTabs {
		t.Fatalf("tab-right active=%d focus=%v", m.active, m.focus)
	}
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = updated.(Model)
	active := m.active
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRight})
	m = updated.(Model)
	if m.active != active {
		t.Fatalf("right outside tab focus switched tabs: %d -> %d", active, m.active)
	}
}

func TestBranchTabFocusUsesLeftAndRightToSwitchTabs(t *testing.T) {
	m := newWithWorkspace(fakeProvider{}, 0, &fakeWorkspace{})
	m.width, m.height = 100, 20
	m.active = 3 // Commit, Files, Graph, Branches.
	m.focus = focusTabs
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyLeft})
	m = updated.(Model)
	if m.active != 2 || m.focus != focusTabs {
		t.Fatalf("branch-tab left active=%d focus=%v, want Graph tab focus", m.active, m.focus)
	}
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRight})
	m = updated.(Model)
	if m.active != 3 || m.focus != focusTabs {
		t.Fatalf("graph-tab right active=%d focus=%v, want Branches tab focus", m.active, m.focus)
	}
	for want := 4; want < m.tabCount(); want++ {
		updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRight})
		m = updated.(Model)
		if m.active != want || m.focus != focusTabs {
			t.Fatalf("right tab %d active=%d focus=%v", want, m.active, m.focus)
		}
	}
}

func TestGraphSearchDownLeavesInputForResultNavigation(t *testing.T) {
	m := New(fakeProvider{}, 0)
	m.active, m.width, m.height, m.loadingList = 4, 100, 20, false
	m.items[provider.Commits] = []provider.Item{{ID: "one", Title: "match first"}, {ID: "two", Title: "match second"}}
	m.graphQuery.SetValue("match")
	m.graphQuery.Focus()
	update, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = update.(Model)
	if m.graphQuery.Focused() || m.focus != focusListItems || m.cursor[provider.Commits] != 0 {
		t.Fatalf("down did not leave search for results: input=%t focus=%v cursor=%d", m.graphQuery.Focused(), m.focus, m.cursor[provider.Commits])
	}
}

func TestGraphSearchEnterReturnsToNavigationAndSlashRefocuses(t *testing.T) {
	m := New(fakeProvider{}, 0)
	m.active, m.width, m.height, m.loadingList = 4, 100, 20, false
	m.items[provider.Commits] = []provider.Item{{ID: "one", Title: "match"}}
	m.graphQuery.SetValue("match")
	m.graphQuery.Focus()
	update, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = update.(Model)
	if m.graphQuery.Focused() {
		t.Fatal("Enter left graph search focused")
	}
	update, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	m = update.(Model)
	if !m.graphQuery.Focused() {
		t.Fatal("slash did not refocus graph search")
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
		plain := ansi.Strip(bar)
		if !strings.HasPrefix(plain, "╭") || !strings.Contains(plain, "├") {
			t.Fatalf("active %d tab bar does not have a rounded box: %q", active, plain)
		}
	}
}

func TestRoundedTabBoxKeepsStatusAndHelpVisible(t *testing.T) {
	m := newWithWorkspace(fakeProvider{}, 0, &fakeWorkspace{})
	m.active = workspaceFilesTab
	m.width, m.height = 80, 14
	m.status = "Ready"

	lines := strings.Split(ansi.Strip(m.View()), "\n")
	if len(lines) != m.height {
		t.Fatalf("view height = %d, want %d", len(lines), m.height)
	}
	if !strings.HasPrefix(lines[3], "├") || !strings.Contains(lines[3], "┴") || !strings.HasSuffix(lines[3], "╮") {
		t.Fatalf("tab/content junction border is malformed: %q", lines[3])
	}
	if !strings.Contains(lines[4], "Filter:") || !strings.HasPrefix(lines[5], "├") || strings.Contains(lines[5], "┬") || !strings.HasSuffix(lines[5], "┤") {
		t.Fatalf("filter section is malformed: %q", lines[4:6])
	}
	if !strings.HasPrefix(lines[len(lines)-3], "╰") || !strings.HasSuffix(lines[len(lines)-3], "╯") {
		t.Fatalf("rounded content bottom border is malformed: %q", lines[len(lines)-3])
	}
	if !strings.Contains(lines[len(lines)-2], "Ready") {
		t.Fatalf("status bar missing from penultimate row: %q", lines[len(lines)-2])
	}
	if !strings.Contains(lines[len(lines)-1], "focused") {
		t.Fatalf("help bar missing from final row: %q", lines[len(lines)-1])
	}
}

func TestFilterRowsCanBeClickedAcrossAllTabTypes(t *testing.T) {
	tests := []struct {
		name   string
		model  Model
		active int
		check  func(Model) bool
	}{
		{name: "Commit", model: newWithWorkspace(fakeProvider{}, 0, &fakeWorkspace{}), active: workspaceCommitTab, check: func(m Model) bool { return m.fileFilter.Focused() && m.focus == focusFileFilter }},
		{name: "Files", model: newWithWorkspace(fakeProvider{}, 0, &fakeWorkspace{}), active: workspaceFilesTab, check: func(m Model) bool { return m.fileFilter.Focused() && m.focus == focusFileFilter }},
		{name: "Graph", model: newWithWorkspace(fakeProvider{}, 0, &fakeWorkspace{}), active: 2, check: func(m Model) bool { return m.graphFilter.Focused() && m.focus == focusGraphFilters }},
		{name: "Remote list", model: New(fakeProvider{}, 0), active: 0, check: func(m Model) bool { return m.graphQuery.Focused() && m.focus == focusListSearch }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			m := test.model
			m.active, m.width, m.height = test.active, 100, 24
			m.screen = listScreen
			updated, _ := m.Update(tea.MouseMsg{X: 10, Y: 4, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
			m = updated.(Model)
			if !test.check(m) || !m.filterFocused() {
				t.Fatalf("Filter click did not focus %s", test.name)
			}
		})
	}
}

func TestFocusedFilterUsesAccentBorder(t *testing.T) {
	if filterBorderStyle(true).GetForeground() != accent || filterBorderStyle(false).GetForeground() != border {
		t.Fatal("Filter border does not switch between accent and normal colors")
	}
}

func TestMouseFocusMovesCursorOutOfFilter(t *testing.T) {
	m := newWithWorkspace(fakeProvider{}, 0, &fakeWorkspace{})
	m.active, m.width, m.height = workspaceCommitTab, 100, 24
	updated, _ := m.Update(tea.MouseMsg{X: 10, Y: 4, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
	m = updated.(Model)
	if !m.fileFilter.Focused() {
		t.Fatal("Filter did not receive initial mouse focus")
	}
	updated, _ = m.Update(tea.MouseMsg{X: 5, Y: 7, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
	m = updated.(Model)
	if m.fileFilter.Focused() || !m.commitMessage.Focused() || m.focus != focusCommitMessage {
		t.Fatalf("Commit message mouse focus left duplicate cursors: filter=%t commit=%t focus=%v", m.fileFilter.Focused(), m.commitMessage.Focused(), m.focus)
	}

	m.active = workspaceFilesTab
	m.workspaceEntries = []worktree.Entry{{Path: "main.go", Name: "main.go"}}
	updated, _ = m.Update(tea.MouseMsg{X: 10, Y: 4, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
	m = updated.(Model)
	updated, _ = m.Update(tea.MouseMsg{X: 3, Y: 6, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
	m = updated.(Model)
	if m.fileFilter.Focused() || m.commitMessage.Focused() || m.focus != focusWorkspaceList {
		t.Fatalf("file mouse selection left an input cursor: filter=%t commit=%t focus=%v", m.fileFilter.Focused(), m.commitMessage.Focused(), m.focus)
	}
}

func TestTabFocusHighlightsRoundedBorder(t *testing.T) {
	m := newWithWorkspace(fakeProvider{}, 0, &fakeWorkspace{})
	m.focus = focusTabs
	focusedColor := m.tabBorderStyle().GetBorderTopForeground()

	m.focus = focusFileFilter
	unfocusedColor := m.tabBorderStyle().GetBorderTopForeground()
	if focusedColor == unfocusedColor {
		t.Fatal("tab border style did not change when focus left the tabs")
	}
	if focusedColor != accent || unfocusedColor != border {
		t.Fatalf("tab border colors: focused=%v unfocused=%v", focusedColor, unfocusedColor)
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
	if len(entries) != 1 || entries[0].Path != "internal/tui/model.go" || entries[0].Name != "internal/tui/model.go" {
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

func TestWorkspaceCommitChangesFormDirectoryTree(t *testing.T) {
	m := newWithWorkspace(fakeProvider{}, 0, &fakeWorkspace{})
	m.active = workspaceCommitTab
	m.workspaceStatus = worktree.Status{Unstaged: []worktree.Change{
		{Path: "cmd/app/main.go", Code: 'M'},
		{Path: "cmd/app/run.go", Code: 'M'},
		{Path: "README.md", Code: 'M'},
	}}

	changes := m.filteredWorkspaceChanges()
	want := []struct {
		path  string
		name  string
		dir   bool
		depth int
	}{
		{path: "README.md", name: "README.md"},
		{path: "cmd/app", name: "cmd/app", dir: true},
		{path: "cmd/app/main.go", name: "main.go", depth: 1},
		{path: "cmd/app/run.go", name: "run.go", depth: 1},
	}
	if len(changes) != len(want) {
		t.Fatalf("tree length = %d, want %d: %#v", len(changes), len(want), changes)
	}
	for index, expected := range want {
		got := changes[index]
		if got.displayPath() != expected.path || got.name != expected.name || got.isDir != expected.dir || got.depth != expected.depth {
			t.Fatalf("tree item %d = path %q dir=%t depth=%d, want %#v", index, got.displayPath(), got.isDir, got.depth, expected)
		}
	}
}

func TestWorkspaceCommitCompressesSingleChildPaths(t *testing.T) {
	m := newWithWorkspace(fakeProvider{}, 0, &fakeWorkspace{})
	m.active = workspaceCommitTab
	m.workspaceStatus.Unstaged = []worktree.Change{
		{Path: "com/a/b/c/f/e.java", Code: 'M'},
		{Path: "com/a/b/d", Code: 'M'},
	}

	changes := m.filteredWorkspaceChanges()
	if len(changes) != 3 {
		t.Fatalf("compressed tree length = %d, want 3: %#v", len(changes), changes)
	}
	want := []struct {
		path, name string
		isDir      bool
		depth      int
	}{
		{path: "com/a/b", name: "com/a/b", isDir: true},
		{path: "com/a/b/c/f/e.java", name: "c/f/e.java", depth: 1},
		{path: "com/a/b/d", name: "d", depth: 1},
	}
	for index, expected := range want {
		got := changes[index]
		if got.path != expected.path || got.name != expected.name || got.isDir != expected.isDir || got.depth != expected.depth {
			t.Fatalf("compressed item %d = %#v, want %#v", index, got, expected)
		}
	}
}

func TestWorkspaceFilesCompressesSingleChildPaths(t *testing.T) {
	workspace := &fakeWorkspace{entriesByDir: map[string][]worktree.Entry{
		"":            {{Path: "com", Name: "com", IsDir: true}},
		"com":         {{Path: "com/a", Name: "a", IsDir: true}},
		"com/a":       {{Path: "com/a/b", Name: "b", IsDir: true}},
		"com/a/b":     {{Path: "com/a/b/c", Name: "c", IsDir: true}, {Path: "com/a/b/d", Name: "d"}},
		"com/a/b/c":   {{Path: "com/a/b/c/f", Name: "f", IsDir: true}},
		"com/a/b/c/f": {{Path: "com/a/b/c/f/e.java", Name: "e.java"}},
	}}
	m := newWithWorkspace(fakeProvider{}, 0, workspace)
	m.active = workspaceFilesTab

	updated, _ := m.Update(m.fetchWorkspaceCmd(m.workspaceEntryRequest)())
	m = updated.(Model)
	entries := m.visibleWorkspaceEntries()
	if len(entries) != 1 || entries[0].Path != "com/a/b" || entries[0].Name != "com/a/b" || !entries[0].IsDir {
		t.Fatalf("compressed root entries = %#v", entries)
	}

	expanded, load := m.toggleWorkspaceDirectory()
	m = expanded
	if load != nil {
		updated, _ = m.Update(load())
		m = updated.(Model)
	}
	entries = m.visibleWorkspaceEntries()
	if len(entries) != 3 || entries[1].Name != "c/f/e.java" || entries[1].Path != "com/a/b/c/f/e.java" || entries[2].Name != "d" {
		t.Fatalf("compressed expanded entries = %#v", entries)
	}
}

func TestWorkspaceFilesCompressesRootPathEndingInFile(t *testing.T) {
	workspace := &fakeWorkspace{entriesByDir: map[string][]worktree.Entry{
		"":        {{Path: "src", Name: "src", IsDir: true}},
		"src":     {{Path: "src/app", Name: "app", IsDir: true}},
		"src/app": {{Path: "src/app/main.go", Name: "main.go"}},
	}}
	m := newWithWorkspace(fakeProvider{}, 0, workspace)
	m.active = workspaceFilesTab

	updated, _ := m.Update(m.fetchWorkspaceCmd(m.workspaceEntryRequest)())
	m = updated.(Model)
	entries := m.visibleWorkspaceEntries()
	if len(entries) != 1 || entries[0].Path != "src/app/main.go" || entries[0].Name != "src/app/main.go" || entries[0].IsDir {
		t.Fatalf("compressed file path = %#v", entries)
	}
	displays := m.visibleWorkspaceEntryDisplays()
	if len(displays) != 1 || displays[0].depth != 0 {
		t.Fatalf("compressed root file display = %#v, want depth 0", displays)
	}
	if row := ansi.Strip(m.workspaceList(40, 1)); !strings.HasPrefix(row, "  "+workspaceFileIcon("main.go")+" src/app/main.go") {
		t.Fatalf("compressed root file row is unexpectedly indented: %q", row)
	}
}

func TestWorkspaceCommitMouseSkipsPanelTitles(t *testing.T) {
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

	_, buttonY, _ := m.commitDashboardGeometry()
	updated, cmd := m.Update(tea.MouseMsg{X: 2, Y: buttonY + 2, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
	m = updated.(Model)
	if cmd != nil || m.workspaceCursor != 0 {
		t.Fatalf("panel title selected cursor %d with cmd=%v", m.workspaceCursor, cmd != nil)
	}
	updated, cmd = m.Update(tea.MouseMsg{X: 2, Y: buttonY + 3, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
	m = updated.(Model)
	if cmd == nil || m.workspaceCursor != 0 {
		t.Fatalf("change click selected cursor %d with cmd=%v", m.workspaceCursor, cmd != nil)
	}
}

func TestWorkspaceCommitPreviewErrorKeepsFileListInteractive(t *testing.T) {
	previewErr := errors.New("large.bin exceeds the 8 MiB diff preview limit")
	workspace := &fakeWorkspace{
		diffs:    map[string]worktree.Diff{"small.go": {Path: "small.go"}},
		diffErrs: map[string]error{"large.bin": previewErr},
	}
	m := newWithWorkspace(fakeProvider{}, 0, workspace)
	m.active, m.width, m.height = workspaceCommitTab, 120, 20
	m.workspaceStatus.Unstaged = []worktree.Change{
		{Path: "large.bin", Code: 'M'},
		{Path: "small.go", Code: 'M'},
	}
	m.workspaceLoading = false
	m.workspacePreviewRequest = 1

	updated, _ := m.Update(workspaceResultMsg{request: 1, op: "diff", err: previewErr})
	m = updated.(Model)
	if m.err != nil || !errors.Is(m.workspacePreviewErr, previewErr) {
		t.Fatalf("preview error state: global=%v preview=%v", m.err, m.workspacePreviewErr)
	}
	view := m.View()
	if !strings.Contains(view, "large.bin") || !strings.Contains(view, "small.go") || !strings.Contains(view, "Unable to load preview") {
		t.Fatalf("preview error replaced the file list:\n%s", view)
	}

	_, _, changesTop := m.commitDashboardGeometry()
	updated, cmd := m.Update(tea.MouseMsg{X: 2, Y: changesTop + 2, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
	m = updated.(Model)
	if cmd == nil || m.workspaceCursor != 1 || m.workspacePreviewErr != nil {
		t.Fatalf("file click after preview error: cmd=%v cursor=%d previewErr=%v", cmd != nil, m.workspaceCursor, m.workspacePreviewErr)
	}
}

func TestWorkspaceCommitFolderClickCollapsesAndExpandsChildren(t *testing.T) {
	m := newWithWorkspace(fakeProvider{}, 0, &fakeWorkspace{})
	m.active, m.width, m.height = workspaceCommitTab, 120, 20
	m.workspaceStatus.Unstaged = []worktree.Change{
		{Path: "cmd/app/main.go", Code: 'M'},
		{Path: "cmd/app/run.go", Code: 'M'},
		{Path: "README.md", Code: 'M'},
	}
	m.workspaceLoading = false
	if got := len(m.filteredWorkspaceChanges()); got != 4 {
		t.Fatalf("expanded changes = %d, want 4", got)
	}

	dashboardChanges := m.dashboardPanelChanges(false)
	directoryIndex := -1
	for index, change := range dashboardChanges {
		if change.isDir && change.path == "cmd/app" {
			directoryIndex = index
		}
	}
	if directoryIndex < 0 {
		t.Fatalf("dashboard tree missing cmd/app: %#v", dashboardChanges)
	}
	_, _, changesTop := m.commitDashboardGeometry()
	updated, cmd := m.Update(tea.MouseMsg{X: 4, Y: changesTop + 1 + directoryIndex, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
	m = updated.(Model)
	if cmd != nil || !m.workspaceChangeCollapsed[workspaceChangeExpansionKey(false, "cmd/app")] {
		t.Fatalf("collapse click: cmd=%v collapsed=%t", cmd != nil, m.workspaceChangeCollapsed[workspaceChangeExpansionKey(false, "cmd/app")])
	}
	changes := m.filteredWorkspaceChanges()
	if len(changes) != 2 || changes[0].path != "README.md" || changes[1].path != "cmd/app" {
		t.Fatalf("collapsed changes = %#v", changes)
	}
	if rendered := m.workspaceList(50, 10); !strings.Contains(rendered, "▸ "+directoryIcon+" cmd/app") {
		t.Fatalf("collapsed folder icon missing: %q", rendered)
	}

	updated, cmd = m.Update(tea.MouseMsg{X: 4, Y: changesTop + 1 + directoryIndex, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
	m = updated.(Model)
	if cmd != nil || m.workspaceChangeCollapsed[workspaceChangeExpansionKey(false, "cmd/app")] || len(m.filteredWorkspaceChanges()) != 4 {
		t.Fatalf("expand click: cmd=%v collapsed=%t changes=%d", cmd != nil, m.workspaceChangeCollapsed[workspaceChangeExpansionKey(false, "cmd")], len(m.filteredWorkspaceChanges()))
	}
}

func TestWorkspaceCommitMessageSubmitsWithCtrlS(t *testing.T) {
	workspace := &fakeWorkspace{}
	m := newWithWorkspace(fakeProvider{}, 0, workspace)
	m.active, m.width, m.height = workspaceCommitTab, 120, 20
	m.workspaceStatus.Staged = []worktree.Change{{Path: "main.go", Code: 'M'}}
	m.commitMessage.SetValue("Describe the change")
	m.commitMessage.Focus()

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlS})
	m = updated.(Model)
	if cmd == nil || !m.actionBusy {
		t.Fatalf("Ctrl+S did not start commit: command=%v busy=%t", cmd != nil, m.actionBusy)
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

func TestWorkspaceCommitDashboardPanelsAndRemoteButtons(t *testing.T) {
	m := newWithWorkspace(fakeProvider{}, 0, &fakeWorkspace{})
	m.active, m.width, m.height = workspaceCommitTab, 100, 24
	if left, _ := m.commitDashboardWidths(); left != 48 {
		t.Fatalf("initial Commit panel width = %d, want fixed width 48", left)
	}
	plain := ansi.Strip(m.View())
	for _, want := range []string{"Commit ( 0↓ 0↑ )", "Staged", "Changes", "File Diff", "Commit message", "Commit"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("commit dashboard missing %q:\n%s", want, plain)
		}
	}
	for _, want := range []string{"No Staging Files", "No Changes Files"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("empty Commit dashboard missing %q:\n%s", want, plain)
		}
	}
	if strings.Contains(plain, "Pull") || strings.Contains(plain, "Push") {
		t.Fatalf("remote buttons rendered without a remote:\n%s", plain)
	}
	lines := strings.Split(plain, "\n")
	if !strings.HasPrefix(lines[3], "├") || !strings.HasSuffix(lines[3], "╮") {
		t.Fatalf("Commit dashboard is not connected to the root border: %q", lines[3])
	}
	if !strings.Contains(lines[4], "Filter:") || !strings.HasPrefix(lines[5], "├") || strings.Contains(lines[5], "┬") {
		t.Fatalf("Commit Filter section is malformed: %q", lines[4:6])
	}
	if !strings.HasPrefix(lines[6], "│╭") || !strings.HasSuffix(lines[6], "│") {
		t.Fatalf("Commit panels are not inside the root border: %q", lines[6])
	}
	if !strings.HasPrefix(lines[len(lines)-3], "╰") || !strings.HasSuffix(lines[len(lines)-3], "╯") {
		t.Fatalf("Commit dashboard root bottom is malformed: %q", lines[len(lines)-3])
	}

	m.workspaceRemote = worktree.RemoteState{Available: true, Ahead: 2, Behind: 3}
	plain = ansi.Strip(m.View())
	for _, want := range []string{"Commit ( 3↓ 2↑ )", "Pull", "Push", "Commit"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("remote commit dashboard missing %q:\n%s", want, plain)
		}
	}
}

func TestWorkspaceCommitMessageAndPanelSplitsResize(t *testing.T) {
	m := newWithWorkspace(fakeProvider{}, 0, &fakeWorkspace{})
	m.active, m.width, m.height = workspaceCommitTab, 100, 30
	m.workspaceStatus = worktree.Status{
		Staged:   []worktree.Change{{Path: "staged.go", Code: 'M'}},
		Unstaged: []worktree.Change{{Path: "changed.go", Code: 'M'}},
	}
	left, _, initialChangesTop := m.commitDashboardGeometry()
	m.commitMessage.SetValue(strings.Repeat("long commit message ", 8))
	_, _, wrappedChangesTop := m.commitDashboardGeometry()
	if wrappedChangesTop <= initialChangesTop {
		t.Fatalf("long message did not grow Commit panel: initial=%d wrapped=%d", initialChangesTop, wrappedChangesTop)
	}

	updated, _ := m.Update(tea.MouseMsg{X: 2, Y: wrappedChangesTop, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
	m = updated.(Model)
	if !m.workspaceCommitDividerDragging {
		t.Fatal("Staged/Changes divider did not start dragging")
	}
	updated, _ = m.Update(tea.MouseMsg{X: 2, Y: wrappedChangesTop + 2, Button: tea.MouseButtonLeft, Action: tea.MouseActionMotion})
	m = updated.(Model)
	if m.workspaceCommitSplitRatio == 0.5 {
		t.Fatal("Staged/Changes divider did not update its ratio")
	}

	updated, _ = m.Update(tea.MouseMsg{X: left, Y: 7, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
	m = updated.(Model)
	if !m.workspaceDividerDragging {
		t.Fatal("File Diff divider did not start dragging")
	}
}

func TestWorkspaceCommitMessageWrapsByTerminalCellWidth(t *testing.T) {
	m := newWithWorkspace(fakeProvider{}, 0, &fakeWorkspace{})
	m.active, m.width, m.height = workspaceCommitTab, 80, 30
	m.workspaceCommitWidth = 24
	m.commitMessage.SetValue(strings.Repeat("한글", 12))
	segments := m.commitMessageSegments(10)
	if len(segments) < 3 {
		t.Fatalf("wide Commit message did not wrap: %#v", segments)
	}
	runes := []rune(m.commitMessage.Value())
	for _, segment := range segments {
		if width := ansi.StringWidth(string(runes[segment.start:segment.end])); width > 10 {
			t.Fatalf("wrapped Commit row width = %d, want <= 10", width)
		}
	}
	shortRows := m.commitMessageRowCount(48)
	longRows := m.commitMessageRowCount(24)
	if longRows <= shortRows {
		t.Fatalf("Commit panel did not grow at narrow width: wide=%d narrow=%d", shortRows, longRows)
	}
}

func TestWorkspaceCommitMessageEnterInsertsNewline(t *testing.T) {
	m := newWithWorkspace(fakeProvider{}, 0, &fakeWorkspace{})
	m.active, m.width, m.height = workspaceCommitTab, 80, 30
	m.focus = focusCommitMessage
	m.commitMessage.SetValue("subjectbody")
	m.commitMessage.SetCursor(len([]rune("subject")))
	m.commitMessage.Focus()

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)
	if cmd != nil || m.commitMessageText() != "subject\nbody" || m.commitMessage.Position() != len([]rune("subject\n")) {
		t.Fatalf("Enter newline state: value=%q position=%d cmd=%v", m.commitMessageText(), m.commitMessage.Position(), cmd != nil)
	}
	segments := m.commitMessageSegments(40)
	if len(segments) != 2 {
		t.Fatalf("explicit newline segments = %#v, want 2 rows", segments)
	}
}

func TestWorkspaceCommitEmptyChangePanelsAreFixedHeight(t *testing.T) {
	m := newWithWorkspace(fakeProvider{}, 0, &fakeWorkspace{})
	m.active, m.width, m.height = workspaceCommitTab, 100, 30
	panelHeight := m.height - 9
	commitHeight := m.commitMessageRowCount(48) + 3
	stagedHeight, changesHeight := m.commitChangePanelHeights(panelHeight - commitHeight)
	if stagedHeight != 3 || changesHeight != 3 {
		t.Fatalf("empty panel heights = staged %d, changes %d; want 3 and 3", stagedHeight, changesHeight)
	}
	_, _, changesTop := m.commitDashboardGeometry()
	updated, _ := m.Update(tea.MouseMsg{X: 2, Y: changesTop, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
	m = updated.(Model)
	if m.workspaceCommitDividerDragging {
		t.Fatal("empty Staged/Changes panels started height resizing")
	}
}

func TestWorkspaceCommitFileArrowButtonsStageAndUnstage(t *testing.T) {
	workspace := &fakeWorkspace{}
	m := newWithWorkspace(fakeProvider{}, 0, workspace)
	m.active, m.width, m.height = workspaceCommitTab, 100, 30
	m.workspaceStatus = worktree.Status{
		Staged:   []worktree.Change{{Path: "ready.go", Code: 'M'}},
		Unstaged: []worktree.Change{{Path: "change.go", Code: 'M'}},
	}
	plain := ansi.Strip(m.View())
	if !strings.Contains(plain, "ready.go") || !strings.Contains(plain, "↓") || !strings.Contains(plain, "change.go") || !strings.Contains(plain, "↑") {
		t.Fatalf("stage arrow buttons missing:\n%s", plain)
	}
	if stageArrowStyle.GetBackground() == unstageArrowStyle.GetBackground() ||
		stageArrowStyle.GetForeground() == stageArrowStyle.GetBackground() ||
		unstageArrowStyle.GetForeground() == unstageArrowStyle.GetBackground() {
		t.Fatal("stage arrow buttons do not have distinct high-contrast colors")
	}

	left, buttonY, changesTop := m.commitDashboardGeometry()
	updated, cmd := m.Update(tea.MouseMsg{X: left - 1, Y: buttonY + 3, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
	m = updated.(Model)
	if cmd == nil {
		t.Fatal("Unstage arrow did not start an action")
	}
	cmd()
	if !reflect.DeepEqual(workspace.unstaged, []string{"ready.go"}) {
		t.Fatalf("unstaged paths = %#v", workspace.unstaged)
	}

	m.actionBusy = false
	updated, cmd = m.Update(tea.MouseMsg{X: left - 1, Y: changesTop + 1, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
	if cmd == nil {
		t.Fatal("Stage arrow did not start an action")
	}
	cmd()
	if !reflect.DeepEqual(workspace.staged, []string{"change.go"}) {
		t.Fatalf("staged paths = %#v", workspace.staged)
	}
}

func TestWorkspaceCommitChangesRenderAsCollapsibleTree(t *testing.T) {
	m := newWithWorkspace(fakeProvider{}, 0, &fakeWorkspace{})
	m.active, m.width, m.height = workspaceCommitTab, 100, 30
	m.workspaceStatus.Unstaged = []worktree.Change{
		{Path: "cmd/app/main.go", Code: 'M'},
		{Path: "cmd/app/run.go", Code: 'M'},
		{Path: "README.md", Code: 'M'},
	}
	tree := m.dashboardPanelChanges(false)
	directoryIndex := -1
	for index, item := range tree {
		if item.isDir && item.path == "cmd/app" {
			directoryIndex = index
		}
	}
	if directoryIndex < 0 {
		t.Fatalf("Changes tree has no compressed cmd/app directory: %#v", tree)
	}
	plain := ansi.Strip(m.View())
	if !strings.Contains(plain, "cmd/app") || !strings.Contains(plain, "main.go") || !strings.Contains(plain, "run.go") {
		t.Fatalf("Changes tree is not rendered:\n%s", plain)
	}

	_, _, changesTop := m.commitDashboardGeometry()
	updated, cmd := m.Update(tea.MouseMsg{X: 3, Y: changesTop + 1 + directoryIndex, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
	m = updated.(Model)
	if cmd != nil || !m.workspaceChangeCollapsed[workspaceChangeExpansionKey(false, "cmd/app")] {
		t.Fatalf("Changes folder click did not collapse: cmd=%v collapsed=%t", cmd != nil, m.workspaceChangeCollapsed[workspaceChangeExpansionKey(false, "cmd/app")])
	}
}

func TestWorkspaceCommitCursorSelectsEachTreeRow(t *testing.T) {
	m := newWithWorkspace(fakeProvider{}, 0, &fakeWorkspace{})
	m.active, m.width, m.height = workspaceCommitTab, 100, 30
	m.workspaceStatus = worktree.Status{
		Staged:   []worktree.Change{{Path: "ready.go", Code: 'M'}},
		Unstaged: []worktree.Change{{Path: "change.go", Code: 'M'}, {Path: "next.go", Code: 'M'}},
	}
	m.focus = focusWorkspaceList
	m.workspaceCursor = 0
	all := m.filteredWorkspaceChanges()
	if len(all) != 3 || !m.dashboardChangeSelected(all[0]) {
		t.Fatalf("initial tree selection = cursor %d changes %#v", m.workspaceCursor, all)
	}

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = updated.(Model)
	all = m.filteredWorkspaceChanges()
	if m.workspaceCursor != 1 || !m.dashboardChangeSelected(all[1]) || m.dashboardChangeSelected(all[0]) {
		t.Fatalf("second tree selection = cursor %d", m.workspaceCursor)
	}
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = updated.(Model)
	all = m.filteredWorkspaceChanges()
	if m.workspaceCursor != 2 || !m.dashboardChangeSelected(all[2]) {
		t.Fatalf("third tree selection = cursor %d", m.workspaceCursor)
	}
}

func TestWorkspaceCommitEnteringChangesClampsStaleCursor(t *testing.T) {
	m := newWithWorkspace(fakeProvider{}, 0, &fakeWorkspace{})
	m.active, m.width, m.height = workspaceCommitTab, 100, 24
	m.workspaceStatus.Unstaged = []worktree.Change{{Path: "only.go", Code: 'M'}}
	m.workspaceCursor = 99
	m.focus = focusCommitMessage
	m.commitMessage.Focus()

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = updated.(Model)
	changes := m.filteredWorkspaceChanges()
	if m.focus != focusWorkspaceList || m.workspaceCursor != 0 || len(changes) != 1 || !m.dashboardChangeSelected(changes[0]) {
		t.Fatalf("Changes entry did not clamp selection: focus=%v cursor=%d changes=%d", m.focus, m.workspaceCursor, len(changes))
	}
}

func TestWorkspaceCommitDiffUsesDashboardWidthOnLoadAndResize(t *testing.T) {
	workspace := &fakeWorkspace{diffs: map[string]worktree.Diff{"main.go": {Path: "main.go", Patch: "@@ -1 +1 @@\n-old\n+new"}}}
	m := newWithWorkspace(fakeProvider{}, 0, workspace)
	m.active, m.width, m.height = workspaceCommitTab, 100, 24
	m.workspaceStatus.Unstaged = []worktree.Change{{Path: "main.go", Code: 'M'}}
	m.workspaceCursor = 0

	loaded, load := m.loadSelectedWorkspaceItem()
	m = loaded
	if load == nil {
		t.Fatal("initial File Diff load command is nil")
	}
	result := load().(workspaceResultMsg)
	if result.width != m.workspaceDiffRenderWidth() {
		t.Fatalf("initial diff width = %d, want dashboard width %d", result.width, m.workspaceDiffRenderWidth())
	}
	updated, _ := m.Update(result)
	m = updated.(Model)
	oldWidth := m.workspaceDiffWidth

	updated, rerender := m.Update(tea.WindowSizeMsg{Width: 130, Height: 24})
	m = updated.(Model)
	if rerender == nil {
		t.Fatal("terminal resize did not rerender File Diff")
	}
	resized := rerender().(workspaceResultMsg)
	if resized.width != m.workspaceDiffRenderWidth() || resized.width == oldWidth {
		t.Fatalf("resized diff width = %d, dashboard=%d old=%d", resized.width, m.workspaceDiffRenderWidth(), oldWidth)
	}
}

func TestWorkspaceCommitRemoteButtonsRunActions(t *testing.T) {
	workspace := &fakeWorkspace{}
	m := newWithWorkspace(fakeProvider{}, 0, workspace)
	m.active, m.width, m.height = workspaceCommitTab, 90, 24
	m.workspaceRemote = worktree.RemoteState{Available: true, Ahead: 1, Behind: 1}
	left, buttonY, _ := m.commitDashboardGeometry()
	buttonWidth := (left - 4) / 3

	updated, cmd := m.Update(tea.MouseMsg{X: 1 + buttonWidth/2, Y: buttonY, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
	m = updated.(Model)
	if cmd == nil {
		t.Fatal("Pull button did not start an action")
	}
	updated, _ = m.Update(cmd())
	m = updated.(Model)
	if workspace.pulls != 1 {
		t.Fatalf("pull calls = %d, want 1", workspace.pulls)
	}

	m.actionBusy = false
	updated, cmd = m.Update(tea.MouseMsg{X: 1 + buttonWidth + 1 + buttonWidth/2, Y: buttonY, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
	if cmd == nil {
		t.Fatal("Push button did not start an action")
	}
	cmd()
	if workspace.pushes != 1 {
		t.Fatalf("push calls = %d, want 1", workspace.pushes)
	}
}

func TestWorkspaceCommitPanelFocusUsesAccentBorder(t *testing.T) {
	if dashboardPanelBorderStyle(true).GetForeground() != accent {
		t.Fatal("focused panel border does not use the accent color")
	}
	if dashboardPanelBorderStyle(false).GetForeground() != border {
		t.Fatal("unfocused panel border does not use the normal border color")
	}
	colors := []lipgloss.TerminalColor{pullButtonStyle.GetBackground(), pushButtonStyle.GetBackground(), dashboardCommitButtonStyle.GetBackground()}
	if colors[0] == colors[1] || colors[1] == colors[2] || colors[0] == colors[2] {
		t.Fatalf("Pull, Push, and Commit button colors are not distinct: %v", colors)
	}
}

func TestWorkspaceCommitCursorAndButtonAvailability(t *testing.T) {
	m := newWithWorkspace(fakeProvider{}, 0, &fakeWorkspace{})
	m.active, m.width, m.height = workspaceCommitTab, 100, 24
	m.focus = focusCommitMessage
	m.commitMessage.Focus()
	plain := ansi.Strip(m.View())
	if !strings.Contains(plain, "Commit message") {
		t.Fatal("focused Commit message does not render a visible cursor")
	}
	if m.commitCanPull() || m.commitCanPush() || m.commitCanCommit() {
		t.Fatal("empty Commit dashboard actions are unexpectedly enabled")
	}
	m.workspaceRemote = worktree.RemoteState{Available: true, Ahead: 2, Behind: 3}
	if !m.commitCanPull() || !m.commitCanPush() {
		t.Fatal("remote actions did not enable from ahead/behind counts")
	}
	m.commitMessage.SetValue("Ready")
	m.workspaceStatus.Staged = []worktree.Change{{Path: "main.go", Code: 'M'}}
	if !m.commitCanCommit() {
		t.Fatal("Commit did not enable with a message and staged file")
	}
	if disabledButtonStyle.GetBackground() == dashboardCommitButtonStyle.GetBackground() {
		t.Fatal("disabled button does not have a distinct background")
	}
}

func TestWorkspaceCommitRemoteStateRefreshUpdatesCounts(t *testing.T) {
	workspace := &fakeWorkspace{remote: worktree.RemoteState{Available: true, Ahead: 4, Behind: 5}}
	m := newWithWorkspace(fakeProvider{}, 0, workspace)
	m.active = workspaceCommitTab
	result := m.fetchWorkspaceRemoteStateCmd()().(workspaceRemoteResultMsg)
	updated, _ := m.Update(result)
	m = updated.(Model)
	if m.workspaceRemote.Ahead != 4 || m.workspaceRemote.Behind != 5 {
		t.Fatalf("remote counts = %#v", m.workspaceRemote)
	}
	updated, tick := m.Update(commitRemoteTickMsg(time.Now()))
	if tick == nil || updated.(Model).workspaceRemote != m.workspaceRemote {
		t.Fatal("5-second Commit remote polling was not scheduled")
	}
}

func TestWorkspaceCommitButtonSubmitsMessage(t *testing.T) {
	workspace := &fakeWorkspace{}
	m := newWithWorkspace(fakeProvider{}, 0, workspace)
	m.active, m.width, m.height = workspaceCommitTab, 120, 20
	m.workspaceStatus.Staged = []worktree.Change{{Path: "main.go", Code: 'M'}}
	m.commitMessage.SetValue("Commit from button")
	left, buttonY, _ := m.commitDashboardGeometry()
	updated, cmd := m.Update(tea.MouseMsg{X: left / 2, Y: buttonY, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
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

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlS})
	m = updated.(Model)
	if m.actionBusy || len(m.workspace.(*fakeWorkspace).commits) != 0 || m.status != "enter a commit message" {
		t.Fatalf("empty message: busy=%t status=%q", m.actionBusy, m.status)
	}
	m.commitMessage.SetValue("Nothing staged")
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlS})
	m = updated.(Model)
	if m.actionBusy || len(m.workspace.(*fakeWorkspace).commits) != 0 || m.status != "stage changes before committing" {
		t.Fatalf("unstaged commit: busy=%t status=%q", m.actionBusy, m.status)
	}
}

func TestWorkspaceSideBySideDiffDistinguishesBlankLinesFromGaps(t *testing.T) {
	diff := worktree.Diff{Path: "main.go", Old: []byte("value\n"), New: []byte("value\n\n"), Patch: "@@ -1 +1,2 @@\n value\n+\n"}
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
	diff := worktree.Diff{Path: "main.go", Old: []byte("public value"), New: []byte("private value"), Patch: "@@ -1 +1 @@\n-public value\n+private value\n"}
	rendered := renderWorkspaceDiff(diff, 100)
	plain := strings.Split(ansi.Strip(rendered), "\n")
	var removal, addition int = -1, -1
	for index, line := range plain {
		if strings.Contains(line, "1 - public value") {
			removal = index
		}
		if strings.Contains(line, "1 + private value") {
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
	if rendered := renderWorkspaceDiff(diff, 99); strings.Contains(rendered, "OLD") || !strings.Contains(rendered, "-old") {
		t.Fatalf("narrow layout = %q", rendered)
	}
	if rendered := renderWorkspaceDiff(diff, 100); !strings.Contains(rendered, "OLD") || !strings.Contains(rendered, "NEW") {
		t.Fatalf("wide layout = %q", rendered)
	}
}

func TestWorkspaceDiffUsesSharedProviderRenderer(t *testing.T) {
	diff := worktree.Diff{
		Path:  "main.go",
		Patch: "@@ -7 +7 @@\n-\told value with a long suffix\n+\tnew value with a long suffix\n",
	}
	const width = 120
	workspace := strings.TrimPrefix(renderWorkspaceDiff(diff, width), kittyDeleteImage())
	file := provider.DiffFile{
		OldPath: "main.go",
		NewPath: "main.go",
		Lines:   provider.ParseUnifiedDiffLines(diff.Patch),
	}
	remote := renderDiffFile([]provider.DiffFile{file}, 0, -1, -1, width)
	if workspace != remote {
		t.Fatalf("Commit tab diff diverged from shared PR/MR renderer:\nworkspace=%q\nremote=%q", workspace, remote)
	}
}

func TestWorkspaceDiffFallsBackForLargeOneSidedChange(t *testing.T) {
	diff := worktree.Diff{
		Path:  "generated.txt",
		New:   []byte(strings.Repeat("line\n", 5_001)),
		Patch: "@@ -0,0 +1 @@\n+large change\n",
	}
	rendered := renderWorkspaceDiff(diff, 120)
	if !strings.Contains(rendered, "OLD") || !strings.Contains(rendered, "NEW") || !strings.Contains(rendered, "large change") {
		t.Fatalf("large one-sided diff did not use the shared split layout: %q", ansi.Strip(rendered))
	}
}

func TestWorkspacePreviewCanScrollWithoutChangingSelection(t *testing.T) {
	m := newWithWorkspace(fakeProvider{}, 0, &fakeWorkspace{})
	m.active = workspaceFilesTab
	m.width, m.height = 120, 16
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

func TestWrappedMarkdownPreviewCanScroll(t *testing.T) {
	m := newWithWorkspace(fakeProvider{}, 0, &fakeWorkspace{})
	m.active = workspaceFilesTab
	m.width, m.height = 48, 12
	m.workspaceEntries = []worktree.Entry{{Path: "README.md", Name: "README.md"}}
	m.workspaceFile = worktree.File{
		Path: "README.md",
		Data: []byte(strings.Repeat("wrapped markdown words need several visual rows ", 12)),
	}

	if count := m.workspacePreviewLineCount(); count <= m.workspaceListHeight() {
		t.Fatalf("rendered Markdown line count = %d, viewport height = %d", count, m.workspaceListHeight())
	}
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyPgDown})
	m = updated.(Model)
	if m.workspacePreviewOffset == 0 || m.workspaceCursor != 0 {
		t.Fatalf("Markdown preview scroll: offset=%d cursor=%d", m.workspacePreviewOffset, m.workspaceCursor)
	}
}

func TestWorkspacePreviewScrollsWithRightPaneMouseWheel(t *testing.T) {
	m := newWithWorkspace(fakeProvider{}, 0, &fakeWorkspace{})
	m.active = workspaceFilesTab
	m.width, m.height = 100, 16
	m.workspaceEntries = []worktree.Entry{{Path: "long.txt", Name: "long.txt"}}
	m.workspaceFile = worktree.File{Path: "long.txt", Data: []byte(strings.Repeat("line\n", 20))}
	leftWidth, _ := m.workspacePaneWidths()

	updated, _ := m.Update(tea.MouseMsg{
		X: leftWidth + 4, Y: 6, Button: tea.MouseButtonWheelDown, Action: tea.MouseActionPress,
	})
	m = updated.(Model)
	if m.workspacePreviewOffset != 3 || m.workspaceCursor != 0 {
		t.Fatalf("right-pane wheel: offset=%d cursor=%d", m.workspacePreviewOffset, m.workspaceCursor)
	}

	updated, _ = m.Update(tea.MouseMsg{
		X: leftWidth + 4, Y: 6, Button: tea.MouseButtonWheelUp, Action: tea.MouseActionPress,
	})
	m = updated.(Model)
	if m.workspacePreviewOffset != 0 || m.workspaceCursor != 0 {
		t.Fatalf("right-pane wheel up: offset=%d cursor=%d", m.workspacePreviewOffset, m.workspaceCursor)
	}
}

func TestCoalescedWheelScrollPreservesWorkspacePaneTarget(t *testing.T) {
	m := newWithWorkspace(fakeProvider{}, 0, &fakeWorkspace{})
	m.active = workspaceFilesTab
	m.width, m.height = 100, 12
	m.workspaceEntries = []worktree.Entry{{Path: "long.txt", Name: "long.txt"}}
	m.workspaceFile = worktree.File{Path: "long.txt", Data: []byte(strings.Repeat("line\n", 40))}
	leftWidth, _ := m.workspacePaneWidths()

	updated, _ := m.Update(WheelScrollMsg{Delta: 5, X: leftWidth + 4, Y: 6})
	m = updated.(Model)
	if m.workspacePreviewOffset != 15 || m.workspaceCursor != 0 {
		t.Fatalf("right-pane coalesced wheel: offset=%d cursor=%d", m.workspacePreviewOffset, m.workspaceCursor)
	}
}

func TestWorkspaceFileRefreshPreservesAndClampsPreviewOffset(t *testing.T) {
	workspace := &fakeWorkspace{entries: []worktree.Entry{{Path: "long.txt", Name: "long.txt"}}}
	m := newWithWorkspace(fakeProvider{}, 0, workspace)
	m.active = workspaceFilesTab
	m.width, m.height = 100, 16
	m.workspaceEntries = workspace.entries
	m.workspaceFile = worktree.File{Path: "long.txt", Data: []byte(strings.Repeat("old\n", 30))}
	m.workspacePreviewOffset = 8
	m.workspaceLoading = true
	m.workspaceEntryPending = 1

	updated, load := m.Update(workspaceResultMsg{
		request: m.workspaceEntryRequest, op: "entries", entries: workspace.entries,
	})
	m = updated.(Model)
	if load == nil || m.workspacePreviewOffset != 8 {
		t.Fatalf("same-file entries refresh: load=%v offset=%d", load != nil, m.workspacePreviewOffset)
	}
	request := m.workspacePreviewRequest
	updated, _ = m.Update(workspaceResultMsg{
		request: request, op: "file", file: worktree.File{Path: "long.txt", Data: []byte(strings.Repeat("new\n", 30))},
	})
	m = updated.(Model)
	if m.workspacePreviewOffset != 8 {
		t.Fatalf("same-file content refresh offset = %d, want 8", m.workspacePreviewOffset)
	}
	m.workspacePreviewRequest++
	m.workspacePreviewLoading = true
	updated, _ = m.Update(workspaceResultMsg{
		request: m.workspacePreviewRequest, op: "file", file: worktree.File{Path: "long.txt", Data: []byte("one\ntwo")},
	})
	m = updated.(Model)
	if m.workspacePreviewOffset != 0 {
		t.Fatalf("shortened file offset = %d, want clamped to 0", m.workspacePreviewOffset)
	}
}

func TestWorkspaceFileSelectionChangeResetsPreviewOffset(t *testing.T) {
	m := newWithWorkspace(fakeProvider{}, 0, &fakeWorkspace{})
	m.active = workspaceFilesTab
	m.workspaceEntries = []worktree.Entry{
		{Path: "first.txt", Name: "first.txt"},
		{Path: "second.txt", Name: "second.txt"},
	}
	m.workspaceFile = worktree.File{Path: "first.txt", Data: []byte(strings.Repeat("line\n", 20))}
	m.workspacePreviewOffset = 7
	m.workspaceCursor = 1

	updated, load := m.loadSelectedWorkspaceItem()
	if load == nil || updated.workspacePreviewOffset != 0 {
		t.Fatalf("new selection: load=%v offset=%d", load != nil, updated.workspacePreviewOffset)
	}
	updated.workspacePreviewOffset = 5
	updated.workspaceEntries[1].IsDir = true
	updated, load = updated.loadSelectedWorkspaceItem()
	if load != nil || updated.workspacePreviewOffset != 0 || updated.workspaceFile.Path != "" {
		t.Fatalf("directory selection: load=%v offset=%d file=%q", load != nil, updated.workspacePreviewOffset, updated.workspaceFile.Path)
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

func TestWorkspaceDividerDragUpdatesAndStopsOnRelease(t *testing.T) {
	m := newWithWorkspace(fakeProvider{}, 0, &fakeWorkspace{})
	m.active, m.width, m.height = workspaceFilesTab, 120, 20
	left, _ := m.workspacePaneWidths()

	updated, cmd := m.Update(tea.MouseMsg{X: left + 1, Y: 7, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
	m = updated.(Model)
	if cmd != nil || !m.workspaceDividerDragging {
		t.Fatalf("divider press: dragging=%t cmd=%v", m.workspaceDividerDragging, cmd != nil)
	}
	updated, _ = m.Update(tea.MouseMsg{X: 70, Y: 6, Button: tea.MouseButtonLeft, Action: tea.MouseActionMotion})
	m = updated.(Model)
	left, _ = m.workspacePaneWidths()
	if left != 69 {
		t.Fatalf("dragged left width = %d, want 69", left)
	}
	updated, _ = m.Update(tea.MouseMsg{X: 70, Y: 6, Button: tea.MouseButtonNone, Action: tea.MouseActionRelease})
	m = updated.(Model)
	if m.workspaceDividerDragging {
		t.Fatal("divider remained active after release")
	}
	updated, _ = m.Update(tea.MouseMsg{X: 40, Y: 6, Button: tea.MouseButtonLeft, Action: tea.MouseActionMotion})
	m = updated.(Model)
	if got, _ := m.workspacePaneWidths(); got != left {
		t.Fatalf("motion after release changed width to %d", got)
	}
}

func TestWorkspaceDividerDragClampsAndResizePreservesRatio(t *testing.T) {
	m := newWithWorkspace(fakeProvider{}, 0, &fakeWorkspace{})
	m.active, m.width, m.height = workspaceFilesTab, 120, 20
	left, _ := m.workspacePaneWidths()
	updated, _ := m.Update(tea.MouseMsg{X: left + 1, Y: 7, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
	m = updated.(Model)
	updated, _ = m.Update(tea.MouseMsg{X: -20, Y: 6, Button: tea.MouseButtonLeft, Action: tea.MouseActionMotion})
	m = updated.(Model)
	if left, _ := m.workspacePaneWidths(); left != workspacePaneMinWidth {
		t.Fatalf("left clamp = %d, want %d", left, workspacePaneMinWidth)
	}
	updated, _ = m.Update(tea.MouseMsg{X: 500, Y: 6, Button: tea.MouseButtonLeft, Action: tea.MouseActionMotion})
	m = updated.(Model)
	if _, right := m.workspacePaneWidths(); right != workspacePaneMinWidth {
		t.Fatalf("right clamp = %d, want %d", right, workspacePaneMinWidth)
	}
	updated, _ = m.Update(tea.MouseMsg{X: 60, Y: 6, Button: tea.MouseButtonLeft, Action: tea.MouseActionMotion})
	m = updated.(Model)
	ratio := m.workspaceSplitRatio
	updated, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 20})
	m = updated.(Model)
	left, right := m.workspacePaneWidths()
	if m.workspaceDividerDragging || left < workspacePaneMinWidth || right < workspacePaneMinWidth {
		t.Fatalf("resize state: dragging=%t left=%d right=%d", m.workspaceDividerDragging, left, right)
	}
	if got := float64(left) / float64(m.width-3); got < ratio-0.02 || got > ratio+0.02 {
		t.Fatalf("resize ratio = %.3f, want near %.3f", got, ratio)
	}
	if left, right := workspacePaneWidthsAt(12, ratio); left < 1 || right < 1 || left+3+right > 12 {
		t.Fatalf("narrow layout left=%d right=%d", left, right)
	}
}

func TestWorkspaceDividerDoesNotStealListClicks(t *testing.T) {
	m := newWithWorkspace(fakeProvider{}, 0, &fakeWorkspace{})
	m.active, m.width, m.height = workspaceFilesTab, 120, 20
	m.workspaceEntries = []worktree.Entry{{Path: "one.txt", Name: "one.txt"}, {Path: "two.txt", Name: "two.txt"}}
	m.workspaceLoading = false

	updated, cmd := m.Update(tea.MouseMsg{X: 2, Y: 8, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
	m = updated.(Model)
	if m.workspaceDividerDragging || m.workspaceCursor != 1 || cmd == nil {
		t.Fatalf("list click conflict: dragging=%t cursor=%d cmd=%v", m.workspaceDividerDragging, m.workspaceCursor, cmd != nil)
	}
}

func TestWorkspaceFilesStartsWithFixedFileListWidth(t *testing.T) {
	m := newWithWorkspace(fakeProvider{}, 0, &fakeWorkspace{})
	m.active, m.width = workspaceFilesTab, 120
	left, right := m.workspacePaneWidths()
	if left != 42 {
		t.Fatalf("initial File List width = %d, want fixed width 42", left)
	}
	if right <= left {
		t.Fatalf("initial Preview width = %d, want wider than File List width %d", right, left)
	}
}

func TestWorkspaceDividerInvalidatesStaleImageRender(t *testing.T) {
	m := newWithWorkspace(fakeProvider{}, 0, &fakeWorkspace{})
	m.active, m.width, m.height = workspaceFilesTab, 120, 20
	m.workspaceFile = worktree.File{Path: "preview.png", Image: true, Data: []byte("not-an-image")}
	m.workspaceImageWidth = 77
	m.workspaceImageHeight = m.workspaceListHeight() - 1
	m.workspacePreviewRequest = 4
	left, _ := m.workspacePaneWidths()

	updated, _ := m.Update(tea.MouseMsg{X: left + 1, Y: 7, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
	m = updated.(Model)
	updated, cmd := m.Update(tea.MouseMsg{X: 65, Y: 6, Button: tea.MouseButtonLeft, Action: tea.MouseActionMotion})
	m = updated.(Model)
	if cmd == nil || m.workspacePreviewRequest != 5 || !m.workspacePreviewLoading {
		t.Fatalf("drag render: cmd=%v request=%d loading=%t", cmd != nil, m.workspacePreviewRequest, m.workspacePreviewLoading)
	}
	updated, _ = m.Update(workspaceResultMsg{request: 4, op: "image", file: m.workspaceFile, image: "stale", width: 77, height: m.workspaceImageHeight})
	m = updated.(Model)
	if m.workspaceImage == "stale" || !m.workspacePreviewLoading {
		t.Fatalf("stale image won: image=%q loading=%t", m.workspaceImage, m.workspacePreviewLoading)
	}
}

func TestWorkspaceDividerDoesNotRenderAGutter(t *testing.T) {
	m := newWithWorkspace(fakeProvider{}, 0, &fakeWorkspace{})
	height := 7
	rendered := m.workspaceDividerView(height)
	rows := strings.Split(rendered, "\n")
	if len(rows) != height {
		t.Fatalf("divider rows = %d, want %d", len(rows), height)
	}
	for index, row := range rows {
		if row != "" || lipgloss.Width(row) != 0 {
			t.Fatalf("divider row %d = %q, width %d", index, row, lipgloss.Width(row))
		}
	}
	m.workspaceDividerDragging = true
	if active := m.workspaceDividerView(1); active != "" {
		t.Fatalf("active divider = %q", active)
	}
}

func TestKittyImageDeletePreservesTerminalCursor(t *testing.T) {
	t.Setenv("KITTY_WINDOW_ID", "1")
	command := kittyDeleteImage()
	if !strings.HasPrefix(command, "\x1b7") || !strings.HasSuffix(command, "\x1b8") {
		t.Fatalf("Kitty image delete does not preserve the cursor: %q", command)
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
	if !strings.Contains(rendered, "safe[2J") || !strings.Contains(rendered, "next    column") {
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

func TestWorkspaceStageAndUnstageDirectoryShortcuts(t *testing.T) {
	for _, test := range []struct {
		name string
		key  rune
		path string
	}{
		{name: "stage directory", key: 's', path: "internal/tui"},
		{name: "unstage directory", key: 'u', path: "internal/tui"},
	} {
		t.Run(test.name, func(t *testing.T) {
			workspace := &fakeWorkspace{}
			m := newWithWorkspace(fakeProvider{}, 0, workspace)
			m.active = workspaceCommitTab
			if test.key == 's' {
				m.workspaceStatus.Unstaged = []worktree.Change{{Path: test.path + "/view.go", Code: 'M'}, {Path: test.path + "/model.go", Code: 'M'}}
			} else {
				m.workspaceStatus.Staged = []worktree.Change{{Path: test.path + "/view.go", Code: 'M'}, {Path: test.path + "/model.go", Code: 'M'}}
			}
			changes := m.filteredWorkspaceChanges()
			for index, change := range changes {
				if change.displayPath() == test.path {
					m.workspaceCursor = index
					break
				}
			}

			updated, cmd := m.Update(key(test.key))
			m = updated.(Model)
			if cmd == nil || !m.actionBusy {
				t.Fatal("directory shortcut did not start an action")
			}
			result := cmd().(workspaceActionResultMsg)
			if result.err != nil {
				t.Fatalf("directory action failed: %v", result.err)
			}
			if test.key == 's' && (len(workspace.staged) != 1 || workspace.staged[0] != test.path) {
				t.Fatalf("stage calls = %#v", workspace.staged)
			}
			if test.key == 'u' && (len(workspace.unstaged) != 1 || workspace.unstaged[0] != test.path) {
				t.Fatalf("unstage calls = %#v", workspace.unstaged)
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
