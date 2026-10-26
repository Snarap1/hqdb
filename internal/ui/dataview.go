package ui

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/Snarap1/hqdb/internal/adapters"
	"github.com/Snarap1/hqdb/internal/config"
	"github.com/Snarap1/hqdb/internal/dataview"
	"github.com/Snarap1/hqdb/internal/query"
	"github.com/Snarap1/hqdb/internal/readonly"
)

// dataViewPageSize is the first-page size a Data view tab loads (spec:
// "Page size defaults to 30"). A SQL Data view tab can change its own page
// size at runtime, and so can a mongodb Data view (mongoview.go).
const dataViewPageSize = 30

// dataViewTitle names a Data view tab after the Profile and the full
// Catalog path of the object it belongs to (spec: "each tab title to name
// the Profile and object it belongs to").
func dataViewTitle(profile string, path []string) string {
	return profile + ": " + strings.Join(path, ".")
}

// ---- SQL Data view (ticket 07) ------------------------------------------

// dataViewTab is a SQL Table's Data view: a page of rows in the shared
// grid component, using the owning Profile's shared connection (spec:
// "Connections"). The WHERE and ORDER BY text is applied on the server as
// typed; on a read-only Profile it is screened first (ADR-0003), so it
// cannot smuggle a write in. The Data view itself only ever builds a
// SELECT.
type dataViewTab struct {
	profile string
	path    []string
	adapter string
	// access is the Profile's access mode; the zero value is read-only.
	access config.Access

	pageSize int
	offset   int

	grid    grid
	loading bool
	err     error

	editingPageSize bool
	pageSizeInput   string

	// where and orderBy are the applied WHERE and ORDER BY text; while
	// editing names one of them, fieldInput holds the text being typed.
	where      string
	orderBy    string
	editing    dvField
	fieldInput string

	// total is the last "#" count for the current where, valid only
	// while hasTotal; counting is set while a count query runs.
	total    string
	hasTotal bool
	counting bool

	// exportStatus is the last export's progress, row count, or error;
	// exportCancel stops a running export, and exportSeq tells a stale
	// export's result from the current one's (see dataview_export.go).
	exportStatus string
	exportCancel context.CancelFunc
	exportSeq    int

	// work tracks running page and count queries for Ctrl+C; notice is
	// the last cancellation, shown in the header until the next load.
	work   cancelSet
	notice string
}

// dvField names which of a SQL Data view's text fields has focus.
type dvField int

const (
	fieldNone dvField = iota
	fieldWhere
	fieldOrderBy
	fieldExport
)

// newDataViewTab builds a SQL Data view tab at the default page size; the
// caller still needs to kick off loadCmd to fill its first page.
func newDataViewTab(profile, adapter string, path []string) *dataViewTab {
	return &dataViewTab{profile: profile, adapter: adapter, path: append([]string{}, path...), pageSize: dataViewPageSize}
}

// setAccess binds the Data view to its Profile's access mode.
func (d *dataViewTab) setAccess(a config.Access) { d.access = a }

// screen refuses the typed WHERE and ORDER BY on a read-only Profile when
// the MCP server would refuse them in a query (readonly.Violation). Only
// the typed text is screened, wrapped in a stand-in SELECT, so a Table
// whose own name is a write keyword stays browsable.
func (d *dataViewTab) screen() error {
	if d.access == config.ReadWrite || (d.where == "" && d.orderBy == "") {
		return nil
	}
	stmt := "SELECT 1"
	if d.where != "" {
		stmt += " WHERE " + d.where
	}
	if d.orderBy != "" {
		stmt += " ORDER BY " + d.orderBy
	}
	if v := readonly.Violation(stmt); v != "" {
		return fmt.Errorf("Refused (read-only profile): %s", v)
	}
	return nil
}

// pageViewMsg reports the result of loading one dataViewTab page.
type pageViewMsg struct {
	tab       *dataViewTab
	result    *query.Result
	err       error
	cancelled bool
}

// loadCmd marks the tab loading and returns the tea.Cmd that runs its
// current page query (Table, WHERE, ORDER BY, page size, offset) against the Profile's shared connection. A query
// that fails on the cached connection is retried once after Reopen, so a
// connection the server dropped recovers without the caller doing anything,
// mirroring loadCmd in load.go.
func (d *dataViewTab) loadCmd(pool *connPool) tea.Cmd {
	d.loading = true
	sqlText, err := dataview.BuildPageQuery(d.adapter, dataview.PageQuery{
		Path: d.path, Where: d.where, OrderBy: d.orderBy, PageSize: d.pageSize, Offset: d.offset,
	})
	if err == nil {
		err = d.screen()
	}
	profile, database := d.profile, d.database()
	d.notice = ""
	ctx, done := d.work.start()
	return func() tea.Msg {
		defer done()
		if err != nil {
			return pageViewMsg{tab: d, err: err}
		}
		res, err := runOnProfile(ctx, pool, profile, database, sqlText)
		return pageViewMsg{tab: d, result: res, err: err, cancelled: ctx.Err() != nil}
	}
}

// runOnProfile runs sqlText on profile's shared SQL connection. A query
// that fails on the cached connection is retried once after Reopen, so a
// connection the server dropped recovers without the caller doing
// anything, mirroring loadCmd in load.go. (A bad WHERE is retried too;
// it fails the same way twice and its error is what the user sees.)
func runOnProfile(ctx context.Context, pool *connPool, profile, database, sqlText string) (*query.Result, error) {
	run := func(conn adapters.DB) (*query.Result, error) {
		sqlConn, ok := conn.(*adapters.SQL)
		if !ok {
			return nil, fmt.Errorf("Data view is not available for %s", conn.Kind())
		}
		return query.Run(ctx, sqlConn.SQL(), sqlText, 0)
	}
	conn, err := pool.Open(poolKey{profile, database})
	var res *query.Result
	if err == nil {
		res, err = run(conn)
	}
	if err != nil && ctx.Err() == nil {
		conn2, rerr := pool.Reopen(poolKey{profile, database})
		if rerr != nil {
			return nil, rerr
		}
		res, err = run(conn2)
	}
	return res, err
}

// countViewMsg reports the result of a "#" count query, run for where.
type countViewMsg struct {
	tab       *dataViewTab
	where     string
	total     string
	err       error
	cancelled bool
}

// countCmd marks the tab counting and returns the tea.Cmd that counts the
// rows matching its current WHERE.
func (d *dataViewTab) countCmd(pool *connPool) tea.Cmd {
	d.counting = true
	sqlText, err := dataview.BuildCountQuery(d.adapter, d.path, d.where)
	if err == nil {
		err = d.screen()
	}
	profile, database, where := d.profile, d.database(), d.where
	d.notice = ""
	ctx, done := d.work.start()
	return func() tea.Msg {
		defer done()
		if err != nil {
			return countViewMsg{tab: d, where: where, err: err}
		}
		res, err := runOnProfile(ctx, pool, profile, database, sqlText)
		if err != nil {
			return countViewMsg{tab: d, where: where, err: err, cancelled: ctx.Err() != nil}
		}
		if len(res.Rows) != 1 || len(res.Rows[0]) != 1 {
			return countViewMsg{tab: d, where: where, err: fmt.Errorf("count query returned an unexpected shape")}
		}
		return countViewMsg{tab: d, where: where, total: fmt.Sprint(res.Rows[0][0])}
	}
}

// applyCountViewMsg writes a count result into its tab, unless the WHERE
// changed since the count started (the total would be stale).
func applyCountViewMsg(msg countViewMsg) {
	d := msg.tab
	if msg.where != d.where {
		return
	}
	d.counting = false
	if msg.cancelled {
		d.notice = "count cancelled"
		return
	}
	if msg.err != nil {
		d.err = msg.err
		return
	}
	d.total, d.hasTotal = msg.total, true
}

// applyPageViewMsg writes a loadCmd result into the tab it was fetched for.
func applyPageViewMsg(msg pageViewMsg) {
	d := msg.tab
	d.loading = false
	if msg.cancelled {
		// The previous page (if any) stays on screen and usable.
		d.notice = "page load cancelled"
		return
	}
	d.err = msg.err
	if msg.err == nil && msg.result != nil {
		query := d.grid.state.query // a search stays applied across pages
		d.grid = newGrid(msg.result.Columns, msg.result.Rows)
		d.grid.state.query = query
	}
}

// Cancel cancels the tab's running page, count, and export (Ctrl+C).
func (d *dataViewTab) Cancel() {
	d.work.cancelAll()
	d.cancelExport()
}

func (d *dataViewTab) Title() string { return dataViewTitle(d.profile, d.path) }

func (d *dataViewTab) Heading() tabHeading {
	return tabHeading{tag: "DATA", profile: d.profile, object: strings.Join(d.path, ".")}
}

func (d *dataViewTab) EditingText() bool {
	return d.editingPageSize || d.editing != fieldNone || d.grid.modal()
}

// CopyText is the current cell, for the global copy key.
func (d *dataViewTab) CopyText() (string, bool) { return d.grid.cellCopyText() }

// CopyRowText is the current row as a JSON object, for the row copy key.
func (d *dataViewTab) CopyRowText() (string, bool) { return d.grid.rowCopyText() }

// dataViewGridTop is how many lines of a Data view tab's content sit above
// its grid (the range / page size header and the WHERE / ORDER BY fields line).
const dataViewGridTop = 2

// ClickCell opens the popup of the cell under (x, y), relative to the
// tab's content area, on a double-click; a single click just moves the
// cell cursor there.
func (d *dataViewTab) ClickCell(x, y, width, height int, double bool) {
	if d.grid.modal() {
		return
	}
	r, c, ok := d.grid.cellAt(x, y-dataViewGridTop, width, height-dataViewGridTop-1)
	if !ok {
		return
	}
	if double {
		d.grid = d.grid.openPopupAt(r, c)
		return
	}
	d.grid.cursorRow, d.grid.cursorCol = r, c
}

func (d *dataViewTab) Help(keys keyMap) []key.Binding {
	return keys.dataViewHelp()
}

// indicator renders the spec's "1-30" style range, with "of N" only once
// "#" has counted the current WHERE (spec: user stories 38-39, "opening a
// Table never runs an expensive count").
func (d *dataViewTab) indicator() string {
	n := len(d.grid.rows)
	var s string
	if n == 0 {
		s = "no rows"
	} else {
		s = fmt.Sprintf("%d-%d", d.offset+1, d.offset+n)
	}
	switch {
	case d.counting:
		s += " (counting...)"
	case d.hasTotal:
		s += " of " + d.total
	}
	return s
}

// fieldsLine renders the WHERE and ORDER BY fields above the grid, with a
// "_" cursor and the accent color on the one being edited, or the export
// path prompt while it is open.
func (d *dataViewTab) fieldsLine(width int) string {
	if d.editing == fieldExport {
		return fieldText(width, "e", "export to (.csv/.json/.ndjson)", d.fieldInput, true)
	}
	where, orderBy := d.where, d.orderBy
	switch d.editing {
	case fieldWhere:
		where = d.fieldInput
	case fieldOrderBy:
		orderBy = d.fieldInput
	}
	half := max(width/2-1, 1)
	return fieldText(half, "f", "WHERE", where, d.editing == fieldWhere) + "  " +
		fieldText(width-half-2, "o", "ORDER BY", orderBy, d.editing == fieldOrderBy)
}

// fieldText renders one labeled text field of a Data view, width cells
// wide: its key, its label, and its value, with a "_" cursor and the
// accent color while editing.
func fieldText(width int, k, label, value string, editing bool) string {
	head := accentStyle.Render(k) + " " + mutedStyle.Render(label) + " "
	valStyle := lipgloss.NewStyle().Foreground(colorText)
	if editing {
		value += "_"
		valStyle = valStyle.Foreground(colorBright).Underline(true)
	} else if value == "" {
		value, valStyle = "-", mutedStyle
	}
	return lipgloss.NewStyle().Inline(true).Width(max(width, 1)).MaxWidth(max(width, 1)).Render(head + valStyle.Render(value))
}

// footerLine is a Data view's bottom line: the last cancellation, the
// search state, and the export state on the left (an export error in the
// error color), and the view's own details (page size, or its field while
// editing) flush right.
func footerLine(width int, notice, search, export string, editingPageSize bool, pageSizeInput string, pageSize int, extra string) string {
	var left []string
	if notice != "" {
		left = append(left, accentStyle.Render(notice))
	}
	if search != "" {
		left = append(left, mutedStyle.Render(search))
	}
	if export != "" {
		style := mutedStyle
		if strings.HasPrefix(export, "export failed") {
			style = errorStyle
		}
		left = append(left, style.Render(export))
	}
	right := mutedStyle.Render(fmt.Sprintf("page size %d", pageSize))
	if editingPageSize {
		right = accentStyle.Render("page size: " + pageSizeInput + "_")
	}
	if extra != "" {
		right = mutedStyle.Render(extra+" · ") + right
	}
	return spread(width, strings.Join(left, mutedStyle.Render(" · ")), right)
}

// withFooter renders body in exactly height-1 lines and footer below it,
// so the footer always sits on the content's last line.
func withFooter(body, footer string, height int) string {
	h := max(height-1, 0)
	return lipgloss.NewStyle().Height(h).MaxHeight(h).Render(body) + "\n" + footer
}

func (d *dataViewTab) View(width, height int, focused bool) string {
	// While loading, the grid still holds the previous page (or none), so
	// its row count cannot tell this page's range; showing a plain
	// "loading..." instead of indicator()'s stale range avoids reporting a
	// range that belongs to a page no longer being fetched.
	badge := "loading..."
	if !d.loading {
		badge = d.indicator()
	}
	header := headerLine(width, strings.Join(d.path, "."), d.profile+" · "+d.adapter, badgeStyle.Render(badge))
	footer := footerLine(width, d.notice, d.grid.searchStatus(), d.exportStatus, d.editingPageSize, d.pageSizeInput, d.pageSize, "")
	// A server error (e.g. a bad WHERE) replaces only the grid, so the
	// fields above it stay visible and editable.
	if d.err != nil {
		errText := "\n" + errorStyle.Width(max(width, 1)).Render(fmt.Sprintf("%s failed to load:\n\n%v", d.Title(), d.err))
		return header + "\n" + d.fieldsLine(width) + "\n" + withFooter(errText, footer, height-dataViewGridTop)
	}
	body := d.grid.view(width, height-dataViewGridTop-1, focused)
	return header + "\n" + d.fieldsLine(width) + "\n" + withFooter(body, footer, height-dataViewGridTop)
}

// Paste goes to the open page-size field (digits only) or the WHERE,
// ORDER BY, or export field (flattened to one line).
func (d *dataViewTab) Paste(text string) {
	switch {
	case d.editingPageSize:
		d.pageSizeInput += digitsOnly(text)
	case d.editing != fieldNone:
		d.fieldInput += singleLine(text)
	}
}

func (d *dataViewTab) Update(k tea.KeyPressMsg, keys keyMap, pool *connPool) tea.Cmd {
	if d.editingPageSize {
		return d.updatePageSizeInput(k, pool)
	}
	if d.editing != fieldNone {
		return d.updateFieldInput(k, pool)
	}
	var handled bool
	if d.grid, handled = d.grid.update(k, keys); handled {
		return nil
	}
	switch {
	case key.Matches(k, keys.Where):
		d.editing, d.fieldInput = fieldWhere, d.where
	case key.Matches(k, keys.OrderBy):
		d.editing, d.fieldInput = fieldOrderBy, d.orderBy
	case key.Matches(k, keys.Count):
		return d.countCmd(pool)
	case key.Matches(k, keys.Export):
		d.editing, d.fieldInput = fieldExport, ""
		return nil
	case key.Matches(k, keys.NextPage):
		d.offset += d.pageSize
		return d.loadCmd(pool)
	case key.Matches(k, keys.PrevPage):
		if d.offset == 0 {
			return nil
		}
		d.offset -= d.pageSize
		if d.offset < 0 {
			d.offset = 0
		}
		return d.loadCmd(pool)
	case key.Matches(k, keys.PageSize):
		// Starts empty rather than pre-filled with the current size, so
		// typing the new size is the only thing the user has to do;
		// Esc cancels back to the unchanged size with no edit needed.
		d.editingPageSize = true
		d.pageSizeInput = ""
	}
	return nil
}

// updatePageSizeInput handles keys while the page-size text field has
// focus: digits append, backspace erases, Enter applies (resetting paging
// to the first page) and reloads, Esc cancels without reloading.
func (d *dataViewTab) updatePageSizeInput(k tea.KeyPressMsg, pool *connPool) tea.Cmd {
	switch k.String() {
	case "esc":
		d.editingPageSize = false
		return nil
	case "enter":
		d.editingPageSize = false
		if n, err := strconv.Atoi(d.pageSizeInput); err == nil && n > 0 {
			d.pageSize = n
			d.offset = 0
			return d.loadCmd(pool)
		}
		return nil
	case "backspace":
		if len(d.pageSizeInput) > 0 {
			d.pageSizeInput = d.pageSizeInput[:len(d.pageSizeInput)-1]
		}
		return nil
	}
	if len(k.Text) == 1 && k.Text[0] >= '0' && k.Text[0] <= '9' {
		d.pageSizeInput += k.Text
	}
	return nil
}

// updateFieldInput handles keys while the WHERE or ORDER BY field has
// focus. The field starts pre-filled with its applied text, so a failed
// WHERE can be fixed rather than retyped. Enter applies the text,
// resetting paging to the first page (and, for WHERE, dropping the now
// stale total) and reloads; Esc cancels without reloading.
func (d *dataViewTab) updateFieldInput(k tea.KeyPressMsg, pool *connPool) tea.Cmd {
	switch k.String() {
	case "esc":
		d.editing = fieldNone
		return nil
	case "enter":
		text := strings.TrimSpace(d.fieldInput)
		if d.editing == fieldExport {
			d.editing = fieldNone
			if text == "" {
				return nil
			}
			return d.exportCmd(pool, text)
		}
		if d.editing == fieldWhere {
			if text != d.where {
				d.hasTotal, d.counting = false, false
			}
			d.where = text
		} else {
			d.orderBy = text
		}
		d.editing = fieldNone
		d.offset = 0
		return d.loadCmd(pool)
	case "backspace":
		if r := []rune(d.fieldInput); len(r) > 0 {
			d.fieldInput = string(r[:len(r)-1])
		}
		return nil
	}
	if k.Text != "" {
		d.fieldInput += k.Text
	}
	return nil
}

func (d *dataViewTab) cellPopupOpen() bool { return d.grid.popupOpen() }

// database is the Database this Data view's connection is bound to (see
// poolDatabase).
func (d *dataViewTab) database() string { return poolDatabase(d.adapter, d.path) }
