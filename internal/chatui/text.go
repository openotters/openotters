package chatui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// skipTags are model-emitted markup tags that wrap tool calls / results
// in raw text instead of going through the proper tool.call / tool.result
// stream events. The dashboard strips them at render time; the CLI does
// the same here so an `otters chat` session reads cleanly.
//
//nolint:gochecknoglobals // immutable allow-list, used as a constant
var skipTags = []string{"function_calls", "invocation_result", "tool_use", "tool_result"}

// textBlock accumulates agent "text.delta" chunks and emits completed
// lines to scrollback line-by-line. First line gets "\n● ", subsequent
// lines align under the body with a 2-space indent.
type textBlock struct {
	theme   *theme
	buf     strings.Builder
	inBlock bool
	// skipTag is the name of an opened tool-markup tag whose
	// content we're currently discarding. Empty when not in a skip
	// block. Persists across feed() calls so partial tags spanning
	// multiple deltas still get filtered.
	skipTag string
	// pending holds an unresolved trailing fragment that might be
	// the start of a skip tag (`<f` or `<func` etc.) — held back
	// from the line buffer until we see enough bytes to decide.
	pending string
}

func newTextBlock(t *theme) *textBlock {
	return &textBlock{theme: t}
}

// feed appends chunk and emits every complete line (up to the last \n).
func (tb *textBlock) feed(chunk string) []tea.Cmd {
	if chunk == "" {
		return nil
	}

	tb.buf.WriteString(tb.filterMarkup(chunk))
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
	tb.skipTag = ""
	tb.pending = ""
}

// filterMarkup runs the chunk through a small state machine that
// drops content inside any of skipTags. Carries state across calls
// via tb.skipTag (currently-open tag) and tb.pending (a trailing
// "<…" fragment held back because we can't yet tell whether it's
// one of the skip tags or normal text).
func (tb *textBlock) filterMarkup(chunk string) string {
	s := tb.pending + chunk
	tb.pending = ""

	var out strings.Builder
	for len(s) > 0 {
		if tb.skipTag != "" {
			// We're inside a skip block. Look for the matching close
			// tag, accepting the rest if we don't see it yet.
			closer := "</" + tb.skipTag + ">"
			i := strings.Index(s, closer)
			if i < 0 {
				// Hold back enough trailing bytes to safely match a
				// close tag that might span chunks (up to len(closer)).
				safe := len(s) - len(closer)
				if safe < 0 {
					safe = 0
				}
				tb.pending = s[safe:]
				return out.String()
			}
			s = s[i+len(closer):]
			tb.skipTag = ""

			continue
		}
		// Not inside a skip block — emit until the next '<'.
		i := strings.IndexByte(s, '<')
		if i < 0 {
			out.WriteString(s)
			return out.String()
		}
		out.WriteString(s[:i])
		s = s[i:]
		// Try to match the start of any skip tag.
		matched := false
		for _, t := range skipTags {
			open := "<" + t
			if strings.HasPrefix(s, open) {
				// Skip until the '>' that closes the open tag.
				gt := strings.IndexByte(s, '>')
				if gt < 0 {
					// Partial open tag spans across this chunk;
					// hold back the rest until we see the '>'.
					tb.pending = s
					return out.String()
				}
				s = s[gt+1:]
				tb.skipTag = t
				matched = true

				break
			}
			// Could still be a prefix of this tag (e.g. "<fun"
			// before we've seen "ction_calls>"). If s is short
			// enough to be a prefix of the open tag, hold back.
			if len(s) < len(open) && strings.HasPrefix(open, s) {
				tb.pending = s
				return out.String()
			}
		}
		if matched {
			continue
		}
		// Not a skip tag — emit the '<' literally and resume scan.
		out.WriteByte('<')
		s = s[1:]
	}
	return out.String()
}

func (tb *textBlock) emit(line string) tea.Cmd {
	if tb.inBlock {
		return tea.Println("  " + line)
	}

	tb.inBlock = true

	return tea.Println("\n" + tb.theme.agentDot.Render("●") + " " + line)
}
