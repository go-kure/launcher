---
title: "Quickstart"
weight: 3
---

# Quickstart

Build Kubernetes manifests from an OAM Application and a platform ClusterProfile.

## 1. An application

`app.yaml` — a single webservice component:

```yaml {check="build" profile="examples/cluster-profiles/minimal.yaml"}
apiVersion: launcher.gokure.dev/v1alpha1
kind: Application
metadata:
  name: hello
spec:
  components:
    - name: web
      type: webservice
      properties:
        image: nginx:1.27
        port: 80
```

## 2. A cluster profile

`profile.yaml` — minimal platform choices:

```yaml {check="snippet"}
apiVersion: launcher.gokure.dev/v1alpha1
kind: ClusterProfile
metadata:
  name: minimal
spec: {}
```

## 3. Build

```bash
# Render to stdout
kurel build app.yaml --profile profile.yaml

# …or write manifests to a directory
kurel build app.yaml --profile profile.yaml -o out/
```

`kurel` resolves the application against the profile and emits ready-to-apply
Kubernetes manifests (here a Deployment, Service, and ServiceAccount).

## Next steps

- Add traits (ingress, certificate, scaler) — see the [Trait Handlers](../api-reference/oam-traits/).
- Explore component types — see the [Component Handlers](../api-reference/oam-components/).
- Parameterize a reusable package (`kurel.yaml`, `--values`, `--set`) — see the
  full [kurel CLI reference](../api-reference/kurel-cli/).
- Browse runnable [Examples](examples/).
