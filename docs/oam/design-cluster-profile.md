# Design: Platform Profile — ClusterProfile

*Status: Final | Issue: [#37](https://github.com/go-kure/launcher/issues/37)*

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
  gitopsEngine: fluxcd # optional; default "fluxcd". Selects native-delivery CR type for helmchart.
  capabilities:
    <trait-type>:      # e.g. "expose", "certificate", "external-secret"
      rendering:       # values injected into trait properties at build time
        <key>: <value>
```

### Field reference

| Field | Type | Description |
|---|---|---|
| `metadata.name` | string | Identifies the cluster; referenced in build tooling |
| `spec.gitopsEngine` | string | GitOps engine for helmchart native delivery. Accepted: `"fluxcd"` (default, optional). |
| `spec.capabilities` | map | Keys are trait types; values are capability bindings |
| `capabilities.<type>.rendering` | map | Platform values merged into trait properties before handler invocation |

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
launcher ClusterProfiles. It selects which native GitOps delivery CRs are emitted for
helmchart components. It is not the same as a downstream runtime's full delivery-wiring
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
overridden; sibling keys from rendering are preserved.

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
