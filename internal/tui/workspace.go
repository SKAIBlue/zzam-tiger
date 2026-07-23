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
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/jwmtp2/gtui/internal/worktree"
)

type workspaceClient interface {
	Root() string
	Entries(context.Context, string) ([]worktree.Entry, error)
	Read(context.Context, string) (worktree.File, error)
	Status(context.Context) (worktree.Status, error)
	Stage(context.Context, string) error
	Unstage(context.Context, string) error
	Diff(context.Context, string, bool) (worktree.Diff, error)
}

type workspaceResultMsg struct {
	request uint64
	op      string
	entries []worktree.Entry
	file    worktree.File
	status  worktree.Status
	diff    worktree.Diff
	image   string
	rows    []string
	width   int
	height  int
	dir     string
	expand  bool
	err     error
}

type workspaceActionResultMsg struct {
	request uint64
	action  string
	err     error
}

type workspaceChange struct {
	change worktree.Change
	staged bool
}

type workspaceChangeRow struct {
	title string
	index int
	item  workspaceChange
}

func (m Model) fetchWorkspaceCmd(request uint64) tea.Cmd {
	active := m.active
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if active == 0 {
			entries, err := m.workspace.Entries(ctx, "")
			return workspaceResultMsg{request: request, op: "entries", entries: entries, err: err}
		}
		status, err := m.workspace.Status(ctx)
		return workspaceResultMsg{request: request, op: "status", status: status, err: err}
	}
}

func (m Model) fetchWorkspaceEntriesCmd(request uint64, dir string, expand bool) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		entries, err := m.workspace.Entries(ctx, dir)
		return workspaceResultMsg{request: request, op: "entries", entries: entries, dir: dir, expand: expand, err: err}
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
	_, width := workspacePaneWidths(m.width)
	return width, max(1, m.workspaceListHeight()-1)
}

func (m Model) fetchWorkspaceDiffCmd(request uint64, path string, staged bool) tea.Cmd {
	_, width := workspacePaneWidths(m.width)
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
	m.err = nil
	if m.active == 0 {
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

func (m Model) startActiveTabLoad() (Model, tea.Cmd) {
	m.loadingList = false
	m.workspaceLoading = false
	m.workspacePreviewLoading = false
	m.workspacePreviewRequest++
	m.err = nil
	if m.localTab() {
		return m.startWorkspaceLoad()
	}
	return m.startListLoad()
}

func (m Model) handleWorkspaceResult(msg workspaceResultMsg) (tea.Model, tea.Cmd) {
	if !m.localTab() {
		return m, nil
	}
	switch msg.op {
	case "entries":
		if m.active != 0 || msg.request != m.workspaceEntryRequest {
			return m, nil
		}
	case "status":
		if m.active != 1 || msg.request != m.workspaceRequest {
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
		} else if msg.op == "entries" {
			m.workspaceEntryPending = max(0, m.workspaceEntryPending-1)
			m.workspaceLoading = m.workspaceEntryPending > 0
		} else {
			m.workspaceLoading = false
		}
		m.err = msg.err
		return m, nil
	}
	m.err = nil
	m.lastUpdated = time.Now()
	switch msg.op {
	case "entries":
		if msg.dir != "" && !m.workspaceDirectoryExists(msg.dir) {
			m.workspaceEntryPending = max(0, m.workspaceEntryPending-1)
			m.workspaceLoading = m.workspaceEntryPending > 0
			return m, nil
		}
		selectedPath := ""
		selected := m.filteredWorkspaceEntries()
		if m.workspaceCursor >= 0 && m.workspaceCursor < len(selected) {
			selectedPath = selected[m.workspaceCursor].Path
		}
		m.replaceWorkspaceDirectory(msg.dir, msg.entries)
		m.workspaceLoaded[msg.dir] = true
		if msg.expand {
			m.workspaceExpanded[msg.dir] = true
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
			return m.loadSelectedWorkspaceItem()
		}
		return m, nil
	case "status":
		if m.active != 1 {
			return m, nil
		}
		m.workspaceStatus = msg.status
		changes := m.filteredWorkspaceChanges()
		if m.workspacePendingPath != "" {
			for index, change := range changes {
				if change.change.Path == m.workspacePendingPath {
					m.workspaceCursor = index
					break
				}
			}
			m.workspacePendingPath = ""
		}
		m.clampWorkspaceCursor(len(changes))
		m.workspaceLoading = false
		return m.loadSelectedWorkspaceItem()
	case "file":
		if m.active != 0 {
			return m, nil
		}
		m.workspaceFile = msg.file
		m.workspaceImage = msg.image
		m.workspaceImageWidth = msg.width
		m.workspaceImageHeight = msg.height
		m.workspacePreviewLoading = false
		width, height := m.workspaceImageDimensions()
		if msg.file.Image && (msg.width != width || msg.height != height) {
			m.workspacePreviewRequest++
			m.workspacePreviewLoading = true
			return m, m.renderWorkspaceImageCmd(m.workspacePreviewRequest, msg.file, width, height)
		}
	case "image":
		if m.active != 0 || msg.file.Path != m.workspaceFile.Path {
			return m, nil
		}
		m.workspaceImage = msg.image
		m.workspaceImageWidth = msg.width
		m.workspaceImageHeight = msg.height
		m.workspacePreviewLoading = false
	case "diff":
		if m.active != 1 {
			return m, nil
		}
		m.workspaceDiff = msg.diff
		m.workspaceDiffRows = msg.rows
		m.workspaceDiffWidth = msg.width
		m.workspacePreviewLoading = false
		_, width := workspacePaneWidths(m.width)
		if msg.width != width {
			m.workspacePreviewRequest++
			m.workspacePreviewLoading = true
			return m, m.renderWorkspaceDiffCmd(m.workspacePreviewRequest, msg.diff, width)
		}
	case "diff-render":
		if m.active != 1 || msg.diff.Path != m.workspaceDiff.Path {
			return m, nil
		}
		m.workspaceDiffRows = msg.rows
		m.workspaceDiffWidth = msg.width
		m.workspacePreviewLoading = false
	}
	return m, nil
}

func (m Model) handleWorkspaceActionResult(msg workspaceActionResultMsg) (tea.Model, tea.Cmd) {
	if msg.request != m.workspaceRequest || m.active != 1 {
		return m, nil
	}
	m.actionBusy = false
	if msg.err != nil {
		m.err = msg.err
		m.status = msg.action + " failed"
		m.workspacePendingPath = ""
		return m, nil
	}
	m.status = msg.action + " completed"
	m.workspaceLoading = false
	return m.startWorkspaceLoad()
}

func (m Model) loadSelectedWorkspaceItem() (Model, tea.Cmd) {
	m.workspacePreviewLoading = false
	m.workspacePreviewRequest++
	m.workspacePreviewOffset = 0
	m.workspaceImage = ""
	m.workspaceImageWidth = 0
	m.workspaceImageHeight = 0
	if m.active == 0 {
		entries := m.filteredWorkspaceEntries()
		if len(entries) == 0 || entries[m.workspaceCursor].IsDir {
			m.workspaceFile = worktree.File{}
			return m, nil
		}
		m.workspacePreviewLoading = true
		return m, m.fetchWorkspaceFileCmd(m.workspacePreviewRequest, entries[m.workspaceCursor].Path)
	}
	m.workspaceDiffRows = nil
	m.workspaceDiffWidth = 0
	changes := m.filteredWorkspaceChanges()
	if len(changes) == 0 {
		m.workspaceDiff = worktree.Diff{}
		return m, nil
	}
	selected := changes[m.workspaceCursor]
	m.workspacePreviewLoading = true
	return m, m.fetchWorkspaceDiffCmd(m.workspacePreviewRequest, selected.change.Path, selected.staged)
}

func (m Model) filteredWorkspaceEntries() []worktree.Entry {
	visible := m.visibleWorkspaceEntries()
	query := strings.ToLower(strings.TrimSpace(m.fileFilter.Value()))
	if query == "" {
		return visible
	}
	keep := make(map[string]bool)
	for _, entry := range visible {
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
	result := make([]worktree.Entry, 0, len(visible))
	for _, entry := range visible {
		if keep[entry.Path] {
			result = append(result, entry)
		}
	}
	return result
}

func (m Model) visibleWorkspaceEntries() []worktree.Entry {
	children := make(map[string][]worktree.Entry)
	for _, entry := range m.workspaceEntries {
		parent := workspaceParent(entry.Path)
		children[parent] = append(children[parent], entry)
	}
	for parent := range children {
		sort.Slice(children[parent], func(i, j int) bool { return children[parent][i].Path < children[parent][j].Path })
	}
	result := make([]worktree.Entry, 0, len(m.workspaceEntries))
	var appendChildren func(string)
	appendChildren = func(parent string) {
		for _, entry := range children[parent] {
			result = append(result, entry)
			if entry.IsDir && m.workspaceExpanded[entry.Path] {
				appendChildren(entry.Path)
			}
		}
	}
	appendChildren("")
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
	if m.active != 0 {
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
		return m, nil
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
	for _, change := range staged {
		result = append(result, workspaceChange{change: change, staged: true})
	}
	for _, change := range changes {
		result = append(result, workspaceChange{change: change})
	}
	return result
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
	if m.active == 1 {
		height = max(1, height-2)
	}
	if m.workspaceCursor < m.workspaceOffset {
		m.workspaceOffset = m.workspaceCursor
	}
	if m.workspaceCursor >= m.workspaceOffset+height {
		m.workspaceOffset = m.workspaceCursor - height + 1
	}
}

func (m Model) workspaceListHeight() int { return max(1, m.height-8) }

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
				if candidate.staged == change.staged {
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
	if m.active == 1 {
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
	left = min(42, max(12, total/3))
	if total < 64 {
		left = max(10, (total-3)/2)
	}
	left = min(left, max(1, total-4))
	right = max(1, total-left-3)
	return left, right
}

func (m Model) workspacePreviewLineCount() int {
	if m.active == 0 {
		if m.workspaceFile.Path == "" {
			return 1
		}
		if m.workspaceFile.Image || m.workspaceFile.Binary || !utf8.Valid(m.workspaceFile.Data) {
			return 2
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
	if m.actionBusy {
		return m, nil
	}
	switch msg.String() {
	case "q":
		return m, tea.Quit
	case "/":
		return m, m.fileFilter.Focus()
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
		if m.active == 1 {
			length = len(m.filteredWorkspaceChanges())
		}
		m.workspaceCursor = max(0, length-1)
		m.ensureWorkspaceCursorVisible()
		return m.loadSelectedWorkspaceItem()
	case "enter", "right", "l":
		return m.toggleWorkspaceDirectory()
	case "left", "h":
		if m.active == 0 {
			entries := m.filteredWorkspaceEntries()
			if len(entries) > 0 && entries[m.workspaceCursor].IsDir && m.workspaceExpanded[entries[m.workspaceCursor].Path] {
				return m.toggleWorkspaceDirectory()
			}
		}
	case "r":
		m.workspaceLoading = false
		return m.startWorkspaceLoad()
	case " ", "s", "S", "u", "U":
		if m.active == 1 {
			return m.toggleWorkspaceStage(msg.String())
		}
	}
	return m, nil
}

func (m Model) toggleWorkspaceStage(key string) (tea.Model, tea.Cmd) {
	changes := m.filteredWorkspaceChanges()
	if len(changes) == 0 {
		return m, nil
	}
	selected := changes[m.workspaceCursor]
	if (key == "s" || key == "S") && selected.staged || (key == "u" || key == "U") && !selected.staged {
		return m, nil
	}
	unstage := selected.staged
	if key == "s" || key == "S" {
		unstage = false
	}
	if key == "u" || key == "U" {
		unstage = true
	}
	action := "stage"
	run := m.workspace.Stage
	if unstage {
		action = "unstage"
		run = m.workspace.Unstage
	}
	m.actionBusy = true
	m.workspacePendingPath = selected.change.Path
	m.status = action + " " + sanitizeWorkspaceLabel(selected.change.Path) + "…"
	request := m.workspaceRequest
	path := selected.change.Path
	return m, func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		return workspaceActionResultMsg{request: request, action: action, err: run(ctx, path)}
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
	content := sanitizeWorkspaceText(strings.ReplaceAll(string(file.Data), "\r\n", "\n"))
	lines := strings.Split(content, "\n")
	offset = min(max(0, offset), max(0, len(lines)-1))
	lines = lines[offset:]
	if height > 1 {
		lines = lines[:min(len(lines), height-1)]
	}
	for i := range lines {
		lines[i] = truncate(lines[i], max(1, width))
	}
	return kittyDeleteImage() + header + strings.Join(lines, "\n")
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
			prefix = fmt.Sprintf("\x1b_Ga=T,f=100,q=2,i=31,C=1,c=%d,r=%d,m=%d;", max(1, width), max(1, height), more)
		}
		parts = append(parts, prefix+encoded[:n]+"\x1b\\")
		encoded = encoded[n:]
	}
	return strings.Join(parts, ""), true
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
	return "\x1b_Ga=d,d=i,i=31,q=2\x1b\\"
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
	if width < 100 {
		return renderUnifiedWorkspaceDiff(diff, width)
	}
	column := max(12, (width-3)/2)
	oldText := sanitizeWorkspaceText(strings.ReplaceAll(string(diff.Old), "\r\n", "\n"))
	newText := sanitizeWorkspaceText(strings.ReplaceAll(string(diff.New), "\r\n", "\n"))
	oldLines := workspaceDiffLines(oldText)
	newLines := workspaceDiffLines(newText)
	if max(len(oldLines), len(newLines)) > 5_000 || len(oldLines)*len(newLines) > 250_000 {
		return renderUnifiedWorkspaceDiff(diff, width)
	}
	rows := []string{sectionTitleStyle.Render(sanitizeWorkspaceLabel(diff.Path) + " · side by side"), metaStyle.Render(padRight("OLD", column) + " │ " + padRight("NEW", column))}
	for _, pair := range alignDiffLines(oldLines, newLines) {
		oldLine, newLine := pair.old, pair.new
		var left, right string
		switch {
		case pair.hasOld && pair.hasNew && oldLine == newLine:
			left = padRight(truncate("  "+oldLine, column), column)
			right = padRight(truncate("  "+newLine, column), column)
		case pair.hasOld && !pair.hasNew:
			left = removedLineStyle.Render(padRight(truncate("- "+oldLine, column), column))
			right = diffGapStyle.Render(strings.Repeat(" ", column))
		case !pair.hasOld && pair.hasNew:
			left = diffGapStyle.Render(strings.Repeat(" ", column))
			right = addedLineStyle.Render(padRight(truncate("+ "+newLine, column), column))
		default:
			left = removedLineStyle.Render(padRight(truncate("- "+oldLine, column), column))
			right = addedLineStyle.Render(padRight(truncate("+ "+newLine, column), column))
		}
		rows = append(rows, left+metaStyle.Render(" │ ")+right)
	}
	return kittyDeleteImage() + strings.Join(rows, "\n")
}

func workspaceDiffLines(text string) []string {
	if text == "" {
		return nil
	}
	lines := strings.Split(text, "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

func renderUnifiedWorkspaceDiff(diff worktree.Diff, width int) string {
	patch := strings.TrimSpace(sanitizeWorkspaceText(diff.Patch))
	if patch == "" {
		patch = "No textual changes."
	}
	lines := strings.Split(patch, "\n")
	for i, line := range lines {
		line = truncate(line, max(1, width))
		switch {
		case strings.HasPrefix(line, "+") && !strings.HasPrefix(line, "+++"):
			lines[i] = addedLineStyle.Render(line)
		case strings.HasPrefix(line, "-") && !strings.HasPrefix(line, "---"):
			lines[i] = removedLineStyle.Render(line)
		default:
			lines[i] = metaStyle.Render(line)
		}
	}
	return kittyDeleteImage() + sectionTitleStyle.Render(sanitizeWorkspaceLabel(diff.Path)+" · unified") + "\n" + strings.Join(lines, "\n")
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

// alignDiffLines uses an LCS to keep insertions and deletions aligned instead
// of making every following line appear changed. Very large files use a
// bounded prefix/suffix alignment to avoid quadratic memory growth.
type alignedDiffLine struct {
	old, new       string
	hasOld, hasNew bool
}

func alignDiffLines(oldLines, newLines []string) []alignedDiffLine {
	if len(oldLines)*len(newLines) > 250_000 {
		pairs := make([]alignedDiffLine, 0, max(len(oldLines), len(newLines)))
		for i := 0; i < max(len(oldLines), len(newLines)); i++ {
			pair := alignedDiffLine{}
			if i < len(oldLines) {
				pair.old, pair.hasOld = oldLines[i], true
			}
			if i < len(newLines) {
				pair.new, pair.hasNew = newLines[i], true
			}
			pairs = append(pairs, pair)
		}
		return pairs
	}
	dp := make([][]int, len(oldLines)+1)
	for i := range dp {
		dp[i] = make([]int, len(newLines)+1)
	}
	for i := len(oldLines) - 1; i >= 0; i-- {
		for j := len(newLines) - 1; j >= 0; j-- {
			if oldLines[i] == newLines[j] {
				dp[i][j] = dp[i+1][j+1] + 1
			} else {
				dp[i][j] = max(dp[i+1][j], dp[i][j+1])
			}
		}
	}
	pairs := make([]alignedDiffLine, 0, len(oldLines)+len(newLines))
	for i, j := 0, 0; i < len(oldLines) || j < len(newLines); {
		switch {
		case i < len(oldLines) && j < len(newLines) && oldLines[i] == newLines[j]:
			pairs = append(pairs, alignedDiffLine{old: oldLines[i], new: newLines[j], hasOld: true, hasNew: true})
			i, j = i+1, j+1
		case j < len(newLines) && (i == len(oldLines) || dp[i][j+1] > dp[i+1][j]):
			pairs = append(pairs, alignedDiffLine{new: newLines[j], hasNew: true})
			j++
		default:
			pairs = append(pairs, alignedDiffLine{old: oldLines[i], hasOld: true})
			i++
		}
	}
	return pairs
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
