# hqdb setup and platform credential stores

ADR-0008 added the reference forms a Profile can read a secret through, but moving an existing plaintext config onto them was manual work: create the store entry, edit the file, get both right.
`hqdb setup` does that move: it finds the literal credentials, offers a store, proves the store works, and rewrites the config line.
The platform's own store is offered first, written through the platform's CLI, because that is where a person already keeps credentials and where the OS keeps them locked; where a platform has none, the rest of the list is all the wizard shows.

## What the wizard does

For every literal credential - the `password` key, a password inside a URL-form `conn_str`, a `Pwd=`/`Password=` value in an ODBC conn_str - the wizard offers the stores below, stores the secret, verifies the store reads it back, and only then patches the config to the store's reference form.
The wizard never renders a secret, and a store that does not read the value back never gets its reference into the config, so the config is never stranded pointing at a store that did not take the value.

## The stores

- Linux: the Secret Service (GNOME Keyring, KWallet), written with `secret-tool store`, secret over stdin, read back with `secret-tool lookup`.
- macOS: the Keychain, written with the built-in `security add-generic-password`.
- Linux and macOS: `pass` (`pass insert -m`, secret over stdin) and an environment variable file (`$XDG_CONFIG_HOME/hqdb/env.sh`, mode 0600), because hqdb cannot export a variable into the shell that launched it.
- Windows: none, neither the platform store, nor the env file, nor `pass`; see below.

## Why Windows gets no store

Windows ships no CLI that returns a stored password: `cmdkey` writes and lists, `secret-tool` and `security` both read back, and nothing on Windows reads.
The first draft closed that gap by reading the store itself: `hqdb credential get` would print the password of a `cmdkey /generic:` credential through a `CredReadW` syscall, and the config would reference it as `${cmd:hqdb credential get hqdb/<profile>}`.
That is gone.
A binary that pulls a stored password out of the OS credential store and prints it to standard output is indistinguishable, to an antivirus scanner, a store reviewer, and an endpoint agent, from an infostealer doing exactly that, and a database client that gets flagged or quarantined on Windows is worse than one that leaves the move manual there.
So Windows is offered nothing hqdb writes into, and the wizard says so instead of opening with no store: every secret there is referenced by hand, `${cmd:...}` for a manager whose CLI is a native `.exe` (1Password, gopass, KeePassXC, Vault), the guide says how.
The env file is left out there for a smaller reason: it exists so a shell can source it, the file is written in POSIX shell syntax, and Windows sources nothing of the kind, so moving the secret there would only bury it in a second plaintext file that the config no longer points at.
`pass` is out as well, and not just as a store: it is a bash script, and a `${cmd:...}` on Windows goes through `cmd /C`, which runs only a native program or a `.bat`/`.cmd`, so the reference would not resolve. gopass is the same idea with a native binary.
The same reasoning bans the `cmdkey /pass` write the first draft also had: the password would travel on a command line.

## The caveats the wizard cannot remove

A store whose CLI takes the password on its command line - `security add-generic-password -w`, the one such store left - shows it to other processes of the same user while the command runs; the stores that take the secret over stdin (`secret-tool`, `pass`, the env file) do not.
An OS keyring also needs its session unlocked, so over SSH without a desktop `secret-tool` and `security` fail; the probe says so instead of writing a reference that cannot be read.

## Patching the config

The config file is rewritten surgically, not remarshalled: only the one line carrying the credential changes, so comments, ordering, and every other value stay exactly as the user wrote them.
The result is verified before the write - the file must parse, the Profile must carry the reference where the credential lived, the old literal must be gone, and every other value must be unchanged - and the write is atomic (temp file in the same directory, then rename).
A line the rewriter cannot prove - a multiline or escaped string, a value that is no longer what `Find` saw - is refused with `edit it by hand` rather than guessed at.
A Profile is patched in the file that contributes it under the merge order, and a `pyproject.toml` profile is patched inside its `[tool.hqdb.profiles.<name>]` section.

`hqdb check` points at the wizard instead of failing: a Profile with a literal credential reports `ok  literal credentials; run hqdb setup`, because a plaintext config is valid, just not where the user wants it.
