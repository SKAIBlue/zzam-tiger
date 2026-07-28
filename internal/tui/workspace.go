package tui

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"golang.org/x/sys/unix"

	"github.com/SKAIBlue/zzam-tiger/internal/provider"
	"github.com/SKAIBlue/zzam-tiger/internal/worktree"
)

type workspaceClient interface {
	Root() string
	Entries(context.Context, string) ([]worktree.Entry, error)
	Read(context.Context, string) (worktree.File, error)
	Status(context.Context) (worktree.Status, error)
	Stage(context.Context, string) error
	StageAll(context.Context) error
	Unstage(context.Context, string) error
	UnstageAll(context.Context) error
	Commit(context.Context, string) error
	RemoteState(context.Context) (worktree.RemoteState, error)
	Pull(context.Context) error
	Push(context.Context) error
	Diff(context.Context, string, bool) (worktree.Diff, error)
	History(context.Context, int) ([]worktree.Commit, error)
	CommitPaths(context.Context, string) ([]string, error)
	Branches(context.Context) ([]worktree.Branch, error)
	CreateBranch(context.Context, string, string) error
	CheckoutBranch(context.Context, string) error
	RenameBranch(context.Context, string, string) error
	DeleteBranch(context.Context, string) error
	DeleteRemoteBranch(context.Context, string, string) error
}

type workspaceResultMsg struct {
	request   uint64
	op        string
	entries   []worktree.Entry
	file      worktree.File
	status    worktree.Status
	remote    worktree.RemoteState
	diff      worktree.Diff
	image     string
	rows      []string
	width     int
	height    int
	dir       string
	expand    bool
	entryDirs []workspaceEntryDirectory
	err       error
}

type workspaceEntryDirectory struct {
	dir     string
	entries []worktree.Entry
}

type workspaceActionResultMsg struct {
	request uint64
	action  string
	err     error
}

func (m Model) fetchWorkspaceRemoteStateCmd() tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		remote, err := m.workspace.RemoteState(ctx)
		return workspaceRemoteResultMsg{remote: remote, err: err}
	}
}

type workspaceChange struct {
	change worktree.Change
	staged bool
	path   string
	name   string
	depth  int
	isDir  bool
}

type workspaceChangeRow struct {
	title string
	index int
	item  workspaceChange
}

func (c workspaceChange) displayPath() string {
	if c.path != "" {
		return c.path
	}
	return c.change.Path
}

func (m Model) fetchWorkspaceCmd(request uint64) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if m.workspaceFilesActive() {
			groups, err := m.readCompressedWorkspaceEntries(ctx, "")
			if err != nil {
				return workspaceResultMsg{request: request, op: "entries", err: err}
			}
			return workspaceResultMsg{request: request, op: "entries", entries: groups[0].entries, entryDirs: groups}
		}
		status, err := m.workspace.Status(ctx)
		if err != nil {
			return workspaceResultMsg{request: request, op: "status", err: err}
		}
		remote, _ := m.workspace.RemoteState(ctx)
		return workspaceResultMsg{request: request, op: "status", status: status, remote: remote}
	}
}

func (m Model) fetchWorkspaceEntriesCmd(request uint64, dir string, expand bool) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		groups, err := m.readCompressedWorkspaceEntries(ctx, dir)
		if err != nil {
			return workspaceResultMsg{request: request, op: "entries", dir: dir, expand: expand, err: err}
		}
		return workspaceResultMsg{request: request, op: "entries", entries: groups[0].entries, dir: dir, expand: expand, entryDirs: groups}
	}
}

func (m Model) readCompressedWorkspaceEntries(ctx context.Context, dir string) ([]workspaceEntryDirectory, error) {
	groups := make([]workspaceEntryDirectory, 0, 1)
	current := dir
	for {
		entries, err := m.workspace.Entries(ctx, current)
		if err != nil {
			return nil, err
		}
		groups = append(groups, workspaceEntryDirectory{dir: current, entries: entries})
		if len(entries) != 1 || !entries[0].IsDir || m.workspaceExpanded[entries[0].Path] {
			return groups, nil
		}
		current = entries[0].Path
	}
}

func (m Model) fetchWorkspaceFileCmd(request uint64, path string) tea.Cmd {
	width, height := m.workspaceImageDimensions()
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		file, err := m.workspace.Read(ctx, path)
		image := ""
		if err == nil && file.Image {
			image, _ = kittyImage(file.Data, width, height)
		} else if err == nil && isMarkdownPath(file.Path) {
			if target := firstLocalMarkdownImage(file.Path, file.Data); target != "" {
				if referenced, readErr := m.workspace.Read(ctx, target); readErr == nil && referenced.Image {
					image, _ = kittyImage(referenced.Data, width, max(1, height/2))
				}
			}
		}
		return workspaceResultMsg{request: request, op: "file", file: file, image: image, width: width, height: height, err: err}
	}
}

func (m Model) renderWorkspaceImageCmd(request uint64, file worktree.File, width, height int) tea.Cmd {
	return func() tea.Msg {
		image, _ := kittyImage(file.Data, width, height)
		return workspaceResultMsg{request: request, op: "image", file: file, image: image, width: width, height: height}
	}
}

func (m Model) workspaceImageDimensions() (int, int) {
	_, width := m.workspacePaneWidths()
	return width, max(1, m.workspaceListHeight()-1)
}

func (m Model) fetchWorkspaceDiffCmd(request uint64, path string, staged bool) tea.Cmd {
	width := m.workspaceDiffRenderWidth()
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		diff, err := m.workspace.Diff(ctx, path, staged)
		var rows []string
		if err == nil {
			rows = strings.Split(renderWorkspaceDiff(diff, width), "\n")
		}
		return workspaceResultMsg{request: request, op: "diff", diff: diff, rows: rows, width: width, err: err}
	}
}

func (m Model) renderWorkspaceDiffCmd(request uint64, diff worktree.Diff, width int) tea.Cmd {
	return func() tea.Msg {
		rows := strings.Split(renderWorkspaceDiff(diff, width), "\n")
		return workspaceResultMsg{request: request, op: "diff-render", diff: diff, rows: rows, width: width}
	}
}

func (m Model) startWorkspaceLoad() (Model, tea.Cmd) {
	if !m.localTab() || m.workspaceLoading {
		return m, nil
	}
	m.workspaceLoading = true
	// Invalidate file/image/diff work started before this workspace snapshot.
	m.workspacePreviewRequest++
	m.workspacePreviewLoading = false
	m.err = nil
	if m.workspaceFilesActive() {
		m.workspaceEntryRequest++
		dirs := []string{""}
		for dir, expanded := range m.workspaceExpanded {
			if expanded {
				dirs = append(dirs, dir)
			}
		}
		sort.Strings(dirs[1:])
		m.workspaceEntryPending = len(dirs)
		commands := make([]tea.Cmd, 0, len(dirs))
		for _, dir := range dirs {
			commands = append(commands, m.fetchWorkspaceEntriesCmd(m.workspaceEntryRequest, dir, false))
		}
		return m, tea.Batch(commands...)
	}
	m.workspaceRequest++
	return m, m.fetchWorkspaceCmd(m.workspaceRequest)
}

func (m Model) finishWorkspaceLoad(cmd tea.Cmd) (tea.Model, tea.Cmd) {
	if m.workspaceLoading || !m.workspaceWatchPending {
		return m, cmd
	}
	m.workspaceWatchPending = false
	next, refresh := m.startWorkspaceLoad()
	return next, tea.Batch(cmd, refresh)
}

func (m Model) startActiveTabLoad() (Model, tea.Cmd) {
	// A tab change always returns ownership of the arrows to the tab bar. This
	// also makes a formerly hidden preview or input impossible to retain focus.
	m.focus = focusTabs
	m.fileFilter.Blur()
	m.commitMessage.Blur()
	m.graphFilter.Blur()
	m.graphQuery.Blur()
	m.loadingList = false
	m.workspaceLoading = false
	m.workspacePreviewLoading = false
	m.workspacePreviewErr = nil
	m.workspacePreviewRequest++
	m.err = nil
	if m.localTab() {
		next, load := m.startWorkspaceLoad()
		return next, tea.Batch(tea.ClearScreen, load)
	}
	next, load := m.startListLoad()
	// File previews can emit terminal-side graphics commands. Clear the frame
	// when changing tabs so Bubble Tea repaints the header and tabs instead of
	// relying on an incremental diff against a terminal that was altered outside
	// its renderer.
	return next, tea.Batch(tea.ClearScreen, load)
}

func (m Model) workspacePreviewAvailable() bool {
	left, right := m.workspacePaneWidths()
	if right < 2 || left < 1 {
		return false
	}
	if m.workspaceFilesActive() {
		entries := m.filteredWorkspaceEntries()
		return m.workspaceCursor >= 0 && m.workspaceCursor < len(entries) && !entries[m.workspaceCursor].IsDir
	}
	changes := m.filteredWorkspaceChanges()
	return m.workspaceCursor >= 0 && m.workspaceCursor < len(changes) && !changes[m.workspaceCursor].isDir
}

// updateWorkspaceFocus handles only the directional focus contract. All other
// shortcuts remain in updateWorkspace below.
func (m Model) updateWorkspaceFocus(msg tea.KeyMsg) (tea.Model, tea.Cmd, bool) {
	key := msg.String()
	switch m.focus {
	case focusTabs:
		switch key {
		case "left":
			m.active = (m.active - 1 + m.tabCount()) % m.tabCount()
			next, cmd := m.startActiveTabLoad()
			return next, cmd, true
		case "right":
			m.active = (m.active + 1) % m.tabCount()
			next, cmd := m.startActiveTabLoad()
			return next, cmd, true
		case "down":
			if m.workspaceCommitActive() {
				m.focus = focusFileFilter
				return m, m.fileFilter.Focus(), true
			}
			m.focus = focusFileFilter
			return m, m.fileFilter.Focus(), true
		}
	case focusCommitMessage:
		if key == "up" {
			m.commitMessage.Blur()
			m.focus = focusFileFilter
			return m, m.fileFilter.Focus(), true
		}
		if key == "down" {
			m.commitMessage.Blur()
			m.focus = focusWorkspaceList
			m.clampWorkspaceCursor(len(m.filteredWorkspaceChanges()))
			return m, nil, true
		}
	case focusFileFilter:
		if key == "up" {
			m.fileFilter.Blur()
			m.focus = focusTabs
			return m, nil, true
		}
		if key == "down" || key == "enter" {
			m.fileFilter.Blur()
			if m.workspaceCommitActive() {
				m.focus = focusCommitMessage
				return m, m.commitMessage.Focus(), true
			}
			m.focus = focusWorkspaceList
			m.clampWorkspaceCursor(len(m.filteredWorkspaceEntries()))
			return m, nil, true
		}
	case focusWorkspaceList:
		switch key {
		case "right":
			if m.workspacePreviewAvailable() {
				m.focus = focusWorkspacePreview
				return m, nil, true
			}
			m.status = "no selectable preview"
			return m, nil, true
		case "up":
			if m.workspaceCursor == 0 {
				if m.workspaceCommitActive() {
					m.focus = focusCommitMessage
					return m, m.commitMessage.Focus(), true
				}
				m.focus = focusFileFilter
				return m, m.fileFilter.Focus(), true
			}
		case "down":
			next, cmd := m.moveWorkspaceCursor(1)
			return next, cmd, true
		}
	case focusWorkspacePreview:
		switch key {
		case "left":
			m.focus = focusWorkspaceList
			return m, nil, true
		case "down":
			return m.moveWorkspacePreview(1), nil, true
		case "up":
			if m.workspacePreviewOffset > 0 {
				return m.moveWorkspacePreview(-1), nil, true
			}
			if m.workspaceCommitActive() {
				m.focus = focusCommitMessage
				return m, m.commitMessage.Focus(), true
			}
			m.focus = focusFileFilter
			return m, m.fileFilter.Focus(), true
		}
	}
	return m, nil, false
}

func (m Model) handleWorkspaceResult(msg workspaceResultMsg) (tea.Model, tea.Cmd) {
	if !m.localTab() {
		return m, nil
	}
	switch msg.op {
	case "entries":
		if !m.workspaceFilesActive() || msg.request != m.workspaceEntryRequest {
			return m, nil
		}
	case "status":
		if !m.workspaceCommitActive() || msg.request != m.workspaceRequest {
			return m, nil
		}
	case "file", "image", "diff", "diff-render":
		if msg.request != m.workspacePreviewRequest {
			return m, nil
		}
	}
	if msg.err != nil {
		if msg.op == "file" || msg.op == "image" || msg.op == "diff" || msg.op == "diff-render" {
			m.workspacePreviewLoading = false
			m.workspacePreviewErr = msg.err
			return m.finishWorkspaceLoad(nil)
		} else if msg.op == "entries" {
			m.workspaceEntryPending = max(0, m.workspaceEntryPending-1)
			m.workspaceLoading = m.workspaceEntryPending > 0
		} else {
			m.workspaceLoading = false
		}
		m.err = msg.err
		return m.finishWorkspaceLoad(nil)
	}
	if msg.op == "file" || msg.op == "image" || msg.op == "diff" || msg.op == "diff-render" {
		m.workspacePreviewErr = nil
	}
	m.err = nil
	m.lastUpdated = time.Now()
	switch msg.op {
	case "entries":
		if msg.dir != "" && !m.workspaceDirectoryExists(msg.dir) {
			m.workspaceEntryPending = max(0, m.workspaceEntryPending-1)
			m.workspaceLoading = m.workspaceEntryPending > 0
			return m.finishWorkspaceLoad(nil)
		}
		selectedPath := ""
		selected := m.filteredWorkspaceEntries()
		if m.workspaceCursor >= 0 && m.workspaceCursor < len(selected) {
			selectedPath = selected[m.workspaceCursor].Path
		}
		groups := msg.entryDirs
		if len(groups) == 0 {
			groups = []workspaceEntryDirectory{{dir: msg.dir, entries: msg.entries}}
		}
		for _, group := range groups {
			m.replaceWorkspaceDirectory(group.dir, group.entries)
			m.workspaceLoaded[group.dir] = true
		}
		if msg.expand {
			m.workspaceExpanded[msg.dir] = true
		}
		for index := 1; msg.expand && index+1 < len(groups); index++ {
			m.workspaceExpanded[groups[index].dir] = true
		}
		if len(groups) > 1 {
			last := groups[len(groups)-1]
			if len(last.entries) == 1 && !last.entries[0].IsDir {
				m.workspaceExpanded[last.dir] = true
			}
		}
		if !msg.expand {
			for index := 1; index+1 < len(groups); index++ {
				m.workspaceExpanded[groups[index].dir] = true
			}
		}
		m.workspaceEntryPending = max(0, m.workspaceEntryPending-1)
		m.workspaceLoading = m.workspaceEntryPending > 0
		entries := m.filteredWorkspaceEntries()
		for index, entry := range entries {
			if entry.Path == selectedPath {
				m.workspaceCursor = index
				break
			}
		}
		m.clampWorkspaceCursor(len(entries))
		if msg.dir == "" {
			loaded, cmd := m.loadSelectedWorkspaceItem()
			return loaded.finishWorkspaceLoad(cmd)
		}
		return m.finishWorkspaceLoad(nil)
	case "status":
		if !m.workspaceCommitActive() {
			return m, nil
		}
		m.workspaceStatus = msg.status
		m.workspaceRemote = msg.remote
		changes := m.filteredWorkspaceChanges()
		if m.workspacePendingPath != "" {
			for index, change := range changes {
				if change.displayPath() == m.workspacePendingPath {
					m.workspaceCursor = index
					break
				}
			}
			m.workspacePendingPath = ""
		}
		m.clampWorkspaceCursor(len(changes))
		m.workspaceLoading = false
		loaded, cmd := m.loadSelectedWorkspaceItem()
		return loaded.finishWorkspaceLoad(cmd)
	case "file":
		if !m.workspaceFilesActive() {
			return m, nil
		}
		m.workspaceFile = msg.file
		m.workspaceImage = msg.image
		m.workspaceImageWidth = msg.width
		m.workspaceImageHeight = msg.height
		m.workspacePreviewLoading = false
		m = m.moveWorkspacePreview(0)
		width, height := m.workspaceImageDimensions()
		if msg.file.Image && (msg.width != width || msg.height != height) {
			m.workspacePreviewRequest++
			m.workspacePreviewLoading = true
			return m, m.renderWorkspaceImageCmd(m.workspacePreviewRequest, msg.file, width, height)
		}
	case "image":
		if !m.workspaceFilesActive() || msg.file.Path != m.workspaceFile.Path {
			return m, nil
		}
		m.workspaceImage = msg.image
		m.workspaceImageWidth = msg.width
		m.workspaceImageHeight = msg.height
		m.workspacePreviewLoading = false
	case "diff":
		if !m.workspaceCommitActive() {
			return m, nil
		}
		m.workspaceDiff = msg.diff
		m.workspaceDiffRows = msg.rows
		m.workspaceDiffWidth = msg.width
		m.workspacePreviewLoading = false
		width := m.workspaceDiffRenderWidth()
		if msg.width != width {
			m.workspacePreviewRequest++
			m.workspacePreviewLoading = true
			return m, m.renderWorkspaceDiffCmd(m.workspacePreviewRequest, msg.diff, width)
		}
	case "diff-render":
		if !m.workspaceCommitActive() || msg.diff.Path != m.workspaceDiff.Path {
			return m, nil
		}
		m.workspaceDiffRows = msg.rows
		m.workspaceDiffWidth = msg.width
		m.workspacePreviewLoading = false
	}
	return m, nil
}

func (m Model) handleWorkspaceActionResult(msg workspaceActionResultMsg) (tea.Model, tea.Cmd) {
	if msg.request != m.workspaceRequest || !m.workspaceCommitActive() {
		return m, nil
	}
	m.actionBusy = false
	if msg.err != nil {
		m.err = msg.err
		m.status = msg.action + " failed"
		m.workspacePendingPath = ""
		if msg.action == "commit" {
			return m, m.commitMessage.Focus()
		}
		return m, nil
	}
	m.status = msg.action + " completed"
	if msg.action == "commit" {
		m.commitMessage.SetValue("")
		m.commitMessage.Blur()
	}
	m.workspaceLoading = false
	return m.startWorkspaceLoad()
}

func (m Model) loadSelectedWorkspaceItem() (Model, tea.Cmd) {
	m.workspacePreviewLoading = false
	m.workspacePreviewErr = nil
	m.workspacePreviewRequest++
	m.workspaceImage = ""
	m.workspaceImageWidth = 0
	m.workspaceImageHeight = 0
	if m.workspaceFilesActive() {
		entries := m.filteredWorkspaceEntries()
		if len(entries) == 0 || entries[m.workspaceCursor].IsDir {
			m.workspacePreviewOffset = 0
			m.workspaceFile = worktree.File{}
			return m, nil
		}
		if entries[m.workspaceCursor].Path != m.workspaceFile.Path {
			m.workspacePreviewOffset = 0
		}
		m.workspacePreviewLoading = true
		return m, m.fetchWorkspaceFileCmd(m.workspacePreviewRequest, entries[m.workspaceCursor].Path)
	}
	m.workspaceDiffRows = nil
	m.workspaceDiffWidth = 0
	changes := m.filteredWorkspaceChanges()
	if len(changes) == 0 {
		m.workspacePreviewOffset = 0
		m.workspaceDiff = worktree.Diff{}
		return m, nil
	}
	selected := changes[m.workspaceCursor]
	if selected.isDir {
		m.workspacePreviewOffset = 0
		m.workspaceDiff = worktree.Diff{}
		return m, nil
	}
	if selected.displayPath() != m.workspaceDiff.Path {
		m.workspacePreviewOffset = 0
	}
	m.workspacePreviewLoading = true
	return m, m.fetchWorkspaceDiffCmd(m.workspacePreviewRequest, selected.displayPath(), selected.staged)
}

func (m Model) filteredWorkspaceEntries() []worktree.Entry {
	displays := m.filteredWorkspaceEntryDisplays()
	result := make([]worktree.Entry, 0, len(displays))
	for _, display := range displays {
		result = append(result, display.entry)
	}
	return result
}

type workspaceEntryDisplay struct {
	entry worktree.Entry
	depth int
}

func (m Model) filteredWorkspaceEntryDisplays() []workspaceEntryDisplay {
	visible := m.visibleWorkspaceEntryDisplays()
	query := strings.ToLower(strings.TrimSpace(m.fileFilter.Value()))
	if query == "" {
		return visible
	}
	keep := make(map[string]bool)
	for _, display := range visible {
		entry := display.entry
		if !strings.Contains(strings.ToLower(entry.Path), query) {
			continue
		}
		keep[entry.Path] = true
		for parent := entry.Path; ; {
			separator := strings.LastIndex(parent, "/")
			if separator < 0 {
				break
			}
			parent = parent[:separator]
			keep[parent] = true
		}
	}
	result := make([]workspaceEntryDisplay, 0, len(visible))
	for _, display := range visible {
		if keep[display.entry.Path] {
			result = append(result, display)
		}
	}
	return result
}

func (m Model) visibleWorkspaceEntries() []worktree.Entry {
	displays := m.visibleWorkspaceEntryDisplays()
	result := make([]worktree.Entry, 0, len(displays))
	for _, display := range displays {
		result = append(result, display.entry)
	}
	return result
}

func (m Model) visibleWorkspaceEntryDisplays() []workspaceEntryDisplay {
	children := make(map[string][]worktree.Entry)
	for _, entry := range m.workspaceEntries {
		parent := workspaceParent(entry.Path)
		children[parent] = append(children[parent], entry)
	}
	for parent := range children {
		sort.Slice(children[parent], func(i, j int) bool { return children[parent][i].Path < children[parent][j].Path })
	}
	result := make([]workspaceEntryDisplay, 0, len(m.workspaceEntries))
	var appendChildren func(string, int)
	appendChildren = func(parent string, depth int) {
		for _, entry := range children[parent] {
			display := entry
			for display.IsDir && m.workspaceExpanded[display.Path] && len(children[display.Path]) == 1 {
				child := children[display.Path][0]
				display.Name += "/" + child.Name
				display.Path = child.Path
				display.IsDir = child.IsDir
			}
			result = append(result, workspaceEntryDisplay{entry: display, depth: depth})
			if display.IsDir && m.workspaceExpanded[display.Path] {
				appendChildren(display.Path, depth+1)
			}
		}
	}
	appendChildren("", 0)
	return result
}

func (m *Model) replaceWorkspaceDirectory(dir string, entries []worktree.Entry) {
	newPaths := make(map[string]bool, len(entries))
	for _, entry := range entries {
		newPaths[entry.Path] = true
	}
	removedDirs := make([]string, 0)
	for _, entry := range m.workspaceEntries {
		if workspaceParent(entry.Path) == dir && !newPaths[entry.Path] && entry.IsDir {
			removedDirs = append(removedDirs, entry.Path)
		}
	}
	kept := make([]worktree.Entry, 0, len(m.workspaceEntries)+len(entries))
	for _, entry := range m.workspaceEntries {
		if workspaceParent(entry.Path) == dir {
			continue
		}
		removed := false
		for _, removedDir := range removedDirs {
			if strings.HasPrefix(entry.Path, removedDir+"/") {
				removed = true
				break
			}
		}
		if !removed {
			kept = append(kept, entry)
		}
	}
	for _, removedDir := range removedDirs {
		m.forgetWorkspaceDirectory(removedDir)
	}
	m.workspaceEntries = append(kept, entries...)
	sort.Slice(m.workspaceEntries, func(i, j int) bool { return m.workspaceEntries[i].Path < m.workspaceEntries[j].Path })
}

func (m *Model) forgetWorkspaceDirectory(dir string) {
	prefix := dir + "/"
	for loaded := range m.workspaceLoaded {
		if loaded == dir || strings.HasPrefix(loaded, prefix) {
			delete(m.workspaceLoaded, loaded)
			delete(m.workspaceExpanded, loaded)
		}
	}
}

func workspaceParent(path string) string {
	if slash := strings.LastIndex(path, "/"); slash >= 0 {
		return path[:slash]
	}
	return ""
}

func (m Model) workspaceDirectoryExists(path string) bool {
	for _, entry := range m.workspaceEntries {
		if entry.Path == path && entry.IsDir {
			return true
		}
	}
	return false
}

func (m Model) toggleWorkspaceDirectory() (Model, tea.Cmd) {
	if !m.workspaceFilesActive() {
		return m, nil
	}
	entries := m.filteredWorkspaceEntries()
	if len(entries) == 0 || m.workspaceCursor >= len(entries) || !entries[m.workspaceCursor].IsDir {
		return m, nil
	}
	dir := entries[m.workspaceCursor].Path
	if m.workspaceExpanded[dir] {
		m.workspaceExpanded[dir] = false
		m.workspaceLoaded[dir] = false
		m.clampWorkspaceCursor(len(m.filteredWorkspaceEntries()))
		return m, nil
	}
	if m.workspaceLoaded[dir] {
		m.workspaceExpanded[dir] = true
		commands := make([]tea.Cmd, 0)
		for _, entry := range m.workspaceEntries {
			if workspaceParent(entry.Path) == dir && entry.IsDir && !m.workspaceLoaded[entry.Path] {
				commands = append(commands, m.fetchWorkspaceEntriesCmd(m.workspaceEntryRequest+1, entry.Path, true))
			}
		}
		if len(commands) == 0 {
			return m, nil
		}
		m.workspaceEntryRequest++
		m.workspaceEntryPending = len(commands)
		m.workspaceLoading = true
		m.err = nil
		if len(commands) == 1 {
			return m, commands[0]
		}
		return m, tea.Batch(commands...)
	}
	m.workspaceEntryRequest++
	m.workspaceEntryPending = 1
	m.workspaceLoading = true
	m.err = nil
	return m, m.fetchWorkspaceEntriesCmd(m.workspaceEntryRequest, dir, true)
}

func (m Model) filteredWorkspaceChanges() []workspaceChange {
	query := strings.ToLower(strings.TrimSpace(m.fileFilter.Value()))
	staged, changes := sortedChangeGroups(m.workspaceStatus, query)
	result := make([]workspaceChange, 0, len(staged)+len(changes))
	result = append(result, m.visibleWorkspaceChangeTree(workspaceChangeTree(staged, true))...)
	result = append(result, m.visibleWorkspaceChangeTree(workspaceChangeTree(changes, false))...)
	return result
}

func (m Model) visibleWorkspaceChangeTree(changes []workspaceChange) []workspaceChange {
	result := make([]workspaceChange, 0, len(changes))
	for _, change := range changes {
		hidden := false
		for parent := workspaceParent(change.path); parent != ""; parent = workspaceParent(parent) {
			if m.workspaceChangeCollapsed[workspaceChangeExpansionKey(change.staged, parent)] {
				hidden = true
				break
			}
		}
		if !hidden {
			result = append(result, change)
		}
	}
	return result
}

func workspaceChangeExpansionKey(staged bool, path string) string {
	if staged {
		return "staged:" + path
	}
	return "changes:" + path
}

func (m Model) toggleWorkspaceChangeDirectory() Model {
	changes := m.filteredWorkspaceChanges()
	if m.workspaceCursor < 0 || m.workspaceCursor >= len(changes) || !changes[m.workspaceCursor].isDir {
		return m
	}
	selected := changes[m.workspaceCursor]
	key := workspaceChangeExpansionKey(selected.staged, selected.path)
	m.workspaceChangeCollapsed[key] = !m.workspaceChangeCollapsed[key]
	m.clampWorkspaceCursor(len(m.filteredWorkspaceChanges()))
	return m
}

func workspaceChangeTree(changes []worktree.Change, staged bool) []workspaceChange {
	directories := make(map[string]struct{})
	for _, change := range changes {
		parts := strings.Split(change.Path, "/")
		for index := 1; index < len(parts); index++ {
			directories[strings.Join(parts[:index], "/")] = struct{}{}
		}
	}
	result := make([]workspaceChange, 0, len(changes)+len(directories))
	for path := range directories {
		result = append(result, newWorkspaceChange(path, staged, true, worktree.Change{}))
	}
	for _, change := range changes {
		result = append(result, newWorkspaceChange(change.Path, staged, false, change))
	}
	sort.SliceStable(result, func(i, j int) bool { return result[i].path < result[j].path })
	return compressWorkspaceChangeTree(result)
}

func compressWorkspaceChangeTree(changes []workspaceChange) []workspaceChange {
	children := make(map[string][]workspaceChange)
	for _, change := range changes {
		children[workspaceParent(change.path)] = append(children[workspaceParent(change.path)], change)
	}
	result := make([]workspaceChange, 0, len(changes))
	var appendChildren func(string, int)
	appendChildren = func(parent string, depth int) {
		for _, change := range children[parent] {
			display := change
			for display.isDir && len(children[display.path]) == 1 {
				child := children[display.path][0]
				display.name += "/" + child.name
				display.path = child.path
				display.change = child.change
				display.isDir = child.isDir
			}
			display.depth = depth
			result = append(result, display)
			if display.isDir {
				appendChildren(display.path, depth+1)
			}
		}
	}
	appendChildren("", 0)
	return result
}

func newWorkspaceChange(path string, staged, isDir bool, change worktree.Change) workspaceChange {
	name := path
	if slash := strings.LastIndex(path, "/"); slash >= 0 {
		name = path[slash+1:]
	}
	return workspaceChange{change: change, staged: staged, path: path, name: name, depth: strings.Count(path, "/"), isDir: isDir}
}

func (m *Model) clampWorkspaceCursor(length int) {
	if length == 0 {
		m.workspaceCursor, m.workspaceOffset = 0, 0
		return
	}
	if m.workspaceCursor >= length {
		m.workspaceCursor = length - 1
	}
	if m.workspaceCursor < 0 {
		m.workspaceCursor = 0
	}
	m.ensureWorkspaceCursorVisible()
}

func (m *Model) ensureWorkspaceCursorVisible() {
	height := m.workspaceListHeight()
	if m.workspaceCommitActive() {
		height = max(1, height-2)
	}
	if m.workspaceCursor < m.workspaceOffset {
		m.workspaceOffset = m.workspaceCursor
	}
	if m.workspaceCursor >= m.workspaceOffset+height {
		m.workspaceOffset = m.workspaceCursor - height + 1
	}
}

func (m Model) workspaceListHeight() int {
	overhead := 11
	if m.workspaceCommitActive() {
		overhead++
	}
	return max(1, m.height-overhead)
}

func (m Model) workspaceChangeRows() []workspaceChangeRow {
	changes := m.filteredWorkspaceChanges()
	start := min(m.workspaceOffset, len(changes))
	end := min(len(changes), start+m.workspaceListHeight())
	rows := make([]workspaceChangeRow, 0, end-start+2)
	lastStaged := false
	haveGroup := false
	for index := start; index < end; index++ {
		change := changes[index]
		if !haveGroup || change.staged != lastStaged {
			title := "Changes"
			if change.staged {
				title = "Staged Changes"
			}
			count := 0
			for _, candidate := range changes {
				if candidate.staged == change.staged && !candidate.isDir {
					count++
				}
			}
			rows = append(rows, workspaceChangeRow{title: fmt.Sprintf("%s (%d)", title, count), index: -1})
			lastStaged, haveGroup = change.staged, true
		}
		rows = append(rows, workspaceChangeRow{index: index, item: change})
	}
	return rows[:min(len(rows), m.workspaceListHeight())]
}

func (m Model) workspaceChangeIndexAtRow(row int) int {
	rows := m.workspaceChangeRows()
	if row < 0 || row >= len(rows) {
		return -1
	}
	return rows[row].index
}

func (m Model) moveWorkspaceCursor(delta int) (Model, tea.Cmd) {
	length := len(m.filteredWorkspaceEntries())
	if m.workspaceCommitActive() {
		length = len(m.filteredWorkspaceChanges())
	}
	if length == 0 {
		return m, nil
	}
	m.workspaceCursor = min(length-1, max(0, m.workspaceCursor+delta))
	m.ensureWorkspaceCursorVisible()
	m.err = nil
	return m.loadSelectedWorkspaceItem()
}

func workspacePaneWidths(total int) (left, right int) {
	return workspacePaneWidthsAt(total, 0)
}

const workspacePaneMinWidth = 10

func workspacePaneWidthsAt(total int, ratio float64) (left, right int) {
	available := max(2, total-3)
	if ratio > 0 {
		left = int(float64(available)*ratio + 0.5)
	} else {
		left = 42
		if total < 64 {
			left = max(workspacePaneMinWidth, (total-3)/2)
		}
	}
	if available >= workspacePaneMinWidth*2 {
		left = min(available-workspacePaneMinWidth, max(workspacePaneMinWidth, left))
	} else {
		left = min(available-1, max(1, left))
	}
	right = max(1, available-left)
	return left, right
}

func (m Model) workspacePaneWidths() (left, right int) {
	return workspacePaneWidthsAt(max(1, m.width-2), m.workspaceSplitRatio)
}

func (m Model) workspaceDiffRenderWidth() int {
	if m.workspaceCommitActive() {
		_, rightWidth := m.commitDashboardWidths()
		return max(1, rightWidth-2)
	}
	_, width := m.workspacePaneWidths()
	return width
}

func (m Model) resizeWorkspaceDivider(x int) (Model, tea.Cmd) {
	if m.workspaceCommitActive() {
		m.workspaceCommitWidth = min(max(24, x-1), max(24, m.width-26))
		if m.workspaceDiff.Path != "" {
			m.workspacePreviewRequest++
			m.workspacePreviewLoading = true
			return m, m.renderWorkspaceDiffCmd(m.workspacePreviewRequest, m.workspaceDiff, m.workspaceDiffRenderWidth())
		}
		return m, nil
	}
	available := max(2, m.width-5)
	dragRatio := max(1.0/float64(available), float64(x-1)/float64(available))
	left, _ := workspacePaneWidthsAt(max(1, m.width-2), dragRatio)
	m.workspaceSplitRatio = float64(left) / float64(available)

	if m.workspaceFilesActive() && m.workspaceFile.Image {
		width, height := m.workspaceImageDimensions()
		if width != m.workspaceImageWidth || height != m.workspaceImageHeight {
			m.workspacePreviewRequest++
			m.workspacePreviewLoading = true
			return m, m.renderWorkspaceImageCmd(m.workspacePreviewRequest, m.workspaceFile, width, height)
		}
	}
	if m.workspaceCommitActive() && m.workspaceDiff.Path != "" {
		_, width := m.workspacePaneWidths()
		if width != m.workspaceDiffWidth {
			m.workspacePreviewRequest++
			m.workspacePreviewLoading = true
			return m, m.renderWorkspaceDiffCmd(m.workspacePreviewRequest, m.workspaceDiff, width)
		}
	}
	return m, nil
}

func (m Model) workspacePreviewLineCount() int {
	if m.workspaceFilesActive() {
		if m.workspaceFile.Path == "" {
			return 1
		}
		if m.workspaceFile.Image || m.workspaceFile.Binary || !utf8.Valid(m.workspaceFile.Data) {
			return 2
		}
		if isMarkdownPath(m.workspaceFile.Path) {
			_, width := m.workspacePaneWidths()
			content := renderWorkspaceMarkdownContent(m.workspaceFile.Data, width)
			return 1 + len(strings.Split(content, "\n"))
		}
		return 2 + bytes.Count(m.workspaceFile.Data, []byte{'\n'})
	}
	if len(m.workspaceDiffRows) == 0 {
		return 1
	}
	return len(m.workspaceDiffRows)
}

func (m Model) moveWorkspacePreview(delta int) Model {
	maximum := max(0, m.workspacePreviewLineCount()-m.workspaceListHeight())
	m.workspacePreviewOffset = min(maximum, max(0, m.workspacePreviewOffset+delta))
	return m
}

func (m Model) updateWorkspace(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if next, cmd, handled := m.updateWorkspaceFocus(msg); handled {
		return next, cmd
	}
	if m.workspaceCommitActive() && m.commitMessage.Focused() {
		switch msg.String() {
		case "esc":
			m.commitMessage.Blur()
			return m, nil
		case "enter":
			return m.insertCommitMessageNewline(), nil
		case "ctrl+s":
			return m.startWorkspaceCommit()
		}
		var cmd tea.Cmd
		m.commitMessage, cmd = m.commitMessage.Update(msg)
		return m, cmd
	}
	if m.fileFilter.Focused() {
		if msg.String() == "esc" {
			m.fileFilter.Blur()
			return m, nil
		}
		old := m.fileFilter.Value()
		var cmd tea.Cmd
		m.fileFilter, cmd = m.fileFilter.Update(msg)
		if old != m.fileFilter.Value() {
			m.workspaceCursor, m.workspaceOffset = 0, 0
			loaded, loadCmd := m.loadSelectedWorkspaceItem()
			m = loaded
			return m, tea.Batch(cmd, loadCmd)
		}
		return m, cmd
	}
	msg = normalizeShortcutKey(msg)
	if m.actionBusy {
		return m, nil
	}
	switch msg.String() {
	case "q":
		return m, tea.Quit
	case "/":
		return m, m.fileFilter.Focus()
	case "c", "C":
		if m.workspaceCommitActive() {
			return m, m.commitMessage.Focus()
		}
	case "tab", "]":
		m.active = (m.active + 1) % m.tabCount()
		return m.startActiveTabLoad()
	case "shift+tab", "[":
		m.active = (m.active - 1 + m.tabCount()) % m.tabCount()
		return m.startActiveTabLoad()
	case "1", "2", "3", "4", "5", "6", "7", "8":
		index := int(msg.Runes[0] - '1')
		if index < m.tabCount() {
			m.active = index
			return m.startActiveTabLoad()
		}
	case "!", "@", "#", "$", "%", "^", "&", "*":
		shiftTabs := map[string]int{"!": 0, "@": 1, "#": 2, "$": 3, "%": 4, "^": 5, "&": 6, "*": 7}
		if index := shiftTabs[msg.String()]; index < m.tabCount() {
			m.active = index
			return m.startActiveTabLoad()
		}
	case "up", "k":
		return m.moveWorkspaceCursor(-1)
	case "down", "j":
		return m.moveWorkspaceCursor(1)
	case "pgup", "ctrl+u":
		return m.moveWorkspacePreview(-max(1, m.workspaceListHeight()/2)), nil
	case "pgdown", "ctrl+d":
		return m.moveWorkspacePreview(max(1, m.workspaceListHeight()/2)), nil
	case "home":
		m.workspaceCursor, m.workspaceOffset = 0, 0
		return m.loadSelectedWorkspaceItem()
	case "end":
		length := len(m.filteredWorkspaceEntries())
		if m.workspaceCommitActive() {
			length = len(m.filteredWorkspaceChanges())
		}
		m.workspaceCursor = max(0, length-1)
		m.ensureWorkspaceCursorVisible()
		return m.loadSelectedWorkspaceItem()
	case "enter", "right", "l":
		if m.workspaceCommitActive() {
			return m.toggleWorkspaceChangeDirectory(), nil
		}
		return m.toggleWorkspaceDirectory()
	case "left", "h":
		if m.workspaceFilesActive() {
			entries := m.filteredWorkspaceEntries()
			if len(entries) > 0 && entries[m.workspaceCursor].IsDir && m.workspaceExpanded[entries[m.workspaceCursor].Path] {
				return m.toggleWorkspaceDirectory()
			}
		} else {
			changes := m.filteredWorkspaceChanges()
			if len(changes) > 0 && changes[m.workspaceCursor].isDir && !m.workspaceChangeCollapsed[workspaceChangeExpansionKey(changes[m.workspaceCursor].staged, changes[m.workspaceCursor].path)] {
				return m.toggleWorkspaceChangeDirectory(), nil
			}
		}
	case "r":
		m.workspaceLoading = false
		return m.startWorkspaceLoad()
	case " ", "s", "u":
		if m.workspaceCommitActive() {
			return m.toggleWorkspaceStage(msg.String())
		}
	case "S", "U":
		if m.workspaceCommitActive() {
			return m.toggleAllWorkspaceStages(msg.String())
		}
	}
	return m, nil
}

func (m Model) insertCommitMessageNewline() Model {
	runes := []rune(m.commitMessage.Value())
	position := min(max(0, m.commitMessage.Position()), len(runes))
	value := make([]rune, 0, len(runes)+1)
	value = append(value, runes[:position]...)
	value = append(value, commitMessageNewlineRune)
	value = append(value, runes[position:]...)
	m.commitMessage.SetValue(string(value))
	m.commitMessage.SetCursor(position + 1)
	return m
}

const commitMessageNewlineRune = '\uE000'

func (m Model) commitMessageText() string {
	return strings.ReplaceAll(m.commitMessage.Value(), string(commitMessageNewlineRune), "\n")
}

func (m Model) startWorkspaceCommit() (tea.Model, tea.Cmd) {
	if m.actionBusy {
		return m, nil
	}
	message := strings.TrimSpace(m.commitMessageText())
	if message == "" {
		m.err = nil
		m.status = "enter a commit message"
		return m, m.commitMessage.Focus()
	}
	if len(m.workspaceStatus.Staged) == 0 {
		m.err = nil
		m.status = "stage changes before committing"
		return m, m.commitMessage.Focus()
	}
	m.actionBusy = true
	m.err = nil
	m.status = "committing…"
	request := m.workspaceRequest
	return m, func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		return workspaceActionResultMsg{request: request, action: "commit", err: m.workspace.Commit(ctx, message)}
	}
}

func (m Model) startWorkspaceRemoteAction(action string) (tea.Model, tea.Cmd) {
	if m.actionBusy || !m.workspaceRemote.Available {
		return m, nil
	}
	run := m.workspace.Pull
	if action == "push" {
		run = m.workspace.Push
	}
	m.actionBusy = true
	m.err = nil
	m.status = action + "ing…"
	request := m.workspaceRequest
	return m, func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		return workspaceActionResultMsg{request: request, action: action, err: run(ctx)}
	}
}

func (m Model) toggleWorkspaceStage(key string) (tea.Model, tea.Cmd) {
	changes := m.filteredWorkspaceChanges()
	if len(changes) == 0 {
		return m, nil
	}
	selected := changes[m.workspaceCursor]
	if key == "s" && selected.staged || key == "u" && !selected.staged {
		return m, nil
	}
	unstage := selected.staged
	if key == "s" {
		unstage = false
	}
	if key == "u" {
		unstage = true
	}
	return m.startWorkspacePathStage(selected.displayPath(), unstage)
}

func (m Model) startWorkspacePathStage(path string, unstage bool) (tea.Model, tea.Cmd) {
	action := "stage"
	run := m.workspace.Stage
	if unstage {
		action = "unstage"
		run = m.workspace.Unstage
	}
	m.actionBusy = true
	m.workspacePendingPath = path
	m.status = action + " " + sanitizeWorkspaceLabel(path) + "…"
	request := m.workspaceRequest
	return m, func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		return workspaceActionResultMsg{request: request, action: action, err: run(ctx, path)}
	}
}

func (m Model) selectDashboardChange(selected workspaceChange) (tea.Model, tea.Cmd) {
	if selected.isDir {
		key := workspaceChangeExpansionKey(selected.staged, selected.path)
		m.workspaceChangeCollapsed[key] = !m.workspaceChangeCollapsed[key]
		return m, nil
	}
	changes := m.filteredWorkspaceChanges()
	for index, change := range changes {
		if change.staged == selected.staged && change.displayPath() == selected.displayPath() {
			m.workspaceCursor = index
			break
		}
	}
	m.focus = focusWorkspaceList
	return m.loadSelectedWorkspaceItem()
}

func (m Model) toggleAllWorkspaceStages(key string) (tea.Model, tea.Cmd) {
	action := "stage all"
	run := m.workspace.StageAll
	if key == "U" {
		action = "unstage all"
		run = m.workspace.UnstageAll
	}
	m.actionBusy = true
	m.workspacePendingPath = ""
	m.status = action + "…"
	request := m.workspaceRequest
	return m, func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		return workspaceActionResultMsg{request: request, action: action, err: run(ctx)}
	}
}

func renderWorkspaceFile(file worktree.File, width, height int) string {
	return renderWorkspaceFileAt(file, width, height, 0)
}

func renderWorkspaceFileAt(file worktree.File, width, height, offset int) string {
	return renderWorkspaceFileWithImageAt(file, "", width, height, offset)
}

func renderWorkspaceFileWithImageAt(file worktree.File, image string, width, height, offset int) string {
	if file.Path == "" {
		return kittyDeleteImage() + metaStyle.Render("Select a file to preview it.")
	}
	header := sectionTitleStyle.Render(sanitizeWorkspaceLabel(file.Path)) + "\n"
	if file.Truncated {
		return kittyDeleteImage() + header + metaStyle.Render(fmt.Sprintf("File is larger than the %d MiB preview limit.", len(file.Data)/(1<<20)))
	}
	if file.Image {
		if image != "" {
			return header + image
		}
		return kittyDeleteImage() + header + metaStyle.Render(fmt.Sprintf("Image preview unavailable in this terminal · %s · %d bytes", file.MIME, len(file.Data)))
	}
	if file.Binary || !utf8.Valid(file.Data) {
		return kittyDeleteImage() + header + metaStyle.Render(fmt.Sprintf("Binary file · %s · %d bytes", file.MIME, len(file.Data)))
	}
	if isMarkdownPath(file.Path) {
		preview := kittyDeleteImage() + header + renderWorkspaceMarkdown(file.Data, width, height, offset)
		if image != "" {
			preview += "\n" + image
		}
		return preview
	}
	content := sanitizeWorkspaceText(strings.ReplaceAll(string(file.Data), "\r\n", "\n"))
	lines := strings.Split(content, "\n")
	offset = min(max(0, offset), max(0, len(lines)-1))
	lines = lines[offset:]
	if height > 1 {
		lines = lines[:min(len(lines), height-1)]
	}
	highlighter := newCodeHighlighter(file.Path)
	for i := range lines {
		// Terminals advance a tab to the next tab stop, while ANSI width helpers
		// cannot infer that position reliably. Expand tabs before highlighting and
		// truncating so a Preview row never wraps past its panel border.
		lines[i] = truncate(highlighter.line(expandWorkspaceTabs(lines[i], 4)), max(1, width))
	}
	return kittyDeleteImage() + header + strings.Join(lines, "\n")
}

func expandWorkspaceTabs(line string, tabWidth int) string {
	if !strings.ContainsRune(line, '\t') {
		return line
	}
	tabWidth = max(1, tabWidth)
	var expanded strings.Builder
	column := 0
	for _, value := range line {
		if value == '\t' {
			spaces := tabWidth - column%tabWidth
			expanded.WriteString(strings.Repeat(" ", spaces))
			column += spaces
			continue
		}
		expanded.WriteRune(value)
		column += max(0, ansi.StringWidth(string(value)))
	}
	return expanded.String()
}

var markdownImagePattern = regexp.MustCompile(`!\[[^\]]*\]\(([^\s)]+)(?:\s+['"][^'"]*['"])?\)`)

func firstLocalMarkdownImage(markdownPath string, data []byte) string {
	match := markdownImagePattern.FindSubmatch(data)
	if len(match) < 2 {
		return ""
	}
	target := strings.TrimSpace(string(match[1]))
	if target == "" || strings.Contains(target, "://") || strings.HasPrefix(target, "data:") || strings.HasPrefix(target, "/") {
		return ""
	}
	if index := strings.IndexByte(target, '#'); index >= 0 {
		target = target[:index]
	}
	return filepath.ToSlash(filepath.Clean(filepath.Join(filepath.Dir(markdownPath), target)))
}

func isMarkdownPath(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".md", ".markdown", ".mdown", ".mkd":
		return true
	default:
		return false
	}
}

func renderWorkspaceMarkdown(data []byte, width, height, offset int) string {
	content := renderWorkspaceMarkdownContent(data, width)
	lines := strings.Split(content, "\n")
	offset = min(max(0, offset), max(0, len(lines)-1))
	lines = lines[offset:]
	if height > 1 {
		lines = lines[:min(len(lines), height-1)]
	}
	return strings.Join(lines, "\n")
}

func renderWorkspaceMarkdownContent(data []byte, width int) string {
	content := sanitizeWorkspaceText(strings.ReplaceAll(string(data), "\r\n", "\n"))
	renderer, err := glamour.NewTermRenderer(
		glamour.WithStandardStyle("dark"),
		glamour.WithWordWrap(max(10, width)),
		glamour.WithPreservedNewLines(),
	)
	if err == nil {
		if rendered, renderErr := renderer.Render(content); renderErr == nil {
			content = strings.TrimSuffix(rendered, "\n")
		}
	}
	return content
}

func cropWorkspaceRows(rows []string, height, offset int) string {
	if height <= 0 {
		return ""
	}
	offset = min(max(0, offset), max(0, len(rows)-1))
	return strings.Join(rows[offset:min(len(rows), offset+height)], "\n")
}

func kittyImage(data []byte, width, height int) (string, bool) {
	if !kittyGraphicsAvailable() {
		return "", false
	}
	if config, _, configErr := image.DecodeConfig(bytes.NewReader(data)); configErr == nil {
		if config.Width <= 0 || config.Height <= 0 || int64(config.Width)*int64(config.Height) > 50_000_000 {
			return "", false
		}
	}
	decoded, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		converted, convertErr := rasterizeSVG(data, width, height)
		if convertErr != nil {
			return "", false
		}
		decoded, _, err = image.Decode(bytes.NewReader(converted))
		if err != nil {
			return "", false
		}
	}
	var pngData bytes.Buffer
	if err := png.Encode(&pngData, decoded); err != nil {
		return "", false
	}
	bounds := decoded.Bounds()
	config := image.Config{Width: bounds.Dx(), Height: bounds.Dy()}
	if config.Width <= 0 || config.Height <= 0 || int64(config.Width)*int64(config.Height) > 50_000_000 {
		return "", false
	}
	cellWidth, cellHeight := terminalCellPixels()
	placement := kittyImagePlacement(config, width, height, cellWidth, cellHeight)
	encoded := base64.StdEncoding.EncodeToString(pngData.Bytes())
	const chunk = 4096
	parts := []string{kittyDeleteImage()}
	for len(encoded) > 0 {
		n := min(chunk, len(encoded))
		more := 0
		if n < len(encoded) {
			more = 1
		}
		prefix := fmt.Sprintf("\x1b_Gq=2,m=%d;", more)
		if len(parts) == 1 {
			prefix = fmt.Sprintf("\x1b_Ga=T,f=100,q=2,i=31,C=1%s,m=%d;", placement, more)
		}
		parts = append(parts, prefix+encoded[:n]+"\x1b\\")
		encoded = encoded[n:]
	}
	return strings.Join(parts, ""), true
}

func kittyImagePlacement(config image.Config, width, height, cellWidth, cellHeight int) string {
	maxPixelWidth := max(1, width) * max(1, cellWidth)
	maxPixelHeight := max(1, height) * max(1, cellHeight)
	if config.Width <= maxPixelWidth && config.Height <= maxPixelHeight {
		return ""
	}
	if int64(maxPixelWidth)*int64(config.Height) <= int64(maxPixelHeight)*int64(config.Width) {
		return fmt.Sprintf(",c=%d", max(1, width))
	}
	return fmt.Sprintf(",r=%d", max(1, height))
}

func terminalCellPixels() (int, int) {
	const fallbackWidth, fallbackHeight = 10, 20
	winsize, err := unix.IoctlGetWinsize(int(os.Stdout.Fd()), unix.TIOCGWINSZ)
	if err != nil || winsize.Col == 0 || winsize.Row == 0 || winsize.Xpixel == 0 || winsize.Ypixel == 0 {
		return fallbackWidth, fallbackHeight
	}
	width := int(winsize.Xpixel) / int(winsize.Col)
	height := int(winsize.Ypixel) / int(winsize.Row)
	if width <= 0 || height <= 0 {
		return fallbackWidth, fallbackHeight
	}
	return width, height
}

func kittyGraphicsAvailable() bool {
	if os.Getenv("TMUX") != "" {
		return false
	}
	return os.Getenv("KITTY_WINDOW_ID") != "" || strings.Contains(strings.ToLower(os.Getenv("TERM")), "kitty")
}

func kittyDeleteImage() string {
	if !kittyGraphicsAvailable() {
		return ""
	}
	// Some Kitty-compatible terminals advance or otherwise disturb the cursor
	// while processing a delete command. Preserve the cursor so selecting a
	// regular file after a directory/image cannot scroll the full-screen TUI.
	return "\x1b7\x1b_Ga=d,d=i,i=31,q=2\x1b\\\x1b8"
}

func rasterizeSVG(data []byte, width, height int) ([]byte, error) {
	trimmed := bytes.TrimSpace(data)
	if !bytes.Contains(bytes.ToLower(trimmed[:min(len(trimmed), 512)]), []byte("<svg")) {
		return nil, fmt.Errorf("not an SVG image")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	pixelWidth, pixelHeight := strconv.Itoa(max(1, width*10)), strconv.Itoa(max(1, height*20))
	if converter, err := exec.LookPath("rsvg-convert"); err == nil {
		cmd := exec.CommandContext(ctx, converter, "-w", pixelWidth, "-h", pixelHeight, "-a", "-f", "png")
		cmd.Stdin = bytes.NewReader(data)
		return cmd.Output()
	}
	if converter, err := exec.LookPath("magick"); err == nil {
		cmd := exec.CommandContext(ctx, converter, "svg:-", "-resize", pixelWidth+"x"+pixelHeight+">", "png:-")
		cmd.Stdin = bytes.NewReader(data)
		return cmd.Output()
	}
	return nil, fmt.Errorf("no SVG rasterizer is installed")
}

func renderWorkspaceDiff(diff worktree.Diff, width int) string {
	if diff.Path == "" {
		return kittyDeleteImage() + metaStyle.Render("Select a changed file to inspect its diff.")
	}
	if diff.Binary {
		return kittyDeleteImage() + sectionTitleStyle.Render(sanitizeWorkspaceLabel(diff.Path)) + "\n" + metaStyle.Render("Binary files differ.")
	}
	path := sanitizeWorkspaceLabel(diff.Path)
	file := provider.DiffFile{
		OldPath: path,
		NewPath: path,
		Lines:   provider.ParseUnifiedDiffLines(sanitizeWorkspaceText(diff.Patch)),
	}
	return kittyDeleteImage() + renderDiffFile([]provider.DiffFile{file}, 0, -1, -1, width)
}

func diffLineParts(line string) (string, string) {
	if line == "" {
		return "", ""
	}
	switch line[0] {
	case '+', '-', ' ':
		return line[:1], line[1:]
	default:
		return "", line
	}
}

func padRight(value string, width int) string {
	return value + strings.Repeat(" ", max(0, width-lipgloss.Width(value)))
}

func sanitizeWorkspaceText(value string) string {
	return strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' || !unicode.IsControl(r) && !unicode.Is(unicode.Cf, r) {
			return r
		}
		return -1
	}, value)
}

func sanitizeWorkspaceLabel(value string) string {
	return strings.Map(func(r rune) rune {
		if r == '\n' || r == '\r' || r == '\t' {
			return ' '
		}
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			return -1
		}
		return r
	}, value)
}

func sortedChangeGroups(status worktree.Status, query string) (staged, changes []worktree.Change) {
	match := func(path string) bool { return query == "" || strings.Contains(strings.ToLower(path), query) }
	for _, change := range status.Staged {
		if match(change.Path) {
			staged = append(staged, change)
		}
	}
	for _, group := range [][]worktree.Change{status.Unstaged, status.Untracked} {
		for _, change := range group {
			if match(change.Path) {
				changes = append(changes, change)
			}
		}
	}
	sort.SliceStable(staged, func(i, j int) bool { return staged[i].Path < staged[j].Path })
	sort.SliceStable(changes, func(i, j int) bool { return changes[i].Path < changes[j].Path })
	return staged, changes
}
