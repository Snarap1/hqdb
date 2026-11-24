# 01: internal/secrets package

Status: done

`internal/secrets`: `Resolve(ctx, p)` returns a copy of the profile table with references expanded (strings and list elements; non-strings untouched; input never mutated).
`Check(p)` validates references without running commands: env presence, non-empty bodies.
Contract error wording, table-driven tests: env unset, empty env name, empty command, command failure with a stderr hint, timeout.
A per-process cache of command outputs with a 5-minute TTL and a test-replaceable clock.
