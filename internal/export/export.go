// Package export streams a SQL query into a csv, json, or ndjson file
// without materializing all rows, and writes mongodb Documents to the same
// formats (Documents). It does not screen the statement; the MCP
// layer screens before calling Query (ADR-0003).
package export

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/Snarap1/hqdb/internal/sqlrows"
	"github.com/Snarap1/hqdb/internal/values"
)

// Formats are the file formats Query can write.
var Formats = []string{"csv", "json", "ndjson"}

// InferFormat guesses the format from the file extension.
func InferFormat(destPath string) (string, error) {
	switch strings.ToLower(filepath.Ext(destPath)) {
	case ".csv", ".tsv":
		return "csv", nil
	case ".json":
		return "json", nil
	case ".ndjson":
		return "ndjson", nil
	}
	return "", fmt.Errorf("cannot infer format from extension %q; pass format explicitly", filepath.Ext(destPath))
}

// Querier is what Query reads rows through: a *sql.DB for the MCP tool, or
// a SQL Console's pinned *sql.Conn so an export sees that session's state.
type Querier interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

// Query streams a SQL query into destPath. limit <= 0 means unlimited. The
// caller is responsible for screening sqlText first. ctx is passed to the
// driver so a cancelled or timed-out caller stops the query in flight; there
// is no default timeout.
func Query(ctx context.Context, conn Querier, sqlText, destPath, format string, limit int) (int, error) {
	dir := filepath.Dir(destPath)
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		return 0, fmt.Errorf("parent directory does not exist: %s", dir)
	}
	rows, err := conn.QueryContext(ctx, sqlText)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil {
		return 0, err
	}
	f, err := os.Create(destPath)
	if err != nil {
		return 0, err
	}
	var written int
	var writeErr error
	switch format {
	case "csv":
		written, writeErr = writeCSV(f, rows, columns, limit)
	case "json":
		written, writeErr = writeJSONRows(f, rows, columns, limit)
	case "ndjson":
		written, writeErr = writeNDJSON(f, rows, columns, limit)
	default:
		writeErr = fmt.Errorf("format must be one of %s (got %q)", strings.Join(Formats, ", "), format)
	}
	if cerr := f.Close(); writeErr == nil {
		writeErr = cerr
	}
	return written, writeErr
}

// DocumentSource streams Documents to emit, one at a time, and returns
// emit's error or its own (a cursor failure). mongocmd.Each fits it.
type DocumentSource func(emit func(doc any) error) error

// Documents streams JSON-safe mongodb Documents (values.Doc in server
// field order, or plain maps) from src to destPath in format, without
// holding them all in memory. JSON and NDJSON keep each Document whole.
// CSV gets one column per top-level field seen in any Document ("_id"
// first, the rest in first-seen order), with nested values as compact JSON
// and a missing field as an empty cell; since the header needs every
// field, CSV spools the Documents to a temporary NDJSON file beside
// destPath first and removes it afterwards.
func Documents(destPath, format string, src DocumentSource) (int, error) {
	dir := filepath.Dir(destPath)
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		return 0, fmt.Errorf("parent directory does not exist: %s", dir)
	}
	if format != "csv" && format != "json" && format != "ndjson" {
		return 0, fmt.Errorf("format must be one of %s (got %q)", strings.Join(Formats, ", "), format)
	}
	if format == "csv" {
		return writeDocumentsCSV(destPath, src)
	}
	f, err := os.Create(destPath)
	if err != nil {
		return 0, err
	}
	var n int
	var writeErr error
	if format == "json" {
		n, writeErr = writeDocumentsJSON(f, src)
	} else {
		n, writeErr = writeDocumentsNDJSON(f, src, nil)
	}
	if cerr := f.Close(); writeErr == nil {
		writeErr = cerr
	}
	return n, writeErr
}

// documentKeys lists one Document's top-level fields in order (a plain
// map's sorted, since it has none).
func documentKeys(d any) []string {
	switch t := d.(type) {
	case values.Doc:
		keys := make([]string, len(t))
		for i, f := range t {
			keys[i] = f.Key
		}
		return keys
	case map[string]any:
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		return keys
	}
	return nil
}

// documentField returns a Document's top-level field named key.
func documentField(d any, key string) (any, bool) {
	switch t := d.(type) {
	case values.Doc:
		return t.Get(key)
	case map[string]any:
		v, ok := t[key]
		return v, ok
	}
	return nil, false
}

// fieldSet collects top-level field names: "_id" first, then first-seen.
type fieldSet struct {
	seen  map[string]bool
	order []string
	hasID bool
}

func (s *fieldSet) add(d any) {
	if s.seen == nil {
		s.seen = map[string]bool{}
	}
	for _, k := range documentKeys(d) {
		if k == "_id" {
			s.hasID = true
			continue
		}
		if !s.seen[k] {
			s.seen[k] = true
			s.order = append(s.order, k)
		}
	}
}

func (s *fieldSet) fields() []string {
	if s.hasID {
		return append([]string{"_id"}, s.order...)
	}
	return append([]string{}, s.order...)
}

// DocumentFields lists every top-level field of docs: "_id" first (when
// present), then the rest in first-seen order.
func DocumentFields(docs []any) []string {
	var s fieldSet
	for _, d := range docs {
		s.add(d)
	}
	return s.fields()
}

func writeDocumentsNDJSON(w io.Writer, src DocumentSource, each func(any)) (int, error) {
	enc := json.NewEncoder(w)
	n := 0
	err := src(func(d any) error {
		if each != nil {
			each(d)
		}
		if err := enc.Encode(d); err != nil {
			return err
		}
		n++
		return nil
	})
	return n, err
}

func writeDocumentsJSON(f *os.File, src DocumentSource) (int, error) {
	if _, err := f.WriteString("["); err != nil {
		return 0, err
	}
	enc := json.NewEncoder(f)
	n := 0
	err := src(func(d any) error {
		if n > 0 {
			if _, err := f.WriteString(","); err != nil {
				return err
			}
		}
		if err := enc.Encode(d); err != nil {
			return err
		}
		n++
		return nil
	})
	if err != nil {
		return n, err
	}
	_, err = f.WriteString("]\n")
	return n, err
}

// writeDocumentsCSV spools src to a temporary NDJSON file while collecting
// the fields, then writes the CSV from the spool.
func writeDocumentsCSV(destPath string, src DocumentSource) (n int, err error) {
	spool, err := os.CreateTemp(filepath.Dir(destPath), ".hqdb-export-*.ndjson")
	if err != nil {
		return 0, err
	}
	defer func() {
		spool.Close()
		os.Remove(spool.Name())
	}()
	var fields fieldSet
	bw := bufio.NewWriter(spool)
	n, err = writeDocumentsNDJSON(bw, src, fields.add)
	if err == nil {
		err = bw.Flush()
	}
	if err != nil {
		return n, err
	}
	if _, err := spool.Seek(0, io.SeekStart); err != nil {
		return n, err
	}
	f, err := os.Create(destPath)
	if err != nil {
		return n, err
	}
	w := csv.NewWriter(f)
	columns := fields.fields()
	err = w.Write(columns)
	dec := json.NewDecoder(bufio.NewReader(spool))
	for err == nil {
		var raw json.RawMessage
		if derr := dec.Decode(&raw); derr == io.EOF {
			break
		} else if derr != nil {
			err = derr
			break
		}
		var d any
		if d, err = values.DecodeOrdered(raw); err != nil {
			break
		}
		rec := make([]string, len(columns))
		for i, c := range columns {
			v, _ := documentField(d, c)
			rec[i] = stringify(v)
		}
		err = w.Write(rec)
	}
	w.Flush()
	if err == nil {
		err = w.Error()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return n, err
}

// writeCSV writes a header row and one line per row, stringifying every value.
func writeCSV(f *os.File, rows *sql.Rows, columns []string, limit int) (int, error) {
	w := csv.NewWriter(f)
	if err := w.Write(columns); err != nil {
		return 0, err
	}
	n, err := sqlrows.Stream(rows, columns, limit, func(row []any) error {
		rec := make([]string, len(row))
		for i, v := range row {
			rec[i] = stringify(v)
		}
		return w.Write(rec)
	})
	w.Flush()
	if err != nil {
		return n, err
	}
	return n, w.Error()
}

// writeJSONRows writes a streamed JSON array of row objects.
func writeJSONRows(f *os.File, rows *sql.Rows, columns []string, limit int) (int, error) {
	if _, err := f.WriteString("["); err != nil {
		return 0, err
	}
	enc := json.NewEncoder(f)
	first := true
	n, err := sqlrows.Stream(rows, columns, limit, func(row []any) error {
		if !first {
			if _, err := f.WriteString(","); err != nil {
				return err
			}
		}
		first = false
		return enc.Encode(sqlrows.Object(columns, row))
	})
	if err != nil {
		return n, err
	}
	_, err = f.WriteString("]\n")
	return n, err
}

// writeNDJSON writes one row object per line.
func writeNDJSON(f *os.File, rows *sql.Rows, columns []string, limit int) (int, error) {
	enc := json.NewEncoder(f)
	return sqlrows.Stream(rows, columns, limit, func(row []any) error {
		return enc.Encode(sqlrows.Object(columns, row))
	})
}

// stringify renders a converted value for CSV output.
func stringify(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case bool:
		return strconv.FormatBool(t)
	case int64:
		return strconv.FormatInt(t, 10)
	case float64:
		return strconv.FormatFloat(t, 'g', -1, 64)
	default:
		b, err := json.Marshal(t)
		if err != nil {
			return fmt.Sprintf("%v", t)
		}
		return string(b)
	}
}
