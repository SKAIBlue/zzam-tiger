package tui

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"

	"github.com/SKAIBlue/zzam-tiger/internal/provider"
	"github.com/SKAIBlue/zzam-tiger/internal/worktree"
)

type screen int

const (
	workspaceCommitTab = iota
	workspaceFilesTab
)

const (
	listScreen screen = iota
	detailScreen
	labelScreen
	diffScreen
	commentScreen
)

type commentMode int

const (
	generalComment commentMode = iota
	generalReview
	inlineReview
	reviewReply
)

var kinds = []provider.Kind{
	provider.PullRequests,
	provider.Issues,
	provider.Milestones,
	provider.Branches,
	provider.Commits,
	provider.CIRuns,
}

var workspaceKinds = []provider.Kind{
	provider.Commits,
	provider.Branches,
	provider.PullRequests,
	provider.Issues,
	provider.Milestones,
	provider.CIRuns,
}

var localWorkspaceKinds = []provider.Kind{
	provider.Commits,
	provider.Branches,
}

type listResultMsg struct {
	request uint64
	kind    provider.Kind
	filter  string
	items   []provider.Item
	err     error
}

type detailResultMsg struct {
	request uint64
	item    provider.Item
	detail  provider.Detail
	err     error
}

type actionResultMsg struct {
	action string
	err    error
}

type tickMsg time.Time

type workspaceWatchMsg struct {
	path   string
	err    error
	closed bool
}

type workspaceDebounceMsg uint64

type workspaceWatcher interface {
	Updates() <-chan worktree.WatchUpdate
	Close() error
}

type updateResultMsg struct {
	available bool
}

type installFinishedMsg struct{ err error }
type restartFinishedMsg struct{ err error }

type updateChecker func(context.Context, string) (string, bool, error)
type installCommand func() *exec.Cmd
type restartCommand func() *exec.Cmd

type Model struct {
	backend   provider.Provider
	remoteErr error
	localErr  error
	workspace workspaceClient
	filesOnly bool
	watcher   workspaceWatcher
	refresh   time.Duration

	width  int
	height int
	screen screen
	active int

	filterIndex map[provider.Kind]int
	items       map[provider.Kind][]provider.Item
	cursor      map[provider.Kind]int
	offset      map[provider.Kind]int

	selected         provider.Item
	detail           provider.Detail
	viewport         viewport.Model
	labels           textinput.Model
	comment          textarea.Model
	fileFilter       textinput.Model
	commitMessage    textinput.Model
	graphFilter      textinput.Model
	graphAuthorScope int // 0 all, 1 mine, 2 others

	workspaceEntries         []worktree.Entry
	workspaceFile            worktree.File
	workspaceImage           string
	workspaceImageWidth      int
	workspaceImageHeight     int
	workspaceStatus          worktree.Status
	workspaceDiff            worktree.Diff
	workspaceDiffRows        []string
	workspaceDiffWidth       int
	workspaceCursor          int
	workspaceOffset          int
	workspacePreviewOffset   int
	workspacePendingPath     string
	workspaceRequest         uint64
	workspaceEntryRequest    uint64
	workspaceEntryPending    int
	workspacePreviewRequest  uint64
	workspaceLoading         bool
	workspacePreviewLoading  bool
	workspaceExpanded        map[string]bool
	workspaceLoaded          map[string]bool
	workspaceChangeCollapsed map[string]bool
	workspaceSplitRatio      float64
	workspaceDividerDragging bool
	workspaceWatchGeneration uint64
	workspaceWatchPending    bool
	workspaceWatcherErr      error
	workspaceDebounce        time.Duration

	commentMode      commentMode
	commentItem      provider.Item
	commentKind      provider.Kind
	commentTarget    provider.ReviewTarget
	commentTargetSet bool
	commentThread    provider.ReviewThreadTarget
	commentOrigin    screen
	diffFile         int
	diffLine         int
	diffAnchor       int
	diffDragging     bool
	detailDiffActive bool
	selectedReview   int

	listRequest   uint64
	detailRequest uint64
	loadingList   bool
	loadingDetail bool
	actionBusy    bool
	lastUpdated   time.Time
	status        string
	err           error

	currentVersion  string
	updateAvailable bool
	checkUpdate     updateChecker
	installUpdate   installCommand
	restartUpdate   restartCommand
}

func New(backend provider.Provider, refresh time.Duration) Model {
	labels := textinput.New()
	labels.Prompt = "Labels (comma separated): "
	labels.CharLimit = 500
	labels.Width = 50
	comment := textarea.New()
	comment.Placeholder = "Write Markdown…"
	comment.ShowLineNumbers = false
	comment.SetWidth(66)
	comment.SetHeight(8)
	fileFilter := textinput.New()
	fileFilter.Prompt = "Filter files: "
	fileFilter.Placeholder = "type a path substring"
	fileFilter.CharLimit = 300
	commitMessage := textinput.New()
	commitMessage.Placeholder = "Commit message"
	commitMessage.CharLimit = 1000
	graphFilter := textinput.New()
	graphFilter.Prompt = "Search commits: "
	graphFilter.Placeholder = "message, ref, path, or author"
	graphFilter.CharLimit = 300

	model := Model{
		backend:        backend,
		refresh:        refresh,
		filterIndex:    make(map[provider.Kind]int),
		items:          make(map[provider.Kind][]provider.Item),
		cursor:         make(map[provider.Kind]int),
		offset:         make(map[provider.Kind]int),
		viewport:       viewport.New(80, 20),
		labels:         labels,
		comment:        comment,
		fileFilter:     fileFilter,
		commitMessage:  commitMessage,
		graphFilter:    graphFilter,
		diffLine:       -1,
		diffAnchor:     -1,
		selectedReview: -1,
		listRequest:    1,
		loadingList:    true,
		lastUpdated:    time.Time{},
	}
	return model
}

// WithRemoteUnavailable keeps the TUI usable while disabling remote operations.
func (m Model) WithRemoteUnavailable(err error) Model {
	m.remoteErr = err
	m.loadingList = false
	return m
}

// WithLocalUnavailable records why local Git tabs are not present.
func (m Model) WithLocalUnavailable(err error) Model {
	m.localErr = err
	return m
}

// NewWithWorktree enables the local Files and Commit tabs before the remote
// provider tabs. New remains available for embedders that only need remote UI.
func NewWithWorktree(backend provider.Provider, refresh time.Duration, workspace *worktree.Client) Model {
	model := newWithWorkspace(backend, refresh, workspace)
	watcher, err := worktree.NewWatcher(workspace.Root())
	if err != nil {
		model.workspaceWatcherErr = fmt.Errorf("filesystem watcher unavailable (manual refresh remains available): %w", err)
		return model
	}
	model.watcher = watcher
	return model
}

// NewFilesOnly opens a filesystem browser without requiring a Git repository.
func NewFilesOnly(workspace *worktree.Client) Model {
	model := newWithWorkspace(nil, 0, workspace)
	model.filesOnly = true
	model.active = workspaceFilesTab
	watcher, err := worktree.NewWatcher(workspace.Root())
	if err != nil {
		model.workspaceWatcherErr = fmt.Errorf("filesystem watcher unavailable (manual refresh remains available): %w", err)
		return model
	}
	model.watcher = watcher
	return model
}

// WithUpdates enables the non-blocking release check and installer action.
func (m Model) WithUpdates(current string, checker func(context.Context, string) (string, bool, error), installer func() *exec.Cmd) Model {
	m.currentVersion = current
	m.checkUpdate = checker
	m.installUpdate = installer
	m.restartUpdate = func() *exec.Cmd { return exec.Command("zt") }
	return m
}

func newWithWorkspace(backend provider.Provider, refresh time.Duration, workspace workspaceClient) Model {
	model := New(backend, refresh)
	model.workspace = workspace
	model.active = 0
	model.loadingList = false
	model.workspaceRequest = 1
	model.workspaceEntryRequest = 1
	model.workspaceEntryPending = 1
	model.workspacePreviewRequest = 1
	model.workspaceLoading = true
	model.workspaceExpanded = make(map[string]bool)
	model.workspaceLoaded = make(map[string]bool)
	model.workspaceChangeCollapsed = make(map[string]bool)
	model.workspaceDebounce = 150 * time.Millisecond
	return model
}

// Close releases filesystem watcher resources. It is safe to call more than once.
func (m Model) Close() error {
	if m.watcher != nil {
		return m.watcher.Close()
	}
	return nil
}

func (m Model) Init() tea.Cmd {
	var initial tea.Cmd
	if m.localTab() {
		request := m.workspaceRequest
		if m.workspaceFilesActive() {
			request = m.workspaceEntryRequest
		}
		initial = m.fetchWorkspaceCmd(request)
	} else if m.remoteErr == nil {
		initial = m.fetchListCmd(m.listRequest, m.kind(), m.filter())
	}
	commands := []tea.Cmd{}
	if initial != nil {
		commands = append(commands, initial)
	}
	if m.watcher != nil {
		commands = append(commands, waitWorkspaceWatchCmd(m.watcher))
	}
	if m.checkUpdate != nil {
		commands = append(commands, m.checkUpdateCmd())
	}
	if m.refresh > 0 && m.remoteErr == nil {
		commands = append(commands, tickCmd(m.refresh))
	}
	return tea.Batch(commands...)
}

func (m Model) checkUpdateCmd() tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, available, err := m.checkUpdate(ctx, m.currentVersion)
		if err != nil {
			return updateResultMsg{}
		}
		return updateResultMsg{available: available}
	}
}

func (m Model) kind() provider.Kind {
	index := m.active
	activeKinds := kinds
	if m.workspace != nil {
		index -= m.localTabCount()
		activeKinds = m.workspaceKinds()
	}
	if index < 0 || index >= len(activeKinds) {
		return provider.PullRequests
	}
	return activeKinds[index]
}

func (m Model) workspaceKinds() []provider.Kind {
	if m.remoteErr != nil || m.backend == nil {
		return localWorkspaceKinds
	}
	return workspaceKinds
}

func (m Model) localGitList(kind provider.Kind) bool {
	return m.workspace != nil && (kind == provider.Commits || kind == provider.Branches) && m.remoteErr != nil
}

func (m Model) localTab() bool {
	return m.workspace != nil && (m.active < m.localTabCount() || m.filesOnly)
}

func (m Model) localTabCount() int {
	if m.filesOnly {
		return 1
	}
	return 2
}

func (m Model) workspaceFilesActive() bool {
	return m.active == workspaceFilesTab || m.filesOnly
}

func (m Model) workspaceCommitActive() bool {
	return !m.filesOnly && m.active == workspaceCommitTab
}

func (m Model) tabCount() int {
	if m.workspace != nil {
		if m.filesOnly {
			return 1
		}
		return len(m.workspaceKinds()) + m.localTabCount()
	}
	return len(kinds)
}

func (m Model) filter() provider.Filter {
	if m.remoteErr != nil {
		return provider.Filter{}
	}
	filters := m.backend.Filters(m.kind())
	index := m.filterIndex[m.kind()]
	if index < 0 || index >= len(filters) {
		index = 0
	}
	return filters[index]
}

func tickCmd(interval time.Duration) tea.Cmd {
	return tea.Tick(interval, func(t time.Time) tea.Msg { return tickMsg(t) })
}

func waitWorkspaceWatchCmd(watcher workspaceWatcher) tea.Cmd {
	return func() tea.Msg {
		update, ok := <-watcher.Updates()
		if !ok {
			return workspaceWatchMsg{closed: true}
		}
		return workspaceWatchMsg{path: update.Path, err: update.Err}
	}
}

func workspaceDebounceCmd(generation uint64, delay time.Duration) tea.Cmd {
	return tea.Tick(delay, func(time.Time) tea.Msg { return workspaceDebounceMsg(generation) })
}

func (m Model) fetchListCmd(request uint64, kind provider.Kind, filter provider.Filter) tea.Cmd {
	workspace := m.workspace
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if m.localGitList(kind) || workspace != nil && kind == provider.Commits {
			if kind == provider.Branches {
				branches, err := workspace.Branches(ctx)
				items := make([]provider.Item, 0, len(branches))
				for _, branch := range branches {
					state := "local"
					if branch.Remote {
						state = "remote"
					}
					meta := shortCommitSHA(branch.SHA)
					if branch.Head {
						meta = "HEAD · " + meta
					}
					items = append(items, provider.Item{ID: branch.Name, Title: branch.Name, State: state, Author: branch.Author, UpdatedAt: branch.UpdatedAt, Meta: meta})
				}
				return listResultMsg{request: request, kind: kind, filter: filter.Value, items: items, err: err}
			}
			commits, err := workspace.History(ctx, 200)
			name, email := "", ""
			if identities, ok := workspace.(interface {
				AuthorIdentity(context.Context) (string, string, error)
			}); ok {
				name, email, _ = identities.AuthorIdentity(ctx)
			}
			items := make([]provider.Item, 0, len(commits))
			for _, commit := range commits {
				refs := make([]provider.CommitRef, 0, len(commit.Refs))
				for _, ref := range commit.Refs {
					refs = append(refs, provider.CommitRef{Name: ref.Name, Remote: ref.Remote, Head: ref.Head, Tag: ref.Tag})
				}
				search := strings.Join(append([]string{commit.Subject, commit.Author, commit.AuthorEmail}, commit.Paths...), "\x00")
				for _, ref := range refs {
					search += "\x00" + ref.Name
				}
				mine := (email != "" && strings.EqualFold(strings.TrimSpace(email), strings.TrimSpace(commit.AuthorEmail))) || (email == "" && name != "" && strings.EqualFold(strings.TrimSpace(name), strings.TrimSpace(commit.Author)))
				if !m.graphCommitMatches(search, mine) {
					continue
				}
				items = append(items, provider.Item{
					ID: commit.SHA, Title: commit.Subject, State: "commit", Author: commit.Author,
					AuthorEmail: commit.AuthorEmail, SearchText: search, AssignedToMe: mine, UpdatedAt: commit.AuthoredAt, Meta: shortCommitSHA(commit.SHA), Parents: commit.Parents, Refs: refs,
				})
			}
			return listResultMsg{request: request, kind: kind, filter: filter.Value, items: items, err: err}
		}
		items, err := m.backend.List(ctx, kind, filter)
		return listResultMsg{request: request, kind: kind, filter: filter.Value, items: items, err: err}
	}
}

func (m Model) graphCommitMatches(search string, mine bool) bool {
	if m.workspace == nil || m.kind() != provider.Commits {
		return true
	}
	query := strings.ToLower(strings.TrimSpace(m.graphFilter.Value()))
	if query != "" && !strings.Contains(strings.ToLower(search), query) {
		return false
	}
	return m.graphAuthorScope == 0 || (m.graphAuthorScope == 1 && mine) || (m.graphAuthorScope == 2 && !mine)
}

func shortCommitSHA(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}

func (m Model) fetchDetailCmd(request uint64, kind provider.Kind, item provider.Item) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		detail, err := m.backend.Detail(ctx, kind, item)
		return detailResultMsg{request: request, item: item, detail: detail, err: err}
	}
}

func (m Model) actionCmd(name string, run func(context.Context) error) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		return actionResultMsg{action: name, err: run(ctx)}
	}
}

func (m Model) startGraphAction(key string) tea.Cmd {
	items := m.items[provider.Commits]
	index := m.cursor[provider.Commits]
	if index < 0 || index >= len(items) {
		m.status = "select a commit first"
		return nil
	}
	sha := items[index].ID
	actions, ok := m.workspace.(interface {
		Checkout(context.Context, string) error
		CherryPick(context.Context, string) error
		ResetSoft(context.Context, string) error
		ResetHard(context.Context, string) error
		Revert(context.Context, string) error
	})
	if !ok {
		m.status = "Graph actions are unavailable for this workspace"
		return nil
	}
	m.actionBusy = true
	var name string
	var run func(context.Context) error
	switch key {
	case "o":
		name, run = "checkout detached HEAD at "+shortCommitSHA(sha), func(ctx context.Context) error { return actions.Checkout(ctx, sha) }
	case "p":
		name, run = "cherry-pick "+shortCommitSHA(sha), func(ctx context.Context) error { return actions.CherryPick(ctx, sha) }
	case "z":
		name, run = "soft reset to "+shortCommitSHA(sha), func(ctx context.Context) error { return actions.ResetSoft(ctx, sha) }
	case "Z":
		name, run = "HARD reset to "+shortCommitSHA(sha), func(ctx context.Context) error { return actions.ResetHard(ctx, sha) }
	case "v":
		name, run = "revert "+shortCommitSHA(sha), func(ctx context.Context) error { return actions.Revert(ctx, sha) }
	}
	m.status = name + " running…"
	return m.actionCmd(name, run)
}

func (m Model) startListLoad() (Model, tea.Cmd) {
	if m.remoteErr != nil && !(m.workspace != nil && (m.kind() == provider.Commits || m.kind() == provider.Branches)) {
		m.loadingList = false
		return m, nil
	}
	if m.loadingList {
		return m, nil
	}
	m.listRequest++
	m.loadingList = true
	m.err = nil
	return m, m.fetchListCmd(m.listRequest, m.kind(), m.filter())
}

func (m Model) startDetailLoad() (Model, tea.Cmd) {
	if m.remoteErr != nil {
		return m, nil
	}
	if m.loadingDetail || m.selected.ID == "" {
		return m, nil
	}
	m.detailRequest++
	m.loadingDetail = true
	m.err = nil
	return m, m.fetchDetailCmd(m.detailRequest, m.kind(), m.selected)
}

func (m Model) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := message.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.workspaceDividerDragging = false
		m.resizeViewport()
		if m.screen == diffScreen || m.commentUsesDiffBackground() {
			m.setDiffContent()
		} else if m.detail.Item.ID != "" {
			m.setDetailContent()
		}
		if m.localTab() && !m.workspacePreviewLoading && m.workspaceFilesActive() && m.workspaceFile.Image {
			width, height := m.workspaceImageDimensions()
			if width != m.workspaceImageWidth || height != m.workspaceImageHeight {
				m.workspacePreviewRequest++
				m.workspacePreviewLoading = true
				return m, m.renderWorkspaceImageCmd(m.workspacePreviewRequest, m.workspaceFile, width, height)
			}
		}
		if m.localTab() && !m.workspacePreviewLoading && m.workspaceCommitActive() && m.workspaceDiff.Path != "" {
			_, width := m.workspacePaneWidths()
			if width != m.workspaceDiffWidth {
				m.workspacePreviewRequest++
				m.workspacePreviewLoading = true
				return m, m.renderWorkspaceDiffCmd(m.workspacePreviewRequest, m.workspaceDiff, width)
			}
		}
		return m, nil

	case workspaceResultMsg:
		return m.handleWorkspaceResult(msg)

	case workspaceActionResultMsg:
		return m.handleWorkspaceActionResult(msg)

	case updateResultMsg:
		m.updateAvailable = msg.available
		return m, nil

	case installFinishedMsg:
		if msg.err != nil {
			m.updateAvailable = true
			m.status = "update failed"
			m.err = fmt.Errorf("install update: %w", msg.err)
			return m, nil
		}
		m.status = "update installed; restarting…"
		m.err = nil
		_ = m.Close()
		if m.restartUpdate == nil {
			return m, tea.Quit
		}
		return m, tea.ExecProcess(m.restartUpdate(), func(err error) tea.Msg {
			return restartFinishedMsg{err: err}
		})

	case restartFinishedMsg:
		return m, tea.Quit

	case workspaceWatchMsg:
		if msg.closed {
			if m.workspaceWatcherErr == nil {
				m.workspaceWatcherErr = fmt.Errorf("filesystem watcher stopped (manual refresh remains available)")
			}
			return m, nil
		}
		commands := []tea.Cmd{waitWorkspaceWatchCmd(m.watcher)}
		if msg.err != nil {
			m.workspaceWatcherErr = fmt.Errorf("filesystem watcher error (manual refresh remains available): %w", msg.err)
			return m, tea.Batch(commands...)
		}
		m.workspaceWatchGeneration++
		commands = append(commands, workspaceDebounceCmd(m.workspaceWatchGeneration, m.workspaceDebounce))
		return m, tea.Batch(commands...)

	case workspaceDebounceMsg:
		if uint64(msg) != m.workspaceWatchGeneration || !m.localTab() || m.actionBusy {
			return m, nil
		}
		if m.workspaceLoading {
			m.workspaceWatchPending = true
			return m, nil
		}
		return m.startWorkspaceLoad()

	case listResultMsg:
		if m.localTab() || msg.request != m.listRequest || msg.kind != m.kind() || msg.filter != m.filter().Value {
			return m, nil
		}
		m.loadingList = false
		if msg.err != nil {
			m.err = msg.err
			return m, nil
		}
		m.items[msg.kind] = msg.items
		m.clampSelection(msg.kind)
		m.lastUpdated = time.Now()
		m.err = nil
		return m, nil

	case detailResultMsg:
		if m.localTab() || msg.request != m.detailRequest || msg.item.ID != m.selected.ID || m.screen == listScreen {
			return m, nil
		}
		m.loadingDetail = false
		if msg.err != nil {
			m.err = msg.err
			if m.detail.Item.ID == "" {
				m.viewport.SetContent("")
			}
			return m, nil
		}
		if m.screen == diffScreen || m.commentUsesDiffBackground() {
			m.diffAnchor = -1
			m.diffDragging = false
		}
		m.detail = msg.detail
		m.selected = msg.detail.Item
		m.clampDiffSelection()
		if m.screen == diffScreen || m.commentUsesDiffBackground() {
			m.setDiffContent()
		} else {
			m.setDetailContent()
		}
		m.lastUpdated = time.Now()
		m.err = nil
		return m, nil

	case actionResultMsg:
		m.actionBusy = false
		if msg.err != nil {
			m.err = msg.err
			m.status = msg.action + " failed"
			if m.screen == commentScreen {
				return m, m.comment.Focus()
			}
			return m, nil
		}
		m.status = msg.action + " completed"
		m.err = nil
		if m.screen == commentScreen {
			m.comment.Blur()
			m.screen = m.commentOrigin
			if m.screen != detailScreen && m.screen != diffScreen {
				m.screen = detailScreen
			}
			m.diffAnchor = -1
			m.diffDragging = false
			m.detailDiffActive = false
		}
		m.loadingList = false
		m.loadingDetail = false
		if m.screen == listScreen {
			return m.startListLoad()
		}
		return m.startDetailLoad()

	case tickMsg:
		commands := []tea.Cmd{}
		if m.refresh > 0 && m.remoteErr == nil {
			commands = append(commands, tickCmd(m.refresh))
		}
		var refreshCmd tea.Cmd
		if !m.localTab() && m.screen == detailScreen && !m.actionBusy {
			m, refreshCmd = m.startDetailLoad()
		} else if !m.localTab() && m.screen == listScreen && !m.actionBusy {
			m, refreshCmd = m.startListLoad()
		}
		if refreshCmd != nil {
			commands = append(commands, refreshCmd)
		}
		return m, tea.Batch(commands...)

	case tea.MouseMsg:
		return m.handleMouse(msg)

	case tea.KeyMsg:
		if msg.String() == "ctrl+c" {
			return m, tea.Quit
		}
		if m.screen == labelScreen {
			return m.updateLabelInput(msg)
		}
		if m.screen == commentScreen {
			return m.updateCommentInput(msg)
		}
		if m.screen == diffScreen {
			return m.updateDiff(msg)
		}
		if m.localTab() {
			return m.updateWorkspace(msg)
		}
		if m.screen == detailScreen {
			return m.updateDetail(msg)
		}
		return m.updateList(msg)
	}

	if m.screen == commentScreen {
		var cmd tea.Cmd
		m.comment, cmd = m.comment.Update(message)
		return m, cmd
	}
	if m.screen == detailScreen || m.screen == diffScreen {
		var cmd tea.Cmd
		m.viewport, cmd = m.viewport.Update(message)
		return m, cmd
	}
	return m, nil
}

func (m Model) updateList(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.workspace != nil && m.kind() == provider.Commits && m.graphFilter.Focused() {
		if msg.String() == "esc" {
			m.graphFilter.Blur()
			return m, nil
		}
		old := m.graphFilter.Value()
		var cmd tea.Cmd
		m.graphFilter, cmd = m.graphFilter.Update(msg)
		if old != m.graphFilter.Value() {
			m.loadingList = false
			return m.startListLoad()
		}
		return m, cmd
	}
	if m.actionBusy {
		if msg.String() == "q" {
			return m, tea.Quit
		}
		return m, nil
	}
	switch msg.String() {
	case "q":
		return m, tea.Quit
	case "tab", "]":
		m.active = (m.active + 1) % m.tabCount()
		m.status = ""
		return m.startActiveTabLoad()
	case "shift+tab", "[":
		m.active = (m.active - 1 + m.tabCount()) % m.tabCount()
		m.status = ""
		return m.startActiveTabLoad()
	case "1", "2", "3", "4", "5", "6", "7", "8":
		index := int(msg.Runes[0] - '1')
		if index < m.tabCount() {
			m.active = index
			m.status = ""
			return m.startActiveTabLoad()
		}
	case "!", "@", "#", "$", "%", "^", "&", "*":
		shiftTabs := map[string]int{"!": 0, "@": 1, "#": 2, "$": 3, "%": 4, "^": 5, "&": 6, "*": 7}
		if index := shiftTabs[msg.String()]; index < m.tabCount() {
			m.active = index
			m.status = ""
			return m.startActiveTabLoad()
		}
	case "left", "h":
		if m.workspace != nil && m.kind() == provider.Commits {
			m.graphAuthorScope = (m.graphAuthorScope + 2) % 3
			m.loadingList = false
			return m.startListLoad()
		}
		return m.changeFilter(-1)
	case "right", "l":
		if m.workspace != nil && m.kind() == provider.Commits {
			m.graphAuthorScope = (m.graphAuthorScope + 1) % 3
			m.loadingList = false
			return m.startListLoad()
		}
		return m.changeFilter(1)
	case "/":
		if m.workspace != nil && m.kind() == provider.Commits {
			return m, m.graphFilter.Focus()
		}
	case "o", "p", "z", "Z", "v":
		if m.workspace != nil && m.kind() == provider.Commits {
			return m, m.startGraphAction(msg.String())
		}
		if msg.String() == "o" {
			if item, ok := m.currentListItem(); ok && m.kind() == provider.Issues {
				return m.startIssueStateAction(item, true)
			}
		}
	case "up", "k":
		m.moveCursor(-1)
	case "down", "j":
		m.moveCursor(1)
	case "home":
		m.cursor[m.kind()] = 0
		m.offset[m.kind()] = 0
	case "end":
		items := m.items[m.kind()]
		if len(items) > 0 {
			m.cursor[m.kind()] = len(items) - 1
			m.ensureCursorVisible()
		}
	case "enter":
		return m.openSelected()
	case "c", "C":
		if item, ok := m.currentListItem(); ok && m.kind() == provider.Issues {
			return m.startIssueStateAction(item, false)
		}
	case "O":
		if item, ok := m.currentListItem(); ok && m.kind() == provider.Issues {
			return m.startIssueStateAction(item, true)
		}
	case "a", "A":
		if item, ok := m.currentListItem(); ok && assignableKind(m.kind()) {
			return m.startAssignmentAction(item, true)
		}
	case "u", "U":
		if item, ok := m.currentListItem(); ok && assignableKind(m.kind()) {
			return m.startAssignmentAction(item, false)
		}
	case "x", "X":
		if item, ok := m.currentListItem(); ok && m.kind() == provider.CIRuns {
			return m.startRunAction(item, false)
		}
	case "R":
		if item, ok := m.currentListItem(); ok && m.kind() == provider.CIRuns {
			return m.startRunAction(item, true)
		}
	case "r":
		m.loadingList = false
		return m.startListLoad()
	}
	return m, nil
}

func (m Model) updateDetail(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.actionBusy {
		if msg.String() == "q" {
			return m, tea.Quit
		}
		return m, nil
	}
	switch msg.String() {
	case "esc":
		m.screen = listScreen
		m.detail = provider.Detail{}
		m.loadingList = false
		return m.startListLoad()
	case "q":
		return m, tea.Quit
	case "up", "k":
		m.viewport.LineUp(1)
	case "down", "j":
		m.viewport.LineDown(1)
	case "pgup", "ctrl+u":
		m.viewport.HalfViewUp()
	case "pgdown", "ctrl+d":
		m.viewport.HalfViewDown()
	case "home", "g":
		m.viewport.GotoTop()
	case "end", "G":
		m.viewport.GotoBottom()
	case "r":
		m.loadingDetail = false
		return m.startDetailLoad()
	case "n", "N":
		if (m.kind() == provider.PullRequests || m.kind() == provider.Issues || m.kind() == provider.Commits) && m.detailReady() {
			return m.openCommentEditor(generalComment)
		}
	case "R":
		if m.kind() == provider.PullRequests && m.detailReady() {
			return m.openCommentEditor(generalReview)
		}
		if m.kind() == provider.CIRuns && m.detailReady() {
			return m.startRunAction(m.selected, true)
		}
	case "d", "D":
		if diffCommentableKind(m.kind()) && m.detailReady() {
			m.screen = diffScreen
			m.clampDiffSelection()
			m.setDiffContent()
			return m, nil
		}
	case "m", "M":
		if m.kind() == provider.PullRequests && m.detailReady() && m.selected.HeadSHA != "" && !m.actionBusy {
			m.actionBusy = true
			m.status = "merging…"
			item := m.selected
			return m, m.actionCmd("merge", func(ctx context.Context) error { return m.backend.Merge(ctx, item) })
		}
	case "c", "C":
		if m.kind() == provider.Issues && m.detailReady() && !m.actionBusy {
			return m.startIssueStateAction(m.selected, false)
		}
	case "o", "O":
		if m.kind() == provider.Issues && m.detailReady() && !m.actionBusy {
			return m.startIssueStateAction(m.selected, true)
		}
	case "a", "A":
		if assignableKind(m.kind()) && m.detailReady() {
			return m.startAssignmentAction(m.selected, true)
		}
	case "u", "U":
		if assignableKind(m.kind()) && m.detailReady() {
			return m.startAssignmentAction(m.selected, false)
		}
	case "l", "L":
		if m.kind() == provider.Issues && m.detailReady() && !m.actionBusy {
			m.screen = labelScreen
			m.labels.SetValue(strings.Join(m.detail.Labels, ", "))
			m.labels.CursorEnd()
			return m, m.labels.Focus()
		}
	case "x", "X":
		if m.kind() == provider.CIRuns && m.detailReady() {
			return m.startRunAction(m.selected, false)
		}
	}
	return m, nil
}

func (m Model) updateDiff(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.actionBusy {
		return m, nil
	}
	m.diffDragging = false
	switch msg.String() {
	case "esc":
		if m.diffAnchor >= 0 {
			m.diffAnchor = -1
			m.status = "range selection cancelled"
			m.setDiffContent()
			return m, nil
		}
		m.screen = detailScreen
		m.setDetailContent()
		return m, nil
	case "q":
		return m, tea.Quit
	case "left", "h":
		m.moveDiffFile(-1)
	case "right", "l":
		m.moveDiffFile(1)
	case "up", "k":
		m.moveDiffLine(-1)
	case "down", "j":
		m.moveDiffLine(1)
	case "v", "V":
		if m.kind() == provider.Commits {
			m.diffAnchor = -1
			m.status = "commit comments support one diff line"
			m.setDiffContent()
			return m, nil
		}
		if m.diffAnchor >= 0 {
			m.diffAnchor = -1
			m.status = "range selection cancelled"
		} else if m.diffLine >= 0 {
			m.diffAnchor = m.diffLine
			m.status = "range selection started"
		}
		m.setDiffContent()
	case "enter":
		if m.detailReady() {
			return m.openCommentEditor(inlineReview)
		}
	case "p", "P":
		if review, ok := m.selectedDiffReview(); ok {
			return m.openReplyEditor(review)
		}
		m.status = "no review thread is selected"
	case "x", "X":
		if review, ok := m.selectedDiffReview(); ok {
			return m.startResolveReview(review)
		}
		m.status = "no resolvable review is selected"
	case "r":
		m.loadingDetail = false
		return m.startDetailLoad()
	}
	return m, nil
}

func (m Model) detailReady() bool {
	return !m.loadingDetail && m.err == nil && m.selected.ID != "" && m.detail.Item.ID == m.selected.ID
}

func assignableKind(kind provider.Kind) bool {
	return kind == provider.PullRequests || kind == provider.Issues
}

func diffCommentableKind(kind provider.Kind) bool {
	return kind == provider.PullRequests || kind == provider.Commits
}

func (m Model) currentListItem() (provider.Item, bool) {
	items := m.items[m.kind()]
	index := m.cursor[m.kind()]
	if index < 0 || index >= len(items) {
		return provider.Item{}, false
	}
	return items[index], true
}

func (m Model) startIssueStateAction(item provider.Item, open bool) (tea.Model, tea.Cmd) {
	if m.actionBusy {
		return m, nil
	}
	m.actionBusy = true
	action := "close issue"
	m.status = "closing issue…"
	if open {
		action = "reopen issue"
		m.status = "reopening issue…"
	}
	return m, m.actionCmd(action, func(ctx context.Context) error {
		return m.backend.SetIssueState(ctx, item, open)
	})
}

func (m Model) startAssignmentAction(item provider.Item, assigned bool) (tea.Model, tea.Cmd) {
	if m.actionBusy {
		return m, nil
	}
	m.actionBusy = true
	action := "assign to me"
	m.status = "assigning to you…"
	if !assigned {
		action = "unassign from me"
		m.status = "removing your assignment…"
	}
	kind := m.kind()
	return m, m.actionCmd(action, func(ctx context.Context) error {
		return m.backend.SetAssigned(ctx, kind, item, assigned)
	})
}

func (m Model) startRunAction(item provider.Item, rerun bool) (tea.Model, tea.Cmd) {
	if m.actionBusy {
		return m, nil
	}
	m.actionBusy = true
	action := "cancel run"
	m.status = "cancelling run…"
	run := func(ctx context.Context) error { return m.backend.CancelRun(ctx, item) }
	if rerun {
		action = "rerun"
		m.status = "starting rerun…"
		run = func(ctx context.Context) error { return m.backend.Rerun(ctx, item) }
	}
	return m, m.actionCmd(action, run)
}

func (m Model) updateLabelInput(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.screen = detailScreen
		m.labels.Blur()
		return m, nil
	case "enter":
		labels := splitLabels(m.labels.Value())
		m.screen = detailScreen
		m.labels.Blur()
		m.actionBusy = true
		m.status = "updating labels…"
		item := m.selected
		return m, m.actionCmd("update labels", func(ctx context.Context) error { return m.backend.SetIssueLabels(ctx, item, labels) })
	}
	var cmd tea.Cmd
	m.labels, cmd = m.labels.Update(msg)
	return m, cmd
}

func (m Model) openCommentEditor(mode commentMode) (tea.Model, tea.Cmd) {
	m.commentOrigin = m.screen
	m.commentItem = m.selected
	m.commentKind = m.kind()
	m.commentTarget = provider.ReviewTarget{}
	m.commentTargetSet = false
	m.commentThread = provider.ReviewThreadTarget{}
	if mode == inlineReview {
		target, err := m.selectedReviewTarget()
		if err != nil {
			m.status = err.Error()
			return m, nil
		}
		if m.kind() == provider.Commits && target.IsRange() {
			m.status = "commit comments support one diff line"
			return m, nil
		}
		m.commentTarget = target
		m.commentTargetSet = true
		m.diffDragging = false
	}
	m.commentMode = mode
	m.screen = commentScreen
	m.comment.SetValue("")
	m.comment.CursorStart()
	m.err = nil
	return m, m.comment.Focus()
}

func (m Model) openReplyEditor(review provider.DiffReview) (tea.Model, tea.Cmd) {
	if !review.Replyable || review.ThreadID == "" && review.ReplyToID == "" {
		m.status = "selected review does not support replies"
		return m, nil
	}
	m.commentOrigin = m.screen
	m.commentMode = reviewReply
	m.commentItem = m.selected
	m.commentKind = m.kind()
	m.commentTarget = provider.ReviewTarget{}
	m.commentTargetSet = false
	m.commentThread = provider.ReviewThreadTarget{ThreadID: review.ThreadID, ReplyToID: review.ReplyToID}
	m.screen = commentScreen
	m.comment.SetValue("")
	m.comment.CursorStart()
	m.err = nil
	return m, m.comment.Focus()
}

func (m Model) commentUsesDiffBackground() bool {
	return m.screen == commentScreen && (m.commentMode == inlineReview || m.commentMode == reviewReply) && m.commentOrigin == diffScreen
}

func (m Model) updateCommentInput(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.actionBusy {
		return m, nil
	}
	switch msg.String() {
	case "esc":
		m.comment.Blur()
		m.screen = m.commentOrigin
		if m.screen == diffScreen {
			m.setDiffContent()
		} else {
			m.screen = detailScreen
			m.setDetailContent()
		}
		m.err = nil
		return m, nil
	case "ctrl+s":
		body := strings.TrimSpace(m.comment.Value())
		if body == "" {
			m.err = fmt.Errorf("comment cannot be blank")
			return m, nil
		}
		m.actionBusy = true
		m.err = nil
		item := m.commentItem
		if m.commentMode == inlineReview {
			if !m.commentTargetSet {
				m.actionBusy = false
				m.screen = diffScreen
				m.err = fmt.Errorf("selected diff line cannot be reviewed")
				return m, nil
			}
			target := m.commentTarget
			if m.commentKind == provider.Commits {
				m.status = "submitting commit comment…"
				return m, m.actionCmd("commit comment", func(ctx context.Context) error {
					return m.backend.AddCommitComment(ctx, item, target, body)
				})
			}
			m.status = "submitting inline review…"
			return m, m.actionCmd("inline review", func(ctx context.Context) error {
				return m.backend.AddReviewComment(ctx, item, target, body)
			})
		}
		if m.commentMode == generalReview {
			m.status = "submitting review…"
			return m, m.actionCmd("review", func(ctx context.Context) error {
				return m.backend.AddReview(ctx, item, body)
			})
		}
		if m.commentMode == reviewReply {
			target := m.commentThread
			m.status = "submitting review reply…"
			return m, m.actionCmd("review reply", func(ctx context.Context) error {
				return m.backend.AddReviewReply(ctx, item, target, body)
			})
		}
		kind := m.commentKind
		m.status = "submitting comment…"
		return m, m.actionCmd("comment", func(ctx context.Context) error {
			return m.backend.AddComment(ctx, kind, item, body)
		})
	}
	m.err = nil
	var cmd tea.Cmd
	m.comment, cmd = m.comment.Update(msg)
	return m, cmd
}

func splitLabels(value string) []string {
	parts := strings.Split(value, ",")
	labels := make([]string, 0, len(parts))
	for _, part := range parts {
		if label := strings.TrimSpace(part); label != "" {
			labels = append(labels, label)
		}
	}
	return labels
}

func (m Model) changeFilter(delta int) (tea.Model, tea.Cmd) {
	if m.remoteErr != nil {
		return m, nil
	}
	filters := m.backend.Filters(m.kind())
	if len(filters) <= 1 {
		return m, nil
	}
	index := (m.filterIndex[m.kind()] + delta + len(filters)) % len(filters)
	m.filterIndex[m.kind()] = index
	m.cursor[m.kind()] = 0
	m.offset[m.kind()] = 0
	m.loadingList = false
	return m.startListLoad()
}

func (m Model) openSelected() (tea.Model, tea.Cmd) {
	items := m.items[m.kind()]
	index := m.cursor[m.kind()]
	if index < 0 || index >= len(items) {
		return m, nil
	}
	if m.remoteErr != nil {
		m.status = "details require an available remote provider"
		return m, nil
	}
	m.selected = items[index]
	m.detail = provider.Detail{}
	m.viewport.SetContent("")
	m.diffFile, m.diffLine, m.diffAnchor = 0, -1, -1
	m.screen = detailScreen
	m.viewport.GotoTop()
	m.loadingDetail = false
	return m.startDetailLoad()
}

func (m *Model) moveCursor(delta int) {
	items := m.items[m.kind()]
	if len(items) == 0 {
		return
	}
	next := m.cursor[m.kind()] + delta
	if next < 0 {
		next = 0
	}
	if next >= len(items) {
		next = len(items) - 1
	}
	m.cursor[m.kind()] = next
	m.ensureCursorVisible()
}

func (m *Model) ensureCursorVisible() {
	height := m.listHeight()
	if m.cursor[m.kind()] < m.offset[m.kind()] {
		m.offset[m.kind()] = m.cursor[m.kind()]
	}
	if m.cursor[m.kind()] >= m.offset[m.kind()]+height {
		m.offset[m.kind()] = m.cursor[m.kind()] - height + 1
	}
}

func (m *Model) clampSelection(kind provider.Kind) {
	items := m.items[kind]
	if len(items) == 0 {
		m.cursor[kind], m.offset[kind] = 0, 0
		return
	}
	if m.cursor[kind] >= len(items) {
		m.cursor[kind] = len(items) - 1
	}
	m.ensureCursorVisible()
}

func (m Model) listHeight() int {
	height := m.height - 7
	if height < 1 {
		return 1
	}
	return height
}

func (m *Model) resizeViewport() {
	width := m.width - 4
	height := m.height - 5
	if width < 20 {
		width = 20
	}
	if height < 3 {
		height = 3
	}
	m.viewport.Width = width
	m.viewport.Height = height
	if m.labels.Width > width-8 {
		m.labels.Width = max(10, width-8)
	}
	m.comment.SetWidth(min(66, max(20, width-8)))
	m.comment.SetHeight(min(7, max(3, height/4)))
	m.fileFilter.Width = max(10, m.width-16)
}

func (m *Model) setDetailContent() {
	selectedLine, anchor := -1, -1
	if m.detailDiffActive {
		selectedLine, anchor = m.diffLine, m.diffAnchor
	}
	content, _ := renderDetailLayout(m.detail, m.viewport.Width, m.diffFile, selectedLine, anchor, m.selectedReview)
	if diffCommentableKind(m.kind()) && len(m.detail.Diffs) == 0 {
		content += "\n" + detailBoxStyle.Width(max(12, m.viewport.Width-2)).Render(sectionTitleStyle.Render("Diff")+"\n"+metaStyle.Render("No patch was provided for this change."))
	}
	m.viewport.SetContent(content)
}

func (m *Model) clampDiffSelection() {
	if len(m.detail.Diffs) == 0 {
		m.diffFile, m.diffLine, m.diffAnchor = 0, -1, -1
		m.diffDragging = false
		return
	}
	if m.diffFile < 0 {
		m.diffFile = 0
	}
	if m.diffFile >= len(m.detail.Diffs) {
		m.diffFile = len(m.detail.Diffs) - 1
	}
	lines := m.detail.Diffs[m.diffFile].Lines
	if len(lines) == 0 {
		m.diffLine, m.diffAnchor = -1, -1
		m.diffDragging = false
		return
	}
	if m.diffLine < 0 {
		m.diffLine = 0
	}
	if m.diffLine >= len(lines) {
		m.diffLine = len(lines) - 1
	}
	if m.diffAnchor >= len(lines) {
		m.diffAnchor = -1
		m.diffDragging = false
	}
}

func (m *Model) moveDiffFile(delta int) {
	if len(m.detail.Diffs) == 0 {
		return
	}
	next := m.diffFile + delta
	if next < 0 || next >= len(m.detail.Diffs) {
		return
	}
	m.diffFile = next
	m.diffLine = -1
	m.diffAnchor = -1
	m.diffDragging = false
	m.selectedReview = -1
	m.status = ""
	m.clampDiffSelection()
	m.setDiffContent()
}

func (m *Model) moveDiffLine(delta int) {
	if m.diffFile < 0 || m.diffFile >= len(m.detail.Diffs) {
		return
	}
	lines := m.detail.Diffs[m.diffFile].Lines
	if len(lines) == 0 {
		return
	}
	next := m.diffLine + delta
	if next < 0 {
		next = 0
	}
	if next >= len(lines) {
		next = len(lines) - 1
	}
	m.diffLine = next
	m.selectedReview = -1
	m.status = ""
	m.setDiffContent()
}

func (m Model) selectedDiffReview() (provider.DiffReview, bool) {
	if m.diffFile < 0 || m.diffFile >= len(m.detail.Diffs) {
		return provider.DiffReview{}, false
	}
	file := m.detail.Diffs[m.diffFile]
	if m.selectedReview >= 0 && m.selectedReview < len(file.Reviews) {
		return file.Reviews[m.selectedReview], true
	}
	if m.diffLine < 0 || m.diffLine >= len(file.Lines) {
		return provider.DiffReview{}, false
	}
	line := file.Lines[m.diffLine]
	for _, review := range file.Reviews {
		for _, matched := range reviewsEndingAt([]provider.DiffReview{review}, line) {
			_ = matched
			return review, true
		}
	}
	return provider.DiffReview{}, false
}

func (m Model) startResolveReview(review provider.DiffReview) (tea.Model, tea.Cmd) {
	if review.Resolved {
		m.status = "review thread is already resolved"
		return m, nil
	}
	if !review.Resolvable || review.ThreadID == "" {
		m.status = "selected review cannot be resolved"
		return m, nil
	}
	m.actionBusy = true
	m.status = "resolving review thread…"
	item := m.selected
	target := provider.ReviewThreadTarget{ThreadID: review.ThreadID, ReplyToID: review.ReplyToID}
	return m, m.actionCmd("resolve review", func(ctx context.Context) error {
		return m.backend.ResolveReview(ctx, item, target)
	})
}

func (m Model) selectedReviewTarget() (provider.ReviewTarget, error) {
	if m.diffFile < 0 || m.diffFile >= len(m.detail.Diffs) {
		return provider.ReviewTarget{}, fmt.Errorf("no diff file is selected")
	}
	file := m.detail.Diffs[m.diffFile]
	if m.diffLine < 0 || m.diffLine >= len(file.Lines) {
		return provider.ReviewTarget{}, fmt.Errorf("no reviewable diff line is selected")
	}
	startIndex, endIndex := m.diffLine, m.diffLine
	if m.diffAnchor >= 0 {
		startIndex = min(m.diffAnchor, m.diffLine)
		endIndex = max(m.diffAnchor, m.diffLine)
	}
	selected := file.Lines[startIndex : endIndex+1]
	allNew, allOld := true, true
	for _, line := range selected {
		allNew = allNew && line.NewLine > 0
		allOld = allOld && line.OldLine > 0
	}
	if !allNew && !allOld {
		return provider.ReviewTarget{}, fmt.Errorf("selected range crosses old and new diff sides")
	}
	side := provider.ReviewSideNew
	if !allNew {
		side = provider.ReviewSideOld
	}
	for index := 1; index < len(selected); index++ {
		previous, current := selected[index-1], selected[index]
		if side == provider.ReviewSideNew && current.NewLine != previous.NewLine+1 ||
			side == provider.ReviewSideOld && current.OldLine != previous.OldLine+1 {
			return provider.ReviewTarget{}, fmt.Errorf("selected range crosses a diff hunk")
		}
	}
	start, end := selected[0], selected[len(selected)-1]
	return provider.ReviewTarget{
		OldPath:          file.OldPath,
		NewPath:          file.NewPath,
		StartOldLine:     start.OldLine,
		StartNewLine:     start.NewLine,
		OldLine:          end.OldLine,
		NewLine:          end.NewLine,
		StartOldPosition: start.OldPosition,
		StartNewPosition: start.NewPosition,
		OldPosition:      end.OldPosition,
		NewPosition:      end.NewPosition,
		Position:         end.Position,
		Side:             side,
		BaseSHA:          file.BaseSHA,
		StartSHA:         file.StartSHA,
		HeadSHA:          file.HeadSHA,
	}, nil
}

func (m *Model) setDiffContent() {
	m.viewport.SetContent(renderDiffFileState(m.detail.Diffs, m.diffFile, m.diffLine, m.diffAnchor, m.selectedReview, m.viewport.Width))
	if m.diffLine >= 0 && m.diffFile >= 0 && m.diffFile < len(m.detail.Diffs) {
		row := diffContentRowForLine(m.detail.Diffs[m.diffFile], m.diffLine, m.viewport.Width)
		m.viewport.SetYOffset(max(0, row-m.viewport.Height/2))
	} else {
		m.viewport.GotoTop()
	}
}

func (m *Model) setDiffContentPreservingOffset() {
	offset := m.viewport.YOffset
	m.viewport.SetContent(renderDiffFileState(m.detail.Diffs, m.diffFile, m.diffLine, m.diffAnchor, m.selectedReview, m.viewport.Width))
	m.viewport.SetYOffset(offset)
}

func (m *Model) setDetailContentPreservingOffset() {
	offset := m.viewport.YOffset
	m.setDetailContent()
	m.viewport.SetYOffset(offset)
}

func (m Model) diffHitAtMouse(x, y int) (diffHitRegion, bool) {
	const viewportTop = 2
	if m.diffFile < 0 || m.diffFile >= len(m.detail.Diffs) || y < viewportTop || y >= viewportTop+m.viewport.Height {
		return diffHitRegion{}, false
	}
	contentRow := m.viewport.YOffset + y - viewportTop
	regions := diffFileHitRegions(m.detail.Diffs[m.diffFile], m.diffFile, m.viewport.Width, 0, 0)
	for index := len(regions) - 1; index >= 0; index-- {
		if regions[index].Row == contentRow {
			return regions[index], true
		}
	}
	return diffHitRegion{}, false
}

func (m Model) detailDiffHitAtMouse(x, y int) (diffHitRegion, bool) {
	const viewportTop = 2
	if !diffCommentableKind(m.kind()) || y < viewportTop || y >= viewportTop+m.viewport.Height {
		return diffHitRegion{}, false
	}
	contentRow := m.viewport.YOffset + y - viewportTop
	_, regions := renderDetailLayout(m.detail, m.viewport.Width, m.diffFile, m.diffLine, m.diffAnchor, m.selectedReview)
	for index := len(regions) - 1; index >= 0; index-- {
		if regions[index].Row == contentRow {
			return regions[index], true
		}
	}
	return diffHitRegion{}, false
}

func resolveButtonHit(hit diffHitRegion, x int) bool {
	return actionButtonHit(hit.ResolveStart, hit.ResolveEnd, x)
}

func replyButtonHit(hit diffHitRegion, x int) bool {
	return actionButtonHit(hit.ReplyStart, hit.ReplyEnd, x)
}

func actionButtonHit(start, end, x int) bool {
	return start >= 0 && x >= start-1 && x <= end
}

func (m Model) diffLineAtMouse(y int) (int, bool) {
	hit, ok := m.diffHitAtMouse(0, y)
	if !ok || hit.Line < 0 || hit.Review >= 0 {
		return -1, false
	}
	return hit.Line, true
}

func diffContentRowForLine(file provider.DiffFile, lineIndex, width int) int {
	row := diffContentHeaderHeight(file)
	for index, line := range file.Lines {
		if index == lineIndex {
			return row
		}
		row++
		for _, review := range reviewsEndingAt(file.Reviews, line) {
			row += lipgloss.Height(renderDiffReview(review, width))
		}
	}
	return row
}

func diffContentHeaderHeight(file provider.DiffFile) int {
	height := 1
	if file.OldPath != "" && file.NewPath != "" && file.OldPath != file.NewPath {
		height++
	}
	if file.TooLarge {
		height++
	}
	return height
}

func renderDetail(detail provider.Detail, width int) string {
	content, _ := renderDetailLayout(detail, width, -1, -1, -1, -1)
	return content
}

type diffHitRegion struct {
	Row          int
	File         int
	Line         int
	Review       int
	ResolveStart int
	ResolveEnd   int
	ReplyStart   int
	ReplyEnd     int
}

func renderDetailLayout(detail provider.Detail, width, selectedFile, selectedLine, rangeAnchor, selectedReview int) (string, []diffHitRegion) {
	if width < 20 {
		width = 20
	}
	renderer, err := glamour.NewTermRenderer(glamour.WithStandardStyle("dark"), glamour.WithWordWrap(max(10, width-6)))
	if err != nil {
		sections := []string{"Description\n" + detail.Body}
		for _, section := range detail.Sections {
			sections = append(sections, section.Title+"\n"+section.Markdown)
		}
		for index := range detail.Diffs {
			sections = append(sections, "Diff\n"+renderDiffFile(detail.Diffs, index, -1, -1, width-4))
		}
		return strings.Join(sections, "\n\n────────────────────\n\n"), nil
	}
	renderMarkdown := func(markdown string) string {
		if strings.TrimSpace(markdown) == "" {
			markdown = "_No content._"
		}
		rendered, renderErr := renderer.Render(markdown)
		if renderErr != nil {
			return markdown
		}
		return strings.TrimSpace(rendered)
	}
	boxWidth := max(12, width-2)
	sections := []string{detailBoxStyle.Width(boxWidth).Render(sectionTitleStyle.Render("Description") + "\n" + renderMarkdown(detail.Body))}
	for _, section := range detail.Sections {
		sections = append(sections, detailBoxStyle.Width(boxWidth).Render(sectionTitleStyle.Render(section.Title)+"\n"+renderMarkdown(section.Markdown)))
	}
	row := lipgloss.Height(sections[0])
	for index := 1; index < len(sections); index++ {
		row += 1 + lipgloss.Height(sections[index])
	}
	var hits []diffHitRegion
	for index := range detail.Diffs {
		line, anchor, review := -1, -1, -1
		if index == selectedFile {
			line, anchor, review = selectedLine, rangeAnchor, selectedReview
		}
		contentWidth := boxWidth - 4
		diffContent := renderDiffFileState(detail.Diffs, index, line, anchor, review, contentWidth)
		box := detailBoxStyle.Width(boxWidth).Render(sectionTitleStyle.Render("Diff") + "\n" + diffContent)
		row++
		boxStart := row
		sections = append(sections, box)
		for localRow := 0; localRow < lipgloss.Height(box); localRow++ {
			hits = append(hits, diffHitRegion{Row: boxStart + localRow, File: index, Line: -1, Review: -1, ResolveStart: -1, ResolveEnd: -1})
		}
		for _, hit := range diffFileHitRegions(detail.Diffs[index], index, contentWidth, boxStart+2, 2) {
			hits = append(hits, hit)
		}
		row += lipgloss.Height(box)
	}
	return strings.Join(sections, "\n"), hits
}

func diffFileHitRegions(file provider.DiffFile, fileIndex, width, baseRow, baseX int) []diffHitRegion {
	regions := make([]diffHitRegion, 0)
	row := diffContentHeaderHeight(file)
	for lineIndex, line := range file.Lines {
		regions = append(regions, diffHitRegion{Row: baseRow + row, File: fileIndex, Line: lineIndex, Review: -1, ResolveStart: -1, ResolveEnd: -1, ReplyStart: -1, ReplyEnd: -1})
		row++
		for _, reviewIndex := range reviewIndexesEndingAt(file.Reviews, line) {
			review := file.Reviews[reviewIndex]
			height := lipgloss.Height(renderDiffReview(review, width))
			for offset := 0; offset < height; offset++ {
				region := newReviewHitRegion(baseRow+row+offset, fileIndex, lineIndex, reviewIndex, baseX, offset, review)
				regions = append(regions, region)
			}
			row += height
		}
	}
	for reviewIndex, review := range file.Reviews {
		if !review.Outdated && (review.OldLine > 0 || review.NewLine > 0) {
			continue
		}
		height := lipgloss.Height(renderDiffReview(review, width))
		for offset := 0; offset < height; offset++ {
			region := newReviewHitRegion(baseRow+row+offset, fileIndex, -1, reviewIndex, baseX, offset, review)
			regions = append(regions, region)
		}
		row += height
	}
	return regions
}

func newReviewHitRegion(row, file, line, reviewIndex, baseX, offset int, review provider.DiffReview) diffHitRegion {
	region := diffHitRegion{
		Row: row, File: file, Line: line, Review: reviewIndex,
		ResolveStart: -1, ResolveEnd: -1, ReplyStart: -1, ReplyEnd: -1,
	}
	if offset != 0 {
		return region
	}
	plain := reviewMetaText(review)
	region.ResolveStart, region.ResolveEnd = reviewActionBounds(plain, "[Resolve]", baseX)
	region.ReplyStart, region.ReplyEnd = reviewActionBounds(plain, "[Reply]", baseX)
	return region
}

func reviewActionBounds(meta, action string, baseX int) (int, int) {
	index := strings.Index(meta, action)
	if index < 0 {
		return -1, -1
	}
	start := baseX + 2 + lipgloss.Width(meta[:index])
	return start, start + lipgloss.Width(action)
}

func (m Model) handleMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	if m.updateAvailable && m.installUpdate != nil && msg.Button == tea.MouseButtonLeft && msg.Action == tea.MouseActionPress && msg.Y == 0 && msg.X >= m.updateButtonStart() {
		m.updateAvailable = false
		return m, tea.ExecProcess(m.installUpdate(), func(err error) tea.Msg { return installFinishedMsg{err: err} })
	}
	if m.workspaceDividerDragging {
		switch msg.Action {
		case tea.MouseActionRelease:
			m.workspaceDividerDragging = false
			return m, nil
		case tea.MouseActionMotion:
			return m.resizeWorkspaceDivider(msg.X)
		case tea.MouseActionPress:
			// Recover if a terminal failed to deliver the prior release.
			m.workspaceDividerDragging = false
		}
	}
	if m.actionBusy {
		return m, nil
	}
	if m.screen == commentScreen {
		return m, nil
	}
	if m.screen == listScreen && msg.Button == tea.MouseButtonLeft && msg.Action == tea.MouseActionPress && msg.Y == 1 {
		if tab := m.tabAt(msg.X); tab >= 0 {
			m.active = tab
			m.status = ""
			return m.startActiveTabLoad()
		}
	}
	if m.localTab() {
		leftWidth, _ := m.workspacePaneWidths()
		dividerStart, dividerEnd := leftWidth, leftWidth+2
		bodyStart := 5
		bodyEnd := bodyStart + m.workspaceListHeight()
		if msg.Button == tea.MouseButtonLeft && msg.Action == tea.MouseActionPress && msg.Y >= bodyStart && msg.Y < bodyEnd && msg.X >= dividerStart && msg.X <= dividerEnd {
			m.workspaceDividerDragging = true
			return m, nil
		}
		if m.workspaceCommitActive() && msg.Button == tea.MouseButtonLeft && msg.Action == tea.MouseActionPress && msg.Y == 4 {
			buttonWidth := lipgloss.Width(commitButtonStyle.Render("Commit"))
			buttonStart := max(0, m.width-buttonWidth-1)
			if msg.X >= buttonStart {
				return m.startWorkspaceCommit()
			}
			m.commitMessage.Focus()
			return m, nil
		}
		if msg.Button == tea.MouseButtonWheelUp {
			if msg.X >= leftWidth+3 {
				return m.moveWorkspacePreview(-3), nil
			}
			return m.moveWorkspaceCursor(-3)
		}
		if msg.Button == tea.MouseButtonWheelDown {
			if msg.X >= leftWidth+3 {
				return m.moveWorkspacePreview(3), nil
			}
			return m.moveWorkspaceCursor(3)
		}
		if msg.Button != tea.MouseButtonLeft || msg.Action != tea.MouseActionPress || msg.Y < 5 {
			return m, nil
		}
		if msg.X >= leftWidth || msg.Y >= 5+m.workspaceListHeight() {
			return m, nil
		}
		row := msg.Y - 5
		index := m.workspaceOffset + row
		if m.workspaceCommitActive() {
			index = m.workspaceChangeIndexAtRow(row)
		}
		length := len(m.filteredWorkspaceEntries())
		if m.workspaceCommitActive() {
			length = len(m.filteredWorkspaceChanges())
		}
		if index >= 0 && index < length {
			m.workspaceCursor = index
			m.ensureWorkspaceCursorVisible()
			if m.workspaceFilesActive() && m.filteredWorkspaceEntries()[index].IsDir {
				return m.toggleWorkspaceDirectory()
			}
			if m.workspaceCommitActive() && m.filteredWorkspaceChanges()[index].isDir {
				return m.toggleWorkspaceChangeDirectory(), nil
			}
			return m.loadSelectedWorkspaceItem()
		}
		return m, nil
	}
	if m.screen == diffScreen {
		switch msg.Action {
		case tea.MouseActionRelease:
			if m.diffDragging {
				m.diffDragging = false
				if m.diffAnchor == m.diffLine {
					m.diffAnchor = -1
					m.status = "review line selected"
				} else {
					m.status = "review range selected"
					return m.openCommentEditor(inlineReview)
				}
			}
			return m, nil
		case tea.MouseActionMotion:
			if !m.diffDragging || msg.Button != tea.MouseButtonLeft {
				return m, nil
			}
			line, ok := m.diffLineAtMouse(msg.Y)
			if !ok || line == m.diffLine {
				return m, nil
			}
			m.diffLine = line
			m.status = "dragging review range"
			m.setDiffContentPreservingOffset()
			return m, nil
		case tea.MouseActionPress:
			if msg.Button != tea.MouseButtonLeft {
				break
			}
			m.diffDragging = false
			if msg.X >= 0 && msg.X < m.viewport.Width {
				hit, ok := m.diffHitAtMouse(msg.X, msg.Y)
				if !ok {
					break
				}
				m.diffFile = hit.File
				if hit.Line >= 0 {
					m.diffLine = hit.Line
				}
				if hit.Review >= 0 {
					m.selectedReview = hit.Review
					m.setDiffContentPreservingOffset()
					review := m.detail.Diffs[hit.File].Reviews[hit.Review]
					if resolveButtonHit(hit, msg.X) {
						return m.startResolveReview(review)
					}
					if replyButtonHit(hit, msg.X) {
						return m.openReplyEditor(review)
					}
					return m, nil
				}
				if hit.Line >= 0 {
					m.selectedReview = -1
					m.diffAnchor = hit.Line
					m.diffDragging = true
					m.status = "dragging review range"
					m.setDiffContentPreservingOffset()
					return m, nil
				}
			}
		}
	}
	if m.screen == detailScreen && diffCommentableKind(m.kind()) {
		switch msg.Action {
		case tea.MouseActionRelease:
			if !m.diffDragging {
				return m, nil
			}
			m.diffDragging = false
			if m.diffAnchor >= 0 && m.diffAnchor != m.diffLine {
				m.status = "review range selected"
				return m.openCommentEditor(inlineReview)
			}
			m.diffAnchor = -1
			m.detailDiffActive = false
			m.screen = diffScreen
			m.setDiffContent()
			return m, nil
		case tea.MouseActionMotion:
			if !m.diffDragging || msg.Button != tea.MouseButtonLeft {
				return m, nil
			}
			hit, ok := m.detailDiffHitAtMouse(msg.X, msg.Y)
			if !ok || hit.File != m.diffFile || hit.Line < 0 || hit.Line == m.diffLine {
				return m, nil
			}
			m.diffLine = hit.Line
			m.status = "dragging review range"
			m.setDetailContentPreservingOffset()
			return m, nil
		case tea.MouseActionPress:
			if msg.Button != tea.MouseButtonLeft {
				break
			}
			hit, ok := m.detailDiffHitAtMouse(msg.X, msg.Y)
			if !ok {
				break
			}
			m.diffFile = hit.File
			if hit.Line >= 0 {
				m.diffLine = hit.Line
			}
			if hit.Review >= 0 {
				m.selectedReview = hit.Review
				m.detailDiffActive = true
				m.setDetailContentPreservingOffset()
				review := m.detail.Diffs[hit.File].Reviews[hit.Review]
				if resolveButtonHit(hit, msg.X) {
					return m.startResolveReview(review)
				}
				if replyButtonHit(hit, msg.X) {
					return m.openReplyEditor(review)
				}
				return m, nil
			}
			if hit.Line < 0 {
				m.diffLine = -1
				m.diffAnchor = -1
				m.selectedReview = -1
				m.detailDiffActive = false
				m.screen = diffScreen
				m.clampDiffSelection()
				m.setDiffContent()
				return m, nil
			}
			m.selectedReview = -1
			m.diffAnchor = hit.Line
			m.diffDragging = true
			m.detailDiffActive = true
			m.status = "dragging review range"
			m.setDetailContentPreservingOffset()
			return m, nil
		}
	}
	if msg.Button == tea.MouseButtonWheelUp {
		if m.screen == diffScreen {
			m.moveDiffLine(-3)
		} else if m.screen == detailScreen || m.screen == labelScreen {
			m.viewport.LineUp(3)
		} else {
			m.moveCursor(-3)
		}
		return m, nil
	}
	if msg.Button == tea.MouseButtonWheelDown {
		if m.screen == diffScreen {
			m.moveDiffLine(3)
		} else if m.screen == detailScreen || m.screen == labelScreen {
			m.viewport.LineDown(3)
		} else {
			m.moveCursor(3)
		}
		return m, nil
	}
	if msg.Button != tea.MouseButtonLeft || msg.Action != tea.MouseActionPress {
		return m, nil
	}
	if m.screen != listScreen {
		return m, nil
	}
	if msg.Y == 3 {
		if filter := m.filterAt(msg.X); filter >= 0 {
			m.filterIndex[m.kind()] = filter
			m.cursor[m.kind()], m.offset[m.kind()] = 0, 0
			m.loadingList = false
			return m.startListLoad()
		}
	}
	if msg.Y >= 5 && msg.Y < 5+m.listHeight() {
		index := m.offset[m.kind()] + msg.Y - 5
		if index >= 0 && index < len(m.items[m.kind()]) {
			m.cursor[m.kind()] = index
			return m.openSelected()
		}
	}
	return m, nil
}

func (m Model) tabAt(x int) int {
	position := 1
	labels := m.tabLabels()
	start, end := m.tabRange(labels)
	if start > 0 {
		position += lipgloss.Width("‹ ")
	}
	for index := start; index < end; index++ {
		name := labels[index]
		label := fmt.Sprintf(" %d %s ", index+1, name)
		end := position + lipgloss.Width(label)
		if x >= position && x < end {
			return index
		}
		position = end + 1
	}
	return -1
}

func (m Model) tabRange(labels []string) (int, int) {
	if len(labels) == 0 {
		return 0, 0
	}
	active := min(max(0, m.active), len(labels)-1)
	available := max(1, m.width)
	bestStart, bestEnd, bestCount, bestBalance := active, active+1, 1, len(labels)*2
	for start := 0; start <= active; start++ {
		for end := active + 1; end <= len(labels); end++ {
			width := 1
			if start > 0 {
				width += lipgloss.Width("‹ ")
			}
			if end < len(labels) {
				width += lipgloss.Width(" ›")
			}
			for index := start; index < end; index++ {
				if index > start {
					width++
				}
				width += lipgloss.Width(fmt.Sprintf(" %d %s ", index+1, labels[index]))
			}
			count := end - start
			balance := absInt((start + end - 1) - 2*active)
			if width <= available && (count > bestCount || count == bestCount && balance < bestBalance) {
				bestStart, bestEnd, bestCount, bestBalance = start, end, count, balance
			}
		}
	}
	return bestStart, bestEnd
}

func absInt(value int) int {
	if value < 0 {
		return -value
	}
	return value
}

func (m Model) tabLabels() []string {
	labels := make([]string, 0, m.tabCount())
	activeKinds := kinds
	if m.workspace != nil {
		labels = append(labels, "Commit", "Files")
		if m.filesOnly {
			return []string{"Files"}
		}
		activeKinds = m.workspaceKinds()
	}
	for _, kind := range activeKinds {
		name := kind.String()
		if m.backend != nil {
			name = m.backend.TabName(kind)
		}
		if kind == provider.Commits {
			name = "Graph"
		}
		labels = append(labels, name)
	}
	return labels
}

func (m Model) filterAt(x int) int {
	if m.remoteErr != nil {
		return -1
	}
	position := 1
	for index, filter := range m.backend.Filters(m.kind()) {
		label := " " + filter.Label + " "
		end := position + lipgloss.Width(label)
		if x >= position && x < end {
			return index
		}
		position = end + 1
	}
	return -1
}
