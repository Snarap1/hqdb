# Access mode per Profile

Supersedes ADR-0001.

Each Profile carries an Access mode, `access = "read-only"` (the default) or `access = "read-write"`, and both front ends obey it.
On a read-only Profile the MCP server screens every statement as before, and the terminal UI refuses the same statements with the same reason instead of asking.
On a read-write Profile the MCP server runs statements unscreened, and the terminal UI asks before any write.
We moved the decision from "which front end is calling" to "which Profile is it" because one team hands the same binary to analysts, who should only read, and to developers, who need full access; the config, not the binary, is what differs between them.

## Considered options

Two builds (a read-only binary with the write paths compiled out, and a full one) were rejected: they double the release matrix, and the binary is a weaker boundary than a database account anyway.
A global switch instead of a per-Profile one was rejected because one person often needs write access to a dev database and read access to production in the same session.

## Consequences

Screening stays out of the core execution functions; the MCP layer and the Consoles call the detectors and decide from the Profile's Access mode.
The Access mode is defense in depth, not a security boundary: anyone who can edit the config can change it, so a read-only setup must also use a database account that can only read.
Read-only now means no writes at all: the screen that used to admit `INSERT`, `UPDATE`, and Redis `SET` (a leftover of the server this one replaced) refuses them, and on a read-write Profile they ask before running like any other write.
