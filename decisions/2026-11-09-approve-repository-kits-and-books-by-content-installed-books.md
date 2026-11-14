# Approve repository kits and books by content; installed books are not yet approved

A repository's kits and the books its config turns on are approved by content fingerprint. Books the user turns on
are not reviewed as of this entry; the fence on kits stops a book from rebinding stage's credentials, but a book's secret
commands are still code on the host. Pinning installed books by content is planned. Until then, turn on books only
from protected, reviewed repositories. Evidence: `TestPlanStopsBeforeWritingTheEnvFileWhenApprovalsAreMissing`,
`TestALegacyTrustFileIsClearedRatherThanMigrated`.
