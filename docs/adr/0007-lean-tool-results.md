# Lean tool results

Every MCP tool keeps returning its data as `structuredContent` under an output schema, and for clients known to give the model `structuredContent` the text content becomes a one-line summary instead of a second copy of the JSON.
We chose this because the go-sdk, when a handler leaves the content empty, puts the whole JSON into `content[0].text` as an escaped string, so a 1000-row, 8-column `run_query` result went over the wire as 380 KB for 176 KB of data; in our in-memory test the summary halves the wire size (221 KB to 104 KB for a 104 KB result).

## Which clients get a summary

`hqdb mcp --result-text` picks the mode: `auto` (the default), `summary`, or `json`.
In `auto` the client's own name decides, read from the per-request `_meta` of the 2026-07-28 protocol or from `initialize`: Claude Code (`claude-code`) gets the summary, every other client the JSON.
We checked Claude Code 2.1.322 end to end: it sends the model `structuredContent` and leaves the text out when both are present, so the model still read the row values from a summary-mode result.
Cursor does the opposite, giving the model the text content only (its staff confirmed this on its forum), so a summary there would hide the data; that is why the default is not `summary` for everyone.
The MCP specification says a tool that returns structured content SHOULD also return the serialized JSON as text, for clients that read text only; `auto` keeps that for every client not known to read `structuredContent`.

A result over 1 MiB of JSON keeps the full JSON as text even in summary mode: Claude Code leaves a `structuredContent` that large off the message and keeps only the content, and a summary alone would then lose the data.

## What a summary says

A summary names counts, column names, masked columns, the operation or command, and what the agent sent (an export path, a table it asked for), and ends with `the data is in structuredContent`.
It never quotes a cell, field, or reply value, so masking (ADR-0005) has nothing to cover in it; the structured content is masked exactly as before.
Error results are unchanged: they are plain text, masked by `maskErr`.

## Considered options

Dropping the output schema and `structuredContent` to return text only was rejected: the schema lets a client validate and parse the result, and Claude Code prefers `structuredContent`.
Sending the full JSON only for small results was rejected as the rule: a text-only client would see the data or not depending on its size.
Avoiding the SDK's validation pass was looked at and left alone: for a typed tool the go-sdk marshals the output, unmarshals it into `any`, applies schema defaults, validates, and marshals it again (`applySchema` in `mcp/tool.go`), which dominated the 10,000-row benchmark (590 ms against 330 ms untyped).
The cheap way out is to register the data tools with the low-level `Server.AddTool`, keep the generated output schema, validate the input ourselves, and set `StructuredContent` to our own `json.RawMessage`; that trades the SDK's input defaults and checks for code of our own, so it is a separate decision.
