# Tests are the spec; live checks go in VERIFICATION.md

Behavior lives in tests, reasons in commits and PRs, and only the choices no test can fail on in `decisions/`. `go test` runs against
fakes, so what has been checked against a live sbx or GitHub is recorded separately, as dated entries that are never
rewritten.
