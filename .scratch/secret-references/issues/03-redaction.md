# 03: no secrets in error messages

Status: done

`config.RedactConnStr` replaces URL credentials (`://` up to the next `@`) and ODBC password values (`Pwd=` / `Password=`) with `***`.
`FillFromURL` parse errors name the conn_str redacted (and drop `url.Parse`'s own error, which embeds the URL verbatim).
`parseODBC`'s "has no Server" error uses the same redaction.
Tests assert the password never appears in either message.
