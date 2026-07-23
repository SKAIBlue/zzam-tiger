package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/jwmtp2/gtui/internal/provider"
)

var (
	accent            = lipgloss.Color("#7D56F4")
	green             = lipgloss.Color("#3DDC97")
	red               = lipgloss.Color("#FF6B6B")
	muted             = lipgloss.Color("#7B8496")
	text              = lipgloss.Color("#E6E9EF")
	border            = lipgloss.Color("#4B5263")
	headerStyle       = lipgloss.NewStyle().Bold(true).Foreground(text)
	tabStyle          = lipgloss.NewStyle().Foreground(muted)
	activeTab         = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#FFFFFF")).Background(accent)
	filterStyle       = lipgloss.NewStyle().Foreground(muted)
	activeFilter      = lipgloss.NewStyle().Bold(true).Foreground(green).Underline(true)
	selectedRow       = lipgloss.NewStyle().Foreground(lipgloss.Color("#FFFFFF")).Background(lipgloss.Color("#343B58"))
	myAssignmentTitle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#E5C07B"))
	metaStyle         = lipgloss.NewStyle().Foreground(muted)
	errorStyle        = lipgloss.NewStyle().Foreground(red)
	statusStyle       = lipgloss.NewStyle().Foreground(green)
	sectionTitleStyle = lipgloss.NewStyle().Bold(true).Foreground(accent)
	detailBoxStyle    = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(border).Padding(0, 1)
)

func (m Model) View() string {
	if m.width == 0 || m.height == 0 {
		return "Starting gtui…"
	}
	if m.screen == detailScreen || m.screen == labelScreen {
		return m.detailView()
	}
	return m.listView()
}

func (m Model) listView() string {
	lines := make([]string, 0, m.height)
	title := fmt.Sprintf(" gtui  %s · %s", m.backend.Name(), m.backend.Repository())
	lines = append(lines, headerStyle.Render(truncate(title, m.width)))
	lines = append(lines, m.tabsView())
	lines = append(lines, strings.Repeat("─", max(1, m.width)))
	lines = append(lines, m.filtersView())
	lines = append(lines, "")

	items := m.items[m.kind()]
	if m.loadingList && len(items) == 0 {
		lines = append(lines, metaStyle.Render("  Loading…"))
	} else if m.err != nil && len(items) == 0 {
		lines = append(lines, errorStyle.Render("  "+truncate(m.err.Error(), max(1, m.width-2))))
	} else if len(items) == 0 {
		lines = append(lines, metaStyle.Render("  No items for this filter."))
	} else {
		start := m.offset[m.kind()]
		end := min(len(items), start+m.listHeight())
		for index := start; index < end; index++ {
			lines = append(lines, m.itemRow(items[index], index == m.cursor[m.kind()]))
		}
	}
	for len(lines) < m.height-2 {
		lines = append(lines, "")
	}
	lines = append(lines, m.statusLine())
	lines = append(lines, metaStyle.Render(truncate(m.listHelp(), m.width)))
	return strings.Join(lines[:min(len(lines), m.height)], "\n")
}

func (m Model) tabsView() string {
	parts := make([]string, 0, len(kinds))
	for index, kind := range kinds {
		label := fmt.Sprintf(" %d %s ", index+1, m.backend.TabName(kind))
		if index == m.active {
			parts = append(parts, activeTab.Render(label))
		} else {
			parts = append(parts, tabStyle.Render(label))
		}
	}
	return " " + strings.Join(parts, " ")
}

func (m Model) filtersView() string {
	filters := m.backend.Filters(m.kind())
	parts := make([]string, 0, len(filters))
	for index, filter := range filters {
		label := " " + filter.Label + " "
		if index == m.filterIndex[m.kind()] {
			parts = append(parts, activeFilter.Render(label))
		} else {
			parts = append(parts, filterStyle.Render(label))
		}
	}
	return " " + strings.Join(parts, " ")
}

func (m Model) itemRow(item provider.Item, selected bool) string {
	state := stateBadge(item.State)
	metaParts := make([]string, 0, 3)
	if assignableKind(m.kind()) {
		metaParts = append(metaParts, assigneeLabel(item.Assignees))
	}
	if item.Meta != "" {
		metaParts = append(metaParts, item.Meta)
	}
	if !item.UpdatedAt.IsZero() {
		metaParts = append(metaParts, relativeTime(item.UpdatedAt))
	}
	meta := strings.Join(metaParts, " · ")
	prefix := " " + state + " "
	contentWidth := max(1, m.width-lipgloss.Width(prefix)-1)
	metaWidth := min(lipgloss.Width(meta), max(0, contentWidth-8-1))
	titleWidth := max(1, contentWidth-metaWidth)
	if metaWidth > 0 {
		titleWidth--
	}
	title := truncate(item.Title, titleWidth)
	if item.AssignedToMe {
		title = myAssignmentTitle.Render(title)
	}
	row := prefix + title
	if metaWidth > 0 {
		row += " " + metaStyle.Render(truncate(meta, metaWidth))
	}
	row = lipgloss.NewStyle().Width(max(1, m.width)).Render(row)
	if selected {
		return selectedRow.Render(row)
	}
	return row
}

func (m Model) listHelp() string {
	help := " ↑/↓ select · ←/→ filter · Shift+1...5 tabs · Enter detail"
	if m.kind() == provider.Issues {
		help += " · C close · O open"
	}
	if assignableKind(m.kind()) {
		help += " · A assign · U unassign"
	}
	return help + " · r refresh · q quit"
}

func assigneeLabel(assignees []provider.Assignee) string {
	if len(assignees) == 0 {
		return "unassigned"
	}
	logins := make([]string, 0, len(assignees))
	for _, assignee := range assignees {
		login := strings.TrimSpace(assignee.Login)
		if login != "" {
			logins = append(logins, "@"+login)
		}
	}
	if len(logins) == 0 {
		return "unassigned"
	}
	return "assigned: " + strings.Join(logins, ", ")
}

func stateBadge(state string) string {
	style := lipgloss.NewStyle().Bold(true)
	switch strings.ToLower(state) {
	case "open", "opened", "active":
		return style.Foreground(green).Render("● " + strings.ToUpper(state))
	case "merged":
		return style.Foreground(accent).Render("◆ MERGED")
	case "closed":
		return style.Foreground(red).Render("● CLOSED")
	case "commit":
		return style.Foreground(lipgloss.Color("#E5C07B")).Render("● COMMIT")
	default:
		return style.Foreground(lipgloss.Color("#61AFEF")).Render("● " + strings.ToUpper(state))
	}
}

func (m Model) detailView() string {
	item := m.selected
	if m.detail.Item.ID != "" {
		item = m.detail.Item
	}
	title := fmt.Sprintf(" ← Esc  %s  %s", stateBadge(item.State), item.Title)
	lines := []string{headerStyle.Render(truncate(title, m.width))}
	metaParts := []string{item.Meta}
	if assignableKind(m.kind()) {
		metaParts = append(metaParts, assigneeLabel(item.Assignees))
	}
	metaParts = append(metaParts, item.URL)
	meta := " " + strings.Join(metaParts, " · ")
	lines = append(lines, metaStyle.Render(truncate(meta, m.width)))
	if m.loadingDetail && m.detail.Item.ID == "" {
		loading := []string{"", metaStyle.Render("  Loading detail…")}
		for len(loading) < m.viewport.Height {
			loading = append(loading, "")
		}
		lines = append(lines, strings.Join(loading[:m.viewport.Height], "\n"))
	} else if m.err != nil && m.detail.Item.ID == "" {
		failure := []string{"", errorStyle.Render("  Unable to load detail: " + truncate(m.err.Error(), max(10, m.width-26)))}
		for len(failure) < m.viewport.Height {
			failure = append(failure, "")
		}
		lines = append(lines, strings.Join(failure[:m.viewport.Height], "\n"))
	} else {
		lines = append(lines, m.viewport.View())
	}
	lines = append(lines, m.statusLine())
	help := " ↑/↓ or wheel scroll · Esc back · r refresh"
	if m.kind() == provider.PullRequests {
		help += " · M merge"
	}
	if m.kind() == provider.Issues {
		help += " · C close · O open · L labels"
	}
	if assignableKind(m.kind()) {
		help += " · A assign · U unassign"
	}
	lines = append(lines, metaStyle.Render(truncate(help, m.width)))
	view := strings.Join(lines, "\n")
	if m.screen == labelScreen {
		modalWidth := min(max(38, m.width-12), 72)
		modal := detailBoxStyle.Width(modalWidth).Render(sectionTitleStyle.Render("Set issue labels") + "\n\n" + m.labels.View() + "\n\n" + metaStyle.Render("Enter apply · Esc cancel"))
		return placeOverlay(m.width, m.height, modal, view)
	}
	return view
}

func (m Model) statusLine() string {
	if m.err != nil {
		return errorStyle.Render(truncate(" Error: "+m.err.Error(), m.width))
	}
	if m.status != "" {
		return statusStyle.Render(truncate(" "+m.status, m.width))
	}
	if m.loadingList || m.loadingDetail || m.actionBusy {
		return metaStyle.Render(" Updating…")
	}
	if !m.lastUpdated.IsZero() {
		limit := ""
		if m.screen == listScreen && len(m.items[m.kind()]) >= 100 {
			limit = " · showing latest 100"
		}
		return metaStyle.Render(fmt.Sprintf(" Updated %s · auto-refresh %s%s", m.lastUpdated.Format("15:04:05"), refreshLabel(m.refresh), limit))
	}
	return ""
}

func relativeTime(value time.Time) string {
	delta := time.Since(value)
	if delta < time.Minute {
		return "now"
	}
	if delta < time.Hour {
		return fmt.Sprintf("%dm", int(delta.Minutes()))
	}
	if delta < 24*time.Hour {
		return fmt.Sprintf("%dh", int(delta.Hours()))
	}
	return fmt.Sprintf("%dd", int(delta.Hours()/24))
}

func refreshLabel(value time.Duration) string {
	if value <= 0 {
		return "off"
	}
	return value.String()
}

func truncate(value string, width int) string {
	if width <= 0 {
		return ""
	}
	if lipgloss.Width(value) <= width {
		return value
	}
	if width <= 3 {
		return strings.Repeat(".", width)
	}
	runes := []rune(value)
	for len(runes) > 0 && lipgloss.Width(string(runes))+3 > width {
		runes = runes[:len(runes)-1]
	}
	return string(runes) + "..."
}

func placeOverlay(width, height int, foreground, background string) string {
	fgLines := strings.Split(foreground, "\n")
	bgLines := strings.Split(background, "\n")
	startY := max(0, (height-len(fgLines))/2)
	fgWidth := lipgloss.Width(foreground)
	startX := max(0, (width-fgWidth)/2)
	for y, line := range fgLines {
		row := startY + y
		if row >= len(bgLines) {
			break
		}
		left := truncate(bgLines[row], startX)
		padding := strings.Repeat(" ", max(0, startX-lipgloss.Width(left)))
		bgLines[row] = left + padding + line
	}
	return strings.Join(bgLines, "\n")
}
