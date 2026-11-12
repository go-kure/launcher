---
title: "Kinds: secrets, certificates and backup"
weight: 11
---

- **issuer**, **clusterissuer**, **certificate** (go-kure/launcher#790) are
  the kind-named projections of three objects of cert-manager's
  `cert-manager.io/v1` API: an Issuer, a ClusterIssuer and a Certificate. Each
  is built on `policyHeldKind` (`policyFreeKind` with a policy check, see
  the **storageclass** entry) and emits that one object, named after the component unless
  `objectName` names it; the handler adds no label, no annotation and no
  default. An `issuer` and a `certificate` are emitted in the build namespace
  and declare their object as namespaced. A `clusterissuer` is emitted with no
  namespace and declares its object as cluster-scoped, so its name is claimed
  in no namespace. An Issuer and a ClusterIssuer share one spec type
  (`IssuerSpec`), and what is said of an issuer below holds for both kinds.

  **No capability is required, and none gates these kinds.** Launcher does
  not ask whether the cluster serves `cert-manager.io/v1`: where cert-manager's
  CRDs are not installed the component builds, and the object is refused at
  apply. Whoever may author a component may author these, a ClusterIssuer
  included. A policy can keep the kinds out of a build through the object
  kind policy (`oam.ObjectKindPolicy`, go-kure/launcher#922). The
  `certificate` trait still requires its
  capability; the kind of the same name does not read it.

  **Authored.** The properties are the top-level json fields of the spec type,
  decoded strictly at every depth: an unknown key is refused wherever it sits
  (a solver, a DNS provider's settings, a keystore).
  - `issuer` and `clusterissuer` (`IssuerSpec`): the five issuer types, `acme`,
    `ca`, `vault`, `selfSigned` and `venafi`. An issuer type authored empty is
    in the object as written (`selfSigned: {}` is a complete self-signed
    issuer); one left out is not.
  - `certificate` (`CertificateSpec`): `secretName`, `issuerRef`, the subject
    (`commonName`, `subject`, `literalSubject`), the subject alternative names
    (`dnsNames`, `ipAddresses`, `uris`, `emailAddresses`, `otherNames`), the
    lifetime (`duration`, `renewBefore`, `renewBeforePercentage`, `renewal`),
    the key and its use (`privateKey`, `signatureAlgorithm`, `usages`,
    `encodeUsagesInRequest`, `isCA`, `nameConstraints`), and what is written
    to the Secret (`secretTemplate`, `keystores`, `additionalOutputFormats`),
    with `revisionHistoryLimit`.
  - **A duration is carried in Go's spelling.** `duration`, `renewBefore` and
    the other durations of these types are authored as a Go duration string
    and nothing else, and the object carries the same duration as Go writes
    it: `2160h` is emitted as `2160h0m0s`.
  - An authored `false` or `0` is kept where the API tells it from an unset
    field (`encodeUsagesInRequest: false`, a keystore's `create: false`, a
    solver pod's `runAsUser: 0`), and left out where the type omits a zero
    that means the same (`isCA: false`). `TestCertManagerKinds_NoDefaultedZeros`
    holds the types to having no number, boolean or string that is omitted
    when zero and that the CRD defaults to something else. A default cert-manager
    applies when it reads the object (a key size, a rotation policy) reads
    the same type, in which an authored `0` and none are one value.

  **Required** is a field the API requires that the Go type writes whether or
  not it was authored, so that the object would not show the omission: the
  rule every kind follows (see the Prometheus operator's kinds). Each
  must be authored (`secretName: required (…)`,
  `acme.solvers[1].dns01.webhook.groupName: required (…)`); an authored empty
  value is a value, and the API server's to refuse.
  - A `certificate`: `secretName`, and `issuerRef` with its `name`. No default
    issuer is filled. Of what is authored below them: an additional output
    format's `type`, and a keystore's `create`.
  - An issuer has no required top-level field. Of an issuer type that is
    authored: `acme.server` and `acme.privateKeySecretRef`; `ca.secretName`;
    `vault.server`, `vault.path` and `vault.auth`, and the role or path of
    the authentication method that is authored; `venafi.zone`, and the URL,
    credentials or token of the platform that is authored; a DNS01 provider's
    own required settings (an ACME-DNS `host`, an RFC 2136 `nameserver`, a
    webhook solver's `groupName` and `solverName`).
  - Of a reference to a Secret or a ServiceAccount that is authored, on either
    kind: its `name`. Unauthored, the type would write `name: ""`.
  - Of a renewal window of a `certificate` that is authored
    (`renewal.windows[]`): its `cron` and its `windowDuration`
    (`renewal.windows[0].cron: required (…)`). The type leaves either out when
    it is empty, so the object would show the omission and the API server
    refuse it; an empty `cron` is refused as an unauthored one is.
    `TestKindComponents_OmittedRequiredAndWrittenDefaults` derives the two
    from the CRD and shows the refusals.
  - Of an ACME HTTP01 solver, on an issuer and a clusterissuer, three fields
    of the Kubernetes and Gateway API types it embeds: the `nodeSelectorTerms`
    of a required node affinity of the solver pod, under `http01.ingress` and
    under `http01.gatewayHTTPRoute`
    (`acme.solvers[0].http01.ingress.podTemplate.spec.affinity.nodeAffinity.requiredDuringSchedulingIgnoredDuringExecution.nodeSelectorTerms:
    required (…)`), and the `name` of a parent reference
    (`acme.solvers[0].http01.gatewayHTTPRoute.parentRefs[0].name: required
    (…)`). Unauthored, the type writes the terms as `null`, which the API
    server drops before it validates, so the required field is then missing;
    and the name as `""`, which is below the CRD's minimum length of 1.
    `TestKindComponents_NullRequired` shows each refusal by running the CRD's
    schema validator on the object the type would encode. An authored empty
    name (`name: ""`) is not refused here: it is a value, and the API server's
    to refuse for that minimum length. An authored empty list of terms
    (`nodeSelectorTerms: []`) is not refused either: it is written as one,
    and the CRDs accept it.
  - Of a match expression that is authored in a label selector of an HTTP01
    solver's pod affinity (`acme.solvers[].http01.ingress.podTemplate.spec.affinity`,
    and under `gatewayHTTPRoute`), on an issuer and a clusterissuer: its `key`
    and `operator`. Presence only: the operator's value and its `values` are
    left to the API server (see ["A label selector's match expressions"](conventions.md#a-label-selectors-match-expressions)).
  - Of every other type these specs embed, Kubernetes or Gateway API, each
    field the CRD requires and the type writes unauthored, as of
    cert-manager's own: under an HTTP01 solver's pod template, the `key` and
    `operator` of a node selector requirement, the `topologyKey` of a pod
    affinity term, the `weight` and the term of a preferred or weighted one,
    a sysctl's `name` and `value` and a seccomp profile's `type`
    (`…podAffinity.requiredDuringSchedulingIgnoredDuringExecution[0].topologyKey:
    required (the cert-manager API requires this field)`).
  - A required field under a parent the author left out is not asked for: the
    list follows what was authored. Where that parent is one the type writes
    unauthored and the CRD does not require (a keystore's
    `passwordSecretRef`, a Route 53 solver's `secretAccessKeySecretRef`, an
    RFC 2136 solver's `tsigSecretSecretRef`, a Vault Kubernetes auth's
    `secretRef`), its `name` is asked for only where the parent is authored;
    the API server accepts the object with the parent left out.

  The lists are generated from the CRDs the linked module ships, which are
  the ones cert-manager's chart installs (see ["Required fields a kind writes
  unauthored and does not refuse"](conventions.md#required-fields-a-kind-writes-unauthored-and-does-not-refuse)): every field a CRD requires and the type
  writes unauthored is listed, whatever type declares it, and nothing else
  is; a field the kind gives no text of its own reads the generic one. A
  dependency bump that adds, drops or moves one fails
  `TestKindComponents_RequiredIsGenerated` until the list is regenerated.
  `TestKindComponents_GeneratedRequiredBothWays` builds each field authored
  and refuses its omission, and runs one per type through the CRD's own
  validation, which refuses it too. `TestLabelSelectorKinds_CoverEverySelector`
  shows, at each of the 16 label selectors, what the API server answers for
  an expression without its `key` or its `operator`. **Not refused:**
  - every other value rule of the CRDs (enumerations, lengths, minima, and
    the one rule the CRDs write as an expression: that a `venafi` issuer
    names exactly one of `tpp`, `cloud` and `ngts`);
  - **the rules of cert-manager's validating webhook,** which refuses more
    than the CRDs do: an issuer that configures no issuer type, or more than
    one; a keystore with a `password` beside a `passwordSecretRef` that
    names a Secret, or with neither (the empty reference the type writes
    beside an authored `password` is not one); a certificate that names no
    subject and no alternative name; `subject` or `commonName` beside
    `literalSubject`.
    Launcher repeats none of them, so an `issuer` with no property builds,
    and is refused at apply where the webhook runs.

  **What the type writes unauthored.** An empty reference, `{name: ""}`,
  where the type holds one by value and the API does not require it: a
  keystore's `passwordSecretRef` (so a keystore that authors `password`
  carries both fields), `vault.auth.kubernetes.secretRef`, an RFC 2136
  solver's `tsigSecretSecretRef` and a Route 53 solver's
  `secretAccessKeySecretRef`. An HTTP01 solver's `podTemplate` carries
  `metadata: {}` and `spec: {}`, and its `ingressTemplate` `metadata: {}`.
  The CRDs accept each, and the same test holds every such field to its
  schema: none is refused empty.

  **Policy.**
  - **An issuer's ACME HTTP01 solver pod is held to the cpu and memory
    maxima.** cert-manager starts a pod to answer an HTTP01 challenge, and a
    solver's `podTemplate.spec.resources` sizes it. Its limits and requests
    are held to the environment policy's cpu and memory maxima, as a
    container's are, on both ways a solver answers (`http01.ingress`,
    `http01.gatewayHTTPRoute`) and on every solver: `component "web":
    acme.solvers[0].http01.ingress.podTemplate.spec.resources: cpu limit "4"
    exceeds enforced maximum "2"`. Nothing else is held and no default is
    filled: the policy's default requests and limits are a workload's. A
    request above its limit is not refused, since cert-manager lays the
    authored block over its controller's own defaults key by key, and the
    pair that reaches the pod is not the authored one. A solver pod template
    has no other field the `pod` kind's refusals speak to: it names no image
    (the solver's image is a flag of the cert-manager controller), no
    container security context, no volume and no host namespace. Its
    pod-level `securityContext`, `serviceAccountName`, `priorityClassName`
    and `imagePullSecrets` are the author's and are held to nothing, as are
    its `nodeSelector`, `affinity` and `tolerations`, as they are on a pod
    kind: the one field of a pod-level security context the policy holds
    there, `windowsOptions.hostProcess`, is not a field of this one.
  - **A certificate's keystore password in the object is refused under a
    policy that forbids explicit secrets.** `keystores.jks.password` and
    `keystores.pkcs12.password` hold the password itself, and the object is
    in the build's output: `component "web": keystores.jks.password: holds
    the keystore password in the object, and the environment policy forbids
    explicit secrets; name the key of a Secret created out of band in
    keystores.jks.passwordSecretRef instead`. The message quotes nothing of
    the value, and an empty password is refused as any other is. A policy
    that allows explicit secrets, one that does not answer the question and
    no policy build it. No other field of a Certificate holds a secret.
  - **No field of an issuer holds a literal secret by design, and none is
    checked.** A credential is a reference to a Secret: an ACME account key,
    a DNS provider's token, a Vault token or App Role secret, a platform's
    API credentials. Free JSON that could hold one is written to the object
    as authored, under a policy that forbids explicit secrets too: a webhook
    solver's `config`.
  - **Hosts are not checked.** A host these objects name is one cert-manager
    reaches, not an artifact source, and none is held to the policy's allowed
    registries: `acme.server`, `vault.server`, a `venafi` platform's `url`
    and token endpoint, an RFC 2136 `nameserver`, an ACME-DNS `host`, an
    Akamai `serviceConsumerDomain`, and the CRL distribution points, OCSP
    servers and issuing certificate URLs an issuer writes into what it
    signs.
  - A nil policy checks nothing.

  **Labels and annotations** are the `labels` and `annotations` properties. A
  solver's `podTemplate.metadata` is not these: it becomes the metadata of the
  pod cert-manager starts to answer an HTTP01 challenge. It is held as a pod's
  is, with or without a policy: on both ways a solver answers and on every
  solver, the component label's key holds the component's value there or the
  build is refused (`Issuer "web":
  spec.acme.solvers[0].http01.ingress.podTemplate.metadata.labels[…]: "db" is
  not the component label of component "web"`), and a key the consumer
  reserved is refused (`solver pod template label "…"
  (spec.acme.solvers[0].http01.ingress.podTemplate.metadata.labels) may not be
  set`); see "Component label and ownership" and "Reserved metadata keys" in
  the OAM model. Launcher writes nothing there. A solver's
  `ingressTemplate.metadata` and the `labels` of its HTTPRoutes reach an
  Ingress and HTTPRoutes, and are not read.

  **The issuer of a certificate is the author's.** `issuerRef` names an
  issuer by `name`, `kind` and `group`; launcher points it at no component
  and does not look for the issuer in the document. To have an `issuer` or
  `clusterissuer` component sign a `certificate` component, name its object:
  the component name, or its `objectName`. cert-manager reads an `issuerRef`
  without a `kind` as an Issuer of the Certificate's namespace. The Secret
  `secretName` names is cert-manager's to create, and the component emits
  none; a Secret an issuer refers to is read in the Issuer's namespace, and
  for a ClusterIssuer in the namespace cert-manager is configured with.

  **Beside the `certificate` trait.** The trait derives a Certificate for the
  workload it is attached to, from a few properties and the issuer the
  cluster's `certificate` capability names, and names it after its
  `secretName`. This kind is the authored object, for what the trait does not
  express: the whole spec, an issuer the author chooses, a Certificate that
  belongs to no workload. A component type and a trait type are two lists, so
  the one name is not ambiguous in a document. The two objects are one kind:
  a trait's and a component's given one name in one namespace are refused
  (`generated-object collision: Certificate.cert-manager.io
  "default/app-tls" is generated by both …`).

  **Not covered.** Whether what is referred to exists (an issuer, a
  Secret and its key, a ServiceAccount, an ingress class, a Gateway), and
  whether cert-manager can reach what an issuer names. The object's status is
  cert-manager's and is not written.

- **secretstore**, **clustersecretstore**, **externalsecret**,
  **clusterexternalsecret** (go-kure/launcher#790) are the kind-named
  projections of four objects of the External Secrets Operator, in its
  `external-secrets.io/v1` API: a SecretStore, a ClusterSecretStore, an
  ExternalSecret and a ClusterExternalSecret. Each emits that one object,
  named after the component unless `objectName` names it; the handler adds no
  label, no annotation and no default. A `secretstore` and an `externalsecret`
  are built in the build namespace. **A `clustersecretstore` and a
  `clusterexternalsecret` are cluster-scoped**: the object carries no
  namespace, whatever namespace the application is built for, and its name is
  claimed cluster-wide. Nothing else is emitted: no Secret, ServiceAccount or
  generator a store or an external secret names, no store an external secret
  reads from, and not the ExternalSecrets a ClusterExternalSecret has the
  operator create. All four are built on `policyHeldKind`: the stores for the
  credentials a provider takes as a value, the two external-secret kinds for
  the one field that has the operator write another kind than a Secret
  (`target.manifest`, under **Policy** below).

  **No capability is required, and none gates these kinds.** Launcher does
  not ask whether the cluster runs the operator: where the CRDs are not
  installed the component builds, and the object is refused at apply. The
  cluster's `external-secret` capability is the trait's, and these kinds do
  not read it. Whoever may author a component may author these, a store that
  every namespace reads from and an external secret created in every selected
  namespace included. A policy can keep the kinds out of a build through the
  object kind policy (`oam.ObjectKindPolicy`, go-kure/launcher#922).

  **Authored.** The properties are the top-level json fields of the spec type,
  decoded strictly at every depth: an unknown key is refused wherever it sits
  (a provider, an authentication method, a `data` entry, a template).
  - `secretstore` and `clustersecretstore` (`SecretStoreSpec`, which the two
    objects share): `provider`, with one provider under its key (`aws`,
    `vault`, `kubernetes`, `webhook` and the others the API lists),
    `controller`, `retrySettings` (`maxRetries`, `retryInterval`),
    `refreshInterval` and `conditions` (`namespaceSelector`, `namespaces`,
    `namespaceRegexes`). The operator reads `conditions` on a
    ClusterSecretStore only; a `secretstore` that authors them carries them.
  - `externalsecret` (`ExternalSecretSpec`): `secretStoreRef` (`name`,
    `kind`), `target` (`name`, `creationPolicy`, `deletionPolicy`,
    `immutable`, `template`, `manifest`), `refreshPolicy`, `refreshInterval`,
    `syncWindows` (`kind`, `windows`), `data` (each a `secretKey`, a
    `remoteRef` and a `sourceRef`) and `dataFrom` (each an `extract`, a
    `find`, a `rewrite` and a `sourceRef`).
  - `clusterexternalsecret` (`ClusterExternalSecretSpec`):
    `externalSecretSpec` (the fields of an `externalsecret`),
    `externalSecretName`, `externalSecretMetadata` (`labels`, `annotations`),
    `namespaceSelector`, `namespaceSelectors`, `namespaces` and `refreshTime`.
  - A duration is written in the form the API type gives it:
    `refreshInterval: 1h` is emitted as `1h0m0s`, and so are a sync window's
    `duration` and a `clusterexternalsecret`'s `refreshTime`.
  - **No default is filled.** The API's defaults (an external secret's
    `refreshInterval`, a target's `creationPolicy` and `deletionPolicy`) are
    the API server's to fill where the field was not authored. The `kind` of a
    `secretStoreRef` has no default in the API: unset, it stays unset in the
    object, and is read as a SecretStore.
  - **No CRD default, read from its marker, replaces an authored `0` or
    `false`, except on three store fields, where the value is refused.** A
    default the operator applies itself is not covered. The type omits a zero in
    `provider.beyondtrust.server.decrypt`,
    `provider.infisical.secretsScope.expandSecretReferences` and
    `provider.onepasswordSDK.cache.maxSize`, and the API defaults each to
    another value (`true`, `true`, `100`), so the authored value would be
    replaced: `provider.infisical.secretsScope.expandSecretReferences: false
    cannot be carried by the external-secrets API types (the field is omitted
    when zero, so the API server would apply its default true)`.
    `TestExternalSecretsKinds_DefaultedZeros` holds that list to the default
    markers of the linked module's source in both directions: no other number
    or boolean of the four specs is of that shape.

  **Required** follows the rule of the Prometheus operator's kinds: a
  field the API requires that the Go type writes whether or not it was
  authored. Each must be authored; an authored empty value is a value, and the
  API server's to refuse. A required field under a parent the author left out
  is not asked for: the list follows what was authored.
  - **A store's `provider`, and inside the provider that is authored
    everything the API requires of it.** The list is not written by hand: it
    is generated from the types of the linked module
    (`zz_generated_kind_required.go`), 265 paths at this pin, over every
    provider the API has, with the two of a condition's `namespaceSelector`
    below. `provider.vault.server`,
    `provider.aws.region`, `provider.aws.service`, `provider.webhook.url` and
    `provider.kubernetes.auth.serviceAccount.name` are five of them. The
    refusal names the path and says which of two things it is: `provider.vault.server:
    required (the external-secrets API requires this field)`, and for the
    `name` of a reference `provider.vault.caProvider.name: required (the
    external-secrets API requires the name of what this refers to)`.
  - **Four fields of a store the API would default, and the type always
    writes.** A default is applied to an absent field, and the object carries
    an empty one: the API's enumeration refuses an empty `version`, and an
    empty mount path or a cache lifetime of zero is not what the default
    would have been. So each must be written where its provider is authored:
    `provider.vault.version` and `provider.openBao.version` (the API's
    default is `v2`), `provider.vault.auth.cert.path` (`cert`) and
    `provider.onepasswordSDK.cache.ttl` (`5m`). **A Vault store therefore
    writes its `version`**: `provider.vault.version: required (the object
    always carries this field, so the external-secrets API's default v2 never
    applies: write the value)`. The four are generated into the same file, in
    a list of their own.
  - Of a match expression authored in a condition's `namespaceSelector`: its
    `key` and `operator`. Presence only: the operator's value and its
    `values` are left to the API server (see ["A label selector's match
    expressions"](conventions.md#a-label-selectors-match-expressions)).
  - An `externalsecret` has no required top-level field. Of what is authored:
    a `data` entry's `secretKey`, `remoteRef` and its `key`; the `key` of an
    `extract`; the `source` and `target` of a `rewrite`'s `regexp` and the
    `template` of its `transform`; the `kind` and `name` of a `generatorRef`;
    of `syncWindows` its `kind` and `windows`, and a window's `schedule` and
    `duration`; of a `target.manifest` its `apiVersion` and `kind`; of a
    ConfigMap or Secret a template is read from
    (`target.template.templateFrom[]`) its `name`, its `items` and an item's
    `key`. 23 paths: 21 generated, and the `kind` and `name` of a `data`
    entry's `generatorRef`, which the generator does not reach (the kind
    refuses that `generatorRef` wherever it is authored) and the kind holds
    beside them.
  - A `clusterexternalsecret`: `externalSecretSpec`, and under it everything
    an `externalsecret` requires; and the `key` and `operator` of a match
    expression authored in `namespaceSelector` or in an entry of
    `namespaceSelectors`, presence only. 28 paths, 26 of them generated.
  - The published schema marks the two top-level ones as required
    (go-kure/launcher#790): `provider` of a `secretstore` and of a
    `clustersecretstore`, and `externalSecretSpec` of a
    `clusterexternalsecret`. That refuses no component that built before,
    since the kind refused each without it already.

  The lists are derived from the source of the linked module, which ships no
  CRD: the markers its CRDs are generated from are read from the Go files. A
  field counts as required where its json tag has no `omitempty` and it
  carries no optional marker, and as written unauthored where the Go type
  encodes it when it is unset; a Kubernetes type the specs embed is read by
  the same rule on the linked source, whatever type declares the field (see
  ["Required fields a kind writes unauthored and does not refuse"](conventions.md#required-fields-a-kind-writes-unauthored-and-does-not-refuse)).
  `TestKindComponents_RequiredIsGenerated` fails where the generated file and
  that derivation differ, in either direction and for either list, and names
  the path; a dependency bump that adds, drops or moves one fails there.
  Regenerate with `UPDATE_KIND_REQUIRED=1 go test ./pkg/oam/builtin/components/
  -run TestKindComponents_RequiredIsGenerated`. The texts are the ones the
  kinds held by hand before the lists were generated, byte for byte.
  `TestExternalSecretsKinds_RequiredMatchSource` shows that no field of the
  four specs is required and omitted by the type when unauthored at this pin,
  and fails on the first one a bump brings. The API server holds the lists
  too: `TestKindComponents_GeneratedRequiredBothWays` runs one path of every
  type holding a generated path through its validator on the operator's CRDs,
  vendored for the tests from the release the linked commit is tagged
  (go-kure/launcher#1108; see "Vendored CRDs" under the required fields), and
  every generated path is one those CRDs declare.

  **The API's count and expression rules.** Every rule the source declares on
  a type these specs reach is classified in `TestExternalSecretsKinds_Rules`,
  which fails on one that is added or reworded. **Checked:**
  - a store configures exactly one provider (`provider: configures no
    provider; the API takes exactly one`; `provider: configures 2 providers
    (aws, vault); the API takes exactly one`). A provider key written as
    `null` configures nothing, as on the API server;
  - a generator is not named as the source of one key of `data`. The API
    takes exactly one of `storeRef` and `generatorRef` in that `sourceRef`,
    and the type writes `storeRef` whether or not it was authored
    (`data[0].sourceRef.generatorRef: cannot be carried: the API takes
    exactly one of storeRef and generatorRef here, and the object always
    carries a storeRef; read from a generator with
    dataFrom[].sourceRef.generatorRef`). A source of `dataFrom` takes a
    generator. On a `clusterexternalsecret` both paths are under
    `externalSecretSpec`.

  **Not checked**, and the API server's to refuse:
  - the count rule on ten other types, each "exactly one of these fields": a
    `rewrite`, the `sourceRef` of a `dataFrom` entry, and eight blocks of
    single providers (an authentication method, a reference, how a Yandex
    provider fetches an entry). The minimum of one on a `data` entry's
    `sourceRef` is met by the `storeRef` the object always carries;
  - the six counts over named fields, on three providers: `openBao` (at most
    one of `caBundle` and `caProvider`; exactly one authentication method
    under `auth`; exactly one of `roleId` and `roleRef` on an app role;
    exactly one of `serviceAccountRef` and `secretRef` on its Kubernetes
    authentication), `onepasswordSDK` (at most one of `vault` and
    `environment`) and `crd` (at most one of `auth` and `authRef`). The type
    omits each of those fields when unauthored, so the object carries what
    was authored and no more;
  - the thirteen rules written as expressions, each on one provider: Barbican
    (four), Pulumi (two), Secret Server (two), AWS, `crd`, Doppler, GitHub and
    Nebius;
  - every other value rule of the API: enumerations, patterns, lengths and
    minima;
  - **the rules of the operator's validating webhook,** which refuses more
    than the CRDs do: an external secret with neither `data` nor `dataFrom`,
    two `data` entries with one `secretKey`, a `deletionPolicy` its
    `creationPolicy` does not allow, a template of a bootstrap-token Secret
    or of a service-account-token Secret that names its service account, a
    `dataFrom` entry that sets none or several of its sources; a store whose
    `namespaceRegexes` do not compile, whose `refreshInterval` is no
    duration, or whose provider refuses its own configuration. Launcher
    repeats none of them, so
    an `externalsecret` with no property builds, and is refused at apply where
    the webhook runs.

  **What the type writes unauthored.** An empty block, `{}`, where the type
  holds one by value and the API does not require it: an external secret's
  `secretStoreRef` and `target`, a `clusterexternalsecret`'s
  `externalSecretMetadata`, an AWS provider's `auth` and a Vault provider's
  `tls`, among others. The API accepts each.
  `TestExternalSecretsKinds_WrittenUnauthored` pins those five.

  **Policy.**
  - **A credential written into a store is refused under a policy that
    forbids explicit secrets.** A few providers take a credential as a
    `value` beside the `secretRef` that names a Secret holding it, and the
    object is in the build's output: `provider.delinea.clientSecret.value:
    holds the credential in the object, and the environment policy forbids
    explicit secrets; name the key of a Secret created out of band in
    provider.delinea.clientSecret.secretRef instead`. The message quotes
    nothing of the value. Refused: `provider.beyondtrust.auth.apiKey`,
    `.auth.certificateKey` and `.auth.clientSecret`,
    `provider.delinea.clientSecret`, `provider.scaleway.secretKey`,
    `provider.secretserver.password` and `.token`. A policy that allows
    explicit secrets, one that does not answer the question and no policy
    build it.
  - **The same union holds an identifier on seven other fields, which are not
    refused:** `provider.barbican.auth.applicationCredentialID` and
    `.auth.username`, `provider.beyondtrust.auth.certificate` and
    `.auth.clientId`, `provider.delinea.clientId`,
    `provider.scaleway.accessKey` and `provider.secretserver.username`.
    `TestExternalSecretsKinds_InlineValues` finds every string `value` beside
    a `secretRef` in the linked module (14 at this pin) and fails on one that
    is in neither list.
  - **The data of the `fake` provider is refused under the same policy.**
    `provider.fake.data` is nothing but the values the store serves, and the
    provider has no reference to a Secret. The API also requires the field,
    and the two refusals come in this order: a `fake` provider with no `data`
    is refused as a required field, under every policy and under none; one
    with entries is refused as an explicit secret under a policy that forbids
    them; `data: []` passes both. `TestSecretStoreKinds_FakeData` pins the
    order.
  - **Text in which a reference and a literal look alike is not checked,** on
    any of the four and under any policy: a header or the body of a `webhook`
    provider's request, a header of a `vault` provider, the user part of a
    provider's URL, and the template of the Secret an external secret writes
    (`target.template.data`, a `templateFrom` literal, the template's
    metadata). A secret written there is in the build's output.
  - **No field of an external secret is a credential, and one field is
    checked: `target.manifest`.** An `externalsecret` and a
    `clusterexternalsecret` name what is read and where it is written, and
    `ApplyPolicy` fills nothing on either. With `target.manifest` (an
    `apiVersion` and a `kind`) the operator writes an object of that kind
    instead of a Secret, from template text it renders in the cluster. The
    `apiVersion` has to be an API version, a version (`v1`) or a group and a
    version (`apps/v1`), each part a name the API can have: the version a
    DNS-1035 label and the group a DNS-1123 subdomain, the bounds a
    CustomResourceDefinition's group and version names have. The core group
    is written without a slash, so `/v1`, `apps/` and `Apps/v1` are none.
    The API bounds the field by a minimum length only, nothing can write an
    object of a version that is none, and where the value does not split
    into the two its kind would read as no kind at all to the check below.
    One that is none is refused under every policy, and not as a policy
    refusal (`target.manifest.apiVersion: "apps/v1/x" is no API version:
    want a version (v1) or a group and a version (apps/v1)`). **That
    object gets no more than the same policy gives the same kind on
    `passthrough`.** What `passthrough` would read cannot be read at build,
    so a kind the rendered-object check reads anything from is refused, in
    any version and whatever the policy's own limits: a Pod, PodTemplate,
    ReplicationController, Deployment, StatefulSet, DaemonSet, ReplicaSet,
    Job, CronJob, PersistentVolumeClaim or PersistentVolume of the built-in
    groups, and a HorizontalPodAutoscaler: `target.manifest: the operator
    would write a Deployment, a kind the environment policy checks, and what
    the object would hold is not known at build, so it cannot be checked
    against environment policy` (on a `clusterexternalsecret` the path is
    `externalSecretSpec.target.manifest`). The transform applies `NoopPolicy`
    when no policy is passed, so this holds then too. **This is stricter than
    `passthrough`** wherever the policy would pass the object it read: a
    claim under a policy with no storage maximum, an autoscaler under one
    with no replica maximum, a workload within every limit. `passthrough`
    reads the object and finds nothing to refuse; here the content is not
    known at build, so there is nothing to read. A core `Secret` named
    there is refused under a policy that forbids explicit secrets and passes
    under any other, as on `passthrough`. **Any other kind (a ConfigMap, a
    custom resource) passes as it would on `passthrough`**, and whether the
    operator writes it at all is the cluster's: the operator's generic-target
    setting and its RBAC. The `target` path of a `templateFrom` entry needs
    no check of its own: it places text inside the object `target.manifest`
    names. `TestPolicyReadsKind_IsWhatTheRenderedObjectCheckReads` fails when
    the refused set and the check differ.
  - **Hosts are not checked.** A host these objects name is one the operator
    reaches, not an artifact source, and none is held to the policy's allowed
    registries: the server, URL, host or endpoint of every provider
    (`provider.vault.server`, `provider.webhook.url`,
    `provider.kubernetes.server.url`, `provider.azurekv.vaultUrl`,
    `provider.conjur.url`, `provider.onepassword.connectHost` and the others).
  - A nil policy checks nothing.

  **The store of an external secret is the author's.** `secretStoreRef` names
  a store by `name` and `kind`; launcher points it at no component, fills
  none from the cluster's capability and does not look for the store in the
  document. To have a `secretstore` or `clustersecretstore` component serve
  an `externalsecret` component, name its object: the component name, or its
  `objectName`. The API reads a `secretStoreRef` without a `kind` as a
  SecretStore of the ExternalSecret's namespace. The Secret `target` names is
  the operator's to create, and the component emits none.

  **Labels and annotations** are the `labels` and `annotations` properties.
  The object also carries the component label, whose value is the component's
  (see "Component label and ownership" in the OAM model). The labels of the
  ExternalSecrets a ClusterExternalSecret creates are its
  `externalSecretMetadata`, and those of the Secret an external secret writes
  its `target.template.metadata`: the operator applies both, and launcher adds
  nothing to either.

  **Beside the `external-secret` trait.** The trait derives an ExternalSecret
  for the workload it is attached to, from a few properties and a store it
  names itself or takes from the cluster's `external-secret` capability, names
  it after its `secretName`, and mounts or injects the Secret into the
  workload. The `externalsecret` kind is the authored object, for what the
  trait does not express: the whole spec, and an ExternalSecret that belongs
  to no workload. The two type names differ (`external-secret`,
  `externalsecret`). The two objects are one kind: a trait's and a
  component's given one name in one namespace are refused (`generated-object
  collision: ExternalSecret.external-secrets.io "default/app-credentials" is
  generated by both …`).

  **Not covered.** The operator's other APIs: its generators and its
  PushSecret. Whether what is referred to exists (a store, a Secret and its
  key, a ServiceAccount, a ConfigMap, a generator, a namespace), and whether
  the operator can reach what a store names. The objects' status is the
  operator's and is not written.
- **replicationsource**, **replicationdestination** (go-kure/launcher#790)
  are the kind-named projections of the two objects of VolSync's
  `volsync.backube/v1alpha1` API: a ReplicationSource, which copies a volume
  out, and a ReplicationDestination, which receives one. Each is built on
  `policyHeldKind` (`policyFreeKind` with a policy check, see above) and
  emits that one object in the build namespace, named after the component
  unless `objectName` names it, and declares it as namespaced; the handler
  adds no label, no annotation and no default. The two share their movers but
  for Syncthing, and what is said of a mover below holds for both kinds.

  **No capability is required, and none gates these kinds.** Launcher does
  not ask whether the cluster serves `volsync.backube/v1alpha1`: where
  VolSync's CRDs are not installed the component builds, and the object is
  refused at apply. A policy can keep the kinds out of a build through the
  object kind policy (`oam.ObjectKindPolicy`, go-kure/launcher#922). The
  kinds read nothing of the cluster
  profile; the `volsync` trait still takes its class defaults from it.

  **Authored.** The properties are the top-level json fields of the spec type,
  decoded strictly at every depth: an unknown key is refused wherever it sits
  (a mover, its security context, a volume mounted into it).
  - `replicationsource` (`ReplicationSourceSpec`): `sourcePVC`, the claim to
    copy; `trigger` (`schedule`, a cron expression, or `manual`); the movers
    `rsync`, `rsyncTLS`, `rclone`, `restic` and `syncthing`; `external`, a
    provider outside VolSync with its `parameters`; and `paused`.
  - `replicationdestination` (`ReplicationDestinationSpec`): `trigger`, the
    movers `rsync`, `rsyncTLS`, `rclone` and `restic`, `external` and
    `paused`. A destination has no Syncthing mover and names no source claim:
    `syncthing` and `sourcePVC` are refused there as unknown keys.
  - `rsync` (rsync over SSH) has its own, shorter type: it takes
    `moverResources`, `moverServiceAccount` and `moverPodLabels`, and has no
    `moverSecurityContext`, `moverAffinity` or `moverVolumes`, which are
    refused under it as unknown keys.
  - A mover authored empty is in the object, empty but for what the type
    writes unauthored (below); one left out is not.
  - An authored `false` or `0` is kept where the API tells it from an unset
    field (`restic.retain.monthly: 0`, a destination's `restic.previous: 0`,
    a Syncthing peer's `introducer: false`, `moverSecurityContext.runAsUser:
    0`), and left out where the type omits a zero (`paused: false`).
    VolSync's CRDs default no field under `spec`, so no authored zero is
    replaced by a CRD default; `TestVolsyncKinds_NoDefaults` holds the linked
    CRDs to that, and a dependency bump that adds a default fails there with
    the field named.

  **The kinds' required lists are generated** (go-kure/launcher#883; see
  ["Required fields a kind writes unauthored and does not refuse"](conventions.md#required-fields-a-kind-writes-unauthored-and-does-not-refuse)): every
  field the API requires that the Go type writes whether or not it was
  authored is refused when unauthored, each where its parent is authored,
  the fields of the Kubernetes types the specs embed included. Neither kind
  has one at the top level. Among them are the fields the kinds refused
  before, each with its text:
  - Of a volume mounted into a mover (`moverVolumes`, on `rsyncTLS`,
    `rclone`, `restic` and `syncthing`): `mountPath` and `volumeSource`
    (`restic.moverVolumes[0].volumeSource: required (…)`).
  - Of a Syncthing peer: `address`, `ID` and `introducer`. No default is
    filled for `introducer`: an authored `false` is a value.
  - Of a mover's affinity that authors a required node affinity
    (`moverAffinity.nodeAffinity.requiredDuringSchedulingIgnoredDuringExecution`):
    its `nodeSelectorTerms`, which the Kubernetes type would write as `null`
    where the CRD requires them. `TestKindComponents_NullRequired` shows the
    refusal with the validator of the linked CRDs.
  - Of a match expression that is authored in a label selector of a mover's
    pod affinity or anti-affinity (`moverAffinity`, on `rsyncTLS`, `rclone`,
    `restic` and `syncthing`): `key` and `operator`. Presence only: the
    operator's value and its `values` are left to the API server (see ["A
    label selector's match expressions"](conventions.md#a-label-selectors-match-expressions)).
  - A required field under a parent the author left out is not asked for: the
    list follows what was authored.

  And, refused since go-kure/launcher#883 (**breaking** for a document that
  leaves one out under an authored parent): the required fields of the
  Kubernetes types the specs embed, on every mover that holds them (`rsync`
  included, for its `moverResources`):
  - in a mover's affinity: the `key` and `operator` of a node selector
    requirement and of a node field selector, a preferred term's `weight`
    and its `preference` or `podAffinityTerm`, and a pod affinity term's
    `topologyKey`;
  - in `moverSecurityContext`: a seccomp or AppArmor profile's `type`, a
    sysctl's `name` and `value`;
  - in `moverResources`: a resource claim's `name`;
  - in a mounted volume source: an NFS volume's `server` and `path`, a
    claim's `claimName`, and the `key` and `path` of a mounted Secret's item.

  The lists (192 paths for a source, 142 for a destination) are derived from
  the CRDs the linked module ships, by `TestKindComponents_RequiredIsGenerated`;
  a dependency bump that adds, drops or moves one fails there until the list
  is regenerated. `TestKindComponents_GeneratedRequiredBothWays` shows the API
  server's answer to the omission of a field of each type that holds one (15
  rows for a source, 14 for a destination). No parent is refused here that
  the API does not require. `TestVolsyncKinds_WrittenEmptyAccepted` holds that
  every other field of VolSync's own types the type writes empty is one the
  CRDs take empty. **Not refused:**
  - a required field the type omits when it is not authored: the object shows
    the omission, and the API server refuses it;
  - every other value rule of the CRDs (enumerations, patterns, minima).

  **Two fields the linked Kubernetes type holds and the CRDs do not are
  refused when authored:** `<mover>.moverVolumes[].volumeSource.secret.defaultUser`
  and `<mover>.moverVolumes[].volumeSource.secret.items[].user`, an authored
  `0` included (`rclone.moverVolumes[0].volumeSource.secret.defaultUser: no
  field of the volsync.backube/v1alpha1 API: its CRD has no such property`).
  The linked Kubernetes API is newer than the one VolSync's CRDs were
  generated from: the Go type of a mounted Secret has the two fields, the
  strict decode reads the Go type, and the CRDs have no property for either.
  `TestVolsyncKinds_AbsentFromCRD` derives the fields the types reach that
  the linked CRDs have no property for and holds the refusals to them, both
  ways: a bump of either module that closes the gap, or widens it, fails
  there with the field named. It also runs the API server's own create
  sequence on the linked CRDs (`crdCreate`): the API server names each of the
  two as an unknown field, for which a request made with strict field
  validation is refused, and prunes it from the object it goes on with.

  **The CRDs declare no expression rule** (`x-kubernetes-validations`), so
  the kinds check none; `TestVolsyncKinds_NoExpressionRules` holds the linked
  CRDs to that, and a dependency bump that adds one fails there with the rule
  named.

  **What the type writes unauthored.** `customCA: {}` on an authored `rclone`
  or `restic` mover, which holds it by value. The CRDs accept it empty, and
  `TestVolsyncKinds_WrittenEmptyAccepted` holds every such field to its schema.

  **Policy.**
  - **An authored capacity is held to the storage maximum.** A mover's
    `capacity` (on `rsync`, `rsyncTLS`, `rclone` and `restic`) sizes a volume
    the operator provisions: on a source the point-in-time image of the
    volume, on a destination the volume the data is received into.
    `restic.cacheCapacity` sizes Restic's metadata cache volume and, on a
    source, `syncthing.configCapacity` Syncthing's configuration volume. Each
    is held to the environment policy's storage maximum: `component "web":
    restic.capacity "20Gi" exceeds enforced maximum "10Gi"`. A capacity the
    author left out is the operator's to choose and is not held; the policy's
    default storage size is not filled.
  - **A mover's cpu and memory are held to the maxima.** The limits and
    requests of `moverResources` are held to the policy's cpu and memory
    maxima, as a container's are, on every mover: `component "web":
    rsync.moverResources: cpu limit "4" exceeds enforced maximum "2"`. No
    default is filled: the policy's default requests and limits are a
    workload's. A request above its limit is not refused here.
  - **A mover's `hostProcess` switch is refused unless the policy allows
    privileged workloads.** `moverSecurityContext.windowsOptions.hostProcess:
    true` runs the mover's containers as Windows HostProcess containers:
    `component "web": restic.moverSecurityContext.windowsOptions.hostProcess
    is not allowed by environment policy`. An authored `false` builds. With
    no policy passed it is refused too, since `NoopPolicy` allows nothing
    privileged.
  - **The `rsync` mover is held to the policy's container capabilities.**
    Authoring the rsync-over-SSH mover is itself a choice of privilege: the
    linked operator version (VolSync v0.16.0) runs its container as root
    (`runAsUser: 0`), not privileged, with every capability dropped and seven
    added (`AUDIT_WRITE`, `CHOWN`, `DAC_OVERRIDE`, `FOWNER`, `SETGID`,
    `SETUID`, `SYS_CHROOT`), on a source and on a destination alike, and its
    builder does not take the namespace's answer on privileged movers. An
    authored `rsync` mover, whatever it holds, is therefore held to the
    policy's allowed and forbidden container capabilities for those seven,
    as a container that adds them is on a pod kind: `component "web": rsync:
    the mover's container as VolSync v0.16.0 writes it:
    securityContext.capabilities.add: "SYS_CHROOT" is forbidden by
    environment policy`. A policy that sets neither list, and no policy,
    build it; allowing privileged workloads does not lift a forbidden
    capability, as it does not on a pod kind. Two limits. The seven are what
    the **linked** operator version writes
    (`internal/controller/mover/rsync/mover.go` in its module): a cluster
    that runs another version of the operator may add other capabilities, and
    the kind does not know. `TestVolsyncKinds_RsyncCapabilities` reads that
    literal and holds the non-test Go source of the two packages the
    container is built in (`internal/controller/mover/rsync` and
    `internal/controller/utils`) to a stated digest: a dependency bump that
    changes either package fails, whatever the change, until a person has
    re-read the mover and restated the digest. The test cannot check that the
    re-reading was done; a bump that touches either package needs it even
    when the container is untouched; and the module's other packages and the
    functions of other modules that the mover calls are outside the digest.
    And that the container runs as root is stated here, not held: the policy
    has no dimension for it.
  - **Not held:** the rest of `moverSecurityContext`. It is a pod security
    context, and of it only `windowsOptions.hostProcess` is held: the user
    and groups the mover runs as, its sysctls and its SELinux and seccomp
    settings are written as authored, since the environment policy has no
    dimension for them. The service account the mover runs under
    (`moverServiceAccount`, on every mover) is an identity carried as
    authored. Nor are held a mover's affinity; the volumes mounted into it
    (`moverVolumes`: a Secret, a claim or an NFS export; the type holds no
    host path); the type of the Service a mover is reached through
    (`serviceType`); and whether an `rsyncTLS`, `rclone`, `restic` or
    `syncthing` mover runs with elevated permissions, which is no field of
    the object: an administrator sets it with an annotation on the namespace
    (`volsync.backube/privileged-movers`), which the linked operator's
    controllers read and hand to the builder of each of those four movers.
    The `rsync` mover's builder drops that answer (above).
  - **Hosts are not checked.** A host these objects name is one the mover
    reaches, not an artifact source, and none is held to the policy's allowed
    registries: `rsync.address`, `rsyncTLS.address`, a Syncthing peer's
    `address`, and the `server` of an NFS export among `moverVolumes`.
  - **No field holds a literal secret by design, and none is checked.** A
    credential is the name of a Secret (`restic.repository`,
    `rclone.rcloneConfig`, `rsync.sshKeys`, `rsyncTLS.keySecret`). The
    `parameters` of an `external` provider are a map of strings the provider
    reads: they are written as authored and not checked, under a policy that
    forbids explicit secrets too.

  **The operator's own rule is not repeated.** When it reconciles, VolSync
  refuses an object that configures no replication method or more than one
  (`a replication method must be specified`, `only one replication method
  can be supplied`). Launcher does not: such a component builds, and the
  rule stays the operator's.

  **Beside the `volsync` trait.** The trait derives a ReplicationSource for a
  claim of the workload it is attached to, a Restic backup on a schedule,
  and names it `<sourcePVC>-backup`. The `replicationsource` kind is the
  authored object, for what the trait does not express: the whole spec, any
  mover, a source that belongs to no workload. No trait builds a
  ReplicationDestination. The trait's object and a `replicationsource`
  component's are one kind: given one name in one namespace they are refused
  (`generated-object collision: ReplicationSource.volsync.backube
  "default/data-backup" is generated by both …`).

  **Labels and annotations** are the `labels` and `annotations` properties. A
  mover's `moverPodLabels` are not these: they are labels the operator adds to
  the mover pods. They are held as a pod's labels are: the component label's
  key holds the component's value there or the build is refused, and a key the
  consumer reserved is refused (`mover pod label "…" may not be set`); see
  "Component label and ownership" and "Reserved metadata keys" in the OAM
  model. Launcher writes nothing there. A destination's `serviceAnnotations`
  reach a Service and are not read.

  **Not covered.** Whether what is referred to exists (the claim to copy, a
  Secret, a storage or snapshot class, a ServiceAccount), and whether the
  mover can reach what the object names. The object's status is the
  operator's and is not written.
