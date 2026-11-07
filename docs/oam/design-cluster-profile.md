# Design: Platform Profile — ClusterProfile

*Status: Final | Issue: [go-kure/launcher#37](https://github.com/go-kure/launcher/issues/37)*

| Version | Date | Summary |
|---|---|---|
| 1.1 | 2026-05-14 | Remove `parameters` field; update GVK to `launcher.gokure.dev/v1alpha1`; add strictness rule; add migration guide |
| 1.0 | 2026-04-19 | Initial draft |

---

## 1. Purpose

A `ClusterProfile` tells the launcher runtime how the platform implements each trait.
It is an environment-level document, written once per cluster by the platform operator and
shared across all applications deployed to that cluster.

The separation it enforces: an Application says "I need ingress" — the `ClusterProfile`
says "ingress on this cluster means a Gateway API `HTTPRoute`." The application spec is
portable; the profile is not.

---

## 2. Schema

```yaml
apiVersion: launcher.gokure.dev/v1alpha1
kind: ClusterProfile
metadata:
  name: <string>       # cluster identifier, e.g. "prod-eu-west"
spec:
  gitopsEngine: fluxcd # optional; default "fluxcd". Names the engine whose delivery CRs built-in components emit.
  kubernetesVersion: "1.35" # optional. The cluster's Kubernetes release; absent means unknown and nothing is checked.
  capabilities:
    <trait-type>:      # e.g. "expose", "certificate", "external-secret"
      rendering:       # values injected into trait properties at build time
        <key>: <value>
```

### Field reference

| Field | Type | Description |
|---|---|---|
| `metadata.name` | string | Identifies the cluster; referenced in build tooling |
| `spec.gitopsEngine` | string | GitOps engine whose delivery CRs the built-in components emit. Accepted: `"fluxcd"` (default, optional). |
| `spec.kubernetesVersion` | string | The Kubernetes release the cluster runs, optional: `1.35`, `v1.35` or `v1.35.2`, the forms `helm template --kube-version` takes; a patch level is read and ignored, and only major `1` is accepted. When set, `kurel build` refuses an authored property whose Kubernetes feature gate is off by default at that release (see "Target Kubernetes version" below). Absent means unknown: nothing is checked and the build is what it was before the field existed. A release newer than the `k8s.io/api` version launcher is built against is a valid target and reads as every known gate on. |
| `spec.capabilities` | map | Keys are trait types; values are capability bindings |
| `capabilities.<type>.rendering` | map | Platform values merged into trait properties before handler invocation, and into the listed properties of a component whose handler implements `ComponentCapabilityDefaults` (the `persistentvolumeclaim` kind reads `pvc`'s `storageClassName`). A value `ComponentCapabilityDefaults` fills into a component is validated against that component's own property schema; a component handler with no schema is refused unless the trait handler or rule registered for the capability's type validates the rendering, by implementing `ValidateAndApplyDefaults` or, for a type that is not built in, through a loaded `CapabilityDefinition` (go-kure/launcher#751, go-kure/launcher#772). A lowering rule may read a binding through `LoweringContext.Capability`: `webservice` and `worker` fill `pvc`'s `storageClassName` into the claims their `pvc` volumes describe (go-kure/launcher#746). A component handler may read one through `ComponentCapabilityFiller`: `statefulset` fills it into its `volumeClaimTemplates` entries (go-kure/launcher#761) |

### Target Kubernetes version

`spec.kubernetesVersion` tells a build which Kubernetes release the cluster runs, so a
field the cluster cannot accept is refused at build time instead of at apply time
(go-kure/launcher#1183). It is the same idea as `helm template --kube-version`, and the
same string works in both.

- **Absent means unknown.** Nothing is checked, the published schemas read as before, and
  every existing document and profile builds exactly as it did. The check never changes
  what an accepted document renders to: a document either builds as it always did or is
  refused.
- **A gated property is refused, never adapted.** Some properties are behind a Kubernetes
  feature gate that is off by default on older releases. The schema names the gate
  (`PropertySchema.Gate`, published as `gate`). When the profile's version is below the
  release where the gate is on by default, an authored value is refused with the
  property's path, the gate, that release and the profile's version. Falling back to
  another shape (an image volume as an init container, say) needs a design per field and
  is not done.
- **The table.** `pkg/oam/featuregates.go` holds one row per gate: its name and the first
  release where its Default is true, read from the Kubernetes feature gates reference. Two
  tests keep it honest: every `Gate` a built-in schema carries names a row, and every row is
  carried by at least one schema.

  | Gate | On by default from | Properties |
  |---|---|---|
  | `EnvFiles` | 1.35 | `env[].valueFrom.fileKeyRef` |
  | `ContainerRestartRules` | 1.35 | `restartPolicyRules` (an init container's on the workload kinds; any container's on the raw-PodSpec kinds below), and on the workload kinds the values `Never` and `OnFailure` of an init container's `restartPolicy` (`Always` is accepted at every version; the raw-PodSpec kinds do not check the `restartPolicy` values, go-kure/launcher#1330) |
  | `PodLevelResources` | 1.34 | `podResources` (the pod kinds' `resources`) |
  | `HostnameOverride` | 1.35 | `hostnameOverride` |
  | `PreferSameTrafficDistribution` | 1.34 | the values `PreferSameZone` and `PreferSameNode` of `service`'s `trafficDistribution` (`PreferClose` is accepted at every version) |

  A gate that covers single values of an enum is named per value (`PropertySchema.ValueGates`,
  published as `valueGates`); the refusal then names the value as well
  (go-kure/launcher#1230).

- **Open schemas name their gated paths.** The check walks the declared schema, so it stops
  at an object that declares no `Properties`. The raw-PodSpec kinds (`pod`, and the kinds
  that take a pod template: `podtemplate`, `replicaset`, `replicationcontroller`) leave
  their containers and template spec open, so the open node carries
  `PropertySchema.GatedPaths` (published as `gatedPaths`): gate-relative paths to the gate
  that field needs, such as `env[].valueFrom.fileKeyRef` to `EnvFiles`. The check reads it
  and refuses the same way as for a declared `Gate`, naming the authored path
  (`template.spec.containers[0].env[0].valueFrom.fileKeyRef`). The raw-kind decode matches
  field names case-insensitively, so the check does too: every key of an open object that
  matches a path key in any case is walked, and the refusal names the spelling the author
  wrote (`template.spec.HostnameOverride`). Property validation does not
  read it, so what a document is accepted as is unchanged. The paths are
  `env[].valueFrom.fileKeyRef` and `restartPolicyRules` of `containers[]` and
  `initContainers[]`, and `hostnameOverride` and `resources` of the pod
  spec (`template.spec.*` on a pod template). Declaring them as nested `Properties`
  instead would make the validator type-check and null-strip them, which changes what is
  accepted (go-kure/launcher#1245). A test holds every gate a `gatedPaths` value names to a
  row of the table and every path to a field of the Kubernetes type.
- **Newer than launcher knows.** A release above the `k8s.io/api` version launcher is built
  against is a valid target: a newer cluster is not wrong. The table then reads every gate
  it knows as on, and says nothing about a gate it does not know.
- **A cluster that changed a gate** against its default is not modelled: the profile says
  the release, not the gate settings. Per-gate overrides are a follow-up
  (go-kure/launcher#1231), as are version-dependent bounds such as a Service name rule
  (go-kure/launcher#1230, second change) and adapting instead of refusing
  (go-kure/launcher#1232).
- **Where it runs.** `kurel build` reads the profile's version and passes it to
  `ValidateAuthoredPropertiesForTarget`. A library caller passes the version to that method
  itself; `ValidateAuthoredPropertiesWithCapabilities` takes none and checks no gate.

### Capability schema

`cluster.yaml` does not carry capability schema — it only carries rendering values.
The schema of what keys a given capability accepts (types, required fields, descriptions)
is separate from the profile itself.

For **built-in handlers**, the rendering schema lives in typed Go structs in
`pkg/oam/builtin/`, one struct per capability type. The handler validates and applies
defaults via a `ValidateAndApplyDefaults` method at ClusterProfile evaluation time.
Unknown rendering keys for built-in handlers are a build error — consistent with the
strict-by-default principle in `design-gvk.md`.

For **custom capabilities** (handlers registered by downstream consumers via library
embedding), the rendering schema is optionally declared in a `CapabilityDefinition`
document (Phase 2/3). See `design-capability-schema.md` for the full design.

### Strictness

Launcher rejects unknown fields in `cluster.yaml`. A `ClusterProfile` with unrecognised
keys is a build error. See `design-gvk.md` for the parser strictness rationale.

### What is NOT in a launcher ClusterProfile

A downstream platform runtime may layer additional fields onto its own cluster profile.
Those fields are downstream-specific and must not appear in a launcher `cluster.yaml`:

- delivery-layer wiring (FluxCD/ArgoCD engine configuration blocks)
- component-catalog / artifact-registry references
- downstream component-variant selection

Note: `spec.gitopsEngine` (a single string field) is launcher-specific and IS present in
launcher ClusterProfiles. It names the GitOps engine whose delivery CRs are emitted by
the built-in components. It is not the same as a downstream runtime's full delivery-wiring
block, which stays in that runtime only.

---

## 3. Capability Key Resolution

At build time the runtime looks up a capability for each trait using a two-step key
resolution:

1. **Scoped key** — `<type>.<scope>` where `scope` comes from the trait's
   `properties.scope` field, if set. Allows a cluster to configure multiple
   implementations of the same trait type (e.g. public and internal ingress).
2. **Bare key** — `<type>` — used when `scope` is absent or no scoped entry is found.

```
trait.type = "expose"
trait.properties.scope = "internal"

→ look up "expose.internal" in capabilities
→ if not found, look up "expose" in capabilities
→ if not found, no capability is resolved (handler proceeds without platform values)
```

Both key forms may be present in the same profile:

```yaml
spec:
  capabilities:
    expose:            # bare key — matches any expose trait without a scope
      rendering:
        controllerType: ingress
    expose.internal:   # scoped key — matched only when trait.properties.scope = "internal"
      rendering:
        controllerType: gateway
        gatewayName: internal-gateway
```

---

## 4. Merge Semantics

Rendering values are platform-provided defaults. Application inline properties always
take precedence:

```
resolved = rendering ∪ application-properties   (application overwrites)
```

An application property authored as `null` is absent, so it does not overwrite: the
rendering's value stands (go-kure/launcher#742).

A rendering value reaches the handler as a copy with its decoded type kept, so a `3`
stays an integer. The transform refuses a rendering holding a `null` below the top
level, `NaN` or `±Inf`, or a Go value that is not a string, boolean, number, list or
string-keyed object, before it builds anything (go-kure/launcher#756).

Example:

```yaml
# cluster.yaml capability rendering:
certificate:
  rendering:
    issuerRef:
      name: letsencrypt-prod
      kind: ClusterIssuer

# Application trait:
traits:
- type: certificate
  properties:
    secretName: my-app-tls
    dnsNames: [my-app.example.com]
    # issuerRef not set — comes from platform

# Resolved trait properties (what the handler receives):
{
  "secretName": "my-app-tls",
  "dnsNames": ["my-app.example.com"],
  "issuerRef": {"name": "letsencrypt-prod", "kind": "ClusterIssuer"}
}
```

If the Application overrides an individual key within a nested map, only that key is
overridden; sibling keys from rendering are preserved (go-kure/launcher#750). The
merge recurses wherever both sides hold a map; a list, or a value of another kind,
replaces the rendered value whole. A `null` is absent at any depth. Authored
validation checks nested `Required` on the merged properties, so a partial override
that relies on the rendering for a required sibling is accepted
(go-kure/launcher#765).

---

## 5. Example Cluster Profiles

### nginx ingress + cert-manager (Let's Encrypt) + Vault ESO

```yaml
apiVersion: launcher.gokure.dev/v1alpha1
kind: ClusterProfile
metadata:
  name: prod-nginx
spec:
  capabilities:
    expose:
      rendering:
        controllerType: ingress
        ingressClassName: nginx
    certificate:
      rendering:
        issuerRef:
          name: letsencrypt-prod
          kind: ClusterIssuer
    external-secret:
      rendering:
        secretStoreRef:
          name: vault-backend
          kind: ClusterSecretStore
```

### Gateway API + cert-manager (internal CA) + AWS Secrets Manager

```yaml
apiVersion: launcher.gokure.dev/v1alpha1
kind: ClusterProfile
metadata:
  name: prod-gateway
spec:
  capabilities:
    expose:
      rendering:
        controllerType: gateway
        gatewayName: prod-gateway
        gatewayNamespace: gateway-system
    expose.internal:
      rendering:
        controllerType: gateway
        gatewayName: internal-gateway
        gatewayNamespace: gateway-system
    certificate:
      rendering:
        issuerRef:
          name: internal-ca
          kind: ClusterIssuer
    external-secret:
      rendering:
        secretStoreRef:
          name: aws-secretsmanager
          kind: ClusterSecretStore
        refreshInterval: "1h"
```

### Minimal (ingress only)

```yaml
apiVersion: launcher.gokure.dev/v1alpha1
kind: ClusterProfile
metadata:
  name: staging-minimal
spec:
  capabilities:
    expose:
      rendering:
        controllerType: ingress
        ingressClassName: traefik
```

---

## 6. CapabilityAware handlers

Some trait handlers require a capability to produce correct output — for example, the
`expose` handler must know `controllerType` to dispatch to the right implementation.
These handlers implement the `CapabilityAware` interface (defined in `pkg/oam`):

```go
type CapabilityAware interface {
    CapabilityRequired() bool
}
```

If `CapabilityRequired()` returns `true` and no capability resolves for the trait, the
runtime returns `ErrMissingCapability` and the build fails with a message naming the
trait type and the cluster profile in use.

Handlers that do not implement `CapabilityAware`, or whose `CapabilityRequired()` returns
`false`, proceed without a capability and rely solely on Application inline properties.

---

## 7. Migrating from a downstream cluster profile

A downstream platform runtime may define its own richer `ClusterProfile` that a launcher
`cluster.yaml` is derived from. Launcher consumes only the capability model above:
`spec.capabilities` and its per-capability `rendering` values map across unchanged.

Everything else — delivery-layer wiring, catalog/registry references, component-variant
selection, and any per-capability `parameters` schema — is out of scope for a launcher
`cluster.yaml` and stays in the downstream runtime. `spec.gitopsEngine` is a launcher-only
addition with no upstream equivalent.

The concrete field-by-field mapping and migration steps for a specific downstream runtime
live in that runtime's own documentation, not here.
