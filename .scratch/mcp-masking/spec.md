# MCP masking: personal data never reaches the agent in clear

Status: ready-for-agent

## Problem

`hqdb mcp` hands query results, documents, Redis values, export files, and database error texts to an AI agent verbatim.
On a production Profile that sends personal data (names, emails, phones, national ID numbers) into the agent's context, the harness logs, and the model provider.
The user works across the EU (Germany, France, Poland) and the CIS, so personal data means more than Russian documents.

## Decision summary

- A Profile opts in with `mask = true`; the default is `false`.
- Masking applies to the MCP server only; the terminal UI is for a human and stays unmasked.
- A masked value becomes `masked-<12 lowercase hex>`: the first 12 hex characters of HMAC-SHA256 over the normalized value, keyed by a random key that lives only in the memory of the `hqdb mcp` process.
  Equal values get equal markers within one MCP session (across columns, tables, and tool calls), so the agent can still join, group, and count distinct values.
  Markers are not stable across sessions, by design: the key is never on disk or in the environment, where an agent with a shell could read it.
- GDPR Art. 9 special categories (health, religion, ethnicity, sexual orientation, biometrics, union membership, blood type) become `masked-redacted`, with no hash, because a low-cardinality column can be decoded from marker frequencies.
- NULL, the empty string, and whitespace-only strings pass through unchanged.
- A value is masked when its column name matches a name rule, or when the value itself is recognized by a value detector, whatever its column.
- Any statement on a masked Profile that contains a marker is refused, whatever the Access mode: `Refused (masked profile): statement contains a masked value (masked-3f9a1c2b4d5e); masked values are placeholders, not data`.
- Masking is defense in depth, not a security boundary: aliases, expressions (`lower(email)`), `row_to_json`, and filter oracles (`count(*) WHERE phone LIKE '49%'`) get around it.
  The real boundary is a database login with column privileges or masking views.
- Under GDPR a keyed hash is pseudonymisation, not anonymisation; the guide must say so.

## Config

```toml
[profiles.prod]
adapter = "postgres"
mask = true            # every adapter accepts it; default false

[masking]              # optional, top level; applies to every masked Profile
columns = ["name", "nickname"]        # extra column rules (globs), added to the built-in rules
allow   = ["product_name", "*_verified"]  # globs that are never masked by name; allow wins over columns and built-ins
```

- `mask` is read with `config.OptBool` through a new `config.ProfileMask(p) (bool, error)`, and `mask` joins every adapter's key list in `adapterKeys`.
- `masking` becomes a valid top-level key in `config.Load`, stored raw as `Config.Masking map[string]any`.
  Merge stays top-level replacement: a project `[masking]` replaces the global one whole.
- `[masking]` accepts only `columns` and `allow`, each an array of strings; an unknown key is rejected with the valid-key list, like adapter keys.
  Each entry is a glob (`path.Match` syntax) over the normalized column name; a malformed glob is an error naming it.
- There is no strategy key and no region key: every detector always runs.

## Name rules

Column (and Document field) names are normalized before matching: Unicode lower case, diacritics removed (`prénom` → `prenom`, `straße` → `strasse`, `ł` → `l`), camelCase split, and every run of non-letter-non-digit characters turned into `_`, with leading and trailing `_` trimmed.
So `firstName`, `First-Name`, and `first name` all become `first_name`.
The normalized name is then split into tokens on `_`.

Built-in rules are token sequences: a rule matches when its tokens appear as a contiguous run of the name's tokens (`first_name` matches `user_first_name` and `firstName`, not `firstname_x` unless written as `firstname`).
Rules marked exact match only the whole normalized name (single letters `f`, `i`, `o` and short ambiguous words).
User `columns` and `allow` entries are globs over the whole normalized name.
For Documents, a rule is tried against the last path segment and against the whole dotted path (array indexes dropped, `contacts.phones.number`).

Bare `name` is deliberately not a built-in rule (it would mask `product.name`, `table_name`); the guide tells users to add it to `columns` when their `users.name` holds a person's name.

Built-in rule set (token rules unless marked exact):

- Names: `first_name`, `firstname`, `given_name`, `last_name`, `lastname`, `family_name`, `surname`, `middle_name`, `maiden_name`, `full_name`, `fullname`, `patronymic`, `vorname`, `nachname`, `familienname`, `geburtsname`, `prenom`, `nom_famille`, `nom_de_famille`, `imie`, `nazwisko`, `imya`, `familiya`, `otchestvo`, `fio`, `pib`, `prizvyshche`, `imia`, `po_batkovi`, `имя`, `фамилия`, `отчество`, `фио`, `ім_я`, `прізвище`, `по_батькові`, `піб`; exact: `f`, `i`, `o`, `nom`.
- Birth: `birth`, `birthday`, `birthdate`, `dob`, `date_of_birth`, `birth_place`, `geburtsdatum`, `geburtstag`, `geburtsort`, `naissance`, `date_naissance`, `lieu_naissance`, `urodzenia`, `data_urodzenia`, `miejsce_urodzenia`, `rozhdeniya`, `data_rozhdeniya`, `mesto_rozhdeniya`, `дата_рождения`, `место_рождения`.
- Contact: `email`, `e_mail`, `mail`, `phone`, `telephone`, `mobile`, `cell`, `msisdn`, `fax`, `telefon`, `handy`, `mobil`, `portable`, `komorka`, `телефон`; exact: `tel`.
- Address: `address`, `street`, `zip`, `zipcode`, `postcode`, `postal_code`, `adresse`, `anschrift`, `strasse`, `hausnummer`, `plz`, `code_postal`, `rue`, `adres`, `ulica`, `kod_pocztowy`, `ulitsa`, `adres`, `адрес`, `улица`; exact: `ort`, `wohnort`, `indeks`.
- Geo and network: `latitude`, `longitude`, `geo`, `geolocation`, `coordinates`, `ip`, `ip_address`, `ipv4`, `ipv6`, `mac_address`, `user_agent`; exact: `lat`, `lon`, `lng`.
- National and tax IDs: `ssn`, `tax_id`, `taxid`, `national_id`, `passport`, `id_card`, `driver_license`, `driving_licence`, `steuer_id`, `steuerid`, `steuernummer`, `sv_nummer`, `rvnr`, `versicherungsnummer`, `ausweis`, `personalausweis`, `reisepass`, `nir`, `numero_secu`, `securite_sociale`, `numero_fiscal`, `spi`, `siren`, `siret`, `cni`, `pesel`, `nip`, `regon`, `dowod`, `dowod_osobisty`, `paszport`, `inn`, `snils`, `ogrnip`, `oms`, `polis`, `pasport`, `паспорт`, `инн`, `снилс`, `iin`, `иин`, `rnokpp`, `ipn`, `unzr`, `pinfl`, `jshshir`, `personal_number`, `fin`.
- Financial: `iban`, `card_number`, `cardnumber`, `pan`, `cvv`, `cvc`, `account_number`, `bank_account`, `kontonummer`, `numer_konta`, `rib`.
- Secrets: `password`, `passwd`, `pwd`, `secret`, `token`, `api_key`, `apikey`, `private_key`, `otp`, `pin`, `pin_code`.
- Special categories (redact, not hash): `diagnosis`, `diagnose`, `diagnostic`, `diagnoza`, `diagnoz`, `health`, `medical`, `disease`, `illness`, `krankheit`, `maladie`, `choroba`, `religion`, `konfession`, `wyznanie`, `religiya`, `ethnicity`, `ethnic`, `race`, `nationality_ethnic`, `sexual_orientation`, `orientation`, `biometric`, `biometrics`, `fingerprint`, `face_id`, `blood_type`, `blutgruppe`, `groupe_sanguin`, `grupa_krwi`, `union_member`, `trade_union`, `gewerkschaft`, `syndicat`.

These lists are the starting set; the implementer may correct obvious mistakes but must keep every category and every language.

## Value detectors (`internal/mask/detect`)

Every detector always runs.
Strong detectors (structure plus checksum, or a strict structure) match a whole value and find spans in free text.
IDs with no checksum or with a format too common to tell apart (Russian passport, German Steuernummer, French CNI and SPI, SIREN/SIRET, REGON, Kyrgyz PIN, Armenian, Georgian, Azerbaijani IDs, postal codes) have no value detector; only name rules catch them.

| Kind | Rule |
|---|---|
| `email` | RFC-ish local@domain.tld |
| `phone` | valid number per `github.com/nyaruka/phonenumbers`: E.164 with `+` for any region, and national formats for DE, FR, PL, RU, KZ, UA, BY, UZ; at least 8 digits |
| `ipv4`, `ipv6` | `net/netip` parse; free-text spans word-bounded |
| `mac` | six hex octets separated by `:` or `-` |
| `card` | 13 to 19 digits (spaces or dashes allowed between groups), Luhn |
| `iban` | country code, 2 check digits, BBAN, mod 97 == 1, country length table |
| `jwt` | three base64url segments, first decodes to JSON with `alg` |
| `token` | well-known prefixes: `sk-`, `sk_live_`, `ghp_`, `gho_`, `github_pat_`, `xox[bpas]-`, `AKIA[0-9A-Z]{16}`, `AIza` |
| `eu_vat` | country prefix plus that country's VAT checksum, for DE, FR, PL |
| `de_steuer_id` | 11 digits, first not 0, ISO 7064 mod 11,10, digit-repetition rule |
| `de_rvnr` | 2 digits, DDMMYY, a letter, 2 digits, check digit |
| `de_id_card` | Personalausweis: 9 characters from `[CFGHJKLMNPRTVWXYZ0-9]` plus ICAO 7-3-1 check digit |
| `de_kvnr` | Krankenversichertennummer: a letter, 9 digits, check digit |
| `fr_nir` | 13 characters (Corsica `2A`/`2B`), plus 2-digit key = 97 - (n mod 97) |
| `pl_pesel` | 11 digits, weights 1-3-7-9, valid birth date with the century encoded in the month |
| `pl_nip` | 10 digits, weights 6-5-7-2-3-4-5-6-7, mod 11 |
| `pl_id_card` | dowód osobisty: 3 letters, 6 digits, check digit with weights 7-3-1 |
| `pl_passport` | 2 letters, 7 digits, check digit |
| `ru_inn` | 10 or 12 digits with the FNS check digits |
| `ru_snils` | 11 digits (optionally `NNN-NNN-NNN NN`), SNILS checksum |
| `ru_ogrnip` | 15 digits, mod 13 check digit |
| `ru_oms` | 16 digits, OMS check digit |
| `kz_iin` | 12 digits, valid birth date, two-pass weighted checksum |
| `by_personal_number` | 14 characters `[1-6]DDMMYY[A-Z]NNN[A-Z]{2}N`, valid date |
| `ua_rnokpp` | 10 digits, weighted checksum |
| `ua_unzr` | `YYYYMMDD-NNNNN`, valid date, check digit |
| `uz_pinfl` | 14 digits, check digit with weights 7-3-1 |

Every checksum must be verified against the official algorithm description while implementing; each detector gets table-driven tests with known-valid numbers (from official examples or computed) and with the same numbers one digit off.

When several kinds match the same text, a fixed priority order picks one (structured kinds before bare-digit kinds; longer before shorter); the kind only affects normalization.

API, fixed so `internal/mask` can be built against it in parallel (see `internal/mask/detect/detect.go`):

```go
type Kind string
type Span struct { Start, End int; Kind Kind } // byte offsets, End exclusive
func Match(s string) (Kind, bool)        // the whole trimmed value is one detected value
func Find(text string) []Span            // sorted, non-overlapping, word-bounded spans
func Normalize(k Kind, s string) string  // canonical form used before hashing
```

Normalization: `email` lower case and trimmed; `phone` E.164; `ipv4`/`ipv6` canonical `netip` form; `mac` lower case with `:`; every ID kind upper case with spaces, dashes, dots, and slashes removed.

## Masker (`internal/mask`)

```go
func NewKey() []byte                                    // 32 random bytes; the server calls it once per process
func New(masking map[string]any, key []byte) (*Masker, error) // validates [masking]; nil map means built-ins only
func (m *Masker) Value(column string, v any) (any, bool) // masked value and whether it changed
func (m *Masker) Row(columns []string, row []any) []any // for the export hook
func (m *Masker) Rows(columns []string, rows [][]any) []string // masks in place, returns the masked column names in column order
func (m *Masker) Doc(v any) (any, []string)              // recursive over values.Doc, map[string]any, []any; returns masked dotted paths, sorted, deduplicated
func (m *Masker) Text(s string) string                   // replaces every detected span with its marker
func MarkerViolation(text string) string                 // "" or "statement contains a masked value (<marker>); masked values are placeholders, not data"
```

Value rules, in order:
1. NULL, empty, and whitespace-only strings: unchanged.
2. Column matched by `allow`: skip the name rules, still run value detectors.
3. Column matched by a special-category rule: `masked-redacted`.
4. Column matched by any other name rule: the whole value becomes a marker.
   The hashed text is `detect.Normalize(kind, s)` when `detect.Match` recognizes the value, otherwise the value's string form trimmed, NFC, case-folded.
   Non-strings hash their canonical string form (integers in decimal, `time.Time` as RFC 3339, others with `fmt.Sprint`); a nested object or array under a name rule is masked whole.
5. Otherwise a string that `detect.Match` recognizes whole becomes a marker; an integer is checked via its decimal string (a phone or INN stored as bigint).
6. Otherwise a string that looks like a JSON object or array (`values.DecodeOrdered` succeeds) is masked as a Document and re-encoded compactly.
7. Otherwise every `detect.Find` span inside the string is replaced by its own marker.

The marker is `masked-` plus the first 12 hex characters of HMAC-SHA256(key, hashed text); the hash input carries no column or kind, so equal values link across columns.

## MCP server

- `Serve` creates the key once with `mask.NewKey()`; every tool call builds its Masker from that key and `cfg.Masking` (fresh per call, because config is reloaded per call).
- `openProfile` also returns whether the Profile is masked and its Masker (nil when unmasked).
- `list_profiles`: each `ProfileInfo` gains `masked bool` (`json:"masked"`).
- `run_query`: rows through `Rows`; `RunQueryOut` gains `masked_columns` (omitempty).
- `run_mongo`: every Document through `Doc`, `distinct` values through `Value(in.Field, v)`; `RunMongoOut` gains `masked_fields` (omitempty).
- `run_redis`: the result through `Doc` (hash replies become field/value objects there already, or are handled as such); bare strings through the value rules with an empty column name; keys in `KEYS`/`SCAN` replies through `Text`.
- `export_query`: `export.Query` gains a final parameter `transform func(columns []string, row []any) []any` (nil means unchanged), applied once to every row before any format writer; the server passes `m.Row` on a masked Profile, the terminal UI passes nil.
- `get_ddl`: output through `Text`.
- `get_schema`, `get_columns`: unchanged.
- Every handler error on a masked Profile passes through `Text` before it is returned (database errors quote values: `Key (email)=(ivan@x.ru) already exists`).
- Marker screen: on a masked Profile, before running, every tool that takes statement text (`run_query`, `export_query` SQL; `run_mongo` `filter`, `projection`, `sort`, `pipeline`, `document`, `documents`, `update`; `run_redis` command and args) is checked with `mask.MarkerViolation`; a violation is refused with `Refused (masked profile): <reason>`, before the access screen, whatever the Access mode.
- Tool descriptions say that on a masked Profile values may come back as `masked-…` markers, equal values share a marker within the session, and markers cannot be used in statements.

## `hqdb check`

Validates `mask` per Profile and `[masking]` by calling `mask.New`, reporting errors in the existing check style.

## Docs

`docs/config-guide.md` gets a Masking section (enable, what is masked, marker format and session scope, special categories, `columns`/`allow`, bare `name`, the marker refusal, defense-in-depth limits, GDPR pseudonymisation note).
`README.md` and `.claude/skills/hqdb-config/` stay in sync; `AGENTS.md` dependency line gains `mask`.
