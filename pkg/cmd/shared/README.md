# Shared CLI Builders

[![Go Reference](https://pkg.go.dev/badge/github.com/go-kure/launcher/pkg/cmd/shared.svg)](https://pkg.go.dev/github.com/go-kure/launcher/pkg/cmd/shared)

Package `shared` provides reusable Cobra/Viper building blocks for the `kurel` CLI:
`InitConfig` (Viper config initialization), `NewCompletionCommand` (shell completion),
and `NewVersionCommand` (version output). These are wired together by
[`pkg/cmd/kurel`](../kurel); user-facing usage is documented in the
[kurel CLI reference](../kurel).

Internal CLI support, not a standalone API. See
[pkg.go.dev](https://pkg.go.dev/github.com/go-kure/launcher/pkg/cmd/shared).
