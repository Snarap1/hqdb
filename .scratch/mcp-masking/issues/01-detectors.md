# 01 - Value detectors: internal/mask/detect

Status: done

Implement `internal/mask/detect` as specified in `../spec.md`, section "Value detectors".
The exported API in `internal/mask/detect/detect.go` is fixed (another ticket builds against it in parallel); fill in the bodies, do not change the signatures.
Add `github.com/nyaruka/phonenumbers` for phones.
Every detector: verify the checksum against the official algorithm, table-driven tests with valid numbers and one-digit-off numbers, `Match`, `Find` (word-bounded, inside free text), and `Normalize`.
Test false positives too: plain integers, dates, UUIDs, order numbers, prices, and timestamps that must not match.

## Comments

Implemented in `internal/mask/detect` (detect.go registry and Find, generic.go, phone.go, ids.go, tests).

- Priority (first wins for identical text; in Find overlapping candidates are resolved by length first, then priority): email, jwt, token, iban, eu_vat, ipv6, ipv4, mac, phone, card, de_rvnr, de_id_card, de_kvnr, fr_nir, by_personal_number, pl_id_card, pl_passport, ua_unzr, ru_ogrnip, ru_oms, uz_pinfl, ru_inn, kz_iin, ru_snils, pl_pesel, de_steuer_id, pl_nip, ua_rnokpp.
- Phone policy: `+` or `00` numbers valid per libphonenumber (8 to 15 digits); national numbers only for DE, FR, UA (trunk 0) and RU, KZ, BY, UZ (trunk 8), at least 9 digits; digits with no separator must be mobile; PL national only as NNN NNN NNN or NN NNN NN NN; bare digit strings without prefix (4915112345678) are never phones, so a phone stored as an integer needs its `+` added by the caller to be found. Region ambiguity (06 12 34 56 78) is resolved by grouping (FR, UA first).
- Card: 13 to 19 digits, Luhn, first digit 3 to 6 or 22 to 27, not all one digit. A Luhn-valid 13-digit number starting 3 to 6 is a card (about 10 percent of such numbers).
- Verified against published examples: DE Steuer-ID 65929970489, RVNR 15070649C103, KVNR A123456780, Personalausweis L01X00T47+1, FR NIR 185057800608491, PESEL 44051401359, NIP 1234563218, PL ID ABA300000, INN 7707083893 and 500100732259, SNILS 11223344595, OGRNIP 304500116000157, IBANs, DE/FR VAT.
- NOT verified against an official source (algorithm from memory, tests only prove self-consistency): ru_oms (Luhn assumed, and Card shadows it for first digits 2 to 6), uz_pinfl (7-3-1 mod 10 over 13 digits), ua_unzr (7-3-1 mod 10 over 12 digits), by_personal_number (7-3-1 mod 10 over 13 chars), pl_passport (weights 7 3 9 1 7 3 1 7 3, sum mod 10 == 0), kz_iin (two-pass 1..11 / 3..11,1,2), ua_rnokpp (first five digits capped at 55000), fr_nir month/department plausibility ranges.
- Follow-up verification of the unverified list (by the main session):
  - pl_passport: confirmed by pl.wikipedia "Paszporty w Polsce" (weights 7 3 9 1 7 3 1 7 3, example CC7999486, now a test case).
  - ru_oms: FOMS order No. 276 prescribes ISO/HL7 27931 Mod10, which is equivalent to Luhn; `TestOMSIsHL7Mod10` implements the standard's wording and checks the equivalence.
  - kz_iin: weights confirmed by the 2023 IIN rules (order No. 32942); the rules no longer promise a birth date, the detector still requires one to keep 12-digit codes out, documented.
  - by_personal_number: algorithm confirmed by MVD resolution No. 345 (mod 10, weights 731..., A..Z = 10..35) and a real published number; fixed: numbers issued since 2012 start with 7 and carry no birth date, so the first digit is 1 to 9 and the date is not checked; Cyrillic look-alike letters are accepted and normalized to Latin.
  - ua_rnokpp: confirmed by python-stdnum ua.rntrc (example 1759013776, which also passes the RU INN-10 checksum and so reports ru_inn).
  - ua_unzr: MRZ method confirmed by uk.wikipedia; no worked example published.
  - uz_pinfl: no official description; agrees with the uz_kit validator example 30101901234569.
  - fr_nir: first digits 3 and 4 added.
- FR VAT keys with letters are not supported (numeric key only). ID kinds with letters match upper case only.
