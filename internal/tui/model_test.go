package tui

import (
	"context"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/jwmtp2/gtui/internal/provider"
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
func (fakeProvider) Merge(context.Context, provider.Item) error               { return nil }
func (fakeProvider) SetIssueState(context.Context, provider.Item, bool) error { return nil }
func (fakeProvider) SetAssigned(context.Context, provider.Kind, provider.Item, bool) error {
	return nil
}
func (fakeProvider) SetIssueLabels(context.Context, provider.Item, []string) error { return nil }

type assignmentCall struct {
	kind     provider.Kind
	item     provider.Item
	assigned bool
}

type actionProvider struct {
	fakeProvider
	states      []bool
	assignments []assignmentCall
}

func (p *actionProvider) SetIssueState(_ context.Context, _ provider.Item, open bool) error {
	p.states = append(p.states, open)
	return nil
}

func (p *actionProvider) SetAssigned(_ context.Context, kind provider.Kind, item provider.Item, assigned bool) error {
	p.assignments = append(p.assignments, assignmentCall{kind: kind, item: item, assigned: assigned})
	return nil
}

func key(value rune) tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{value}}
}

func TestShiftNumberKeysSwitchTabs(t *testing.T) {
	m := New(fakeProvider{}, 5*time.Second)
	for input, want := range map[rune]int{'!': 0, '@': 1, '#': 2, '$': 3, '%': 4} {
		m.loadingList = false
		updated, _ := m.Update(key(input))
		m = updated.(Model)
		if m.active != want {
			t.Fatalf("key %q selected tab %d, want %d", input, m.active, want)
		}
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
