package tui

import (
	"unicode"

	tea "github.com/charmbracelet/bubbletea"
)

// normalizeShortcutKey translates characters produced by common non-Latin
// keyboard layouts back to the Latin key at the same physical position. A
// terminal sends characters rather than physical key codes, so this is done
// only while the UI is handling shortcuts, never while a text field is active.
func normalizeShortcutKey(msg tea.KeyMsg) tea.KeyMsg {
	if msg.Type != tea.KeyRunes || len(msg.Runes) != 1 {
		return msg
	}
	latin, ok := shortcutRuneMap[msg.Runes[0]]
	if !ok {
		return msg
	}
	msg.Runes = []rune{latin}
	return msg
}

var shortcutRuneMap = map[rune]rune{
	'ㅂ': 'q', 'ㅈ': 'w', 'ㄷ': 'e', 'ㄱ': 'r', 'ㅅ': 't',
	'ㅛ': 'y', 'ㅕ': 'u', 'ㅑ': 'i', 'ㅐ': 'o', 'ㅔ': 'p',
	'ㅁ': 'a', 'ㄴ': 's', 'ㅇ': 'd', 'ㄹ': 'f', 'ㅎ': 'g',
	'ㅗ': 'h', 'ㅓ': 'j', 'ㅏ': 'k', 'ㅣ': 'l',
	'ㅋ': 'z', 'ㅌ': 'x', 'ㅊ': 'c', 'ㅍ': 'v', 'ㅠ': 'b',
	'ㅜ': 'n', 'ㅡ': 'm',
	'ㅃ': 'Q', 'ㅉ': 'W', 'ㄸ': 'E', 'ㄲ': 'R', 'ㅆ': 'T',
	'ㅒ': 'O', 'ㅖ': 'P',

	// Some terminals/input methods emit modern Hangul Jamo rather than the
	// compatibility Jamo above.
	'ᄇ': 'q', 'ᄌ': 'w', 'ᄃ': 'e', 'ᄀ': 'r', 'ᄉ': 't',
	'ᅭ': 'y', 'ᅧ': 'u', 'ᅣ': 'i', 'ᅢ': 'o', 'ᅦ': 'p',
	'ᄆ': 'a', 'ᄂ': 's', 'ᄋ': 'd', 'ᄅ': 'f', 'ᄒ': 'g',
	'ᅩ': 'h', 'ᅥ': 'j', 'ᅡ': 'k', 'ᅵ': 'l',
	'ᄏ': 'z', 'ᄐ': 'x', 'ᄎ': 'c', 'ᄑ': 'v', 'ᅲ': 'b',
	'ᅮ': 'n', 'ᅳ': 'm',
	'ᄈ': 'Q', 'ᄍ': 'W', 'ᄄ': 'E', 'ᄁ': 'R', 'ᄊ': 'T',
	'ᅤ': 'O', 'ᅨ': 'P',
}

func init() {
	// Layouts whose output alphabet is distinct from Latin can be recognized
	// safely. Latin layouts such as AZERTY and QWERTZ cannot: their output is
	// indistinguishable from intentional QWERTY input at the terminal boundary.
	addCaseMappedLayout("йцукенгшщзх", "qwertyuiop[")
	addCaseMappedLayout("фывапролдж", "asdfghjkl;")
	addCaseMappedLayout("ячсмитьбю", "zxcvbnm,.")

	addCaseMappedLayout("ςερτυθιοπ", "wertyuiop")
	addCaseMappedLayout("ασδφγηξκλ", "asdfghjkl")
	addCaseMappedLayout("ζχψωβνμ", "zxcvbnm")

	addMappedLayout("たていすかんなにらせ", "qwertyuiop")
	addMappedLayout("ちとしはきくまのり", "asdfghjkl")
	addMappedLayout("つさそひこみも", "zxcvbnm")
	addMappedLayout("タテイスカンナニラセ", "qwertyuiop")
	addMappedLayout("チトシハキクマノリ", "asdfghjkl")
	addMappedLayout("ツサソヒコミモ", "zxcvbnm")

	addMappedLayout("ضصثقفغعهخح", "qwertyuiop")
	addMappedLayout("شسيبلاتنم", "asdfghjkl")
	addMappedLayout("ئءؤرىة", "zxcvnm")

	addMappedLayout("קראטוןםפ", "ertyuiop")
	addMappedLayout("שדגכעיחלך", "asdfghjkl")
	addMappedLayout("זסבהנמצ", "zxcvbnm")
}

func addMappedLayout(characters, latinKeys string) {
	chars, keys := []rune(characters), []rune(latinKeys)
	if len(chars) != len(keys) {
		panic("invalid shortcut keyboard layout")
	}
	for i, character := range chars {
		shortcutRuneMap[character] = keys[i]
	}
}

func addCaseMappedLayout(characters, latinKeys string) {
	addMappedLayout(characters, latinKeys)
	chars, keys := []rune(characters), []rune(latinKeys)
	for i, character := range chars {
		upper := unicode.ToUpper(character)
		if upper != character {
			shortcutRuneMap[upper] = unicode.ToUpper(keys[i])
		}
	}
}
