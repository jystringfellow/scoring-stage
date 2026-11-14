# Create sandboxes unattended; report sbx's bindings, do not gate on them

`sbx env create --force --no-input` keeps unattended runs possible; stage asks its own questions (repository
approvals, ship review) instead of sbx's prompts. sbx's `credentials.yaml` is sbx's and undocumented, so `stage
setup` and `stage up --plan` only report what it holds and how to remove it. A parse failure is a warning.
Evidence: `TestSetupStillAuditsBindingsWhenGlobalSecretsAreUnreadable`, `TestUpPlanAuditsGlobalBindingsEvenWhenThePlanFails`.
