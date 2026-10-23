# The terminal UI does not screen statements

Superseded by ADR-0003: the Profile's Access mode, not the front end, now decides whether a statement is screened.

The MCP server refuses writes through `readonly.Violation`, the redis whitelist, and the mongodb operation surface, because its caller is an agent.
The terminal UI runs whatever the human at the keyboard types, so it skips that screening; the human, not the screening, is the safety boundary there.
Screening stays a property of the MCP front end, not of the shared core, so the two front ends cannot drift into sharing a weaker check.

## Consequences

Core execution functions must not screen by themselves; the MCP layer screens before calling them.
Any future guard rails in the TUI (confirmations, per-Profile protection) are a TUI concern and must not loosen MCP screening.
