package chatui

// history is a bash-style prompt ring navigated with Up/Down. Entries
// are ordered oldest → newest. The cursor has three positional states:
//
//   - cursor == len(entries) — "not browsing", fresh input
//   - 0 <= cursor < len(entries) — viewing a recalled entry
//
// Draft saves the textinput value stashed at the first Up keypress so
// that stepping back down past the newest entry restores it exactly.
type history struct {
	entries  []string
	cursor   int
	draft    string
	capacity int
}

func newHistory(capacity int) *history {
	if capacity <= 0 {
		capacity = 500
	}

	return &history{capacity: capacity}
}

// preload replaces the ring with prompts (oldest → newest). Called once
// on chat start after fetching history from the daemon.
func (h *history) preload(prompts []string) {
	if len(prompts) == 0 {
		h.entries = nil
		h.cursor = 0

		return
	}

	// Respect cap by keeping only the newest `cap` entries.
	if len(prompts) > h.capacity {
		prompts = prompts[len(prompts)-h.capacity:]
	}

	h.entries = append(h.entries[:0], prompts...)
	h.cursor = len(h.entries)
}

// append adds s to the end of the ring. Duplicates of the last entry
// are skipped (bash HISTCONTROL=ignoredups). The ring is trimmed from
// the head when it exceeds cap.
func (h *history) append(s string) {
	if s == "" {
		return
	}

	if n := len(h.entries); n > 0 && h.entries[n-1] == s {
		return
	}

	h.entries = append(h.entries, s)

	if len(h.entries) > h.capacity {
		h.entries = h.entries[len(h.entries)-h.capacity:]
	}
}

// prev walks one step back. On the first call from a fresh cursor it
// stashes `current` as the draft so `next` can restore it past the
// newest entry. Returns the current entry's text; if the history is
// empty, returns current unchanged.
func (h *history) prev(current string) string {
	if len(h.entries) == 0 {
		return current
	}

	if h.cursor == len(h.entries) {
		h.draft = current
	}

	if h.cursor > 0 {
		h.cursor--
	}

	return h.entries[h.cursor]
}

// next walks one step forward. Past the newest entry it restores the
// stashed draft and moves cursor into the "not browsing" position.
func (h *history) next() string {
	if len(h.entries) == 0 {
		return ""
	}

	if h.cursor >= len(h.entries)-1 {
		h.cursor = len(h.entries)
		draft := h.draft
		h.draft = ""

		return draft
	}

	h.cursor++

	return h.entries[h.cursor]
}

// reset returns to the "not browsing" position and clears any stashed
// draft. Call after submit so the next Up starts from the newest entry.
func (h *history) reset() {
	h.cursor = len(h.entries)
	h.draft = ""
}
