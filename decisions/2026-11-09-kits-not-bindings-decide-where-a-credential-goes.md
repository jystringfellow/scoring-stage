# Kits, not bindings, decide where a credential goes

A live probe showed a kit's `credential@1` inject domain decides where a secret is sent, and a binding only
authorises the service. So books and repository configs may use only local kits, which stage checks; a book's kits
may declare only credentials the book supplies, and a repository's none; no book or repository may claim a reserved
service. The user's own config may still name published kits. Evidence: VERIFICATION.md, 2026-11-09 credential
injection entry; `TestBooksCannotNamePublishedKits`, `TestARepositoryKitIsADotPathNeverAPublishedKit`,
`TestABookKitCannotDeclareAReservedCredential`, `TestUserConfigMayStillNamePublishedKits`.
