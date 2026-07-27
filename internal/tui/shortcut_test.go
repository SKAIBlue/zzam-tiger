package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestNormalizeKoreanTwoSetShortcut(t *testing.T) {
	tests := map[rune]rune{
		'ㅊ': 'c',
		'ㅓ': 'j',
		'ㅃ': 'Q',
		'ᄎ': 'c',
		'с': 'c', // Russian
		'С': 'C',
		'ψ': 'c', // Greek
		'そ': 'c', // Japanese hiragana
		'ソ': 'c', // Japanese katakana
		'ؤ': 'c', // Arabic
		'ב': 'c', // Hebrew
	}
	for input, want := range tests {
		got := normalizeShortcutKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{input}})
		if len(got.Runes) != 1 || got.Runes[0] != want {
			t.Errorf("normalizeShortcutKey(%q) = %q, want %q", input, got.String(), want)
		}
	}
}

func TestNormalizeShortcutLeavesOtherInputUnchanged(t *testing.T) {
	for _, input := range []tea.KeyMsg{
		{Type: tea.KeyRunes, Runes: []rune{'c'}},
		{Type: tea.KeyRunes, Runes: []rune("한글")},
		{Type: tea.KeyEnter},
	} {
		if got := normalizeShortcutKey(input); got.String() != input.String() {
			t.Errorf("normalizeShortcutKey(%q) = %q", input.String(), got.String())
		}
	}
}

func TestKoreanShortcutDoesNotAlterFocusedSearchInput(t *testing.T) {
	m := New(fakeProvider{}, 0)
	m.graphQuery.Focus()
	m.focus = focusListSearch

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'ㅊ'}})
	got := updated.(Model).graphQuery.Value()
	if got != "ㅊ" {
		t.Fatalf("focused search value = %q, want %q", got, "ㅊ")
	}
}

func TestKoreanShortcutConfirmsModal(t *testing.T) {
	m := New(fakeProvider{}, 0)
	m, _ = m.OpenModal(ModalRequest{HasConfirm: true})

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'ㅛ'}})
	m = updated.(Model)
	result, ok := m.LastModalResult()
	if !ok || !result.Confirm {
		t.Fatalf("Korean-layout y shortcut did not confirm modal: result=%#v, ok=%t", result, ok)
	}
}
