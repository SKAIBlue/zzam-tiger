package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"

	"github.com/jwmtp2/gtui/internal/provider"
)

type screen int

const (
	listScreen screen = iota
	detailScreen
	labelScreen
)

var kinds = []provider.Kind{
	provider.PullRequests,
	provider.Issues,
	provider.Milestones,
	provider.Branches,
	provider.Commits,
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

type Model struct {
	backend provider.Provider
	refresh time.Duration

	width  int
	height int
	screen screen
	active int

	filterIndex map[provider.Kind]int
	items       map[provider.Kind][]provider.Item
	cursor      map[provider.Kind]int
	offset      map[provider.Kind]int

	selected provider.Item
	detail   provider.Detail
	viewport viewport.Model
	labels   textinput.Model

	listRequest   uint64
	detailRequest uint64
	loadingList   bool
	loadingDetail bool
	actionBusy    bool
	lastUpdated   time.Time
	status        string
	err           error
}

func New(backend provider.Provider, refresh time.Duration) Model {
	labels := textinput.New()
	labels.Prompt = "Labels (comma separated): "
	labels.CharLimit = 500
	labels.Width = 50

	model := Model{
		backend:     backend,
		refresh:     refresh,
		filterIndex: make(map[provider.Kind]int),
		items:       make(map[provider.Kind][]provider.Item),
		cursor:      make(map[provider.Kind]int),
		offset:      make(map[provider.Kind]int),
		viewport:    viewport.New(80, 20),
		labels:      labels,
		listRequest: 1,
		loadingList: true,
		lastUpdated: time.Time{},
	}
	return model
}

func (m Model) Init() tea.Cmd {
	commands := []tea.Cmd{m.fetchListCmd(m.listRequest, m.kind(), m.filter())}
	if m.refresh > 0 {
		commands = append(commands, tickCmd(m.refresh))
	}
	return tea.Batch(commands...)
}

func (m Model) kind() provider.Kind { return kinds[m.active] }

func (m Model) filter() provider.Filter {
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

func (m Model) fetchListCmd(request uint64, kind provider.Kind, filter provider.Filter) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		items, err := m.backend.List(ctx, kind, filter)
		return listResultMsg{request: request, kind: kind, filter: filter.Value, items: items, err: err}
	}
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

func (m Model) startListLoad() (Model, tea.Cmd) {
	if m.loadingList {
		return m, nil
	}
	m.listRequest++
	m.loadingList = true
	m.err = nil
	return m, m.fetchListCmd(m.listRequest, m.kind(), m.filter())
}

func (m Model) startDetailLoad() (Model, tea.Cmd) {
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
		m.resizeViewport()
		if m.detail.Item.ID != "" {
			m.setDetailContent()
		}
		return m, nil

	case listResultMsg:
		if msg.request != m.listRequest || msg.kind != m.kind() || msg.filter != m.filter().Value {
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
		if msg.request != m.detailRequest || msg.item.ID != m.selected.ID || m.screen == listScreen {
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
		m.detail = msg.detail
		m.selected = msg.detail.Item
		m.setDetailContent()
		m.lastUpdated = time.Now()
		m.err = nil
		return m, nil

	case actionResultMsg:
		m.actionBusy = false
		if msg.err != nil {
			m.err = msg.err
			m.status = msg.action + " failed"
			return m, nil
		}
		m.status = msg.action + " completed"
		m.err = nil
		m.loadingDetail = false
		return m.startDetailLoad()

	case tickMsg:
		commands := []tea.Cmd{}
		if m.refresh > 0 {
			commands = append(commands, tickCmd(m.refresh))
		}
		var refreshCmd tea.Cmd
		if m.screen == detailScreen && !m.actionBusy {
			m, refreshCmd = m.startDetailLoad()
		} else if m.screen == listScreen {
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
		if m.screen == detailScreen {
			return m.updateDetail(msg)
		}
		return m.updateList(msg)
	}

	if m.screen == detailScreen {
		var cmd tea.Cmd
		m.viewport, cmd = m.viewport.Update(message)
		return m, cmd
	}
	return m, nil
}

func (m Model) updateList(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q":
		return m, tea.Quit
	case "tab", "]":
		m.active = (m.active + 1) % len(kinds)
		m.status = ""
		m.loadingList = false
		return m.startListLoad()
	case "shift+tab", "[":
		m.active = (m.active - 1 + len(kinds)) % len(kinds)
		m.status = ""
		m.loadingList = false
		return m.startListLoad()
	case "1", "2", "3", "4", "5":
		m.active = int(msg.Runes[0] - '1')
		m.status = ""
		m.loadingList = false
		return m.startListLoad()
	case "!", "@", "#", "$", "%":
		shiftTabs := map[string]int{"!": 0, "@": 1, "#": 2, "$": 3, "%": 4}
		m.active = shiftTabs[msg.String()]
		m.status = ""
		m.loadingList = false
		return m.startListLoad()
	case "left", "h":
		return m.changeFilter(-1)
	case "right", "l":
		return m.changeFilter(1)
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
	case "r":
		m.loadingList = false
		return m.startListLoad()
	}
	return m, nil
}

func (m Model) updateDetail(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "backspace":
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
	case "m", "M":
		if m.kind() == provider.PullRequests && m.detailReady() && m.selected.HeadSHA != "" && !m.actionBusy {
			m.actionBusy = true
			m.status = "merging…"
			item := m.selected
			return m, m.actionCmd("merge", func(ctx context.Context) error { return m.backend.Merge(ctx, item) })
		}
	case "c", "C":
		if m.kind() == provider.Issues && m.detailReady() && !m.actionBusy {
			m.actionBusy = true
			m.status = "closing issue…"
			item := m.selected
			return m, m.actionCmd("close issue", func(ctx context.Context) error { return m.backend.SetIssueState(ctx, item, false) })
		}
	case "o", "O":
		if m.kind() == provider.Issues && m.detailReady() && !m.actionBusy {
			m.actionBusy = true
			m.status = "reopening issue…"
			item := m.selected
			return m, m.actionCmd("reopen issue", func(ctx context.Context) error { return m.backend.SetIssueState(ctx, item, true) })
		}
	case "l", "L":
		if m.kind() == provider.Issues && m.detailReady() && !m.actionBusy {
			m.screen = labelScreen
			m.labels.SetValue(strings.Join(m.detail.Labels, ", "))
			m.labels.CursorEnd()
			return m, m.labels.Focus()
		}
	}
	return m, nil
}

func (m Model) detailReady() bool {
	return !m.loadingDetail && m.err == nil && m.selected.ID != "" && m.detail.Item.ID == m.selected.ID
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
	m.selected = items[index]
	m.detail = provider.Detail{}
	m.viewport.SetContent("")
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
}

func (m *Model) setDetailContent() {
	m.viewport.SetContent(renderDetail(m.detail, m.viewport.Width))
}

func renderDetail(detail provider.Detail, width int) string {
	if width < 20 {
		width = 20
	}
	renderer, err := glamour.NewTermRenderer(glamour.WithStandardStyle("dark"), glamour.WithWordWrap(max(10, width-6)))
	if err != nil {
		sections := []string{"Description\n" + detail.Body}
		for _, section := range detail.Sections {
			sections = append(sections, section.Title+"\n"+section.Markdown)
		}
		return strings.Join(sections, "\n\n────────────────────\n\n")
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
	return strings.Join(sections, "\n")
}

func (m Model) handleMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	if msg.Button == tea.MouseButtonWheelUp {
		if m.screen == detailScreen || m.screen == labelScreen {
			m.viewport.LineUp(3)
		} else {
			m.moveCursor(-3)
		}
		return m, nil
	}
	if msg.Button == tea.MouseButtonWheelDown {
		if m.screen == detailScreen || m.screen == labelScreen {
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
	if msg.Y == 1 {
		if tab := m.tabAt(msg.X); tab >= 0 {
			m.active = tab
			m.loadingList = false
			return m.startListLoad()
		}
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
	for index, kind := range kinds {
		label := fmt.Sprintf(" %d %s ", index+1, m.backend.TabName(kind))
		end := position + lipgloss.Width(label)
		if x >= position && x < end {
			return index
		}
		position = end + 1
	}
	return -1
}

func (m Model) filterAt(x int) int {
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
