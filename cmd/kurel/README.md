# kurel (binary entrypoint)

Package `main` is the `kurel` binary entrypoint; it simply calls
`pkg/cmd/kurel.Execute()`. There is no API surface here.

User-facing CLI usage is documented in the
[kurel CLI reference](../../pkg/cmd/kurel).
