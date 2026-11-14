# Refuse pushes on ports 443 and 80 only

A bounded deny makes sbx require HTTP inspection, and sbx fails closed on connections it cannot inspect. A push
deny on `**` refused every non-HTTP connection, such as a database on 1433, so it is limited to `**:443` and
`**:80`. SSH or another port to a git host is a push path the proxy does not refuse; that is a documented
limitation. Evidence: VERIFICATION.md, 2026-11-08 to 2026-11-09 entry.
