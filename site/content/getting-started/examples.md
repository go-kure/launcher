---
title: "Examples"
weight: 4
---

# Examples

The [`examples/`](https://github.com/go-kure/launcher/tree/main/examples) directory
contains runnable OAM Applications and cluster profiles. Build any of them with:

```bash
kurel build examples/01-webservice-minimal.yaml \
  --profile examples/cluster-profiles/minimal.yaml
```

## Application examples

| Example | Shows |
|---------|-------|
| `01-webservice-minimal` / `02-…-with-expose` / `03-…-with-tls` / `04-…-full` | webservice from minimal to full (expose, TLS, probes, volumes) |
| `05-worker-minimal` / `06-worker-with-traits` | background workers |
| `07-cronjob-minimal` / `08-cronjob-full` | scheduled jobs |
| `09-postgresql-minimal` / `10-postgresql-ha` | CloudNativePG databases |
| `11-helmchart` | Helm chart via Flux |
| `12-daemonset` / `13-statefulset` | node daemons and stateful workloads |
| `14-full-stack` | a multi-component application |
| `15-passthrough-minimal` | emit an arbitrary object verbatim |

## Cluster profiles & custom capabilities

- [`cluster-profiles/`](https://github.com/go-kure/launcher/tree/main/examples/cluster-profiles)
  — platform profiles (minimal, cert-manager + vault, gateway + AWS, …).
- [`custom-capability/`](https://github.com/go-kure/launcher/tree/main/examples/custom-capability)
  — extending kurel with a custom capability.

See the [Component Handlers](../api-reference/oam-components/) and
[Trait Handlers](../api-reference/oam-traits/) references for the full set of types
and their properties.
