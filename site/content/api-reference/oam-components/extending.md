---
title: "Extending"
weight: 14
---

## Extending

Custom component types implement `oam.ComponentHandler` (`CanHandle` +
`ToApplicationConfig`) and are registered alongside the built-ins. Exported helpers:
`ValidateImageRef` (image policy), `BuildPVC` (PVC from a `PVCConfig`), and the
`persistentvolumeclaim` kind's claim path, `ParseClaimProperties`,
`ApplyClaimPolicy` and `GenerateClaim`, which the `pvc` trait builds through. A custom
`Generate()` that builds standalone PVCs from a `PVCConfig` list should qualify their
names with the component name the way the role kinds do — see `roleClaims`
(unexported, `role_members.go`) — to avoid two components colliding on the same
pod-local volume name.

See [pkg.go.dev](https://pkg.go.dev/github.com/go-kure/launcher/pkg/oam/builtin/components)
for the full type/field reference, the [OAM model](https://pkg.go.dev/github.com/go-kure/launcher/pkg/oam)
for the handler interfaces, and `examples/` for runnable applications.
