# Agents sign in per sandbox; copy_logins is opt-in

Credentials stay on the host wherever the proxy can hold them. agy's sign-in is a token file inside the sandbox, so
by default agy signs in per sandbox. `copy_logins` copies a saved login in, only when the user's own config asks.
Evidence: `TestLoginsAreLeftAloneUnlessCopyLoginsAsksForThem`.
