# Secret references in Profiles

Profile passwords used to live in the config file in plain text, in the `password` key or inside a `conn_str` URL or ODBC string.
The file is committed, backed up, and read by agents, and the password traveled with it.
Masking (ADR-0005) protects what a Profile returns, not what its own config holds, so this is a separate layer.

## Decision

A string option may carry a reference to a secret instead of the secret itself, and hqdb expands it where a connection is opened (ADR-0008's two forms):

- `${env:NAME}`: the value of the environment variable `NAME`.
  An unset variable is an error, so a typo fails loudly instead of sending an empty password.
- `${cmd:command}`: the standard output of `command`, run through the shell, with trailing carriage returns and newlines trimmed.
  One form reaches every secret manager with a CLI - `pass`, `op` (1Password), `vault`, `secret-tool`, `security` - without hqdb knowing any of them.

Both forms expand inside a longer value, so they work in a `conn_str` URL and in each element of the ODBC list form.
A string that matches neither form is literal text: a password written in the file keeps working, and the reference syntax is the only change to the config format.

## Where the expansion happens

Only inside the public `adapters.Open`, `OpenDatabase`, `OpenReusable`, and `Databases` entries, which are the single way both front ends and `hqdb check --probe` reach a database.
Validation runs after the expansion, so a reference may fill any string option and the expanded value is still checked.
Everything else keeps seeing the raw table from the config file: `list_profiles`, the terminal UI's config tree, and the MCP pool, which hashes the raw table for its key (ADR-0006).
A `${cmd:...}` Profile therefore keeps one pool key whatever the command returns, and a config edit still produces a new key.

A command's output is cached per command for five minutes.
Connections are pooled anyway, so the cache only keeps a command from running on every call for adapters whose connections reopen after statements (mysql, clickhouse native, redis).
A rotated secret reaches a newly opened connection within the cache TTL; a pooled connection holds the password it was opened with until the pool evicts it, exactly as it holds one from an edited file until the next checkout.

`hqdb check` validates references without running any command: the syntax, and that every `${env:NAME}` names a variable set on the machine the check runs on.
`--probe` resolves for real, so a broken command fails its probe line.

## Errors never echo the secret

Every failure names the reference (which holds no secret) and a reason: `secret reference ${env:NAME}: environment variable "NAME" is not set`, `secret reference ${cmd:command}: command failed: exit status 1: <first stderr line>`, or `command timed out after 30s`.
The timeout keeps a command waiting on a prompt (an unlocked keyring, an agent asking for a passphrase) from hanging a call.
Error messages that name a `conn_str` go through `config.RedactConnStr`, which replaces URL credentials and ODBC `Pwd=`/`Password=` values with `***`; `FillFromURL` also drops `url.Parse`'s own error, which embeds the URL verbatim.

## Alternatives rejected

- An encrypted config with a master password.
  `hqdb mcp` is a headless stdio server an agent launches in the background; there is no one to ask, and a pinentry dependency would break the fully non-interactive mode.
- Native OS keychain integration (Keychain, DPAPI, Secret Service).
  It needs cgo or per-OS branches against the single Go binary, and the `cmd:` form already reaches each keychain's CLI.
- "Encryption" with a key baked into the binary, the way some GUI clients ship it.
  It is obfuscation, not protection, and it teaches users that the file is safe to commit when it is not.
