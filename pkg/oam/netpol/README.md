# NetworkPolicy Synthesis Types

[![Go Reference](https://pkg.go.dev/badge/github.com/go-kure/launcher/pkg/oam/netpol.svg)](https://pkg.go.dev/github.com/go-kure/launcher/pkg/oam/netpol)

Package `netpol` contains shared types for automatic `NetworkPolicy` synthesis:

- `TrafficSource` — one allowed **inbound** source, collected from routing traits'
  platform-reserved `networkPolicy.trafficSources`. It lives outside `pkg/oam` so that
  both the synthesis stage (`pkg/oam`) and the routing trait configs that expose the
  collected sources (`pkg/oam/builtin/traits`) can import it without an import cycle.
- `EgressPeer` — one allowed **egress** destination (namespace selector + optional pod
  selector + ports). Unlike `TrafficSource`, it is a downstream-supplied, non-authorable
  synthesis input carried on `TransformContext.EgressPeers` (never set from OAM YAML or
  capability rendering); it has no trait-side producer.
- `Endpoint` — a component's declared in-cluster data-plane endpoint: a pod selector
  (matchLabels only) + ports. Namespace is deliberately absent (the caller knows the target's
  namespace). Declared by a component handler via the optional `oam.EndpointProvider`.
- `IngressPeer` — one **target-side** allow (an `Endpoint` + the `Sources` allowed to reach
  it), carried on `TransformContext.IngressPeers`; platform-supplied and non-authorable.
  **Fail-closed contract:** unlike `TrafficSource` (where a nil pod selector means "all pods"),
  each `IngressPeer.Source` MUST carry a namespace and a non-empty matchLabels pod selector —
  a namespace-wide source is dropped. Both endpoint and source selectors are matchLabels-only.

Internal support type, not a user-facing API. See
[pkg.go.dev](https://pkg.go.dev/github.com/go-kure/launcher/pkg/oam/netpol).
