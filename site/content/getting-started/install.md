---
title: "Install"
weight: 2
---

# Install

## Release binaries

Each [release](https://github.com/go-kure/launcher/releases) carries `kurel`
for Linux on amd64 and arm64 as `kurel_<version>_linux_<arch>.tar.gz`, with
`checksums.txt`, a Sigstore bundle signing it (`checksums.txt.sigstore.json`)
and an SBOM per archive. `<version>` is the tag without its `v`.

The steps below download a release and check that the go-kure release workflow
signed its checksums for launcher at that release's tag. The workflow is shared
by every go-kure repository, so the certificate must also name
`go-kure/launcher` and the tag. They then check the archive against the
checksums and only then install `kurel` (into `~/.local/bin`; use any directory
on your `PATH`). Each step runs only if the one before it succeeded:

```bash
VERSION=0.2.0-beta.1   # a release tag without the leading v
ARCH=amd64             # or arm64
gh release download "v${VERSION}" -R go-kure/launcher \
    -p "kurel_${VERSION}_linux_${ARCH}.tar.gz" -p 'checksums.txt*' &&
  cosign verify-blob checksums.txt \
    --bundle checksums.txt.sigstore.json \
    --certificate-identity https://github.com/go-kure/.github/.github/workflows/release-publish.yml@refs/heads/main \
    --certificate-oidc-issuer https://token.actions.githubusercontent.com \
    --certificate-github-workflow-repository go-kure/launcher \
    --certificate-github-workflow-ref "refs/tags/v${VERSION}" &&
  sha256sum --ignore-missing -c checksums.txt &&
  tar xzf "kurel_${VERSION}_linux_${ARCH}.tar.gz" kurel &&
  install -D -m 0755 kurel "$HOME/.local/bin/kurel"
```

## From a checkout (Go)

```bash
git clone https://github.com/go-kure/launcher
cd launcher
git checkout v0.2.0-beta.1   # optional: a release tag
go install ./cmd/kurel
```

This builds the `kurel` binary into `$(go env GOPATH)/bin`; make sure that
directory is on your `PATH`.

`go install github.com/go-kure/launcher/cmd/kurel@<version>` does not work:
launcher's `go.mod` carries `replace` directives, mirrored from kure, that hold
`k8s.io/kube-openapi` and kustomize at the versions launcher builds with, and Go
refuses to install a module at a version when its `go.mod` has any.

## Build a checkout

```bash
git clone https://github.com/go-kure/launcher
cd launcher
make build        # or: mise run build
./bin/kurel version
```

## Use launcher as a Go library

Go applies `replace` directives only in the main module, so a module that
imports launcher does not get launcher's. Copy the `replace` lines of launcher's
`go.mod` at the version you require into your own `go.mod`, as kure asks of its
own consumers; without the `k8s.io/kube-openapi` one the build fails, and without
the kustomize ones it uses a different kustomize than Flux.

## Verify

```bash
kurel version
```

Next: the [Quickstart](quickstart/).
