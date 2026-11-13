package server

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/Snarap1/hqdb/internal/catalog"
	"github.com/Snarap1/hqdb/internal/ddl"
)

// ResultText decides what the text content of a successful tool result
// carries beside the structured content (ADR-0007).
type ResultText string

const (
	// ResultTextAuto sends a summary to clients known to give the model
	// the structured content, and the full JSON to every other client.
	ResultTextAuto ResultText = "auto"
	// ResultTextSummary always sends a short summary.
	ResultTextSummary ResultText = "summary"
	// ResultTextJSON always sends the full JSON, as the MCP specification
	// suggests for clients that read text only.
	ResultTextJSON ResultText = "json"
)

// ParseResultText reads the --result-text value of hqdb mcp.
func ParseResultText(s string) (ResultText, error) {
	switch v := ResultText(s); v {
	case ResultTextAuto, ResultTextSummary, ResultTextJSON:
		return v, nil
	}
	return "", fmt.Errorf("--result-text must be auto, summary, or json (got %q)", s)
}

// resultText is the mode of this process; Serve sets it.
var resultText = ResultTextAuto

// structuredClients are the clientInfo names of the MCP clients known to
// hand structuredContent to the model, so a text summary loses nothing.
// Claude Code does (since 2.0.21); Cursor, for one, reads text only.
var structuredClients = map[string]bool{"claude-code": true}

// maxSummaryJSON is the structured content size above which the full
// JSON is sent as text even in summary mode: Claude Code leaves a
// structuredContent over about 1 MiB off the result message and keeps
// only the text content.
const maxSummaryJSON = 1 << 20

// summaryMode reports whether a call's result carries a summary.
func summaryMode(mode ResultText, req *mcp.CallToolRequest) bool {
	switch mode {
	case ResultTextSummary:
		return true
	case ResultTextJSON:
		return false
	}
	if req == nil {
		return false
	}
	// ClientInfo reads the per-request _meta of the 2026-07-28 protocol
	// (which Claude Code speaks) and falls back to the initialize request.
	info := req.ClientInfo()
	return info != nil && structuredClients[info.Name]
}

// lean returns the CallToolResult for out: a text summary in summary
// mode, so the SDK does not also put the whole JSON in the text content,
// or nil, which lets the SDK send the JSON as text. Summaries never quote
// a cell, field, or reply value, so masking (ADR-0005) has nothing to
// cover in them; they name only counts, columns, and what the agent sent.
func lean(req *mcp.CallToolRequest, out any, summary string) *mcp.CallToolResult {
	if !summaryMode(resultText, req) {
		return nil
	}
	if raw, err := json.Marshal(out); err != nil || len(raw) > maxSummaryJSON {
		return nil
	}
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: summary}}}
}

// plural renders n with the singular or plural noun.
func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, many)
}

// maxSummaryNames bounds the column names a summary lists.
const maxSummaryNames = 20

// nameList joins names, cut after maxSummaryNames.
func nameList(names []string) string {
	if len(names) <= maxSummaryNames {
		return strings.Join(names, ", ")
	}
	return strings.Join(names[:maxSummaryNames], ", ") + fmt.Sprintf(", ... (%d more)", len(names)-maxSummaryNames)
}

// structuredNote ends every summary, so a reader of the text alone knows
// where the data is.
const structuredNote = "; the data is in structuredContent"

func summarizeQuery(out RunQueryOut) string {
	var b strings.Builder
	b.WriteString(plural(out.RowCount, "row", "rows"))
	if out.Truncated {
		b.WriteString(" (truncated at the limit)")
	}
	b.WriteString(", " + plural(len(out.Columns), "column", "columns"))
	if len(out.Columns) > 0 {
		b.WriteString(": " + nameList(out.Columns))
	}
	if len(out.MaskedColumns) > 0 {
		b.WriteString("; masked: " + nameList(out.MaskedColumns))
	}
	return b.String() + structuredNote
}

func summarizeMongo(out RunMongoOut) string {
	var b strings.Builder
	b.WriteString(out.Operation + ": ")
	switch out.Operation {
	case "count":
		b.WriteString(fmt.Sprintf("%d matching documents", out.Count))
	case "distinct":
		b.WriteString(plural(len(out.Documents), "value", "values"))
	case "find", "aggregate":
		b.WriteString(plural(len(out.Documents), "document", "documents"))
	default:
		b.WriteString("summary document returned")
	}
	if out.Truncated {
		b.WriteString(" (truncated at the limit)")
	}
	if len(out.MaskedFields) > 0 {
		b.WriteString("; masked: " + nameList(out.MaskedFields))
	}
	return b.String() + structuredNote
}

func summarizeRedis(out RunRedisOut) string {
	return firstField(out.Command) + ": " + redisShape(out.Result) + structuredNote
}

// redisShape describes a reply by its type and size, never its value.
func redisShape(v any) string {
	switch t := v.(type) {
	case nil:
		return "nil reply"
	case string:
		return "string reply"
	case []any:
		return "array reply of " + plural(len(t), "element", "elements")
	case map[string]any:
		return "map reply of " + plural(len(t), "entry", "entries")
	case bool:
		return "boolean reply"
	case float64, float32, int, int64, uint64, json.Number:
		return "number reply"
	}
	return "reply"
}

func summarizeSchema(out catalog.SchemaOut) string {
	s := plural(len(out.Nodes), "node", "nodes")
	if len(out.Columns) > 0 {
		s += ", " + plural(len(out.Columns), "column", "columns")
	}
	if out.Truncated {
		s += " (truncated)"
	}
	return s + structuredNote
}

func summarizeColumns(out catalog.ColumnsOut) string {
	return plural(len(out.Columns), "column", "columns") + " of " + out.Table + structuredNote
}

func summarizeDDL(out ddl.Out) string {
	kind := "reconstructed from the catalog"
	if out.Native {
		kind = "the server's own text"
	}
	lines := strings.Count(strings.TrimRight(out.Definition, "\n"), "\n") + 1
	return fmt.Sprintf("definition of %s (%s), %s", out.Table, kind, plural(lines, "line", "lines")) + structuredNote
}

func summarizeExport(out ExportQueryOut) string {
	return fmt.Sprintf("wrote %s to %s as %s", plural(out.RowCount, "row", "rows"), out.Path, out.Format) + structuredNote
}

func summarizeProfiles(out ListProfilesOut) string {
	s := plural(len(out.Profiles), "profile", "profiles")
	for _, p := range out.Profiles {
		if p.IsDefault {
			s += " (default: " + p.Name + ")"
		}
	}
	return s + structuredNote
}
