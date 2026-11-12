# 04 - Docs

Status: done

03 is done.
Implement `../spec.md` section "Docs": `docs/config-guide.md` Masking section, `README.md`, `.claude/skills/hqdb-config/`, `AGENTS.md` (architecture and dependency line).

## Comments

- Docs written from the code: `docs/config-guide.md` section 8 (plus top-level keys, `mask` in the valid-option example, `hqdb check` `[masking]` line, troubleshooting rows), `README.md` Masking section, `.claude/skills/hqdb-config/SKILL.md` Masking section, `AGENTS.md` dependency line, server masking notes, and three rules-that-look-like-bugs lines.
- Phone policy is described as implemented (01 comment): a bare digit string is never a phone, so a phone stored as an integer is caught by name only; this is stricter than the spec's "integer is checked via its decimal string".
- `list_profiles` `masked`, `masked_columns`, `masked_fields` (mongo only, none for redis) documented as implemented.
