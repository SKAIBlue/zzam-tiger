package tui

import (
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
)

// ModalRequest describes a form presented above the current TUI screen.
// Items are rendered in order. Supported Type values are "select", "text",
// "divider", "input", and "button".
type ModalRequest struct {
	Title      string
	HasConfirm bool
	Items      []ModalItem
}

// ModalItem is one modal form element. Required applies to select and input.
type ModalItem struct {
	Type         string
	ID           string
	Text         string
	Items        []ModalItem
	Default      string
	CloseOnClick bool
	Required     bool
}

// ModalResult is available after a modal closes. Confirm is false for Esc or
// Cancel; Results only contains controls that have a value.
type ModalResult struct {
	Confirm bool
	Results map[string]any
}

type modalState struct {
	request  ModalRequest
	focus    int // index in focusableItems
	inputs   map[int]textinput.Model
	selected map[int]int
}

func (m Model) OpenModal(request ModalRequest) (Model, tea.Cmd) {
	state := &modalState{request: request, inputs: map[int]textinput.Model{}, selected: map[int]int{}}
	for i, item := range request.Items {
		switch item.Type {
		case "input":
			input := textinput.New()
			input.Prompt = item.Text + ": "
			input.SetValue(item.Default)
			input.CursorEnd()
			input.Width = max(10, min(50, m.width-16))
			state.inputs[i] = input
		case "select":
			for n, option := range item.Items {
				if option.ID == item.Default {
					state.selected[i] = n
					break
				}
			}
		}
	}
	m.modal, m.lastModalResult, m.modalError = state, nil, ""
	if len(m.modalFocusable()) > 0 {
		return m.focusModalControl(), nil
	}
	return m, nil
}

// LastModalResult returns and clears the most recently completed modal result.
func (m *Model) LastModalResult() (ModalResult, bool) {
	if m.lastModalResult == nil {
		return ModalResult{}, false
	}
	r := *m.lastModalResult
	m.lastModalResult = nil
	return r, true
}

func (m Model) modalFocusable() []int {
	if m.modal == nil {
		return nil
	}
	items := []int{}
	for i, item := range m.modal.request.Items {
		if item.Type == "select" || item.Type == "input" || item.Type == "button" {
			items = append(items, i)
		}
	}
	if m.modal.request.HasConfirm {
		items = append(items, -1, -2)
	} // confirm, cancel
	return items
}

func (m Model) focusModalControl() Model {
	for i, input := range m.modal.inputs {
		input.Blur()
		m.modal.inputs[i] = input
	}
	controls := m.modalFocusable()
	if len(controls) == 0 {
		return m
	}
	m.modal.focus = min(max(0, m.modal.focus), len(controls)-1)
	if item := controls[m.modal.focus]; item >= 0 && m.modal.request.Items[item].Type == "input" {
		input := m.modal.inputs[item]
		input.Focus()
		m.modal.inputs[item] = input
	}
	return m
}

func (m Model) closeModal(confirm bool, extraID string) (tea.Model, tea.Cmd) {
	results := map[string]any{}
	for i, item := range m.modal.request.Items {
		switch item.Type {
		case "input":
			if item.ID != "" {
				results[item.ID] = m.modal.inputs[i].Value()
			}
		case "select":
			if selected, ok := m.modal.selected[i]; ok && selected >= 0 && selected < len(item.Items) && item.ID != "" {
				results[item.ID] = item.Items[selected].ID
			}
		}
	}
	if extraID != "" {
		results[extraID] = true
	}
	result := ModalResult{Confirm: confirm, Results: results}
	m.modal, m.lastModalResult, m.modalError = nil, &result, ""
	return m, nil
}

func (m Model) modalValid() bool {
	for i, item := range m.modal.request.Items {
		if !item.Required {
			continue
		}
		if item.Type == "input" && strings.TrimSpace(m.modal.inputs[i].Value()) == "" {
			return false
		}
		if item.Type == "select" {
			if _, ok := m.modal.selected[i]; !ok {
				return false
			}
		}
	}
	return true
}

func (m Model) updateModal(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()
	if key == "esc" {
		return m.closeModal(false, "")
	}
	controls := m.modalFocusable()
	if len(controls) == 0 {
		return m, nil
	}
	if key == "up" || key == "shift+tab" {
		m.modal.focus = (m.modal.focus - 1 + len(controls)) % len(controls)
		return m.focusModalControl(), nil
	}
	if key == "down" || key == "tab" {
		m.modal.focus = (m.modal.focus + 1) % len(controls)
		return m.focusModalControl(), nil
	}
	itemIndex := controls[m.modal.focus]
	if itemIndex == -2 {
		if key == "enter" || key == " " || key == "n" {
			return m.closeModal(false, "")
		}
		return m, nil
	}
	if itemIndex == -1 {
		if key == "enter" || key == " " || key == "y" {
			if !m.modalValid() {
				m.modalError = "Fill in all required fields."
				return m, nil
			}
			return m.closeModal(true, "")
		}
		if key == "n" {
			return m.closeModal(false, "")
		}
		return m, nil
	}
	item := m.modal.request.Items[itemIndex]
	if item.Type == "select" {
		if len(item.Items) == 0 {
			return m, nil
		}
		if key == "left" || key == "right" || key == "enter" || key == " " {
			delta := 1
			if key == "left" {
				delta = -1
			}
			current, selected := m.modal.selected[itemIndex]
			if !selected {
				m.modal.selected[itemIndex] = 0
				return m, nil
			}
			m.modal.selected[itemIndex] = (current + delta + len(item.Items)) % len(item.Items)
			return m, nil
		}
	}
	if item.Type == "button" && (key == "enter" || key == " ") {
		if item.CloseOnClick {
			return m.closeModal(true, item.ID)
		}
		return m, nil
	}
	if item.Type == "input" {
		var cmd tea.Cmd
		m.modal.inputs[itemIndex], cmd = m.modal.inputs[itemIndex].Update(msg)
		return m, cmd
	}
	return m, nil
}

func (m Model) modalOverlay(background string) string {
	lines := []string{sectionTitleStyle.Render(m.modal.request.Title)}
	controls := m.modalFocusable()
	focused := -99
	if len(controls) > 0 {
		focused = controls[m.modal.focus]
	}
	for i, item := range m.modal.request.Items {
		switch item.Type {
		case "text":
			lines = append(lines, item.Text)
		case "divider":
			lines = append(lines, metaStyle.Render(strings.Repeat("─", max(8, min(48, m.width-16)))))
		case "input":
			value := m.modal.inputs[i].View()
			if focused == i {
				value = selectedRow.Render(value)
			}
			lines = append(lines, value)
		case "select":
			value := metaStyle.Render("(none)")
			if selected, ok := m.modal.selected[i]; ok && selected < len(item.Items) {
				value = item.Items[selected].Text
			}
			line := item.Text + ": " + value + metaStyle.Render("  ←/→ choose")
			if focused == i {
				line = selectedRow.Render(line)
			}
			lines = append(lines, line)
		case "button":
			line := "[ " + item.Text + " ]"
			if focused == i {
				line = selectedRow.Render(line)
			}
			lines = append(lines, line)
		}
	}
	if m.modal.request.HasConfirm {
		confirm, cancel := "[ Confirm ]", "[ Cancel ]"
		if focused == -1 {
			confirm = selectedRow.Render(confirm)
		}
		if focused == -2 {
			cancel = selectedRow.Render(cancel)
		}
		lines = append(lines, confirm+"  "+cancel)
	}
	if m.modalError != "" {
		lines = append(lines, errorStyle.Render(m.modalError))
	}
	lines = append(lines, metaStyle.Render("↑/↓ focus · Enter select · Esc cancel"))
	width := min(max(28, m.width-12), 76)
	return placeOverlay(m.width, m.height, detailBoxStyle.Width(width).Render(strings.Join(lines, "\n\n")), background)
}

// updateModalMouse deliberately consumes every mouse event while a modal is
// open. A click on a control focuses it; select controls also choose the next
// option and buttons activate just like Enter.
func (m Model) updateModalMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	if msg.Button != tea.MouseButtonLeft || msg.Action != tea.MouseActionPress {
		return m, nil
	}
	controls := m.modalFocusable()
	if len(controls) == 0 {
		return m, nil
	}
	// Modal content begins one row below its border. This is intentionally based
	// on the same vertical rhythm used by modalOverlay (one blank row between
	// elements), keeping mouse focus usable at any terminal size.
	contentHeight := 1
	for _, item := range m.modal.request.Items {
		contentHeight += 2 + max(1, len(strings.Split(item.Text, "\n")))
	}
	if m.modal.request.HasConfirm {
		contentHeight += 2
	}
	contentHeight += 2 // help row
	start := max(0, (m.height-(contentHeight+2))/2) + 1
	row, target := start, -99
	for i, item := range m.modal.request.Items {
		row += 2
		if msg.Y >= row && msg.Y < row+max(1, len(strings.Split(item.Text, "\n"))) && (item.Type == "select" || item.Type == "input" || item.Type == "button") {
			target = i
			break
		}
		row += max(1, len(strings.Split(item.Text, "\n")))
	}
	if target == -99 && m.modal.request.HasConfirm && msg.Y >= row+2 {
		target = -1
	}
	for i, control := range controls {
		if control == target {
			m.modal.focus = i
			break
		}
	}
	m = m.focusModalControl()
	if target >= 0 && m.modal.request.Items[target].Type == "select" {
		return m.updateModal(tea.KeyMsg{Type: tea.KeyRight})
	}
	if target >= 0 && m.modal.request.Items[target].Type == "button" {
		return m.updateModal(tea.KeyMsg{Type: tea.KeyEnter})
	}
	if target == -1 {
		return m.updateModal(tea.KeyMsg{Type: tea.KeyEnter})
	}
	return m, nil
}
