---
title: "Introduction"
weight: 1
---

**launcher** is a Go library that translates OAM-style application documents into
Kubernetes objects, and **kurel** is the supported command-line tool built on it. You
describe an application with an OAM-style document (`app.yaml`) — a set of typed *components*
(webservice, worker, cronjob, postgresql, …) decorated with *traits* (ingress,
certificate, scaler, external-secret, …) — and `kurel build` resolves it into
static, GitOps-ready Kubernetes manifests.

## The two-config-set model

kurel separates **what an application needs** from **how a cluster provides it**:

- **Package config** — the application author's `app.yaml` (and an optional
  `kurel.yaml` parameter schema for distributable, parameterized packages).
- **Site config** — a platform `ClusterProfile` that supplies cluster-specific
  choices (which ingress/gateway controller, which cert-manager issuer, which
  secret store, …).

At build time the two are resolved together, so the same package renders correctly
on different clusters without edits.

## Where to go next

- [Install](install/) kurel.
- Follow the [Quickstart](quickstart/) to build your first app.
- Browse runnable [Examples](examples/).
- Read the [Concepts](../concepts/) for the design and OAM model, and the
  [API reference](../api-reference/) for packages and the CLI.
