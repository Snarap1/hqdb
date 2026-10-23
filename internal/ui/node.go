package ui

import (
	"fmt"

	"github.com/Snarap1/hqdb/internal/catalog"
)

// nodeKind identifies what a Catalog tree row represents.
type nodeKind int

const (
	kindProfile nodeKind = iota
	kindDatabase
	kindSchema
	kindTable
	kindColumn
	// kindLoadMore is a synthetic row appended to a level that was cut
	// short by the chunk size; activating it fetches the next chunk into
	// its parent.
	kindLoadMore
	// kindKeyPrefix and kindKey are the redis Catalog levels: a Key prefix
	// (one separator-split segment shared by several keys) expands into
	// further prefixes and keys; a key is a leaf that opens its value.
	kindKeyPrefix
	kindKey
)

// node is one row of the Catalog tree: a Profile root, or a Database,
// Schema, Table, or Column reached by expanding one. Nodes form a tree of
// pointers so that an async load's result can be written directly into the
// node it was fetched for, regardless of how many times the (value-typed)
// Model has been copied since the fetch started.
type node struct {
	kind    nodeKind
	name    string
	profile string // owning Profile name, set on every node
	adapter string // the Profile's Adapter, propagated to every descendant
	path    []string
	parent  *node

	// objKind marks a kindTable node as "table" or "view".
	objKind string
	// colType and colNullable describe a kindColumn node.
	colType     string
	colNullable string

	// validateErr is set once, at tree build time, when the Profile's
	// config table fails validation. connectErr is set and cleared
	// dynamically as connection attempts fail or succeed. Both apply to
	// kindProfile nodes only.
	validateErr error
	connectErr  error
	// fetchErr is the error from this node's own last Catalog fetch.
	fetchErr error

	expanded bool
	loading  bool
	loaded   bool
	// loadedN is how many real (non-sentinel) children are currently
	// loaded, used as the offset for a "load more" fetch.
	loadedN  int
	children []*node

	// searchHits are extra children found by a server-side name search
	// fired while the tree filter is active, for a level whose locally
	// loaded chunk was cut short by a "load more" row. They are
	// display-only: never counted in loadedN, never merged into children,
	// and replaced wholesale by the next search rather than appended to.
	searchHits []*node
}

// hasSchemas reports whether adapter has a Schema level between Database
// and Table.
func hasSchemas(adapter string) bool {
	switch adapter {
	case "postgres", "mssql", "odbc":
		return true
	}
	return false
}

// nextKind returns the kind of node that expanding a node of kind produces
// for adapter.
func nextKind(adapter string, kind nodeKind) nodeKind {
	switch kind {
	case kindProfile:
		return kindDatabase
	case kindDatabase:
		if hasSchemas(adapter) {
			return kindSchema
		}
		return kindTable
	case kindSchema:
		return kindTable
	case kindTable:
		return kindColumn
	default:
		return kindColumn // columns and load-more nodes have no further level
	}
}

// expandable reports whether kind is ever expanded by fetching a Catalog
// level (every kind except the leaf Column and the load-more sentinel).
func (k nodeKind) expandable() bool {
	return k != kindColumn && k != kindLoadMore && k != kindKey
}

// isDDLTable reports whether n is a Table whose Adapter actually has DDL.
// Every SQL Adapter does; redis and mongodb have no DDL concept (spec: "d is
// unavailable on redis and mongodb nodes"). This also gates kindDatabase,
// kindSchema, kindProfile, and the other non-Table kinds, since only a
// Table has a definition to show.
func (n *node) isDDLTable() bool {
	if n.kind != kindTable {
		return false
	}
	return adapterUIs[n.adapter].ddl
}

// label is this node's own text as the tree shows it: the name, and the
// muted detail after it (the Adapter, object kind, or column type), without
// the glyph or indentation the tree view adds.
func (n *node) label() (name, meta string) {
	switch n.kind {
	case kindProfile:
		return n.name, n.adapter
	case kindLoadMore:
		return "load more", ""
	case kindTable:
		kindTag := n.objKind
		if kindTag == "" {
			kindTag = "table"
		}
		return n.name, kindTag
	case kindKeyPrefix:
		return n.name, "prefix"
	case kindKey:
		return n.name, "key"
	case kindColumn:
		nullable := "NOT NULL"
		if n.colNullable == "YES" {
			nullable = "NULL"
		}
		return n.name, n.colType + " " + nullable
	default: // kindDatabase, kindSchema
		return n.name, ""
	}
}

// depth is how many ancestors this node has, used for the tree's
// indentation.
func (n *node) depth() int {
	d := 0
	for p := n.parent; p != nil; p = p.parent {
		d++
	}
	return d
}

// profileRoot walks up to the kindProfile ancestor that owns n (n itself
// when n is already the root).
func (n *node) profileRoot() *node {
	r := n
	for r.parent != nil {
		r = r.parent
	}
	return r
}

// broken reports whether this Profile node failed validation or its last
// connection attempt. Only meaningful for kindProfile nodes.
func (n *node) broken() bool {
	return n.validateErr != nil || n.connectErr != nil
}

// brokenMessage is the full-screen explanation shown when a broken Profile
// is selected, or "" when n is healthy.
func (n *node) brokenMessage() string {
	switch {
	case n.validateErr != nil:
		return fmt.Sprintf("Profile %s (%s) failed validation:\n\n%v", n.name, n.adapter, n.validateErr)
	case n.connectErr != nil:
		return fmt.Sprintf("Profile %s (%s) failed to connect:\n\n%v", n.name, n.adapter, n.connectErr)
	}
	return ""
}

// fetchErrMessage is the full-screen explanation shown when a node whose
// own Catalog fetch failed is selected.
func (n *node) fetchErrMessage() string {
	return fmt.Sprintf("%s failed to load:\n\n%v", n.name, n.fetchErr)
}

// levelChildren converts one GetSchema level's nodes into Catalog tree
// children of parent, propagating identity (Profile, Adapter, path) so
// each child can be expanded or fetched on its own.
func levelChildren(parent *node, nodes []catalog.SchemaNode) []*node {
	children := make([]*node, 0, len(nodes))
	for _, sn := range nodes {
		children = append(children, schemaChildren(parent, sn)...)
	}
	return children
}

// schemaChildren converts one GetSchema node into the tree row(s) it
// becomes under parent. A redis name is a Key prefix, a key, or both: the
// Catalog reports "key" when an exact key exists, and a Count above one
// then means longer keys share it as a prefix too (fixture "user:1000"
// next to "user:1000:roles"), so both rows are offered.
func schemaChildren(parent *node, sn catalog.SchemaNode) []*node {
	mk := func(kind nodeKind) *node {
		return &node{
			kind:    kind,
			name:    sn.Name,
			objKind: sn.Kind,
			profile: parent.profile,
			adapter: parent.adapter,
			path:    append(append([]string{}, parent.path...), sn.Name),
			parent:  parent,
		}
	}
	if parent.adapter != "redis" {
		return []*node{mk(nextKind(parent.adapter, parent.kind))}
	}
	if sn.Kind != "key" {
		return []*node{mk(kindKeyPrefix)}
	}
	if sn.Count > 1 {
		return []*node{mk(kindKeyPrefix), mk(kindKey)}
	}
	return []*node{mk(kindKey)}
}

// columnChildren converts a GetColumns result into the Column leaves of a
// Table node.
func columnChildren(parent *node, cols []catalog.Column) []*node {
	children := make([]*node, len(cols))
	for i, c := range cols {
		children[i] = &node{
			kind:        kindColumn,
			name:        c.Name,
			colType:     c.Type,
			colNullable: c.Nullable,
			profile:     parent.profile,
			adapter:     parent.adapter,
			parent:      parent,
		}
	}
	return children
}
