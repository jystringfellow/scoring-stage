# GitHub access is a required choice

`[github] mode` is fine-grained, app, gh or none, with no default, because each trades access against safety
differently. gh mode is allowed with a warning, since GraphQL lets it write. stage ships no GitHub App; users
register their own. Under managed settings, app and gh are refused unless `allowed_modes` lists them. Evidence:
`TestUpRefusesUntilAGitHubModeIsChosen`, `TestManagedSettingsWithoutAllowedModesRefuseOnlyGhMode` (gh),
`TestManagedSettingsRefuseAppModeUntilAllowedModesListsIt` (app).
