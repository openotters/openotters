package chatui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// textBlock accumulates agent "text.delta" chunks and emits completed
// lines to scrollback line-by-line. First line gets "\n● ", subsequent
// lines align under the body with a 2-space indent.
type textBlock struct {
	theme   *theme
	buf     strings.Builder
	inBlock bool
}

func newTextBlock(t *theme) *textBlock {
	return &textBlock{theme: t}
}

// feed appends chunk and emits every complete line (up to the last \n).
func (tb *textBlock) feed(chunk string) []tea.Cmd {
	if chunk == "" {
		return nil
	}

	tb.buf.WriteString(chunk)
	s := tb.buf.String()

	var (
		cmds []tea.Cmd
		i    int
	)

	for {
		nl := strings.IndexByte(s[i:], '\n')
		if nl < 0 {
			break
		}

		cmds = append(cmds, tb.emit(s[i:i+nl]))

		i += nl + 1
	}

	tb.buf.Reset()
	if i < len(s) {
		tb.buf.WriteString(s[i:])
	}

	return cmds
}

// flush commits any buffered partial line.
func (tb *textBlock) flush() []tea.Cmd {
	if tb.buf.Len() == 0 {
		return nil
	}

	line := tb.buf.String()
	tb.buf.Reset()

	return []tea.Cmd{tb.emit(line)}
}

// endBlock signals the end of the current text block.
func (tb *textBlock) endBlock() { tb.inBlock = false }

// reset clears everything.
func (tb *textBlock) reset() {
	tb.buf.Reset()
	tb.inBlock = false
}

func (tb *textBlock) emit(line string) tea.Cmd {
	if tb.inBlock {
		return tea.Println("  " + line)
	}

	tb.inBlock = true

	return tea.Println("\n" + tb.theme.agentDot.Render("●") + " " + line)
}
