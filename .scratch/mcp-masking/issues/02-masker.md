# 02 - Masker: internal/mask

Status: done

Implement `internal/mask` as specified in `../spec.md`, sections "Name rules" and "Masker".
Build against the fixed `internal/mask/detect` API; ticket 01 fills it in parallel, so in tests that need a detected value use one that ticket 01 will certainly detect (an email such as `ivan@example.com`), and keep detector-specific behavior out of this package's tests.
Cover: name normalization (camelCase, diacritics, Cyrillic, separators), token and exact rules, user `columns`/`allow` globs and `[masking]` validation messages, special categories, NULL/empty passthrough, deterministic markers under a fixed key, cross-column linking, non-string values, JSON-in-string, nested Documents with dotted paths, `Text`, `MarkerViolation`.

## Comments

Implemented in `internal/mask` (`mask.go`, `names.go`, `rules.go`, `marker.go`, `mask_test.go`).
- The spec's rule lists are used unchanged (the duplicate `adres` in Address collapses harmlessly).
- Note: `firstname_x` DOES match the `firstname` rule (token run); only `first_name` does not match it.
- Doc rules: token rules are tried against the last segment and the `_`-joined path; globs against the last segment and the dotted path of normalized segments (e.g. `contacts.phones.number`).
- Value detection also treats whole-number floats as integers (JSON numbers decode to float64).
- Latin diacritics are stripped only on Latin letters, so Cyrillic `й`/`ї` keep their letters.
- `golang.org/x/text` is now a direct import; its go.mod line still carries `// indirect` (no `go mod tidy` run, to avoid racing the detect work).
- `TestDetectDependentEmail` needs a real `detect` implementation and fails while detect is a stub.
