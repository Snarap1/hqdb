# MCP masking per Profile

A Profile can set `mask = true` (default `false`), and then the MCP server masks personal data in everything it returns: query rows, Documents, Redis replies, export files, DDL text, and database error messages.
The terminal UI does not mask, because its reader is the human who owns the data; the agent's context, the harness logs, and the model provider are where personal data must not go.
Like the Access mode, the switch is per Profile, because the same person masks production and leaves a dev database in clear in the same session.

A value is masked when its column or field name matches a name rule (built-in rules in English, German, French, Polish, Russian, and Ukrainian, plus user globs), or when a value detector recognizes it whatever its column (email, phone, IP, card, IBAN, tokens, and checksum-validated national IDs of Germany, France, Poland, and the CIS).
A masked value becomes `masked-<12 hex>`, a truncated HMAC-SHA256 of the normalized value under a key that lives only in the memory of the `hqdb mcp` process.
Equal values share a marker within the session, so the agent can still join, group, and count; GDPR special categories become `masked-redacted` instead, because a low-cardinality column can be decoded from marker frequencies.
A statement on a masked Profile that contains a marker is refused, whatever the Access mode, so an agent cannot write placeholders back over real data or silently query for a placeholder.

## Considered options

Plain `***` redaction was rejected as the default: it hides whether two values are equal, which most analysis needs.
A key from the environment or the config was rejected: an agent with a shell on the same machine can read either and brute-force low-entropy values such as phone numbers and birth-date-bearing IDs; the cost is that markers are stable only within one MCP session.
Five hex characters were rejected for being too short: about 1,200 distinct values already give even odds of two different values sharing a marker.
A region switch in the config was rejected in favor of running every detector always; detectors for IDs without a checksum were left out instead, and those IDs are caught by name only.
Masking in the core execution functions was rejected for the same reason screening stays out of them: the front end decides, the core runs what it is given (the export core takes an optional row transform).

## Consequences

Masking is defense in depth, not a security boundary: aliases, expressions, `row_to_json`, and filter oracles get around name rules, and the boundary is a database login with column privileges or masking views.
Under GDPR a keyed hash is pseudonymisation, so masked output is still personal data in law; it keeps clear values out of the agent's context, it does not anonymise.
