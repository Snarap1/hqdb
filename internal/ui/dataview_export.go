package ui

import (
	"context"
	"fmt"

	tea "charm.land/bubbletea/v2"

	"github.com/Snarap1/hqdb/internal/adapters"
	"github.com/Snarap1/hqdb/internal/dataview"
	"github.com/Snarap1/hqdb/internal/export"
)

// exportViewMsg reports the result of a SQL Data view export.
type exportViewMsg struct {
	tab     *dataViewTab
	seq     int
	format  string
	written int
	err     error
	// cancelled is set when Ctrl+C stopped the export.
	cancelled bool
}

// exportCmd starts exporting the tab's current query (Table, WHERE, ORDER
// BY, no page limit) to destPath, in the format export.InferFormat picks
// from its extension, through the same core export.Query the MCP
// export_query tool uses, screened like a page load (ADR-0003). The work runs under a
// cancellable context kept on the tab (cancelExport), so the tab's
// Ctrl+C (Cancel) can stop it like any other tab work.
func (d *dataViewTab) exportCmd(pool *connPool, destPath string) tea.Cmd {
	format, err := export.InferFormat(destPath)
	if err != nil {
		d.exportStatus = "export failed: " + err.Error()
		return nil
	}
	sqlText, err := dataview.BuildExportQuery(d.adapter, d.path, d.where, d.orderBy)
	if err == nil {
		err = d.screen()
	}
	if err != nil {
		d.exportStatus = "export failed: " + err.Error()
		return nil
	}
	d.cancelExport()
	ctx, cancel := context.WithCancel(context.Background())
	d.exportCancel = cancel
	d.exportStatus = "exporting..."
	d.notice = ""
	d.exportSeq++
	profile, database, seq := d.profile, d.database(), d.exportSeq
	return func() tea.Msg {
		defer cancel()
		conn, err := pool.Open(poolKey{profile, database})
		if err != nil {
			return exportViewMsg{tab: d, seq: seq, format: format, err: err}
		}
		sqlConn, ok := conn.(*adapters.SQL)
		if !ok {
			return exportViewMsg{tab: d, seq: seq, format: format, err: fmt.Errorf("export is not available for %s", conn.Kind())}
		}
		n, err := export.Query(ctx, sqlConn.SQL(), sqlText, destPath, format, 0)
		return exportViewMsg{tab: d, seq: seq, format: format, written: n, err: err, cancelled: ctx.Err() != nil}
	}
}

// cancelExport stops the tab's running export, if any.
func (d *dataViewTab) cancelExport() {
	if d.exportCancel != nil {
		d.exportCancel()
		d.exportCancel = nil
	}
}

// applyExportViewMsg writes an export's outcome into its tab's status,
// unless a newer export has started since (its outcome would be stale).
func applyExportViewMsg(msg exportViewMsg) {
	d := msg.tab
	if msg.seq != d.exportSeq {
		return
	}
	d.exportCancel = nil
	if msg.cancelled {
		d.exportStatus = "export cancelled"
		return
	}
	if msg.err != nil {
		d.exportStatus = "export failed: " + msg.err.Error()
		return
	}
	d.exportStatus = fmt.Sprintf("export (%s): %d rows written", msg.format, msg.written)
}
