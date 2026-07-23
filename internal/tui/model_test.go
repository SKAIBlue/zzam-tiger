package tui

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/SKAIBlue/zzam-tiger/internal/provider"
)

type fakeProvider struct{}

func (fakeProvider) Name() string       { return "TestHub" }
func (fakeProvider) Repository() string { return "owner/repo" }
func (fakeProvider) TabName(kind provider.Kind) string {
	if kind == provider.PullRequests {
		return "PRs"
	}
	return kind.String()
}
func (fakeProvider) Filters(kind provider.Kind) []provider.Filter {
	if kind == provider.PullRequests {
		return []provider.Filter{{Label: "Open", Value: "open"}, {Label: "Merged", Value: "merged"}}
	}
	return []provider.Filter{{Label: "All", Value: "all"}}
}
func (fakeProvider) List(context.Context, provider.Kind, provider.Filter) ([]provider.Item, error) {
	return nil, nil
}
func (fakeProvider) Detail(context.Context, provider.Kind, provider.Item) (provider.Detail, error) {
	return provider.Detail{}, nil
}
func (fakeProvider) AddComment(context.Context, provider.Kind, provider.Item, string) error {
	return nil
}
func (fakeProvider) AddReview(context.Context, provider.Item, string) error { return nil }
func (fakeProvider) AddReviewComment(context.Context, provider.Item, provider.ReviewTarget, string) error {
	return nil
}
func (fakeProvider) AddCommitComment(context.Context, provider.Item, provider.ReviewTarget, string) error {
	return nil
}
func (fakeProvider) AddReviewReply(context.Context, provider.Item, provider.ReviewThreadTarget, string) error {
	return nil
}
func (fakeProvider) ResolveReview(context.Context, provider.Item, provider.ReviewThreadTarget) error {
	return nil
}
func (fakeProvider) Merge(context.Context, provider.Item) error               { return nil }
func (fakeProvider) SetIssueState(context.Context, provider.Item, bool) error { return nil }
func (fakeProvider) SetAssigned(context.Context, provider.Kind, provider.Item, bool) error {
	return nil
}
func (fakeProvider) SetIssueLabels(context.Context, provider.Item, []string) error { return nil }
func (fakeProvider) CancelRun(context.Context, provider.Item) error                { return nil }
func (fakeProvider) Rerun(context.Context, provider.Item) error                    { return nil }

type assignmentCall struct {
	kind     provider.Kind
	item     provider.Item
	assigned bool
}

type actionProvider struct {
	fakeProvider
	states      []bool
	assignments []assignmentCall
	comments    []commentCall
	topReviews  []reviewCall
	reviews     []reviewCall
	commitLines []reviewCall
	replies     []replyCall
	resolved    []provider.ReviewThreadTarget
	cancelled   []provider.Item
	rerun       []provider.Item
}

type commentCall struct {
	kind provider.Kind
	item provider.Item
	body string
}

type reviewCall struct {
	item   provider.Item
	target provider.ReviewTarget
	body   string
}

type replyCall struct {
	item   provider.Item
	target provider.ReviewThreadTarget
	body   string
}

func (p *actionProvider) SetIssueState(_ context.Context, _ provider.Item, open bool) error {
	p.states = append(p.states, open)
	return nil
}

func (p *actionProvider) SetAssigned(_ context.Context, kind provider.Kind, item provider.Item, assigned bool) error {
	p.assignments = append(p.assignments, assignmentCall{kind: kind, item: item, assigned: assigned})
	return nil
}

func (p *actionProvider) AddComment(_ context.Context, kind provider.Kind, item provider.Item, body string) error {
	p.comments = append(p.comments, commentCall{kind: kind, item: item, body: body})
	return nil
}

func (p *actionProvider) AddReview(_ context.Context, item provider.Item, body string) error {
	p.topReviews = append(p.topReviews, reviewCall{item: item, body: body})
	return nil
}

func (p *actionProvider) AddReviewComment(_ context.Context, item provider.Item, target provider.ReviewTarget, body string) error {
	p.reviews = append(p.reviews, reviewCall{item: item, target: target, body: body})
	return nil
}

func (p *actionProvider) AddCommitComment(_ context.Context, item provider.Item, target provider.ReviewTarget, body string) error {
	p.commitLines = append(p.commitLines, reviewCall{item: item, target: target, body: body})
	return nil
}

func (p *actionProvider) AddReviewReply(_ context.Context, item provider.Item, target provider.ReviewThreadTarget, body string) error {
	p.replies = append(p.replies, replyCall{item: item, target: target, body: body})
	return nil
}

func (p *actionProvider) ResolveReview(_ context.Context, _ provider.Item, target provider.ReviewThreadTarget) error {
	p.resolved = append(p.resolved, target)
	return nil
}

func (p *actionProvider) CancelRun(_ context.Context, item provider.Item) error {
	p.cancelled = append(p.cancelled, item)
	return nil
}

func (p *actionProvider) Rerun(_ context.Context, item provider.Item) error {
	p.rerun = append(p.rerun, item)
	return nil
}

func key(value rune) tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{value}}
}

func TestShiftNumberKeysSwitchTabs(t *testing.T) {
	m := New(fakeProvider{}, 5*time.Second)
	for input, want := range map[rune]int{'!': 0, '@': 1, '#': 2, '$': 3, '%': 4, '^': 5} {
		m.loadingList = false
		updated, _ := m.Update(key(input))
		m = updated.(Model)
		if m.active != want {
			t.Fatalf("key %q selected tab %d, want %d", input, m.active, want)
		}
	}
}

func TestCIRunShortcutsWorkFromList(t *testing.T) {
	for _, test := range []struct {
		key    rune
		cancel bool
		action string
	}{{key: 'X', cancel: true, action: "cancel run"}, {key: 'R', action: "rerun"}} {
		backend := &actionProvider{}
		m := New(backend, 0)
		m.active = 5
		m.loadingList = false
		m.items[provider.CIRuns] = []provider.Item{{ID: "42", Title: "build"}}

		updated, cmd := m.Update(key(test.key))
		m = updated.(Model)
		if cmd == nil || !m.actionBusy {
			t.Fatalf("key %q did not start a CI run action", test.key)
		}
		result := cmd()
		if test.cancel && len(backend.cancelled) != 1 {
			t.Fatalf("key %q recorded cancellations %#v", test.key, backend.cancelled)
		}
		if !test.cancel && len(backend.rerun) != 1 {
			t.Fatalf("key %q recorded reruns %#v", test.key, backend.rerun)
		}
		message, ok := result.(actionResultMsg)
		if !ok || message.action != test.action || message.err != nil {
			t.Fatalf("key %q result = %#v, want successful %q", test.key, result, test.action)
		}
		updated, refresh := m.Update(result)
		m = updated.(Model)
		if refresh == nil || !m.loadingList {
			t.Fatalf("key %q did not refresh the run list", test.key)
		}
	}
}

func TestCIRunShortcutsWorkFromDetail(t *testing.T) {
	backend := &actionProvider{}
	m := New(backend, 0)
	m.active = 5
	m.screen = detailScreen
	m.selected = provider.Item{ID: "42", Title: "build"}
	m.detail = provider.Detail{Item: m.selected}
	m.loadingDetail = false

	updated, cmd := m.Update(key('R'))
	m = updated.(Model)
	if cmd == nil || len(backend.rerun) != 0 {
		t.Fatal("rerun did not start from CI detail")
	}
	result := cmd()
	if len(backend.rerun) != 1 || backend.rerun[0].ID != "42" {
		t.Fatalf("rerun calls = %#v", backend.rerun)
	}
	updated, refresh := m.Update(result)
	m = updated.(Model)
	if refresh == nil || !m.loadingDetail {
		t.Fatal("successful rerun did not refresh CI detail")
	}
}

func TestLeftRightChangeFilter(t *testing.T) {
	m := New(fakeProvider{}, 0)
	m.loadingList = false
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRight})
	m = updated.(Model)
	if got := m.filter().Value; got != "merged" {
		t.Fatalf("right selected %q, want merged", got)
	}
	m.loadingList = false
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyLeft})
	m = updated.(Model)
	if got := m.filter().Value; got != "open" {
		t.Fatalf("left selected %q, want open", got)
	}
}

func TestEscapeReturnsFromDetail(t *testing.T) {
	m := New(fakeProvider{}, 0)
	m.screen = detailScreen
	m.selected = provider.Item{ID: "7", Title: "detail"}
	m.loadingList = false
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = updated.(Model)
	if m.screen != listScreen {
		t.Fatalf("screen = %v, want list", m.screen)
	}
}

func TestBackspaceDoesNotReturnToPreviousScreen(t *testing.T) {
	for _, current := range []screen{detailScreen, diffScreen} {
		m := New(fakeProvider{}, 0)
		m.screen = current
		m.selected = provider.Item{ID: "7", Title: "detail"}
		m.loadingList = false

		updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyBackspace})
		m = updated.(Model)
		if m.screen != current {
			t.Fatalf("screen = %v after backspace, want %v", m.screen, current)
		}
		if cmd != nil {
			t.Fatalf("backspace returned a command on screen %v", current)
		}
	}
}

func TestOpeningItemClearsPreviousViewport(t *testing.T) {
	m := New(fakeProvider{}, 0)
	m.items[provider.PullRequests] = []provider.Item{{ID: "2", Title: "new item"}}
	m.viewport.SetContent("stale detail")
	updated, _ := m.openSelected()
	m = updated.(Model)
	if strings.Contains(m.viewport.View(), "stale detail") {
		t.Fatal("previous detail remained visible while opening a new item")
	}
}

func TestActionsAreDisabledUntilMatchingDetailLoads(t *testing.T) {
	m := New(fakeProvider{}, 0)
	m.active = 1 // Issues
	m.screen = detailScreen
	m.selected = provider.Item{ID: "8", Title: "issue"}
	m.loadingDetail = true

	updated, cmd := m.Update(key('L'))
	m = updated.(Model)
	if m.screen != detailScreen || cmd != nil {
		t.Fatal("label editor opened before issue detail finished loading")
	}
	updated, cmd = m.Update(key('a'))
	if cmd != nil {
		t.Fatal("assignment started before issue detail finished loading")
	}
}

func TestIssueStateShortcutsWorkFromList(t *testing.T) {
	for _, test := range []struct {
		key  rune
		open bool
	}{{key: 'c', open: false}, {key: 'o', open: true}} {
		backend := &actionProvider{}
		m := New(backend, 0)
		m.active = 1
		m.loadingList = false
		m.items[provider.Issues] = []provider.Item{{ID: "8", Title: "issue"}}

		updated, cmd := m.Update(key(test.key))
		m = updated.(Model)
		if cmd == nil || !m.actionBusy {
			t.Fatalf("key %q did not start a list action", test.key)
		}
		result := cmd()
		if len(backend.states) != 1 || backend.states[0] != test.open {
			t.Fatalf("key %q recorded states %#v, want open=%t", test.key, backend.states, test.open)
		}
		updated, refresh := m.Update(result)
		m = updated.(Model)
		if refresh == nil || !m.loadingList {
			t.Fatalf("key %q did not refresh the list after success", test.key)
		}
	}
}

func TestAssignmentShortcutsWorkFromList(t *testing.T) {
	for _, test := range []struct {
		key      rune
		assigned bool
	}{{key: 'a', assigned: true}, {key: 'u', assigned: false}} {
		backend := &actionProvider{}
		m := New(backend, 0)
		m.loadingList = false
		m.items[provider.PullRequests] = []provider.Item{{ID: "12", Title: "pull request"}}

		updated, cmd := m.Update(key(test.key))
		m = updated.(Model)
		if cmd == nil || !m.actionBusy {
			t.Fatalf("key %q did not start a list action", test.key)
		}
		_ = cmd()
		if len(backend.assignments) != 1 {
			t.Fatalf("key %q recorded assignments %#v", test.key, backend.assignments)
		}
		call := backend.assignments[0]
		if call.kind != provider.PullRequests || call.item.ID != "12" || call.assigned != test.assigned {
			t.Fatalf("key %q recorded unexpected assignment %#v", test.key, call)
		}
	}
}

func TestListContextCannotChangeWhileActionIsRunning(t *testing.T) {
	backend := &actionProvider{}
	m := New(backend, 0)
	m.loadingList = false
	m.items[provider.PullRequests] = []provider.Item{{ID: "12", Title: "first"}, {ID: "13", Title: "second"}}

	updated, action := m.Update(key('a'))
	m = updated.(Model)
	if action == nil || !m.actionBusy {
		t.Fatal("assignment action did not start")
	}
	for _, input := range []tea.KeyMsg{key('j'), key('2'), {Type: tea.KeyEnter}} {
		updated, cmd := m.Update(input)
		m = updated.(Model)
		if cmd != nil {
			t.Fatalf("key %q started a command while action was running", input.String())
		}
	}
	if m.active != 0 || m.cursor[provider.PullRequests] != 0 || m.screen != listScreen {
		t.Fatalf("list context changed while action was running: active=%d cursor=%d screen=%v", m.active, m.cursor[provider.PullRequests], m.screen)
	}
}

func TestAssignedItemRowHighlightsTitleAndShowsAssignees(t *testing.T) {
	m := New(fakeProvider{}, 0)
	m.active = 1
	m.width = 100
	item := provider.Item{
		ID:           "8",
		Title:        "Assigned issue",
		State:        "open",
		Meta:         "#8 · author",
		Assignees:    []provider.Assignee{{Login: "me"}, {Login: "reviewer"}},
		AssignedToMe: true,
	}
	row := m.itemRow(item, false)
	if !strings.Contains(row, myAssignmentTitle.Render(item.Title)) {
		t.Fatalf("assigned title was not highlighted: %q", row)
	}
	if !strings.Contains(row, "assigned: @me, @reviewer") {
		t.Fatalf("assignees missing from row: %q", row)
	}
}

func TestTruncateUsesThreeDots(t *testing.T) {
	if got := truncate("abcdefgh", 6); got != "abc..." {
		t.Fatalf("truncate = %q, want %q", got, "abc...")
	}
}

func TestItemRowTruncatesLongTitleToScreenWidth(t *testing.T) {
	m := New(fakeProvider{}, 0)
	m.active = 1
	m.width = 48
	row := m.itemRow(provider.Item{
		ID:    "8",
		Title: "A title that is much wider than the available terminal row",
		State: "open",
		Meta:  "#8 · author",
	}, false)
	if !strings.Contains(row, "...") {
		t.Fatalf("long title did not end with three dots: %q", row)
	}
	if got := lipgloss.Width(row); got != m.width {
		t.Fatalf("row width = %d, want %d", got, m.width)
	}
}

func TestSplitLabels(t *testing.T) {
	got := splitLabels("bug, needs review, ,backend")
	want := []string{"bug", "needs review", "backend"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("splitLabels = %#v, want %#v", got, want)
	}
}

func TestRenderDetailSeparatesSections(t *testing.T) {
	content := renderDetail(provider.Detail{
		Body:     "# Body",
		Sections: []provider.Section{{Title: "Comment · @octo", Markdown: "Hello **world**"}},
	}, 80)
	if !strings.Contains(content, "Description") || !strings.Contains(content, "Comment · @octo") {
		t.Fatalf("detail sections missing from render: %q", content)
	}
}

func TestRenderDetailIncludesDiffContentAndLineNumbers(t *testing.T) {
	content := renderDetail(provider.Detail{Diffs: []provider.DiffFile{{
		NewPath: "internal/app.go",
		Lines: []provider.DiffLine{
			{OldLine: 4, Text: "-old value"},
			{NewLine: 4, Text: "+new value"},
		},
	}}}, 80)
	for _, want := range []string{"internal/app.go", "4", "-old value", "+new value"} {
		if !strings.Contains(content, want) {
			t.Fatalf("rendered detail missing %q: %q", want, content)
		}
	}
}

func TestDiffRenderingShowsReviewUnderTargetLine(t *testing.T) {
	content := renderDiffFile([]provider.DiffFile{{
		NewPath: "internal/app.go",
		Lines: []provider.DiffLine{
			{NewLine: 4, Text: "+new value"},
			{NewLine: 5, Text: "+next value"},
		},
		Reviews: []provider.DiffReview{{
			Author:       "reviewer",
			Body:         "Please extract this before merging.",
			CreatedAt:    time.Now().Add(-2 * time.Hour),
			StartNewLine: 4,
			NewLine:      4,
			Side:         provider.ReviewSideNew,
		}},
	}}, 0, -1, -1, 80)
	lineAt := strings.Index(content, "+new value")
	reviewAt := strings.Index(content, "@reviewer")
	nextAt := strings.Index(content, "+next value")
	if lineAt < 0 || reviewAt <= lineAt || nextAt <= reviewAt || !strings.Contains(content, "Please extract this") {
		t.Fatalf("review was not rendered directly below its diff line: %q", content)
	}
}

func TestDiffReviewRendersMarkdownAndIndentsEveryBodyLine(t *testing.T) {
	rendered := ansi.Strip(renderDiffReview(provider.DiffReview{
		Author:  "reviewer",
		Body:    "**Important**  \nSecond line",
		NewLine: 4,
		Side:    provider.ReviewSideNew,
	}, 80))
	if strings.Contains(rendered, "**Important**") {
		t.Fatalf("review body was rendered as raw Markdown: %q", rendered)
	}
	lines := strings.Split(rendered, "\n")
	if len(lines) < 3 {
		t.Fatalf("Markdown hard break was not preserved: %q", rendered)
	}
	for _, line := range lines[1:] {
		if strings.TrimSpace(line) != "" && !strings.HasPrefix(line, "  ") {
			t.Fatalf("review body line lost its two-column inset: %q in %q", line, rendered)
		}
	}
}

func TestDiffMouseMappingSkipsRenderedReviewRows(t *testing.T) {
	m := readyDetailModel(fakeProvider{}, provider.PullRequests)
	m.screen = diffScreen
	m.diffFile = 0
	m.detail.Diffs[0].Reviews = []provider.DiffReview{{
		Author:  "reviewer",
		Body:    "This review occupies rows between two selectable code lines.",
		NewLine: 2,
		Side:    provider.ReviewSideNew,
	}}
	m.viewport.SetYOffset(0)
	firstRow := diffContentRowForLine(m.detail.Diffs[0], 0, m.viewport.Width)
	secondRow := diffContentRowForLine(m.detail.Diffs[0], 1, m.viewport.Width)
	if secondRow <= firstRow+1 {
		t.Fatalf("review rows were not included in diff layout: first=%d second=%d", firstRow, secondRow)
	}
	if line, ok := m.diffLineAtMouse(2 + firstRow + 1); ok {
		t.Fatalf("review row mapped to selectable diff line %d", line)
	}
	if line, ok := m.diffLineAtMouse(2 + secondRow); !ok || line != 1 {
		t.Fatalf("line after review mapped to line=%d ok=%t", line, ok)
	}
}

func TestPRDetailCanSubmitTopLevelReview(t *testing.T) {
	backend := &actionProvider{}
	m := readyDetailModel(backend, provider.PullRequests)
	updated, _ := m.Update(key('R'))
	m = updated.(Model)
	if m.screen != commentScreen || m.commentMode != generalReview || !strings.Contains(m.View(), "Review") {
		t.Fatalf("review composer did not open from detail: screen=%v mode=%v", m.screen, m.commentMode)
	}
	m.comment.SetValue("Ready after one small fix")
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlS})
	m = updated.(Model)
	if cmd == nil || m.screen != commentScreen || !m.actionBusy {
		t.Fatalf("review submit did not start: screen=%v busy=%t", m.screen, m.actionBusy)
	}
	result := cmd()
	if len(backend.topReviews) != 1 || backend.topReviews[0].body != "Ready after one small fix" || backend.topReviews[0].item.ID != "12" {
		t.Fatalf("unexpected top-level review call: %#v", backend.topReviews)
	}
	updated, _ = m.Update(result)
	m = updated.(Model)
	if m.screen != detailScreen {
		t.Fatalf("successful review did not return to detail: screen=%v", m.screen)
	}
}

func TestDiffRenderingExplainsUnavailablePatchContent(t *testing.T) {
	if got := renderDiffFile(nil, 0, -1, -1, 80); !strings.Contains(got, "No patch") {
		t.Fatalf("missing no-patch explanation: %q", got)
	}
	got := renderDiffFile([]provider.DiffFile{{NewPath: "large.go", TooLarge: true}}, 0, -1, -1, 80)
	if !strings.Contains(got, "too large or collapsed") || !strings.Contains(got, "No patch content") {
		t.Fatalf("missing collapsed-patch explanation: %q", got)
	}
}

func TestRemoteDiffSwitchesToSplitLayoutWhenWide(t *testing.T) {
	files := []provider.DiffFile{{NewPath: "main.go", Lines: []provider.DiffLine{
		{OldLine: 1, NewLine: 1, Text: " same"},
		{OldLine: 2, Text: "-old"},
		{NewLine: 2, Text: "+new"},
	}}}
	narrow := ansi.Strip(renderDiffFile(files, 0, -1, -1, 99))
	if strings.Contains(narrow, "OLD") || !strings.Contains(narrow, "-old") {
		t.Fatalf("narrow diff should stay unified: %q", narrow)
	}
	wide := ansi.Strip(renderDiffFile(files, 0, -1, -1, 100))
	if !strings.Contains(wide, "OLD") || !strings.Contains(wide, "NEW") || !strings.Contains(wide, "2 - old") || !strings.Contains(wide, "2 + new") {
		t.Fatalf("wide diff should be split: %q", wide)
	}
}

func readyDetailModel(backend provider.Provider, kind provider.Kind) Model {
	m := New(backend, 0)
	for index, candidate := range kinds {
		if candidate == kind {
			m.active = index
			break
		}
	}
	m.width, m.height = 100, 30
	m.resizeViewport()
	m.screen = detailScreen
	m.selected = provider.Item{ID: "12", Title: "change", HeadSHA: "head"}
	m.detail = provider.Detail{
		Item: m.selected,
		Diffs: []provider.DiffFile{
			{NewPath: "first.go", BaseSHA: "base", StartSHA: "start", HeadSHA: "head", Lines: []provider.DiffLine{{OldLine: 2, NewLine: 2, OldPosition: 10, NewPosition: 20, Text: " context"}, {NewLine: 3, OldPosition: 11, NewPosition: 21, Text: "+added"}}},
			{OldPath: "old.go", NewPath: "second.go", BaseSHA: "base2", StartSHA: "start2", HeadSHA: "head2", Lines: []provider.DiffLine{{OldLine: 9, OldPosition: 30, NewPosition: 40, Text: "-removed"}, {OldLine: 10, OldPosition: 31, NewPosition: 40, Text: "-also removed"}}},
			{OldPath: "mixed.go", NewPath: "mixed.go", BaseSHA: "base3", StartSHA: "start3", HeadSHA: "head3", Lines: []provider.DiffLine{{OldLine: 5, OldPosition: 50, NewPosition: 60, Text: "-old"}, {NewLine: 5, OldPosition: 51, NewPosition: 60, Text: "+new"}}},
		},
	}
	m.loadingDetail = false
	m.setDetailContent()
	return m
}

func TestDiffScreenNavigatesFilesAndLines(t *testing.T) {
	m := readyDetailModel(fakeProvider{}, provider.PullRequests)
	updated, _ := m.Update(key('d'))
	m = updated.(Model)
	if m.screen != diffScreen || m.diffFile != 0 || m.diffLine != 0 {
		t.Fatalf("diff opened with screen=%v file=%d line=%d", m.screen, m.diffFile, m.diffLine)
	}
	updated, _ = m.Update(key('j'))
	m = updated.(Model)
	if m.diffLine != 1 || !strings.Contains(m.viewport.View(), selectedRow.Render(lipgloss.NewStyle().Width(m.viewport.Width).Render(addedLineStyle.Render("        3 │ +added")))) {
		t.Fatalf("selected added line was not highlighted: line=%d view=%q", m.diffLine, m.viewport.View())
	}
	updated, _ = m.Update(key('v'))
	m = updated.(Model)
	updated, _ = m.Update(key('l'))
	m = updated.(Model)
	if m.diffFile != 1 || m.diffLine != 0 || m.diffAnchor != -1 || !strings.Contains(m.viewport.View(), "second.go") {
		t.Fatalf("file navigation failed: file=%d line=%d anchor=%d view=%q", m.diffFile, m.diffLine, m.diffAnchor, m.viewport.View())
	}
}

func TestCommitDetailReusesDiffAndCommentInterface(t *testing.T) {
	backend := &actionProvider{}
	m := readyDetailModel(backend, provider.Commits)
	m.selected.State = "commit"
	m.detail.Item = m.selected
	m.detail.Diffs[0].Lines[0].Position = 1
	m.detail.Diffs[0].Lines[1].Position = 2
	m.setDetailContent()

	if view := m.View(); !strings.Contains(view, "D diff") || !strings.Contains(view, "N comment") {
		t.Fatalf("commit detail omitted diff/comment actions: %q", view)
	}
	updated, _ := m.Update(key('d'))
	m = updated.(Model)
	if m.screen != diffScreen || !strings.Contains(m.View(), "Enter comment") || strings.Contains(m.View(), "P reply") {
		t.Fatalf("commit diff did not use its constrained review interface: screen=%v view=%q", m.screen, m.View())
	}
	updated, _ = m.Update(key('j'))
	m = updated.(Model)
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)
	if m.screen != commentScreen || m.commentMode != inlineReview || !strings.Contains(m.View(), "Commit comment") {
		t.Fatalf("commit line composer did not open: screen=%v mode=%v view=%q", m.screen, m.commentMode, m.View())
	}
	m.comment.SetValue("Explain this line")
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlS})
	m = updated.(Model)
	if cmd == nil || !m.actionBusy {
		t.Fatal("commit line comment did not start")
	}
	_ = cmd()
	want := provider.ReviewTarget{
		NewPath:          "first.go",
		StartNewLine:     3,
		NewLine:          3,
		StartOldPosition: 11,
		StartNewPosition: 21,
		OldPosition:      11,
		NewPosition:      21,
		Position:         2,
		Side:             provider.ReviewSideNew,
		BaseSHA:          "base",
		StartSHA:         "start",
		HeadSHA:          "head",
	}
	if len(backend.commitLines) != 1 || backend.commitLines[0].target != want || backend.commitLines[0].body != "Explain this line" {
		t.Fatalf("unexpected commit line comment: %#v", backend.commitLines)
	}
}

func TestCommitDetailCanSubmitGeneralComment(t *testing.T) {
	backend := &actionProvider{}
	m := readyDetailModel(backend, provider.Commits)
	m.selected.State = "commit"
	m.detail.Item = m.selected
	updated, _ := m.Update(key('n'))
	m = updated.(Model)
	if m.screen != commentScreen || m.commentMode != generalComment {
		t.Fatalf("commit comment composer did not open: screen=%v mode=%v", m.screen, m.commentMode)
	}
	m.comment.SetValue("Overall commit note")
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlS})
	m = updated.(Model)
	if cmd == nil {
		t.Fatal("general commit comment did not start")
	}
	_ = cmd()
	if len(backend.comments) != 1 || backend.comments[0].kind != provider.Commits || backend.comments[0].body != "Overall commit note" {
		t.Fatalf("unexpected general commit comment: %#v", backend.comments)
	}
}

func TestCommitDiffRejectsRangeComments(t *testing.T) {
	m := readyDetailModel(fakeProvider{}, provider.Commits)
	m.screen = diffScreen
	m.diffFile, m.diffLine = 0, 0
	m.setDiffContent()
	updated, _ := m.Update(key('v'))
	m = updated.(Model)
	if m.diffAnchor != -1 || !strings.Contains(m.status, "one diff line") {
		t.Fatalf("commit range selection was not rejected: anchor=%d status=%q", m.diffAnchor, m.status)
	}
}

func TestForwardDiffRangeMapsNewSideTarget(t *testing.T) {
	backend := &actionProvider{}
	m := readyDetailModel(backend, provider.PullRequests)
	m.screen = diffScreen
	m.diffFile, m.diffLine = 0, 0
	m.setDiffContent()
	for _, input := range []tea.KeyMsg{key('v'), key('j'), {Type: tea.KeyEnter}} {
		updated, _ := m.Update(input)
		m = updated.(Model)
	}
	if m.screen != commentScreen || !m.commentTargetSet {
		t.Fatalf("forward range did not open composer: screen=%v status=%q", m.screen, m.status)
	}
	want := provider.ReviewTarget{
		NewPath:          "first.go",
		StartOldLine:     2,
		StartNewLine:     2,
		NewLine:          3,
		StartOldPosition: 10,
		StartNewPosition: 20,
		OldPosition:      11,
		NewPosition:      21,
		Side:             provider.ReviewSideNew,
		BaseSHA:          "base",
		StartSHA:         "start",
		HeadSHA:          "head",
	}
	if m.commentTarget != want {
		t.Fatalf("forward range target = %#v, want %#v", m.commentTarget, want)
	}
	if !strings.Contains(m.View(), "first.go:2-3") {
		t.Fatalf("composer title did not show forward range: %q", m.View())
	}
}

func TestReverseDiffRangeNormalizesOldSidePayload(t *testing.T) {
	backend := &actionProvider{}
	m := readyDetailModel(backend, provider.PullRequests)
	m.screen = diffScreen
	m.diffFile, m.diffLine = 1, 1
	m.setDiffContent()
	for _, input := range []tea.KeyMsg{key('V'), key('k'), {Type: tea.KeyEnter}} {
		updated, _ := m.Update(input)
		m = updated.(Model)
	}
	m.comment.SetValue("Review both removed lines")
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlS})
	m = updated.(Model)
	if cmd == nil {
		t.Fatal("reverse range did not submit")
	}
	_ = cmd()
	want := provider.ReviewTarget{
		OldPath:          "old.go",
		NewPath:          "second.go",
		StartOldLine:     9,
		OldLine:          10,
		StartOldPosition: 30,
		StartNewPosition: 40,
		OldPosition:      31,
		NewPosition:      40,
		Side:             provider.ReviewSideOld,
		BaseSHA:          "base2",
		StartSHA:         "start2",
		HeadSHA:          "head2",
	}
	if len(backend.reviews) != 1 || backend.reviews[0].target != want {
		t.Fatalf("reverse range payload = %#v, want %#v", backend.reviews, want)
	}
}

func TestMixedSideDiffRangeIsRejected(t *testing.T) {
	m := readyDetailModel(fakeProvider{}, provider.PullRequests)
	m.screen = diffScreen
	m.diffFile, m.diffLine = 2, 0
	m.setDiffContent()
	for _, input := range []tea.KeyMsg{key('v'), key('j'), {Type: tea.KeyEnter}} {
		updated, _ := m.Update(input)
		m = updated.(Model)
	}
	if m.screen != diffScreen || !strings.Contains(m.status, "crosses old and new diff sides") {
		t.Fatalf("mixed-side range was not clearly rejected: screen=%v status=%q", m.screen, m.status)
	}
}

func TestDiffRangeAcrossHunksIsRejected(t *testing.T) {
	m := readyDetailModel(fakeProvider{}, provider.PullRequests)
	m.detail.Diffs = []provider.DiffFile{{
		NewPath: "gapped.go",
		Lines: []provider.DiffLine{
			{NewLine: 3, NewPosition: 3, Text: "+first hunk"},
			{NewLine: 20, NewPosition: 20, Text: "+second hunk"},
		},
	}}
	m.screen = diffScreen
	m.diffFile, m.diffLine = 0, 0
	m.setDiffContent()
	for _, input := range []tea.KeyMsg{key('v'), key('j'), {Type: tea.KeyEnter}} {
		updated, _ := m.Update(input)
		m = updated.(Model)
	}
	if m.screen != diffScreen || !strings.Contains(m.status, "crosses a diff hunk") {
		t.Fatalf("cross-hunk range was not rejected: screen=%v status=%q", m.screen, m.status)
	}
}

func TestDiffRangeToggleAndEscapeCancelBeforeLeaving(t *testing.T) {
	m := readyDetailModel(fakeProvider{}, provider.PullRequests)
	m.screen = diffScreen
	m.diffLine = 0
	updated, _ := m.Update(key('v'))
	m = updated.(Model)
	if m.diffAnchor != 0 {
		t.Fatalf("range anchor = %d, want 0", m.diffAnchor)
	}
	m.diffDragging = true
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = updated.(Model)
	if m.screen != diffScreen || m.diffAnchor != -1 || m.diffDragging {
		t.Fatalf("Esc did not cancel range in place: screen=%v anchor=%d dragging=%t", m.screen, m.diffAnchor, m.diffDragging)
	}
	updated, _ = m.Update(key('V'))
	m = updated.(Model)
	updated, _ = m.Update(key('v'))
	m = updated.(Model)
	if m.screen != diffScreen || m.diffAnchor != -1 {
		t.Fatalf("V did not toggle range off: screen=%v anchor=%d", m.screen, m.diffAnchor)
	}
}

func TestDetailRefreshClearsActiveDiffRange(t *testing.T) {
	m := readyDetailModel(fakeProvider{}, provider.PullRequests)
	m.detailRequest = 9
	m.screen = diffScreen
	m.diffLine, m.diffAnchor = 1, 0
	m.diffDragging = true
	refreshed := m.detail
	refreshed.Diffs[0].Lines = []provider.DiffLine{{NewLine: 70, NewPosition: 70, Text: "+new response"}}
	updated, _ := m.Update(detailResultMsg{request: 9, item: m.selected, detail: refreshed})
	m = updated.(Model)
	if m.diffAnchor != -1 || m.diffDragging {
		t.Fatalf("detail refresh retained stale range state: anchor=%d dragging=%t", m.diffAnchor, m.diffDragging)
	}
}

func TestDiffRangeRendersRangeAndStrongerCursorHighlight(t *testing.T) {
	files := readyDetailModel(fakeProvider{}, provider.PullRequests).detail.Diffs
	got := renderDiffFile(files, 0, 1, 0, 80)
	anchorRow := metaStyle.Render("   2    2 │  context")
	anchorRow = lipgloss.NewStyle().Width(80).Render(anchorRow)
	if !strings.Contains(got, rangeRowStyle.Render(anchorRow)) {
		t.Fatalf("range anchor was not highlighted: %q", got)
	}
	cursorRow := addedLineStyle.Render("        3 │ +added")
	cursorRow = lipgloss.NewStyle().Width(80).Render(cursorRow)
	if !strings.Contains(got, selectedRow.Render(cursorRow)) {
		t.Fatalf("range cursor lacked stronger selection: %q", got)
	}
}

func TestMouseDragSelectsDiffRangeAndOpensComposer(t *testing.T) {
	m := readyDetailModel(fakeProvider{}, provider.PullRequests)
	m.screen = diffScreen
	m.diffFile, m.diffLine = 0, 0
	m.setDiffContent()

	for _, event := range []tea.MouseMsg{
		{X: 12, Y: 3, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress},
		{X: 12, Y: 4, Button: tea.MouseButtonLeft, Action: tea.MouseActionMotion},
		{X: 12, Y: 4, Button: tea.MouseButtonNone, Action: tea.MouseActionRelease},
	} {
		updated, _ := m.Update(event)
		m = updated.(Model)
	}
	if m.diffAnchor != 0 || m.diffLine != 1 || m.diffDragging || m.status != "review range selected" {
		t.Fatalf("drag selection = anchor %d line %d dragging %t status %q", m.diffAnchor, m.diffLine, m.diffDragging, m.status)
	}

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)
	if m.screen != commentScreen || !m.commentTargetSet || m.commentTarget.StartNewLine != 2 || m.commentTarget.NewLine != 3 {
		t.Fatalf("dragged range did not open the expected composer: screen=%v targetSet=%t target=%#v", m.screen, m.commentTargetSet, m.commentTarget)
	}
}

func TestMouseClickSelectsSingleDiffLineWithoutRange(t *testing.T) {
	m := readyDetailModel(fakeProvider{}, provider.PullRequests)
	m.screen = diffScreen
	m.diffFile, m.diffLine = 0, 1
	m.setDiffContent()

	for _, event := range []tea.MouseMsg{
		{X: 12, Y: 3, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress},
		{X: 12, Y: 3, Button: tea.MouseButtonLeft, Action: tea.MouseActionRelease},
	} {
		updated, _ := m.Update(event)
		m = updated.(Model)
	}
	if m.diffLine != 0 || m.diffAnchor != -1 || m.diffDragging || m.status != "review line selected" {
		t.Fatalf("click selection = anchor %d line %d dragging %t status %q", m.diffAnchor, m.diffLine, m.diffDragging, m.status)
	}
}

func TestMouseMotionOutsideDiffKeepsSelectionAndReleaseEndsDrag(t *testing.T) {
	m := readyDetailModel(fakeProvider{}, provider.PullRequests)
	m.screen = diffScreen
	m.diffFile, m.diffLine = 0, 0
	m.setDiffContent()

	updated, _ := m.Update(tea.MouseMsg{X: 12, Y: 3, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
	m = updated.(Model)
	updated, _ = m.Update(tea.MouseMsg{X: 12, Y: 1, Button: tea.MouseButtonLeft, Action: tea.MouseActionMotion})
	m = updated.(Model)
	if m.diffAnchor != 0 || m.diffLine != 0 || !m.diffDragging {
		t.Fatalf("out-of-bounds motion changed drag: anchor=%d line=%d dragging=%t", m.diffAnchor, m.diffLine, m.diffDragging)
	}
	updated, _ = m.Update(tea.MouseMsg{X: 12, Y: 1, Button: tea.MouseButtonNone, Action: tea.MouseActionRelease})
	m = updated.(Model)
	if m.diffAnchor != -1 || m.diffLine != 0 || m.diffDragging {
		t.Fatalf("out-of-bounds release did not finish click: anchor=%d line=%d dragging=%t", m.diffAnchor, m.diffLine, m.diffDragging)
	}
}

func TestMouseDragNormalizesReverseDiffRange(t *testing.T) {
	m := readyDetailModel(fakeProvider{}, provider.PullRequests)
	m.screen = diffScreen
	m.diffFile, m.diffLine = 1, 0
	m.setDiffContent()

	for _, event := range []tea.MouseMsg{
		{X: 12, Y: 5, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress},
		{X: 12, Y: 4, Button: tea.MouseButtonLeft, Action: tea.MouseActionMotion},
		{X: 12, Y: 4, Button: tea.MouseButtonNone, Action: tea.MouseActionRelease},
	} {
		updated, _ := m.Update(event)
		m = updated.(Model)
	}
	target, err := m.selectedReviewTarget()
	if err != nil {
		t.Fatal(err)
	}
	if m.diffAnchor != 1 || m.diffLine != 0 || target.StartOldLine != 9 || target.OldLine != 10 || target.Side != provider.ReviewSideOld {
		t.Fatalf("reverse drag was not normalized: anchor=%d line=%d target=%#v", m.diffAnchor, m.diffLine, target)
	}
}

func TestMouseDragUsesViewportOffsetWithoutRecentering(t *testing.T) {
	m := readyDetailModel(fakeProvider{}, provider.PullRequests)
	lines := make([]provider.DiffLine, 16)
	for index := range lines {
		lines[index] = provider.DiffLine{NewLine: index + 1, NewPosition: index + 1, Text: fmt.Sprintf("+line %d", index+1)}
	}
	m.detail.Diffs = []provider.DiffFile{{NewPath: "long.go", Lines: lines}}
	m.screen = diffScreen
	m.diffFile, m.diffLine = 0, 0
	m.viewport.Height = 4
	m.setDiffContent()
	m.viewport.SetYOffset(5)

	updated, _ := m.Update(tea.MouseMsg{X: 12, Y: 2, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
	m = updated.(Model)
	if m.diffAnchor != 4 || m.diffLine != 4 || m.viewport.YOffset != 5 {
		t.Fatalf("scrolled press mapped incorrectly: anchor=%d line=%d offset=%d", m.diffAnchor, m.diffLine, m.viewport.YOffset)
	}
	updated, _ = m.Update(tea.MouseMsg{X: 12, Y: 4, Button: tea.MouseButtonLeft, Action: tea.MouseActionMotion})
	m = updated.(Model)
	if m.diffAnchor != 4 || m.diffLine != 6 || m.viewport.YOffset != 5 {
		t.Fatalf("scrolled drag mapped incorrectly: anchor=%d line=%d offset=%d", m.diffAnchor, m.diffLine, m.viewport.YOffset)
	}
}

func TestMouseDiffLineMappingAccountsForMetadataRows(t *testing.T) {
	m := readyDetailModel(fakeProvider{}, provider.PullRequests)
	m.detail.Diffs = []provider.DiffFile{{
		OldPath:  "before.go",
		NewPath:  "after.go",
		TooLarge: true,
		Lines:    []provider.DiffLine{{NewLine: 7, Text: "+visible"}},
	}}
	m.screen = diffScreen
	m.diffFile = 0
	m.setDiffContent()
	if _, ok := m.diffLineAtMouse(4); ok {
		t.Fatal("metadata row was treated as a diff line")
	}
	if line, ok := m.diffLineAtMouse(5); !ok || line != 0 {
		t.Fatalf("first diff row mapped to line=%d ok=%t, want line 0", line, ok)
	}
}

func detailHitScreenY(m *Model, hit diffHitRegion) int {
	m.viewport.SetYOffset(max(0, hit.Row-3))
	return 2 + hit.Row - m.viewport.YOffset
}

func findDetailHit(t *testing.T, m Model, file, line, review int) diffHitRegion {
	t.Helper()
	_, hits := renderDetailLayout(m.detail, m.viewport.Width, m.diffFile, m.diffLine, m.diffAnchor, m.selectedReview)
	for _, hit := range hits {
		if hit.File == file && hit.Line == line && hit.Review == review {
			return hit
		}
	}
	t.Fatalf("detail hit not found: file=%d line=%d review=%d", file, line, review)
	return diffHitRegion{}
}

func TestDetailDiffClickOpensDedicatedDiffAtClickedLine(t *testing.T) {
	m := readyDetailModel(fakeProvider{}, provider.PullRequests)
	hit := findDetailHit(t, m, 1, 0, -1)
	y := detailHitScreenY(&m, hit)
	updated, _ := m.Update(tea.MouseMsg{X: 12, Y: y, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
	m = updated.(Model)
	updated, _ = m.Update(tea.MouseMsg{X: 12, Y: y, Button: tea.MouseButtonNone, Action: tea.MouseActionRelease})
	m = updated.(Model)
	if m.screen != diffScreen || m.diffFile != 1 || m.diffLine != 0 {
		t.Fatalf("detail diff click opened screen=%v file=%d line=%d", m.screen, m.diffFile, m.diffLine)
	}
}

func TestDetailDiffDragOpensMultilineComposerWithHighlightedRange(t *testing.T) {
	m := readyDetailModel(fakeProvider{}, provider.PullRequests)
	start := findDetailHit(t, m, 0, 0, -1)
	end := findDetailHit(t, m, 0, 1, -1)
	m.viewport.SetYOffset(max(0, start.Row-3))
	startY := 2 + start.Row - m.viewport.YOffset
	endY := 2 + end.Row - m.viewport.YOffset
	for _, event := range []tea.MouseMsg{
		{X: 12, Y: startY, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress},
		{X: 12, Y: endY, Button: tea.MouseButtonLeft, Action: tea.MouseActionMotion},
		{X: 12, Y: endY, Button: tea.MouseButtonNone, Action: tea.MouseActionRelease},
	} {
		updated, _ := m.Update(event)
		m = updated.(Model)
	}
	if m.screen != commentScreen || m.commentOrigin != detailScreen || m.commentMode != inlineReview || m.diffAnchor != 0 || m.diffLine != 1 || !m.detailDiffActive {
		t.Fatalf("detail range did not open composer: screen=%v origin=%v mode=%v anchor=%d line=%d active=%t", m.screen, m.commentOrigin, m.commentMode, m.diffAnchor, m.diffLine, m.detailDiffActive)
	}
	view := m.View()
	if !strings.Contains(view, "Inline review · first.go:2-3") || !strings.Contains(view, " context") || !strings.Contains(view, "+added") {
		t.Fatalf("detail range composer lost selected context: %q", view)
	}
	contentWidth := max(12, m.viewport.Width-2) - 4
	anchorRow := metaStyle.Render("   2    2 │  context")
	anchorRow = lipgloss.NewStyle().Width(contentWidth).Render(anchorRow)
	cursorRow := addedLineStyle.Render("        3 │ +added")
	cursorRow = lipgloss.NewStyle().Width(contentWidth).Render(cursorRow)
	if !strings.Contains(view, rangeRowStyle.Render(anchorRow)) || !strings.Contains(view, selectedRow.Render(cursorRow)) {
		t.Fatalf("detail range composer did not keep both line highlights: %q", view)
	}
}

func TestDiffMultilineDragImmediatelyOpensComposer(t *testing.T) {
	m := readyDetailModel(fakeProvider{}, provider.PullRequests)
	m.screen = diffScreen
	m.diffFile, m.diffLine = 0, 0
	m.setDiffContent()
	for _, event := range []tea.MouseMsg{
		{X: 12, Y: 3, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress},
		{X: 12, Y: 4, Button: tea.MouseButtonLeft, Action: tea.MouseActionMotion},
		{X: 12, Y: 4, Button: tea.MouseButtonNone, Action: tea.MouseActionRelease},
	} {
		updated, _ := m.Update(event)
		m = updated.(Model)
	}
	if m.screen != commentScreen || m.commentOrigin != diffScreen || m.commentMode != inlineReview || m.diffAnchor != 0 || m.diffLine != 1 {
		t.Fatalf("diff drag did not immediately open composer: screen=%v origin=%v mode=%v anchor=%d line=%d", m.screen, m.commentOrigin, m.commentMode, m.diffAnchor, m.diffLine)
	}
}

func TestReviewClickRepliesAndResolveButtonActsOnThread(t *testing.T) {
	backend := &actionProvider{}
	m := readyDetailModel(backend, provider.PullRequests)
	m.detail.Diffs[0].Reviews = []provider.DiffReview{{
		ID: "10", ThreadID: "THREAD_1", ReplyToID: "10", Author: "alice", Body: "Please revise", NewLine: 2, Side: provider.ReviewSideNew, Resolvable: true, Replyable: true,
	}}
	m.screen = diffScreen
	m.diffFile, m.diffLine = 0, 0
	m.setDiffContent()
	regions := diffFileHitRegions(m.detail.Diffs[0], 0, m.viewport.Width, 0, 0)
	var reviewHit diffHitRegion
	for _, hit := range regions {
		if hit.Review == 0 && hit.ResolveStart >= 0 {
			reviewHit = hit
			break
		}
	}
	updated, _ := m.Update(tea.MouseMsg{X: reviewHit.ReplyStart, Y: 2 + reviewHit.Row, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
	m = updated.(Model)
	if m.screen != commentScreen || m.commentMode != reviewReply || m.commentThread.ThreadID != "THREAD_1" {
		t.Fatalf("review click did not open reply composer: screen=%v mode=%v target=%#v", m.screen, m.commentMode, m.commentThread)
	}
	m.comment.SetValue("Fixed in the next push")
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlS})
	m = updated.(Model)
	if cmd == nil {
		t.Fatal("review reply did not submit")
	}
	_ = cmd()
	if len(backend.replies) != 1 || backend.replies[0].target.ThreadID != "THREAD_1" || backend.replies[0].body != "Fixed in the next push" {
		t.Fatalf("unexpected review reply: %#v", backend.replies)
	}

	m = readyDetailModel(backend, provider.PullRequests)
	m.detail.Diffs[0].Reviews = []provider.DiffReview{{
		ID: "10", ThreadID: "THREAD_1", ReplyToID: "10", Author: "alice", Body: "Please revise", NewLine: 2, Side: provider.ReviewSideNew, Resolvable: true, Replyable: true,
	}}
	m.screen = diffScreen
	m.diffFile, m.diffLine = 0, 0
	m.setDiffContent()
	regions = diffFileHitRegions(m.detail.Diffs[0], 0, m.viewport.Width, 0, 0)
	for _, hit := range regions {
		if hit.Review == 0 && hit.ResolveStart >= 0 {
			reviewHit = hit
			break
		}
	}
	updated, cmd = m.Update(tea.MouseMsg{X: reviewHit.ResolveEnd, Y: 2 + reviewHit.Row, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
	m = updated.(Model)
	if cmd == nil || !m.actionBusy {
		t.Fatal("Resolve button did not start action")
	}
	_ = cmd()
	if len(backend.resolved) != 1 || backend.resolved[0].ThreadID != "THREAD_1" {
		t.Fatalf("unexpected resolve call: %#v", backend.resolved)
	}
}

func TestReviewClickOutsideExplicitActionsDoesNotOpenReply(t *testing.T) {
	m := readyDetailModel(fakeProvider{}, provider.PullRequests)
	m.detail.Diffs[0].Reviews = []provider.DiffReview{{
		ID: "10", ThreadID: "THREAD_1", ReplyToID: "10", Author: "alice", Body: "Please revise", NewLine: 2, Side: provider.ReviewSideNew, Resolved: true, Replyable: true,
	}}
	m.screen = diffScreen
	m.diffFile, m.diffLine = 0, 0
	m.setDiffContent()
	regions := diffFileHitRegions(m.detail.Diffs[0], 0, m.viewport.Width, 0, 0)
	var reviewHit diffHitRegion
	for _, hit := range regions {
		if hit.Review == 0 {
			reviewHit = hit
			break
		}
	}
	updated, cmd := m.Update(tea.MouseMsg{X: 2, Y: 2 + reviewHit.Row, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
	m = updated.(Model)
	if cmd != nil || m.screen != diffScreen || m.selectedReview != 0 {
		t.Fatalf("non-action review click triggered an action: screen=%v selected=%d cmd=%v", m.screen, m.selectedReview, cmd)
	}
}

func TestReviewWithoutReplyPermissionHidesAndRejectsReply(t *testing.T) {
	review := provider.DiffReview{ThreadID: "THREAD_1", ReplyToID: "10", Author: "alice", Body: "locked", NewLine: 2, Side: provider.ReviewSideNew}
	if rendered := renderDiffReview(review, 80); strings.Contains(rendered, "[Reply]") {
		t.Fatalf("reply button shown without permission: %q", rendered)
	}
	m := readyDetailModel(fakeProvider{}, provider.PullRequests)
	m.screen = diffScreen
	updated, cmd := m.openReplyEditor(review)
	m = updated.(Model)
	if cmd != nil || m.screen != diffScreen || !strings.Contains(m.status, "does not support replies") {
		t.Fatalf("reply opened without permission: screen=%v status=%q", m.screen, m.status)
	}
}

func TestCommentEditorRejectsBlankAndSubmitsComment(t *testing.T) {
	backend := &actionProvider{}
	m := readyDetailModel(backend, provider.Issues)
	updated, _ := m.Update(key('n'))
	m = updated.(Model)
	if m.screen != commentScreen {
		t.Fatalf("screen = %v, want comment editor", m.screen)
	}
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlS})
	m = updated.(Model)
	if cmd != nil || m.screen != commentScreen || m.err == nil || m.actionBusy {
		t.Fatalf("blank submit left editor incorrectly: screen=%v err=%v busy=%t", m.screen, m.err, m.actionBusy)
	}
	m.comment.SetValue("Looks good\nwith one note")
	updated, cmd = m.Update(tea.KeyMsg{Type: tea.KeyCtrlS})
	m = updated.(Model)
	if cmd == nil || m.screen != commentScreen || !m.actionBusy {
		t.Fatalf("comment submit did not start action: screen=%v busy=%t", m.screen, m.actionBusy)
	}
	result := cmd()
	if len(backend.comments) != 1 || backend.comments[0].kind != provider.Issues || backend.comments[0].body != "Looks good\nwith one note" {
		t.Fatalf("unexpected comment call: %#v", backend.comments)
	}
	updated, refresh := m.Update(result)
	m = updated.(Model)
	if refresh == nil || !m.loadingDetail {
		t.Fatal("successful comment did not refresh detail")
	}
}

func TestFailedReviewSubmissionPreservesDraftForRetry(t *testing.T) {
	m := readyDetailModel(fakeProvider{}, provider.PullRequests)
	updated, _ := m.Update(key('R'))
	m = updated.(Model)
	m.comment.SetValue("Do not lose this review")
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlS})
	m = updated.(Model)
	if cmd == nil || m.screen != commentScreen || !m.actionBusy {
		t.Fatal("review submission did not remain in the composer")
	}
	updated, focus := m.Update(actionResultMsg{action: "review", err: context.DeadlineExceeded})
	m = updated.(Model)
	if focus == nil || m.screen != commentScreen || m.actionBusy || m.comment.Value() != "Do not lose this review" || m.err != context.DeadlineExceeded {
		t.Fatalf("failed review lost retry state: screen=%v busy=%t body=%q err=%v", m.screen, m.actionBusy, m.comment.Value(), m.err)
	}
}

func TestCommentComposerFloatsAtBottom(t *testing.T) {
	m := readyDetailModel(fakeProvider{}, provider.Issues)
	updated, _ := m.Update(key('n'))
	m = updated.(Model)
	lines := strings.Split(m.View(), "\n")
	titleLine := -1
	for index, line := range lines {
		if strings.Contains(line, "✎ Comment") {
			titleLine = index
			break
		}
	}
	if titleLine < m.height/2 {
		t.Fatalf("comment composer was not anchored near the bottom: title line=%d height=%d\n%s", titleLine, m.height, m.View())
	}
	if !strings.Contains(lines[0], "change") || !strings.Contains(m.View(), "Ctrl+S submit") {
		t.Fatalf("floating composer obscured the detail header or controls: %q", m.View())
	}
}

func TestInlineReviewMapsSelectedDiffLine(t *testing.T) {
	backend := &actionProvider{}
	m := readyDetailModel(backend, provider.PullRequests)
	m.screen = diffScreen
	m.diffFile = 1
	m.diffLine = 0
	m.setDiffContent()
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)
	if m.screen != commentScreen || m.commentMode != inlineReview {
		t.Fatalf("inline editor did not open: screen=%v mode=%v", m.screen, m.commentMode)
	}
	m.comment.SetValue("Please keep this")
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlS})
	m = updated.(Model)
	if cmd == nil {
		t.Fatal("inline review did not start an action")
	}
	_ = cmd()
	if len(backend.reviews) != 1 {
		t.Fatalf("review calls = %#v", backend.reviews)
	}
	call := backend.reviews[0]
	if call.target.OldPath != "old.go" || call.target.NewPath != "second.go" || call.target.OldLine != 9 || call.target.NewLine != 0 || call.target.BaseSHA != "base2" || call.target.StartSHA != "start2" || call.target.HeadSHA != "head2" {
		t.Fatalf("unexpected review target: %#v", call.target)
	}
}

func TestInlineReviewIgnoresWheelNavigationBehindEditor(t *testing.T) {
	backend := &actionProvider{}
	m := readyDetailModel(backend, provider.PullRequests)
	m.screen = diffScreen
	m.diffFile = 0
	m.diffLine = 0
	m.setDiffContent()

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)
	frozen := m.commentTarget
	updated, cmd := m.Update(tea.MouseMsg{Button: tea.MouseButtonWheelDown, Action: tea.MouseActionPress})
	m = updated.(Model)
	if cmd != nil || m.diffFile != 0 || m.diffLine != 0 || m.commentTarget != frozen {
		t.Fatalf("wheel moved the diff behind the editor: file=%d line=%d target=%#v", m.diffFile, m.diffLine, m.commentTarget)
	}
	m.comment.SetValue("Review the original line")
	updated, cmd = m.Update(tea.KeyMsg{Type: tea.KeyCtrlS})
	m = updated.(Model)
	if cmd == nil {
		t.Fatal("inline review did not start an action")
	}
	_ = cmd()
	if len(backend.reviews) != 1 || backend.reviews[0].target != frozen {
		t.Fatalf("submitted target changed after wheel input: %#v", backend.reviews)
	}
}

func TestCancellingRangeComposerReturnsToSameDiffRange(t *testing.T) {
	m := readyDetailModel(fakeProvider{}, provider.PullRequests)
	m.screen = diffScreen
	m.diffFile, m.diffAnchor, m.diffLine = 0, 0, 1
	m.setDiffContent()

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)
	if m.screen != commentScreen || !m.commentTargetSet {
		t.Fatalf("range composer did not open: screen=%v targetSet=%t", m.screen, m.commentTargetSet)
	}
	frozen := m.commentTarget
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = updated.(Model)
	if cmd != nil || m.screen != diffScreen || m.diffFile != 0 || m.diffAnchor != 0 || m.diffLine != 1 {
		t.Fatalf("composer cancel did not restore range: screen=%v file=%d anchor=%d line=%d", m.screen, m.diffFile, m.diffAnchor, m.diffLine)
	}
	if !m.commentTargetSet || m.commentTarget != frozen || m.err != nil {
		t.Fatalf("composer cancel corrupted frozen range state: targetSet=%t target=%#v err=%v", m.commentTargetSet, m.commentTarget, m.err)
	}
}

func TestFailedDetailRefreshPreservesRangeComposerState(t *testing.T) {
	backend := &actionProvider{}
	m := readyDetailModel(backend, provider.PullRequests)
	m.detailRequest = 8
	m.screen = diffScreen
	m.diffFile, m.diffAnchor, m.diffLine = 0, 0, 1
	m.setDiffContent()

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)
	frozen := m.commentTarget
	m.comment.SetValue("Keep this range despite refresh failure")
	updated, _ = m.Update(detailResultMsg{request: 8, item: m.selected, err: context.DeadlineExceeded})
	m = updated.(Model)
	if m.screen != commentScreen || m.diffAnchor != 0 || m.diffLine != 1 || m.commentTarget != frozen || !m.commentTargetSet {
		t.Fatalf("failed refresh changed range state: screen=%v anchor=%d line=%d targetSet=%t target=%#v", m.screen, m.diffAnchor, m.diffLine, m.commentTargetSet, m.commentTarget)
	}
	if m.comment.Value() != "Keep this range despite refresh failure" || m.err != context.DeadlineExceeded {
		t.Fatalf("failed refresh changed composer state: body=%q err=%v", m.comment.Value(), m.err)
	}
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlS})
	m = updated.(Model)
	if cmd == nil {
		t.Fatal("frozen range could not be submitted after refresh failure")
	}
	_ = cmd()
	if len(backend.reviews) != 1 || backend.reviews[0].target != frozen {
		t.Fatalf("refresh failure retargeted submitted range: %#v", backend.reviews)
	}
}

func TestInlineReviewTargetSurvivesDetailRefreshResponse(t *testing.T) {
	backend := &actionProvider{}
	m := readyDetailModel(backend, provider.PullRequests)
	m.detailRequest = 7
	m.screen = diffScreen
	m.diffFile = 0
	m.diffLine = 1
	m.diffAnchor = 0
	m.setDiffContent()

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)
	frozen := m.commentTarget
	m.comment.SetValue("Keep the original target")
	refreshed := provider.Detail{
		Item: m.selected,
		Diffs: []provider.DiffFile{{
			NewPath: "replacement.go",
			BaseSHA: "new-base",
			HeadSHA: "new-head",
			Lines:   []provider.DiffLine{{NewLine: 77, Text: "+replacement"}},
		}},
	}
	updated, _ = m.Update(detailResultMsg{request: 7, item: m.selected, detail: refreshed})
	m = updated.(Model)
	if m.screen != commentScreen || m.comment.Value() != "Keep the original target" || m.commentTarget != frozen {
		t.Fatalf("detail response disturbed inline editor: screen=%v body=%q target=%#v", m.screen, m.comment.Value(), m.commentTarget)
	}
	if !strings.Contains(m.View(), "first.go:2-3") {
		t.Fatalf("modal title no longer identifies frozen target: %q", m.View())
	}
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlS})
	m = updated.(Model)
	if cmd == nil {
		t.Fatal("inline review did not start after detail refresh")
	}
	_ = cmd()
	if len(backend.reviews) != 1 || backend.reviews[0].target != frozen {
		t.Fatalf("submitted target changed after detail response: %#v", backend.reviews)
	}
}

func TestCommentAndDiffActionsRequireMatchingDetail(t *testing.T) {
	m := New(fakeProvider{}, 0)
	m.screen = detailScreen
	m.selected = provider.Item{ID: "12", Title: "change"}
	m.loadingDetail = true
	for _, input := range []rune{'n', 'd'} {
		updated, cmd := m.Update(key(input))
		m = updated.(Model)
		if cmd != nil || m.screen != detailScreen {
			t.Fatalf("key %q bypassed detail readiness gate", input)
		}
	}
	m = readyDetailModel(fakeProvider{}, provider.PullRequests)
	m.screen = diffScreen
	m.loadingDetail = true
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)
	if cmd != nil || m.screen != diffScreen {
		t.Fatal("inline review opened while detail was loading")
	}
}
