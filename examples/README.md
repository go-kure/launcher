# Examples

This directory contains runnable OAM Application YAMLs paired with the ClusterProfiles needed
to build them. All commands assume the working directory is the **repo root** and the binary is
at `bin/kurel` (built with `mise run build`).

## Quick start

```bash
mise run build
bin/kurel build examples/01-webservice-minimal.yaml \
  --profile examples/cluster-profiles/minimal.yaml
```

## Application examples

| # | File | Components | Traits | Profile |
|---|------|-----------|--------|---------|
| 01 | [01-webservice-minimal.yaml](01-webservice-minimal.yaml) | webservice | — | minimal |
| 02 | [02-webservice-with-expose.yaml](02-webservice-with-expose.yaml) | webservice | expose | minimal |
| 03 | [03-webservice-with-tls.yaml](03-webservice-with-tls.yaml) | webservice | expose, certificate | nginx-certmanager-vault |
| 04 | [04-webservice-full.yaml](04-webservice-full.yaml) | webservice | expose, certificate, external-secret, scaler | nginx-certmanager-vault |
| 05 | [05-worker-minimal.yaml](05-worker-minimal.yaml) | worker | — | minimal |
| 06 | [06-worker-with-traits.yaml](06-worker-with-traits.yaml) | worker | external-secret, configmap | nginx-certmanager-vault |
| 07 | [07-cronjob-minimal.yaml](07-cronjob-minimal.yaml) | cronjob | — | minimal |
| 08 | [08-cronjob-full.yaml](08-cronjob-full.yaml) | cronjob | external-secret | nginx-certmanager-vault |
| 09 | [09-postgresql-minimal.yaml](09-postgresql-minimal.yaml) | postgresql | — | minimal |
| 10 | [10-postgresql-ha.yaml](10-postgresql-ha.yaml) | postgresql | — | minimal |
| 11 | [11-helmchart.yaml](11-helmchart.yaml) | helmchart | — | minimal |
| 12 | [12-daemonset.yaml](12-daemonset.yaml) | daemonset | — | minimal |
| 13 | [13-statefulset.yaml](13-statefulset.yaml) | statefulset | — | minimal |
| 14 | [14-full-stack.yaml](14-full-stack.yaml) | webservice×3, worker, cronjob, postgresql, helmchart, daemonset, statefulset | expose, expose.internal, certificate, external-secret, configmap, scaler | gateway-certmanager-aws |
| 15 | [15-passthrough-minimal.yaml](15-passthrough-minimal.yaml) | passthrough (SparkApplication CRD + cluster-scoped ClusterRole) | — | minimal |

**Profile compatibility notes:**
- Examples 03–04, 06, 08 also work with `gateway-certmanager-aws.yaml`
- Example 14 also works with `nginx-certmanager-vault.yaml`
- Examples 01, 05, 07, 09–13, 15 work with any profile

## Cluster profiles

| File | Profile name | Capabilities | Use for |
|------|-------------|-------------|---------|
| [minimal.yaml](cluster-profiles/minimal.yaml) | `staging-minimal` | expose (Traefik ingress) | dev/staging with no TLS or secrets |
| [nginx-certmanager-vault.yaml](cluster-profiles/nginx-certmanager-vault.yaml) | `prod-nginx` | expose (NGINX), certificate (Let's Encrypt), external-secret (Vault) | production with NGINX and Vault |
| [gateway-certmanager-aws.yaml](cluster-profiles/gateway-certmanager-aws.yaml) | `prod-gateway` | expose (Gateway API), expose.internal (internal gateway), certificate (internal CA), external-secret (AWS Secrets Manager) | production with Gateway API and AWS |
| [custom-capability.yaml](cluster-profiles/custom-capability.yaml) | `staging-with-redis-sidecar` | expose (NGINX), redis-sidecar (custom) | custom-capability library example only |

## Custom capability extension example

Directory: [`custom-capability/`](custom-capability/)

| File | Purpose |
|------|---------|
| [`app.yaml`](custom-capability/app.yaml) | Application using the custom `redis-sidecar` trait alongside `expose` |
| [`definitions/redis-sidecar.yaml`](custom-capability/definitions/redis-sidecar.yaml) | CapabilityDefinition schema: validates `image` (required), `maxMemory`, `port` |
| [`cluster-profiles/custom-capability.yaml`](cluster-profiles/custom-capability.yaml) | ClusterProfile that supplies rendering values for the `redis-sidecar` capability |

### Why this example cannot be run with `kurel build`

Running the following command will fail:

```bash
bin/kurel build examples/custom-capability/app.yaml \
  --profile examples/cluster-profiles/custom-capability.yaml \
  --capability-def examples/custom-capability/definitions/redis-sidecar.yaml
# Error: transforming application: no handler for trait type "redis-sidecar"
```

**Two separate mechanisms are involved:**

1. **CapabilityDefinition** (`definitions/redis-sidecar.yaml`) — declares the *schema* for the
   trait's rendering configuration: required fields, types, and default values. This runs at
   profile evaluation time to validate and fill defaults on the rendering values coming from the
   ClusterProfile. It is pure data — no code.

2. **TraitHandler** — a Go struct with an `Apply` method that actually generates Kubernetes
   objects (Deployments, ConfigMaps, annotations, sidecar containers, etc.) from the resolved
   trait properties. This must be registered with the Transformer at runtime.

`kurel build` only registers the 15 built-in handlers. When the Transformer reaches
`type: redis-sidecar` it finds no registered handler and returns the error above.

### Using custom traits from Go

This example is intended for developers building on the kurel library. The workflow:

1. Implement `oam.TraitHandler`:

```go
type RedisSidecarHandler struct{}

func (h *RedisSidecarHandler) Apply(trait *oam.Trait, app *stack.Application, bundle *stack.Bundle) error {
    image := trait.Properties["image"].(string)
    // inject sidecar container into the bundle...
    return nil
}
```

2. Optionally implement `oam.CapabilityAware` if the trait must fail when no matching capability
   exists in the ClusterProfile.

3. Register the handler and wire capability definitions:

```go
transformer := oam.NewTransformer(componentHandlers, nil)
transformer.RegisterTrait("redis-sidecar", &RedisSidecarHandler{})

capDefs, _ := oam.LoadCapabilityDefinitions(defPaths, definitionsDir)
transformer.SetCapabilityDefs(capDefs)
```

### Future work

The `passthrough` component type (see example 15) already emits arbitrary CRDs and
non-standard objects with no Go handler. Broader config-driven extensibility — custom
*traits* and template/plugin-rendered components without Go code — is tracked in
[issue #102](https://github.com/go-kure/launcher/issues/102).
