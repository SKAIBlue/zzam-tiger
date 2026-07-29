package tui

import (
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/SKAIBlue/zzam-tiger/internal/aiusage"
	"github.com/SKAIBlue/zzam-tiger/internal/provider"
	"github.com/SKAIBlue/zzam-tiger/internal/worktree"
)

var (
	accent                     = lipgloss.Color("#7D56F4")
	green                      = lipgloss.Color("#3DDC97")
	red                        = lipgloss.Color("#FF6B6B")
	muted                      = lipgloss.Color("#7B8496")
	text                       = lipgloss.Color("#E6E9EF")
	border                     = lipgloss.Color("#4B5263")
	headerPurple               = lipgloss.Color("#6C4EE3")
	headerBlue                 = lipgloss.Color("#2E86C1")
	headerSlate                = lipgloss.Color("#273142")
	headerStyle                = lipgloss.NewStyle().Bold(true).Foreground(text)
	versionStyle               = lipgloss.NewStyle().Foreground(lipgloss.Color("#61AFEF"))
	tabStyle                   = lipgloss.NewStyle().Foreground(muted)
	activeTab                  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#FFFFFF")).Background(accent)
	tabBoxStyle                = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(border)
	filterStyle                = lipgloss.NewStyle().Foreground(muted)
	activeFilter               = lipgloss.NewStyle().Bold(true).Foreground(green).Underline(true)
	focusedFilter              = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#111318")).Background(accent)
	selectedRow                = lipgloss.NewStyle().Foreground(lipgloss.Color("#FFFFFF")).Background(lipgloss.Color("#343B58"))
	myAssignmentTitle          = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#E5C07B"))
	metaStyle                  = lipgloss.NewStyle().Foreground(muted)
	errorStyle                 = lipgloss.NewStyle().Foreground(red)
	statusStyle                = lipgloss.NewStyle().Foreground(green)
	sectionTitleStyle          = lipgloss.NewStyle().Bold(true).Foreground(accent)
	detailBoxStyle             = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(border).Padding(0, 1)
	composerStyle              = detailBoxStyle.Copy().BorderForeground(accent).Background(lipgloss.Color("#1B1F2A"))
	addedLineStyle             = lipgloss.NewStyle().Background(lipgloss.Color("#203C2F"))
	removedLineStyle           = lipgloss.NewStyle().Background(lipgloss.Color("#482B31"))
	diffGapStyle               = lipgloss.NewStyle().Background(lipgloss.Color("#2D3348"))
	reviewMetaStyle            = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#E5C07B"))
	reviewBodyStyle            = lipgloss.NewStyle().Foreground(text).BorderLeft(true).BorderStyle(lipgloss.ThickBorder()).BorderForeground(accent).PaddingLeft(1)
	selectedReviewStyle        = lipgloss.NewStyle().Background(lipgloss.Color("#2D3348"))
	commitButtonStyle          = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#FFFFFF")).Background(accent).Padding(0, 1)
	pullButtonStyle            = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#FFFFFF")).Background(headerBlue)
	pushButtonStyle            = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#FFFFFF")).Background(green)
	dashboardCommitButtonStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#FFFFFF")).Background(accent)
	disabledButtonStyle        = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#111318")).Background(lipgloss.Color("#6B7280"))
	stageArrowStyle            = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#111318")).Background(lipgloss.Color("#E5C07B"))
	unstageArrowStyle          = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#111318")).Background(lipgloss.Color("#61AFEF"))
	revertArrowStyle           = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#FFFFFF")).Background(lipgloss.Color("#E06C75"))
	updateButtonStyle          = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#FFFFFF")).Background(green).Padding(0, 1)
	headerBrandStyle           = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#FFFFFF")).Background(headerPurple).Padding(0, 1)
	headerVersionStyle         = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#DCE7FF")).Background(headerBlue).Padding(0, 1)
	headerContextStyle         = lipgloss.NewStyle().Foreground(text).Background(headerSlate).Padding(0, 1)
	headerAccentStyle          = lipgloss.NewStyle().Foreground(headerPurple).Background(headerBlue)
	headerContextEdge          = lipgloss.NewStyle().Foreground(headerBlue).Background(headerSlate)
	headerWarningStyle         = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#FFE8E8")).Background(lipgloss.Color("#651C2A")).Padding(0, 1)
)

func (m Model) View() string {
	if m.aiUsageActive() {
		return m.aiUsageView()
	}
	if m.width == 0 || m.height == 0 {
		return "Starting Zzam Tiger…"
	}
	if m.modal != nil {
		background := m.baseView()
		if m.screen == branchScreen {
			background = m.listView()
		}
		return m.modalOverlay(background)
	}
	return m.baseView()
}

func (m Model) baseView() string {
	if m.screen == diffScreen {
		return m.diffView()
	}
	if m.screen == commentScreen {
		background := m.detailView()
		if m.commentUsesDiffBackground() {
			background = m.diffView()
		}
		return m.commentOverlay(background)
	}
	if m.screen == detailScreen || m.screen == labelScreen {
		return m.detailView()
	}
	view := m.listView()
	if m.screen == branchScreen {
		return m.branchOverlay(view)
	}
	return view
}

func (m Model) listView() string {
	if m.localTab() {
		return m.workspaceView()
	}
	lines := make([]string, 0, m.height)
	title := "  remote unavailable"
	if m.backend != nil {
		title = fmt.Sprintf("  %s · %s · %s", sanitizeWorkspaceLabel(m.backend.Name()), sanitizeWorkspaceLabel(m.backend.Repository()), m.activeTabLabel())
	}
	// List views do not render Kitty images. In particular, emitting an image
	// delete command here makes Kitty consume the title row on some terminals.
	lines = append(lines, m.headerView(title))
	lines = append(lines, m.tabsView())
	contentStart := len(lines)
	if m.remoteErr == nil && m.workspace != nil && m.kind() == provider.Commits {
		query := m.graphFilter.View()
		if !m.graphFilter.Focused() && m.graphFilter.Value() == "" {
			query = metaStyle.Render("Filter: press / to search")
		}
		lines = append(lines, contentBoxRaw(filterContentBoxRow(" "+truncate(query, max(1, m.width-3)), m.width, m.filterFocused())))
		lines = append(lines, contentBoxRaw(contentBoxDivider(m.width, m.filterFocused())))
		lines = append(lines, m.filtersView())
	} else if m.remoteErr == nil || m.workspace != nil && m.kind() == provider.Branches {
		query := m.graphQuery.View()
		if !m.graphQuery.Focused() && m.graphQuery.Value() == "" {
			query = metaStyle.Render("Filter: press / to search")
		}
		lines = append(lines, contentBoxRaw(filterContentBoxRow(" "+truncate(query, max(1, m.width-3)), m.width, m.filterFocused())))
		lines = append(lines, contentBoxRaw(contentBoxDivider(m.width, m.filterFocused())))
		lines = append(lines, m.filtersView())
		if m.remoteErr != nil {
			lines = append(lines, metaStyle.Render(" Remote integration unavailable"))
		}
	} else {
		lines = append(lines, metaStyle.Render(" Remote integration unavailable"))
	}
	if m.localErr != nil {
		lines = append(lines, metaStyle.Render(" Local Git features unavailable: "+truncate(sanitizeWorkspaceLabel(m.localErr.Error()), max(1, m.width-34))))
	}
	lines = append(lines, "")
	panelStart := len(lines)
	items := m.visibleListItems()
	if m.remoteErr != nil && !m.localGitList(m.kind()) {
		lines = append(lines, errorStyle.Render("  "+truncate(sanitizeWorkspaceLabel(m.remoteErr.Error()), max(1, m.width-2))))
	} else if m.loadingList && len(items) == 0 {
		lines = append(lines, metaStyle.Render("  Loading…"))
	} else if m.err != nil && len(items) == 0 {
		lines = append(lines, errorStyle.Render("  "+truncate(sanitizeWorkspaceLabel(m.err.Error()), max(1, m.width-2))))
	} else if len(items) == 0 {
		lines = append(lines, metaStyle.Render("  No items for this filter."))
	} else {
		start := m.offset[m.kind()]
		end := min(len(items), start+m.listHeight())
		graphPrefixes := commitGraphPrefixes(items)
		showGraph := m.kind() == provider.Commits && hasCommitGraphMetadata(items)
		for index := start; index < end; index++ {
			if showGraph {
				selected := index == m.cursor[m.kind()]
				lines = append(lines, m.graphItemRow(items[index], graphPrefixes[index], selected))
			} else {
				lines = append(lines, m.itemRow(items[index], index == m.cursor[m.kind()]))
			}
		}
	}
	bodyRows := append([]string(nil), lines[panelStart:]...)
	panelHeight := m.listHeight() + 2
	if m.kind() == provider.Commits {
		rightRows := []string{}
		if len(items) > 0 && m.cursor[m.kind()] >= 0 && m.cursor[m.kind()] < len(items) {
			for _, row := range graphTreeRows(graphFilePaths(items[m.cursor[m.kind()]])) {
				rightRows = append(rightRows, m.highlightSearchMatch(row))
			}
		}
		leftWidth, rightWidth := m.graphPaneWidths(len(rightRows) > 0)
		left := contentPanel("Commit Graph", bodyRows, leftWidth, panelHeight, m.focus == focusGraphCommits || m.focus == focusListItems)
		right := contentPanel("Changed Files", rightRows, rightWidth, panelHeight, m.graphDepth == graphFileDepth)
		bodyRows = strings.Split(lipgloss.JoinHorizontal(lipgloss.Top, left, right), "\n")
	} else {
		bodyRows = strings.Split(contentPanel(m.activeTabLabel(), bodyRows, m.width-2, panelHeight, m.focus == focusListItems), "\n")
	}
	lines = append(lines[:panelStart], bodyRows...)
	for renderedLineCount(lines) < m.height-3 {
		lines = append(lines, "")
	}
	lines = append(lines[:contentStart], append(contentBoxRows(lines[contentStart:], m.width), contentBoxBottom(m.width))...)
	lines = append(lines, m.statusLine())
	lines = append(lines, metaStyle.Render(truncate(m.listHelp(), m.width)))
	return strings.Join(lines[:min(len(lines), m.height)], "\n")
}

func (m Model) aiUsageView() string {
	lines := []string{m.headerView("  AI Usage"), m.tabsView()}
	rows := m.aiUsageRows()
	visibleHeight := m.aiUsageVisibleHeight()
	offset := min(max(0, m.aiUsageScrollOffset), max(0, len(rows)-visibleHeight))
	rows = rows[offset:min(len(rows), offset+visibleHeight)]
	for len(rows) < visibleHeight {
		rows = append(rows, "")
	}
	lines = append(lines, contentBoxRows(rows, m.width)...)
	help := " ↑/↓ or wheel scroll · PgUp/PgDn page · Home/End · ←/→ tabs · r refresh · q quit"
	lines = append(lines, contentBoxBottom(m.width), m.statusLine(), metaStyle.Render(truncate(help, m.width)))
	return strings.Join(lines[:min(len(lines), m.height)], "\n")
}

func (m Model) aiUsageRows() []string {
	lines := []string{}
	if len(m.aiUsage) == 0 && (m.aiUsageLimitsLoading || m.aiUsageActivityLoading) {
		lines = append(lines, metaStyle.Render(" Loading AI usage…"))
	} else if len(m.aiUsage) == 0 {
		lines = append(lines, metaStyle.Render(" No supported AI credentials found."))
	} else {
		for _, usage := range m.aiUsage {
			rows := []string{}
			totalRows := []string{}
			if usage.ActivityLoaded {
				totalRows = append(totalRows, fmt.Sprintf(" Total tokens  Total %s  This month %s", formatTokenCount(usage.TotalTokens), formatTokenCount(usage.MonthlyTokens)))
				totalCost, monthlyCost, complete := aiusage.EstimatedCosts(usage.Models)
				qualifier := ""
				if !complete {
					qualifier = "  partial"
				}
				totalRows = append(totalRows, fmt.Sprintf(" Est. API cost  Total %s  This month %s%s", formatUSDCost(totalCost), formatUSDCost(monthlyCost), qualifier))
				totalRows = append(totalRows, metaStyle.Render(" Paid API equivalent · pricing "+aiusage.PriceEffectiveDate()))
			} else {
				totalRows = append(totalRows, metaStyle.Render(" Total tokens  Collecting…"))
			}
			rows = appendUsageSection(rows, totalRows)
			rows = appendUsageSection(rows, modelUsageTableRows(usage.Models, m.width-6))

			limitRows := []string{}
			if !usage.LimitsLoaded {
				limitRows = append(limitRows, metaStyle.Render(" Loading subscription limits…"))
			} else if len(usage.Limits) == 0 {
				notice := usage.Notice
				if notice == "" {
					notice = "Limit data is not available for this account"
				}
				limitRows = append(limitRows, metaStyle.Render(" "+notice))
			} else {
				for _, limit := range usage.Limits {
					reset := "unknown"
					if !limit.Reset.IsZero() {
						reset = limit.Reset.Local().Format("Jan 02 15:04")
					}
					remaining := remainingUsagePercent(limit.Used)
					limitRows = append(limitRows, fmt.Sprintf(" %-8s %s  Remaining %5.1f%%  Reset %s", limit.Label, usageGauge(remaining, 18), remaining, reset))
				}
			}
			if len(usage.Limits) > 0 && usage.Notice != "" {
				limitRows = append(limitRows, metaStyle.Render(" "+usage.Notice))
			}
			rows = appendUsageSection(rows, limitRows)

			activityRows := []string{sectionTitleStyle.Render(" Activity")}
			if usage.ActivityLoaded {
				activityRows = append(activityRows, usageGrassRows(usage.Days, max(20, m.width-8), time.Now())...)
			} else {
				activityRows = append(activityRows, metaStyle.Render(" Collecting activity…"))
			}
			rows = appendUsageSection(rows, activityRows)
			if !usage.Updated.IsZero() {
				rows = appendUsageSection(rows, []string{metaStyle.Render(" Updated     " + usage.Updated.Local().Format("2006-01-02 15:04"))})
			}
			lines = append(lines, strings.Split(contentPanel(usage.Name, rows, m.width-2, len(rows)+2, false), "\n")...)
		}
	}
	return lines
}

func (m Model) aiUsageVisibleHeight() int {
	// The header occupies one rendered row and tabsView occupies three. Keep
	// those four rows plus the content bottom, status, and help rows visible.
	return max(1, m.height-7)
}

func (m Model) aiUsageMaxScrollOffset() int {
	return max(0, len(m.aiUsageRows())-m.aiUsageVisibleHeight())
}

func (m Model) moveAIUsageScroll(delta int) Model {
	maximum := m.aiUsageMaxScrollOffset()
	current := min(max(0, m.aiUsageScrollOffset), maximum)
	m.aiUsageScrollOffset = min(max(0, current+delta), maximum)
	return m
}

func appendUsageSection(rows, section []string) []string {
	if len(section) == 0 {
		return rows
	}
	if len(rows) > 0 {
		rows = append(rows, "")
	}
	return append(rows, section...)
}

func modelUsageTableRows(models []aiusage.ModelUsage, width int) []string {
	if len(models) == 0 {
		return nil
	}
	modelWidth := 30
	for _, model := range models {
		modelWidth = max(modelWidth, lipgloss.Width(model.Model))
	}
	// Leave room for the period, three token counts, and one cost column.
	modelWidth = min(modelWidth, max(18, width-54))
	row := func(model, period, input, cached, output, cost string) string {
		return fmt.Sprintf(" %-*s  %-7s  %9s  %9s  %9s  %9s", modelWidth, truncate(model, modelWidth), period, input, cached, output, cost)
	}
	divider := row(strings.Repeat("─", modelWidth), "───────", "─────────", "─────────", "─────────", "─────────")
	rows := []string{
		sectionTitleStyle.Render(row("Model", "Period", "Input", "Cached", "Output", "Cost")),
		metaStyle.Render(divider),
	}
	for index, model := range models {
		if index > 0 {
			rows = append(rows, metaStyle.Render(divider))
		}
		totalCost, totalKnown := model.EstimatedCost(false)
		monthlyCost, monthlyKnown := model.EstimatedCost(true)
		totalLabel, monthlyLabel := "—", "—"
		if totalKnown {
			totalLabel = formatUSDCost(totalCost)
		}
		if monthlyKnown {
			monthlyLabel = formatUSDCost(monthlyCost)
		}
		rows = append(rows,
			row(model.Model, "Total", formatTokenCount(model.Input), formatTokenCount(model.Cached), formatTokenCount(model.Output), totalLabel),
			row("", "Month", formatTokenCount(model.MonthlyInput), formatTokenCount(model.MonthlyCached), formatTokenCount(model.MonthlyOutput), monthlyLabel),
		)
	}
	return rows
}

func formatUSDCost(cost float64) string {
	if cost < .01 && cost > 0 {
		return fmt.Sprintf("$%.4f", cost)
	}
	return fmt.Sprintf("$%.2f", cost)
}

func usageGauge(percent float64, width int) string {
	percent = min(100, max(0, percent))
	filled := int(percent*float64(width)/100 + .5)
	return lipgloss.NewStyle().Foreground(green).Render(strings.Repeat("█", filled)) + metaStyle.Render(strings.Repeat("░", width-filled))
}

func remainingUsagePercent(used float64) float64 {
	return min(100, max(0, 100-used))
}

func usageGrassRows(days []aiusage.Day, width int, now time.Time) []string {
	const labelWidth, cellWidth = 5, 5
	weeks := max(1, (width-labelWidth)/cellWidth)
	byDate := map[string]int64{}
	var peak int64
	for _, d := range days {
		byDate[d.Date] = d.Tokens
		if d.Tokens > peak {
			peak = d.Tokens
		}
	}
	styles := []lipgloss.Style{
		lipgloss.NewStyle().Foreground(border), lipgloss.NewStyle().Foreground(lipgloss.Color("#24543A")),
		lipgloss.NewStyle().Foreground(lipgloss.Color("#2E7D4F")), lipgloss.NewStyle().Foreground(green),
	}
	endWeek := beginningOfUsageWeek(now)
	startWeek := endWeek.AddDate(0, 0, -7*(weeks-1))
	header := strings.Repeat(" ", labelWidth)
	for column := 0; column < weeks; column++ {
		week := startWeek.AddDate(0, 0, column*7)
		marker := fmt.Sprintf("%d", week.Day())
		if column == 0 || week.Month() != week.AddDate(0, 0, -7).Month() {
			marker = fmt.Sprintf("%d/%d", int(week.Month()), week.Day())
		}
		alignment := lipgloss.Center
		if column == 0 {
			alignment = lipgloss.Left
		}
		header += lipgloss.NewStyle().Width(cellWidth).Align(alignment).Render(marker)
	}
	rows := []string{metaStyle.Render(header)}
	dayLabels := []string{"SUN", "MON", "TUE", "WED", "THU", "FRI", "SAT"}
	for weekday := 0; weekday < 7; weekday++ {
		row := lipgloss.NewStyle().Width(labelWidth).Render(dayLabels[weekday])
		for column := 0; column < weeks; column++ {
			date := startWeek.AddDate(0, 0, column*7+weekday)
			if date.After(now) {
				row += strings.Repeat(" ", cellWidth)
				continue
			}
			n := byDate[date.Format("2006-01-02")]
			level := 0
			if peak > 0 && n > 0 {
				level = 1 + int(n*2/peak)
			}
			cell := styles[min(3, level)].Render("■")
			row += lipgloss.NewStyle().Width(cellWidth).Align(lipgloss.Center).Render(cell)
		}
		rows = append(rows, row)
	}
	return rows
}

func beginningOfUsageWeek(value time.Time) time.Time {
	local := value.In(value.Location())
	day := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, local.Location())
	return day.AddDate(0, 0, -int(day.Weekday()))
}

func formatTokenCount(value int64) string {
	if value >= 1_000_000_000 {
		return fmt.Sprintf("%.2fB", float64(value)/1e9)
	}
	if value >= 1_000_000 {
		return fmt.Sprintf("%.2fM", float64(value)/1e6)
	}
	if value >= 1_000 {
		return fmt.Sprintf("%.1fK", float64(value)/1e3)
	}
	return fmt.Sprintf("%d", value)
}

func (m Model) workspaceCommitDashboard(lines []string) string {
	panelHeight := max(1, m.height-9)
	leftWidth, rightWidth := m.commitDashboardWidths()
	filter := m.fileFilter.View()
	if !m.fileFilter.Focused() {
		value := m.fileFilter.Value()
		if value == "" {
			value = metaStyle.Render("press / to filter paths")
		}
		filter = "Filter: " + value
	}
	lines = append(lines, filterContentBoxRow(" "+truncate(filter, max(1, m.width-3)), m.width, m.filterFocused()))
	lines = append(lines, dashboardContentDivider(leftWidth, rightWidth, m.filterFocused()))
	messageRows := m.commitMessageRowCount(leftWidth)
	commitHeight := min(panelHeight, messageRows+3)
	remaining := max(0, panelHeight-commitHeight)
	stagedHeight, changesHeight := m.commitChangePanelHeights(remaining)

	left := m.commitPanel(leftWidth, commitHeight, messageRows)
	if stagedHeight > 0 {
		left += "\n" + m.changePanel("Staged", true, leftWidth, stagedHeight)
	}
	if changesHeight > 0 {
		left += "\n" + m.changePanel("Changes", false, leftWidth, changesHeight)
	}
	right := m.fileDiffPanel(rightWidth, panelHeight)
	panels := strings.Split(lipgloss.JoinHorizontal(lipgloss.Top, left, right), "\n")
	lines = append(lines, contentBoxRows(panels, m.width)...)
	lines = append(lines, contentBoxBottom(m.width))
	lines = append(lines, m.statusLine(), metaStyle.Render(truncate(m.workspaceFocusHelp(), m.width)))
	return strings.Join(lines[:min(len(lines), m.height)], "\n")
}

func dashboardContentDivider(leftWidth, rightWidth int, focused bool) string {
	return filterBorderStyle(focused).Render("├" + strings.Repeat("─", max(1, leftWidth+rightWidth)) + "┤")
}

func (m Model) commitDashboardWidths() (int, int) {
	contentWidth := max(2, m.width-2)
	left := m.workspaceCommitWidth
	if left <= 0 {
		left = 48
	}
	left = max(24, left)
	left = min(left, max(24, contentWidth-24))
	return left, max(1, contentWidth-left)
}

func (m Model) commitMessageRowCount(leftWidth int) int {
	return len(m.commitMessageSegments(max(1, leftWidth-4)))
}

type commitMessageSegment struct {
	start int
	end   int
}

func (m Model) commitMessageSegments(width int) []commitMessageSegment {
	runes := []rune(m.commitMessage.Value())
	if len(runes) == 0 {
		return []commitMessageSegment{{}}
	}
	segments := make([]commitMessageSegment, 0, 1)
	start, cells := 0, 0
	for index, value := range runes {
		if value == commitMessageNewlineRune {
			segments = append(segments, commitMessageSegment{start: start, end: index})
			start, cells = index+1, 0
			continue
		}
		runeWidth := max(1, ansi.StringWidth(string(value)))
		if cells > 0 && cells+runeWidth > width {
			segments = append(segments, commitMessageSegment{start: start, end: index})
			start, cells = index, 0
		}
		cells += runeWidth
	}
	segments = append(segments, commitMessageSegment{start: start, end: len(runes)})
	if m.focus == focusCommitMessage && m.commitMessage.Position() == len(runes) && cells+1 > width {
		segments = append(segments, commitMessageSegment{start: len(runes), end: len(runes)})
	}
	return segments
}

func (m Model) commitDashboardGeometry() (leftWidth, buttonY, changesTop int) {
	leftWidth, _ = m.commitDashboardWidths()
	messageRows := m.commitMessageRowCount(leftWidth)
	panelHeight := max(1, m.height-9)
	commitHeight := min(panelHeight, messageRows+3)
	remaining := max(0, panelHeight-commitHeight)
	stagedHeight, _ := m.commitChangePanelHeights(remaining)
	buttonY = 7 + messageRows
	changesTop = 6 + commitHeight + stagedHeight
	return
}

func (m Model) commitChangePanelHeights(available int) (staged, changes int) {
	if available <= 0 {
		return 0, 0
	}
	stagedEmpty := len(m.workspaceStatus.Staged) == 0
	changesEmpty := len(m.workspaceStatus.Unstaged)+len(m.workspaceStatus.Untracked) == 0
	const emptyHeight = 3
	switch {
	case stagedEmpty && changesEmpty:
		staged = min(emptyHeight, available)
		changes = min(emptyHeight, max(0, available-staged))
	case stagedEmpty:
		staged = min(emptyHeight, available)
		changes = max(0, available-staged)
	case changesEmpty:
		changes = min(emptyHeight, available)
		staged = max(0, available-changes)
	default:
		staged = int(float64(available) * m.workspaceCommitSplitRatio)
		staged = min(max(1, staged), max(1, available-1))
		changes = max(0, available-staged)
	}
	return staged, changes
}

func dashboardPanelBorderStyle(focused bool) lipgloss.Style {
	color := border
	if focused {
		color = accent
	}
	return lipgloss.NewStyle().Foreground(color)
}

func panelBorderLine(left, fill, right string, width int, focused bool) string {
	return dashboardPanelBorderStyle(focused).Render(left + strings.Repeat(fill, max(0, width-2)) + right)
}

func titledPanelTop(title string, width int, focused bool) string {
	label := "─ " + title + " "
	return dashboardPanelBorderStyle(focused).Render("╭" + truncate(label, max(1, width-2)) + strings.Repeat("─", max(0, width-2-lipgloss.Width(label))) + "╮")
}

func panelRow(content string, width int, focused bool) string {
	inner := max(1, width-2)
	borderStyle := dashboardPanelBorderStyle(focused)
	return borderStyle.Render("│") + lipgloss.NewStyle().Width(inner).Render(truncate(content, inner)) + borderStyle.Render("│")
}

func contentPanel(title string, content []string, width, height int, focused bool) string {
	rows := []string{titledPanelTop(title, width, focused)}
	for _, row := range content {
		if len(rows) >= height-1 {
			break
		}
		rows = append(rows, panelRow(row, width, focused))
	}
	for len(rows) < height-1 {
		rows = append(rows, panelRow("", width, focused))
	}
	if height > 1 {
		rows = append(rows, panelBorderLine("╰", "─", "╯", width, focused))
	}
	return strings.Join(rows[:min(len(rows), height)], "\n")
}

const graphPaneMinWidth = 12

func graphPaneWidthsAt(total int, ratio float64, hasChangedFiles bool) (left, right int) {
	total = max(2, total)
	if ratio > 0 {
		left = int(float64(total)*ratio + 0.5)
	} else if hasChangedFiles {
		left = max(graphPaneMinWidth, total/3)
	} else {
		left = max(graphPaneMinWidth, total*2/3)
	}
	if total >= graphPaneMinWidth*2 {
		left = min(total-graphPaneMinWidth, max(graphPaneMinWidth, left))
	} else {
		left = min(total-1, max(1, left))
	}
	return left, max(1, total-left)
}

func (m Model) graphPaneWidths(hasChangedFiles bool) (left, right int) {
	return graphPaneWidthsAt(max(2, m.width-2), m.graphSplitRatio, hasChangedFiles)
}

func (m Model) graphHasChangedFiles() bool {
	items := m.visibleListItems()
	index := m.cursor[m.kind()]
	return index >= 0 && index < len(items) && len(items[index].Paths) > 0
}

func (m Model) commitPanel(width, height, messageRows int) string {
	focused := m.focus == focusCommitMessage
	title := fmt.Sprintf("Commit ( %d↓ %d↑ )", m.workspaceRemote.Behind, m.workspaceRemote.Ahead)
	rows := []string{titledPanelTop(title, width, focused)}
	value := m.commitMessage.Value()
	runes := []rune(value)
	segments := m.commitMessageSegments(max(1, width-4))
	cursorRow := -1
	if focused && len(runes) > 0 {
		position := m.commitMessage.Position()
		for index, segment := range segments {
			atLineBreak := position == segment.end && position < len(runes) && runes[position] == commitMessageNewlineRune
			if position < segment.end || atLineBreak || position == len(runes) && index == len(segments)-1 {
				cursorRow = index
				break
			}
		}
	}
	for index := 0; index < messageRows && len(rows) < height-2; index++ {
		content := ""
		segment := segments[min(index, len(segments)-1)]
		start, end := segment.start, segment.end
		if len(runes) == 0 && index == 0 {
			placeholder := []rune("Commit message")
			if focused {
				cursor := m.commitMessage.Cursor
				cursor.TextStyle = metaStyle
				cursor.SetChar(string(placeholder[0]))
				content = cursor.View() + metaStyle.Render(string(placeholder[1:]))
			} else {
				content = metaStyle.Render(string(placeholder))
			}
		} else if start < len(runes) || focused && cursorRow == index {
			position := m.commitMessage.Position()
			cursorInRow := cursorRow == index
			if focused && cursorInRow {
				cursor := m.commitMessage.Cursor
				cursor.TextStyle = m.commitMessage.TextStyle
				before := string(runes[start:min(position, len(runes))])
				if position < len(runes) && runes[position] != commitMessageNewlineRune {
					cursor.SetChar(string(runes[position]))
					content = before + cursor.View() + string(runes[position+1:end])
				} else {
					cursor.SetChar(" ")
					content = before + cursor.View()
				}
			} else {
				content = string(runes[start:end])
			}
		}
		rows = append(rows, panelRow(" "+content, width, focused))
	}
	if len(rows) < height-1 {
		labels := []string{"Commit"}
		if m.workspaceRemote.Available {
			labels = []string{"Pull", "Push", "Commit"}
		}
		available := max(1, width-2)
		gapWidth := len(labels) - 1
		buttonWidth := max(1, (available-gapWidth)/len(labels))
		buttons := make([]string, 0, len(labels))
		for _, label := range labels {
			style := dashboardCommitButtonStyle
			if label == "Pull" {
				style = pullButtonStyle
				if !m.commitCanPull() {
					style = disabledButtonStyle
				}
			} else if label == "Push" {
				style = pushButtonStyle
				if !m.commitCanPush() {
					style = disabledButtonStyle
				}
			} else if !m.commitCanCommit() {
				style = disabledButtonStyle
			}
			buttons = append(buttons, style.Copy().Width(buttonWidth).Align(lipgloss.Center).Render(label))
		}
		rows = append(rows, panelRow(truncate(strings.Join(buttons, " "), available), width, focused))
	}
	for len(rows) < height-1 {
		rows = append(rows, panelRow("", width, focused))
	}
	if height > 1 {
		rows = append(rows, panelBorderLine("╰", "─", "╯", width, focused))
	}
	return strings.Join(rows[:min(len(rows), height)], "\n")
}

func (m Model) commitCanPull() bool {
	return m.workspaceRemote.Available && m.workspaceRemote.Behind > 0
}

func (m Model) commitCanPush() bool {
	return m.workspaceRemote.Available && m.workspaceRemote.Ahead > 0
}

func (m Model) commitCanCommit() bool {
	return strings.TrimSpace(m.commitMessageText()) != "" && len(m.workspaceStatus.Staged) > 0
}

func (m Model) changePanel(title string, staged bool, width, height int) string {
	focused := false
	if m.focus == focusWorkspaceList {
		changes := m.filteredWorkspaceChanges()
		if m.workspaceCursor >= 0 && m.workspaceCursor < len(changes) {
			focused = changes[m.workspaceCursor].staged == staged
		} else {
			focused = staged
		}
	}
	rows := []string{titledPanelTop(title, width, focused)}
	changes := m.dashboardPanelChanges(staged)
	if len(changes) == 0 && len(rows) < height-1 {
		emptyText := "No Changes Files"
		if staged {
			emptyText = "No Staging Files"
		}
		rows = append(rows, panelRow(" "+metaStyle.Render(emptyText), width, focused))
	}
	start := m.dashboardPanelStart(staged, height)
	for index := start; index < len(changes) && len(rows) < height-1; index++ {
		item := changes[index]
		path := m.highlightWorkspaceMatch(sanitizeWorkspaceLabel(item.name))
		innerWidth := max(1, width-2)
		indent := strings.Repeat("  ", item.depth)
		if item.isDir {
			marker := "▾ "
			icon := renderDirectoryIcon(true)
			if m.workspaceChangeCollapsed[workspaceChangeExpansionKey(staged, item.path)] {
				marker = "▸ "
				icon = renderDirectoryIcon(false)
			}
			arrow := stageArrowStyle.Render("↑")
			if staged {
				arrow = unstageArrowStyle.Render("↓")
			}
			actions := revertArrowStyle.Render("↶") + " " + arrow
			label := indent + marker + icon + " " + path
			label = lipgloss.NewStyle().Width(max(1, innerWidth-3)).Render(truncate(label, max(1, innerWidth-3)))
			row := label + actions
			if m.dashboardChangeSelected(item) {
				row = selectedRow.Copy().Width(max(1, innerWidth-3)).Render(ansi.Strip(label)) + actions
			}
			rows = append(rows, panelRow(row, width, focused))
			continue
		}
		arrow := stageArrowStyle.Render("↑")
		if staged {
			arrow = unstageArrowStyle.Render("↓")
		}
		label := fmt.Sprintf("%s %c %s %s", indent, item.change.Code, renderWorkspaceFileIcon(item.path), path)
		actions := revertArrowStyle.Render("↶") + " " + arrow
		label = lipgloss.NewStyle().Width(max(1, innerWidth-3)).Render(truncate(label, max(1, innerWidth-3)))
		row := label + actions
		if m.dashboardChangeSelected(item) {
			row = selectedRow.Copy().Width(max(1, innerWidth-3)).Render(ansi.Strip(label)) + actions
		}
		rows = append(rows, panelRow(row, width, focused))
	}
	for len(rows) < height-1 {
		rows = append(rows, panelRow("", width, focused))
	}
	if height > 1 {
		rows = append(rows, panelBorderLine("╰", "─", "╯", width, focused))
	}
	return strings.Join(rows[:min(len(rows), height)], "\n")
}

func (m Model) dashboardChangeSelected(item workspaceChange) bool {
	if m.focus != focusWorkspaceList {
		return false
	}
	changes := m.filteredWorkspaceChanges()
	if m.workspaceCursor < 0 || m.workspaceCursor >= len(changes) {
		return false
	}
	selected := changes[m.workspaceCursor]
	return selected.staged == item.staged && selected.path == item.path && selected.isDir == item.isDir
}

func (m Model) dashboardPanelChanges(staged bool) []workspaceChange {
	changes := m.workspaceStatus.Staged
	if !staged {
		changes = append(append([]worktree.Change{}, m.workspaceStatus.Unstaged...), m.workspaceStatus.Untracked...)
	}
	return m.visibleWorkspaceChangeTree(workspaceChangeTree(changes, staged))
}

func (m Model) dashboardPanelStart(staged bool, height int) int {
	capacity := max(0, height-2)
	changes := m.dashboardPanelChanges(staged)
	if capacity == 0 || len(changes) <= capacity || m.focus != focusWorkspaceList {
		return 0
	}
	selectedIndex := -1
	for index, change := range changes {
		if m.dashboardChangeSelected(change) {
			selectedIndex = index
			break
		}
	}
	if selectedIndex >= capacity {
		return selectedIndex - capacity + 1
	}
	return 0
}

func (m Model) fileDiffPanel(width, height int) string {
	focused := m.focus == focusWorkspacePreview
	rows := []string{titledPanelTop("File Diff", width, focused)}
	content := cropWorkspaceRows(m.workspaceDiffRows, max(1, height-2), m.workspacePreviewOffset)
	if m.workspacePreviewErr != nil {
		content = errorStyle.Render("Unable to load preview: " + sanitizeWorkspaceLabel(m.workspacePreviewErr.Error()))
	}
	for _, row := range strings.Split(content, "\n") {
		if len(rows) >= height-1 {
			break
		}
		rows = append(rows, panelRow(row, width, focused))
	}
	for len(rows) < height-1 {
		rows = append(rows, panelRow("", width, focused))
	}
	if height > 1 {
		rows = append(rows, panelBorderLine("╰", "─", "╯", width, focused))
	}
	return strings.Join(rows[:min(len(rows), height)], "\n")
}

func (m Model) activeTabLabel() string {
	labels := m.tabLabels()
	if m.active < 0 || m.active >= len(labels) {
		return ""
	}
	return labels[m.active]
}

func hasCommitGraphMetadata(items []provider.Item) bool {
	for _, item := range items {
		if len(item.Parents)+len(item.Refs) > 0 {
			return true
		}
	}
	return false
}

func (m Model) tabsView() string {
	labels := m.tabLabels()
	start, end := m.tabRange(labels)
	parts := make([]string, 0, end-start)
	widths := make([]int, 0, end-start)
	for index := start; index < end; index++ {
		name := labels[index]
		label := fmt.Sprintf(" %d %s ", index+1, name)
		widths = append(widths, lipgloss.Width(label))
		if index == m.active {
			style := activeTab
			if m.focus == focusTabs {
				style = style.Copy().Underline(true)
			}
			parts = append(parts, style.Render(label))
		} else {
			parts = append(parts, tabStyle.Render(label))
		}
	}
	borderStyle := lipgloss.NewStyle().Foreground(m.tabBorderStyle().GetBorderTopForeground())
	top := "╭"
	middle := borderStyle.Render("│")
	bottom := "├"
	for index, width := range widths {
		top += strings.Repeat("─", width)
		bottom += strings.Repeat("─", width)
		if index < len(widths)-1 {
			top += "┬"
			bottom += "┴"
			middle += parts[index] + borderStyle.Render("│")
		} else {
			top += "╮"
			bottom += "┴"
			middle += parts[index] + borderStyle.Render("│")
		}
	}
	extension := max(0, m.width-lipgloss.Width(bottom)-1)
	bottom += strings.Repeat("─", extension) + "╮"
	bottomStyle := borderStyle
	if m.filterFocused() {
		bottomStyle = lipgloss.NewStyle().Foreground(accent)
	}
	return strings.Join([]string{borderStyle.Render(top), middle, bottomStyle.Render(bottom)}, "\n")
}

func (m Model) tabBorderStyle() lipgloss.Style {
	if m.focus == focusTabs {
		return tabBoxStyle.Copy().BorderForeground(accent)
	}
	return tabBoxStyle
}

func (m Model) workspaceView() string {
	lines := make([]string, 0, m.height)
	remote := "unavailable"
	if m.backend != nil {
		remote = sanitizeWorkspaceLabel(m.backend.Name()) + "/" + sanitizeWorkspaceLabel(m.backend.Repository())
	}
	title := fmt.Sprintf("  local %s · remote %s", sanitizeWorkspaceLabel(m.workspace.Root()), remote)
	lines = append(lines, m.headerView(title))
	lines = append(lines, m.tabsView())
	if m.workspaceCommitActive() {
		return m.workspaceCommitDashboard(lines)
	}
	contentStart := len(lines)
	filter := m.fileFilter.View()
	if !m.fileFilter.Focused() {
		value := m.fileFilter.Value()
		if value == "" {
			value = metaStyle.Render("press / to filter paths")
		}
		filter = "Filter: " + value
	}
	lines = append(lines, contentBoxRaw(filterContentBoxRow(" "+truncate(filter, max(1, m.width-3)), m.width, m.filterFocused())))
	leftWidth, rightWidth := m.workspacePaneWidths()
	lines = append(lines, contentBoxRaw(workspaceContentDivider(leftWidth, rightWidth, m.filterFocused())))
	rightWidth += 3 // Reuse the removed divider width in the Preview panel.
	if m.workspaceCommitActive() {
		lines = append(lines, m.workspaceCommitComposer())
	}

	contentHeight := m.workspaceListHeight()
	bodyHeight := contentHeight + 2
	leftContentWidth := max(1, leftWidth-2)
	rightContentWidth := max(1, rightWidth-2)
	left := m.workspaceList(leftContentWidth, contentHeight)
	right := ""
	if m.workspacePreviewErr != nil {
		right = errorStyle.Render("Unable to load preview: " + truncate(sanitizeWorkspaceLabel(m.workspacePreviewErr.Error()), max(1, rightContentWidth-24)))
	} else if m.workspaceFilesActive() {
		right = renderWorkspaceFileWithImageAt(m.workspaceFile, m.workspaceImage, rightContentWidth, contentHeight, m.workspacePreviewOffset)
	} else {
		if len(m.workspaceDiffRows) == 0 {
			right = metaStyle.Render("Select a changed file to inspect its diff.")
		} else {
			right = cropWorkspaceRows(m.workspaceDiffRows, contentHeight, m.workspacePreviewOffset)
		}
	}
	left = contentPanel("File List", strings.Split(left, "\n"), leftWidth, bodyHeight, m.focus == focusWorkspaceList)
	right = contentPanel("Preview", strings.Split(right, "\n"), rightWidth, bodyHeight, m.focus == focusWorkspacePreview)
	body := lipgloss.JoinHorizontal(lipgloss.Top,
		lipgloss.NewStyle().Width(leftWidth).Height(bodyHeight).Render(left),
		lipgloss.NewStyle().Width(rightWidth).Height(bodyHeight).Render(right),
	)
	lines = append(lines, strings.Split(body, "\n")...)
	for renderedLineCount(lines) < m.height-3 {
		lines = append(lines, "")
	}
	lines = append(lines[:contentStart], append(contentBoxRows(lines[contentStart:], m.width), contentBoxBottom(m.width))...)
	lines = append(lines, m.statusLine())
	help := m.workspaceFocusHelp()
	lines = append(lines, metaStyle.Render(truncate(help, m.width)))
	return strings.Join(lines[:min(len(lines), m.height)], "\n")
}

func renderedLineCount(lines []string) int {
	count := 0
	for _, line := range lines {
		count += strings.Count(line, "\n") + 1
	}
	return count
}

func contentBoxRows(lines []string, width int) []string {
	innerWidth := max(1, width-2)
	style := lipgloss.NewStyle().Foreground(border)
	result := make([]string, 0, len(lines))
	for _, line := range lines {
		if strings.HasPrefix(line, contentBoxRawPrefix) {
			result = append(result, strings.TrimPrefix(line, contentBoxRawPrefix))
			continue
		}
		content := lipgloss.NewStyle().Width(innerWidth).Render(truncate(line, innerWidth))
		result = append(result, style.Render("│")+content+style.Render("│"))
	}
	return result
}

const contentBoxRawPrefix = "\x00content-box-raw:"

func contentBoxRaw(line string) string { return contentBoxRawPrefix + line }

func contentBoxDivider(width int, focused bool) string {
	innerWidth := max(1, width-2)
	return filterBorderStyle(focused).Render("├" + strings.Repeat("─", innerWidth) + "┤")
}

func workspaceContentDivider(leftWidth, rightWidth int, focused bool) string {
	return filterBorderStyle(focused).Render("├" + strings.Repeat("─", max(1, leftWidth+rightWidth+3)) + "┤")
}

func filterBorderStyle(focused bool) lipgloss.Style {
	color := border
	if focused {
		color = accent
	}
	return lipgloss.NewStyle().Foreground(color)
}

func filterContentBoxRow(content string, width int, focused bool) string {
	innerWidth := max(1, width-2)
	line := lipgloss.NewStyle().Width(innerWidth).Render(truncate(content, innerWidth))
	style := filterBorderStyle(focused)
	return style.Render("│") + line + style.Render("│")
}

func (m Model) filterFocused() bool {
	if m.localTab() {
		return m.fileFilter.Focused()
	}
	if m.workspace != nil && m.kind() == provider.Commits {
		return m.graphFilter.Focused()
	}
	return m.graphQuery.Focused()
}

func contentBoxBottom(width int) string {
	innerWidth := max(1, width-2)
	return lipgloss.NewStyle().Foreground(border).Render("╰" + strings.Repeat("─", innerWidth) + "╯")
}

func (m Model) workspaceFocusHelp() string {
	switch m.focus {
	case focusTabs:
		return "Tabs focused · ←/→ switch tabs · ↓ enter content"
	case focusCommitMessage:
		return "Commit message focused · Enter newline · Ctrl+S commit · ↓ changed files"
	case focusFileFilter:
		if m.workspaceCommitActive() {
			return "Search focused · type to filter files · ↓ commit message"
		}
		return "File filter focused · type to edit · ↓ file list"
	case focusWorkspacePreview:
		return "Preview focused · ← file list · ↑/↓ scroll · ↑ at top returns to input"
	case focusWorkspaceList:
		if m.workspaceCommitActive() {
			return "Changed files focused · ↑/↓ select · → preview · Space stage · Enter expand"
		}
		return "File list focused · ↑/↓ select · → preview · Enter expand"
	default:
		return "↓ move focus"
	}
}

func (m Model) workspaceDividerView(height int) string {
	rows := make([]string, max(1, height))
	return strings.Join(rows, "\n")
}

func (m Model) workspaceCommitComposer() string {
	const label = " Commit message: "
	button := commitButtonStyle.Render("Commit")
	contentWidth := max(1, m.width-2)
	inputWidth := max(1, contentWidth-lipgloss.Width(label)-lipgloss.Width(button)-2)
	input := m.commitMessage
	input.Width = inputWidth
	field := lipgloss.NewStyle().Width(inputWidth).Render(input.View())
	return truncate(label+field+" "+button, contentWidth)
}

func (m Model) headerView(title string) string {
	content := m.headerContent(title)
	if !m.updateAvailable {
		return lipgloss.NewStyle().Background(headerSlate).Width(m.width).Render(truncate(content, m.width))
	}
	button := updateButtonStyle.Render("Update")
	titleWidth := max(0, m.width-lipgloss.Width(button)-1)
	left := lipgloss.NewStyle().Width(titleWidth).Render(truncate(content, titleWidth))
	return truncate(left+" "+button, m.width)
}

func (m Model) headerContent(title string) string {
	// Powerlevel10k-inspired powerline segments give the product, version, and
	// current context distinct visual weight without adding another header row.
	brand := headerBrandStyle.Render("◆ Zzam Tiger")
	version := ""
	if m.currentVersion != "" {
		version = headerAccentStyle.Render("") + headerVersionStyle.Render(m.currentVersion)
	}
	warning := m.headerWarning()
	if warning != "" {
		version += headerWarningStyle.Render(warning)
	}
	context := strings.TrimSpace(title)
	if context == "" {
		return brand + version
	}
	return brand + version + headerContextEdge.Render("") + headerContextStyle.Render(context)
}

func (m Model) headerWarning() string {
	if m.remoteErr != nil {
		return " remote unavailable: " + sanitizeWorkspaceLabel(m.remoteErr.Error())
	}
	if m.filesOnly {
		return " Git repository not detected · file browser only"
	}
	return ""
}

func (m Model) updateButtonStart() int {
	return max(0, m.width-lipgloss.Width(updateButtonStyle.Render("Update")))
}

func (m Model) workspaceList(width, height int) string {
	if m.workspaceLoading && len(m.workspaceEntries) == 0 && m.workspaceFilesActive() ||
		m.workspaceLoading && len(m.filteredWorkspaceChanges()) == 0 && m.workspaceCommitActive() {
		return metaStyle.Render(" Loading…")
	}
	if m.err != nil {
		return errorStyle.Render(" " + truncate(sanitizeWorkspaceLabel(m.err.Error()), max(1, width-1)))
	}
	rows := make([]string, 0, height)
	if m.workspaceFilesActive() {
		displays := m.filteredWorkspaceEntryDisplays()
		start := min(m.workspaceOffset, len(displays))
		for index := start; index < len(displays) && len(rows) < height; index++ {
			display := displays[index]
			entry := display.entry
			prefix := "  "
			icon := renderWorkspaceFileIcon(entry.Name)
			if entry.IsDir {
				prefix = "▸ "
				icon = renderDirectoryIcon(false)
				if m.workspaceExpanded[entry.Path] {
					prefix = "▾ "
					icon = renderDirectoryIcon(true)
				}
			}
			name := m.highlightWorkspaceMatch(sanitizeWorkspaceLabel(entry.Name))
			row := strings.Repeat("  ", display.depth) + prefix + icon + " " + name
			row = lipgloss.NewStyle().Width(width).Render(truncate(row, width))
			if index == m.workspaceCursor {
				row = selectedRow.Render(row)
			}
			rows = append(rows, row)
		}
		if len(displays) == 0 {
			rows = append(rows, metaStyle.Render(" No matching files."))
		}
		return strings.Join(rows, "\n")
	}

	changes := m.filteredWorkspaceChanges()
	for _, display := range m.workspaceChangeRows() {
		if display.index < 0 {
			rows = append(rows, sectionTitleStyle.Render(truncate(" "+display.title, width)))
			continue
		}
		item := display.item
		change := item.change
		badge := string(change.Code)
		prefix := "  "
		icon := renderWorkspaceFileIcon(item.displayPath())
		if item.isDir {
			badge = " "
			prefix = "▾ "
			icon = renderDirectoryIcon(true)
			if m.workspaceChangeCollapsed[workspaceChangeExpansionKey(item.staged, item.path)] {
				prefix = "▸ "
				icon = renderDirectoryIcon(false)
			}
		} else if badge == "?" {
			badge = "U"
		}
		name := item.name
		if name == "" {
			name = item.displayPath()
		}
		name = m.highlightWorkspaceMatch(sanitizeWorkspaceLabel(name))
		row := fmt.Sprintf("  %s %s%s%s %s", badge, strings.Repeat("  ", item.depth), prefix, icon, name)
		row = lipgloss.NewStyle().Width(width).Render(truncate(row, width))
		if display.index == m.workspaceCursor {
			row = selectedRow.Render(row)
		}
		rows = append(rows, row)
	}
	if len(changes) == 0 {
		rows = append(rows, metaStyle.Render(" Working tree clean."))
	}
	return strings.Join(rows[:min(len(rows), height)], "\n")
}

func (m Model) filtersView() string {
	if m.workspace != nil && m.kind() == provider.Commits {
		scopes := []string{"All", "Mine", "Others"}
		parts := make([]string, 0, len(scopes))
		for index, scope := range scopes {
			label := " " + scope + " "
			if index == m.graphAuthorScope && m.focus == focusGraphFilters && !m.graphFilter.Focused() {
				parts = append(parts, focusedFilter.Render(label))
			} else if index == m.graphAuthorScope {
				parts = append(parts, activeFilter.Render(label))
			} else {
				parts = append(parts, filterStyle.Render(label))
			}
		}
		return " " + strings.Join(parts, " ")
	}
	filters := m.filters()
	parts := make([]string, 0, len(filters))
	for index, filter := range filters {
		label := " " + filter.Label + " "
		if index == m.filterIndex[m.kind()] && m.focus == focusListFilters {
			parts = append(parts, focusedFilter.Render(label))
		} else if index == m.filterIndex[m.kind()] {
			parts = append(parts, activeFilter.Render(label))
		} else {
			parts = append(parts, filterStyle.Render(label))
		}
	}
	return " " + strings.Join(parts, " ")
}

func (m Model) itemRow(item provider.Item, selected bool) string {
	return m.itemRowForKind(item, selected, m.kind(), m.width)
}

func (m Model) itemRowForKind(item provider.Item, selected bool, kind provider.Kind, width int) string {
	state := stateBadge(item.State)
	metaParts := make([]string, 0, 3)
	if assignableKind(kind) {
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
	contentWidth := max(1, width-lipgloss.Width(prefix)-1)
	metaWidth := min(lipgloss.Width(meta), max(0, contentWidth-8-1))
	titleWidth := max(1, contentWidth-metaWidth)
	if metaWidth > 0 {
		titleWidth--
	}
	title := m.highlightSearchMatch(truncate(item.Title, titleWidth))
	if item.AssignedToMe {
		title = myAssignmentTitle.Render(title)
	}
	row := prefix + title
	if metaWidth > 0 {
		row += " " + metaStyle.Render(m.highlightSearchMatch(truncate(meta, metaWidth)))
	}
	row = lipgloss.NewStyle().Width(max(1, width)).Render(row)
	if selected {
		return selectedRow.Render(row)
	}
	return row
}

func (m Model) milestoneIssuesPanel() string {
	parts := make([]string, 0, len(milestoneIssueFilters))
	for index, filter := range milestoneIssueFilters {
		label := " " + filter.Label + " "
		if index == m.milestoneIssueFilter {
			parts = append(parts, activeFilter.Render(label))
		} else {
			parts = append(parts, filterStyle.Render(label))
		}
	}
	rows := []string{" " + strings.Join(parts, " ")}
	items := m.filteredMilestoneIssues()
	if len(items) == 0 {
		rows = append(rows, metaStyle.Render(" No issues for this filter."))
	} else {
		innerWidth := max(1, m.viewport.Width-4)
		for index, issue := range items {
			rows = append(rows, m.itemRowForKind(issue, index == m.milestoneIssueCursor, provider.Issues, innerWidth))
		}
	}
	return contentPanel("Issues", rows, max(12, m.viewport.Width-2), len(rows)+2, true)
}

func (m Model) graphItemRow(item provider.Item, graph string, selected bool) string {
	refs := make([]string, 0, len(item.Refs))
	for _, ref := range item.Refs {
		label := ref.Name
		style := sectionTitleStyle
		if ref.Tag {
			label = "tag:" + label
			style = reviewMetaStyle
		} else if ref.Remote {
			style = metaStyle.Copy().Foreground(accent)
		}
		if ref.Head {
			label = "HEAD→" + label
			style = style.Copy().Bold(true).Foreground(green)
		}
		refs = append(refs, style.Render("["+m.highlightSearchMatch(label)+"]"))
	}
	prefix := " " + graph + " "
	refText := strings.Join(refs, " ")
	meta := strings.TrimSpace(strings.Join([]string{item.Meta, item.Author, relativeTime(item.UpdatedAt)}, " · "))
	reserved := lipgloss.Width(prefix) + lipgloss.Width(refText) + lipgloss.Width(meta) + 3
	title := m.highlightSearchMatch(truncate(item.Title, max(1, m.width-reserved)))
	row := prefix
	if refText != "" {
		row += refText + " "
	}
	row += title
	if meta != "" {
		row += " " + metaStyle.Render(m.highlightSearchMatch(meta))
	}
	row = lipgloss.NewStyle().Width(max(1, m.width)).MaxWidth(max(1, m.width)).Render(row)
	if selected {
		return selectedRow.Render(row)
	}
	return row
}

var graphMatchStyle = lipgloss.NewStyle().Background(lipgloss.Color("#625A2D")).Foreground(lipgloss.Color("#FFFFFF"))

// highlightSearchMatch highlights every case-insensitive, non-overlapping
// match in a visible list field without changing the original text or
// splitting UTF-8 runes. It is shared by Graph and every other list tab.
func (m Model) highlightSearchMatch(value string) string {
	return highlightMatch(value, m.activeListSearchQuery())
}

// highlightWorkspaceMatch applies the Files/Commit path filter to the local
// workspace rows, which do not use a provider list kind.
func (m Model) highlightWorkspaceMatch(value string) string {
	return highlightMatch(value, m.fileFilter.Value())
}

func highlightMatch(value, query string) string {
	query = strings.TrimSpace(query)
	if query == "" {
		return value
	}
	valueRunes, queryRunes := []rune(value), []rune(query)
	for i := range queryRunes {
		queryRunes[i] = unicode.ToLower(queryRunes[i])
	}
	var out strings.Builder
	for i := 0; i < len(valueRunes); {
		match := i+len(queryRunes) <= len(valueRunes)
		for j := range queryRunes {
			if !match || unicode.ToLower(valueRunes[i+j]) != queryRunes[j] {
				match = false
				break
			}
		}
		if match {
			out.WriteString(graphMatchStyle.Render(string(valueRunes[i : i+len(queryRunes)])))
			i += len(queryRunes)
			continue
		}
		out.WriteRune(valueRunes[i])
		i++
	}
	return out.String()
}

func (m Model) graphFileRows(item provider.Item) []string {
	if len(item.Paths) == 0 {
		return nil
	}
	paths := graphFilePaths(item)
	const indent = "   "
	// Fill the terminal width after accounting for the left indent, two border
	// cells, and the box's horizontal padding.
	contentWidth := max(1, m.width-lipgloss.Width(indent)-4)
	rows := graphTreeRows(paths)
	boxContentWidth := contentWidth
	for index, row := range rows {
		rows[index] = truncate(m.highlightSearchMatch(row), contentWidth)
	}
	// Build each bordered line independently. Lip Gloss multiline styles can
	// reflow only the first content row when its width and padding interact;
	// direct line assembly guarantees that tree rows are never split.
	boxedRows := make([]string, 0, len(rows)+2)
	boxedRows = append(boxedRows, indent+"╭"+strings.Repeat("─", boxContentWidth+2)+"╮")
	for _, row := range rows {
		boxedRows = append(boxedRows, indent+"│ "+padRight(row, boxContentWidth)+" │")
	}
	boxedRows = append(boxedRows, indent+"╰"+strings.Repeat("─", boxContentWidth+2)+"╯")
	return boxedRows
}

type graphTreeNode struct {
	dirs  map[string]*graphTreeNode
	files []string
}

func graphTreeRows(paths []string) []string {
	root := &graphTreeNode{dirs: make(map[string]*graphTreeNode)}
	for _, path := range paths {
		parts := strings.Split(path, "/")
		node := root
		for _, dir := range parts[:len(parts)-1] {
			if node.dirs[dir] == nil {
				node.dirs[dir] = &graphTreeNode{dirs: make(map[string]*graphTreeNode)}
			}
			node = node.dirs[dir]
		}
		node.files = append(node.files, parts[len(parts)-1])
	}
	rows := make([]string, 0, len(paths))
	var render func(*graphTreeNode, int)
	render = func(node *graphTreeNode, depth int) {
		dirs := make([]string, 0, len(node.dirs))
		for name := range node.dirs {
			dirs = append(dirs, name)
		}
		sort.Strings(dirs)
		for _, name := range dirs {
			child, label := node.dirs[name], name
			for len(child.files) == 0 && len(child.dirs) == 1 {
				for nextName, next := range child.dirs {
					label += "/" + nextName
					child = next
				}
			}
			rows = append(rows, strings.Repeat("  ", depth)+"▾ "+renderDirectoryIcon(true)+" "+label)
			render(child, depth+1)
		}
		sort.Strings(node.files)
		for _, name := range node.files {
			rows = append(rows, strings.Repeat("  ", depth)+"  "+renderWorkspaceFileIcon(name)+" "+name)
		}
	}
	render(root, 0)
	return rows
}

func graphFilePaths(item provider.Item) []string {
	paths := append([]string(nil), item.Paths...)
	sort.Strings(paths)
	return paths
}

func commitGraphPrefixes(items []provider.Item) []string {
	rows := make([]string, len(items))
	lanes := make([]string, 0, 8)
	for row, item := range items {
		lane := indexOfString(lanes, item.ID)
		if lane < 0 {
			lanes = append([]string{item.ID}, lanes...)
			lane = 0
		}
		parts := make([]string, len(lanes))
		for index := range lanes {
			parts[index] = "│"
		}
		parts[lane] = "●"
		rows[row] = strings.Join(parts, " ")
		if len(item.Parents) > 1 {
			rows[row] += "─┬"
		}

		next := append([]string(nil), lanes...)
		next = append(next[:lane], next[lane+1:]...)
		insert := make([]string, 0, len(item.Parents))
		for _, parent := range item.Parents {
			if parent == "" || indexOfString(insert, parent) >= 0 {
				continue
			}
			if existing := indexOfString(next, parent); existing >= 0 {
				next = append(next[:existing], next[existing+1:]...)
				if existing < lane {
					lane--
				}
			}
			insert = append(insert, parent)
		}
		next = append(next, make([]string, len(insert))...)
		copy(next[lane+len(insert):], next[lane:len(next)-len(insert)])
		copy(next[lane:], insert)
		lanes = next
	}
	return rows
}

func indexOfString(values []string, target string) int {
	for index, value := range values {
		if value == target {
			return index
		}
	}
	return -1
}

func (m Model) listHelp() string {
	if m.workspace != nil && !m.localTab() && m.kind() == provider.Commits {
		switch m.focus {
		case focusTabs:
			return "Tabs focused · ←/→ switch tabs · ↓ graph filters · r refresh · q quit"
		case focusGraphFilters:
			if m.graphFilter.Focused() {
				return "Graph search focused · type to filter · ↓ author filters · ↑ tabs · r refresh · q quit"
			}
			return "Graph author filters focused · ←/→ select · ↑ search · ↓ commit navigation · r refresh · q quit"
		case focusGraphCommits:
			return "Graph commits focused · ↑ at first returns to filters · → changed files · / search · r refresh · q quit"
		}
	}
	if m.focus == focusListSearch {
		return "Search focused · type to filter · ↓ filter options · ↑ tabs · Esc results"
	}
	if m.focus == focusListFilters {
		return "Filter options focused · ←/→ select · ↑ search · ↓ results · r refresh · q quit"
	}
	help := fmt.Sprintf(" ↑/↓ select · ←/→ filter · Shift+1...%d tabs · Enter detail", m.tabCount())
	if m.workspace != nil && m.kind() == provider.Commits {
		help = " ↑/↓ select · / search · ←/→ author scope · o checkout · p cherry-pick · z soft reset · Z hard reset · v revert"
	}
	if m.workspace != nil && m.kind() == provider.Branches {
		help = " ↑/↓ select · n create · o checkout · e rename · d delete · Enter detail"
	}
	if m.kind() == provider.Issues {
		help += " · C close · O open"
	}
	if assignableKind(m.kind()) {
		help += " · A assign · U unassign"
	}
	if m.kind() == provider.CIRuns {
		help += " · X cancel · R rerun"
	}
	return help + " · r refresh · q quit"
}

func (m Model) branchOverlay(background string) string {
	modalWidth := min(max(42, m.width-12), 76)
	if m.branchAction == "delete" {
		kind, target, operation := "local", "Local branch: "+m.branchTarget.ID, "git branch -d -- "+m.branchTarget.ID
		if m.branchTarget.State == "remote" {
			remote, name, _ := strings.Cut(m.branchTarget.ID, "/")
			kind = "remote"
			target = "Remote: " + remote + "\nBranch: " + name
			operation = "git push " + remote + " --delete " + name
		}
		modal := detailBoxStyle.Width(modalWidth).Render(sectionTitleStyle.Render("Delete "+kind+" branch?") + "\n\n" + target + "\n\n" + metaStyle.Render(operation) + "\n" + errorStyle.Render("This cannot be undone.") + "\n\n" + metaStyle.Render("y delete · n/Esc cancel"))
		return placeOverlay(m.width, m.height, modal, background)
	}
	title := "Create branch"
	hint := "Created from the selected branch, or HEAD when none is selected."
	if m.branchAction == "rename" {
		title, hint = "Rename branch "+m.branchTarget.ID, "Enter apply · Esc cancel"
	}
	modal := detailBoxStyle.Width(modalWidth).Render(sectionTitleStyle.Render(title) + "\n\n" + m.branchInput.View() + "\n\n" + metaStyle.Render(hint+" · Enter apply · Esc cancel"))
	return placeOverlay(m.width, m.height, modal, background)
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
	stateKey := strings.ToLower(state)
	if _, conclusion, ok := strings.Cut(stateKey, "/"); ok && conclusion != "" {
		stateKey = conclusion
	}
	switch stateKey {
	case "open", "opened", "active", "success", "passed":
		return style.Foreground(green).Render("● " + strings.ToUpper(state))
	case "queued", "pending", "waiting", "running", "in_progress", "created", "preparing", "scheduled", "manual":
		return style.Foreground(lipgloss.Color("#61AFEF")).Render("● " + strings.ToUpper(state))
	case "merged":
		return style.Foreground(accent).Render("◆ MERGED")
	case "closed", "failed", "failure", "cancelled", "canceled", "timed_out":
		return style.Foreground(red).Render("● " + strings.ToUpper(state))
	case "skipped", "neutral", "stale":
		return style.Foreground(muted).Render("● " + strings.ToUpper(state))
	case "commit":
		return style.Foreground(lipgloss.Color("#E5C07B")).Render("● COMMIT")
	default:
		return style.Foreground(lipgloss.Color("#61AFEF")).Render("● " + strings.ToUpper(state))
	}
}

func (m Model) detailView() string {
	kind := m.currentDetailKind()
	item := m.selected
	if m.detail.Item.ID != "" {
		item = m.detail.Item
	}
	title := fmt.Sprintf(" ← Esc  %s  %s", stateBadge(item.State), item.Title)
	lines := []string{m.headerView(title)}
	metaParts := []string{item.Meta}
	if assignableKind(kind) {
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
		failure := []string{"", errorStyle.Render("  Unable to load detail: " + truncate(sanitizeWorkspaceLabel(m.err.Error()), max(10, m.width-26)))}
		for len(failure) < m.viewport.Height {
			failure = append(failure, "")
		}
		lines = append(lines, strings.Join(failure[:m.viewport.Height], "\n"))
	} else {
		lines = append(lines, m.viewport.View())
	}
	lines = append(lines, m.statusLine())
	help := " ↑/↓ or wheel scroll · Esc back · r refresh"
	if kind == provider.PullRequests {
		help += " · D diff · R review · N comment · M merge"
	}
	if kind == provider.Commits {
		help += " · D diff · N comment"
	}
	if kind == provider.Issues {
		help += " · N comment · C close · O open · L labels"
	}
	if kind == provider.Milestones {
		help = " ←/→ filter · ↑/↓ select issue · Enter issue detail · C close · O open · A assign · U unassign · PgUp/PgDn scroll · Esc back"
	}
	if kind == provider.CIRuns {
		help += " · X cancel · R rerun"
	}
	if assignableKind(kind) {
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

func (m Model) diffView() string {
	item := m.selected
	if m.detail.Item.ID != "" {
		item = m.detail.Item
	}
	title := fmt.Sprintf(" ← Esc  %s  Diff · %s", stateBadge(item.State), item.Title)
	lines := []string{m.headerView(title)}
	fileLabel := " No changed files"
	if m.diffFile >= 0 && m.diffFile < len(m.detail.Diffs) {
		file := m.detail.Diffs[m.diffFile]
		fileLabel = fmt.Sprintf(" File %d/%d · %s", m.diffFile+1, len(m.detail.Diffs), diffPath(file))
	}
	lines = append(lines, metaStyle.Render(truncate(fileLabel, m.width)))
	lines = append(lines, m.viewport.View())
	lines = append(lines, m.statusLine())
	help := " h/l file · j/k line · drag review · Enter review · P reply · X resolve · Esc detail"
	if m.currentDetailKind() == provider.Commits {
		help = " h/l file · j/k line · Enter comment · Esc detail"
	}
	lines = append(lines, metaStyle.Render(truncate(help, m.width)))
	return strings.Join(lines, "\n")
}

func (m Model) commentOverlay(background string) string {
	title := "✎ Comment"
	if m.commentMode == generalReview {
		title = "✓ Review"
	}
	if m.commentMode == inlineReview {
		title = "⌁ Inline review"
		if m.commentKind == provider.Commits {
			title = "⌁ Commit comment"
		}
		if m.commentTargetSet {
			start, end := reviewTargetLines(m.commentTarget)
			location := fmt.Sprintf("%d", end)
			if start != end {
				location = fmt.Sprintf("%d-%d", start, end)
			}
			prefix := "Inline review"
			if m.commentKind == provider.Commits {
				prefix = "Commit comment"
			}
			title = fmt.Sprintf("⌁ %s · %s:%s", prefix, reviewPath(m.commentTarget), location)
		}
	}
	if m.commentMode == reviewReply {
		title = "↳ Reply to review"
	}
	composerWidth := max(20, min(96, m.width-6))
	comment := m.comment
	comment.SetWidth(max(12, composerWidth-4))
	body := sectionTitleStyle.Render(title) + "\n" + comment.View()
	if m.err != nil {
		body += "\n" + errorStyle.Render(sanitizeWorkspaceLabel(m.err.Error()))
	}
	body += "\n" + metaStyle.Render("Ctrl+S submit · Esc cancel")
	composer := composerStyle.Width(composerWidth).Render(body)
	return placeBottomOverlay(m.width, m.height, composer, background)
}

func reviewTargetLines(target provider.ReviewTarget) (int, int) {
	if target.Side == provider.ReviewSideOld {
		return target.StartOldLine, target.OldLine
	}
	return target.StartNewLine, target.NewLine
}

func reviewPath(target provider.ReviewTarget) string {
	if strings.TrimSpace(target.NewPath) != "" {
		return target.NewPath
	}
	if strings.TrimSpace(target.OldPath) != "" {
		return target.OldPath
	}
	return "unknown file"
}

func diffPath(file provider.DiffFile) string {
	if strings.TrimSpace(file.NewPath) != "" {
		return file.NewPath
	}
	if strings.TrimSpace(file.OldPath) != "" {
		return file.OldPath
	}
	return "unknown file"
}

func renderDiffFile(files []provider.DiffFile, fileIndex, selectedLine, rangeAnchor, width int) string {
	return renderDiffFileState(files, fileIndex, selectedLine, rangeAnchor, -1, width)
}

func renderDiffFileState(files []provider.DiffFile, fileIndex, selectedLine, rangeAnchor, selectedReview, width int) string {
	if len(files) == 0 {
		return metaStyle.Render("No patch was provided for this change.")
	}
	if fileIndex < 0 || fileIndex >= len(files) {
		fileIndex = 0
	}
	file := files[fileIndex]
	highlighter := newCodeHighlighter(diffPath(file))
	lines := []string{sectionTitleStyle.Render(diffPath(file))}
	if file.OldPath != "" && file.NewPath != "" && file.OldPath != file.NewPath {
		lines = append(lines, metaStyle.Render(file.OldPath+" → "+file.NewPath))
	}
	if file.TooLarge {
		lines = append(lines, errorStyle.Render("Diff is too large or collapsed by the provider."))
	}
	if len(file.Lines) == 0 {
		lines = append(lines, metaStyle.Render("No patch content is available for this file."))
		return strings.Join(lines, "\n")
	}
	split := width >= 100
	column := max(12, (width-3)/2)
	if split {
		lines = append(lines, metaStyle.Render(padRight("OLD", column)+" │ "+padRight("NEW", column)))
	}
	for index, line := range file.Lines {
		line.Text = expandDiffTabs(sanitizeWorkspaceText(line.Text))
		inRange := rangeAnchor >= 0 && index >= min(rangeAnchor, selectedLine) && index <= max(rangeAnchor, selectedLine)
		isSelected := index == selectedLine
		oldNumber := ""
		newNumber := ""
		if line.OldLine > 0 {
			oldNumber = fmt.Sprintf("%d", line.OldLine)
		}
		if line.NewLine > 0 {
			newNumber = fmt.Sprintf("%d", line.NewLine)
		}
		marker, content := diffLineParts(line.Text)
		rows := []string{}
		if split {
			rows = renderSplitDiffRows(oldNumber, newNumber, marker, content, column, highlighter)
		} else {
			const gutterWidth = 13 // "0000 0000 │ +"
			contentWidth := max(1, width-gutterWidth)
			// Wrap the plain source before adding ANSI syntax styles. Wrapping the
			// highlighted string can split style sequences and make the enclosing
			// Lip Gloss box wrap the row a second time at irregular positions.
			wrapped := strings.Split(ansi.Hardwrap(content, contentWidth, true), "\n")
			rows = append(rows, fmt.Sprintf("%4s %4s │ %s%s", oldNumber, newNumber, marker, highlighter.line(wrapped[0])))
			for _, continuation := range wrapped[1:] {
				rows = append(rows, "          │  "+highlighter.line(continuation))
			}
		}
		isAddition := strings.HasPrefix(line.Text, "+")
		isRemoval := strings.HasPrefix(line.Text, "-")
		for _, row := range rows {
			if !isAddition && !isRemoval {
				row = metaStyle.Render(row)
			}
			if !split && (isAddition || isRemoval) {
				row = padRight(row, width)
			}
			switch {
			case isSelected:
				row = renderDiffBackground(row, "#315F85")
			case inRange:
				row = renderDiffBackground(row, "#244B6B")
			case isAddition:
				row = renderDiffBackground(row, "#203C2F")
			case isRemoval:
				row = renderDiffBackground(row, "#482B31")
			}
			lines = append(lines, row)
		}
		for _, reviewIndex := range reviewIndexesEndingAt(file.Reviews, line) {
			lines = append(lines, renderDiffReviewState(file.Reviews[reviewIndex], width, reviewIndex == selectedReview))
		}
	}
	for reviewIndex, review := range file.Reviews {
		if review.Outdated || review.OldLine == 0 && review.NewLine == 0 {
			lines = append(lines, renderDiffReviewState(review, width, reviewIndex == selectedReview))
		}
	}
	return strings.Join(lines, "\n")
}

func expandDiffTabs(value string) string {
	// Lip Gloss expands every tab to four cells during its final render. Do it
	// before wrapping and column measurement so split separators stay fixed.
	return strings.ReplaceAll(value, "\t", "    ")
}

func renderSplitDiffRows(oldNumber, newNumber, marker, content string, column int, highlighter codeHighlighter) []string {
	const gutterWidth = 7 // "0000 + "
	wrapped := strings.Split(ansi.Hardwrap(content, max(1, column-gutterWidth), true), "\n")
	if len(wrapped) == 0 {
		wrapped = []string{""}
	}
	rows := make([]string, 0, len(wrapped))
	for index, fragment := range wrapped {
		left, right := "", ""
		prefixOld, prefixNew := "       ", "       "
		if index == 0 {
			prefixOld = fmt.Sprintf("%4s - ", oldNumber)
			prefixNew = fmt.Sprintf("%4s + ", newNumber)
			if marker != "+" && marker != "-" {
				prefixOld = fmt.Sprintf("%4s   ", oldNumber)
				prefixNew = fmt.Sprintf("%4s   ", newNumber)
			}
		}
		highlighted := highlighter.line(fragment)
		switch marker {
		case "+":
			right = prefixNew + highlighted
		case "-":
			left = prefixOld + highlighted
		default:
			left = prefixOld + highlighted
			right = prefixNew + highlighted
		}
		left = padRight(left, column)
		right = padRight(right, column)
		if strings.TrimSpace(ansi.Strip(left)) == "" {
			left = diffGapStyle.Render(strings.Repeat(" ", column))
		}
		if strings.TrimSpace(ansi.Strip(right)) == "" {
			right = diffGapStyle.Render(strings.Repeat(" ", column))
		}
		rows = append(rows, left+metaStyle.Render(" │ ")+right)
	}
	return rows
}

func reviewsEndingAt(reviews []provider.DiffReview, line provider.DiffLine) []provider.DiffReview {
	matched := make([]provider.DiffReview, 0)
	for _, index := range reviewIndexesEndingAt(reviews, line) {
		matched = append(matched, reviews[index])
	}
	return matched
}

func reviewIndexesEndingAt(reviews []provider.DiffReview, line provider.DiffLine) []int {
	matched := make([]int, 0)
	for index, review := range reviews {
		if review.Outdated {
			continue
		}
		side := review.Side
		if side == "" {
			if review.NewLine > 0 {
				side = provider.ReviewSideNew
			} else {
				side = provider.ReviewSideOld
			}
		}
		if side == provider.ReviewSideNew && review.NewLine > 0 && review.NewLine == line.NewLine ||
			side == provider.ReviewSideOld && review.OldLine > 0 && review.OldLine == line.OldLine {
			matched = append(matched, index)
		}
	}
	return matched
}

func renderDiffReview(review provider.DiffReview, width int) string {
	return renderDiffReviewState(review, width, false)
}

func renderDiffReviewState(review provider.DiffReview, width int, selected bool) string {
	collapsible := review.Resolved || review.Outdated
	meta := reviewMetaText(review)
	if collapsible {
		if selected {
			meta += " [Close]"
		} else {
			meta += " [Open]"
		}
	}
	if collapsible && !selected {
		return "  " + reviewMetaStyle.Render(truncate(meta, max(1, width-2)))
	}
	body := strings.TrimSpace(review.Body)
	if body == "" {
		body = "No review body."
	}
	contentWidth := max(8, width-5)
	body = renderReviewMarkdown(body, contentWidth)
	rendered := "  " + reviewMetaStyle.Render(truncate(meta, max(1, width-2))) + "\n" + reviewBodyStyle.Copy().MarginLeft(2).MaxWidth(contentWidth).Render(body)
	if selected {
		return selectedReviewStyle.Render(rendered)
	}
	return rendered
}

func renderReviewMarkdown(markdown string, width int) string {
	renderer, err := glamour.NewTermRenderer(
		glamour.WithStandardStyle("dark"),
		glamour.WithWordWrap(max(10, width)),
		glamour.WithPreservedNewLines(),
	)
	if err != nil {
		return wrapReviewBody(markdown, width)
	}
	rendered, err := renderer.Render(markdown)
	if err != nil {
		return wrapReviewBody(markdown, width)
	}
	return strings.Trim(rendered, "\n")
}

func reviewMetaText(review provider.DiffReview) string {
	location := reviewLineLabel(review)
	meta := "↳"
	if review.Resolved {
		meta += " [Resolved]"
	} else if review.Resolvable {
		meta += " [Resolve]"
	}
	if review.Replyable && (review.ThreadID != "" || review.ReplyToID != "") {
		meta += " [Reply]"
	}
	meta += " @" + strings.TrimPrefix(strings.TrimSpace(review.Author), "@")
	if location != "" {
		meta += " · " + location
	}
	if !review.CreatedAt.IsZero() {
		meta += " · " + relativeTime(review.CreatedAt)
	}
	if review.Outdated {
		meta += " · outdated"
	}
	return meta
}

func reviewLineLabel(review provider.DiffReview) string {
	if review.FileLevel {
		return "file"
	}
	start, end := review.StartNewLine, review.NewLine
	if review.Side == provider.ReviewSideOld || end == 0 {
		start, end = review.StartOldLine, review.OldLine
	}
	if end == 0 {
		return ""
	}
	if start > 0 && start != end {
		return fmt.Sprintf("lines %d–%d", start, end)
	}
	return fmt.Sprintf("line %d", end)
}

func wrapReviewBody(body string, width int) string {
	var wrapped []string
	for _, paragraph := range strings.Split(body, "\n") {
		words := strings.Fields(paragraph)
		if len(words) == 0 {
			wrapped = append(wrapped, "")
			continue
		}
		line := words[0]
		for _, word := range words[1:] {
			if lipgloss.Width(line)+1+lipgloss.Width(word) <= width {
				line += " " + word
				continue
			}
			wrapped = append(wrapped, line)
			line = word
		}
		wrapped = append(wrapped, line)
	}
	return strings.Join(wrapped, "\n")
}

func (m Model) statusLine() string {
	if m.err != nil {
		return errorStyle.Render(truncate(" Error: "+sanitizeWorkspaceLabel(m.err.Error()), m.width))
	}
	if m.workspaceWatcherErr != nil {
		return errorStyle.Render(truncate(" Error: "+sanitizeWorkspaceLabel(m.workspaceWatcherErr.Error()), m.width))
	}
	if m.status != "" {
		return statusStyle.Render(truncate(" "+m.status, m.width))
	}
	if m.loadingList || m.loadingDetail || m.workspaceLoading || m.workspacePreviewLoading || m.actionBusy {
		return metaStyle.Render(" Updating…")
	}
	if !m.lastUpdated.IsZero() {
		limit := ""
		if m.screen == listScreen && !m.localTab() && len(m.items[m.kind()]) >= 100 {
			limit = " · showing latest 100"
		}
		refresh := "local filesystem watch"
		if !m.localTab() {
			refresh = "remote auto-refresh " + refreshLabel(m.refresh)
		}
		return metaStyle.Render(fmt.Sprintf(" Updated %s · %s%s", m.lastUpdated.Format("15:04:05"), refresh, limit))
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
	if ansi.StringWidth(value) <= width {
		return value
	}
	if width <= 3 {
		return strings.Repeat(".", width)
	}
	return ansi.Truncate(value, width, "...")
}

func placeOverlay(width, height int, foreground, background string) string {
	return placeOverlayAt(width, height, foreground, background, max(0, (height-lipgloss.Height(foreground))/2))
}

func placeBottomOverlay(width, height int, foreground, background string) string {
	backgroundHeight := len(strings.Split(background, "\n"))
	startY := max(0, min(height, backgroundHeight)-lipgloss.Height(foreground))
	return placeOverlayAt(width, height, foreground, background, startY)
}

func placeOverlayAt(width, height int, foreground, background string, startY int) string {
	fgLines := strings.Split(foreground, "\n")
	bgLines := strings.Split(background, "\n")
	fgWidth := lipgloss.Width(foreground)
	startX := max(0, (width-fgWidth)/2)
	for y, line := range fgLines {
		row := startY + y
		if row >= len(bgLines) {
			break
		}
		left := strings.Repeat(" ", startX)
		if startX > 3 {
			left = truncate(bgLines[row], startX)
		}
		padding := strings.Repeat(" ", max(0, startX-lipgloss.Width(left)))
		bgLines[row] = left + padding + line
	}
	return strings.Join(bgLines, "\n")
}
