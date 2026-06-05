# Global CLI Options

[![Go Reference](https://pkg.go.dev/badge/github.com/go-kure/launcher/pkg/cmd/shared/options.svg)](https://pkg.go.dev/github.com/go-kure/launcher/pkg/cmd/shared/options)

Package `options` defines `GlobalOptions` — the global flags shared by all `kurel`
commands (config file, verbose/debug/strict, output format/file, namespace, dry-run)
— along with `AddFlags`, `Complete` (Viper binding), and `Validate`.

Internal CLI support, not a standalone API. The flags are documented for users in the
[kurel CLI reference](../../kurel). See
[pkg.go.dev](https://pkg.go.dev/github.com/go-kure/launcher/pkg/cmd/shared/options).
