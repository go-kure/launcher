# Design: Platform Profile — ClusterProfile

*Status: Draft | Issue: [#37](https://github.com/go-kure/launcher/issues/37)*

---

## 1. Purpose

A `ClusterProfile` tells the launcher runtime how the platform implements each OAM trait.
It is an environment-level document, written once per cluster by the platform operator and
shared across all applications deployed to that cluster.

The separation it enforces: an OAM Application says "I need ingress" — the `ClusterProfile`
says "ingress on this cluster means a Gateway API `HTTPRoute`." The application spec is
portable; the profile is not.

---

## 2. Schema

```yaml
apiVersion: launcher.wharf.zone/v1alpha1
kind: ClusterProfile
metadata:
  name: <string>         # cluster identifier, e.g. "prod-eu-west"
spec:
  capabilities:
    <trait-type>:        # e.g. "expose", "certificate", "external-secret"
      parameters:        # schema: what keys this capability accepts
        <key>:
          type: <string | integer | boolean | object | array>
          required: <bool>      # default: false
          description: <string> # optional, for documentation
      rendering:         # values: injected into trait properties at build time
        <key>: <value>
```

### Field reference

| Field | Type | Description |
|---|---|---|
| `metadata.name` | string | Identifies the cluster; referenced in build tooling |
| `spec.capabilities` | map | Keys are OAM trait types; values are capability definitions |
| `capabilities.<type>.parameters` | map | Schema declaration: documents accepted keys and types |
| `capabilities.<type>.rendering` | map | Platform values: merged into trait properties before handler invocation |

### What is NOT in a launcher ClusterProfile

The following fields exist in crane's `ClusterProfile` but are crane-specific and must not
appear in the launcher type:

- `spec.gitops` — FluxCD/ArgoCD wiring; this is a delivery-layer concern, not an OAM runtime concern
- `spec.componentCatalog` / `spec.catalog` — Harbor catalog references
- `spec.componentVariants` — crane layer-3 variant selection
- `spec.capabilities[*].parameters` is present but **not validated** in Phase 0 or Phase 1 — it is reserved for future schema enforcement

---

## 3. Capability Key Resolution

At build time the runtime looks up a capability for each trait using a two-step key resolution:

1. **Scoped key** — `<type>.<scope>` where `scope` comes from the trait's `properties.scope`
   field, if set. This allows a cluster to configure multiple implementations of the same
   trait type (e.g. a public and an internal ingress).
2. **Bare key** — `<type>` — used when `scope` is absent or no scoped entry is found.

```
trait.type = "expose"
trait.properties.scope = "internal"

→ look up "expose.internal" in capabilities
→ if not found, look up "expose" in capabilities
→ if not found, no capability is resolved (handler proceeds without platform values)
```

Capability keys in the YAML file may be written in either form:

```yaml
spec:
  capabilities:
    expose:               # bare key — resolved for any expose trait without a scope
      rendering:
        controllerType: ingress
    expose.internal:      # scoped key — resolved only when trait.properties.scope = "internal"
      rendering:
        controllerType: gateway
        gatewayName: internal-gateway
```

---

## 4. Merge Semantics

Rendering values act as platform-provided defaults. OAM Application inline properties
always take precedence:

```
resolved = rendering ∪ oam-properties   (OAM overwrites)
```

Example:

```yaml
# cluster.yaml capability rendering:
certificate:
  rendering:
    issuerRef:
      name: letsencrypt-prod
      kind: ClusterIssuer

# OAM Application trait:
traits:
- type: certificate
  properties:
    secretName: my-app-tls
    dnsNames: [my-app.example.com]
    # issuerRef not set — will come from platform

# Resolved trait properties (what the handler receives):
{
  "secretName": "my-app-tls",
  "dnsNames": ["my-app.example.com"],
  "issuerRef": {"name": "letsencrypt-prod", "kind": "ClusterIssuer"}
}
```

If the OAM Application overrides an individual key within a nested map, only that key is
overridden; unmentioned sibling keys from rendering are preserved.

---

## 5. Parameters Field

The `parameters` field documents the schema of a capability — what keys it accepts, their
types, and which are required. In Phase 0–1, this field is **informational only** — the
runtime writes it but does not validate rendering or OAM trait properties against it.

Its purpose in this phase is to give package authors a machine-readable description of what
a capability provides, so tools (editors, linters) can validate `app.yaml` against the
cluster's declared capability shape.

Example:

```yaml
expose:
  parameters:
    controllerType:
      type: string
      required: true
      description: "ingress or gateway"
    ingressClassName:
      type: string
      required: false
      description: "ingress class name; required when controllerType is ingress"
    gatewayName:
      type: string
      required: false
      description: "gateway name; required when controllerType is gateway"
  rendering:
    controllerType: ingress
    ingressClassName: nginx
```

---

## 6. Example Cluster Profiles

### nginx ingress + cert-manager (Let's Encrypt) + Vault ESO

```yaml
apiVersion: launcher.wharf.zone/v1alpha1
kind: ClusterProfile
metadata:
  name: prod-nginx
spec:
  capabilities:
    expose:
      parameters:
        controllerType:
          type: string
          required: true
        ingressClassName:
          type: string
          required: false
      rendering:
        controllerType: ingress
        ingressClassName: nginx
    certificate:
      parameters:
        issuerRef:
          type: object
          required: true
      rendering:
        issuerRef:
          name: letsencrypt-prod
          kind: ClusterIssuer
    external-secret:
      parameters:
        provider:
          type: string
          required: true
        secretStoreRef:
          type: object
          required: false
      rendering:
        secretStoreRef:
          name: vault-backend
          kind: ClusterSecretStore
```

### Gateway API + cert-manager (internal CA) + AWS Secrets Manager

```yaml
apiVersion: launcher.wharf.zone/v1alpha1
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

### Minimal (ingress only, no cert-manager, no ESO)

```yaml
apiVersion: launcher.wharf.zone/v1alpha1
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

## 7. CapabilityAware handlers

Some trait handlers require a capability to produce correct output — for example, the
`expose` handler must know `controllerType` to dispatch to the right implementation. These
handlers are marked with the `CapabilityAware` interface (defined in `pkg/oam`):

```go
type CapabilityAware interface {
    CapabilityRequired() bool
}
```

If `CapabilityRequired()` returns `true` and no capability resolves for the trait, the
runtime returns `ErrMissingCapability` and the build fails with a clear message naming the
trait type and the cluster profile in use.

Handlers that do not implement `CapabilityAware`, or whose `CapabilityRequired()` returns
`false`, proceed without a capability — they rely solely on OAM inline properties.

---

## 8. Relationship to crane's ClusterProfile

Crane's `ClusterProfile` type (`pkg/api.ClusterProfileSpec`) maps to this design as follows:

| crane field | launcher | Notes |
|---|---|---|
| `spec.capabilities` | `spec.capabilities` | Same structure, same semantics |
| `spec.gitops` | — | Stays in crane |
| `spec.catalog` | — | Stays in crane |
| `spec.componentCatalog` | — | Stays in crane |
| `spec.componentVariants` | — | Stays in crane |
| `spec.capabilities[*].parameters` | `spec.capabilities[*].parameters` | Same field, informational only in Phase 0 |
| `spec.capabilities[*].rendering` | `spec.capabilities[*].rendering` | Same field, same semantics |

crane's `ClusterProfile` document can be used directly as a launcher `ClusterProfile` by
dropping the crane-specific fields. The launcher runtime ignores unknown fields.
