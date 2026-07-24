package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/SKAIBlue/zzam-tiger/internal/provider"
)

var (
	accent              = lipgloss.Color("#7D56F4")
	green               = lipgloss.Color("#3DDC97")
	red                 = lipgloss.Color("#FF6B6B")
	muted               = lipgloss.Color("#7B8496")
	text                = lipgloss.Color("#E6E9EF")
	border              = lipgloss.Color("#4B5263")
	headerPurple        = lipgloss.Color("#6C4EE3")
	headerBlue          = lipgloss.Color("#2E86C1")
	headerSlate         = lipgloss.Color("#273142")
	headerStyle         = lipgloss.NewStyle().Bold(true).Foreground(text)
	versionStyle        = lipgloss.NewStyle().Foreground(lipgloss.Color("#61AFEF"))
	tabStyle            = lipgloss.NewStyle().Foreground(muted)
	activeTab           = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#FFFFFF")).Background(accent)
	filterStyle         = lipgloss.NewStyle().Foreground(muted)
	activeFilter        = lipgloss.NewStyle().Bold(true).Foreground(green).Underline(true)
	selectedRow         = lipgloss.NewStyle().Foreground(lipgloss.Color("#FFFFFF")).Background(lipgloss.Color("#343B58"))
	myAssignmentTitle   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#E5C07B"))
	metaStyle           = lipgloss.NewStyle().Foreground(muted)
	errorStyle          = lipgloss.NewStyle().Foreground(red)
	statusStyle         = lipgloss.NewStyle().Foreground(green)
	sectionTitleStyle   = lipgloss.NewStyle().Bold(true).Foreground(accent)
	detailBoxStyle      = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(border).Padding(0, 1)
	composerStyle       = detailBoxStyle.Copy().BorderForeground(accent).Background(lipgloss.Color("#1B1F2A"))
	addedLineStyle      = lipgloss.NewStyle().Background(lipgloss.Color("#203C2F"))
	removedLineStyle    = lipgloss.NewStyle().Background(lipgloss.Color("#482B31"))
	diffGapStyle        = lipgloss.NewStyle().Background(lipgloss.Color("#2D3348"))
	reviewMetaStyle     = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#E5C07B"))
	reviewBodyStyle     = lipgloss.NewStyle().Foreground(text).BorderLeft(true).BorderStyle(lipgloss.ThickBorder()).BorderForeground(accent).PaddingLeft(1)
	selectedReviewStyle = lipgloss.NewStyle().Background(lipgloss.Color("#2D3348"))
	commitButtonStyle   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#FFFFFF")).Background(accent).Padding(0, 1)
	updateButtonStyle   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#FFFFFF")).Background(green).Padding(0, 1)
	headerBrandStyle    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#FFFFFF")).Background(headerPurple).Padding(0, 1)
	headerVersionStyle  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#DCE7FF")).Background(headerBlue).Padding(0, 1)
	headerContextStyle  = lipgloss.NewStyle().Foreground(text).Background(headerSlate).Padding(0, 1)
	headerAccentStyle   = lipgloss.NewStyle().Foreground(headerPurple).Background(headerBlue)
	headerContextEdge   = lipgloss.NewStyle().Foreground(headerBlue).Background(headerSlate)
	headerWarningStyle  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#FFE8E8")).Background(lipgloss.Color("#651C2A")).Padding(0, 1)
)

func (m Model) View() string {
	if m.width == 0 || m.height == 0 {
		return "Starting Zzam Tiger…"
	}
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
	return m.listView()
}

func (m Model) listView() string {
	if m.localTab() {
		return m.workspaceView()
	}
	lines := make([]string, 0, m.height)
	title := "  remote unavailable"
	if m.backend != nil {
		title = fmt.Sprintf("  %s · %s", sanitizeWorkspaceLabel(m.backend.Name()), sanitizeWorkspaceLabel(m.backend.Repository()))
	}
	lines = append(lines, m.headerView(title))
	lines = append(lines, m.tabsView())
	lines = append(lines, strings.Repeat("─", max(1, m.width)))
	if m.remoteErr == nil {
		lines = append(lines, m.filtersView())
	} else {
		lines = append(lines, metaStyle.Render(" Remote integration unavailable"))
	}
	if m.localErr != nil {
		lines = append(lines, metaStyle.Render(" Local Git features unavailable: "+truncate(sanitizeWorkspaceLabel(m.localErr.Error()), max(1, m.width-34))))
	}
	lines = append(lines, "")

	items := m.items[m.kind()]
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
				lines = append(lines, m.graphItemRow(items[index], graphPrefixes[index], index == m.cursor[m.kind()]))
			} else {
				lines = append(lines, m.itemRow(items[index], index == m.cursor[m.kind()]))
			}
		}
	}
	for len(lines) < m.height-2 {
		lines = append(lines, "")
	}
	lines = append(lines, m.statusLine())
	lines = append(lines, metaStyle.Render(truncate(m.listHelp(), m.width)))
	return strings.Join(lines[:min(len(lines), m.height)], "\n")
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
	for index := start; index < end; index++ {
		name := labels[index]
		label := fmt.Sprintf(" %d %s ", index+1, name)
		if index == m.active {
			parts = append(parts, activeTab.Render(label))
		} else {
			parts = append(parts, tabStyle.Render(label))
		}
	}
	leading, trailing := " ", ""
	if start > 0 {
		leading += metaStyle.Render("‹ ")
	}
	if end < len(labels) {
		trailing = metaStyle.Render(" ›")
	}
	return kittyDeleteImage() + leading + strings.Join(parts, " ") + trailing
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
	lines = append(lines, strings.Repeat("─", max(1, m.width)))
	filter := m.fileFilter.View()
	if !m.fileFilter.Focused() {
		value := m.fileFilter.Value()
		if value == "" {
			value = metaStyle.Render("press / to filter paths")
		}
		filter = "Filter files: " + value
	}
	lines = append(lines, " "+truncate(filter, max(1, m.width-1)))
	if m.workspaceCommitActive() {
		lines = append(lines, m.workspaceCommitComposer())
	} else {
		lines = append(lines, "")
	}

	bodyHeight := m.workspaceListHeight()
	leftWidth, rightWidth := m.workspacePaneWidths()
	left := m.workspaceList(leftWidth, bodyHeight)
	right := ""
	if m.workspaceFilesActive() {
		right = renderWorkspaceFileWithImageAt(m.workspaceFile, m.workspaceImage, rightWidth, bodyHeight, m.workspacePreviewOffset)
	} else {
		if len(m.workspaceDiffRows) == 0 {
			right = metaStyle.Render("Select a changed file to inspect its diff.")
		} else {
			right = cropWorkspaceRows(m.workspaceDiffRows, bodyHeight, m.workspacePreviewOffset)
		}
	}
	body := lipgloss.JoinHorizontal(lipgloss.Top,
		lipgloss.NewStyle().Width(leftWidth).Height(bodyHeight).Render(left),
		m.workspaceDividerView(bodyHeight),
		lipgloss.NewStyle().Width(rightWidth).Height(bodyHeight).Render(right),
	)
	lines = append(lines, strings.Split(body, "\n")...)
	for len(lines) < m.height-2 {
		lines = append(lines, "")
	}
	lines = append(lines, m.statusLine())
	help := " ↑/↓ select · Enter/→ expand · ← collapse · drag divider resize · PgUp/PgDn preview · / filter"
	if m.workspaceCommitActive() {
		help = " c message · Enter commit · drag divider resize · ↑/↓ select · Space toggle · s/u file or folder · S/U all · / filter"
	}
	lines = append(lines, metaStyle.Render(truncate(help, m.width)))
	return strings.Join(lines[:min(len(lines), m.height)], "\n")
}

func (m Model) workspaceDividerView(height int) string {
	divider := lipgloss.NewStyle().Foreground(border)
	glyph := " ┃ "
	if m.workspaceDividerDragging {
		divider = lipgloss.NewStyle().Bold(true).Foreground(accent)
	}
	rows := make([]string, max(1, height))
	for index := range rows {
		rows[index] = divider.Render(glyph)
	}
	return strings.Join(rows, "\n")
}

func (m Model) workspaceCommitComposer() string {
	const label = " Commit message: "
	button := commitButtonStyle.Render("Commit")
	inputWidth := max(1, m.width-lipgloss.Width(label)-lipgloss.Width(button)-2)
	input := m.commitMessage
	input.Width = inputWidth
	field := lipgloss.NewStyle().Width(inputWidth).Render(input.View())
	return truncate(label+field+" "+button, m.width)
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
		entries := m.filteredWorkspaceEntries()
		start := min(m.workspaceOffset, len(entries))
		for index := start; index < len(entries) && len(rows) < height; index++ {
			entry := entries[index]
			depth := strings.Count(entry.Path, "/")
			icon := "·"
			if entry.IsDir {
				icon = "▸"
				if m.workspaceExpanded[entry.Path] {
					icon = "▾"
				}
			}
			row := strings.Repeat("  ", depth) + icon + " " + sanitizeWorkspaceLabel(entry.Name)
			row = lipgloss.NewStyle().Width(width).Render(truncate(row, width))
			if index == m.workspaceCursor {
				row = selectedRow.Render(row)
			}
			rows = append(rows, row)
		}
		if len(entries) == 0 {
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
		icon := "·"
		if item.isDir {
			badge = " "
			icon = "▾"
			if m.workspaceChangeCollapsed[workspaceChangeExpansionKey(item.staged, item.path)] {
				icon = "▸"
			}
		} else if badge == "?" {
			badge = "U"
		}
		name := item.name
		if name == "" {
			name = item.displayPath()
		}
		row := fmt.Sprintf("  %s %s%s %s", badge, strings.Repeat("  ", item.depth), icon, sanitizeWorkspaceLabel(name))
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
		query := m.graphFilter.View()
		if !m.graphFilter.Focused() {
			if value := m.graphFilter.Value(); value != "" {
				query = "Search commits: " + value
			} else {
				query = "Search commits: press /"
			}
		}
		return " " + activeFilter.Render(" "+scopes[m.graphAuthorScope]+" ") + "  " + query
	}
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
		refs = append(refs, style.Render("["+label+"]"))
	}
	prefix := " " + graph + " "
	refText := strings.Join(refs, " ")
	meta := strings.TrimSpace(strings.Join([]string{item.Meta, item.Author, relativeTime(item.UpdatedAt)}, " · "))
	reserved := lipgloss.Width(prefix) + lipgloss.Width(refText) + lipgloss.Width(meta) + 3
	title := truncate(item.Title, max(1, m.width-reserved))
	row := prefix
	if refText != "" {
		row += refText + " "
	}
	row += title
	if meta != "" {
		row += " " + metaStyle.Render(meta)
	}
	row = lipgloss.NewStyle().Width(max(1, m.width)).MaxWidth(max(1, m.width)).Render(row)
	if selected {
		return selectedRow.Render(row)
	}
	return row
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
	help := fmt.Sprintf(" ↑/↓ select · ←/→ filter · Shift+1...%d tabs · Enter detail", m.tabCount())
	if m.workspace != nil && m.kind() == provider.Commits {
		help = " ↑/↓ select · / search · ←/→ author scope · o checkout · p cherry-pick · z soft reset · Z hard reset · v revert"
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
	item := m.selected
	if m.detail.Item.ID != "" {
		item = m.detail.Item
	}
	title := fmt.Sprintf(" ← Esc  %s  %s", stateBadge(item.State), item.Title)
	lines := []string{m.headerView(title)}
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
	if m.kind() == provider.PullRequests {
		help += " · D diff · R review · N comment · M merge"
	}
	if m.kind() == provider.Commits {
		help += " · D diff · N comment"
	}
	if m.kind() == provider.Issues {
		help += " · N comment · C close · O open · L labels"
	}
	if m.kind() == provider.CIRuns {
		help += " · X cancel · R rerun"
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
	if m.kind() == provider.Commits {
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
		line.Text = sanitizeWorkspaceText(line.Text)
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
		highlighted := content
		highlighted = highlighter.line(content)
		row := fmt.Sprintf("%4s %4s │ %s%s", oldNumber, newNumber, marker, highlighted)
		if split {
			left, right := "", ""
			switch marker {
			case "+":
				right = fmt.Sprintf("%4s + %s", newNumber, highlighted)
			case "-":
				left = fmt.Sprintf("%4s - %s", oldNumber, highlighted)
			default:
				left = fmt.Sprintf("%4s   %s", oldNumber, highlighted)
				right = fmt.Sprintf("%4s   %s", newNumber, highlighted)
			}
			left = padRight(truncate(left, column), column)
			right = padRight(truncate(right, column), column)
			if left == strings.Repeat(" ", column) {
				left = diffGapStyle.Render(left)
			}
			if right == strings.Repeat(" ", column) {
				right = diffGapStyle.Render(right)
			}
			row = left + metaStyle.Render(" │ ") + right
		}
		isAddition := strings.HasPrefix(line.Text, "+")
		isRemoval := strings.HasPrefix(line.Text, "-")
		if !isAddition && !isRemoval {
			row = metaStyle.Render(row)
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
	meta := reviewMetaText(review)
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
