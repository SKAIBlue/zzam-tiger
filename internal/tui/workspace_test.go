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
	staged       []string
	unstaged     []string
	stageAll     int
	unstageAll   int
	commits      []string
	history      []worktree.Commit
	branches     []worktree.Branch
	branchCalls  []string
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

func TestGraphKeyboardFileNavigationAndSearchHighlight(t *testing.T) {
	m := New(fakeProvider{}, 0)
	m.active = 4
	m.width, m.height, m.loadingList = 100, 20, false
	m.items[provider.Commits] = []provider.Item{
		{ID: "one", Title: "Fix CAFÉ cafe", Paths: []string{"docs/cafe.md", "main.go"}},
		{ID: "two", Title: "next", Paths: []string{"next.go"}},
	}
	m.graphQuery.SetValue("café")
	if view := ansi.Strip(m.View()); !strings.Contains(view, "Fix CAFÉ cafe") {
		t.Fatalf("graph search did not retain case-insensitive Unicode match: %q", view)
	}
	m.graphQuery.SetValue("")
	update, _ := m.Update(tea.KeyMsg{Type: tea.KeyRight})
	m = update.(Model)
	if m.graphDepth != graphFileDepth || m.graphFile != 0 {
		t.Fatalf("right = depth %v file %d", m.graphDepth, m.graphFile)
	}
	update, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = update.(Model)
	if m.graphFile != 1 {
		t.Fatalf("down = file %d, want 1", m.graphFile)
	}
	update, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = update.(Model)
	if m.graphDepth != graphCommitDepth || m.cursor[provider.Commits] != 1 {
		t.Fatalf("last-file down = depth %v cursor %d", m.graphDepth, m.cursor[provider.Commits])
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
	if !strings.Contains(graphView, "Zzam Tiger") || !strings.Contains(graphView, "Graph") || !strings.Contains(graphView, "All") || !strings.Contains(graphView, "Search:") {
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
	if m.focus != focusGraphCommits {
		t.Fatalf("filter down focus = %v, want graph commits", m.focus)
	}
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyUp})
	m = updated.(Model)
	if m.focus != focusGraphFilters || !m.graphFilter.Focused() {
		t.Fatalf("first commit up did not focus Graph search: focus=%v input=%t", m.focus, m.graphFilter.Focused())
	}
	m.graphFilter.Blur()
	m.focus, m.graphDepth, m.graphFile = focusListItems, graphFileDepth, 0
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyUp})
	m = updated.(Model)
	if m.focus != focusGraphFilters || !m.graphFilter.Focused() || m.graphDepth != graphCommitDepth {
		t.Fatalf("first Graph file up did not focus search: focus=%v input=%t depth=%v", m.focus, m.graphFilter.Focused(), m.graphDepth)
	}

	m.active = workspaceCommitTab
	m.focus = focusTabs
	m.workspaceStatus = worktree.Status{Unstaged: []worktree.Change{{Path: "one.go", Code: 'M'}}}
	m.workspaceDiffRows = strings.Split("header\n"+strings.Repeat("changed line\n", 30), "\n")
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = updated.(Model)
	if m.focus != focusFileFilter || !m.fileFilter.Focused() {
		t.Fatalf("commit tab down did not focus search: focus=%v input=%t", m.focus, m.fileFilter.Focused())
	}
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = updated.(Model)
	if m.focus != focusCommitMessage || !m.commitMessage.Focused() {
		t.Fatalf("search down did not focus message: focus=%v input=%t", m.focus, m.commitMessage.Focused())
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
		dir   bool
		depth int
	}{
		{path: "README.md"},
		{path: "cmd", dir: true},
		{path: "cmd/app", dir: true, depth: 1},
		{path: "cmd/app/main.go", depth: 2},
		{path: "cmd/app/run.go", depth: 2},
	}
	if len(changes) != len(want) {
		t.Fatalf("tree length = %d, want %d: %#v", len(changes), len(want), changes)
	}
	for index, expected := range want {
		got := changes[index]
		if got.displayPath() != expected.path || got.isDir != expected.dir || got.depth != expected.depth {
			t.Fatalf("tree item %d = path %q dir=%t depth=%d, want %#v", index, got.displayPath(), got.isDir, got.depth, expected)
		}
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

func TestWorkspaceCommitFolderClickCollapsesAndExpandsChildren(t *testing.T) {
	m := newWithWorkspace(fakeProvider{}, 0, &fakeWorkspace{})
	m.active, m.width, m.height = workspaceCommitTab, 120, 20
	m.workspaceStatus.Unstaged = []worktree.Change{
		{Path: "cmd/app/main.go", Code: 'M'},
		{Path: "cmd/app/run.go", Code: 'M'},
		{Path: "README.md", Code: 'M'},
	}
	m.workspaceLoading = false
	if got := len(m.filteredWorkspaceChanges()); got != 5 {
		t.Fatalf("expanded changes = %d, want 5", got)
	}

	// The group header is row 5 and the first sorted item (README.md) is row 6,
	// so the cmd directory is the next clickable row.
	updated, cmd := m.Update(tea.MouseMsg{X: 4, Y: 7, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
	m = updated.(Model)
	if cmd != nil || !m.workspaceChangeCollapsed[workspaceChangeExpansionKey(false, "cmd")] {
		t.Fatalf("collapse click: cmd=%v collapsed=%t", cmd != nil, m.workspaceChangeCollapsed[workspaceChangeExpansionKey(false, "cmd")])
	}
	changes := m.filteredWorkspaceChanges()
	if len(changes) != 2 || changes[0].path != "README.md" || changes[1].path != "cmd" {
		t.Fatalf("collapsed changes = %#v", changes)
	}
	if rendered := m.workspaceList(50, 10); !strings.Contains(rendered, "▸ cmd") {
		t.Fatalf("collapsed folder icon missing: %q", rendered)
	}

	updated, cmd = m.Update(tea.MouseMsg{X: 4, Y: 7, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
	m = updated.(Model)
	if cmd != nil || m.workspaceChangeCollapsed[workspaceChangeExpansionKey(false, "cmd")] || len(m.filteredWorkspaceChanges()) != 5 {
		t.Fatalf("expand click: cmd=%v collapsed=%t changes=%d", cmd != nil, m.workspaceChangeCollapsed[workspaceChangeExpansionKey(false, "cmd")], len(m.filteredWorkspaceChanges()))
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
	m.width, m.height = 100, 12
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
	m.width, m.height = 100, 12
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

	updated, cmd := m.Update(tea.MouseMsg{X: left + 1, Y: 6, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
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
	updated, _ := m.Update(tea.MouseMsg{X: left + 1, Y: 6, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
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

	updated, cmd := m.Update(tea.MouseMsg{X: 2, Y: 6, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
	m = updated.(Model)
	if m.workspaceDividerDragging || m.workspaceCursor != 1 || cmd == nil {
		t.Fatalf("list click conflict: dragging=%t cursor=%d cmd=%v", m.workspaceDividerDragging, m.workspaceCursor, cmd != nil)
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

	updated, _ := m.Update(tea.MouseMsg{X: left + 1, Y: 6, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
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

func TestWorkspaceDividerRendersAtFullBodyHeight(t *testing.T) {
	m := newWithWorkspace(fakeProvider{}, 0, &fakeWorkspace{})
	height := 7
	rendered := m.workspaceDividerView(height)
	rows := strings.Split(rendered, "\n")
	if len(rows) != height {
		t.Fatalf("divider rows = %d, want %d", len(rows), height)
	}
	for index, row := range rows {
		if !strings.Contains(row, "┃") || lipgloss.Width(row) != 3 {
			t.Fatalf("divider row %d = %q, width %d", index, row, lipgloss.Width(row))
		}
	}
	m.workspaceDividerDragging = true
	if active := m.workspaceDividerView(1); !strings.Contains(active, " ┃ ") || strings.Contains(active, "━") {
		t.Fatalf("active divider = %q", active)
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
				m.workspaceStatus.Unstaged = []worktree.Change{{Path: test.path + "/view.go", Code: 'M'}}
			} else {
				m.workspaceStatus.Staged = []worktree.Change{{Path: test.path + "/view.go", Code: 'M'}}
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
