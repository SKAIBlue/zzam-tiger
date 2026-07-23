package tui

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/alecthomas/chroma/v2/styles"
)

type codeHighlighter struct {
	lexer chroma.Lexer
}

func newCodeHighlighter(path string) codeHighlighter {
	lexer := lexers.Match(path)
	if lexer == nil || strings.EqualFold(lexer.Config().Name, "fallback") {
		return codeHighlighter{}
	}
	return codeHighlighter{lexer: chroma.Coalesce(lexer)}
}

func (h codeHighlighter) line(value string) string {
	if h.lexer == nil || value == "" {
		return value
	}
	iterator, err := h.lexer.Tokenise(nil, value)
	if err != nil {
		return value
	}
	var output bytes.Buffer
	base := styles.GitHubDark.Get(chroma.Text)
	for token := iterator(); token != chroma.EOF; token = iterator() {
		entry := styles.GitHubDark.Get(token.Type)
		if entry.Colour == base.Colour && entry.Bold != chroma.Yes && entry.Italic != chroma.Yes && entry.Underline != chroma.Yes {
			output.WriteString(token.Value)
			continue
		}
		if entry.Bold == chroma.Yes {
			output.WriteString("\x1b[1m")
		}
		if entry.Italic == chroma.Yes {
			output.WriteString("\x1b[3m")
		}
		if entry.Underline == chroma.Yes {
			output.WriteString("\x1b[4m")
		}
		if entry.Colour.IsSet() {
			fmt.Fprintf(&output, "\x1b[38;2;%d;%d;%dm", entry.Colour.Red(), entry.Colour.Green(), entry.Colour.Blue())
		}
		output.WriteString(token.Value)
		output.WriteString("\x1b[0m")
	}
	return output.String()
}

func (h codeHighlighter) available() bool {
	return h.lexer != nil
}

func renderDiffBackground(value, hex string) string {
	colour := chroma.ParseColour(hex)
	if !colour.IsSet() || value == "" {
		return value
	}
	background := fmt.Sprintf("\x1b[48;2;%d;%d;%dm", colour.Red(), colour.Green(), colour.Blue())
	return background + strings.ReplaceAll(value, "\x1b[0m", "\x1b[0m"+background) + "\x1b[0m"
}
