package ui

import (
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/Snarap1/hqdb/internal/adapters"
	"github.com/Snarap1/hqdb/internal/catalog"
)

// catalogChunk is the Catalog level chunk size the tree loads at a time
// (spec: "Catalog limits"). The MCP server's payload-size cap does not
// apply here; the TUI has no serialized-response limit to protect. It is a
// var, not a const, so the gated teatest suite can shrink it for the
// duration of one test (see WithCatalogChunk) rather than seeding hundreds
// of real rows into a shared fixture just to get a "load more" row.
var catalogChunk = 500

// WithCatalogChunk overrides the Catalog level chunk size for the life of
// the returned restore function. Exported only for the gated teatest
// suite's server-side name search path, which needs a level to run out
// partway through its chunk.
func WithCatalogChunk(n int) (restore func()) {
	prev := catalogChunk
	catalogChunk = n
	return func() { catalogChunk = prev }
}

// catalogMsg reports the result of loading the node's next Catalog level or
// Column list, or of the connection attempt that precedes it.
type catalogMsg struct {
	node     *node
	schema   *catalog.SchemaOut
	columns  *catalog.ColumnsOut
	loadMore bool // true: append this chunk; false: replace the level
	err      error
	// connectErr is true when the Profile itself could not be reached,
	// which marks it broken rather than just this one node.
	connectErr bool
}

// loadCmd returns a tea.Cmd that opens (or reuses) n's Profile connection
// and fetches n's next Catalog level: Columns for a kindTable node,
// otherwise a GetSchema chunk starting at offset 0, or at n's current
// loadedN when loadMore is true.
//
// database/sql connects lazily on first use, not on Open, so a dead server
// usually only shows up once the query itself runs - there is no separate
// "the connection failed to open" moment to catch earlier. Whatever fails
// first (Open or the query) is given one retry against a freshly Reopened
// connection, which is what lets a connection the server dropped recover on
// its own; if that retry also fails, the Profile is reported unreachable
// (connectErr), since by then a plain per-node error and "the server is
// actually down" are indistinguishable anyway.
func loadCmd(work *cancelSet, pool *connPool, n *node, loadMore bool) tea.Cmd {
	ctx, done := work.start()
	return func() tea.Msg {
		defer done()
		query := func(conn adapters.DB) (*catalog.SchemaOut, *catalog.ColumnsOut, error) {
			if n.kind == kindTable {
				cols, err := catalog.GetColumns(ctx, conn, n.path)
				return nil, cols, err
			}
			offset := 0
			if loadMore {
				offset = n.loadedN
			}
			limits := catalog.Limits{NodeCap: catalogChunk, Offset: offset}
			out, err := catalog.GetSchema(ctx, conn, n.path, "", false, limits)
			return out, nil, err
		}

		profile := n.profileRoot().name
		var schema *catalog.SchemaOut
		var cols *catalog.ColumnsOut

		conn, err := pool.Open(profile)
		if err == nil {
			schema, cols, err = query(conn)
		}
		if err != nil && ctx.Err() != nil {
			// Cancelled (Ctrl+C): not a connection failure, so no retry and
			// the Profile is not marked broken.
			return catalogMsg{node: n, err: errCancelled}
		}
		if err != nil {
			conn, rerr := pool.Reopen(profile)
			if rerr != nil {
				return catalogMsg{node: n, err: rerr, connectErr: true}
			}
			schema, cols, err = query(conn)
			if err != nil {
				return catalogMsg{node: n, err: err, connectErr: true}
			}
		}
		return catalogMsg{node: n, schema: schema, columns: cols, loadMore: loadMore}
	}
}

// applyCatalogMsg writes a loadCmd result into the node it was fetched for.
func applyCatalogMsg(msg catalogMsg) {
	n := msg.node
	n.loading = false

	if msg.err != nil {
		if msg.connectErr {
			root := n.profileRoot()
			root.connectErr = msg.err
			if n != root {
				n.fetchErr = msg.err
			}
		} else {
			n.fetchErr = msg.err
		}
		return
	}

	n.fetchErr = nil
	if n.kind == kindProfile {
		n.connectErr = nil
	}

	switch {
	case msg.columns != nil:
		n.children = columnChildren(n, msg.columns.Columns)
		n.loadedN = len(n.children)
	case msg.schema != nil:
		fresh := levelChildren(n, msg.schema.Nodes)
		if msg.loadMore {
			n.children = append(dropLoadMore(n.children), fresh...)
		} else {
			n.children = fresh
		}
		if msg.schema.Truncated {
			n.children = append(n.children, &node{kind: kindLoadMore, parent: n, profile: n.profile})
		}
		n.loadedN = len(dropLoadMore(n.children))
	}

	n.loaded = true
	n.expanded = true
}

// dropLoadMore returns children without a trailing load-more sentinel, if
// any.
func dropLoadMore(children []*node) []*node {
	if hasLoadMore(children) {
		return children[:len(children)-1]
	}
	return children
}

// hasLoadMore reports whether children ends with a load-more sentinel,
// i.e. whether this level's local chunk was cut short of the full list.
func hasLoadMore(children []*node) bool {
	return len(children) > 0 && children[len(children)-1].kind == kindLoadMore
}

// filterSearchMsg reports the result of a server-side name search fired
// while the tree filter is active, for a level whose local chunk was cut
// short by a "load more" row (see catalogTree.searchCmds). query is the
// filter text the search was fired for, so a response that arrives after
// the user has kept typing can be told apart from the one that still
// matches the live filter and discarded.
type filterSearchMsg struct {
	node  *node
	query string
	nodes []catalog.SchemaNode
	err   error
}

// searchCmd asks node's Profile for every child of node whose name
// matches query, uncapped (the filter needs every match, not another
// "load more"). Unlike loadCmd, a connection failure here does not mark
// the node or its Profile broken: the local filter still works on its own,
// and the search is only a best-effort addition to it.
func searchCmd(work *cancelSet, pool *connPool, n *node, query string) tea.Cmd {
	ctx, done := work.start()
	return func() tea.Msg {
		defer done()
		conn, err := pool.Open(n.profileRoot().name)
		if err != nil {
			return filterSearchMsg{node: n, query: query, err: err}
		}
		out, err := catalog.GetSchema(ctx, conn, n.path, query, false, catalog.Limits{})
		if err != nil {
			return filterSearchMsg{node: n, query: query, err: err}
		}
		return filterSearchMsg{node: n, query: query, nodes: out.Nodes}
	}
}

// applyFilterSearchMsg writes a searchCmd result into its node's
// searchHits, deduped by name against the node's already-loaded children
// (a name the local chunk already has needs no extra row). A failed
// search, or one for a query the tree's filter has since moved past, is
// discarded: the local filter keeps working either way, and an older
// response must never clobber a newer one.
func applyFilterSearchMsg(t catalogTree, msg filterSearchMsg) catalogTree {
	if msg.err != nil || msg.query != t.filterQuery {
		return t
	}
	n := msg.node
	loaded := make(map[string]bool, len(n.children))
	for _, c := range n.children {
		if c.kind != kindLoadMore {
			loaded[strings.ToLower(c.name)] = true
		}
	}
	hits := make([]*node, 0, len(msg.nodes))
	for _, sn := range msg.nodes {
		if loaded[strings.ToLower(sn.Name)] {
			continue
		}
		hits = append(hits, schemaChildren(n, sn)...)
	}
	n.searchHits = hits
	return t
}
