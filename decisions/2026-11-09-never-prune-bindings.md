# Never prune bindings

`sbx env rm --prune-bindings` removes a service's whole shared binding for every sandbox on the machine. `stage
down` never passes it. Evidence: VERIFICATION.md, 2026-11-09 credential injection entry;
`TestStageNeverPrunesTheSharedBindings`.
