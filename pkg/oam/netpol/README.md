# NetworkPolicy Synthesis Types

[![Go Reference](https://pkg.go.dev/badge/github.com/go-kure/launcher/pkg/oam/netpol.svg)](https://pkg.go.dev/github.com/go-kure/launcher/pkg/oam/netpol)

Package `netpol` contains shared types for automatic `NetworkPolicy` synthesis:

- `TrafficSource` — one allowed **inbound** source, collected from routing traits'
  platform-reserved `networkPolicy.trafficSources`. It lives outside `pkg/oam` so that
  both the synthesis stage (`pkg/oam`) and the routing trait configs that expose the
  collected sources (`pkg/oam/builtin/traits`) can import it without an import cycle.
- `EgressPeer` — one allowed **egress** destination (namespace selector + optional pod
  selector + ports). Unlike `TrafficSource`, it is a crane-supplied, non-authorable
  synthesis input carried on `TransformContext.EgressPeers` (never set from OAM YAML or
  capability rendering); it has no trait-side producer.

Internal support type, not a user-facing API. See
[pkg.go.dev](https://pkg.go.dev/github.com/go-kure/launcher/pkg/oam/netpol).
