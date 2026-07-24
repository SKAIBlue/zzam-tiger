package main

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/SKAIBlue/zzam-tiger/internal/tui"
)

func TestFilterRapidWheelEventsDropsRepeatedWheelInput(t *testing.T) {
	filter := coalesceWheelInput()
	up := tea.MouseMsg{Button: tea.MouseButtonWheelUp, Action: tea.MouseActionPress}
	if _, ok := filter(nil, up).(wheelInputMsg); !ok {
		t.Fatal("wheel event was not converted for coalescing")
	}
	if _, ok := filter(nil, up).(wheelInputMsg); !ok {
		t.Fatal("repeated wheel event was discarded")
	}
	if got := filter(nil, tea.KeyMsg{}); got == nil {
		t.Fatal("non-wheel input was dropped")
	}
}

type wheelTestModel struct {
	updates *int
	views   *int
	delta   *int
}

func (m wheelTestModel) Init() tea.Cmd { return nil }

func (m wheelTestModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	*m.updates++
	if wheel, ok := msg.(tui.WheelScrollMsg); ok {
		*m.delta = wheel.Delta
	}
	return m, nil
}

func (m wheelTestModel) View() string {
	*m.views++
	return "view"
}

func TestWheelCoalescingModelFlushesBurstInOneRender(t *testing.T) {
	var updates, views, delta int
	m := newWheelCoalescingModel(wheelTestModel{updates: &updates, views: &views, delta: &delta})

	for range 60 {
		updated, cmd := m.Update(wheelInputMsg{button: tea.MouseButtonWheelUp})
		m = updated.(*wheelCoalescingModel)
		if cmd == nil && !m.flushPending {
			t.Fatal("first wheel input did not schedule a flush")
		}
		_ = m.View()
	}
	if updates != 0 {
		t.Fatalf("wheel burst updated wrapped model %d times before flush", updates)
	}
	if views != 1 {
		t.Fatalf("wheel burst rendered %d times before flush, want 1 cached view", views)
	}

	updated, _ := m.Update(wheelFlushMsg{})
	m = updated.(*wheelCoalescingModel)
	_ = m.View()
	if updates != 1 {
		t.Fatalf("flush updated wrapped model %d times, want 1", updates)
	}
	if delta != -60 {
		t.Fatalf("coalesced delta=%d, want -60", delta)
	}
	if views != 2 {
		t.Fatalf("flush rendered %d times, want 2", views)
	}
}
