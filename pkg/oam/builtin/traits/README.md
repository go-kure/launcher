# OAM Built-in Trait Handlers

[![Go Reference](https://pkg.go.dev/badge/github.com/go-kure/launcher/pkg/oam/builtin/traits.svg)](https://pkg.go.dev/github.com/go-kure/launcher/pkg/oam/builtin/traits)

Package `traits` implements `oam.TraitHandler` for the built-in trait types. A trait
decorates or augments a component — adding networking, security, storage, scaling,
or operational behavior. Handlers are registered with the transformer in
`pkg/cmd/kurel` via `RegisterBuiltinTrait(type, handler)`; each implements
`CanHandle` + `Apply`. Some traits are **capability-aware** (`CapabilityRequired`)
and draw platform choices (issuer, gateway, secret store) from the `ClusterProfile`.
Handlers may also implement `oam.PropertySchemaProvider` (`PropertySchema()`) to declare a
constrained schema for their properties so crane can validate them before invocation — the
`configmap` handler is a worked example.

## Trait catalog

### Networking
| `type` | Produces | Key properties |
|--------|----------|----------------|
| `ingress` | Ingress | `rules[]` (`host`, `paths[]`), `ingressClassName`, `tls[]`, `annotations` |
| `httproute` | Gateway API HTTPRoute | `parentRefs[]`, `rules[]` (`matches`/`backendRefs`/`filters`/`timeouts`), `hostnames[]` |
| `expose` | Ingress **or** HTTPRoute | `rules[]`, `hostnames[]` — controller chosen by ClusterProfile (`controllerType`) |
| `networkpolicy` | NetworkPolicy | `ingress[]`/`egress[]` (`from`/`to`, `ports`) |
| `cilium-networkpolicy` | CiliumNetworkPolicy | `name`, `endpointSelector`, `ingress`/`egress` (raw Cilium rules) |

### Security
| `type` | Produces | Key properties |
|--------|----------|----------------|
| `certificate` | cert-manager Certificate | `secretName`, `dnsNames[]`, `duration`, `renewBefore` (issuer from ClusterProfile) |
| `rbac` | Role/RoleBinding (+ClusterRole/Binding) | `rules[]` (`apiGroups`/`resources`/`verbs`), `clusterWide` |
| `external-secret` | ESO ExternalSecret | `secretName`, `data[]`/`dataFrom[]`, `refreshInterval` (store from ClusterProfile or `provider`) |
| `security-context` | (modifies PodSpec) | `psaLevel` (`restricted`\|`baseline`\|`privileged`), optional: `runAsNonRoot`, `allowPrivilegeEscalation`, `readOnlyRootFilesystem`, `runAsUser`, `runAsGroup`, `fsGroup` |

### Storage
| `type` | Produces | Key properties |
|--------|----------|----------------|
| `pvc` | PersistentVolumeClaim | `name`, `size`, `storageClassName`, `accessModes[]` (policy: `maxStorageSize`) |
| `volsync` | VolSync ReplicationSource | `sourcePVC`, `schedule`, `copyMethod`, `retain.{daily,weekly,monthly}` |

### Configuration & scaling
| `type` | Produces | Key properties |
|--------|----------|----------------|
| `configmap` | ConfigMap (+ optional volume mount) | `name`, `data`, `mountPath` |
| `scaler` | HorizontalPodAutoscaler (+ optional PDB) | `minReplicas`, `maxReplicas`, `cpuUtilization`, `memoryUtilization`, `enablePDB` |

### Operational (FluxCD)
| `type` | Effect | Key properties |
|--------|--------|----------------|
| `fluxcd-patches` | Appends `Kustomization.spec.patches` | `patches[]` (`patch`, `target`) |
| `fluxcd-postbuild` | Sets `Kustomization.spec.postBuild` | `substitute`, `substituteFrom[]` |
| `prune-protection` | Adds `kustomize.toolkit.fluxcd.io/prune: disabled` | (no properties) |

## Capability-aware traits

These require (or optionally use) a `ClusterProfile` capability, so the platform —
not the app — chooses the implementation:

- **expose** → `controllerType` (ingress vs gateway) + gateway/ingress details.
  On the **ingress** path, expose is platform-managed for TLS: it derives `spec.tls[]`
  from the rule hosts under a deterministic `<component>-tls` secret and emits the
  `cert-manager.io/cluster-issuer` annotation from the `certManagerClusterIssuer`
  capability field (empty ⇒ managed TLS disabled). Users do **not** author TLS on the
  expose trait (use the low-level `ingress` trait for full TLS control). Both paths
  validate user hostnames against the `allowedHostnameWildcard` capability field (empty ⇒
  no validation); a violation is a `ValidationError`.
- **certificate** → `issuerRef` (cert-manager issuer/cluster-issuer).
- **external-secret** → `secretStoreRef` (or the inline `provider` shorthand).

## Auto-synthesized NetworkPolicy

Routing traits (`ingress`/`httproute`/`expose`) can surface platform-reserved
`networkPolicy.trafficSources`, which the OAM layer collects to synthesize a
matching `NetworkPolicy` (see [`pkg/oam/netpol`](https://pkg.go.dev/github.com/go-kure/launcher/pkg/oam/netpol)).

## Extending

Custom traits implement `oam.TraitHandler` (`CanHandle` + `Apply`), optionally
`CapabilityAware` + `ValidateAndApplyDefaults` for capability validation.

See [pkg.go.dev](https://pkg.go.dev/github.com/go-kure/launcher/pkg/oam/builtin/traits)
for the full config-field reference, the [OAM model](https://pkg.go.dev/github.com/go-kure/launcher/pkg/oam)
for the interfaces, and `examples/` for runnable applications.

## Conventions

Handlers use `k8s.io/api` constants for well-known Kubernetes enum values (access
modes, restart policies, etc.) rather than string literals — never re-define values
that already exist upstream.
