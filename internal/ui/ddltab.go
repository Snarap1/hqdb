package ui

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/Snarap1/hqdb/internal/ddl"
)

// reconstructedLabel marks a DDL tab's definition as not the server's own
// stored text (spec: "reconstructed DDL is clearly labeled as
// reconstructed"); postgres, mssql, and odbc tables reconstruct from the
// Catalog, while mysql, clickhouse, and every view show their native
// stored definition instead.
const reconstructedLabel = "-- reconstructed from the catalog, not the server's own definition"

// copier is implemented by a tab that has a notion of "current content"
// for the global copy key ("y") to emit via OSC52.
type copier interface {
	// CopyText returns the content to copy, or false when there is
	// nothing to copy yet (e.g. still loading).
	CopyText() (string, bool)
}

// ddlTab shows one Table's DDL, syntax-highlighted.
type ddlTab struct {
	title   string
	profile string
	path    []string
	loading bool
	err     error
	// ddl is set once the definition has loaded; nil while loading or on
	// error.
	ddl *ddl.Out
	// cancelled is set when Ctrl+C stopped the load.
	cancelled bool
	work      cancelSet
}

// Cancel cancels the running DDL load (Ctrl+C).
func (t *ddlTab) Cancel() { t.work.cancelAll() }

func (t *ddlTab) Title() string { return t.title }

func (t *ddlTab) Heading() tabHeading {
	return tabHeading{tag: "DDL", profile: t.profile, object: strings.Join(t.path, ".")}
}

// Update does nothing: the copy key is routed by the Model through
// CopyText, and a DDL tab has no other keys of its own.
func (t *ddlTab) Update(tea.KeyPressMsg, keyMap, *connPool) tea.Cmd { return nil }

func (t *ddlTab) EditingText() bool { return false }

// Help hints the copy key only once there is something to copy.
func (t *ddlTab) Help(keys keyMap) []key.Binding {
	if _, ok := t.CopyText(); ok {
		return []key.Binding{keys.Copy}
	}
	return nil
}

func (t *ddlTab) CopyText() (string, bool) {
	if t.ddl == nil {
		return "", false
	}
	return t.ddl.Definition, true
}

// ddlMsg reports the result of loading a DDL tab's definition.
type ddlMsg struct {
	tab       *ddlTab
	out       *ddl.Out
	err       error
	cancelled bool
}

// openDDLCmd starts loading the DDL of the Table at path on profile,
// returning the tab immediately (in its loading state) and the tea.Cmd
// that fills it (spec: "d on a Table opens its DDL in a tab"). Mirrors
// openMongoDataViewCmd's shape. A fetch on the cached connection that
// fails is retried once after Reopen, the same recovery loadCmd gives the
// Catalog tree.
func openDDLCmd(pool *connPool, profile, adapter string, path []string) (*ddlTab, tea.Cmd) {
	t := &ddlTab{
		title:   dataViewTitle(profile, path) + " (DDL)",
		profile: profile,
		path:    append([]string{}, path...),
		loading: true,
	}
	database := poolDatabase(adapter, path)
	ctx, done := t.work.start()
	cmd := func() tea.Msg {
		defer done()
		conn, err := pool.OpenIn(profile, database)
		var out *ddl.Out
		if err == nil {
			out, err = ddl.GetDDL(ctx, conn, path)
		}
		if err != nil && ctx.Err() == nil {
			if conn2, rerr := pool.ReopenIn(profile, database); rerr == nil {
				out, err = ddl.GetDDL(ctx, conn2, path)
			}
		}
		return ddlMsg{tab: t, out: out, err: err, cancelled: ctx.Err() != nil}
	}
	return t, cmd
}

// applyDDLMsg writes an openDDLCmd result into the tab it was fetched for.
func applyDDLMsg(msg ddlMsg) {
	msg.tab.loading = false
	if msg.cancelled {
		msg.tab.cancelled = true
		return
	}
	msg.tab.err = msg.err
	msg.tab.ddl = msg.out
}

// View is a DDL tab's content: a header, then the definition, syntax-
// highlighted with line numbers, labeled as reconstructed when it is not
// the server's own text. Height is not used; the frame clips the content.
func (t *ddlTab) View(width, _ int, _ bool) string {
	sub := t.profile + " · DDL"
	if t.ddl != nil && !t.ddl.Native {
		sub += " · reconstructed"
	}
	var b strings.Builder
	b.WriteString(headerLine(width, strings.Join(t.path, "."), sub, ""))
	b.WriteString("\n\n")
	switch {
	case t.loading:
		b.WriteString(mutedStyle.Render("Loading..."))
	case t.cancelled:
		b.WriteString(accentStyle.Render("load cancelled"))
	case t.err != nil:
		b.WriteString(errorStyle.Width(max(width, 1)).Render("Error: " + t.err.Error()))
	case t.ddl != nil:
		text := t.ddl.Definition
		if !t.ddl.Native {
			text = reconstructedLabel + "\n\n" + text
		}
		b.WriteString(numberLines(strings.TrimRight(highlightSQL(text), "\n"), width))
	}
	return b.String()
}

// numberLines prefixes each line of text with its muted line number,
// clipping lines to width.
func numberLines(text string, width int) string {
	lines := strings.Split(text, "\n")
	w := len(fmt.Sprint(len(lines)))
	gutter := lipgloss.NewStyle().Foreground(colorGutter).Width(w).Align(lipgloss.Right)
	clip := lipgloss.NewStyle().Inline(true).MaxWidth(max(width-w-2, 1))
	for i, l := range lines {
		lines[i] = gutter.Render(fmt.Sprint(i+1)) + "  " + clip.Render(l)
	}
	return strings.Join(lines, "\n")
}
