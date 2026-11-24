# 04: hqdb check validates references

Status: done

`check.checkProfile` runs `secrets.Check(p)` after `config.Validate`, so `--no-probe` reports a `${env:MISSING}` password without touching a secret store, and a `${cmd:...}` body is checked for emptiness but never executed.
Probe mode resolves for real through `adapters.Open`, so a broken command fails the probe line.
