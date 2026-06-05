---
title: "Install"
weight: 2
---

# Install

> Tagged binary releases are not yet available. Until the first release, install
> from source.

## From source (Go)

```bash
go install github.com/go-kure/launcher/cmd/kurel@latest
```

This builds the `kurel` binary into `$(go env GOPATH)/bin`; make sure that
directory is on your `PATH`.

## Build a checkout

```bash
git clone https://github.com/go-kure/launcher
cd launcher
make build        # or: mise run build
./bin/kurel version
```

## Verify

```bash
kurel version
```

Next: the [Quickstart](quickstart/).
