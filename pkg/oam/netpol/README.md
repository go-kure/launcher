# NetworkPolicy Synthesis Types

[![Go Reference](https://pkg.go.dev/badge/github.com/go-kure/launcher/pkg/oam/netpol.svg)](https://pkg.go.dev/github.com/go-kure/launcher/pkg/oam/netpol)

Package `netpol` contains shared types for automatic `NetworkPolicy` synthesis —
primarily `TrafficSource`. It lives outside `pkg/oam` so that both the synthesis
stage (`pkg/oam`) and the routing trait configs that expose collected sources
(`pkg/oam/builtin/traits`) can import it without an import cycle.

Internal support type, not a user-facing API. See
[pkg.go.dev](https://pkg.go.dev/github.com/go-kure/launcher/pkg/oam/netpol).
