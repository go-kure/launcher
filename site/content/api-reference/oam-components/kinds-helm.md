---
title: "Kinds: Helm"
weight: 7
---

- **helm** (go-kure/launcher#349, `helm.go`) — the role-named Helm component: a
  component-position lowering rule (`HelmRule`), not a handler, that lowers to the
  kind-named terminals. Properties: `chart`, `version`, `delivery`
  (`flux` default | `template`), `source` (inline `url` with optional `kind` and,
  for a GitRepository, `ref`, for an OCI chart, `digest`; an inline Bucket's `kind`, `endpoint`, `bucketName`,
  `provider`, `region`, `prefix`; beside any of these a `name` for the generated
  source; or a reference `{name, kind, namespace}` to an
  existing HelmRepository, GitRepository, Bucket, OCIRepository or HelmChart),
  `values`, `valuesMode` (`inline` |
  `configMap`), `valuesConfigMapName`, `secretValues` (the sensitive part of the
  values tree, see below), `valuesSecretName`, `helmReleaseName` (the name of
  the HelmRelease object, see below),
  `scopeOverrides`, `hookGroupNamePrefix` and `layoutKustomizationName` (each under
  `delivery: template` only, see below),
  and the HelmRelease keys `interval`, `releaseName`,
  `targetNamespace`, `driftDetection`, `install`, `upgrade`, `valuesFrom`.
  - `delivery: flux` emits a `helmrelease` under the authored name, with the
    authored traits and annotations. A HelmRepository, GitRepository or Bucket
    source (go-kure/launcher#336) becomes `chart.spec.sourceRef` with `chart`
    required: the chart name, or for a GitRepository or Bucket the chart's path in
    the fetched artifact. For a GitRepository or Bucket the rule also sets
    `chart.spec.reconcileStrategy: Revision`, so a new source revision deploys even
    when the chart's version is unchanged (Flux's `ChartVersion` default would skip
    it); a HelmRepository keeps that default. An OCIRepository or HelmChart source
    becomes `chartRef`. `values` and the HelmRelease keys are forwarded verbatim, so their shape is
    the `helmrelease` terminal's to check. `valuesMode` is never forwarded, and the
    rule has no registration-time default for it: `inline` (or no `valuesMode`)
    keeps `values` on the `helmrelease`.
  - **`valuesMode: configMap`** (go-kure/launcher#702) with non-empty `values`
    moves them into a `configmap` trait the rule appends to the `helmrelease`, after
    its authored traits, and drops `values` from it. The trait's ConfigMap carries
    the values under the key `values.json`, and a `valuesFrom` entry
    `{kind: ConfigMap, name: <it>, valuesKey: values.json}` is placed before the
    authored entries, so an authored entry wins on a shared key (Flux merges
    `spec.valuesFrom` in list order). The values are serialized once, as indented JSON
    with sorted keys and numbers kept exact (JSON is YAML, which is how Flux reads a
    values reference); those exact bytes are both stored and hashed into the name:
    `<component>-values-<first 10 hex digits of their sha256>`. A name that would
    exceed 253 bytes is shortened by the one shortening rule
    (`oam.ShortenNameWithSuffix`, go-kure/launcher#793): the component name is cut to a
    prefix plus the first 10 hex digits of the sha256 of the full component name (8
    before go-kure/launcher#793, so such a name changes once), and the suffix is kept, so
    it is always a legal DNS-1123 subdomain and always carries the values hash. Identical values hash alike whatever their key order, and any change
    to them renames the ConfigMap and so changes the HelmRelease's spec, which is what
    makes Flux upgrade the release at once on a values-only edit (under this default
    name; see `valuesConfigMapName` below). Empty or absent `values`
    add no trait and no entry; `values` that are not a JSON object, a non-finite
    number in them, or a `valuesFrom` that is not a list are `helm:` build errors.
    Being an ordinary `configmap` trait, it is checked by that trait (a key it refuses,
    or data over the ConfigMap size limit, fails the build), labelled
    `app: <label value>` like the trait's other ConfigMaps, emitted after the
    HelmRelease, and moved with the release into the Flux namespace, since its
    `valuesFrom` names it. The trait builds its ConfigMap through the `configmap`
    kind's own code (go-kure/launcher#741); the values travel as one string, so the
    kind's string-only typing never refuses them. Known gap, shared with authored traits
    (go-kure/launcher#757): an authored `configmap` trait on the same component that
    takes the same name is not refused, and both ConfigMaps are emitted.
  - **`valuesConfigMapName` and `valuesSecretName`** (go-kure/launcher#787) name
    the values ConfigMap and the values Secret (`secretValues`, below). Each name
    is resolved in the one order of every generated name: the author's property,
    else the consumer's `Naming` hook (roles `values-configmap` and
    `values-secret`, asked with the component and the hashed default), else the
    default `<component>-values-<hash>` or `<component>-secret-values-<hash>`.
    A name from the author or the hook is used as written and gets no hash. It
    must be a DNS-1123 subdomain of at most 253 characters, and is refused
    otherwise, never shortened. Both objects follow the HelmRelease to the Flux
    namespace, so the name is held there against any other ConfigMap or Secret
    launcher names: one of the same name in the application namespace is another
    object.

    **The cost of a fixed name.** The default name moves with the content, so a
    values-only edit changes the HelmRelease's `spec.valuesFrom`, and Flux
    upgrades the release as soon as it sees the new HelmRelease. Under a name
    that does not move, the edit changes only the ConfigMap's or Secret's
    content, and the HelmRelease is unchanged. helm-controller still merges the
    referenced values on every reconciliation and upgrades the release when the
    result differs from the last one applied, so the edit is picked up at the
    release's next reconciliation: within its `interval`, not at once. Flux
    reconciles at once only where the ConfigMap or Secret carries the label
    `reconcile.fluxcd.io/watch: Enabled`, or helm-controller runs with a
    `--watch-configs-label-selector` that selects it. The rule sets no such
    label.

    A property that names nothing is refused: `valuesConfigMapName` without
    `valuesMode: configMap` or with empty `values`, `valuesSecretName` with
    empty or absent `secretValues`, and either under `delivery: template`, which
    generates neither object. Two components of one document cannot write the
    same name for the same kind: each generates its own object, and the second
    is refused (`helm: naming the values ConfigMap: name collision: ConfigMap
    "shared" is named by component "a" (role "values-configmap", set by
    valuesConfigMapName) and by component "b" (…); give one of them another
    name`).
  - **`helmReleaseName`** (go-kure/launcher#787) names the HelmRelease object,
    in the same order: the author's property, else the consumer's `Naming` hook
    (role `helm-release`, asked with the component), else the component name,
    so a document that does not set it and a hook that declines give the same
    output as before. A name that is not the default is used as written: a
    DNS-1123 subdomain of at most 253 characters, refused otherwise and never
    shortened. With a HelmRepository, GitRepository or Bucket source the
    HelmRelease carries a chart template, so its name, the default included, is
    also held to 63 characters, the owner label helm-controller gives the
    HelmChart it generates (go-kure/launcher#1080, see `helmrelease`); a longer
    component name needs a shorter `helmReleaseName`. An OCI source is reached
    through a `chartRef` and is not held. **`helmReleaseName` is the name of the HelmRelease object in the
    cluster (`metadata.name`); `releaseName` is the name Helm gives the release
    (`spec.releaseName`, the chart's `.Release.Name`).** Renaming the object
    moves nothing else: `spec.releaseName` stays the authored `releaseName` or
    the component name, so the installed release is not reinstalled under
    another name, the values ConfigMap and Secret keep their names, and the
    `helmrelease` member keeps the component's name, with its group, its
    placement and its traits. Nothing launcher writes names the HelmRelease, so
    no reference moves. The name is claimed where the HelmRelease lands, the
    default included: an authored `helmrelease` component whose `objectName` is
    the same name is refused in the transform with both named (`name collision:
    HelmRelease.helm.toolkit.fluxcd.io "default/chart" is named by component
    "chart" (role "helm-release", its default) and by component "rel" (role
    "object", set by properties.objectName); give one of them another name`).
    Under `delivery: template` the property is refused, since no HelmRelease is
    generated, and the hook is not asked.
  - **`secretValues`** (go-kure/launcher#786) is a second values tree, for the
    values that must not sit in the HelmRelease or in a ConfigMap. It is an object
    like `values`; absent, `null` or empty it changes nothing. The `helmrelease`
    kind has no such property: there, name a Secret in `valuesFrom`.
    - *Under `delivery: flux`* the rule appends a `secret` trait to the
      `helmrelease`, after its authored traits and after the values ConfigMap's
      trait. The trait's Secret holds the tree under the key `values.json`,
      serialized as the values ConfigMap's is, and is named
      `<component>-secret-values-<first 10 hex digits of the sha256 of those
      bytes>`, shortened past 253 bytes by the same rule
      (`oam.ShortenNameWithSuffix`). A `valuesFrom` entry
      `{kind: Secret, name: <it>, valuesKey: values.json}` is placed after the
      values ConfigMap's entry and before the authored entries, so the order is:
      values ConfigMap, values Secret, authored. No value of `secretValues` is
      written to the HelmRelease or to the values ConfigMap. Being an ordinary
      `secret` trait, it is checked by that trait (the 1 MiB limit), labelled
      `app: <label value>`, emitted after the HelmRelease, and moved with the
      release into the Flux namespace. A change in `secretValues` renames the
      Secret and so changes the HelmRelease, which is what makes Flux upgrade the
      release at once (under this default name; see `valuesSecretName` above).
    - *Under `delivery: template`* the tree is passed to the `helmtemplate`, which
      merges it over `values` for the render (see **helmtemplate**). Nothing is
      emitted for it: a value is in the output only where the chart renders it.
    - *A path set in both `values` and `secretValues` is refused*, under either
      delivery and either values mode:
      `helm: auth.password is set in both values and secretValues; a path may be
      set in only one of them`. Two objects at the same key are compared key by
      key; anything else at a key both trees set (a scalar, a list, a null, and
      a nil map a Go caller passes, which is a null) is a shared path. Under
      `delivery: template` the two trees are compared as they are, without a
      JSON round trip, since the render takes the values with their Go types:
      an object is a `map[string]any`, which is all a parsed document holds, and
      a map of another type a Go caller passes (`map[string]string`) at a key
      both trees set is a shared path too. An empty
      key is a key like any other and is written `""` in
      the message. Without the refusal the winner would depend on the values mode,
      since Flux applies inline `spec.values` after every `valuesFrom` entry.
    - *A key named `global` below the top level of `secretValues` is refused*,
      under either delivery and either values mode, before anything is rendered or
      emitted (go-kure/launcher#794, item 9):
      `helm: secretValues: redis.global: a key named global is allowed only at the
      top level, …`. Helm reads `<dependency>.global` as that dependency's globals.
      Where their shape conflicts with the chart's own `global` at a key, a table
      on one side and a plain value on the other, it drops the dependency's entry
      and prints it in a warning that cannot be intercepted: in the build's log
      under `delivery: template`, in the Flux controller's under `delivery: flux`.
      Launcher does not read the chart, so it cannot tell a dependency from any
      other key: a key named `global` below the top level that is not a
      dependency's globals cannot be given through `secretValues` either, only
      through `values`. Put a sensitive global under the top-level `global`, which
      every dependency receives and Helm does not print. The tree is searched as
      JSON, through objects only: a key in a list element is not refused. The
      message names the path by its keys and no value.
    - *Policy.* A policy that forbids explicit secrets
      (`oam.ExplicitSecretPolicy`, see the `pkg/oam` README) refuses a component
      with `secretValues`, under either delivery, with a violation naming the
      component. A policy that does not implement that optional interface allows
      it.
    - *No refusal of the property repeats a value.* It names `secretValues`, a
      path by its keys, or the type of a wrong value, and never wraps an encoding
      or decoding error, which could quote one. Under `delivery: template` an
      error about an object the chart rendered is another matter (see
      **helmtemplate**, "not covered").

    **Security note.** `secretValues` keeps sensitive values out of the
    HelmRelease and the values ConfigMap. It does not encrypt them. The generated
    Secret is in the build output in clear form (`data` is base64, an encoding):
    the output is exactly as sensitive as the input document, and so is every
    place it is written to, a Git repository or an OCI artifact included.
    Encrypting it is the consumer's business (SOPS or the like, on the written
    manifests). The Secret's name, and the HelmRelease naming it, carry 40 bits of
    a digest of the tree, so whoever reads either can test a guess of the whole
    tree against it. To keep a value out of the document and the output altogether,
    create the Secret out of band (an `external-secret` trait, a sealed or
    externally managed Secret) and name it in `valuesFrom`, or use the chart's own
    existing-secret values.
  - An inline source also emits the source: a `helmrepository` with only the URL
    for `http(s)://`, or an `ocirepository` with `ref.tag: <version>` for `oci://`.
    **`source.digest`** (go-kure/launcher#1064) pins an inline `oci://` chart to a
    manifest digest, `sha256:` followed by 64 lowercase hex digits: the
    `ocirepository` gets it as `ref.digest`, beside `ref.tag` when `version` is
    set and alone without it, and Flux then pulls by digest, so a tag pushed again
    upstream does not change what the cluster installs. It is valid only with an
    inline OCI source (an `oci://` `url`, `kind` unset or `OCIRepository`).
    A Git repository needs `kind: GitRepository` set (an `http(s)://` URL alone
    means a Helm repository) and emits a `gitrepository` with the URL (`http://`
    or `https://`) and `source.ref`, which must set exactly one of
    `branch`, `tag`, `semver`, `name`, `commit`: Flux would otherwise check out
    branch `master`, or pick one of several fields by precedence. The schema
    publishes the rule as an `exclusive` group on `source.ref`, and a field
    authored as an empty string beside a set one counts as a second field.
    `kind: Bucket`
    with `endpoint` and `bucketName` (and optionally `provider`, `region`,
    `prefix`), and no `url`, emits a `bucket` with exactly those keys.
    Credentials (`secretRef` and the like) have no inline form: author the
    `gitrepository` or `bucket` component and reference it. That includes every
    `ssh://` repository, which Flux reads only with a key from `secretRef`.
    The `ocirepository` also selects the Helm chart content layer
    (`application/vnd.cncf.helm.chart.content.v1.tar+gzip`) with
    `operation: copy`. Flux therefore passes the chart archive through unchanged.
    By default it would extract and re-archive the chart, dropping files that its
    ignore rules exclude (`*.zip`, `*.png`, ...) even when the chart reads them
    with `.Files.Get`.
    It is named `<document>-source-<digest>`, where the 10-hex digest is taken
    over the content identity: `helm:<url>`, `oci:<url>:<version>`, with a
    digest `oci-digest:<JSON of url, version and digest>`,
    `git:<JSON of url and ref>`, or `bucket:<JSON of provider, endpoint,
    bucketName, region, prefix>` (JSON, so a `:` inside a URL or endpoint cannot
    make two identities collide). Two components with one OCI URL and version
    therefore share a source only when their digests are equal.
    Components of one document with the same identity share one source; the
    first emits it and the rest only reference it
    (`LoweringContext.ResolveSharedName`).
    Different documents never share or collide, because the document name is part
    of the source name. The source carries no traits and keeps its terminal's
    interval default rather than the release `interval`. The source terminal's
    registry allowlist (`ApplyPolicy`) applies to it.
  - **Naming the generated source** (go-kure/launcher#787). The name is
    resolved in the one order of every generated name: the author's
    `source.name`, else the consumer's `Naming` hook, else the default above.
    - *The hook* is asked under role `helm-source`, once for each source
      identity of a document, with the default `<document>-source-<digest>` and
      no component, since the source belongs to the document: every component of
      that identity that names no source adopts the answer.
    - *`source.name` beside an inline source* names the source that component
      generates. The name is the component's own choice. Components that write
      the same name for the same identity share one source; a component that
      writes none does not share it, and gets the document's source beside it
      (two sources of one identity), unless the name written is that source's
      own (its default, or the hook's answer): one name for one identity is one
      source. One name for two identities, or for two
      kinds, is refused as a collision naming both components, and so is a hook
      answer that gives two identities one name.
    - A name from the author or the hook is used as written: it must be a
      DNS-1123 subdomain of at most 253 characters and is refused otherwise,
      never shortened.
    - *The source cannot take the name of a component.* It is a component of the
      lowered document itself, so `source.name` equal to the component's own
      name, or to the name of another component the document holds, is refused
      (`helm: source.name "web" is the component's own name; the generated
      source is a component of the document too, so give it another name`). The
      default and a hook answer are held to the same rule (`helm: the generated
      source would be named "shop-source-…", the name of a helmrepository
      component of the document; … so rename that component, name the source
      with source.name, or have the Naming hook return another name for role
      "helm-source"`). To give a HelmRepository and its HelmRelease one name,
      author the `helmrepository` component with `objectName` and reference it.
    - The source lands beside the HelmRelease, in the Flux namespace when one is
      set. Its name is held there against every other object of its kind
      launcher names; an object of the same name and kind in the application
      namespace is another object.
    - `source.name` alone, with no inline source, references an existing source
      as before, and `source.namespace` belongs to that form only.

    **Breaking** (go-kure/launcher#787). A `helm` component with both
    `source.url` and `source.name` was refused and builds now, under
    `delivery: flux`. The refusal of `source.namespace` beside an inline source
    reads `helm: source.namespace is only valid with a reference to an existing
    source (source.name and no inline source); a generated source is created
    beside the HelmRelease`. An authored component that already has the name a
    generated source would take by default was refused by `pkg/oam` as a
    duplicate component name, and is now refused by the rule with the message
    above.
  - **The generated source is applied with the application bundle, ahead of
    every group** (go-kure/launcher#783). The rule orders the release after the
    source it generates or adopts (`Component.OrderAfter`), and `pkg/oam` puts
    such a source among the application bundle's own applications, beside the
    ordered groups. A source several releases share therefore exists once and
    precedes all of them, whatever tier a release is placed in. A `placement`
    policy or tier annotation naming the generated source, and a `dependency`
    rule making it wait on a component, fail the build. A source the component
    references by `source.name` is the author's own component and the rule
    orders nothing after it.
  - `delivery: template` emits a `helmtemplate` with the URL, its resolved kind,
    `chart`, `version`, `values`, `secretValues`, `scopeOverrides` and an authored `releaseName`,
    which the `helmtemplate` checks and, when unset, defaults to the component
    name, the release name the `delivery: flux` HelmRelease carries too
    (go-kure/launcher#785; see **helmrelease**). An authored `hookGroupNamePrefix`
    (go-kure/launcher#787) is passed on as written and is the `helmtemplate`'s; under
    `delivery: flux` it is refused with a `helm:` message, since a HelmRelease installs the
    chart, no hook-group layout exists and the prefix would name nothing. An authored
    `layoutKustomizationName` (go-kure/launcher#787) is passed on and refused the same way:
    under `delivery: flux` the component has no layout of its own. No source is emitted, and an authored
    `valuesMode: inline` is dropped. The rule refuses everything a client-side
    render cannot honour, each with a `helm:` message:
    - a source reference, and `valuesMode: configMap`;
    - `source.name` beside an inline source, `valuesConfigMapName`,
      `valuesSecretName` and `helmReleaseName` (go-kure/launcher#787): the render
      generates no source, no values ConfigMap, no values Secret and no
      HelmRelease, so each would name nothing;
    - an inline GitRepository or Bucket source, since the render fetches only
      from a Helm or OCI repository;
    - an OCI source without `version`;
    - `source.digest` (go-kure/launcher#1064): the render fetches the chart by
      `version` and does not pin it to a digest, and only the `ocirepository`
      that `delivery: flux` generates reads it;
    - every other HelmRelease key (`interval`, `targetNamespace`,
      `driftDetection`, `install`, `upgrade`, `valuesFrom`), which `helmtemplate`
      does not accept.
  - **`scopeOverrides`** (go-kure/launcher#794, item 11) states the scope of a kind
    the chart renders, for `delivery: template`: the list of `{apiVersion, kind,
    scope}` the `helmtemplate` and `manifests` components take, with the meaning it
    has there (see **Scope overrides** under **helmtemplate**). The rule forwards
    the authored list to the `helmtemplate` as written, under the declared spelling
    of the key, so a chart delivered through `helm` and one authored as a
    `helmtemplate` with the same list render the same objects. `null` reads as
    omission under either delivery.
    - *A malformed entry is refused by the rule*, with the shared parser's message
      under its own prefix (`helm: scopeOverrides[0]: scope "cluster" is invalid;
      must be "Cluster" or "Namespaced"`), so the message names the component type
      the author wrote. An entry that contradicts a `CustomResourceDefinition` the
      chart renders is refused when the chart is rendered, by the `helmtemplate`.
    - *`delivery: flux` refuses the property*, set or defaulted and whatever its
      shape: `helm: delivery: flux does not support scopeOverrides (only a
      client-side render reads it)`. Helm creates a HelmRelease's objects in the
      cluster, so nothing in the build could apply a stated scope, and dropping the
      property would let an author believe it took effect.
  - Strict, unlike the removed `helmchart` composite. An undeclared key at the top level or inside
    `source` is refused, and so are:
    - `delivery: native`;
    - `source.namespace` with an inline source;
    - an inline HelmRepository or OCIRepository URL carrying a user or password
      (`https://user:token@host`, `oci://user@registry/chart`), under either
      delivery: the URL is copied verbatim into the generated source or the
      render, so the credential would sit in plain text in the output.
      Credentials go in the `secretRef` of an authored `helmrepository` or
      `ocirepository`, referenced with `source.name`; a client-side render takes
      none. No message repeats the URL, not even the refusal of one that does
      not parse;
    - an inline GitRepository without exactly one `source.ref` field, or with a
      URL that is not `http://` or `https://` (an `ssh://` one needs credentials),
      or one that is more or less than a host, an optional port and a
      repository path (user info, a query or a fragment is refused), or whose
      port is outside 1–65535;
    - `url` with an inline Bucket, or one without `endpoint` or `bucketName`, or
      an `endpoint` that is more than a `host[:port]` or an `https://` URL of a
      host and port (user info, a path, a query, a fragment or `http://` is
      refused: a non-TLS endpoint needs `insecure: true`, which only an authored
      `bucket` takes) or whose port is outside 1–65535, or a `provider` other
      than `generic`, `aws`, `gcp` or `azure` (checked at the helm rule; the
      `bucket` terminal leaves it to the CRD);
    - `source.ref` other than on an inline GitRepository, and `endpoint`,
      `bucketName`, `provider`, `region`, `prefix` other than on an inline
      Bucket;
    - `source.digest` other than on an inline OCI source (a HelmRepository,
      GitRepository or Bucket source has no manifest digest, and a referenced
      source pins its own), or other than `sha256:` followed by 64 lowercase hex
      digits;
    - `chart` with an OCIRepository or HelmChart source;
    - `version` with a referenced OCIRepository or HelmChart source, which pins
      its own;
    - `version` with a GitRepository or Bucket source: Flux reads that chart at
      the source's fetched revision and ignores `version`;
    - two keys equal ignoring case, at the top level or inside `source`
      (`chart` and `Chart`): the decode would match both to one field and keep
      either.

  An authored component already named like a generated source fails the build:
  the rule refuses it when the document holds that component as the rule runs
  (see **Naming the generated source**), and `pkg/oam` refuses it as a duplicate
  component name when another rule emits it in the same lowering round.
- **helmchart** — since go-kure/launcher#351, the kind-named terminal for Flux's `HelmChart`:
  see **helmrepository / ocirepository / gitrepository / bucket / helmchart**. Until
  go-kure/launcher#350 the name belonged to a role-level composite (a HelmRelease plus its source,
  or a client-side render), which is gone; use `helm`. A document written for the composite
  fails validation on a key `HelmChartSpec` does not declare. When that key is one of the
  composite's own that `HelmChartSpec` lacks (`delivery`, `releaseName`, `targetNamespace`,
  `source`, `values`, `valuesMode`, `driftDetection`, `install`, `upgrade`, `valuesFrom`), the
  error adds a pointer to `helm`; any other unknown key gets the plain error. To
  migrate, change `type: helmchart` to `type: helm` and rename `delivery: native` to
  `delivery: flux`; the other properties keep their names. The output then changes only as
  this table says. `TestHelmParity` (`pkg/cmd/kurel`) pins it on three fixture pairs: the
  last `helmchart` output of each input, frozen before the removal, is diffed against the
  `helm` build, and the diffs are in `pkg/cmd/kurel/testdata/helm-parity/`:

  | # | What changes with `helm` |
  |---|---|
  | 1 | The values ConfigMap name carries a values hash. |
  | 2 | A generated source is named `<app>-source-<digest>`, not after the component. |
  | 3 | *(void)* `oci` and Helm-over-OCI components: neither shares an OCIRepository with the other (go-kure/launcher#665). |
  | 4 | The delivery value `native` is `flux`. |
  | 5 | *(void)* `targetNamespace` under a Flux namespace: both default it to the application namespace (go-kure/launcher#625). |
  | 6 | A generated source keeps its terminal's default interval, not the release interval. |
  | 7 | No registration-time `valuesMode` default; `valuesMode` is never forwarded (under `configMap` the values become a `configmap` trait). |
  | 8 | *(void)* Generated sources: neither gets an automatic health check; launcher sets none (go-kure/launcher#781). |
  | 9 | The registry allowlist applies to inline sources. |
  | 10 | Generated sources are applied with the application bundle, ahead of every group. A tier annotation on the component places only its release. |
  | 11 | Template delivery refuses `targetNamespace`, and an unset `releaseName` renders under the component name, shortened as Flux shortens a release name, instead of `release` (go-kure/launcher#776, go-kure/launcher#785). |
  | 12 | `chart` is refused with an OCIRepository or HelmChart source. |
  | 13 | `version` is refused with a referenced OCIRepository or HelmChart source. |
  | 14 | `source.namespace` is refused together with `url`. |
  | 15 | Unknown keys are refused at any depth of `source`, and so are two keys that differ only in case. |
  | 16 | *(void)* A generated OCIRepository: both set `layerSelector` (the chart content layer, `copy`) (go-kure/launcher#665). |
  | 17 | `placement` may not place a generated source in any tier, and a `dependency` rule may not make it wait. |
  | 18 | Any other `helmchart` default or build-time check a terminal does not reproduce (strict decoding). |
  | 19 | The HelmRelease carries the component label and a post-renderer that sets it on the chart's pod templates, as every component's output does (go-kure/launcher#788). |
  | 20 | Every HelmRelease carries `spec.releaseName`: the authored value, else the component name, where `helmchart` left an unset one to Flux (go-kure/launcher#785). |

  Two `helmchart` behaviours have no `helm` counterpart beyond row 18: `valuesMode: configMap`
  no longer makes the component a `LayoutAugmenter` (the values ConfigMap comes from a
  `configmap` trait on the `helmrelease`, ordinary build output, so `kurel build` accepts it and a layout-walking consumer
  no longer moves the component into a sub-layout), and under `delivery: template` the render's
  `.Release.Namespace` can no longer be authored (row 11; see **helmtemplate**).
- **helmrelease** — the kind-named terminal for Flux's `HelmRelease`
  (go-kure/launcher#327, part of the Helm-family redesign go-kure/launcher#336). Its
  properties are exactly the top-level JSON keys of `HelmReleaseSpec` in the
  helm-controller API version `go.mod` links; a test ties the published schema to that struct, so a helm-controller bump that adds or
  drops a spec field fails the suite until the schema follows. It emits no source object:
  `chart.spec.sourceRef` or `chartRef` names one that already exists, and with
  `chart.kind: OCIRepository` helm-controller generates one (see **Chart template of kind
  OCIRepository**).

  **Decoding.** The whole property map is decoded with `builtin.DecodeStrictJSON` into
  `HelmReleaseSpec`. A key the struct does not declare,
  at any depth (`chart.spec.chartVersion`, a stray key inside a `valuesFrom` entry), is refused
  by name, and so is a wrongly typed value (`suspend: "yes"`, `maxHistory: "3"`, an
  unparsable duration). The schema keeps the nested Flux blocks as open objects; the strict
  decode is what checks them. `valuesMode` is refused like any other unknown key; the
  error adds a pointer to the `helm` component's `valuesMode: configMap`, or a
  `configmap` trait plus a `valuesFrom` entry, which replace it (go-kure/launcher#702).
  Known gap, inherited from the decoder:
  inside a type with its own `UnmarshalJSON` unknown keys are not refused — `values` is the
  case that matters, and it is open by design. Keys match case-insensitively, as in
  `encoding/json`; schema validation, which a `kurel build` runs first, is exact.

  **Defaults and checks.** `interval` defaults to `60m` when unset (a zero duration counts
  as unset); Flux requires the field. A set `interval` must be a duration Flux accepts
  (go-kure/launcher#590, go-kure/launcher#601; `oci` applies the same check). The CRDs
  require `^([0-9]+(\.[0-9]+)?(ms|s|m|h))+$`: unsigned, in `ms`, `s`, `m` or `h` (`10m`,
  `1h30m`, `1.5h`). `time.ParseDuration` alone would also accept `-5m` or `500us`, which
  would fail at apply time; both are build errors. The value is emitted as a
  `metav1.Duration`, which serializes `Duration.String()` rather than the authored text, so
  that form is checked too: `0.5ms` matches the pattern but is written as `500µs`, and is
  refused as below Flux's millisecond resolution. A positive value too small for the
  duration type (`0.0000000001ms`) is refused the same way rather than emitted as `0s`. In
  practice any value of `0s` or at least `1ms` is accepted. The check runs on the authored
  text, which also refuses a positive value that would decode to zero and be replaced by the
  default, and again in `Generate` on the decoded duration's emitted form, for a config built
  directly. It lives in the internal `pkg/oam/internal/fluxduration`. Its other
  duration fields are checked
  the same way, in the same form (go-kure/launcher#606): `timeout`, `chart.spec.interval`,
  `install.timeout`, `upgrade.timeout`, `test.timeout`, `rollback.timeout`,
  `uninstall.timeout`, and `install.strategy.retryInterval` and
  `upgrade.strategy.retryInterval`. `chart.spec.timeout`, read only under
  `chart.kind: OCIRepository`, takes no `h`, as an `ocirepository`'s timeout does not, and must
  be under an hour (see **Chart template of kind OCIRepository**). A nested key matches
  case-insensitively at every level, as the decode does. Exactly one of
  `chart` and `chartRef` is required. The published schema lists both as optional and says
  so in the handler's `exclusive` group (go-kure/launcher#790): a validator built from it
  refuses both and neither, as the build does. The template's `kind` is HelmChart, its
  default, or OCIRepository (see **Chart template of kind OCIRepository**); any other is
  refused. Under an authored `chart` of kind HelmChart, `chart.spec.sourceRef.kind`
  is required (go-kure/launcher#790): the API requires it and takes `HelmRepository`,
  `GitRepository` or `Bucket`, the Go type leaves an empty one out of the object, and the
  kind does not choose a source's kind for the author, so a reference without one is
  refused with `chart.spec.sourceRef.kind: required (…)`. Which of the three it is stays the
  CRD's to check. A template of kind HelmChart without `chart.spec.sourceRef`, or without
  `chart.spec.chart`, is refused the same way: the API's rules require both for that kind.
  So, under kind HelmChart, are the OCIRepository fields the template inlines, which that
  kind does not read, whatever their value (`false` and `null` included): `url`, `ref`,
  `layerSelector`, `provider`, `secretRef`, `serviceAccountName`, `certSecretRef`,
  `proxySecretRef`, `timeout`, `ignore`, `insecure`, `suspend`, and
  `verify.matchOIDCIdentity` and `verify.trustedRootSecretRef`; a source's own settings go
  on the source `sourceRef` names. An authored `chart.spec.verify` that names no `provider` is written with
  `provider: cosign`, the API's default: the Go type writes the field whether or not it was
  authored, so the API's default never applies, and the object is then what the API would
  have made of the omitted field. An authored `provider: ""` is refused, since the type
  cannot tell it from an unauthored one and the API's enum (`cosign`, `notation`) refuses it
  as written. Two spellings of one key on the way (`Provider` and `provider`) are refused,
  since the decode keeps one value and drops the other. `TestKindComponents_OmittedRequiredAndWrittenDefaults` holds both to the
  markers in the source of the linked Flux modules, which ship no CRD: the required field
  to a refusal shown on a document, and the written value to the default the marker states,
  so an upstream change of that default fails the test. "Required" there is what the
  markers say; no API server was asked. `values` must be a JSON object; a non-finite number
  (`.nan`, `.inf`) is a build error, never a panic. Each `valuesFrom` entry is held to the
  `ValuesReference` CRD's own constraints, and a violation is a build error naming the
  entry's index: `kind` is `Secret` or `ConfigMap`, exactly as Flux's enum spells them
  (go-kure/launcher#748); `name` is required and at most 253 characters; a set `valuesKey`
  is at most 253 characters and matches `^[\-._a-zA-Z0-9]+$`; a set `targetPath` is at
  most 250 characters and matches the CRD's pattern (dot-path characters, `\`, `/`, and
  `[n]` indexes of up to five digits) (go-kure/launcher#762). Lengths count characters, as
  the CRD's `maxLength` does, not bytes. Like the other checks these run again in
  `Generate`, and they cover the `valuesFrom` a `helm` component passes through.
  **Every other required field the type writes unauthored is refused too**
  (go-kure/launcher#883), where it is left out under a parent that is authored: the `name`
  of a `dependsOn` entry, of `chartRef` (with its `kind`), of a Secret or ConfigMap reference
  (`kubeConfig`, the chart template's `secretRef`, `certSecretRef`, `proxySecretRef` and its
  `verify` references), of an install, upgrade or wait `strategy` and of a test filter; a
  `verify.matchOIDCIdentity` entry's `issuer` and `subject`; a drift ignore rule's `paths`; a
  post-renderer's patch and image `name`; a health check expression's `apiVersion` and
  `current` (`dependsOn[0].name: required (the Flux API requires the name of what this refers
  to)`). The list is generated from the markers of the linked helm-controller module (see
  ["Required fields a kind writes unauthored and does not refuse"](conventions.md#required-fields-a-kind-writes-unauthored-and-does-not-refuse)) and runs after the checks
  above, so `chart.spec` and a `valuesFrom` entry's `kind` and `name` keep their words. Nothing
  else is checked here: the enum checks on `driftDetection.mode`, `install.crds` and
  `upgrade.crds` are left to the HelmRelease CRD's own admission, and so are the limits of
  an authored `releaseName`.

  **Chart template of kind OCIRepository** (go-kure/launcher#1072). With
  `chart.kind: OCIRepository`, helm-controller generates an OCIRepository for the chart
  artifact `chart.spec.url` names. The component holds the template to the Flux API's rules
  for that kind (helm-controller API v1.7.1): `chart.spec.url` is required and starts with
  `oci://`, and carries no user or password, which it would write in plain text into the
  HelmRelease (put the registry credential in a Secret named by `chart.spec.secretRef`);
  `chart`, `sourceRef`, `valuesFiles` and `ignoreMissingValuesFiles` are refused;
  `version` takes only `*` and `reconcileStrategy` only `ChartVersion`, since the chart's
  version is the tag or digest `chart.spec.ref` names. The OCIRepository fields the template
  inlines, refused under kind HelmChart (above), are taken and written as authored. Under a
  non-empty allowlist `chart.spec.url` is held to the allowed registries as an
  `ocirepository`'s `url` is: its registry named explicitly, with a repository path after it.
  `chart.spec.timeout` takes no `h` and must be under an hour: helm-controller writes the
  OCIRepository it generates through the unstructured converter, which writes a duration of
  an hour or more with an `h`, and the OCIRepository's timeout refuses that whatever form it
  was authored in. For a longer timeout, use an `ocirepository` component and a `chartRef`
  to it. helm-controller names the OCIRepository `<namespace>-<name>` after the HelmRelease,
  in the HelmRelease's namespace, gives it the template's interval (else the HelmRelease's)
  and, when the template sets no `chart.spec.layerSelector`, the chart layer as its layer
  selector (an authored one is kept), takes over an object of that name and deletes
  it with the HelmRelease, when the kind, name or namespace of the chart source it generates changes, or
  when the release switches to `chartRef`. So an object of that identity the build
  generates is refused as a collision (see the collision contract in `pkg/oam`'s README).
  Prefer an `ocirepository` component and a `chartRef` when several releases share the
  source or it is authored on its own.

  **Generated HelmChart** (go-kure/launcher#1074). With a chart template of kind HelmChart,
  helm-controller generates the HelmChart `<namespace>-<name>`, named after the HelmRelease,
  in the namespace `chart.spec.sourceRef` names, else in the HelmRelease's, takes over an
  object of that name and deletes it with the HelmRelease, when the kind, name or namespace
  of the chart source it generates changes, or when the release switches to `chartRef`. So a
  `helmchart` component, or any other HelmChart the build generates, of that identity is
  refused as a collision naming helm-controller's claim, as for kind OCIRepository. A
  `chartRef` claims nothing.

  **Name of the generated chart source** (go-kure/launcher#1080). With a chart template of
  either kind, helm-controller labels the source it generates with the HelmRelease's name as
  the `helm.toolkit.fluxcd.io/name` owner label (helm-controller v1.7.1
  `internal/reconcile/helmchart_template.go:147`), and names it `<namespace>-<name>` after the
  HelmRelease's own namespace and name: a `chart.spec.sourceRef.namespace` moves the
  HelmChart, never its name (`oam.GeneratedChartSource`). A label value is at most 63
  characters and a name at most 253, and a source helm-controller cannot apply leaves the
  HelmRelease never reconciled. So `Generate` refuses a HelmRelease name over 63 characters,
  whether from the component name or from `objectName`, and a `<namespace>-<name>` over 253,
  the namespace being the HelmRelease's after the move to the Flux namespace. The remedy is a
  shorter name, or a `chartRef` to an authored source, which is not held. A component's
  namespace is a DNS label, so with the name held to 63 only a `HelmReleaseConfig` built
  directly with a longer `Namespace` reaches the 253 limit. This is a new build refusal for a
  document of kind HelmChart, the default, whose component or object name is over 63
  characters, and for a `helm` component with a HelmRepository, GitRepository or Bucket
  source whose HelmRelease name is: such a HelmRelease never reconciled.

  **Release name** (go-kure/launcher#785). A component that authors no `releaseName` gets
  `spec.releaseName` set to the component name, so every HelmRelease in the output names its
  release. A name over 53 characters, Helm's limit, is shortened as Flux helm-controller
  shortens a release name — its first 40 characters, `-`, and the first 12 hex digits of the
  SHA-256 of the whole name (`oam.ShortenName` at `oam.ShortenLimitHelmRelease`,
  go-kure/launcher#793) — so the written name is the one Flux would shorten the component
  name to itself. An authored `releaseName` is written as it is, never shortened.
  `helmtemplate` renders under the same default, so a `helm` component releases its chart
  under one name with `delivery: flux`, with or without a Flux namespace, and with
  `delivery: template`. A default Helm would not install under is a build error with the
  remedy to set `releaseName`: the shortening can cut a dotted name just after a `.`, leaving
  a label that starts with `-`. The check runs again in `Generate`, where a
  `HelmReleaseConfig` built directly with neither a `Name` nor a `releaseName` is refused as
  well: it has nothing to derive a default from.

  **Breaking output change** (go-kure/launcher#785): every HelmRelease gains
  `spec.releaseName`. Left unset, Flux names a release `<targetNamespace>-<name>` when
  `spec.targetNamespace` is set and `<name>` otherwise, so the release name itself changes
  wherever a target namespace is set and no `releaseName` is authored: under a Flux
  namespace, which defaults one (below), and with an authored `targetNamespace` alone. There
  the release `shop-web` of a component `web` with target namespace `shop` becomes `web`. Flux
  does not rename a release that is already installed: it uninstalls the existing release and
  installs a new one under the new name (the warning on `.spec.releaseName` in Flux's
  HelmRelease reference). To keep the installed release, author its name as `releaseName` on
  such a component: `<targetNamespace>-<name>`, or, where that is over 53 characters, the
  shortened name Flux installed it under (its first 40 characters, `-`, 12 hex digits of its
  SHA-256; the `name` of an entry in the HelmRelease's `status.history`). The long form is
  not accepted there: the HelmRelease CRD limits `releaseName` to 53 characters. Without a
  target namespace the name stays `<name>` and only the field is new.

  **Identity and namespaces.** The HelmRelease is named after the component and lands in
  the Flux namespace when one is configured, else in the application namespace
  (`SetFluxNamespace`). Under a Flux namespace, a component that does not author
  `targetNamespace` gets `spec.targetNamespace` set to the application namespace, so the
  release still installs there rather than into the Flux namespace; an authored value wins.
  The release's Helm storage follows the release (go-kure/launcher#988): a component that
  does not author `storageNamespace` gets `spec.storageNamespace` set to the effective
  target namespace, authored or defaulted, so the `sh.helm.release.v1.*` Secrets, which hold
  the rendered manifests and the merged values, stay with the release rather than collect in
  the Flux namespace; an authored value wins. Without a Flux namespace neither is set, and
  helm-controller keeps the storage in the HelmRelease's own namespace, the application
  namespace. A `helm` component takes no `storageNamespace`, so its release always gets the
  default.
  The Kustomization `oci` emits has no such default, deliberately (go-kure/launcher#794,
  item 1): a HelmRelease's `targetNamespace` only says where the release installs, while a
  Kustomization's overrides the namespace of every namespaced object in the artifact (see
  `oci`).
  The ConfigMaps and Secrets the HelmRelease reads from its own namespace — `valuesFrom`,
  `kubeConfig.secretRef` / `configMapRef`, and `chart.spec.verify.secretRef` when
  helm-controller creates the HelmChart beside the release (`chart.spec.sourceRef` names no
  namespace, or names the Flux namespace), and under `chart.kind: OCIRepository` the
  `chart.spec.secretRef`, `certSecretRef`, `proxySecretRef`, `verify.secretRef` and
  `verify.trustedRootSecretRef` of the OCIRepository it generates beside the release — must
  live in the Flux namespace too. A `configmap`, `external-secret` or `certificate` trait on the
  component whose object one of them names moves there with the release; one none of them names
  stays in the application namespace with the release's workloads (go-kure/launcher#740). A
  trait ConfigMap named in `valuesFrom` therefore leaves the application namespace even when the
  chart's pods read it too; give the pods their own copy, under another name, if they need it.
  *Required reads*: the HelmRelease cannot become Ready without each of those but a
  `valuesFrom` entry marked `optional: true`, and of an OCIRepository chart template the
  `verify.trustedRootSecretRef` only under the `cosign` provider; a suspended release
  (`suspend: true`) requires none. Under a delivery whose groups wait for their Flux objects, a
  trait on a component of a later group that makes one is refused (go-kure/launcher#1079), and
  so is a component of a later group that makes one where the release lands
  (go-kure/launcher#1086). So is a component of a later group making the source the `chartRef`
  or a chart template of kind `HelmChart` names, in the reference's namespace or else the
  release's (go-kure/launcher#1088).
  The target namespace does not reach the release name: `spec.releaseName` is always set
  (see **Release name**), so Flux does not derive `<targetNamespace>-<name>`. Author
  `releaseName` when a specific release name matters — for instance when taking over a
  release installed under another name.

  **Breaking output change** (go-kure/launcher#988): under a Flux namespace every
  HelmRelease that authors no `storageNamespace` gains `spec.storageNamespace`. Left unset,
  helm-controller kept the release's Helm storage in the Flux namespace, where the
  HelmRelease sits. Where such a release is already installed, helm-controller reads the
  changed storage namespace as a changed release target: it uninstalls the existing release
  from the old storage namespace and installs it again in the new one (its
  `ReleaseTargetChanged` check), so the release's objects are deleted and recreated and its
  history starts over. To keep an installed release in place, author `storageNamespace` as
  the Flux namespace on a `helmrelease` component. Output without a Flux namespace is
  unchanged.

  **Values in a ConfigMap.** The HelmRelease is the only object `helmrelease` emits.
  Until go-kure/launcher#702 it took a launcher-owned `valuesMode: configMap` and emitted
  the values ConfigMap itself; that key is gone (see **Decoding**). The `helm`
  component's `valuesMode: configMap` produces the same ConfigMap through a `configmap`
  trait (see **helm**), and on a `helmrelease` authored directly the same result is a
  `configmap` trait plus a `valuesFrom` entry naming it.
- **helmtemplate** — the kind-named terminal for a client-side Helm render
  (go-kure/launcher#348, part of the Helm-family redesign go-kure/launcher#336), and what the
  role-named `helm` rule lowers to under `delivery: template`.
  It fetches and renders the chart at build time and emits the rendered manifests, each checked
  against the environment policy (see **Policy** below). It creates no source CR and no
  `HelmRelease`.

  **Properties.** `source` is
  required: `url` (required) is an `http://` or `https://` Helm repository URL, or an `oci://`
  URL naming the chart; `kind` (optional, `HelmRepository` or `OCIRepository`) is inferred from
  the scheme when unset — `oci://` is `OCIRepository`, anything else `HelmRepository` — and
  must agree with it when set. A `url` carrying a user or password is refused, since the render
  takes no credentials, and the message never repeats it. `chart` is required for a
  `HelmRepository`; an `OCIRepository`'s URL already names the chart, so there `chart` is not
  used. `version`
  is required for an `OCIRepository`. `values` is an open object, the Helm values tree, and must
  be representable as JSON: a non-finite number (`.nan`, `.inf`) is a build error. The source
  checks are shared with the `helm` rule's inline source rather than copied. `scopeOverrides`
  states the scope of a kind the chart renders (see **Scope overrides** below).
  `hookGroupNamePrefix` (go-kure/launcher#787) is the prefix of the names of the hook-group
  layouts, in place of `<application>-<component>`, and so of their Flux Kustomizations under
  `FluxIntegratedPerLayout`; so does a prefix the `Naming` hook returns for the `hook-group`
  role. With neither, the default prefix names only the layouts: kure names each Kustomization
  `<unit>-<layout name>` (go-kure/launcher#941). See the hook-group paragraph below.
  `layoutKustomizationName` (go-kure/launcher#787) is the name of the Flux Kustomization of
  the component's own layout, in place of `<bundle>-<component>`; see the same paragraph.

  `secretValues` (go-kure/launcher#786) is a second open object, for the sensitive part of the
  values tree; it is what the `helm` rule forwards its own `secretValues` as. The chart is
  rendered with `secretValues` merged over `values`, object by object, with the authored value
  types. The terminal emits nothing for it, so a value is in the output only where the chart
  renders it: a chart that puts it in a Secret emits that Secret in clear form, and one that
  puts it in a ConfigMap or an environment variable emits it there. The output is as sensitive
  as the input; see the security note under **helm**. Further:
  - a path set in both trees is refused, naming the path
    (`helmtemplate: auth.password is set in both values and secretValues; …`), by the rule the
    `helm` component states;
  - a key named `global` below the top level is refused, naming the path, before any fetch
    (`helmtemplate: secretValues: redis.global: a key named global is allowed only at the top
    level, …`), by the rule the `helm` component states and with its trade-off: such a key that
    is not a dependency's globals can be given through `values` only, and a sensitive global
    goes under the top-level `global`;
  - a policy that forbids explicit secrets (`oam.ExplicitSecretPolicy`) refuses the component
    in `ApplyPolicy`, before any fetch
    (`helmtemplate: secretValues is set and the environment policy forbids explicit secrets; …`).
    **Not covered:** a Secret the chart itself renders. It is emitted under such a policy, where
    the `passthrough` and `manifests` components refuse a Secret they carry: its content comes
    from the chart and its values, and most charts render one;
  - a render or decode failure is not reported as Helm reports it, since a template error can
    quote a value (`fail`, `required`, a YAML parse error showing the line). The chart is
    rendered a second time with `values` alone. If that fails too, its error is the one
    reported, after `rendering chart with secretValues failed; the cause is withheld because it
    can repeat a sensitive value. Without them it fails with: …`. If it succeeds, the message is
    only `rendering chart with secretValues failed, and without them it renders; the cause is
    withheld because it can repeat a sensitive value`. To debug such a chart, render it
    with placeholder values in `values`. The second render repeats the fetch;
  - *not covered:* an error about a rendered object. No check that runs on what the chart
    rendered, once it has decoded, scrubs its error. It names the object it refuses by kind
    and name, and can quote any part of the object it refuses or locates the refusal by. The
    policy checks (see **Policy**) quote a container's or a volume's name, an image reference,
    a resource quantity, a capability; the transform's component label quotes the key of a
    label that is not a string. So any part of a rendered object can reach an error: a chart
    that builds one from a sensitive value has it quoted there. The refusal of a field the kind's type does not
    declare is not among them: it is a decode failure, withheld as above. The refusal of a
    `scopeOverrides` entry that contradicts a CustomResourceDefinition the chart renders (see
    **Scope overrides** below) is such an error and is not withheld: it names the kind, the
    object's name and the two scopes, never a value;
  - Helm's own warnings about a values conflict quote the value Helm drops. With the refusal
    above that is never a value of `secretValues`: for a top-level `global` it is the
    dependency's `global` entry from `values`, and for a dependency's ordinary key it is the
    dependency's default. A test pins both, each way round, on the process's standard log and
    its default `slog` logger.

  **Release name.** `releaseName` is the render's `.Release.Name`. Unset, it is the default the
  `helmrelease` terminal writes to `spec.releaseName` (go-kure/launcher#785): the component
  name, and for a name over 53 characters, Flux helm-controller's shortened form of it — its
  first 40 characters, `-`, and the first 12 hex digits of the SHA-256 of the whole name. A
  `helm` component therefore renders under `delivery: template` with the same release name its
  HelmRelease is installed under with `delivery: flux`, whatever its target namespace. The chart
  and its source play no part. The name, authored or defaulted, must be a valid Helm release name
  by Helm's own rule — a DNS-1123 subdomain of at most 53 characters — since kure's render checks
  nothing. An authored name is never shortened: one over 53 characters is refused. A default that
  is not valid — the shortening can cut a dotted name just after a `.`, leaving a label that starts
  with `-` — is refused with the remedy to set `releaseName`, as Flux would fail to install it. A
  `HelmTemplateConfig` built directly defaults it from `Name` the same way, and is refused when
  both are empty. Since component names are unique in a document, two renders of one chart no
  longer share a release name by default. Two given the same `releaseName` in one namespace
  generate the same objects when the chart names its objects after the release, as most do, and
  `kurel build` refuses them as a generated-object collision rather than merging them. Before
  go-kure/launcher#776 every render used kure's default `release`; the project is pre-GA, so the
  changed object names of an existing template-rendered chart carry no compatibility shim.

  **Decoding.** `values`, `secretValues` and `scopeOverrides` are split off, and the rest of the property map is decoded with
  `builtin.DecodeStrictJSON` into a closed struct, so any other key, at any depth, is refused by
  name, as is a wrongly typed value. `values` itself reaches the render exactly as authored, with
  its YAML-decoded value types, rather than the strict decoder's `json.Number` re-reading, which
  a chart template comparing a value with a number would treat differently; `secretValues` likewise.
  A `secretValues` that is not an object is refused by its type, never by its content. The schema declares
  the same keys, with `source` closed to `url` and `kind`, and a test ties it to the struct. Keys
  match case-insensitively in the handler, as in `encoding/json`; schema validation, which a
  `kurel build` runs first, is exact.

  **Refused outright.** `targetNamespace`, every property only a Flux-reconciled release reads
  (`interval`, `driftDetection`, `install`, `upgrade`, `valuesFrom`, `valuesMode`), the `helm`
  rule's `delivery` switch, and a source reference (`source.name`, `source.namespace`) are
  undeclared keys: schema validation refuses each, and so does the strict decode. An
  `OCIRepository` source without `version` is refused by the handler. The key itself is what is
  refused, whatever it holds. The render's `.Release.Namespace` is the application namespace;
  the terminal declares no `targetNamespace`, so it cannot be set.

  **Namespace.** A namespaced rendered object that carries no `metadata.namespace` is given the
  application namespace, where a Helm install into that namespace would create it
  (go-kure/launcher#794, item 4). Each object's scope is resolved as the `manifests` component
  resolves it, by the one function both call (`resolveObjectScope`, `manifests.go`): a
  `scopeOverrides` entry for the object's kind, else kure's scope table (`manifest.Scope`) — the
  kinds kure registers, in any API version — plus the scope a `CustomResourceDefinition` among
  the emitted objects declares for the kind it defines. Unlike `manifests`, the render refuses
  nothing about what the chart wrote, since the application does not author a chart:
  - a namespace the chart wrote is kept, on a cluster-scoped object too, as Helm keeps it;
  - a cluster-scoped object without one stays without;
  - an object of unknown scope without one is left as rendered, with no namespace and no error:
    a kind kure does not register — a custom resource, or a built-in of an API group kure's
    scheme does not hold — with no CRD for it among the emitted objects and no `scopeOverrides`
    entry. `Lease`, `EndpointSlice` and the image-reflector kinds (`ImageRepository`,
    `ImagePolicy`) are registered, so one without a namespace is given the application
    namespace. Whoever applies the output decides where it lands (Flux's `targetNamespace`, a
    client's default namespace). A chart's `crds/` directory is not rendered, and a CRD under a
    dropped hook is not emitted, so neither gives a kind a scope.

  The policy check runs on the stamped objects, so a violation names an object with the
  namespace it is emitted in. A `HelmTemplateConfig` built directly with an empty `Namespace`
  has none to give and stamps nothing.

  **Scope overrides** (go-kure/launcher#794, item 11). `scopeOverrides` states the scope of a
  kind the chart renders, for a kind of unknown scope above: a list of `{apiVersion, kind,
  scope}`, `scope` being `Cluster` or `Namespaced`, matched on the exact `apiVersion` and
  `kind`. It is the property `manifests` has, read by the same parser (`parseScopeOverrides`)
  and resolved by the same function, so an entry means the same on both: it outranks kure's
  own table, is ignored for a kind the Kubernetes API itself scopes, and must agree with a
  `CustomResourceDefinition` among the emitted objects (see **crd / manifests**). A test holds
  the two components to the same answer for the same objects and overrides.
  - `Namespaced`: an object of the kind without `metadata.namespace` is given the application
    namespace, as a kind kure knows to be namespaced is.
  - `Cluster`: an object of the kind is left as rendered. That includes one the chart wrote a
    namespace on, which `manifests` refuses: template delivery refuses nothing about what a
    chart wrote.
  - An entry for a kind the chart does not render does nothing. `null` reads as omission.

  *Refused*, two cases only. A malformed entry, at decode and before any render, with the
  `manifests` messages under this component's prefix (`helmtemplate: scopeOverrides[0]: scope
  "cluster" is invalid; must be "Cluster" or "Namespaced"`). And an entry that contradicts a
  `CustomResourceDefinition` the chart renders for the kind, when the chart is rendered
  (`helmtemplate "<component>": object Widget "w": scopeOverrides says Cluster but the
  CustomResourceDefinition for Widget.fixtures.example.com the chart renders declares
  Namespaced; …`): that CRD defines the scope the cluster will serve, so no cluster can honour
  the entry. The message names the object's kind and name and the two scopes, nothing else of
  the object. A `HelmTemplateConfig` built directly holds the overrides in `ScopeOverrides`,
  where a value other than the two scopes is refused before the render.

  The `helm` rule takes the same property under `delivery: template` and forwards it here as
  written, after refusing a malformed entry under its own prefix; under `delivery: flux` it
  refuses the property (see **helm**).

  *Not covered:* no policy or placement check on template output reads an object's scope, so
  the overrides change the namespace stamp and nothing else.
  Not breaking: a document that does not use the property renders as before.

  **Breaking output change** (go-kure/launcher#794): such objects gain `metadata.namespace` in
  the output. Before, a chart that left it unset rendered namespace-less objects, which landed
  wherever the applying client defaulted them.

  **Breaking output change** (go-kure/launcher#790, with the kure version that registers
  them): `Lease`, `EndpointSlice`, `PriorityClass`, `RuntimeClass`, `APIService`, the
  `admissionregistration.k8s.io/v1` kinds and the image-reflector kinds are read as their
  Go types, no longer as kinds the build does not know. One that sets only fields its type
  declares is emitted from that type; one that sets a field the type does not declare is
  emitted as rendered, the field kept (*Undeclared fields*, below); a field of the wrong type
  is now a build error. The namespaced ones (`Lease`, `EndpointSlice`, `ImageRepository`,
  `ImagePolicy`) gain `metadata.namespace` when the chart left it unset. A chart that renders
  a `v1` `List` or a typed list now builds, and each of its items is held to the policy and
  to *Undeclared fields* as a document of its own is. A list of a kind the scheme does not
  register no longer builds when a `helm.sh/hook` annotation is involved, on the list or on
  an item (see "Rendered objects"): it built before, with a wrong output, the list's hook
  lost and its items emitted as ordinary resources.

  **Breaking** (go-kure/launcher#790, with the kure version that reads a list in one place,
  go-kure/kure#1014), for template delivery, `manifests` and `crd` alike. Built before,
  refused now:

  | Document | Before | Now |
  |---|---|---|
  | A list (a `v1` `List`, a typed list, a list of an unregistered kind) with a label or an annotation on its own metadata | Built: the items emitted, the list's metadata dropped. Template delivery refused only `helm.sh/hook` there, and still does, with its own text | `List has metadata of its own that its items cannot keep: annotations …; labels …` |
  | `Kind` or `apiversion` beside the exact key, on a document or on an item of a list | Built, with two exceptions: emitted as written, the extra key kept. A workload or a claim was refused already, by *Undeclared fields* (`undeclared field Kind`), and so was an item of a typed list whose `Kind` or `apiversion` states another value than the list holds, by the parser (`the item states …, the list holds …`). Both are refused now with the other text | `the key "Kind" equals "kind" only after case folding; write it "kind" or remove it` |
  | `Items` on a `v1` `List` | Decoded, to no object at all: a chart or a source with other objects built. A `manifests` or `crd` source that held nothing else was refused already (`source resolved to no manifests`) | The same text, for `"Items"` |
  | `Items` on a list of an unregistered kind | Built: the list emitted as one object | The same text |
  | A `null` item in a list of an unregistered kind | Built: an otherwise empty object emitted, with the list's `apiVersion` and its kind without `List` | `item 0 of WidgetList: the item is null, not an object` |
  | An item of a registered kind, in a list of an unregistered kind, that does not decode as its type (`data: 1` on a ConfigMap) | Built: the item emitted as written. A workload, a claim or a PersistentVolume there did not build: the policy check refused it as unreadable, and it is refused now for the decode | The item's decode error, naming its position |
  | In a JSON document, an item of a registered kind, in a list of an unregistered kind, that states `apiVersion` or `kind` and then `null` for it, where the stated value is not the one its list gives it | Built: the item emitted as written. A workload, a claim or a PersistentVolume there did not build: the policy check refused it as unreadable | `item 0 of FooList: Deployment "web": the object was read as apps/v1 Deployment, and the decode that checks its fields reads the document as example.com/v1 Deployment, so its fields cannot be checked; …` (*Undeclared fields*, below) |
  | An object of an unregistered kind that does not end in `List`, with a top-level `items` array | Built: the entries emitted in its place, each unstructured | `… an `items` array on an object of a kind that is no list (example.com/v1 Widget) is read as a list by what applies the output …` |

  Not refusals, and a change of output all the same: an item of a registered kind in a list of
  an unregistered kind is read as its Go type, as in a `v1` `List`, where it was emitted as
  written. It is emitted from that type, *Undeclared fields* applies to it, and the policy
  check reads it: a workload, a claim or a PersistentVolume there was refused as unreadable
  and is now checked, and builds when the policy allows it. A list inside such a list was
  left whole, and refused by the policy check; it is opened in turn.

  Texts that changed: a `null` item of a `v1` `List` was `nil runtime object provided` and is
  `the item is null, not an object`; `… is nested more than 8 lists deep` now holds for a
  list of an unregistered kind too; the policy check's refusal of an object with a top-level
  `items` array no longer says the object `sits inside a list of an unregistered kind`, and
  no decode returns such an object any more (*Limits*, below).

  **Output order.** Every rendered manifest carrying a `helm.sh/hook` annotation (or a standalone
  `helm.sh/hook-weight`) is grouped by `(phase, weight)` via kure's `helm.SplitByHookWeight`.
  `Generate` returns every rendered object flat, in hook-execution order —
  `pre-install, pre-upgrade, main, post-install, post-upgrade, <unknown, alphabetical>` — with a
  multi-event annotation such as `pre-install,pre-upgrade` placed by its earliest phase; a chart
  with no hook annotations at all yields one group, in chart-render order.
  `pre-delete`/`post-delete`/`pre-rollback`/`post-rollback`/`test` objects are dropped outright
  (kure does not surface them as static manifests) — mostly a bug fix, since a `test` Pod rendered
  as a static GitOps object would otherwise be reconciled on every apply, but it is silent data
  loss for a chart that relies on one of those hooks; `pkg/oam` has no logging channel to flag it.

  **Rendered objects.** The render is decoded with kure's manifest parser
  (`io.ParseYAMLWithOptions`, unstructured allowed), the decode the `manifests` component uses
  (go-kure/launcher#791). An object of a kind kure's scheme registers is emitted as its Go type —
  a Deployment as `*appsv1.Deployment`, a hook Job as `*batchv1.Job` — and any other as
  unstructured. What follows from reading a document as the API server does:
  - a field the registered type does not declare is not dropped, as the parser alone would drop
    it: the object is refused or emitted as rendered (*Undeclared fields*, below). A value of
    the wrong type (an unquoted `true` as an annotation value) is a build error;
  - YAML is read as YAML 1.1, so an unquoted `yes` or `y` is a boolean; a mapping key that is not
    a string (`1:`) becomes its string form, and an unquoted timestamp stays the string the chart
    wrote; in an unstructured object an integer an int64 cannot hold becomes a float;
  - a large integer survives the write where the object holds it as an integer. Since
    go-kure/kure#1006 kure's manifest writer writes an integer field of a typed object, and an
    integer in an unstructured one, with its own digits: `9007199254740993` and
    `9223372036854775807` are written as rendered (they were written `9007199254740992` and
    `9223372036854776000`). That holds for every object launcher writes, whichever component
    emitted it. A number the object holds as a float is written as that float, with the
    shortest digits that read back as it, and an integer an int64 cannot hold is such a float
    in an unstructured object (the item above): `9223372036854775808` is written
    `9223372036854776000`, and `18446744073709551615` as `1.8446744073709552e+19`. A value
    that large has to be a string in a field that takes one;
  - an empty, null or comment-only document is skipped, while a scalar, a sequence, `{}` and a
    mapping without `apiVersion` and `kind` are build errors — in a document
    of a dropped hook as well, since the render is decoded before hooks are grouped;
  - a list document is replaced by its items, in the list's order. What a list is, is kure's
    parser's to say, in one place since go-kure/kure#1014: a list kind the scheme registers
    (a `v1` `List`, a typed list such as `DeploymentList`), and a kind the scheme does not
    register whose name ends in `List` and that states `items`. Each item of a typed list is
    decoded as the kind the list holds. Each item of a `v1` `List` and of a list of an
    unregistered kind is decoded as a document of its own: typed when its kind is registered,
    unstructured otherwise, and a list among them is replaced by its items in turn, to the
    eight levels the parser bounds. An item of a list of an unregistered kind that leaves
    `apiVersion` or `kind` out is read with the list's `apiVersion` and the list's kind
    without `List`. An item that does not decode, or is `null`, is a build error naming its
    position (`item 0 of WidgetList: the item is null, not an object`). A list is never
    emitted, so what it states on its own metadata would be lost: a label or an annotation
    there is the parser's error (`List has metadata of its own that its items cannot keep:
    annotations helm.sh/resource-policy`). A kind the scheme does not register that ends in
    `List` and states no `items` is one object; a registered list kind that states none is a
    list without items, and yields no object;
  - `apiVersion`, `kind` and, on a list, `items` are read under those exact keys, and a key
    that equals one of them only after case folding (`Kind`, `apiversion`, `Items`) is the
    parser's error, on a document and on an item of a list (`the key "Kind" equals "kind" only
    after case folding; write it "kind" or remove it`): the readers of a document downstream
    do not agree on which of two such keys stands;
  - an object of a kind the scheme does not register and that does not end in `List`, with a
    top-level `items` array (`{kind: Widget, items: [...]}`), is a build error, with a policy
    or with none, at the top of the render and as an item of a list (`Widget "w": an `items`
    array on an object of a kind that is no list (example.com/v1 Widget) is read as a list by
    what applies the output, which would apply its entries in the object's place; write the
    entries as documents of their own, or give the object a kind ending in List`). The parser
    reads such a document as one object. What applies the output tells a list by that array
    and not by the kind, so the entries would reach the cluster without any check here having
    read them. It is the envelope `passthrough` refuses (**Lists**, under `passthrough`), and
    the one refused on a registered kind that declares no `items` (*Undeclared fields*,
    below). An `items` that is no array, or one below the top level, is the object's own
    content. The refusal is no refusal by the policy and carries no class. Under template
    delivery it is still the component's `oam.ViolationError`, as every render that does not
    decode is, since the chart is rendered at the transform's policy step; a `manifests`
    source reports it as its parse error, without one;
  - a document the decoder of its kind panics on is a build error, not a crash. The API type
    of a registered kind may decode itself and not handle what was written: Cilium's ICMP
    field dereferences the `type` an `icmps` field of a `CiliumNetworkPolicy` or a
    `CiliumClusterwideNetworkPolicy` left out. Since go-kure/kure#1009 kure's parser reports
    the panic itself, as that document's parse error. The error names the object by its kind
    and its name and holds the panic (`decoding rendered manifests: parse error in Kubernetes
    object: failed to decode object: the decoder panicked on CiliumNetworkPolicy "demo/p":
    runtime error: invalid memory address or nil pointer dereference`); it names neither the
    field nor the document's position, and an item of a list by its position in the list
    (`item 0 of List`). Such a document is one bad document among the others: the error holds
    every document's own, and the YAML error of input that stops being YAML further down.
    Before that change launcher caught the panic itself, named the document by its position
    (`document 2 (CiliumNetworkPolicy "demo/p")`) and reported that document alone;
  - malformed JSON the decoder cannot read past is a build error: `{]`, alone or after JSON
    documents. What follows it in the input is not read. Before go-kure/kure#1012 kure's
    parser did not return on such input, and neither did the build;
  - which kinds are registered is kure's to say. Since go-kure/kure#1007 a MetalLB `BGPPeer`
    at `metallb.io/v1beta2`, the version MetalLB stores, is one, beside `v1beta1`.
    **Breaking** for a chart or a manifest source that holds such a document, which was
    emitted as rendered: it is now emitted as its Go type, so `spec.passwordSecret: {}` is
    written where the document left the field out, and a field of the wrong type
    (`holdTime: [1]`) is a build error. One that sets an undeclared field is still emitted as
    rendered (*Undeclared fields*, below);
  - a list document where a `helm.sh/hook` annotation is involved is a build error naming the
    list: the annotation on the list's own metadata, or on one of its items (for a `v1` `List`
    and a list of an unregistered kind, whose items are documents of their own, at every
    depth the parser opens). Helm reads a hook on the rendered document's own
    metadata and nowhere else, and the parser reads only a list's items. So the items of a hook
    list would be emitted as ordinary resources, and an item's own annotation, which Helm does
    not read, would group it as a hook or drop it. The check reads a list as the parser does,
    with one addition: Helm reads `metadata` under any case of the key and the parser reads
    the exact key, so a hook on a list under `Metadata` is refused here. A list without the
    annotation, and without any other label or annotation of its own, builds; an object that
    is not a list keeps its hook.

  A decode failure is reported as `decoding rendered manifests: …`. `Generate` returns a fresh
  copy of the decoded objects on every call, as `manifests` does, so a trait that decorates a
  typed workload (`topology-spread`, `security-context`, a mounted `configmap`,
  `external-secret`) acts on a chart's Deployment as on an authored one, and generating the
  same result twice gives the same output.

  **Policy.** `ApplyPolicy` holds the chart to the environment policy in two steps
  (go-kure/launcher#791); the transform calls it with `NoopPolicy` when no policy is passed.
  - *The source, before any fetch.* The host of `source.url` must match an entry of the policy's
    allowed registries (`AllowedRegistries`) exactly, as for `oci`, `crd` and `manifests`; for an
    `oci://` URL that is its first segment, which is what the Helm registry client pulls from. A
    refused source fails before any request is made: only an allowed source is rendered. No
    allowlist, or an empty one, permits every host.
  - *Every emitted workload.* The chart is then rendered — in the transform, so a fetch or render
    failure is reported there, as that component's policy error — and each Pod, PodTemplate,
    ReplicationController, Deployment, StatefulSet, DaemonSet, ReplicaSet, Job and CronJob in it,
    a kept hook's included, is checked as an authored workload is: host namespaces, hostPath
    volumes, the cpu/memory maxima, the storage maximum on a generic ephemeral volume's claim,
    the registry allowlist on an image volume's reference (**breaking**, go-kure/launcher#790:
    a chart that renders one from a registry outside the list built before; see *Image volumes*
    under **pod**),
    and for every init and regular container the registry allowlist, the privileged, HostProcess
    and capability gates, and `ValidateImageRef` (no untagged image, no `:latest`), which holds
    the reference of an image volume too (**breaking**, go-kure/launcher#790: a chart that
    renders an untagged or `:latest` one built before). Ephemeral
    containers are refused. The storage a PersistentVolumeClaim, or a StatefulSet's claim
    template, requests is held to the storage maximum (`MaxStorageSize`), as the
    `persistentvolumeclaim` and `statefulset` kinds hold theirs. The replica count of a
    Deployment, StatefulSet, ReplicaSet or ReplicationController (one when the chart sets none)
    and the `maxReplicas` of a HorizontalPodAutoscaler, in any API version, are held to the
    replica maximum (`MaxReplicas`), as the `deployment` and `statefulset` kinds and the
    `scaler` trait hold theirs. A PersistentVolume is held to what the `persistentvolume`
    kind holds its own to: a `hostPath` or `local` source needs `AllowHostPathVolumes()`,
    and `spec.capacity.storage` is held to the storage maximum (breaking for a chart that
    renders one; see **persistentvolume**). The error names the rendered
    object and the field (`helmtemplate: rendered Deployment "demo/web":
    spec.template.spec.containers[0] "app": …`).

  **Behaviour change:** a chart that renders a privileged container, a host namespace or a
  hostPath volume no longer builds unless the policy allows it — and with no policy passed
  nothing allows it, since `NoopPolicy` denies all five. The policy accessors that allow such a
  chart are `AllowPrivileged()`, `AllowHostNetwork()`, `AllowHostPID()`, `AllowHostIPC()` and
  `AllowHostPathVolumes()`; there is no per-chart exemption. A chart image without a tag, or
  tagged `:latest`, has to be pinned through the chart's values.

  **Undeclared fields** (go-kure/launcher#794, item 7). Kure's parser decodes a registered kind
  leniently: a field the vendored API type does not declare — one a newer Kubernetes version
  added, say — is dropped, with no error. The policy check reads that type, so it would pass a
  workload whose undeclared pod spec field it never saw, and the object would be written
  without the field. Template delivery, `manifests`, `crd` and `passthrough` therefore decode
  each document of a registered kind once more, strictly, over the same scheme, and act on
  what that reports:
  - *A workload or a claim* — the kinds the policy check reads as Go types: Pod, PodTemplate,
    ReplicationController, Deployment, StatefulSet, DaemonSet, ReplicaSet, Job, CronJob,
    PersistentVolumeClaim and PersistentVolume, which "a workload or a claim" stands for
    wherever this rule is cited — that sets an undeclared field is refused, under any policy
    and with none. A kind the check comes to read as its Go type joins them. The error names the object and the path of every such field (`decoding rendered
    manifests: Deployment "demo/web": undeclared field
    spec.template.spec.fieldOfALaterVersion: the apps/v1 Deployment type this build reads the
    object with does not declare it, so the object cannot be checked against environment
    policy`).
  - *Any other registered kind* is emitted as the document was rendered, unstructured, the
    field kept. A HorizontalPodAutoscaler is one of these: the check reads its `maxReplicas`
    from the unstructured object as it does from the Go type. One shape is refused: a
    top-level `items` array on a kind that declares none, since written out the object is a
    list to whatever applies it, and its items would be applied in its place.

  A list (a `v1` `List`, a typed list, a list of an unregistered kind) is replaced by its
  items, so the rule is each item's: an item is refused or kept as a document of its own is,
  the error naming its position (`decoding rendered manifests: item 1 of List: Deployment
  "demo/web": undeclared field …`; `item 0 of WidgetList: item 0 of List: …` for a list in a
  list). A kept item that left `apiVersion` and `kind` out is written with the ones its list
  gives it: the kind a typed list holds, and for a list of an unregistered kind the list's
  `apiVersion` and its kind without `List`. The list's other fields are not read: the list
  is never emitted. A document whose items cannot be matched to the objects the parser made
  of them is refused, not passed with an item unread.

  A key written twice is not an undeclared field and is read as before (the last value
  stands). One case of it is refused, whatever the kind: a JSON document that writes so
  many keys twice that the strict decode's record of errors, which holds a hundred, is
  full without naming an undeclared field. Whether such a document also sets one cannot
  be told. (A document whose undeclared field is recorded before the record fills is
  treated as any other: refused as a workload or a claim, kept as another kind.)

  Another is an item of a list of an unregistered kind, in a JSON document, that states
  `apiVersion` or `kind` and then `null` for it. The parser reads the last statement, takes
  the `null` for the key left out and gives the item its list's; the strict decode keeps the
  string the `null` follows. Where that string is what the list gives too, the two agree
  and nothing changes. Where it is not, the strict decode checks the fields of another kind
  than the one emitted, and an item the parser made a Go type of is refused, with or without
  an undeclared field
  (`item 0 of FooList: Deployment "web": the object was read as apps/v1 Deployment, and the
  decode that checks its fields reads the document as example.com/v1 Deployment, so its
  fields cannot be checked; a second apiVersion or kind that is null is the known cause,
  state each once`). The parser refuses the same in an item of a typed list itself. An item
  the parser leaves untyped, its kind not registered at the list's `apiVersion`, is emitted
  as the parser read it and is not checked as the kind the strict decode would read: nothing
  of an unstructured object is dropped.

  Whether a cluster accepts a kept field its own version does not know is not verified
  here; the object reaches it as the chart wrote it.

  **Breaking**, in two ways. A chart that renders a workload or a claim with an undeclared
  field built before, the field dropped from the output, and no longer builds: the field has
  to go, or wait for a launcher whose pinned API types declare it. And the output of every
  other registered kind gains the fields the typed decode had dropped since
  go-kure/launcher#791; such an object is written from the document and not from its Go type,
  so the rest of it is as the chart wrote it too.

  Known limit: a type that unmarshals itself decodes its own keys, so an undeclared key inside
  one is not reported and is still dropped. A CustomResourceDefinition's `items` schema is
  such a type: the same kind of type as a `CiliumNetworkPolicy`'s endpoint selector, which
  the kind inventory records and an authored Cilium policy checks on its own. A key next to
  `properties` in the same schema is reported and kept. No type under a workload or a claim does this beyond scalar
  leaves (`Quantity`, `IntOrString`, `Time`) and the raw field set of `managedFields`; a test
  walks those types and fails when one starts to.

  Limits. The items of a list, whatever its kind, are checked as documents of their own are,
  a list among them opened in turn.
  A workload, claim or PersistentVolume in an API version kure's scheme
  does not register
  (`batch/v1beta1`, `apps/v1beta2`) cannot be read and
  is refused rather than passed unchecked, inside a list as outside one. The decode returns no object with a
  top-level `items` array (*Rendered objects*, above), so the check meets none; one that reaches it
  another way is refused as unreadable. Not checked: an object of a dropped hook (never emitted); the pods a
  custom resource's controller creates, and the replica count a custom resource sets; the host of the chart archive a Helm repository's index
  points at, and any redirect, which kure's renderer follows. That one is a decided limit
  (go-kure/launcher#794, item 6): the allowlist is checked on the chart's source URL before
  anything is fetched, and holding every later request to it needs a hook in kure's chart
  renderer, which takes a release name and a namespace and nothing about its requests
  (`helm.RenderChart`). A nil policy (a direct
  `ApplyPolicy(nil)`, or `Generate` on a config no policy was applied to) checks nothing, and
  `ApplyPolicy(nil)` withdraws no policy applied before. A chart
  delivered as a Flux `HelmRelease` (`helmrelease`, `helm` under `delivery: flux`) is rendered
  on the cluster, so nothing it renders can be checked at build time; only the host of a source
  component in the document is.

  **Hook-group layout.** For a layout-walking consumer (the `layout.LayoutAugmenter` path), more
  than one hook group makes `AugmentLayout` clear the component's flat `Resources` and replace
  them with one child `ManifestLayout` per group, named
  `<application>-<component>-NN-<phase-slug>`, written to
  its own directory `<component dir>/<child>` (its `Namespace` is the component layout's own path,
  which kure joins with the child's name, so a hook-group directory is never nested twice) and
  chained via `DependsOn`, listing each child's preceding sibling in the order kure's
  `helm.SplitByHookWeight` synthesizes from Helm's hook phases — a combined install/upgrade
  ordering for GitOps reconciliation, not Helm's own per-operation execution order (kure
  `pkg/stack/helm/hooks.go:28-36`). `DependsOn` is set on every child, and only kure's
  `FluxIntegratedPerLayout` placement carries it: there kure's layout integrator gives each child
  a Flux Kustomization CR of its own and writes each entry into its `spec.dependsOn` as the name
  of the Kustomization of the sibling the entry names (kure `pkg/stack/layout/manifest.go`'s
  `DependsOn` field doc). Under `FluxSeparate` and `FluxIntegratedPerBundle` a child gets no
  Kustomization of its own, and kure's Flux integration refuses the child's `DependsOn` and
  `KustomizationName`, naming the child (go-kure/kure#1032); before, it dropped them without an
  error, and the order between hook groups was lost. A chart with more than one hook group
  therefore needs `FluxIntegratedPerLayout` under kure's Flux integration. When it
  partitions, `AugmentLayout` also sets the component layout's `ApplicationFileMode` to
  `AppFilePerResource` unless the caller already set one, so the component stays a directory
  whose `kustomization.yaml` lists the children. Without that, a writer-wide `AppFileSingle`
  default (kure `layout.Config.ApplicationFileMode`) would make kure's `WriteManifest` refuse the
  tree under any placement but `FluxIntegratedPerLayout`: an `AppFileSingle` layout writes no
  `kustomization.yaml`, so nothing would list its child layouts (`go-kure/launcher#563`). The
  children carry no mode of their own, so under such a default each hook group is written as one
  file, `<component dir>/<child>.yaml`, instead of a sub-directory, and the component's
  `kustomization.yaml` lists it; under `FluxIntegratedPerLayout` kure's layout integrator keeps
  every child a directory with its own Flux Kustomization, whatever the default. kure names
  that Kustomization `<bundle's Kustomization>-<child>` and shortens it past 63 characters by
  its own rule (go-kure/kure#1030, go-kure/kure#1036), while the child's name, its directory,
  is held to 253 (`site/content/api-reference/oam/pipeline.md`, "Pipeline"). A single-group
  chart's `AugmentLayout` is a no-op. Every `helmtemplate` component is a `LayoutAugmenter`, a
  hook-free chart included, since the group count is known only after the render — so even a
  hook-free chart gets its own sub-layout directory under a layout-walking consumer, for no
  behavioural benefit. The child name begins with the application name
  (go-kure/launcher#792): component names are unique only within one Application, while the
  Kustomization CRs a consumer generates for the hook-group children of every application can
  share one namespace, so two differently named Applications that each have a component `db`
  get `<application>-db-NN-<phase-slug>` children that differ. The transform hands the config its
  application (`oam.ApplicationNameSetter`, the `Application` field); a `HelmTemplateConfig`
  built directly, outside a transform, has none unless the caller sets the field, and its
  children are then named `<layout name>-NN-<phase-slug>`. **Breaking output change**: every
  hook-group child name, and so its directory and the Flux Kustomization a consumer derives
  from it, gains the leading `<application>-`. The Kustomization kure generates for the child
  under `FluxIntegratedPerLayout` is `<bundle's Kustomization>-<child>`
  (`shop-shop-db-01-main`), and the next group's `spec.dependsOn` follows it. **Breaking output
  change** (go-kure/launcher#941): from go-kure/launcher#787 launcher named it by the child
  alone (`shop-db-01-main`) and shortened it to 63 characters itself; it now leaves the name to
  kure, whose `<unit>-` keeps two applications of one name in different bundles apart, and
  which refuses the name under the coarser placements (go-kure/kure#1032). A layout name over
  253 characters is shortened by the one shortening rule (`oam.ShortenNameWithSuffix`, which
  `helm` uses for its values ConfigMap name): the `-NN-<phase-slug>` suffix is kept whole and `<application>-<component>` becomes
  its own beginning plus 10 hex digits of its sha256 (8 before go-kure/launcher#793), so two
  long names that differ anywhere almost never shorten to the same one. Known limitation: the
  two names are joined by a plain `-`, which either may contain, so application `a-b` with
  component `c` and application `a` with component `b-c` still get the same child names. The
  default carries no namespace either: like the bundle name, a child name is built from the
  application's name alone, so two Applications with one name in different namespaces get the
  same child names. Keeping those apart is the author's or the consumer's: the
  `hookGroupNamePrefix` property, or the `Naming` hook's answer for the `hook-group` role
  (go-kure/launcher#787), replaces `<application>-<component>` in the name of every child,
  directory and Kustomization alike. Such a prefix is a DNS-1123 subdomain and is never
  shortened: a child name over 63 characters built from it fails the transform, in an error
  with the component, the role and the full name (`*oam.HookGroupNameError`, which answers
  to `oam.ErrHookGroupNameTooLong`), and two components of one document that
  resolve to one prefix fail the transform. The transform has the chart rendered by its
  policy step, with or without a policy of the caller's, and asks the config then
  (`CheckHookGroupNames`); `AugmentLayout` returns the same error for a config built
  directly or a prefix set after the transform. Known limit: the transform holds the prefixes
  apart, not the names built from them after the render, so two different prefixes can still
  give one Kustomization name (a written prefix that equals kure's name for another
  component's default child, less its suffix; a prefix that ends as another chart's phase
  begins). kure refuses that name, used twice, when the
  walked tree is integrated; another `hookGroupNamePrefix` on one of the components is the
  way out (`site/content/api-reference/oam/names-and-overrides.md`, "Name roles and the `Naming` hook"). A `HelmTemplateConfig` built directly sets `HookGroupNamePrefix`.
  The component's own layout, the parent of the hook-group children, also gets a Flux
  Kustomization under `FluxIntegratedPerLayout`, which kure names `<bundle>-<component>` and
  shortens past 63 characters by its own rule; launcher leaves that default to kure.
  **Breaking output change** (go-kure/launcher#941): before, the transform shortened a default
  over 63 characters by launcher's rule and `AugmentLayout` set it on the layout, so such a
  layout's Kustomization name changes. The `layoutKustomizationName` property, or the
  `Naming` hook's answer for the `layout` role, sets another name, a DNS-1123 subdomain of at
  most 63 characters used as written and never shortened; any other fails the transform,
  naming the component and the role. A name set on the layout needs per-layout placement:
  under any other the layout gets no Kustomization of its own, and kure's Flux integration
  refuses the name, naming the layout (go-kure/kure#1032). A
  `HelmTemplateConfig` built directly sets `LayoutKustomizationName`; `AugmentLayout` never
  overwrites a `KustomizationName` the layout already carries. `GenerateCoversAugmentLayout` is always true —
  `Generate`'s output is already the flat union `AugmentLayout` repartitions — so `kurel build`,
  which never walks a layout, accepts the component and emits `Generate`'s flat output.
- **oci** — `source.url` (`oci://…`), `source.name` (a name for the generated
  OCIRepository, see below), `source.objectName` (a name for the OCIRepository
  the component keeps to itself, see below), `kustomizationName` (a name for
  the Kustomization, see below), the source's registry access
  `source.secretRef`, `source.provider`, `source.insecure`,
  `source.certSecretRef`, `source.proxySecretRef` (go-kure/launcher#999) and
  `source.serviceAccountName` (go-kure/launcher#1009),
  `version` (tag or `sha256:…`), `path`, `prune`, `interval`,
  `targetNamespace`, `wait`, `healthChecks`.

  **Lowering (go-kure/launcher#784).** `oci` is a role-named component: `OCIRule`
  lowers it to the two kind-named terminals it stands for, an `ocirepository`
  (`url`, `ref.tag` from `version`, or `ref.digest` for a `sha256:` value, the
  authored `interval`, and each registry access key that is set, as written)
  and a `fluxcd-kustomization` (`path`, `prune`,
  `sourceRef`, and `interval`, `targetNamespace`, `wait` and `healthChecks`
  when set). No handler is registered for `oci`. The rule runs the checks
  below first, with their `oci:` messages; everything past them is the
  terminals' own.
  Each Secret reference of the registry access names a Secret in the
  namespace the OCIRepository lands in, the Flux namespace when one is set,
  as on `ocirepository`; `source.serviceAccountName` names a ServiceAccount
  in that namespace. A Secret-making trait (`secret`,
  `external-secret`, `certificate`) goes to the Kustomization, not the source.
  The Secret it makes moves to the Flux namespace when the source reads it,
  whichever form the source takes: the source the component keeps to itself,
  as the two land there together (go-kure/launcher#1012), or one generated on
  its own, which several components share or `source.name` names, and which
  every consumer is ordered after (go-kure/launcher#1016). A workload that
  needs the Secret in the application namespace gets a second Secret of its
  own; or provide the Secret in the Flux namespace. One consumer making it is
  enough; two consumers making the same Secret collide. A consumer that
  makes it in a later group than another consumer of the same source (a
  `dependency` policy orders it after that one) is admitted under the
  default delivery, which converges. Under
  `TransformContext.GroupsWaitForFluxObjects`, a delivery that makes a group
  Ready only once its Flux objects are, it never would: the earlier
  consumer waits for the source, the source for the Secret, and the
  Secret's group for the earlier one. The transform then refuses it
  (go-kure/launcher#1039): put the trait on a consumer in the earliest
  group, or provide the Secret apart from the document. The same holds for
  any Flux object that requires a ConfigMap or Secret a trait makes in a
  later group, whichever component carries the trait (go-kure/launcher#1079;
  see each kind's *Required reads*), and for one a component of a later
  group makes: a `secret` or `configmap` component, a `certificate`'s
  `secretName`, an `externalsecret`'s target, where it lands with the
  source (go-kure/launcher#1086): place the component in the group of the
  earliest consumer or an earlier one. An
  `external-secret` under `target.creationPolicy: Merge` or `None` makes no
  Secret and is admitted; so is a `verify.trustedRootSecretRef` under a
  provider other than `cosign`, which source-controller does not read.
  - *A component alone on its artifact* lowers to a same-name group: both
    objects are named after the component (unless `kustomizationName` or
    `source.objectName` names them, below) and deploy as one unit, the
    OCIRepository first. This is the pair of objects `oci` has always emitted,
    byte for byte. Annotations go to both members, so a tier annotation, a
    `placement` policy or a `dependency` rule naming the component acts on the
    pair. `prune-protection` and `force-replace` cover both objects (the
    pair's application takes the delivery intent); every other trait goes to
    the Kustomization.
  - *Two or more `oci` components of one document on the same source* share
    it. The source then belongs to the document, not to the component that
    comes first: it is emitted once, named `<document>-source-<digest>` (the
    10-hex digest of the source identity, through
    `LoweringContext.ResolveSharedName`, which shortens a default over 253
    characters by the one rule), and each component lowers to its Kustomization alone,
    referencing it and ordered after it (`Component.OrderAfter`). Like a
    source `helm` generates, it is then a generated source: `pkg/oam` applies
    it with the application bundle, ahead of every group, wherever its
    consumers are placed. It carries no trait and no annotation of any
    consumer, and may not be placed in a tier or made to wait on a component.
    The consumer's `Naming` hook may rename it: it is asked under role
    `helm-source`, once for each source identity of a document, with that
    default and no component (go-kure/launcher#787).
  - *`source.name` names the source* (go-kure/launcher#787), and makes it the
    shared form whatever the number of consumers: the OCIRepository is emitted
    once under that name, applied with the application bundle ahead of every
    group, and the component lowers to its Kustomization alone. **A source
    named this way carries no annotations and no traits of the component**, not
    even for a component alone on its artifact: `prune-protection` and
    `force-replace` then cover the Kustomization only, and a tier annotation
    places only the Kustomization. The name is the component's own choice.
    Components that write the same name for the same identity share the source;
    a component that writes none does not share it and is not counted among
    its consumers, so it keeps its own source, or shares the document's with
    the other components that name none. A name equal to that shared source's
    own names that source: one name for one identity is one source. The name
    is used as written: it must
    be a DNS-1123 subdomain of at most 253 characters, and cannot be the
    component's own name or that of another component of the document (`oci:
    source.name "base" is the component's own name; the generated source is a
    component of the document too, so give it another name`). One name for two
    identities is refused as a collision. `source.name: ""` reads as absent.
  - *`kustomizationName` and `source.objectName` name the two objects that
    carry the component's name* (go-kure/launcher#787): the Kustomization, in
    both forms, and the source a component keeps to itself. Each is the
    author's property, else the consumer's `Naming` hook's answer (roles
    `oci-kustomization` and `oci-source`, asked with the component), else the
    component name, so a document that sets neither and a hook that declines
    give the same output as before. A name that is not the default is used as
    written: a DNS-1123 subdomain of at most 253 characters, refused otherwise
    and never shortened. It names the object alone. Both members keep the
    component's name, so the pair stays one unit: a tier annotation, a
    `placement` policy or a `dependency` rule naming the component still acts
    on both, and the traits go where they went. The Kustomization's `sourceRef`
    names the kept source by the name that object takes. Every one of these
    names is claimed, the default included, so an authored `ocirepository` or
    `fluxcd-kustomization` component whose `objectName` is the same name is
    refused in the transform with both named:

    ```
    name collision: OCIRepository.source.toolkit.fluxcd.io "default/manifests" is named by component "manifests" (role "oci-source", its default) and by component "repo" (role "object", set by properties.objectName); give one of them another name
    ```

    `source.objectName` and `source.name` both name the OCIRepository, in two
    ways, and are refused together. Write `source.objectName` to rename the
    source the component keeps to itself: it stays with the Kustomization and
    carries the component's annotations and its `prune-protection` and
    `force-replace` traits. Write `source.name` for a source generated on its
    own, which components that write the same name share. A component that
    writes `source.objectName` keeps its own source whatever other component
    has the same identity, and is not counted among the consumers of a shared
    one.
  - *The source identity* is the `url`, the `version`, the effective
    `interval` and the registry access. Unset, `0s`, `60m` and `1h` are one
    interval. Components whose intervals differ do not share: each keeps a
    source of its own, polling at its own interval, as a component alone on
    its artifact does. Nor do components whose access differs in any key, so
    no component pulls with another's credentials, provider, ServiceAccount
    or TLS settings; one `source.name` written with two accesses is refused
    as one name for two identities. An unset key (a `null` reference, an
    empty `provider` or `serviceAccountName`, `insecure: false`) is no access and leaves the identity, and a shared
    source's name, as they were before these keys; an authored `{}`
    reference is access of its own, and `provider: generic` differs from no
    provider.
    An `oci` and a `helm` component on one artifact never share either: the
    identities differ, and a Helm source copies the chart layer instead of
    extracting it.

  **Output changes.** For a document in which several `oci` components
  reconcile one artifact, the OCIRepository is renamed from the name of the
  component deployed first to `<document>-source-<digest>`. It is applied
  with the application bundle, ahead of every group, instead of with that
  component, so the application has an ordered group even when the document
  declares no order. That component's `prune-protection` or `force-replace`
  no longer covers it. The rename also happens when a second consumer
  is added to a document that had one, and is undone when it is removed.
  Components that shared one source while their intervals differed now emit
  one source each. `interval: 0s` now emits the 60m default on both objects,
  since both terminals read a zero interval as unset; it used to be emitted
  as `0s`. Nothing else changes.

  Sharing is decided among the `oci` components the document holds when the
  rule runs: an `oci` component a third-party rule emits in a later lowering
  round is not counted as a consumer of a source lowered before it.

  **Messages.** The checks the rule runs keep their `oci:` text. A lowering
  refusal is reported with the component and its document
  (`component "x" (type "oci") in document …: oci: version is required …`).
  The registry allowlist is the `ocirepository` terminal's check, so its
  refusals name that kind and its field (`ocirepository: url: …`) rather
  than `oci: source.url`.

  `targetNamespace` has no default (go-kure/launcher#622). Unset, the
  Kustomization emits no `spec.targetNamespace` and each object keeps the
  namespace the artifact's own kustomize build gives it: the one it carries,
  or a `namespace` set in the artifact's `kustomization.yaml`.
  kustomize-controller does not fill in its own namespace for an object that
  still has none. Author `targetNamespace` when namespaced objects are left
  without a namespace after that build: they otherwise fail at apply with
  `namespace not specified`, with or without a Flux namespace. This deliberately differs from `helmrelease`, which under a Flux namespace defaults
  `targetNamespace` to the application namespace. A Kustomization's
  `targetNamespace` sets or overrides the namespace of every namespaced object
  it applies, Flux custom resources included, so a default would move the
  objects of a deliberately multi-namespace artifact.
  `wait` and `healthChecks` are opt-in readiness settings for the delivery
  Kustomization (go-kure/launcher#432). A document in which neither requests
  anything — `wait` absent or `false`, and `healthChecks` absent, null or
  empty — builds the same Kustomization byte for byte, and the default stays
  unset.
  `wait: true` sets `spec.wait`, so Flux reports the Kustomization ready only
  once everything it applied is ready; `false`, like an omitted key, emits
  nothing. `healthChecks` is a list of `{apiVersion, kind, name, namespace}`
  entries copied, in authored order, into `spec.healthChecks`. The component
  delivers an opaque artifact, so the list is authored, never derived.
  `apiVersion` (`apps/v1`, or `v1` for a core kind), `kind` and `name` are
  required non-empty strings, and `namespace` is optional, left out for a
  cluster-scoped kind. Any other key in an entry is refused by name, a
  wrongly typed field is a type error (`healthChecks[0].name: must be a
  string, got int`), and an empty or null list emits nothing. `wait: true`
  together with a non-empty `healthChecks` is refused: kustomize-controller
  ignores `healthChecks` when `wait` is true, so the list would never be
  checked. There is no `timeout` property; Flux's own default applies.
  Under a policy with a non-empty registry allowlist (`AllowedRegistries`),
  `source.url` must name its registry explicitly — `oci://<registry>/<repository>`
  with a non-empty repository and a registry that is `localhost` or contains
  `.` or `:` — and that registry must match an allowlist entry exactly
  (go-kure/launcher#580). Flux's source-controller parses the url with
  go-containerregistry's `name.NewRepository`, which reads any other first
  segment as part of a Docker Hub repository: `oci://ghcr.io` and
  `oci://registry/my-artifact` are pulled from Docker Hub, so they are refused
  even when `ghcr.io` or `registry` is listed. No policy, or an empty
  allowlist, accepts every `oci://` url this rule would refuse; a user or
  password in `source.url` is refused whatever the policy, by its
  `ocirepository` terminal (see **helmrepository / ocirepository /
  gitrepository / bucket / helmchart**).
  `interval` (default `60m`) must be a duration Flux accepts, checked exactly as
  for `helmrelease` (go-kure/launcher#590): see its Defaults paragraph.
