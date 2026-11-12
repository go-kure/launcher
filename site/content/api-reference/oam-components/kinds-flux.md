---
title: "Kinds: Flux"
weight: 6
---

Each kind below has the same sections: Properties, Outputs, Validation and
Example. Its implementation, history and tests are on the
[contributor page](../../contributing/oam-reference/kinds-flux.md).

## fluxcd-alert, fluxcd-provider, fluxcd-receiver, imagepolicy, imagerepository, imageupdateautomation, artifactgenerator, resourcesetinputprovider

`fluxcd-alert`, `fluxcd-provider`, `fluxcd-receiver`, `imagepolicy`, `imagerepository`,
`imageupdateautomation`, `artifactgenerator`, `resourcesetinputprovider` are the kind-named
projections of objects of the Flux APIs beside the sources, the HelmRelease and the
Kustomization: a notification.toolkit.fluxcd.io/v1 Alert, Provider and Receiver, an
image.toolkit.fluxcd.io/v1 ImagePolicy, ImageRepository and ImageUpdateAutomation, a
source.extensions.fluxcd.io/v1beta1 ArtifactGenerator, and a
fluxcd.controlplane.io/v1 ResourceSetInputProvider of the Flux Operator's API. The type is
`fluxcd-alert` rather than `alert`, which beside a `prometheusrule` would read as an alerting
rule.

### Properties

**Nothing gates what a Flux object reaches outside its namespace, or the
identity it acts under.** A Flux object may name objects of another
namespace, and some may act under an account that is not their
controller's. Launcher writes such a field as authored and refuses none,
under a nil policy and a strict one alike: the environment policy has no
rule for the namespace or the account a Flux object names, and
`ApplyPolicy` checks neither here, as it checks neither on `helmrelease` (its
`chart.spec.sourceRef.namespace`, `chartRef.namespace`,
`serviceAccountName` and `kubeConfig`) and on `fluxcd-kustomization` (its
`sourceRef.namespace`, `serviceAccountName` and `kubeConfig`). Whoever may
author a component may author these fields, and a consumer that restricts
them restricts the component types it registers.
*Assumption, not read here:* a cluster's Flux controllers can be started so
that they refuse a reference into another namespace. Launcher reads no such
setting, and no build depends on it.

**In the Flux namespace these objects share a namespace with every other
application's Flux objects.** When a Flux namespace is set, the objects of
this group land there (Namespace, below), and so do those of every other
application built with that Flux namespace. A reference without a
namespace, and a selector over "the object's namespace", then reach them:
an Alert's `providerRef` may name another application's Provider, a
Receiver's `resources` another application's Flux objects, an
ImagePolicy's `imageRepositoryRef` another application's ImageRepository,
the source of an ImageUpdateAutomation or of an ArtifactGenerator another
application's source, a ResourceSetInputProvider's selector without a
`namespace` another application's ExternalArtifacts, and an
ImageUpdateAutomation without a
`policySelector` takes every ImagePolicy there. An ImageRepository there
is in the namespace of every other application's ImagePolicy, so a
reference to it is not the cross-namespace one the API says `accessFrom`
is for. Nothing gates this
either, and no `namespace` has to be written for it.

The fields, per kind:
- `fluxcd-alert`: `eventSources[].namespace` names the namespace of the
  objects whose events are sent, and a source's `name: "*"` takes every
  object of its kind there, or, with `matchLabels`, every one that carries
  those labels. The events
  go to the Provider `providerRef` names, so an Alert sends what happens
  to another namespace's Flux objects to a receiver its author chose.
  `providerRef` holds a name and no namespace: the Provider is one of the
  namespace the Alert lands in. An Alert names no account.
- `fluxcd-provider`: the object makes the notification controller send
  the events of the Alerts that name it to `address`, which the API calls
  "the endpoint, in a generic sense, to where alerts are sent" (for some
  types "a project ID or a namespace"), through the proxy the Secret of
  `proxySecretRef` holds. Both are hosts outside the cluster unless the
  receiver runs in it; launcher writes `address` as authored and does not
  read the Secret.
  **`serviceAccountName` names the account the controller authenticates
  as:** in the API's words "the Kubernetes ServiceAccount used to
  authenticate with cloud provider services through workload identity",
  for the types `azureeventhub`, `azuredevops` and `googlepubsub`, and
  only with the controller's `ObjectLevelWorkloadIdentity` feature gate.
  The field holds a name and no namespace. *Assumption, not read here:* the
  controller takes the account from the namespace of the Provider, which
  in the Flux namespace holds the accounts of every application's Flux
  objects. Launcher writes it as authored and refuses none.
- `fluxcd-receiver`: **a Receiver opens an inbound path on the
  notification controller.** A request there that the Receiver validates,
  by the procedure its `type` names ("the validation procedure and payload
  deserialization"), makes the controller reconcile the objects
  `resources` names. **`resources[].namespace` names the namespace of the
  objects a webhook makes the controller reconcile, and a resource's
  `name: "*"` takes every object of its kind there, or, with
  `matchLabels`, every one that carries those labels.** So a Receiver lets
  the sender of a webhook trigger the reconciliation of another
  namespace's Flux objects. The token a request is validated with is in
  the Secret `secretRef` names, one of the namespace the Receiver lands in;
  a `generic-oidc` Receiver validates a request by the OIDC issuers of
  `oidcProviders` instead. **`oidcProviders[].issuerURL` names a host
  outside the cluster**, which the controller discovers the issuer at;
  launcher writes it as authored and holds its host to no policy; a user
  or a password in it is refused (Policy, below). A Receiver names no
  account.
- `imagepolicy`: `imageRepositoryRef.namespace` names the namespace of
  the ImageRepository whose scanned tags the policy selects from, so an
  ImagePolicy may name the ImageRepository of another namespace and read
  the tags it scanned. The API documents, on the
  ImageRepository, an `accessFrom` list "for allowing cross-namespace
  references to the ImageRepository object based on the caller's namespace
  labels"; launcher reads no ImageRepository to see whether the one named
  allows it. An ImagePolicy names no account.
- `imagerepository`: the object makes its controller scan the tags of
  `image` at the registry that name holds, which is a host outside the
  cluster unless the registry runs in it. **`accessFrom` opens the
  ImageRepository to other namespaces:** the API calls it "an ACL for
  allowing cross-namespace references to the ImageRepository object based
  on the caller's namespace labels", its `namespaceSelectors` are joined
  by OR, and "an empty map of MatchLabels matches all namespaces in a
  cluster", so `accessFrom: {namespaceSelectors: [{}]}` opens the scanned
  tags to an ImagePolicy of any namespace. **`serviceAccountName` names
  the account the scan authenticates as:** in the API's words "the
  Kubernetes ServiceAccount used to authenticate the image pull if the
  service account has attached pull secrets". The field holds a name and
  no namespace. *Assumption, not read here:* the controller takes the
  account from the namespace of the ImageRepository, which in the Flux
  namespace holds the accounts of every application's Flux objects.
  **`insecure: true` "allows connecting to a
  non-TLS HTTP container registry".** **The proxy the registry is reached
  through is not in the object:** `proxySecretRef` names a Secret that
  holds its configuration, and no build sees that host. Launcher writes
  each of the four as authored and refuses none.
- `imageupdateautomation`: `sourceRef.namespace` names the namespace of
  the GitRepository that, in the API's words, gives "access details to a
  git repository". **An ImageUpdateAutomation makes its controller commit
  to that repository and push**, to the branch the GitRepository or
  `git.checkout.ref` names, or to `git.push.branch` and `git.push.refspec`;
  so one that names another namespace's GitRepository writes to a
  repository with the access details that GitRepository gives. Nothing
  holds the
  branch or the refspec either. It names no account. The ImagePolicies
  whose selections it applies are those of the namespace it lands in
  (`policySelector` narrows them; it names no other namespace), and the
  Secret of `git.commit.signingKey` is one of that namespace too.
  **In the Flux namespace, an ImageUpdateAutomation without a
  `policySelector` selects every ImagePolicy there, those of other
  applications included** (the API: "By default includes all policies in
  namespace"), and the automation then commits their selections through
  its own GitRepository.
- `artifactgenerator`: `sources[].namespace` names the namespace of a
  Flux source (a Bucket, GitRepository, OCIRepository, HelmChart or
  ExternalArtifact); left out, the API takes "the same namespace as the
  ArtifactGenerator". **An ArtifactGenerator copies files out of the
  sources it names into the artifacts it generates**, which the API
  describes as ExternalArtifacts, so one that names another namespace's
  source republishes that source's content as an artifact its author
  named. `artifacts[].namespace` names the namespace an ExternalArtifact
  is written into; left out, the generator's own. **There the controller
  creates it, adopts an ExternalArtifact of that name no generator owns,
  takes over one another generator owns, and deletes it once no artifact
  names it.** Where `pathPattern` is set the namespace may be a
  `{capture}` placeholder, which the controller fills in from the content
  of the source when it reconciles: the source then chooses the
  namespace, and no build sees it. **`serviceAccountName` names the
  account, of the namespace the generator lands in, that the controller
  checks every ExternalArtifact against**, in every namespace an artifact
  names, its own included, with a SubjectAccessReview before each write;
  the account's RBAC decides where the generator may write, while the
  writes themselves use the controller's own credentials. Left out, an
  artifact in the generator's own namespace is not checked, and one in
  another namespace is checked against the account a cluster may
  configure with `--default-service-account`; without that setting it is
  not checked either. In the Flux namespace
  the account named is one of the shared namespace, as on `helmrelease`
  and `fluxcd-kustomization`.
- `resourcesetinputprovider`: the object makes the Flux Operator ask the
  provider its `type` names for input sets, which, in the API's words,
  "populate the inputs" of a ResourceSet; **the answer decides what that
  ResourceSet deploys.** Every type but `Static` and `ExternalArtifact`
  asks the host of `url` (Policy, below), with the credentials of the
  Secret `secretRef` names; an `ExternalService` provider sends that
  Secret's token or user and password to it. **An `ExternalArtifact`
  provider reads ExternalArtifacts of the cluster instead:
  `selectors[].namespace` names the namespace it lists them in, and
  `namespace: "*"` lists them "across all namespaces".** Left out, the
  namespace is the provider's own. **`serviceAccountName` names the
  account the operator acts as:** in the API's words the ServiceAccount
  "used for authentication with AWS, Azure or GCP services through
  workload identity federation features", taken, without one, from "the
  ServiceAccount of the operator". **For an `ExternalArtifact` provider,
  the list is made with the operator's own client unless an account is
  set**; with one, the operator lists as that account, of the namespace
  the object lands in. *Read from the controller of the linked module, not
  from the type:* an operator started with a default account lists as
  that account where none is set; launcher reads no operator setting, and
  the installed operator may differ from the linked one. Launcher writes
  the selectors and the account as authored and refuses none.

**Authored.** The properties are the top-level json fields of the spec
type, decoded strictly at every depth: an unknown key is refused wherever
it sits (a source, a reference, a policy).
- `fluxcd-alert` (`AlertSpec`): `providerRef`, `eventSources`,
  `eventSeverity`, `inclusionList`, `exclusionList`, `eventMetadata` and
  `suspend`. The v1 Alert has no `summary`: it is the `eventMetadata`
  key `summary`, and an authored `summary` is refused with that hint.
- `fluxcd-provider` (`ProviderSpec`): `type`, `channel`, `username`,
  `address`, `timeout`, `proxySecretRef`, `secretRef`, `certSecretRef`
  (each a `name`), `serviceAccountName`, `suspend` and
  `commitStatusExpr`. The v1 Provider has no `interval` (unused under
  v1beta3 too) and no `proxy` (the Secret `proxySecretRef` names holds the
  proxy's address, and optionally its username and password); an authored
  one is refused with that hint. `type` takes every value of the API's
  enum, `zoom`, `incident.io` and `mastodon` among them; each reads its
  token from the Secret `secretRef` names.
  **`commitStatusExpr` is a CEL expression the controller evaluates** to
  the message of a commit status; launcher writes it as authored and does
  not parse it.
- `fluxcd-receiver` (`ReceiverSpec`): `type`, `interval`, `events`,
  `resources` (each an `apiVersion`, `kind`, `name`, `namespace`,
  `matchLabels` and `filter`), `resourceFilter`, `secretRef` (a `name`),
  `oidcProviders` (each an `issuerURL`, `audience`, `variables` and
  `validations`) and `suspend`. **`resourceFilter`, a resource's `filter`,
  and the `expression` of an OIDC variable and of an OIDC validation are
  CEL expressions the controller evaluates**; launcher writes them as
  authored and does not parse them.
- `imagepolicy` (`ImagePolicySpec`): `imageRepositoryRef`, `policy`
  (`semver`, `alphabetical`, `numerical`), `filterTags`,
  `digestReflectionPolicy`, `interval` and `suspend`. An authored
  `filterTags` is written with both its `pattern` and its `extract`, the
  one left out as the empty string: the Go type omits neither.
- `imagerepository` (`ImageRepositorySpec`): `image`, `interval`,
  `timeout`, `secretRef`, `proxySecretRef`, `certSecretRef` (each a
  `name`), `serviceAccountName`, `suspend`, `accessFrom`
  (`namespaceSelectors`, each a `matchLabels`), `exclusionList`,
  `provider` and `insecure`. `image` names a repository, such as
  `ghcr.io/org/app`; launcher does not parse it beyond its registry
  (Policy, below), and an entry of `exclusionList` is a regular expression
  it does not compile.
- `imageupdateautomation` (`ImageUpdateAutomationSpec`): `sourceRef`,
  `git` (`checkout`, `commit`, `push`), `interval`, `policyChangeDelay`,
  `policySelector`,
  `update` and `suspend`. `git.commit.messageTemplate` is a template the
  controller renders; launcher writes it as authored and does not parse
  it.
- `artifactgenerator` (`ArtifactGeneratorSpec`): `commonMetadata`,
  `sources`, `pathPattern`, `serviceAccountName` and `artifacts` (each a
  `name`, `namespace`, `revision`, `originRevision`, `inputsFrom` and
  `copy`; a copy a `from`, `to`,
  `exclude`, `strategy` and `optional`). A copy's `optional` lets a copy
  that matches no file pass, where the API's default, `false`, fails the
  reconcile; an authored `false` is not written. An artifact's
  `inputsFrom` names a YAML file, `@<alias>/<path>`, of one of the
  generator's own sources or of the artifact itself, whose content the
  generated ExternalArtifact exports in its status: whatever reads that
  status reads content of the sources, which the generator republishes in
  any case. **`commonMetadata` is not the object's own metadata:** its
  `labels` and `annotations` are written into the spec, where the API says
  they are "applied to all resources", the ExternalArtifacts it generates;
  the ArtifactGenerator's own labels and annotations are the `labels` and
  `annotations` properties. At generation they are held to the reserved
  metadata keys (`pkg/oam` README, "Reserved metadata keys"), and not to
  the component label's value: an ExternalArtifact is no pod. Launcher
  writes nothing there. The
  aliases, the `@<alias>/…` paths and the `{capture}` placeholders are
  written as authored: launcher resolves none of them. `serviceAccountName`
  and an artifact's `namespace` are written as authored too, and what they
  reach is under "Nothing gates what a Flux object reaches" above.
  **`managedNamespaces` is refused, whatever its value** (`managedNamespaces:
  refused: it hands the controller the lifecycle of cluster-scoped
  Namespaces, …`). It makes the controller create the Namespaces the
  artifacts name, adopt an existing one no generator owns, take over one
  another generator owns, label them, and, with `prune`, delete them once
  no artifact names them or the generator goes, adopted ones included,
  with everything in them, the objects of other applications too, unless
  the Namespace itself opts out with Flux's prune annotations. Only
  `default`, `kube-system`, `kube-public`, `flux-system` and the
  generator's own namespace are out of its reach; everything else is
  bounded only by the cluster-wide namespace rights of the account it
  checks. An application's components make namespaced objects, and the
  Namespaces here are the controller's, not objects the build emits, so
  no environment policy and no object-kind rule of a consumer could refuse
  them short of dropping the `artifactgenerator` type. A Namespace an
  application needs is the `namespace` component's.
- `resourcesetinputprovider` (`ResourceSetInputProviderSpec`): `type`,
  `url`, `serviceAccountName`, `secretRef`, `certSecretRef` (each a
  `name`), `insecure`, `defaultValues`, `filter` (`includeBranch`,
  `excludeBranch`, `includeTag`, `excludeTag`, `includeEnvironment`,
  `excludeEnvironment`, `labels`, `limit` and `semver`), `skip` (`labels`),
  `schedule` (each a `cron`, `timeZone` and `window`) and `selectors`
  (each a `name`, `namespace`, `matchLabels` and `matchExpressions`).
  `defaultValues` is a free map, written as authored; the regular
  expressions of `filter`, its `semver` range and a schedule's `cron` are
  written as authored and not parsed.
- **No default is filled.** The API's own (`eventSeverity: info`,
  `digestReflectionPolicy: Never`, a policy's `order: asc`,
  `update: {strategy: Setters}`, an ImageRepository's `provider: generic`
  and its `exclusionList` of `^.*\.sig$`, a Receiver's `interval: 10m`, a
  ResourceSetInputProvider's `filter.limit: 100` and a schedule's
  `timeZone: UTC`) is
  applied by the API server to what the object leaves out; the `timeout`
  of an ImageRepository left out is, in the API's words, the "'Interval'
  duration", and the `audience` of a Receiver's OIDC provider
  `notification-controller`, which are the controller's to apply. A
  schedule's `window` left out is written `0s`, the API's own default.
  One default cannot apply: `sourceRef.kind`, which
  the API defaults to `GitRepository` and the Go type writes empty when
  it is left out, an empty value being a value. It is required instead
  (below). `eventSeverity`, `digestReflectionPolicy`, a policy's `order`,
  `update.strategy`, an ImageRepository's `provider` and a schedule's
  `timeZone` are strings the
  type omits when empty, so an authored `""` on one would be left out and
  defaulted: it is refused (`eventSeverity: "" cannot be carried by the
  Flux API types (…)`). A ResourceSetInputProvider's `filter.limit: 0` is
  omitted the same way, and the API server would then apply its default
  100, so it is refused too (`filter.limit: 0 cannot be carried by the
  Flux Operator API types (…)`). An authored empty `exclusionList` (`[]`) is not refused, and it does not
  mean that nothing is excluded: the type omits an empty list, so the
  object gets the API's default list (`^.*\.sig$`), and the controller also
  reads an empty list as that default.
- **A duration is held to the pattern its field declares**, as on the Flux
  kinds below: unsigned, in the units `ms`, `s`,
  `m` and `h`. A value outside it is refused (`imagepolicy: interval
  "-5m" is invalid: must be a Flux duration (…)`), and so is one below a
  millisecond, which would be written in a unit the pattern does not take
  (`0.5ms` as `500µs`). It is written as Go formats it (`10m` as
  `10m0s`). `TestFluxKinds_DurationsMatchMarkers` holds each kind's list
  of durations to its type and to the pattern markers of the linked
  source: the `interval` of a Receiver, of an ImagePolicy and of an
  ImageUpdateAutomation, and the `interval` and `timeout` of a Provider
  and of an ImageRepository, and the `window` of a ResourceSetInputProvider's
  schedule; an Alert and an ArtifactGenerator have none. **A schedule's
  `window` is held by its authored text:** it sits under a list, and a
  window of `500us` in any schedule is refused (`resourcesetinputprovider:
  schedule[].window "500us" is invalid: must be a Flux duration (…)`); its
  pattern takes `h`, so one is written as Go formats it (`90m` as
  `1h30m0s`).
  **The `timeout` of a Provider and of an ImageRepository takes no `h`:**
  its pattern holds the units `ms`, `s` and `m`, so `1h` is refused
  (`imagerepository: timeout "1h" is invalid: must be a Flux duration
  (…)`) and `60m` is not.
  Go formats an hour or more with an `h`, which the pattern would refuse
  at apply, so such a timeout is written in minutes (`90m` as `90m0s`),
  as the timeout of a Flux source is.

**Required** is a field the API requires that the Go type writes whether or
not it was authored, the rule every kind follows (see the Prometheus
operator's kinds below). Each must be authored (`providerRef: required
(…)`, `eventSources[1].name: required (…)`); an authored empty value is a
value, and the API server's to refuse.
- A `fluxcd-alert`: `providerRef` with its `name`, and `eventSources`; of
  each source its `kind` and `name`.
- A `fluxcd-provider`: `type`; of an authored `secretRef`,
  `proxySecretRef` or `certSecretRef` its `name`.
- A `fluxcd-receiver`: `type` and `resources`; of each resource its `kind`
  and `name`; of an authored `secretRef` its `name`; of each OIDC provider
  its `issuerURL` and `validations`, of each validation its `expression`
  and `message`, of each variable its `name` and `expression`.
- An `imagepolicy`: `imageRepositoryRef` with its `name`, and `policy`; of
  a `semver` policy its `range`.
- An `imagerepository`: `interval`; of an authored `secretRef`,
  `proxySecretRef` or `certSecretRef` its `name`; of an authored
  `accessFrom` its `namespaceSelectors`. **`image` is required too, by
  another rule:** the API requires it and the Go type leaves an empty one
  out, so the kind refuses a component without it, and one whose `image`
  is authored empty (`image: required (…)`).
- An `imageupdateautomation`: `sourceRef` with its `kind` and `name`, and
  `interval`. Of an authored `git`, `commit` with its `author` and the
  author's `email`; of an authored `git.checkout`, its `ref`; of an
  authored `git.commit.signingKey`, its `secretRef` with its `name`. Of a
  match expression of `policySelector`, its `key` and its `operator`
  (`policySelector.matchExpressions[0].operator: required (…)`); its
  `values` and the operator's value are the API server's.
- An `artifactgenerator`: `sources` and `artifacts`; of each source its
  `alias`, `kind` and `name`; of each artifact its `name` and `copy`; of
  each copy its `from` and `to`.
- A `resourcesetinputprovider`: `type`; of an authored `secretRef` or
  `certSecretRef` its `name`; of each schedule its `cron`. Of a match
  expression of a selector, its `key` and its `operator`
  (`selectors[0].matchExpressions[0].operator: required (…)`); its
  `values` and the operator's value are the API server's.

**These lists are generated from the markers of the Go source, not from a
CRD** (see ["Required fields a kind writes unauthored
and does not refuse"](conventions.md#required-fields-a-kind-writes-unauthored-and-does-not-refuse)). The API modules of the Flux controllers hold the Go
types and ship no CRD; `TestKindComponents_RequiredIsGenerated` derives
each list from the markers of the linked modules' source, as the kinds of
the Prometheus operator's API are derived: every field the source requires
that the type writes unauthored is listed, and nothing else is. A field
with no marker whose json tag keeps it when empty is required by the
schema generators' rule (a match expression's `key` and `operator`, a verification's `provider`). The Flux Operator's module ships its CRDs: the list of a
`resourcesetinputprovider` is derived from its CRD instead, and held to that
CRD by the API server. Of an `artifactgenerator`, the
fields of `managedNamespaces` stay in its list by hand, under a path the
kind refuses whatever is authored there.

### Outputs

Each emits that one object, named after the component unless `objectName` names it; the
handler adds no label, no annotation and no default.

**Namespace.** The object lands in the Flux namespace when one is
configured, else in the build namespace (`SetFluxNamespace`), as the Flux
kinds below do, and its name is claimed there. A reference without a
namespace of its own is written without one, under a Flux namespace too:
launcher fills none in. The Flux objects of the same document
(`helmrelease`, `fluxcd-kustomization`, the sources) land in the Flux
namespace with it. `FluxNamespaceReads` reports the ConfigMaps and Secrets
a kind reads by name from the namespace it lands in, so that a trait's
object one of them names moves with it; an Alert, an ImagePolicy and an ArtifactGenerator read
none, a Provider and an ImageRepository read the Secrets of `secretRef`,
`proxySecretRef` and `certSecretRef`, a Receiver reads the Secret of
`secretRef`, a ResourceSetInputProvider reads the Secrets of `secretRef`
and `certSecretRef`, and an ImageUpdateAutomation reads
the Secret of `git.commit.signingKey.secretRef`. The ServiceAccount of a
Provider's, an ImageRepository's, a ResourceSetInputProvider's or an
ArtifactGenerator's `serviceAccountName` is not reported: the report holds
ConfigMaps and Secrets only. *Required reads*: what of that a kind's object
cannot become Ready without (`FluxRequiredReads`), which a delivery whose groups wait for
their Flux objects refuses a trait to make in a later group, is every Secret a Receiver, an ImageRepository
and an ImageUpdateAutomation read, none under `suspend: true`. An Alert, a
Provider, an ImagePolicy, an ArtifactGenerator and a
ResourceSetInputProvider require none: a ResourceSetInputProvider reads its
Secrets only on a schedule or while not disabled by annotation, which the
check does not follow.

**Labels and annotations** are the `labels` and `annotations` properties.

### Validation

**A `fluxcd-receiver` resource's `apiVersion` is launcher's own check,
outside these lists.** The API marks it optional, and the notification
controller fills it in only for the kinds of `defaultFluxAPIVersions` in
notification-controller v1.9.4 (`Bucket`, `GitRepository`, `HelmChart`,
`HelmRepository`, `OCIRepository`, `ImageRepository`,
`ArtifactGenerator`); for any other kind it fails every webhook the
Receiver gets. So a resource of any other kind without `apiVersion`, or
with an empty one, is refused at build time (`fluxcd-receiver:
resources[1].apiVersion: required for kind "Kustomization", …; set
apiVersion, such as kustomize.toolkit.fluxcd.io/v1`).
The refusal names the version of the refused kind in the Flux API module
launcher pins: `kustomize.toolkit.fluxcd.io/v1` for a `Kustomization`,
`helm.toolkit.fluxcd.io/v2` for a `HelmRelease`, `image.toolkit.fluxcd.io/v1`
for an `ImagePolicy` or an `ImageUpdateAutomation`, and
`source.toolkit.fluxcd.io/v1` for an `ExternalArtifact`, the other kinds the
Receiver API's `kind` enum admits. A kind outside the enum gets the
HelmRelease version as an example.

**Not refused:**
- a list the API wants an item of that is authored empty
  (the `sources`, `artifacts` and `copy` of an `artifactgenerator`), and
  one longer than the API allows;
- every value rule of the API but the pattern of a duration: enumerations
  (`eventSeverity`, a source's `kind`, `digestReflectionPolicy`, a
  policy's `order`), lengths (a source's `name` and `namespace`,
  `summary`), and that a source with `matchLabels` is named `*`, which
  the type documents and no marker states;
- a `policy` that names none of `semver`, `alphabetical` and `numerical`,
  or more than one: the type calls it a union, and no marker holds it to
  one;
- of a `fluxcd-provider`, the enumeration of `type`, the lengths of
  `channel`, `username` and `address`; an `address` that is no URL with a host is
  written as authored unless it holds an `@` (Policy, below);
- of a `fluxcd-receiver`, the enumerations (`type`, a resource's `kind`),
  the lengths of a resource's `name` and `namespace`, the pattern of an
  `issuerURL` (an `http` or `https` URL; one that parses with no host is
  written as authored unless it holds an `@`, and one that does not parse
  is refused without naming the value), a `validations` list authored
  empty, two OIDC providers of one `issuerURL`, which the type keys the
  list by, and that a resource with `matchLabels` is named `*`, which the
  type documents and no marker states;
- of an `imagerepository`, the enumeration of `provider`, the length of
  `serviceAccountName`, an `exclusionList` of more than 25 entries, an
  `accessFrom` whose `namespaceSelectors` is authored empty, and whether
  `image` is a repository name the controller accepts: one that carries a
  tag or a digest is written as authored;
- an `imageupdateautomation` with no `git`, which the type documents as
  "technically optional, but in practice mandatory" and no marker
  requires; the pattern of `git.push.refspec` and the enumerations
  (`sourceRef.kind`, `update.strategy`, a signing key's `type`);
- of an `artifactgenerator`, the patterns (`serviceAccountName`, a
  source's `alias`, `name` and `namespace`, `pathPattern`, an artifact's
  `inputsFrom`, `revision` and `originRevision`, a copy's `from` and
  `to`), the lengths and the
  enumerations (a source's `kind`, a copy's `strategy`); that an alias is
  unique and that a path, a `revision` or an `originRevision` names an
  alias that is declared, which the type documents and no marker states;
- of a `resourcesetinputprovider`, the enumeration of `type`, the pattern
  of `url` (an `http`, `https` or `oci` URL) and the maximum of
  `filter.limit` (10000); the `url` scheme each type takes is an
  expression rule (below).

**The APIs' expression rules are not checked.** A kind checks an
expression rule only where the check is held to the API server's own
validator, which answers from a CRD, and the build
reads none for these kinds: the linked modules of the Flux controllers' APIs
ship none, and the CRDs vendored for the tests are test data. **The Flux Operator's
module does ship its CRDs, and the `resourcesetinputprovider` kind
deliberately checks none of their rules:** it leaves every one to the API
server, as the other Flux kinds do. A component that breaks one of the rules
below builds, and the API server refuses the object at apply.
- The types of an Alert, of an ImageRepository and of an
  ImageUpdateAutomation declare none.
- A `fluxcd-provider`: `commitStatusExpr` set on a `type` other than
  `github`, `gitlab`, `gitea`, `bitbucketserver`, `bitbucket` and
  `azuredevops`. It builds and is refused at apply.
- A `fluxcd-receiver`: a `generic-oidc` receiver without an OIDC provider
  or with a `secretRef`, and a receiver of another type with
  `oidcProviders` or without a `secretRef`. Each builds and is refused at
  apply.
- An `imagepolicy`: `interval` without `digestReflectionPolicy: Always`,
  and `digestReflectionPolicy: Always` without `interval`. Each builds and
  is refused at apply.
- An `artifactgenerator`: where no `pathPattern` is set, every artifact's
  `name` must be a Kubernetes object name (lower case letters, digits,
  `-` and `.`), and its `namespace`, if written, a namespace name (lower
  case letters, digits and `-`, at most 63). One that is not,
  `App_Manifests` for one, builds and is refused at apply.
- A `resourcesetinputprovider`: seventeen rules, sixteen of them on the
  spec and one on a selector. `url` must be left out under `Static` and
  `ExternalArtifact` and written under every other type, with the scheme
  the type takes (`http` or `https` for a Git, AzureDevOps and
  `ExternalService` provider, `https` for AWSCodeCommit, `oci` with a
  repository after the registry for an OCI provider); `https` for an
  `ExternalService` provider unless `insecure` is true, and `insecure`
  only under `ExternalService` and `OCIArtifactTag`; `serviceAccountName`
  only under the AzureDevOps, AWSCodeCommit, artifact-tag and
  `ExternalArtifact` types; `secretRef` and `certSecretRef` not under
  the types that take none; `selectors` written under `ExternalArtifact`
  and only there; and a selector's `name` not beside `matchLabels` or
  `matchExpressions`. Each builds and is refused at apply.

**Policy.** Two dimensions of the environment policy reach three of these
objects: the allowed registries hold the `image` of an `imagerepository`
and the host of a `resourcesetinputprovider`'s `url`, and a policy that
forbids explicit secrets refuses a `fluxcd-provider` `address` that is the
credential (below). No other dimension reaches any of them: they run no
pod, request no storage and have no replica count.
- **No field of an Alert holds a secret or a host.** The address and the
  credentials are the Provider's. `eventMetadata` is a free map, written to
  the object as authored under a policy that forbids explicit secrets too.
- **A user or a password in a Provider's `address` is
  refused**, under every policy and under none, by the rule that refuses
  one in an inline chart source's URL (`fluxcd-provider: address must not
  carry a user or password, which would be written in plain text into the
  Provider; …`): it would sit in the object in plain text. An `address`
  that is no URL with a host is refused when it holds an `@`
  (an `address` with no `@` passes, since the API allows a project ID or a
  namespace there); `bot:pw@host` and `bot@host` parse as URLs with no
  user, so parsing alone does not clear them.
- **An `address` that is the credential is refused under a policy that
  forbids explicit secrets** (`RefusalExplicitSecret`; `fluxcd-provider:
  address: the address of a msteams Provider is the credential it posts
  with, …`), since the object is in the build's output; the message names
  the type and quotes nothing of the value. Read in the notification
  controller's source at v1.10.0 (`internal/notifier`): `discord`,
  `rocket`, `msteams`, `googlechat` and `lark` post to the address with no
  token of their own, so it is a webhook URL whose path or query holds the
  token; `slack` does the same where its Secret holds no token, and the
  kind does not read the Secret, so a `slack` address is refused even at
  the public API endpoint a token-holding Provider names; `generic` and
  `generic-hmac` post to the address as written, and nothing tells an
  endpoint from one whose path or query is the credential. An
  `azureeventhub` address that holds `SharedAccessKey` is a connection
  string with its key, which the notifier connects with as written; any
  other is an endpoint and passes. The remedy the message names: omit
  `address`, set `secretRef`, and put the URL under the `address` key of
  that Secret, which the controller reads in place of `address` for every
  type (`internal/server/event_handlers.go`). An empty `address` passes, and so
  does every `address` under a policy that allows explicit secrets, one
  that does not answer the question, and none.
  **A token in the path or the query of an
  address of another type is not something the kind can tell**, and is
  written as authored. **Not held:**
  the host of `address`: the Provider sends events there and
  fetches no artifact, so no dimension of the policy speaks of them;
  `serviceAccountName`. The credentials, the proxy configuration and the
  certificates of `secretRef`, `proxySecretRef` and `certSecretRef` are
  Secrets the object names, not values.
- **Flux 2.10 or later.** The Alert and the Provider are of the v1 API,
  which notification-controller serves from Flux 2.10 on; an older
  cluster refuses them. Launcher does not read the cluster's Flux
  version.
- **What the v1.10.0 controller refuses in the Secrets, which launcher
  never reads.** A `proxy` key in the Secret `secretRef` names: the proxy
  goes under the `address` key of the Secret `proxySecretRef` names
  (`internal/server/event_handlers.go`). TLS data in `secretRef` for a
  commit-status type: it goes in the Secret `certSecretRef` names. A
  static `token` for `azureeventhub` (`internal/notifier/azure_eventhub.go`):
  the type authenticates with a SAS connection string in `address` or through
  workload identity. A Provider migrated from v1beta3 that relied on any
  of these builds; the controller refuses it when it sends an event.
- **A `pagerduty` Provider's routing key in `channel` is an identifier,
  not a credential**, and is written as authored under every policy, one
  that forbids explicit secrets included. The
  controller hands `channel` to the pagerduty notifier as its Events API
  routing key (`internal/notifier/factory.go`, `pagerduty.go` at v1.10.0).
  The key reaches only the Events API of the service it routes to, where
  it opens alerts and acknowledges or resolves one by its `dedup_key`; it
  reads nothing and changes no configuration. Launcher does not hold it
  as a secret, and no Secret can supply it: the controller reads `channel`
  from the spec alone (`internal/server/event_handlers.go`), unlike
  `address`. Refusing it would leave the type unusable under such a policy.
- **No field of a Receiver holds an image or a secret.** The token is in
  the Secret `secretRef` names. **A user or a password in an OIDC
  provider's `issuerURL` is refused**, under every policy and under none,
  by the same rule as a Provider's `address` (`fluxcd-receiver:
  oidcProviders[1].issuerURL must not carry a user or password, which
  would be written in plain text into the Receiver; …`), and so is one
  that does not parse, or is no URL with a host and holds an `@`; the
  refusal names the
  provider's index, not the value. **The host of an OIDC provider's
  `issuerURL` is not held:** the controller discovers an issuer there and
  fetches no artifact, so no dimension of the policy speaks of it. That a
  Receiver opens an inbound path is not a dimension of the policy either.
- **No field of an ImagePolicy holds an image, a secret or a host.** The
  image and the credentials of its registry are the ImageRepository's; the
  policy holds the rule by which one of that image's tags is selected.
  The allowed registries and the tag rule of the environment policy are
  not applied to it.
- **The registry of an ImageRepository's `image` is held to the allowed
  registries**, by the rule that holds the image of a pod: under a policy
  that allows `registry.example`, `image: ghcr.io/shop/web` is refused
  (`image "ghcr.io/shop/web" is not from an allowed registry
  [registry.example]`, a refusal of the class `registry`). A name with no
  registry is one of `docker.io`, and a registry with a port is another
  registry than the one without. A policy that lists no registry refuses
  none, and neither does a build without a policy. **The tag rule is not
  applied:** the field names a repository, and the tags are what the scan
  finds. **Not held:** the host of the proxy, which is in the Secret
  `proxySecretRef` names; `insecure`, which no dimension of the policy
  speaks of; `provider` and `serviceAccountName`. The credentials, the
  proxy configuration and the certificates are Secrets the object names,
  not values, so a policy that forbids explicit secrets has nothing of it
  to refuse.
- **No field of an ImageUpdateAutomation holds a secret or a host.** The
  address of the repository and its credentials are the GitRepository's,
  and the signing key is a Secret named by `git.commit.signingKey`, not a
  value. `git.commit.messageTemplateValues` and `git.push.options` are
  free maps, written to the object as authored under a policy that
  forbids explicit secrets too. **The images the automation writes into
  the repository are not held to the allowed registries or the tag
  rule:** they are what the ImagePolicies select at run time, and no
  build sees them.
- **No field of an ArtifactGenerator holds a secret or a host.** The
  addresses and the credentials are those of the sources it names.
  `commonMetadata` holds two free maps, written to the object as authored
  under a policy that forbids explicit secrets too, and held to the
  reserved metadata keys alone at generation. **What an artifact
  carries is not checked:** the copy is the controller's to perform, and no
  build sees the files, so the rules a policy holds a workload or a Secret to
  do not reach manifests that travel inside an artifact.
- **A user or a password in a ResourceSetInputProvider's `url` is
  refused**, under every policy and under none, by the same rule
  (`resourcesetinputprovider: url must not carry a user or password, which
  would be written in plain text into the ResourceSetInputProvider; …`):
  the credentials belong in the Secret `secretRef` names. A `url` that is
  no URL with a host is refused too when it holds an `@`, as a Provider's
  `address` is. **The host of
  `url` is held to the allowed registries, whatever the type:** the answer
  of that host decides what a ResourceSet deploys, the `ExternalService`
  call sends the referenced credential to it, and one rule holds a type a
  later operator version adds. An `oci://` url is held by the rule that
  holds an `ocirepository`'s: under a policy that lists registries it must
  name its registry explicitly and a repository after it. Every other url
  is held by the host rule of the Flux sources: under a policy that allows
  `registry.example`, `url: https://git.other.example/shop/fleet` is
  refused, a refusal of the class `registry`. A `Static` or
  `ExternalArtifact` provider has no `url` and nothing is held. A policy
  that lists no registry refuses none, and neither does a build without a
  policy. **Not held:** `serviceAccountName`, `insecure` and the
  selectors of an `ExternalArtifact` provider, which name no host; the
  inputs the provider returns, which the operator reads at run time and
  no build sees. `defaultValues` is a free map, written to the object as
  authored under a policy that forbids explicit secrets too.

**Not covered.** Whether what is referred to exists (the Provider, the
objects of a source, the ImageRepository, the GitRepository, the signing
key's Secret, the sources and the ServiceAccount of an ArtifactGenerator
and the namespaces its artifacts name, the Secrets and the
ServiceAccount of a Provider and of an ImageRepository, the resources and
the Secret of a Receiver, the Secrets, the ServiceAccount and the
ExternalArtifacts of a ResourceSetInputProvider), whether the registry of
an ImageRepository answers and holds the repository, whether the address
of a Provider, the issuer of a Receiver or the `url` of a
ResourceSetInputProvider answers, whether a `commitStatusExpr` or a
Receiver's CEL expression compiles,
whether a `filterTags`
pattern, a `semver` range, a commit message template, a `pathPattern`, a
ResourceSetInputProvider's filter expression or a schedule's `cron`
parses, and whether the cluster serves the API: the
component builds where the CRD is not installed, and the object is refused
at apply. The object's status is the controller's and is not written.

### Example

- `fluxcd-alert`: The component in `pkg/cmd/kurel/testdata/fluxcd-alert-component/app.yaml`,
  built against the `cluster.yaml` beside it, gives the `expected.yaml` beside it.
  The fixture test compares the two.
- `fluxcd-provider`: The component in `pkg/cmd/kurel/testdata/fluxcd-provider-component/app.yaml`,
  built against the `cluster.yaml` beside it, gives the `expected.yaml` beside it.
  The fixture test compares the two.
- `fluxcd-receiver`: The component in `pkg/cmd/kurel/testdata/fluxcd-receiver-component/app.yaml`,
  built against the `cluster.yaml` beside it, gives the `expected.yaml` beside it.
  The fixture test compares the two.
- `imagepolicy`: The component in `pkg/cmd/kurel/testdata/imagepolicy-component/app.yaml`,
  built against the `cluster.yaml` beside it, gives the `expected.yaml` beside it.
  The fixture test compares the two.
- `imagerepository`: The component in `pkg/cmd/kurel/testdata/imagerepository-component/app.yaml`,
  built against the `cluster.yaml` beside it, gives the `expected.yaml` beside it.
  The fixture test compares the two.
- `imageupdateautomation`: The component in `pkg/cmd/kurel/testdata/imageupdateautomation-component/app.yaml`,
  built against the `cluster.yaml` beside it, gives the `expected.yaml` beside it.
  The fixture test compares the two.
- `artifactgenerator`: The component in `pkg/cmd/kurel/testdata/artifactgenerator-component/app.yaml`,
  built against the `cluster.yaml` beside it, gives the `expected.yaml` beside it.
  The fixture test compares the two.
- `resourcesetinputprovider`: The component in `pkg/cmd/kurel/testdata/resourcesetinputprovider-component/app.yaml`,
  built against the `cluster.yaml` beside it, gives the `expected.yaml` beside it.
  The fixture test compares the two.

## fluxcd-kustomization

`fluxcd-kustomization` is the kind-named terminal for Flux's `Kustomization`. The type is
`fluxcd-kustomization` rather than `kustomization`, which would read as the
`kustomization.yaml` file.

### Properties

Its properties are exactly the top-level JSON keys of
`KustomizationSpec` in the kustomize-controller API version `go.mod` links. It is an authored Flux
object, like `helmrelease`: it does not deliver the application it is part
of, and it creates no source — `sourceRef` names one that exists, authored
beside it (`ocirepository`, `gitrepository`, `bucket`) or already on the
cluster.

**Decoding.** The whole property map is decoded with `builtin.DecodeStrictJSON`
into `KustomizationSpec`, so `patches`, `postBuild`, `dependsOn`, `force`,
`timeout`, `serviceAccountName`, `decryption`, `kubeConfig` and every other
field are emitted as authored. A key the struct does not declare, at any
depth, is refused with its path from the property root
(`unknown field "patches[0].target.kinds"`,
`builtin.UnknownJSONFieldPath`): the spec declares `kind`, `name` and
`namespace` in several places, so the bare key would not say where. A
wrongly typed value is refused too. Keys match case-insensitively, as in
`encoding/json`; schema validation, which a `kurel build` runs first, is
exact.

**Defaults and checks.** `interval` defaults to `60m` when unset (a zero
duration counts as unset); Flux requires the field. `interval`,
`retryInterval` and `timeout`, when set, must be durations Flux accepts,
checked as on `helmrelease`. `sourceRef.kind` is required and one of
`OCIRepository`, `GitRepository`, `Bucket`, `ExternalArtifact`, the CRD's
enum; `sourceRef.name` is required. `prune` is a required field of the
spec with no default here: unset, `false` is emitted (`oci` passes `true`
unless told otherwise). `targetNamespace` is never defaulted, with or
without a Flux namespace, for the reason given under **oci**. **Every other
required field the type writes unauthored is refused too**, where it is left out under a parent that is
authored: the `name` of a `dependsOn` entry, of a `healthChecks` entry
(with its `kind`), of a `postBuild.substituteFrom` entry (with its `kind`),
of an image override and of the `decryption` and `kubeConfig` references;
`decryption.provider`; a patch's `patch`; an ignore rule's `paths`; a
health check expression's `apiVersion` and `current`
(`dependsOn[0].name: required (the Flux API requires the name of what
this refers to)`). The list is generated from the markers of the linked
kustomize-controller module (see ["Required fields a kind writes
unauthored and does not refuse"](conventions.md#required-fields-a-kind-writes-unauthored-and-does-not-refuse)) and runs after the checks above, so
`sourceRef` keeps its words.

### Outputs

**Identity and namespaces.** The Kustomization is named after the component
and lands in the Flux namespace when one is configured, else in the
application namespace (`SetFluxNamespace`). The ConfigMaps and Secrets it
reads from its own namespace — `decryption.secretRef`,
`kubeConfig.secretRef` / `configMapRef` and each `postBuild.substituteFrom`
entry — must live there too: a `configmap`, `external-secret` or
`certificate` trait on the component whose object one of them names moves
with the Kustomization (`FluxNamespaceReads`).
*Required reads*: the Kustomization cannot become Ready without its
`decryption.secretRef` and `kubeConfig`, none when suspended. Not its
`postBuild.substituteFrom`: kustomize-controller loads it only for a
rendered resource that does not disable substitution, which only the
artifact says. Under a delivery whose groups wait for their Flux objects, a
trait on a component of a later group that makes a required one is refused.

### Validation

Nothing else is checked: the enums and the cross-field rules are left to the
Kustomization CRD's own admission, and `wait: true` beside `healthChecks`,
which `oci` refuses, is emitted as authored (kustomize-controller then
ignores the list). Whether the source exists is known only on the cluster. The checks run again in `Generate`,
for a config built directly.

`ApplyPolicy` checks nothing: a Kustomization fetches nothing itself, and
the registry allowlist applies where its source is authored.

### Example

No example yet.

## helmrepository, ocirepository, gitrepository, bucket, helmchart

`helmrepository`, `ocirepository`, `gitrepository`, `bucket`, `helmchart` are the kind-named
terminals for Flux's four fetching source kinds and for its `HelmChart`, which builds a chart
artifact from one of them.

### Properties

Each component's properties are exactly the top-level JSON
keys of its source-controller spec type — `HelmRepositorySpec`, `OCIRepositorySpec`,
`GitRepositorySpec`, `BucketSpec`, `HelmChartSpec`, in the version `go.mod` links.

**Decoding.** The whole property map is decoded with `builtin.DecodeStrictJSON` into the spec
type, with no launcher-owned keys. A key the struct does not declare, at any depth
(`secretRef.namespace`, a stray key in a GitRepository `include` entry), is refused by name, and
so is a wrongly typed value (`suspend: "yes"`, an unparsable duration). The schema keeps the
nested Flux blocks as open objects; the strict decode is what checks them. Keys match case-insensitively, as in `encoding/json`; schema
validation, which a `kurel build` runs first, is exact.

**Checks and defaults.** Required: `url` on `helmrepository`, `ocirepository` and
`gitrepository`; `bucketName` and `endpoint` on `bucket`; `chart`, `sourceRef.kind` and
`sourceRef.name` on `helmchart`. `interval` defaults to `60m` when unset (a zero duration counts as unset), matching the
`helmrelease` default; a `helmrepository` with `type: oci` gets no default, since Flux
does not poll it, and its emitted `interval` reads `0s`, the value the Go type always writes.
A set `interval` must be a duration Flux accepts, checked exactly as on `helmrelease`; under `type: oci` too, where the CRD pattern still applies.
A set `timeout` (a `helmchart` has none) is checked the same way, against the source CRDs'
narrower pattern, which has
no `h` unit: `^([0-9]+(\.[0-9]+)?(ms|s|m))+$`, so an authored `1h` is
refused. A `timeout` of an hour or more authored in minutes or seconds (`90m`, `3600s`) is
accepted and emitted with its hours folded into minutes: `90m0s`, `60m0s`. A `metav1.Duration` would write `Duration.String()`, `1h30m0s`, which
the CRD rejects, so such a source is emitted as an unstructured object carrying that text; a
`timeout` below an hour, or none, leaves the source emitted exactly as before. A config built
directly gets the same minutes form.
An authored `verify` that names no `provider`, on an `ocirepository` or a `helmchart`, is
written with `provider: cosign`, the API's default, and an authored `provider: ""` is refused, for the reason given on `helmrelease` above: the Go type
writes the field whether or not it was authored, so the API's default never applies. A
config built directly with an empty provider gets the default too.
`TestKindComponents_OmittedRequiredAndWrittenDefaults` holds the five source kinds, with
`helmrelease` and `fluxcd-kustomization`, to the required fields and the defaults the
markers of the linked Flux modules state, in the test's two sets only: a required field
the type leaves out when it is empty, and a default the type writes over unauthored. The
seven have four such fields: `chart.spec.sourceRef.kind` on `helmrelease`, refused, and
`verify.provider` on that kind's chart, on `ocirepository` and on `helmchart`, filled.
**Every other required field the type writes unauthored is refused too**, where it is left out under a parent that is authored: the `name` of
an authored `secretRef`, `certSecretRef` or `proxySecretRef`, and of the Secret references
under `verify` and `sts`; a `verify.matchOIDCIdentity` entry's `issuer` and `subject`; a
Bucket's `sts.endpoint` and `sts.provider`; a GitRepository `include` entry's `repository`
with its `name`; a HelmRepository's `accessFrom.namespaceSelectors`
(`verify.secretRef.name: required (the Flux API requires the name of what this refers to)`).
The list is generated from the markers of the linked source-controller module (see ["Required
fields a kind writes unauthored and does not refuse"](conventions.md#required-fields-a-kind-writes-unauthored-and-does-not-refuse)) and runs after the checks above, so a
field they require keeps its words.

### Outputs

Each emits exactly one CR of its kind, named after the component, and nothing else: no
Kustomization (compare `oci`), no HelmRelease (compare `helmrelease`), no source for a
`helmchart` (its `sourceRef` names one that exists, as a `helmrelease`'s
`chart.spec.sourceRef` does), and no source dedup — two components naming the same URL emit
two CRs. A `helmrelease` consumes a `helmchart` through `chartRef` with `kind: HelmChart`.

**Namespace and references.** The CR lands in the Flux namespace when one is configured, else in
the application namespace (`SetFluxNamespace`). Every local reference it carries — `secretRef`,
`certSecretRef`, `proxySecretRef`, the Secret references under `verify` and `sts`,
`serviceAccountName`, a GitRepository `include`, a HelmChart's `sourceRef` — resolves in the
namespace the CR lands in, so under a Flux namespace the objects it names must live there. A
`helmchart` whose `sourceRef` names a source component in the same document finds it there:
both move to the Flux namespace. So does the Secret of an `external-secret` or `certificate`
trait (or the ConfigMap of a `configmap` trait) on the component when one of the Secret
references names it — a `certSecretRef` naming a `certificate` trait's `secretName` moves that
Certificate
(`FluxNamespaceReads`); a trait object no reference names stays in the
application namespace. *Required reads*: a source cannot become Ready without the Secrets it
reads, but an `ocirepository`'s `verify.trustedRootSecretRef` only under the `cosign`
provider; a `helmrepository` of `type: oci`, a static object that never sets Ready, and a
suspended source (`suspend: true`) require none. Under a delivery whose groups wait for their
Flux objects, a trait on a component of a later group that makes a required one is refused.

**Generated by the `helm` rule.** An inline source on a `helm` component generates one of
these components: a `helmrepository` for an `http(s)://` URL; an `ocirepository` for an
`oci://` URL, with a `ref.tag` from `version`, a `ref.digest` from `source.digest` and a `layerSelector` copying the Helm chart
content layer; a `gitrepository` for `kind: GitRepository` with its URL and one `source.ref`
field; or a `bucket` for `kind: Bucket` with `endpoint` and `bucketName` and no URL. It is
named `<app>-source-<digest>` and shared by every `helm` component with the same source
identity (see **helm**). An authored source component exposes the whole spec
(credentials, `type: oci`, `provider`, verification, …) and is never shared. The rule never
generates a `helmchart`.

### Validation

A `url` must start with a scheme the
CRD's own pattern admits: `http://`, `https://` or `oci://` for `helmrepository`, and only
`oci://` under `type: oci`; `oci://` for `ocirepository`; `http://`, `https://` or `ssh://` for
`gitrepository`, so an scp-style `git@host:org/repo` is refused — write
`ssh://git@host/org/repo`. The CRD has no pattern for `endpoint`, so it is only required. A `url` must not carry a credential, since the source would hold it in
plain text (put the credential in a Secret named by `secretRef`). On `helmrepository`,
`ocirepository` and an `http://` or `https://` `gitrepository`, a user or password is refused,
and so is a value with no host that holds an `@`. On an `ssh://` `gitrepository` only a password
is refused, and a value with no host that holds an `@`. Its user is kept (`ssh://git@host/…`,
`ssh://alice@gerrit:29418/…`): it is the SSH login, which Flux's Git client reads from the URL
alone. Over SSH it reads no `username` from the Secret, and falls back to `git` when the URL
names none. The refusal does not quote the value. These are build refusals that documents built
before the change did not get. The `oci` component lowers onto an `ocirepository`, so its
`source.url` is refused the same way, in that kind's words (`ocirepository: url must not carry a
user or password…`). A config built directly is refused by `Generate`. Only its `ApplyPolicy`
still checks the host of such a url, without quoting the credential. Nothing else is checked or defaulted: enums (`type`, `provider`, `layerSelector.operation`,
`verify.mode`, a HelmChart's `sourceRef.kind` and `reconcileStrategy`) and cross-field rules
(a Bucket's `sts` against its `provider`, `serviceAccountName` against `secretRef`, a
HelmChart's `verify` only with a HelmRepository source) are left to the CRD's own admission.
Nor is the source a `helmchart` names: whether it exists, or is of the kind `sourceRef.kind`
says, is known only on the cluster.

**Policy.** `ApplyPolicy` checks the host the source is fetched from against the policy's
allowed registries (`AllowedRegistries`), with the same exact match `oci`, `crd` and
`manifests` apply: the `url` host for the three repositories, the `endpoint` host for `bucket`.
A port is part of the host, so `registry.local:5000` matches only an entry with that port. The
user of an `ssh://` URL is dropped (`ssh://git@github.com/org/repo` checks `github.com`);
userinfo on an `http://`, `https://` or `oci://` URL is not, so such a URL matches no entry and
is refused (a component reaches no policy with one: its parse refuses it first, see above). An empty allowlist permits every host, and none of the rules below applies. A
`helmchart` checks nothing: it fetches from the source its `sourceRef` names, whose host is
checked where that source is authored, as for a `helmrelease`.

No refusal prints the value it refused, since a URL or `endpoint` can carry a credential in
its userinfo (the user included), path, query or IPv6 zone. An
allowlist refusal names the host as `displayHost` reduces the url's raw authority (an
`ssh://` user included, not the host the match used): without userinfo, query, fragment or
zone, port kept, saying when something was dropped; the matching itself is unchanged. It
names no host at all when the reduced text is not a plain host (a DNS name, IPv4 address or
bracketed IPv6 address, with an optional numeric port), or when a `[`, `]`, `?` or `#`, none
of which a userinfo may hold, precedes the last `@`, which may then sit inside IPv6 brackets
or a query instead. A scheme refusal (the four sources', `oci`'s, and `crd`'s and
`manifests`' `http`/`https` check) and the explicit-registry refusal below name only the
component, the field and the form expected, and the Amazon S3 refusal names the reduced host.

Some sources are not fetched from the host their field names, so the check follows Flux instead:

- *An `oci://` URL must name its registry explicitly.* Flux parses an OCIRepository `url`, and a
  Helm OCI chart reference when it verifies the chart's signature, with go-containerregistry,
  which takes the first path segment as the registry only when a `/` follows it and it is
  `localhost` or contains `.` or `:`; anything else is a Docker Hub repository. So
  `oci://ghcr.io` and `oci://registry/app` are pulled from Docker Hub, whichever entry their
  first segment matched. Under a non-empty allowlist such a URL is refused, with an error
  asking for a qualified registry (`oci://docker.io/library/app`,
  `oci://registry.example:5000/org/app`). An `ocirepository` `url` also needs a repository
  path after the registry; a `helmrepository` `oci://` URL, of either `type`, may stop at the
  registry (`oci://ghcr.io`), since Flux appends the chart name to it.
- *A `bucket` with `provider: gcp` is checked against `storage.googleapis.com`.* Flux's GCP
  client never reads `endpoint`; it uses Google Cloud Storage's own host. The `generic`, `aws`
  and `azure` providers, and no provider, fetch from `endpoint`, which stays the host checked,
  except as the next rule says.
- *A `bucket` with an Amazon S3 `endpoint` is refused, unless its provider is `azure` or
  `gcp`.* The S3 client behind `generic`, `aws` and no provider does not fetch from an Amazon
  S3 endpoint: it contacts the S3 host of `region`, which may name any AWS region or partition,
  or of the bucket's own location when `region` is unset, chosen at runtime. That host cannot
  be checked, so under a non-empty allowlist the build fails, even when the allowlist lists the
  endpoint. The test errs broad, since it fails closed: any `endpoint` containing `amazonaws`,
  in any case, counts, because the client's own host patterns also match look-alikes such as
  `s3.x-amazonaws.com`.

Hosts a source reaches indirectly are not checked: the chart URLs a Helm repository index
advertises on other hosts (what `passCredentials` exists for), a Bucket's `sts.endpoint`, the
token endpoints a `provider` authenticates against, and a proxy. Apart from the two `bucket`
cases above, a `provider`, `region` or `insecure` setting changes how the source authenticates
or which scheme it uses, not which host it fetches from.

### Example

- `helmrepository`: The component in `pkg/cmd/kurel/testdata/flux-sources/app.yaml`,
  built against the `cluster.yaml` beside it, gives the `expected.yaml` beside it.
  The fixture test compares the two.
- `ocirepository`: The component in `pkg/cmd/kurel/testdata/flux-sources/app.yaml`,
  built against the `cluster.yaml` beside it, gives the `expected.yaml` beside it.
  The fixture test compares the two.
- `gitrepository`: The component in `pkg/cmd/kurel/testdata/flux-sources/app.yaml`,
  built against the `cluster.yaml` beside it, gives the `expected.yaml` beside it.
  The fixture test compares the two.
- `bucket`: The component in `pkg/cmd/kurel/testdata/flux-sources/app.yaml`,
  built against the `cluster.yaml` beside it, gives the `expected.yaml` beside it.
  The fixture test compares the two.
- `helmchart`: The component in `pkg/cmd/kurel/testdata/flux-sources/app.yaml`,
  built against the `cluster.yaml` beside it, gives the `expected.yaml` beside it.
  The fixture test compares the two.
