# 06: hqdb setup wizard

Status: done

`internal/setup` (ADR-0009): Find scans merged Profiles for literal credentials (password key, URL conn_str password, ODBC Pwd), Stores offers the platform store (secret-tool / security, written through their CLI), pass, and an env file on Linux and macOS; Apply stores, verifies the read-back, then patches only the contributing config line, verified before an atomic rename.
Windows offers no store at all: no CLI there reads a password back, reading the Credential Manager from hqdb is what an antivirus treats as an infostealer, and Windows sources no POSIX env file, so `cmdkey`, a `credential get` subcommand, and the env store were all dropped there.
`internal/ui/setupui.go` is the wizard over it.
`hqdb check` hints `literal credentials; run hqdb setup` on an otherwise-passing Profile.
