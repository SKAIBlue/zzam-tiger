package tui

import (
	"context"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

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
func (fakeProvider) Merge(context.Context, provider.Item) error                    { return nil }
func (fakeProvider) SetIssueState(context.Context, provider.Item, bool) error      { return nil }
func (fakeProvider) SetIssueLabels(context.Context, provider.Item, []string) error { return nil }

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
