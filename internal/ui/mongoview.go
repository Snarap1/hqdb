package ui

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/Snarap1/hqdb/internal/adapters"
	"github.com/Snarap1/hqdb/internal/export"
	"github.com/Snarap1/hqdb/internal/mongocmd"
	"github.com/Snarap1/hqdb/internal/mongoshell"
	"github.com/Snarap1/hqdb/internal/values"
)

// mongoTab is a mongodb Collection's Data view, Compass-style (ticket 21):
// a Filter ("f") and a Sort ("o") field in mongo shell syntax, translated
// by mongoshell and applied on the server; a page of Documents shown as
// cards with nested fields expandable and values colored by type, or (the
// view toggle) as the shared grid with one column per field sampled off
// the page. Paging, page size, "#" count, copy, and export mirror the SQL
// Data view. Like every Data view it is read-only: it only ever runs find
// and count.
type mongoTab struct {
	profile string
	path    []string

	pageSize int

	// docCards holds the page (docs), its first Document's offset (skip,
	// also the paging position), and the card cursor and expansion.
	docCards

	loading bool
	err     error

	// gridMode shows the page in grid instead of cards; grid is rebuilt
	// from docs on every page load either way.
	gridMode bool
	grid     grid

	// filter and sort are the applied text as typed; filterJSON and
	// sortJSON are their Extended JSON translations sent to the server.
	filter, sort         string
	filterJSON, sortJSON string

	// editing names the focused text field (fieldWhere is the Filter,
	// fieldOrderBy the Sort); fieldErr is the last translation error of
	// the text being edited.
	editing    dvField
	fieldInput string
	fieldErr   string

	editingPageSize bool
	pageSizeInput   string

	total    int64
	hasTotal bool
	counting bool

	exportStatus string
	exportCancel context.CancelFunc
	exportSeq    int

	// work holds the running page loads and counts for Ctrl+C; notice
	// reports the last cancellation until the next load.
	work   cancelSet
	notice string
}

// Cancel cancels the tab's running page load, count, and export (Ctrl+C).
func (t *mongoTab) Cancel() {
	t.work.cancelAll()
	t.cancelExport()
}

// newMongoTab builds a mongodb Data view at the default page size; the
// caller still needs to run loadCmd to fill its first page.
func newMongoTab(profile string, path []string) *mongoTab {
	return &mongoTab{profile: profile, path: append([]string{}, path...), pageSize: dataViewPageSize, docCards: newDocCards()}
}

// mongoDataViewMsg reports the result of loading one mongoTab page.
type mongoDataViewMsg struct {
	tab       *mongoTab
	documents []any
	err       error
	cancelled bool
}

// request is the find (or count) request for the tab's Collection with its
// applied Filter and Sort; paging is left to the caller.
func (t *mongoTab) request(op string) (mongocmd.Request, error) {
	if len(t.path) < 2 {
		return mongocmd.Request{}, fmt.Errorf("path %q is too short for a Data view", strings.Join(t.path, "."))
	}
	return mongocmd.Request{
		Database:   t.path[0],
		Collection: t.path[len(t.path)-1],
		Operation:  op,
		Filter:     t.filterJSON,
		Sort:       t.sortJSON,
	}, nil
}

// onMongo runs fn on profile's shared mongodb connection, retrying once
// after Reopen like runOnProfile does for SQL, so a dropped connection
// recovers without the user doing anything. No retry follows a
// cancellation, or a failure after fn reported progress (retryable false).
func onMongo(ctx context.Context, pool *connPool, profile string, fn func(*adapters.Mongo) (retryable bool, err error)) error {
	run := func(conn adapters.DB) (bool, error) {
		mc, ok := conn.(*adapters.Mongo)
		if !ok {
			return false, fmt.Errorf("data view is not available for %s", conn.Kind())
		}
		return fn(mc)
	}
	conn, err := pool.Open(profile)
	retryable := true
	if err == nil {
		retryable, err = run(conn)
	}
	if err != nil && retryable && ctx.Err() == nil {
		conn2, rerr := pool.Reopen(profile)
		if rerr != nil {
			return err
		}
		_, err = run(conn2)
	}
	return err
}

// runMongo runs req on profile's shared mongodb connection (see onMongo),
// with Documents in server field order.
func runMongo(ctx context.Context, pool *connPool, profile string, req mongocmd.Request) (*mongocmd.Result, error) {
	var res *mongocmd.Result
	err := onMongo(ctx, pool, profile, func(mc *adapters.Mongo) (bool, error) {
		var err error
		res, err = mongocmd.RunOrdered(ctx, mc, req)
		return true, err
	})
	return res, err
}

// loadCmd marks the tab loading and returns the tea.Cmd that fetches its
// current page.
func (t *mongoTab) loadCmd(pool *connPool) tea.Cmd {
	t.loading = true
	t.notice = ""
	req, err := t.request("find")
	req.Limit, req.Skip = t.pageSize, t.skip
	profile := t.profile
	ctx, done := t.work.start()
	return func() tea.Msg {
		defer done()
		if err != nil {
			return mongoDataViewMsg{tab: t, err: err}
		}
		res, err := runMongo(ctx, pool, profile, req)
		if err != nil {
			return mongoDataViewMsg{tab: t, err: err, cancelled: ctx.Err() != nil}
		}
		return mongoDataViewMsg{tab: t, documents: res.Documents}
	}
}

// applyMongoDataViewMsg writes a load's result into the tab it was fetched
// for. A failed load keeps the previous page's Documents, so fixing the
// Filter does not lose them.
func applyMongoDataViewMsg(msg mongoDataViewMsg) {
	t := msg.tab
	t.loading = false
	if msg.cancelled {
		// The previous page (if any) stays on screen and usable.
		t.notice = "page load cancelled"
		return
	}
	t.err = msg.err
	if msg.err != nil {
		return
	}
	t.docs = msg.documents
	t.cursor = 0
	t.expanded = map[string]bool{}
	query := t.grid.state.query // a search stays applied across pages
	t.grid = mongoGrid(t.docs)
	t.grid.state.query = query
}

// mongoCountMsg reports a "#" count, run for filter.
type mongoCountMsg struct {
	tab       *mongoTab
	filter    string
	total     int64
	err       error
	cancelled bool
}

func (t *mongoTab) countCmd(pool *connPool) tea.Cmd {
	t.counting = true
	t.notice = ""
	req, err := t.request("count")
	profile, filter := t.profile, t.filterJSON
	ctx, done := t.work.start()
	return func() tea.Msg {
		defer done()
		if err != nil {
			return mongoCountMsg{tab: t, filter: filter, err: err}
		}
		res, err := runMongo(ctx, pool, profile, req)
		if err != nil {
			return mongoCountMsg{tab: t, filter: filter, err: err, cancelled: ctx.Err() != nil}
		}
		return mongoCountMsg{tab: t, filter: filter, total: res.Count}
	}
}

// applyMongoCountMsg writes a count into its tab unless the Filter changed
// since it started.
func applyMongoCountMsg(msg mongoCountMsg) {
	t := msg.tab
	if msg.filter != t.filterJSON {
		return
	}
	t.counting = false
	if msg.cancelled {
		t.notice = "count cancelled"
		return
	}
	if msg.err != nil {
		t.err = msg.err
		return
	}
	t.total, t.hasTotal = msg.total, true
}

// mongoExportMsg reports the outcome of a mongodb Data view export.
type mongoExportMsg struct {
	tab     *mongoTab
	seq     int
	format  string
	written int
	err     error
}

// exportCmd streams every Document matching the applied Filter, in Sort
// order, unpaged, and in server field order, to destPath in the format its
// extension names, without holding them all in memory.
func (t *mongoTab) exportCmd(pool *connPool, destPath string) tea.Cmd {
	format, err := export.InferFormat(destPath)
	var req mongocmd.Request
	if err == nil {
		req, err = t.request("find")
	}
	if err != nil {
		t.exportStatus = "export failed: " + err.Error()
		return nil
	}
	t.cancelExport()
	ctx, cancel := context.WithCancel(context.Background())
	t.exportCancel = cancel
	t.exportStatus = "exporting..."
	t.exportSeq++
	profile, seq := t.profile, t.exportSeq
	return func() tea.Msg {
		defer cancel()
		var n int
		err := onMongo(ctx, pool, profile, func(mc *adapters.Mongo) (bool, error) {
			started := false
			var err error
			n, err = export.Documents(destPath, format, func(emit func(any) error) error {
				_, err := mongocmd.Each(ctx, mc, req, func(d any) error {
					started = true
					return emit(d)
				})
				return err
			})
			return !started, err
		})
		if ctx.Err() != nil {
			err = errCancelled
		}
		return mongoExportMsg{tab: t, seq: seq, format: format, written: n, err: err}
	}
}

func (t *mongoTab) cancelExport() {
	if t.exportCancel != nil {
		t.exportCancel()
		t.exportCancel = nil
	}
}

func applyMongoExportMsg(msg mongoExportMsg) {
	t := msg.tab
	if msg.seq != t.exportSeq {
		return
	}
	t.exportCancel = nil
	if msg.err != nil {
		t.exportStatus = "export failed: " + msg.err.Error()
		return
	}
	t.exportStatus = fmt.Sprintf("export (%s): %d documents written", msg.format, msg.written)
}

// ---- value rendering ----------------------------------------------------

var (
	mongoStringStyle   = lipgloss.NewStyle().Foreground(colorGreen)
	mongoNumberStyle   = lipgloss.NewStyle().Foreground(colorPurple)
	mongoBoolStyle     = lipgloss.NewStyle().Foreground(colorRed)
	mongoObjectIDStyle = lipgloss.NewStyle().Foreground(colorAccent)
	mongoDateStyle     = lipgloss.NewStyle().Foreground(colorLight)
	mongoOtherStyle    = lipgloss.NewStyle().Foreground(colorRed)
	mongoSummaryStyle  = lipgloss.NewStyle().Foreground(colorMuted)
	mongoKeyStyle      = lipgloss.NewStyle().Foreground(colorBlue)
)

// extType reports the Extended JSON wrapper key of v ("$oid", "$date",
// ...) when v is a typed BSON scalar rather than a nested Document.
func extType(v any) (string, any, bool) {
	d, ok := v.(values.Doc)
	if !ok || len(d) != 1 || !strings.HasPrefix(d[0].Key, "$") {
		return "", nil, false
	}
	return d[0].Key, d[0].Value, true
}

// isContainer reports whether v is a nested Document or array a card can
// expand.
func isContainer(v any) bool {
	switch v.(type) {
	case []any:
		return true
	case values.Doc:
		_, _, ext := extType(v)
		return !ext
	}
	return false
}

func numberText(f float64) string { return strconv.FormatFloat(f, 'f', -1, 64) }

func compactJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprint(v)
	}
	return string(b)
}

// scalarText renders a non-container value in mongo shell notation, with
// the style its type is colored in.
func scalarText(v any) (string, lipgloss.Style) {
	switch t := v.(type) {
	case nil:
		return "null", mongoBoolStyle
	case string:
		return compactJSON(t), mongoStringStyle
	case float64:
		return numberText(t), mongoNumberStyle
	case bool:
		return strconv.FormatBool(t), mongoBoolStyle
	}
	if k, inner, ok := extType(v); ok {
		s, isStr := inner.(string)
		switch {
		case k == "$oid" && isStr:
			return "ObjectId('" + s + "')", mongoObjectIDStyle
		case k == "$date" && isStr:
			return "ISODate('" + s + "')", mongoDateStyle
		case k == "$numberLong" && isStr:
			return "NumberLong(" + s + ")", mongoNumberStyle
		case k == "$numberDecimal" && isStr:
			return "NumberDecimal('" + s + "')", mongoNumberStyle
		case k == "$numberDouble" && isStr:
			return s, mongoNumberStyle
		case k == "$regularExpression":
			if d, ok := inner.(values.Doc); ok {
				pattern, _ := d.Get("pattern")
				options, _ := d.Get("options")
				return fmt.Sprintf("/%v/%v", pattern, options), mongoOtherStyle
			}
		}
	}
	return compactJSON(v), mongoOtherStyle
}

// summaryText is a collapsed container's one-line summary.
func summaryText(v any) string {
	switch t := v.(type) {
	case []any:
		if len(t) == 1 {
			return "[1 item]"
		}
		return fmt.Sprintf("[%d items]", len(t))
	case values.Doc:
		if len(t) == 1 {
			return "{1 field}"
		}
		return fmt.Sprintf("{%d fields}", len(t))
	}
	return ""
}

// copyValue is what "y" copies for a value: a string verbatim, a nested
// value as compact JSON, anything else in its shell notation.
func copyValue(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	if isContainer(v) {
		return compactJSON(v)
	}
	s, _ := scalarText(v)
	return s
}

// mongoGrid builds the grid view of docs: one column per field sampled off
// the page ("_id" first, then in first-seen server order), typed scalars in shell notation, nested values as compact JSON
// (the cell popup pretty-prints them), a missing field as NULL.
func mongoGrid(docs []any) grid {
	columns := export.DocumentFields(docs)
	rows := make([][]any, len(docs))
	for i, d := range docs {
		doc, _ := d.(values.Doc)
		row := make([]any, len(columns))
		for c, col := range columns {
			v, ok := doc.Get(col)
			switch {
			case !ok || v == nil:
				row[c] = nil
			case isContainer(v):
				row[c] = compactJSON(v)
			default:
				if s, isStr := v.(string); isStr {
					row[c] = s
				} else {
					row[c], _ = scalarText(v)
				}
			}
		}
		rows[i] = row
	}
	return newGrid(columns, rows)
}

// ---- cards --------------------------------------------------------------

// cardLine is one rendered line of the card view: a Document's header, or
// one field (at depth 1 for top-level fields) of it.
type cardLine struct {
	doc    int
	header bool
	path   string
	depth  int
	key    string
	value  any
}

// docCards is the Document card view, shared by the mongodb Data view
// and the mongodb Console: Documents shown as cards, nested fields
// expandable, values colored by type.
type docCards struct {
	docs []any
	// skip is the first Document's offset, for "Document N" numbering.
	skip int
	// cursor is the selected card line; expanded holds the expanded
	// nested fields, keyed by cardKey.
	cursor   int
	expanded map[string]bool
}

func newDocCards() docCards { return docCards{expanded: map[string]bool{}} }

func cardKey(doc int, path string) string { return strconv.Itoa(doc) + "/" + path }

// cardLines flattens the page into card lines, descending into expanded
// containers only.
func (t *docCards) cardLines() []cardLine {
	var lines []cardLine
	var walk func(doc int, path string, depth int, v any)
	walk = func(doc int, path string, depth int, v any) {
		add := func(k string, child any) {
			p := k
			if path != "" {
				p = path + "." + k
			}
			lines = append(lines, cardLine{doc: doc, path: p, depth: depth, key: k, value: child})
			if isContainer(child) && t.expanded[cardKey(doc, p)] {
				walk(doc, p, depth+1, child)
			}
		}
		switch c := v.(type) {
		case values.Doc:
			// Server field order, as stored.
			for _, f := range c {
				add(f.Key, f.Value)
			}
		case []any:
			for i, e := range c {
				add(strconv.Itoa(i), e)
			}
		}
	}
	for i, d := range t.docs {
		lines = append(lines, cardLine{doc: i, header: true})
		walk(i, "", 1, d)
	}
	return lines
}

// cursorLineText is the selected card line as plain text ("" with none).
func (t *docCards) cursorLineText() string {
	lines := t.cardLines()
	if t.cursor >= len(lines) {
		return ""
	}
	return t.renderCardLine(lines[t.cursor], false)
}

func (t *docCards) renderCardLine(l cardLine, styled bool) string {
	style := func(s lipgloss.Style, text string) string {
		if styled {
			return s.Render(text)
		}
		return text
	}
	if l.header {
		return style(mongoSummaryStyle, fmt.Sprintf("-- Document %d --", t.skip+l.doc+1))
	}
	marker := "  "
	var val string
	if isContainer(l.value) {
		marker = "+ "
		if t.expanded[cardKey(l.doc, l.path)] {
			marker = "- "
		}
		val = style(mongoSummaryStyle, summaryText(l.value))
	} else {
		text, s := scalarText(l.value)
		val = style(s, text)
	}
	return strings.Repeat("  ", l.depth-1) + marker + style(mongoKeyStyle, l.key) + ": " + val
}

func (t *docCards) cardsView(width, height int, focused bool) string {
	lines := t.cardLines()
	top := scrollTopFor(t.cursor, len(lines), height)
	bottom := len(lines)
	if height > 0 && top+height < bottom {
		bottom = top + height
	}
	out := make([]string, 0, bottom-top)
	for i := top; i < bottom; i++ {
		text := t.renderCardLine(lines[i], true)
		prefix := "  "
		if i == t.cursor {
			prefix = mutedStyle.Render("▌") + " "
			if focused {
				prefix = accentStyle.Render("▌") + " "
			}
		}
		out = append(out, lipgloss.NewStyle().MaxWidth(width).Render(prefix+text))
	}
	return strings.Join(out, "\n")
}

func (t *docCards) toggleCursorLine() {
	lines := t.cardLines()
	if t.cursor >= len(lines) {
		return
	}
	l := lines[t.cursor]
	if !l.header && isContainer(l.value) {
		k := cardKey(l.doc, l.path)
		t.expanded[k] = !t.expanded[k]
	}
}

// update moves the card cursor or toggles the nested field under it,
// reporting whether k was one of those keys.
func (t *docCards) update(k tea.KeyPressMsg, keys keyMap) bool {
	switch {
	case key.Matches(k, keys.Up):
		if t.cursor > 0 {
			t.cursor--
		}
	case key.Matches(k, keys.Down):
		if t.cursor < len(t.cardLines())-1 {
			t.cursor++
		}
	case key.Matches(k, keys.Expand):
		t.toggleCursorLine()
	default:
		return false
	}
	return true
}

// copyText is the selected field's value, or on a header line the whole
// Document as compact JSON.
func (t *docCards) copyText() (string, bool) {
	lines := t.cardLines()
	if t.cursor >= len(lines) {
		return "", false
	}
	l := lines[t.cursor]
	if !l.header {
		return copyValue(l.value), true
	}
	return compactJSON(t.docs[l.doc]), true
}

// ---- tab interface ------------------------------------------------------

func (t *mongoTab) Title() string { return dataViewTitle(t.profile, t.path) }

func (t *mongoTab) Heading() tabHeading {
	return tabHeading{tag: "DATA", profile: t.profile, object: strings.Join(t.path, ".")}
}

func (t *mongoTab) EditingText() bool {
	return t.editingPageSize || t.editing != fieldNone || (t.gridMode && t.grid.modal())
}

func (t *mongoTab) Help(keys keyMap) []key.Binding { return keys.mongoDataViewHelp(t.gridMode) }

// currentDoc is the Document under the cursor in either view.
func (t *mongoTab) currentDoc() (any, bool) {
	i := t.grid.cursorRow
	if !t.gridMode {
		lines := t.cardLines()
		if t.cursor >= len(lines) {
			return nil, false
		}
		i = lines[t.cursor].doc
	}
	if i >= len(t.docs) {
		return nil, false
	}
	return t.docs[i], true
}

// CopyText is the current cell (grid) or field value (cards); on a card's
// header line it is the whole Document.
func (t *mongoTab) CopyText() (string, bool) {
	if t.gridMode {
		return t.grid.cellCopyText()
	}
	return t.docCards.copyText()
}

// CopyRowText is the current Document as compact JSON.
func (t *mongoTab) CopyRowText() (string, bool) {
	d, ok := t.currentDoc()
	if !ok {
		return "", false
	}
	return compactJSON(d), true
}

// ClickCell moves the cursor to the clicked card line or grid cell; a
// double-click toggles a nested field or opens the cell popup.
func (t *mongoTab) ClickCell(x, y, width, height int, double bool) {
	if t.EditingText() {
		return
	}
	bodyTop := len(t.topLines(width))
	if t.gridMode {
		r, c, ok := t.grid.cellAt(x, y-bodyTop, width, height-bodyTop-1)
		if !ok {
			return
		}
		if double {
			t.grid = t.grid.openPopupAt(r, c)
			return
		}
		t.grid.cursorRow, t.grid.cursorCol = r, c
		return
	}
	lines := t.cardLines()
	i := scrollTopFor(t.cursor, len(lines), height-bodyTop-1) + y - bodyTop
	if y < bodyTop || y >= height-1 || i >= len(lines) {
		return
	}
	t.cursor = i
	if double {
		t.toggleCursorLine()
	}
}

func (t *mongoTab) indicator() string {
	n := len(t.docs)
	s := "no documents"
	if n > 0 {
		s = fmt.Sprintf("%d-%d", t.skip+1, t.skip+n)
	}
	switch {
	case t.counting:
		s += " (counting...)"
	case t.hasTotal:
		s += fmt.Sprintf(" of %d", t.total)
	}
	return s
}

func (t *mongoTab) fieldsLine(width int) string {
	if t.editing == fieldExport {
		return fieldText(width, "e", "export to (.csv/.json/.ndjson)", t.fieldInput, true)
	}
	filter, sortText := t.filter, t.sort
	switch t.editing {
	case fieldWhere:
		filter = t.fieldInput
	case fieldOrderBy:
		sortText = t.fieldInput
	}
	half := max(width*3/5, 1)
	return fieldText(half, "f", "Filter", filter, t.editing == fieldWhere) + "  " +
		fieldText(width-half-2, "o", "Sort", sortText, t.editing == fieldOrderBy)
}

// topLines are the lines above the cards or grid, each clipped to width:
// the header with the range badge, the Filter and Sort fields, and a
// Filter or Sort translation error on a line of its own while there is
// one.
func (t *mongoTab) topLines(width int) []string {
	badge := t.indicator()
	if t.loading {
		badge = "loading..."
	}
	header := headerLine(width, strings.Join(t.path, "."), t.profile+" · mongodb", badgeStyle.Render(badge))
	lines := []string{header, t.fieldsLine(width)}
	if t.fieldErr != "" {
		lines = append(lines, errorStyle.Render("error: "+t.fieldErr))
	}
	clip := lipgloss.NewStyle().Inline(true).MaxWidth(max(width, 1))
	for i, l := range lines {
		lines[i] = clip.Render(l)
	}
	return lines
}

func (t *mongoTab) View(width, height int, focused bool) string {
	lines := t.topLines(width)
	top := strings.Join(lines, "\n") + "\n"
	bodyHeight := height - len(lines) - 1
	view, search := "cards", ""
	if t.gridMode {
		view, search = "grid", t.grid.searchStatus()
	}
	footer := footerLine(width, t.notice, search, t.exportStatus, t.editingPageSize, t.pageSizeInput, t.pageSize, "view: "+view)
	var body string
	switch {
	case t.err != nil:
		body = "\n" + errorStyle.Width(max(width, 1)).Render(fmt.Sprintf("%s failed to load:\n\n%v", t.Title(), t.err))
	case len(t.docs) == 0 && !t.loading:
		body = mutedStyle.Render("(no documents)")
	case t.gridMode:
		body = t.grid.view(width, bodyHeight, focused)
	default:
		body = t.cardsView(width, bodyHeight, focused)
	}
	return top + withFooter(body, footer, bodyHeight+1)
}

func (t *mongoTab) Update(k tea.KeyPressMsg, keys keyMap, pool *connPool) tea.Cmd {
	if t.editingPageSize {
		return t.updatePageSizeInput(k, pool)
	}
	if t.editing != fieldNone {
		return t.updateFieldInput(k, pool)
	}
	if t.gridMode {
		var handled bool
		if t.grid, handled = t.grid.update(k, keys); handled {
			return nil
		}
	} else if t.docCards.update(k, keys) {
		return nil
	}
	switch {
	case key.Matches(k, keys.ToggleView):
		t.gridMode = !t.gridMode
	case key.Matches(k, keys.Where):
		t.editing, t.fieldInput, t.fieldErr = fieldWhere, t.filter, ""
	case key.Matches(k, keys.OrderBy):
		t.editing, t.fieldInput, t.fieldErr = fieldOrderBy, t.sort, ""
	case key.Matches(k, keys.Count):
		return t.countCmd(pool)
	case key.Matches(k, keys.Export):
		t.editing, t.fieldInput, t.fieldErr = fieldExport, "", ""
	case key.Matches(k, keys.NextPage):
		// A short page is the last one; there is nothing after it.
		if len(t.docs) < t.pageSize {
			return nil
		}
		t.skip += t.pageSize
		return t.loadCmd(pool)
	case key.Matches(k, keys.PrevPage):
		if t.skip == 0 {
			return nil
		}
		t.skip = max(t.skip-t.pageSize, 0)
		return t.loadCmd(pool)
	case key.Matches(k, keys.PageSize):
		t.editingPageSize, t.pageSizeInput = true, ""
	}
	return nil
}

// updatePageSizeInput mirrors the SQL Data view's page-size field.
func (t *mongoTab) updatePageSizeInput(k tea.KeyPressMsg, pool *connPool) tea.Cmd {
	switch k.String() {
	case "esc":
		t.editingPageSize = false
	case "enter":
		t.editingPageSize = false
		if n, err := strconv.Atoi(t.pageSizeInput); err == nil && n > 0 {
			t.pageSize, t.skip = n, 0
			return t.loadCmd(pool)
		}
	case "backspace":
		if len(t.pageSizeInput) > 0 {
			t.pageSizeInput = t.pageSizeInput[:len(t.pageSizeInput)-1]
		}
	default:
		if len(k.Text) == 1 && k.Text[0] >= '0' && k.Text[0] <= '9' {
			t.pageSizeInput += k.Text
		}
	}
	return nil
}

// updateFieldInput handles keys while the Filter, Sort, or export field
// has focus. Enter translates a Filter or Sort from mongo shell syntax; a
// translation error is shown with its position and the field stays open
// with the text, while the previous result stays on screen untouched.
func (t *mongoTab) updateFieldInput(k tea.KeyPressMsg, pool *connPool) tea.Cmd {
	switch k.String() {
	case "esc":
		t.editing, t.fieldErr = fieldNone, ""
		return nil
	case "enter":
		text := strings.TrimSpace(t.fieldInput)
		if t.editing == fieldExport {
			t.editing = fieldNone
			if text == "" {
				return nil
			}
			return t.exportCmd(pool, text)
		}
		translated, err := mongoshell.Translate(text)
		if err != nil {
			t.fieldErr = err.Error()
			return nil
		}
		if t.editing == fieldWhere {
			if translated != t.filterJSON {
				t.hasTotal, t.counting = false, false
			}
			t.filter, t.filterJSON = text, translated
		} else {
			t.sort, t.sortJSON = text, translated
		}
		t.editing, t.fieldErr = fieldNone, ""
		t.skip = 0
		return t.loadCmd(pool)
	case "backspace":
		if r := []rune(t.fieldInput); len(r) > 0 {
			t.fieldInput = string(r[:len(r)-1])
		}
		return nil
	}
	if k.Text != "" {
		t.fieldInput += k.Text
	}
	return nil
}

func (t *mongoTab) cellPopupOpen() bool { return t.gridMode && t.grid.popupOpen() }
