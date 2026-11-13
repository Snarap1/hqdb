# Masking personal data

A Profile with `mask = true` makes `hqdb mcp` replace personal data with markers before anything reaches the agent.
The terminal UI never masks: its reader is the human who owns the data.
How to switch it on and tune it is in [`config-guide.md`](config-guide.md#8-masking-mcp-only); this page is how it behaves.
The decision and the rejected options are in [ADR-0005](adr/0005-mcp-masking.md).

## What is masked

| tool | masked |
| --- | --- |
| `run_query` | every cell of `rows`; `masked_columns` lists the columns that had a masked value |
| `run_mongo` | every Document, recursively; `masked_fields` lists the dotted paths (array indexes dropped); `distinct` values are masked by the `field` name and `masked_fields` is `[field]` |
| `run_redis` | `KEYS`, `SCAN`, and `RANDOMKEY` replies string by string, so `user:ivan@x.ru` becomes `user:masked-<12 hex>`; every other reply like a Document, with hash field names acting as column names and bare strings checked by the value detectors only; no `masked_fields` |
| `export_query` | every row, before any file format is written |
| `get_ddl` | the definition text (a default or check constraint can hold personal data) |
| errors | every error text of a masked profile, including connection errors, because database errors quote values (`Key (email)=(ivan@x.ru) already exists`) |

`get_schema` and `get_columns` return names and types only and are never masked.
`list_profiles` reports `masked` per profile.
NULL, the empty string, and whitespace-only strings pass through unchanged.

## Markers

A masked value becomes `masked-<12 hex>`: the first 12 hex characters of HMAC-SHA256 over the normalized value.
Equal values get the same marker across columns, tables, and tool calls, so the agent can still join, group, and count distinct values.
Before hashing, a recognized value is normalized (an email to lower case, a phone to E.164, an ID without separators), so differently written equal values share a marker.
The key is random, made once per `hqdb mcp` process, and lives only in its memory.
Markers are therefore stable within one MCP session and never across sessions.
That is deliberate: a key in the environment or on disk could be read by an agent with a shell, which could then brute-force low-entropy values such as phone numbers.

A value in a special category (GDPR Art. 9: health, religion, ethnicity, sexual orientation, biometrics, union membership, blood type) becomes `masked-redacted` with no hash.
A low-cardinality column such as blood type could be decoded from marker frequencies.

## What counts as personal data

A value is masked when its column or field name matches a name rule, or when its content is recognized by a value detector, whatever the column.
Inside free text, a JSON string, or a Document string, each detected span is replaced by its own marker.

### Name rules

Name rules are built in for English, German, French, Polish, Russian, and Ukrainian (Cyrillic and transliterated spellings), in these categories: person names, birth data, contact, address, geo and network, national and tax IDs, financial, secrets, and special categories.
The full list is in `internal/mask/rules.go`.
Names are normalized before matching: lower case, diacritics removed, camelCase split, and every run of other characters turned into `_`, so `firstName`, `First-Name`, and `first name` are all `first_name`.
A rule matches when its words appear as a contiguous run in the name (`first_name` matches `user_first_name`); a few short ambiguous words match only the whole name.
For a Document field, the rule is tried against the last path segment and against the whole dotted path.
A bare `name` column is not a built-in rule, because it would also mask `product.name` and `table_name`; add it in `[masking] columns` when it holds a person's name.

### Value detectors

Value detectors always run, with no region switch:

| region | kinds |
| --- | --- |
| any | email, phone, IPv4, IPv6, MAC address, payment card (Luhn), IBAN (mod 97), JWT, well-known API token prefixes (`sk-`, `ghp_`, `AKIA`, ...) |
| EU | VAT numbers of DE, FR, PL |
| Germany | Steuer-ID, Rentenversicherungsnummer, Personalausweis, Krankenversichertennummer |
| France | NIR |
| Poland | PESEL, NIP, ID card, passport |
| Russia | INN, SNILS, OGRNIP, OMS |
| Kazakhstan, Belarus, Ukraine, Uzbekistan | IIN, personal number, RNOKPP, UNZR, PINFL |

Every ID detector checks the official checksum.
The Uzbek PINFL checksum has no official public description; the detector follows the weights 7, 3, 1 that published validators use.
A Kazakh IIN is detected by value only when it starts with the birth date, as almost every issued IIN does; one that does not is caught by column name only.
A Belarusian personal number is detected with Latin letters or the Cyrillic look-alikes people type for them.
An ID without a checksum, or in a format too common to tell apart from other numbers (Russian passport, German Steuernummer, French CNI and SPI, SIREN, SIRET, REGON, and the Armenian, Georgian, Azerbaijani, and Kyrgyz IDs), has no detector and is caught by column name only.

A phone is detected only when it has a `+` or `00` prefix, or is a national number of DE, FR, UA, RU, KZ, BY, or UZ with its trunk prefix; a bare digit string such as `4915112345678` is never a phone.
A phone stored as a bare integer is therefore caught by its column name only.
In free text, every phone of a list is found, however many phones are joined by spaces or dashes (`8 916 123-45-67 8 916 123-45-68 ...`).

## Statements with markers are refused

On a masked profile, a statement that contains a marker is refused before it runs, whatever the access mode:

```
Refused (masked profile): statement contains a masked value (masked-3f9a1c2b4d5e); masked values are placeholders, not data
```

The screen covers `run_query` and `export_query` SQL, the `run_mongo` JSON arguments, and the `run_redis` command and args.
It ignores letter case, so `MASKED-3F9A1C2B4D5E` and `Masked-Redacted` are refused too: a database with a case-insensitive collation would treat them as the marker.
A marker is a placeholder, so an agent can neither write it back over real data nor query for it.
To look up one person, the human runs the query in the terminal UI.

## Limits

Masking is defense in depth, not a security boundary.
Aliases (`SELECT email AS x`), expressions (`lower(email)`), `row_to_json`, and filter oracles (`count(*) WHERE phone LIKE '49%'`) get around the name rules.
The real boundary is a database login with column privileges, or masking views the profile reads instead of the tables.

Under GDPR a keyed hash is pseudonymisation, not anonymisation: masked output is still personal data in law.
Masking keeps clear values out of the agent's context, the harness logs, and the model provider; it does not make the output anonymous.

## Troubleshooting

| symptom | cause |
| --- | --- |
| `Refused (masked profile): statement contains a masked value ...` | the statement carries a `masked-...` marker copied from an earlier result; markers are placeholders, so use the real value from the terminal UI |
| a personal column comes back in clear | no name rule matches it (bare `name`, an alias, an expression) and no detector recognizes the value; add it to `[masking] columns` |
| a harmless column comes back as `masked-...` | a name rule or a detector matched; add it to `[masking] allow` (detectors still run) |
| markers differ between two MCP sessions | by design; the key lives in memory of one `hqdb mcp` process |
