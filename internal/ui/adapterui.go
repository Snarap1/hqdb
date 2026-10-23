package ui

import (
	tea "charm.land/bubbletea/v2"

	"github.com/Snarap1/hqdb/internal/consolestate"
)

// adapterUI is what the terminal UI offers for one Adapter: whether its
// Tables have DDL, the Data view a Table (or Collection, or key) opens,
// and the Console a Profile opens. adapterUIs is the single place that
// dispatches on the Adapter name for these.
type adapterUI struct {
	ddl         bool
	openData    func(pool *connPool, n *node) (tab, tea.Cmd)
	openConsole func(n *node, profile string, state *consolestate.Store) tab
}

var sqlAdapterUI = adapterUI{
	ddl: true,
	openData: func(pool *connPool, n *node) (tab, tea.Cmd) {
		dv := newDataViewTab(n.profile, n.adapter, n.path)
		return dv, dv.loadCmd(pool)
	},
	// A postgres Console binds to the Database n is in (ADR-0004), else to
	// the Profile's own; poolDatabase is "" for every other Adapter.
	openConsole: func(n *node, profile string, state *consolestate.Store) tab {
		return newConsoleTab(profile, poolDatabase(n.adapter, n.path), state)
	},
}

var adapterUIs = map[string]adapterUI{
	"postgres":   sqlAdapterUI,
	"mysql":      sqlAdapterUI,
	"mssql":      sqlAdapterUI,
	"odbc":       sqlAdapterUI,
	"clickhouse": sqlAdapterUI,
	"mongodb": {
		openData: func(pool *connPool, n *node) (tab, tea.Cmd) {
			mt := newMongoTab(n.profile, n.path)
			return mt, mt.loadCmd(pool)
		},
		// A mongodb Console starts on the Database n is in (spec story
		// 85), else the Profile's.
		openConsole: func(n *node, profile string, state *consolestate.Store) tab {
			db := ""
			if len(n.path) > 0 {
				db = n.path[0]
			}
			return newMongoConsoleTab(profile, db, state)
		},
	},
	"redis": {
		openData: func(pool *connPool, n *node) (tab, tea.Cmd) {
			t, cmd := openRedisKeyCmd(pool, n.profile, n.path)
			return t, cmd
		},
		openConsole: func(_ *node, profile string, state *consolestate.Store) tab {
			return newRedisConsoleTab(profile, state)
		},
	},
}
