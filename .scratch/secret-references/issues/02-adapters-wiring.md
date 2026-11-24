# 02: resolve at the adapters entries

Status: done

`adapters.Open`, `OpenDatabase`, `OpenReusable`, and `Databases` validate and open a resolved copy of the Profile (resolve, then validate, so an expanded `access` is still checked).
Internal paths (`openAdapter`, `openPostgresIn`, `connectReusablePostgres`) take the resolved table and never resolve again.
The MCP pool and `list_profiles` keep seeing the raw table.
