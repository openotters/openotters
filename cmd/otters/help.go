package main

import (
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/alecthomas/kong"
)

// dockerStyleGroups is the ordered list of command groups surfaced in the
// help output. Fields on the CMD struct use `group:"<key>"` to opt in;
// anything without a group falls under a trailing "Commands" section.
//
//nolint:gochecknoglobals // configuration table consumed by kong help
var dockerStyleGroups = []kong.Group{
	{Key: "lifecycle", Title: "Common Commands"},
	{Key: "management", Title: "Management Commands"},
}

// dockerStyleHelp renders help in the layout Docker's CLI uses:
// usage line, short description, grouped command lists, then a single
// "Global Options:" block. Compact column alignment and a trailing
// "Run '<cmd> --help' for more …" hint keep the surface familiar.
func dockerStyleHelp(_ kong.HelpOptions, ctx *kong.Context) error {
	w := ctx.Stdout
	node := ctx.Selected()

	if node == nil {
		node = ctx.Model.Node
	}

	writeUsage(w, node)
	writeSummary(w, node)
	writeCommandSections(w, node)
	writeFlags(w, node)
	writeFooter(w, node)

	return nil
}

func writeUsage(w io.Writer, node *kong.Node) {
	path := nodePath(node)

	fmt.Fprintf(w, "\nUsage:  %s", path)
	if hasVisibleFlags(node) {
		fmt.Fprint(w, " [OPTIONS]")
	}

	if len(visibleChildren(node)) > 0 {
		fmt.Fprint(w, " COMMAND")
	}

	for _, a := range node.Positional {
		fmt.Fprintf(w, " %s", positionalToken(a))
	}

	fmt.Fprintln(w)
}

// positionalToken renders a positional argument the way usage lines
// typically show one: uppercase name in angle brackets, trailing "..."
// for slice args, square brackets around the whole thing when optional.
func positionalToken(a *kong.Positional) string {
	name := strings.ToUpper(a.Name)
	token := "<" + name + ">"

	if a.IsSlice() {
		token += "..."
	}

	if !a.Required {
		token = "[" + token + "]"
	}

	return token
}

func writeSummary(w io.Writer, node *kong.Node) {
	summary := node.Help
	if node.Detail != "" {
		summary = node.Detail
	}

	if summary == "" {
		return
	}

	fmt.Fprintln(w)
	fmt.Fprintln(w, summary)
}

func writeCommandSections(w io.Writer, node *kong.Node) {
	children := visibleChildren(node)
	if len(children) == 0 {
		return
	}

	grouped := make(map[string][]*kong.Node)
	for _, c := range children {
		key := ""
		if c.Group != nil {
			key = c.Group.Key
		}

		grouped[key] = append(grouped[key], c)
	}

	for _, group := range dockerStyleGroups {
		if cmds := grouped[group.Key]; len(cmds) > 0 {
			writeSection(w, group.Title, cmds)
		}

		delete(grouped, group.Key)
	}

	// Anything left over (including the empty-group bucket) lands in
	// a generic "Commands" section, sorted by name so the output is
	// stable regardless of struct-field order.
	var leftover []*kong.Node

	for _, cmds := range grouped {
		leftover = append(leftover, cmds...)
	}

	if len(leftover) > 0 {
		sort.Slice(leftover, func(i, j int) bool { return leftover[i].Name < leftover[j].Name })

		title := "Other Commands"
		if len(grouped) == len(children) || !anyGrouped(children) {
			// No group tags anywhere on this node — collapse to the
			// plain "Commands:" label. Happens on leaf sub-groups like
			// `otters image` where nothing opts into a group.
			title = "Commands"
		}

		writeSection(w, title, leftover)
	}
}

func anyGrouped(nodes []*kong.Node) bool {
	for _, n := range nodes {
		if n.Group != nil {
			return true
		}
	}

	return false
}

func writeSection(w io.Writer, title string, cmds []*kong.Node) {
	sort.Slice(cmds, func(i, j int) bool { return cmds[i].Name < cmds[j].Name })

	fmt.Fprintln(w)
	fmt.Fprintf(w, "%s:\n", title)

	width := 0

	for _, c := range cmds {
		if n := len(c.Name); n > width {
			width = n
		}
	}

	for _, c := range cmds {
		fmt.Fprintf(w, "  %-*s  %s\n", width, c.Name, c.Help)
	}
}

func writeFlags(w io.Writer, node *kong.Node) {
	flags := visibleFlags(node)
	if len(flags) == 0 {
		return
	}

	sort.Slice(flags, func(i, j int) bool { return flags[i].Name < flags[j].Name })

	fmt.Fprintln(w)
	fmt.Fprintln(w, "Global Options:")

	rows := make([][2]string, 0, len(flags))
	width := 0

	for _, f := range flags {
		left := flagLeft(f)
		if n := len(left); n > width {
			width = n
		}

		rows = append(rows, [2]string{left, f.Help})
	}

	for _, r := range rows {
		fmt.Fprintf(w, "  %-*s  %s\n", width, r[0], r[1])
	}
}

func writeFooter(w io.Writer, node *kong.Node) {
	if len(visibleChildren(node)) == 0 {
		return
	}

	fmt.Fprintln(w)
	fmt.Fprintf(w, "Run '%s COMMAND --help' for more information on a command.\n", nodePath(node))
}

func nodePath(node *kong.Node) string {
	var parts []string
	for n := node; n != nil; n = n.Parent {
		if n.Name == "" {
			continue
		}

		parts = append([]string{n.Name}, parts...)
	}

	return strings.Join(parts, " ")
}

func visibleChildren(node *kong.Node) []*kong.Node {
	out := make([]*kong.Node, 0, len(node.Children))

	for _, c := range node.Children {
		if c.Hidden || c.Type != kong.CommandNode {
			continue
		}

		out = append(out, c)
	}

	return out
}

func visibleFlags(node *kong.Node) []*kong.Flag {
	out := make([]*kong.Flag, 0, len(node.Flags))

	for _, f := range node.Flags {
		if f.Hidden || f.Name == "help" {
			continue
		}

		out = append(out, f)
	}

	return out
}

func hasVisibleFlags(node *kong.Node) bool {
	return len(visibleFlags(node)) > 0
}

// flagLeft builds the left-hand column of a flag row: optional short
// form, long form, and placeholder. Mirrors Docker's spacing: two
// leading spaces for short-less flags so the long form aligns under
// the "-x, " prefix used when a short form is present.
func flagLeft(f *kong.Flag) string {
	var b strings.Builder

	if f.Short != 0 {
		fmt.Fprintf(&b, "-%c, ", f.Short)
	} else {
		b.WriteString("    ")
	}

	b.WriteString("--")
	b.WriteString(f.Name)

	if !f.IsBool() && !f.IsCounter() {
		b.WriteString(" ")
		b.WriteString(f.FormatPlaceHolder())
	}

	return b.String()
}
