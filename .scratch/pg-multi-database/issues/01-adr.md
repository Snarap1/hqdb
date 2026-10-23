# 01 - ADR: postgres database discovery

Status: needs-triage

Write `docs/adr/0004-postgres-database-discovery.md` recording the decision in `../spec.md`.
Cover: no `dbname` means discover, `dbname` means narrow; the discovery query and its `CONNECT` filter; access mode per Profile, not per database; why there is no `dbnames` list (can be added later as a filter).
Add any new terms (for example "discovered database") to `GLOSSARY.md`.

## Comments
