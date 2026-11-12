# OAM Built-in Component Handlers

[![Go Reference](https://pkg.go.dev/badge/github.com/go-kure/launcher/pkg/oam/builtin/components.svg)](https://pkg.go.dev/github.com/go-kure/launcher/pkg/oam/builtin/components)

Package `components` implements `oam.ComponentHandler` for the built-in component
types. Each handler parses a typed config from a component's `properties` and
produces the corresponding Kubernetes resources via kure's builders. Handlers are
registered with the transformer in `pkg/cmd/kurel` (`newBuiltinTransformer`), each
mapping a component `type` string to a handler implementing `CanHandle` +
`ToApplicationConfig`. Every built-in component handler also implements
`oam.PropertySchemaProvider` (`PropertySchema()`), declaring a constrained schema for its
user-facing properties so the downstream runtime can validate them before invocation. Nested
Kubernetes shapes are modeled field-by-field at full fidelity — closed objects, enums where the
API has them, and the same value validation real admission applies (ADR-036 L1: one PodSpec/
Container projection shared by every kind). Only genuine escape-hatch fields (`passthrough.object`,
`manifests`/`crd` inline content) and key→value maps whose keys are data (`nodeSelector`,
`resources.requests`/`limits`) stay open by design; the remaining open objects (`probes`,
`lifecycle`, `volumes`) are a known gap, not the target shape; the
`volumeMounts` and `volumeDevices` items inside an `initContainers`/`sidecars` entry
(go-kure/launcher#1172 closed the first), and a sidecar's `ports` items (go-kure/launcher#660), are
closed, and so is the four-key `affinity` shorthand: its schema declares the four keys and
no other, and since go-kure/launcher#790 its parser refuses any other key too. The
`initContainers`/`sidecars` entries themselves are closed (go-kure/launcher#321, see "Common
config"). The raw `corev1` `affinity` that `deployment` publishes is
a different schema and is not part of that gap — it is modeled field-by-field. `helmrelease`
takes the other route to the same strictness: its schema declares exactly the top-level
`HelmReleaseSpec` keys and leaves the nested Flux blocks open, and its handler decodes the
whole property map strictly into `HelmReleaseSpec`, refusing an unknown or wrongly typed key at
any depth (see its entry under "Per-type highlights"). Every property
(including nested object fields and array item
schemas at every depth) carries a `Description`, surfaced in the downstream runtime's generated Handler API
Reference.

The kind-named Flux source components (`helmrepository`, `ocirepository`, `gitrepository`,
`bucket`, `helmchart`) reach the same strictness another way: each schema declares exactly the top-level keys
of its source-controller spec type and leaves the nested Flux blocks open, and each handler
decodes the whole property map strictly into that type, refusing an unknown or wrongly typed key
at any depth (see their entry under "Per-type highlights"). `fluxcd-kustomization` does the same
with kustomize-controller's `KustomizationSpec`.

Every built-in component handler and component lowering rule implements
`oam.ContractDescriber` (go-kure/launcher#789): family is the component type, version is
`builtin.ContractVersion` (`v1alpha1`), no capability key is required and none is deprecated.
Only `postgresql` declares `Prerequisites`: what its `backup` and `recovery` need in the
cluster (see its entry under "Per-type highlights"). It is also the only one that declares
`Readiness` (go-kure/launcher#1144), one entry for each kind it lowers into, each signal read
from the status type of the CloudNativePG v1.30.1 or Barman Cloud plugin v0.15.1 module go.mod
pins (nothing checks it against a live resource). A consumer's naming hook can answer another
name for any kind:

| Kind | Named | Signal | Generic check |
|------|-------|--------|---------------|
| `postgresql.cnpg.io` `Cluster` | the component name, or `clusterObjectName` | condition `Ready` (`ClusterStatus.Conditions`) | judges it |
| `barmancloud.cnpg.io` `ObjectStore` | the component name, or `backup.objectStoreObjectName`; `<component>-recovery-source` for an inline `recovery.source.objectStore` | none: `ObjectStoreStatus` holds only a backup history | cannot judge |
| `postgresql.cnpg.io` `Pooler` | `<component>-pooler`, or `poolerName` | `status.phase` is `active` (`PoolerStatus.Phase`) | cannot judge |
| `postgresql.cnpg.io` `Database` | `<component>-<database>`, or `databases[].objectName` | `status.applied` is `true` (`DatabaseStatus.Applied`) | cannot judge |
| `postgresql.cnpg.io` `ScheduledBackup` | `<component>-<schedule>` | none: `ScheduledBackupStatus` holds schedule times and an error | cannot judge |

The four rules (`webservice`, `worker`, `helm`, `postgresql`) also implement
`oam.LoweringTargetDeclarer`, naming every type they lower into:

| Rule | Components | Traits | Policies |
|------|------------|--------|----------|
| `webservice` | `deployment`, `service`, `serviceaccount` | `topology-spread`, `pvc` | |
| `worker` | `deployment`, `serviceaccount` | `topology-spread`, `pvc` | |
| `helm` | `helmrelease`, `helmtemplate`, `helmrepository`, `ocirepository`, `gitrepository`, `bucket` | `configmap`, `secret` | |
| `postgresql` | `cnpg-cluster`, `cnpg-objectstore`, `cnpg-pooler`, `cnpg-database`, `cnpg-scheduledbackup` | | `dependency`, `placement` |

`Transformer.Seal`, which every transform runs first, refuses a registry that holds a rule
without all of its targets, whatever the document: `registry incomplete: lowering rule
component/webservice@v1alpha1 lowers into component type "service", which is not registered`.
**Breaking library change**: a consumer that registers one of these rules with only the
handlers its own documents reach (a `postgresql` without the pooler, say) now fails at the
first transform, and registers the rest. See Contract metadata in
[`pkg/oam`](https://pkg.go.dev/github.com/go-kure/launcher/pkg/oam).

## Traits a lowering rule forwards

A rule that emits several components decides which of them carries each authored trait,
and a trait acts where it is carried: on that member's objects, or on the bundle that
member's group is applied in. The rules of this package forward by what a trait covers
(go-kure/launcher#794, item 8):

- **A trait that covers every object the component generates** (`prune-protection`,
  `force-replace`: a delivery intent on the application of the member that carries it)
  goes to every member the rule emits for that component: each member of `webservice`
  and `worker`, the Kustomization and the own source of `oci`, the Cluster and every
  other member of `postgresql`. A source `helm` generates, and a source several `oci`
  components share, belongs to the document and carries no trait.
- **A trait that configures how a bundle is delivered** (`fluxcd-patches`,
  `fluxcd-postbuild`: not built in; a consumer that delivers through Flux registers
  them) goes to one member per bundle, never to two members of one group, so its
  handler sees it once per bundle. `webservice`, `worker` and an `oci` with its own
  source emit one same-name sibling group, which is one bundle: the `deployment` member
  carries the trait for the first two, the Kustomization for the third. `helm`, and an
  `oci` on a shared source, emit one component beside the source, and that component
  carries it.
- **Any other trait** goes to the one member whose objects or contracts it acts on; each
  rule's entry under "Per-type highlights" says which.

**A rule whose members can land in different groups forwards a bundle trait only where
it can tell the groups, and refuses the document where it cannot.** Groups are computed
after lowering has settled, so a rule cannot ask which bundle a member will be applied
in. Applying a forwarded trait once per resulting bundle, whatever the grouping turns out
to be, is not offered: it would have to happen in the engine, after grouping, and the
refusal was kept instead.

`postgresql` is the one built-in case. Its Cluster carries every authored trait. Unless
the document orders its components (a `dependency` policy with a rule), the other
members stay in the Cluster's bundle, which the Cluster's copy configures. When it
does, the Pooler, the Databases and the ScheduledBackups are ordered after the Cluster, wait for nothing else
and are placed where it is, so they share one later group, and the first of them carries
a second copy for that group's bundle. A policy of the document that orders or places
one of those members on its own (a `dependency` rule or a `placement` whose `component`
is a generated name) could move it to a group neither copy reaches, so a document that
authors one of the two traits on the `postgresql` component and holds such a policy is
refused, with this cause:

```text
trait "fluxcd-patches" configures the bundle of every object this component generates, but placement policy "pooler-last" names "db-pooler", a component generated for it, on its own: that component could be applied in a bundle the trait does not reach; name "db" in the policy instead, or remove the trait
```

A policy that makes another component wait for a member separates nothing and is left
alone. A rule an extension registers owes the same: one copy per bundle where the
members provably share a group, a refusal naming the trait and the policy where a
document could separate them.

## How to read the wrong-type notes below

This document states wrong-type handling **per field**, and makes no blanket
guarantee across the package. Where a field says a present-but-wrong-type value
is rejected, that is a claim about the helper named beside it —
`parseStringField`, `parseBoolField`, `parseInt32Field`, `parseInt64Field`,
`parseObjectField`, `parseStringList` — each of which reports presence
separately from value and returns an error naming the field. A parser that
does not use one of those helpers may still reject a wrong type on its own —
several do, with their own error — so its behavior is left unspecified here.
Some fields may still fall through a bare type assertion and silently
discard a wrongly typed value instead of erroring. The shared
`parseAffinity`'s four sub-fields (`enablePodAntiAffinity`, `topologyKey`,
`podAntiAffinityType`, `nodeSelector`) were the documented example until
go-kure/launcher#452 moved them onto the helpers; the absence of a named
example is not a claim that none remain. Do not generalize a rejection note
from one field to its neighbours: adjudicate against the parser that actually
reads it.

## Component types

| `type` | Produces | Summary |
|--------|----------|---------|
| `alertmanager` | Alertmanager | Kind-named Prometheus operator Alertmanager: the whole `AlertmanagerSpec`, strictly decoded; no top-level field is required. The operator runs the pods: what the spec says of them (`image`, `replicas`, `resources`, storage, `containers`, `initContainers`, `volumes`, `securityContext`, `hostNetwork`) is held to the environment policy as a workload's is, and the deprecated `baseImage`, `tag` and `sha` are not in the schema, and are refused when not empty (an empty one writes nothing where the properties are not first validated against the schema, which refuses all three). The replica count and memory request the operator fills where they are unset are held, not written. An image the operator chooses, for `alertmanager` or one of its two reloaders, is refused under a policy with allowed registries. No capability is required — see below. |
| `apiservice` | APIService | Kind-named APIService: the whole `APIServiceSpec`, strictly decoded; `group`, `version`, `groupPriorityMinimum` and `versionPriority` are required, and a `service` names its `namespace` and `name`. The object must be named `<version>.<group>`, by the component's name or `objectName`. It hands a group and version of the API to the Service it names, which is not resolved at build. Cluster-scoped; only the environment policy's object kind rules apply — see below. |
| `artifactgenerator` | ArtifactGenerator | Kind-named Flux ArtifactGenerator: the whole `ArtifactGeneratorSpec`, strictly decoded; `sources`, each with its `alias`, `kind` and `name`, and `artifacts`, each with its `name` and a `copy` of `from` and `to`, are required. A source may be one of another namespace, whose content the generator copies into its artifacts, and an artifact may be written into another namespace, checked only against the RBAC of the account `serviceAccountName` names; launcher gates neither. The API's expression rules are not checked, and `managedNamespaces` is refused. No environment policy applies — see below. |
| `backendtlspolicy` | BackendTLSPolicy | Kind-named Gateway API BackendTLSPolicy: the whole `BackendTLSPolicySpec`, strictly decoded; at least one of `targetRefs`, and `validation` with its `hostname`, are required. No capability is required and no environment policy applies — see below. |
| `bucket` | Bucket | Kind-named: the full Flux `BucketSpec`. |
| `certificate` | Certificate | Kind-named cert-manager Certificate: the whole `CertificateSpec`, strictly decoded; `secretName` and `issuerRef` with its `name` are required. A keystore password written into the object is refused under an environment policy that forbids explicit secrets. The issuer is the author's, and no capability is required. Under a delivery whose groups wait for their Flux objects, a Certificate in a later group than a Flux object requiring its `secretName` Secret is refused (go-kure/launcher#1086). Shares its name with the `certificate` trait — see below. |
| `cilium-bgpadvertisement` | CiliumBGPAdvertisement | Kind-named Cilium BGP advertisement: the whole `CiliumBGPAdvertisementSpec` (`advertisements`, required), strictly decoded; an entry's `advertisementType` is required, and its `service`, `interface` and `selector` are held to the type. Cluster-scoped; no environment policy applies and no capability is required — see below. |
| `cilium-bgpclusterconfig` | CiliumBGPClusterConfig | Kind-named Cilium BGP cluster configuration: the whole `CiliumBGPClusterConfigSpec` (`nodeSelector`, `bgpInstances`), strictly decoded; `bgpInstances`, an instance's `name` and a peer's `name` are required. The node selector is the author's. Cluster-scoped; no environment policy applies and no capability is required — see below. |
| `cilium-bgpnodeconfigoverride` | CiliumBGPNodeConfigOverride | Kind-named Cilium BGP per-node override: the whole `CiliumBGPNodeConfigOverrideSpec` (`bgpInstances`, required), strictly decoded; an instance's `name` and a peer's `name` are required. It overrides the CiliumBGPNodeConfig of the same name: `objectName` sets that name. Cluster-scoped; no environment policy applies and no capability is required — see below. |
| `cilium-bgppeerconfig` | CiliumBGPPeerConfig | Kind-named Cilium BGP peer configuration: the whole `CiliumBGPPeerConfigSpec` (`transport`, `timers`, `authSecretRef`, `gracefulRestart`, `ebgpMultihop`, `families`), strictly decoded; no top-level field is required. Cluster-scoped; no environment policy applies and no capability is required — see below. |
| `cilium-cidrgroup` | CiliumCIDRGroup | Kind-named Cilium CIDR group: the whole `CiliumCIDRGroupSpec` (`externalCIDRs`, required), strictly decoded. A Cilium network policy refers to the group by its name: `objectName` sets that name. Cluster-scoped; no environment policy applies and no capability is required — see below. |
| `cilium-clusterwidenetworkpolicy` | CiliumClusterwideNetworkPolicy | Kind-named Cilium cluster-wide network policy: `spec`, `specs` or both, each a Cilium rule, strictly decoded as the `cilium-networkpolicy` kind decodes them. A rule takes exactly one of `endpointSelector` and `nodeSelector`, and at least one of `ingress`, `ingressDeny`, `egress` and `egressDeny`. Cluster-scoped; no environment policy applies and no capability is required — see below. |
| `cilium-egressgatewaypolicy` | CiliumEgressGatewayPolicy | Kind-named Cilium egress gateway policy: the whole `CiliumEgressGatewayPolicySpec` (`selectors`, `destinationCIDRs`, `excludedCIDRs`, `egressGateway`, `egressGateways`), strictly decoded; `selectors`, `destinationCIDRs`, `egressGateway` and a gateway's `nodeSelector` are required. The selectors are the author's. Cluster-scoped; no environment policy applies and no capability is required — see below. |
| `cilium-loadbalancerippool` | CiliumLoadBalancerIPPool | Kind-named Cilium load balancer address pool: the whole `CiliumLoadBalancerIPPoolSpec` (`serviceSelector`, `allowFirstLastIPs`, `blocks`, `disabled`), strictly decoded; no top-level field is required. The Service selector is the author's. Cluster-scoped; no environment policy applies and no capability is required — see below. |
| `cilium-localredirectpolicy` | CiliumLocalRedirectPolicy | Kind-named Cilium local redirect policy: the whole `CiliumLocalRedirectPolicySpec` (`redirectFrontend`, `redirectBackend`, `skipRedirectFromBackend`, `description`), strictly decoded; the frontend, the backend, its selector and its ports are required, and the frontend takes exactly one of `addressMatcher` and `serviceMatcher`. Namespaced; no environment policy applies and no capability is required — see below. |
| `cilium-networkpolicy` | CiliumNetworkPolicy | Kind-named CiliumNetworkPolicy: its `spec` (one rule) and `specs` (a list of rules), each the whole Cilium rule, strictly decoded. At least one is required; each rule needs an `endpointSelector` and an entry in `ingress`, `ingressDeny`, `egress` or `egressDeny`, and `nodeSelector` is refused. An authored object, not the `cilium-networkpolicy` trait; no environment policy applies — see below. |
| `cilium-nodeconfig` | CiliumNodeConfig | Kind-named Cilium per-node configuration: the whole `CiliumNodeConfigSpec` (`defaults`, `nodeSelector`, both required), strictly decoded. The keys and values of `defaults` are written as authored. Namespaced; no environment policy applies and no capability is required — see below. |
| `clusterexternalsecret` | ClusterExternalSecret | Kind-named External Secrets Operator ClusterExternalSecret: the whole `ClusterExternalSecretSpec`, strictly decoded; `externalSecretSpec` is required, with what an `externalsecret` requires and what it refuses of a `target.manifest` under the environment policy. Cluster-scoped; no capability is required — see below. |
| `clusterissuer` | ClusterIssuer | Kind-named cert-manager ClusterIssuer: the `IssuerSpec` of `issuer`, strictly decoded, and its policy check. Cluster-scoped. No capability is required — see below. |
| `clusterrole` | ClusterRole | Kind-named RBAC ClusterRole: the object's own fields (`rules`, `aggregationRule`), strictly decoded; a rule's `verbs` are required, and either its `apiGroups` and `resources` or its `nonResourceURLs`; an `aggregationRule` needs a selector. Cluster-scoped. **Ungated: no capability and no environment-policy check restricts what a role grants** — see below. |
| `clusterrolebinding` | ClusterRoleBinding | Kind-named RBAC ClusterRoleBinding: the object's own fields (`subjects`; `roleRef`, required with its `kind` and `name`), strictly decoded; a subject's `kind` and `name` are required, and a ServiceAccount subject's `namespace`. Cluster-scoped. **Ungated: no capability and no environment-policy check restricts what a role grants** — see below. |
| `clustersecretstore` | ClusterSecretStore | Kind-named External Secrets Operator ClusterSecretStore: the `SecretStoreSpec` of `secretstore`, strictly decoded, and its policy check. Cluster-scoped. No capability is required — see below. |
| `cnpg-backup` | CNPG Backup | Kind-named CloudNativePG Backup: the whole `BackupSpec`, strictly decoded; `cluster.name` is required. A one-shot request with a spec the API never lets change: a backup that recurs is a `cnpg-scheduledbackup`. Namespaced; no environment policy applies and no capability is required — see below. |
| `cnpg-cluster` | CNPG Cluster | Operator-CR kind component: the whole `postgresql.cnpg.io/v1` `ClusterSpec`, strictly decoded, with no launcher opinions — see below. |
| `cnpg-clusterimagecatalog` | CNPG ClusterImageCatalog | Kind-named CloudNativePG cluster-wide image catalog: the same `ImageCatalogSpec`, strictly decoded, and the same policy check and tag rule. Cluster-scoped. No capability is required — see below. |
| `cnpg-database` | CNPG Database | Operator-CR kind component: the whole `DatabaseSpec`, strictly decoded — see below. |
| `cnpg-databaserole` | CNPG DatabaseRole | Kind-named CloudNativePG DatabaseRole: the whole `DatabaseRoleSpec`, strictly decoded; `cluster.name` and `name` are required, and the names the CRD reserves, `ensure: absent`, a password Secret beside `disablePassword: true` and a client certificate without `login: true` are refused. Its password is a Secret's name, never a value. Namespaced; no environment policy applies and no capability is required — see below. |
| `cnpg-imagecatalog` | CNPG ImageCatalog | Kind-named CloudNativePG image catalog: the whole `ImageCatalogSpec` (`images`, required, and `componentImages`), strictly decoded; an image's `image` and `major` and a component image's `key` and `image` are required. Every image the catalog names is held to the environment policy's allowed registries and, with or without a policy, to the tag rule (a tag or a digest, no `:latest`). Namespaced; no capability is required — see below. |
| `cnpg-objectstore` | Barman Cloud ObjectStore | Operator-CR kind component: the whole `barmancloud.cnpg.io/v1` `ObjectStoreSpec`, strictly decoded — see below. |
| `cnpg-pooler` | CNPG Pooler | Operator-CR kind component: the whole `PoolerSpec`, strictly decoded — see below. |
| `cnpg-publication` | CNPG Publication | Kind-named CloudNativePG Publication: the whole `PublicationSpec`, strictly decoded; `cluster.name`, `name`, `dbname` and `target` are required, and the target publishes all tables or a list of objects, not both. Namespaced; no environment policy applies and no capability is required — see below. |
| `cnpg-scheduledbackup` | CNPG ScheduledBackup | Kind-named CloudNativePG ScheduledBackup: the whole `ScheduledBackupSpec`, strictly decoded; `schedule` and `cluster.name` are required. The schedule is the operator's to read. Namespaced; no environment policy applies and no capability is required — see below. |
| `cnpg-subscription` | CNPG Subscription | Kind-named CloudNativePG Subscription: the whole `SubscriptionSpec`, strictly decoded; `cluster.name`, `name`, `dbname`, `publicationName` and `externalClusterName` are required. Namespaced; no environment policy applies and no capability is required — see below. |
| `configmap` | ConfigMap | Kind-named ConfigMap: `data`, `binaryData`, `immutable`. A workload reads it through a `configMap` volume or `envFrom` — see below. |
| `crd` | CustomResourceDefinition(s) | CRDs from `inline`/`url`; rejects non-CRD docs. |
| `cronjob` | CronJob | Scheduled job; cron `schedule` + history limits + CronJobSpec/JobSpec fields, plus the raw `affinity`/`tolerations`/`topologySpreadConstraints` (see below). |
| `csidriver` | CSIDriver | Kind-named CSIDriver: the whole `CSIDriverSpec`, strictly decoded. Cluster-scoped, and the object's name (the component's, or its `objectName`) is the driver's name; no environment policy applies — see below. |
| `daemonset` | DaemonSet | Per-node daemon; honors `tolerations`, the raw `affinity` and `topologySpreadConstraints`, and takes `sidecars`. Emits no Service (go-kure/launcher#690). Declares one endpoint per main-container port. |
| `deployment` | Deployment | Kind-named Deployment: the shared container and pod surface, the rest of `DeploymentSpec`, the main container's `ports`, and the raw `corev1` `affinity`/`tolerations`/`topologySpreadConstraints`. Declares one endpoint per main-container port. Not a superset of `worker` — see below. |
| `endpointslice` | EndpointSlice | Kind-named EndpointSlice: the object's own fields (`addressType`, required; `endpoints`; `ports`), strictly decoded; an endpoint's `addresses` are required. It belongs to a Service through the `kubernetes.io/service-name` label, authored under `labels` as a literal that does not follow a Service's `objectName`. Namespaced; no environment policy applies and no capability is required — see below. |
| `externalsecret` | ExternalSecret | Kind-named External Secrets Operator ExternalSecret: the whole `ExternalSecretSpec` (`secretStoreRef`, `target`, `refreshPolicy`, `refreshInterval`, `syncWindows`, `data`, `dataFrom`), strictly decoded; no top-level field is required. The store is the author's. The environment policy reaches one field: a `target.manifest` of a kind the policy checks is refused. No capability is required. Under a delivery whose groups wait for their Flux objects, an ExternalSecret in a later group than a Flux object requiring its target Secret is refused, unless `creationPolicy` is `Merge` or `None`, a `target.manifest` is set, or the document holds that Secret by the Flux object's group (go-kure/launcher#1086). Beside the `external-secret` trait — see below. |
| `fluxcd-alert` | Alert | Kind-named Flux Alert: the whole `AlertSpec`, strictly decoded; `providerRef` with its `name` and `eventSources`, each with its `kind` and `name`, are required. A source may name the objects of another namespace, and nothing gates it. No environment policy applies — see below. |
| `fluxcd-kustomization` | Kustomization | Kind-named: the full Flux `KustomizationSpec`, against an existing source; what `oci` lowers to beside an `ocirepository` (`OCIRule`, see below). An authored Flux object, not how an application is delivered. Under a delivery whose groups wait for their Flux objects, a component of a later group making the source its `sourceRef` names is refused (go-kure/launcher#1088). |
| `fluxcd-provider` | Provider | Kind-named Flux Provider: the whole `ProviderSpec`, strictly decoded; `type` is required, and of an authored Secret reference its `name`. A user or a password in `address` is refused under every policy and under none, and an `address` that is itself the credential under a policy that forbids explicit secrets; the host is written as authored, and nothing gates `serviceAccountName` — see below. |
| `fluxcd-receiver` | Receiver | Kind-named Flux Receiver: the whole `ReceiverSpec`, strictly decoded; `type` and `resources`, each with its `kind` and `name`, are required, and of an authored `secretRef` its `name`. A resource without `apiVersion` is refused unless its kind is one the notification controller gives a default version (`Bucket`, `GitRepository`, `HelmChart`, `HelmRepository`, `OCIRepository`, `ImageRepository`, `ArtifactGenerator`; go-kure/launcher#984), and the refusal names the kind's own apiVersion for a kind the API's `kind` enum admits (go-kure/launcher#1010). The Receiver opens an inbound path on the notification controller, and a resource may name the objects of another namespace; nothing gates either. The API's expression rules are not checked. No environment policy applies — see below. |
| `gateway` | Gateway | Kind-named Gateway API Gateway: the whole `GatewaySpec`, strictly decoded; `gatewayClassName` and `listeners` are required, and of a listener its `name`, `port` and `protocol`. It is not the Gateway a capability names for the `httproute` trait. No capability is required and no environment policy applies — see below. |
| `gatewayclass` | GatewayClass | Kind-named Gateway API GatewayClass: the whole `GatewayClassSpec` (`controllerName`, required, `parametersRef` and `description`), strictly decoded. Cluster-scoped. No capability is required and no environment policy applies — see below. |
| `gitrepository` | GitRepository | Kind-named: the full Flux `GitRepositorySpec`. |
| `grpcroute` | GRPCRoute | Kind-named GRPCRoute: the whole `GRPCRouteSpec` (`parentRefs`, `useDefaultGateways`, `hostnames`, `rules`), strictly decoded, on the `httproute` kind's recipe: nothing required or filled, no NetworkPolicy allow rule and no environment policy — see below. |
| `helm` | via `helmrelease` (+ a values `configmap` trait, a `secretValues` `secret` trait) + a generated `helmrepository`/`ocirepository`/`gitrepository`/`bucket`, or via `helmtemplate` | Role-named Helm component: Flux (`flux`) or client-side `template` delivery. Lowered to the kind-named terminals (`HelmRule`), sharing one generated source per content identity within a document. `endpoints` declares the pods the chart renders (see "Declared endpoints of a chart" below). See below. |
| `helmchart` | HelmChart | Kind-named: the full Flux `HelmChartSpec`, a chart from an existing source. Not the composite removed under this name (go-kure/launcher#350); that is `helm`. Under a delivery whose groups wait for their Flux objects, a component of a later group making the source its `sourceRef` names is refused (go-kure/launcher#1088). |
| `helmrelease` | HelmRelease | Kind-named: the full Flux `HelmReleaseSpec`, against an existing source, or an OCI chart helm-controller fetches itself (`chart.kind: OCIRepository`). Under a delivery whose groups wait for their Flux objects, a component of a later group making the source its `chartRef` or its chart template's `sourceRef` names is refused (go-kure/launcher#1088). Beside the Flux fields it takes launcher's own `endpoints` (the pods the chart renders), split off before the decode and never part of the emitted spec. |
| `helmrepository` | HelmRepository | Kind-named: the full Flux `HelmRepositorySpec`, and nothing else. |
| `helmtemplate` | rendered manifests | Kind-named client-side Helm render: `source.url`, `chart`, `version`, `values`, `secretValues`, `scopeOverrides`, and `endpoints` (the pods the chart renders). What `helm` lowers to under `delivery: template`, authorable directly. The source host and every rendered workload are checked against the environment policy — see below. The `volumeMounts` of every rendered workload are held to the `app.yaml` mount rules, whatever the policy (go-kure/launcher#1223). A container name used by more than one init or regular container of a rendered workload's pod is refused, whatever the policy, naming both entries (go-kure/launcher#1253). A `lifecycle` or probe on an init container of a rendered workload without `restartPolicy: Always` is refused the same way (go-kure/launcher#1237), as is a `lifecycle` sleep below zero or longer than the pod's `terminationGracePeriodSeconds` (30 when unset; go-kure/launcher#1236); a refused render is not kept (go-kure/launcher#1238). |
| `horizontalpodautoscaler` | HorizontalPodAutoscaler | Kind-named HorizontalPodAutoscaler: the whole `HorizontalPodAutoscalerSpec`, strictly decoded; `scaleTargetRef` and `maxReplicas` are required. `maxReplicas` is held to the environment policy's replica maximum, no default filled; the target is the author's and is not checked — see below. |
| `httproute` | HTTPRoute | Kind-named HTTPRoute: the whole `HTTPRouteSpec` (`parentRefs`, `useDefaultGateways`, `hostnames`, `rules`), strictly decoded. An authored object, not the `httproute` trait: no parent is synthesized from a capability, no NetworkPolicy allow rule is synthesized for it and no environment policy applies — see below. |
| `imagepolicy` | ImagePolicy | Kind-named Flux ImagePolicy: the whole `ImagePolicySpec`, strictly decoded; `imageRepositoryRef` with its `name` and `policy` are required, and of a `semver` policy its `range`. The policy may name the ImageRepository of another namespace, and nothing gates it. The API's expression rules are not checked. No environment policy applies — see below. |
| `imagerepository` | ImageRepository | Kind-named Flux ImageRepository: the whole `ImageRepositorySpec`, strictly decoded; `image` and `interval` are required, and of an authored Secret reference its `name`. The registry of `image` is held to the environment policy's allowed registries; the tag rule is not applied, and nothing gates `accessFrom`, `serviceAccountName` or `insecure` — see below. |
| `imageupdateautomation` | ImageUpdateAutomation | Kind-named Flux ImageUpdateAutomation: the whole `ImageUpdateAutomationSpec`, strictly decoded; `sourceRef` with its `kind` and `name` and `interval` are required, and of an authored `git` its `commit` with the author's `email`. The automation commits and pushes to the repository of the GitRepository it names, which may be one of another namespace, and nothing gates it. Under a delivery whose groups wait for their Flux objects, a component of a later group making the GitRepository is refused (go-kure/launcher#1088). No environment policy applies — see below. |
| `ingress` | Ingress | Kind-named Ingress: the whole `IngressSpec` (`ingressClassName`, `defaultBackend`, `tls`, `rules`), strictly decoded. An authored object, not the `ingress` trait: no NetworkPolicy allow rule is synthesized for it and no environment policy applies — see below. |
| `ingressclass` | IngressClass | Kind-named IngressClass: the whole `IngressClassSpec` (`controller`, required, and `parameters`), strictly decoded. Cluster-scoped; no environment policy applies — see below. |
| `issuer` | Issuer | Kind-named cert-manager Issuer: the whole `IssuerSpec` (`acme`, `ca`, `vault`, `selfSigned`, `venafi`), strictly decoded; no top-level field is required. The cpu and memory of an ACME HTTP01 solver's pod template are held to the environment policy's maxima. No capability is required — see below. |
| `job` | Job | Run-to-completion workload; the same JobSpec fields as `cronjob`'s job template, plus its own `suspend` and the raw `affinity`/`tolerations`/`topologySpreadConstraints` (see below). |
| `limitrange` | LimitRange | Kind-named LimitRange: the whole `LimitRangeSpec` (`limits`, required), strictly decoded — see below. |
| `listenerset` | ListenerSet | Kind-named Gateway API ListenerSet: the whole `ListenerSetSpec`, strictly decoded; `parentRef` with its `name` and at least one of `listeners`, each with its `name`, `port` and `protocol`, are required. No capability is required and no environment policy applies — see below. |
| `manifests` | any | Raw manifests from `inline`/`url` with namespace stamping + `scopeOverrides`. Every object is checked against the environment policy — see below. The `volumeMounts` of every container and init container of a Pod, PodTemplate, ReplicationController, Deployment, StatefulSet, DaemonSet, ReplicaSet, Job or CronJob are held to the `app.yaml` mount rules, whatever the policy (go-kure/launcher#1212). A `lifecycle` or probe on an init container of those objects without `restartPolicy: Always` is refused the same way (go-kure/launcher#1237), as is a `lifecycle` sleep below zero or longer than the pod's `terminationGracePeriodSeconds` (30 when unset; go-kure/launcher#1236). A container name used by more than one init or regular container of one of those objects is refused the same way, naming both entries (go-kure/launcher#1253). An object that fails several of these checks is refused by the container-name check first, then the mount rules, then the pod rules (the order the typed pod reader applies them in, and a chart render and a `passthrough` component the same), and the first object that fails any check is the one refused (go-kure/launcher#1297). |
| `metallb-bfdprofile` | BFDProfile | Kind-named MetalLB BFD profile: the whole `BFDProfileSpec` (`receiveInterval`, `transmitInterval`, `echoInterval`, `detectMultiplier`, `echoMode`, `passiveMode` and `minimumTtl`, none required), strictly decoded. It is the timers of the BFD session of the BGP peers that name it, and so how fast the loss of such a peer is noticed. Namespaced; no environment policy applies and no capability is required — see below. |
| `metallb-bgpadvertisement` | BGPAdvertisement | Kind-named MetalLB advertisement over BGP: the whole `BGPAdvertisementSpec` (`ipAddressPools`, `ipAddressPoolSelectors`, `peers`, `nodeSelectors`, `serviceSelectors`, `aggregationLength`, `aggregationLengthV6`, `localPref`, `asPathPrepend` and `communities`, none required), strictly decoded. It says which pools' addresses MetalLB announces to which BGP peers, for which Services, and with which route attributes; one that authors nothing limits none of them. `serviceSelectors` is refused beside an aggregation length other than 32 (IPv4) or 128 (IPv6), the CRD's expression rule. Namespaced; no environment policy applies and no capability is required — see below. |
| `metallb-bgppeer` | BGPPeer | Kind-named MetalLB BGP peer, at `metallb.io/v1beta2`: the whole `BGPPeerSpec` (`myASN`, required; the peer's AS number, its address or interface, its port and timers, `nodeSelectors`, `password`, `passwordSecret`, `bfdProfile` and the switches of the session), strictly decoded. It is a router the cluster's nodes hold a BGP session with, and so one MetalLB announces addresses to. A `connectTime` outside 1 to 65535 seconds, or not a whole number of seconds read in whole milliseconds, is refused, the CRD's two expression rules on a create, and so is a `peerPort: 0`. A `password` written into the object is refused under an environment policy that forbids explicit secrets. Namespaced; no capability is required — see below. |
| `metallb-community` | Community | Kind-named MetalLB Community: the whole `CommunitySpec` (`communities`, a list of aliases of a `name` and a `value`, none required), strictly decoded. It gives names to BGP community values; a BGP advertisement that names one attaches its value to what it announces. Namespaced; no environment policy applies and no capability is required — see below. |
| `metallb-ipaddresspool` | IPAddressPool | Kind-named MetalLB address pool: the whole `IPAddressPoolSpec` (`addresses`, required, `autoAssign`, `avoidBuggyIPs` and `serviceAllocation`), strictly decoded. It says which Services, in which namespaces, MetalLB gives an address of which range. Namespaced; no environment policy applies and no capability is required — see below. |
| `metallb-l2advertisement` | L2Advertisement | Kind-named MetalLB advertisement on the local network: the whole `L2AdvertisementSpec` (`ipAddressPools`, `ipAddressPoolSelectors`, `nodeSelectors`, `preferredNodeSelectors`, `interfaces` and `serviceSelectors`, none required), strictly decoded. It says which pools' addresses MetalLB announces on the local network, from which nodes and interfaces, for which Services; one that authors nothing limits none of them. Namespaced; no environment policy applies and no capability is required — see below. |
| `mutatingadmissionpolicy` | MutatingAdmissionPolicy | Kind-named MutatingAdmissionPolicy: its `MutatingAdmissionPolicySpec`, strictly decoded; `matchConstraints` with at least one rule, at least one of `mutations`, each with its `patchType` and the patch of that type, and `reinvocationPolicy` are required. It changes objects of any namespace after the build. Served from Kubernetes 1.36. Cluster-scoped; only the environment policy's object kind rules apply — see below. |
| `mutatingadmissionpolicybinding` | MutatingAdmissionPolicyBinding | Kind-named MutatingAdmissionPolicyBinding: its `MutatingAdmissionPolicyBindingSpec`, strictly decoded; `policyName` is required, and a `paramRef` holds exactly one of `name` and `selector` and its `parameterNotFoundAction`. Neither the policy nor the parameter is resolved at build. Served from Kubernetes 1.36. Cluster-scoped; only the environment policy's object kind rules apply — see below. |
| `mutatingwebhookconfiguration` | MutatingWebhookConfiguration | Kind-named MutatingWebhookConfiguration: its `webhooks`, strictly decoded; a webhook's `name`, `clientConfig` (exactly one of `url` and `service`), `sideEffects` and `admissionReviewVersions` are required, and a rule names its operations, API groups, versions and resources. It changes objects of any namespace after the build. Cluster-scoped; only the environment policy's object kind rules apply — see below. |
| `namespace` | Namespace | Kind-named Namespace: the whole `NamespaceSpec` (`finalizers`), strictly decoded. Cluster-scoped, named after the component; its labels are the `labels` property — see below. |
| `networkpolicy` | NetworkPolicy | Kind-named NetworkPolicy: the whole `NetworkPolicySpec` (`podSelector`, `ingress`, `egress`, `policyTypes`), strictly decoded. An authored object, not the `networkpolicy` trait: nothing scopes it to a component's pods, so an unwritten `podSelector` selects every pod of the namespace; no `policyTypes` are derived; a defective match expression of a selector is refused; no environment policy applies — see below. |
| `oci` | OCIRepository, Kustomization | Sync manifests from an OCI artifact (Flux). |
| `ocirepository` | OCIRepository | Kind-named: the full Flux `OCIRepositorySpec`, with no Kustomization (compare `oci`). |
| `passthrough` | any (verbatim) | Emit **one** arbitrary object as-declared; a built-in kind has the scope the Kubernetes API gives it, any other kind the scope an authored `clusterScoped` states, or else the one registered for it (namespaced when unknown); a list is rejected, a workload, claim or autoscaler is held to the environment policy, and a Secret is refused under a policy that forbids explicit secrets. The `volumeMounts` of a workload are held to the `app.yaml` mount rules, whatever the policy (go-kure/launcher#1223), and a container name used by more than one init or regular container of its pod is refused, naming both entries (go-kure/launcher#1253). A `lifecycle` or probe on an init container of a workload without `restartPolicy: Always` is refused the same way (go-kure/launcher#1237), as is a `lifecycle` sleep below zero or longer than the pod's `terminationGracePeriodSeconds` (30 when unset; go-kure/launcher#1236; go-kure/launcher#1238). |
| `persistentvolume` | PersistentVolume | Kind-named PersistentVolume: the whole `PersistentVolumeSpec`, its volume sources included, strictly decoded. Cluster-scoped. A `hostPath` or `local` source and `capacity.storage` are held to environment policy — see below. |
| `persistentvolumeclaim` | PersistentVolumeClaim | Kind-named claim: `size`, `storageClassName`, `accessModes`, `volumeMode`, `selector`, `dataSourceRef`, `volumeName`, `volumeAttributesClassName`. A workload mounts it with a `pvc` volume's `claimName` — see below. |
| `pod` | Pod | Kind-named bare Pod: the whole `PodSpec` less `ephemeralContainers`, `priority` and `overhead`, strictly decoded. Held to environment policy as a rendered Pod is; no default filled. The `volumeMounts` of every container and init container are held to the `app.yaml` mount rules (go-kure/launcher#1212). A `lifecycle` or probe on an init container without `restartPolicy: Always` is refused (go-kure/launcher#1237), and so is a `lifecycle` sleep below zero or longer than `terminationGracePeriodSeconds` (30 when unset; go-kure/launcher#1236). A container name used by more than one init or regular container is refused, naming both entries (go-kure/launcher#1253). Carries the `app` label, so traits and Services select it — see below. |
| `poddisruptionbudget` | PodDisruptionBudget | Kind-named PodDisruptionBudget: the whole `PodDisruptionBudgetSpec` (`minAvailable`, `maxUnavailable`, `selector`, `unhealthyPodEvictionPolicy`), strictly decoded; no top-level field is required and the `selector` is the author's, a defective match expression of it refused. No environment policy applies — see below. |
| `podmonitor` | PodMonitor | Kind-named Prometheus operator PodMonitor: the whole `PodMonitorSpec`, strictly decoded; `selector` is required. The selector is the author's. No environment policy applies and no capability is required — see below. |
| `podtemplate` | PodTemplate | Kind-named PodTemplate: its one field, `template`, strictly decoded into `PodTemplateSpec`. The pod spec is held to what the `pod` kind holds its own to and to environment policy; `activeDeadlineSeconds` is allowed, no default filled. A container name used by more than one init or regular container of the template is refused, naming both entries (go-kure/launcher#1253). Stored, not run: no `app` label and not a trait target — see below. |
| `postgresql` | CNPG Cluster, Pooler, ObjectStore, Database, ScheduledBackup | CloudNativePG database (backup/monitoring/pooling). |
| `priorityclass` | PriorityClass | Kind-named PriorityClass: `value` (`0` when unauthored), `globalDefault`, `description`, `preemptionPolicy`, strictly decoded. Cluster-scoped; no environment policy applies — see below. |
| `prometheus` | Prometheus | Kind-named Prometheus operator Prometheus: the whole `PrometheusSpec`, strictly decoded, every field under its upstream name, `externalLabels` included; no top-level field is required. The operator runs the pods: `image` and `thanos.image`, `replicas` times `shards`, `resources` and `thanos.resources`, storage, `containers`, `initContainers`, `volumes`, `securityContext` and `hostNetwork` are held to the environment policy as the Alertmanager's are, the deprecated `baseImage`, `tag` and `sha` are refused (the spec's when not empty, an empty one writing nothing; the sidecar's whenever set, the empty string included), and a literal `bearerToken` of `remoteWrite[]`, `remoteRead[]` or `apiserverConfig` is refused under a policy that forbids explicit secrets. The pod count the operator runs, an unset replica or shard count read as 1, is held, not written. An image the operator chooses, for `prometheus`, its two reloaders or, where `thanos` is set, `thanos-sidecar`, is refused under a policy with allowed registries. No capability is required — see below. |
| `prometheus-probe` | Probe | Kind-named Prometheus operator Probe: the whole `ProbeSpec`, strictly decoded; `prober.url` is required. The prober and the targets are the author's. No environment policy applies and no capability is required — see below. |
| `prometheusrule` | PrometheusRule | Kind-named Prometheus operator PrometheusRule: the whole `PrometheusRuleSpec` (`groups`), strictly decoded; a group's `name` and a rule's `expr` are required. No environment policy applies and no capability is required — see below. |
| `referencegrant` | ReferenceGrant | Kind-named Gateway API ReferenceGrant: the whole `ReferenceGrantSpec`, strictly decoded; `from` and `to` are required, with the group, kind and namespace of a source and the group and kind of a target. No capability is required and no environment policy applies — see below. |
| `replicaset` | ReplicaSet | Kind-named bare ReplicaSet: the whole `ReplicaSetSpec`, strictly decoded; `selector` and `template` are required. The pod template is held to what the `pod` kind holds its spec to, less `activeDeadlineSeconds`, a container name used by more than one init or regular container of the template is refused (go-kure/launcher#1253), and it gains the `app` label; `replicas` and the template are held to environment policy, no default filled — see below. |
| `replicationcontroller` | ReplicationController | Kind-named bare ReplicationController: the whole `ReplicationControllerSpec`, strictly decoded; `template` is required, `selector` (a plain label map) optional. The pod template and `replicas` are held as the `replicaset` kind's are, `activeDeadlineSeconds` refused included and a repeated container name too (go-kure/launcher#1253), and the template gains the `app` label — see below. |
| `replicationdestination` | ReplicationDestination | Kind-named VolSync ReplicationDestination: the whole `ReplicationDestinationSpec` (`trigger`, the movers `rsync`, `rsyncTLS`, `rclone` and `restic`, `external`, `paused`), strictly decoded, with the policy checks of `replicationsource`. No top-level field is required and no capability is required — see below. |
| `replicationsource` | ReplicationSource | Kind-named VolSync ReplicationSource: the whole `ReplicationSourceSpec` (`sourcePVC`, `trigger`, the movers `rsync`, `rsyncTLS`, `rclone`, `restic` and `syncthing`, `external`, `paused`), strictly decoded; no top-level field is required. An authored capacity is held to the environment policy's storage maximum, a mover's cpu and memory to its maxima, and a mover's `hostProcess` switch is refused unless privileged workloads are allowed. An `rsync` mover is held to the policy's container capabilities for the seven the linked operator version adds to its container. No capability is required. Its object is of the kind the `volsync` trait builds — see below. |
| `resourcequota` | ResourceQuota | Kind-named ResourceQuota: the whole `ResourceQuotaSpec` (`hard`, `scopes`, `scopeSelector`), strictly decoded — see below. |
| `resourcesetinputprovider` | ResourceSetInputProvider | Kind-named Flux Operator ResourceSetInputProvider: the whole `ResourceSetInputProviderSpec`, strictly decoded; `type` is required, of an authored Secret reference its `name` and of a schedule its `cron`. A user or a password in `url` is refused under every policy and under none, and the host of `url` is held to the environment policy's allowed registries, whatever the type. An `ExternalArtifact` provider's selectors may list the ExternalArtifacts of another namespace or of every namespace, and nothing gates them or `serviceAccountName`. The API's expression rules are not checked — see below. |
| `role` | Role | Kind-named RBAC Role: the object's own field (`rules`), strictly decoded; a rule's `verbs`, `apiGroups` and `resources` are required, and `nonResourceURLs` are refused. Namespaced. **Ungated: no capability and no environment-policy check restricts what a role grants** — see below. |
| `rolebinding` | RoleBinding | Kind-named RBAC RoleBinding: the object's own fields (`subjects`; `roleRef`, required with its `kind` and `name`), strictly decoded; a subject's `kind` and `name` are required. The names of the role and of the subjects are the author's literals. Namespaced. **Ungated: no capability and no environment-policy check restricts what a role grants** — see below. |
| `runtimeclass` | RuntimeClass | Kind-named RuntimeClass: `handler` (required), `overhead`, `scheduling`, strictly decoded. Cluster-scoped; no environment policy applies — see below. |
| `secret` | Secret | Kind-named Secret: `stringData`, `data`, `type`, `immutable`. Every entry is emitted under `data`, base64-encoded and not encrypted. Refused under an environment policy that forbids explicit secrets. A workload reads it through a `secret` volume, `envFrom` or a `secretKeyRef` — see below. |
| `secretstore` | SecretStore | Kind-named External Secrets Operator SecretStore: the whole `SecretStoreSpec` (`provider`, `controller`, `retrySettings`, `refreshInterval`, `conditions`), strictly decoded; `provider` with exactly one provider is required, and of that provider what the API requires. A credential written into the object is refused under an environment policy that forbids explicit secrets. No capability is required — see below. |
| `service` | Service | Kind-named Service in front of pods another component owns: `selector`, the full `ports` list (with `nodePort` and `appProtocol`), all four `type` values, `clusterIP: None` for a headless one, `externalName`, the traffic policies, session affinity, the IP families and the load-balancer fields. Emits nothing else — see below. |
| `serviceaccount` | ServiceAccount | Kind-named ServiceAccount: `automountServiceAccountToken`, `imagePullSecrets`. A workload names it with `serviceAccountName` — see below. |
| `servicecidr` | ServiceCIDR | Kind-named ServiceCIDR: the whole `ServiceCIDRSpec` (`cidrs`, required, at least one), strictly decoded. Cluster-scoped; no environment policy applies — see below. |
| `servicemonitor` | ServiceMonitor | Kind-named Prometheus operator ServiceMonitor: the whole `ServiceMonitorSpec`, strictly decoded; `endpoints` and `selector` are required. The selector is the author's. No environment policy applies and no capability is required — see below. |
| `statefulset` | StatefulSet | Stateful workload with `volumeClaimTemplates`; `serviceName` names a governing `service` authored beside it. Emits no Service (go-kure/launcher#690). Declares one endpoint per main-container port. |
| `storageclass` | StorageClass | Kind-named StorageClass: the object's fields beside its identity (`provisioner`, required, `parameters`, `reclaimPolicy`, …), strictly decoded. Cluster-scoped; no environment policy applies — see below. |
| `tcproute` | TCPRoute | Kind-named Gateway API TCPRoute: the whole `TCPRouteSpec`, strictly decoded; `rules` and a rule's `backendRefs` are required, with the `name` of a backend and the `port` of one that is a Service. An authored object, as the `httproute` kind is: no parent is synthesized from a capability, no NetworkPolicy allow rule is synthesized for it, no capability is required and no environment policy applies — see below. |
| `thanosruler` | ThanosRuler | Kind-named Prometheus operator ThanosRuler: the whole `ThanosRulerSpec`, strictly decoded, with the spec's `labels` (Prometheus external labels) published as `externalLabels`; no top-level field is required. The operator runs the pods: `image`, `replicas`, `resources`, storage, `containers`, `initContainers`, `volumes` and `securityContext` are held to the environment policy as the Alertmanager's are, and a literal `remoteWrite[].bearerToken` under a policy that forbids explicit secrets. The replica count and memory request the operator fills where they are unset are held, not written. An image the operator chooses, for `thanos-ruler` or its config-reloader, is refused under a policy with allowed registries. No capability is required — see below. |
| `tlsroute` | TLSRoute | Kind-named Gateway API TLSRoute: the whole `TLSRouteSpec`, strictly decoded; `hostnames` is required, and the rest as on a `tcproute`. An authored object on the same terms — see below. |
| `udproute` | UDPRoute | Kind-named Gateway API UDPRoute: the whole `UDPRouteSpec`, strictly decoded; required as on a `tcproute`, and an authored object on the same terms — see below. |
| `validatingadmissionpolicy` | ValidatingAdmissionPolicy | Kind-named ValidatingAdmissionPolicy: its `ValidatingAdmissionPolicySpec`, strictly decoded; `matchConstraints` with at least one rule and at least one of `validations` and `auditAnnotations` are required. It can deny the requests of any namespace its rules match, once bound. Served from Kubernetes 1.30. Cluster-scoped; only the environment policy's object kind rules apply — see below. |
| `validatingadmissionpolicybinding` | ValidatingAdmissionPolicyBinding | Kind-named ValidatingAdmissionPolicyBinding: its `ValidatingAdmissionPolicyBindingSpec`, strictly decoded; `policyName` and at least one of `validationActions` are required, and a `paramRef` as on a `mutatingadmissionpolicybinding`. Neither the policy nor the parameter is resolved at build. Served from Kubernetes 1.30. Cluster-scoped; only the environment policy's object kind rules apply — see below. |
| `validatingwebhookconfiguration` | ValidatingWebhookConfiguration | Kind-named ValidatingWebhookConfiguration: its `webhooks`, strictly decoded, required as on a `mutatingwebhookconfiguration`. The API server calls it to admit or deny the requests of any namespace its rules match. Cluster-scoped; only the environment policy's object kind rules apply — see below. |
| `volumeattributesclass` | VolumeAttributesClass | Kind-named VolumeAttributesClass: `driverName` and `parameters` (both required, `parameters` with at least one entry), strictly decoded. Cluster-scoped; no environment policy applies — see below. |
| `webservice` | Deployment, Service, ServiceAccount (+PVC) | HTTP service with replicas, probes, env, volumes. Lowered to a same-name `deployment`, `service` and (unless `serviceAccountName` is authored) `serviceaccount` group plus a `topology-spread` trait (`WebserviceRule`) — see below. |
| `worker` | Deployment, ServiceAccount (+PVC) | Background workload (no Service/port). Lowered to a same-name `deployment` and (unless `serviceAccountName` is authored) `serviceaccount` group plus a `topology-spread` trait (`WorkerRule`) — see below. |

Only the two role kinds, `webservice` and `worker`, emit a per-component
ServiceAccount, and only when the component does not author
`serviceAccountName`. The five pod kinds (`deployment`, `statefulset`,
`daemonset`, `job`, `cronjob`) emit no ServiceAccount and no claim of their own
(go-kure/launcher#702). See "Pod-level properties" and "Referencing an existing
claim" below.

## Kind inventory

Every object the base library can construct, and how a document reaches it
(go-kure/launcher#790). A row is one of the base library's generated constructors
(`pkg/kubernetes/**/zz_generated_create.go` in `github.com/go-kure/kure`), written
`<package directory>.Create<Kind>`. Two tests hold the table to the code:

- `TestKindInventory_CoversEveryConstructor` reads those files from the linked module and
  fails when a constructor has no row, a row has no constructor, or a row's Kind cell is not
  the API version, kind and scope the constructor's doc comment gives. A base-library bump
  that adds a kind, or moves one to another API version, therefore fails until the table
  says so.
- `TestKindInventory_MatchesCallSites` holds the Status and Type columns to this package and
  `../traits`: a `kind` row's constructor is called here, a `trait` row's there or here (a
  trait may build through this package, as `configmap` does), and a `missing`, `held` or
  `not authorable` row's by neither. A `kind` or `component` row's Type is a type a handler
  or lowering rule of this package declares contract metadata for, a `trait` row's one of
  `../traits`, so a row stops passing when its handler is removed.

Not held by a test: which handler makes the call, so a `kind` row passes while its Type has a
handler and any component of this package calls its constructor; whether a `component` row's
constructor is called; the step from `trait` to `kind`, which the change that adds the kind
component makes in the row; the Decode and Notes columns, checked for presence only; and code
outside the two packages, which is not read.

Status is one of:

- `kind`: a kind component projects the object; Type is its component `type`.
- `component`: a component that is not a kind component emits it; Notes says why there is no
  kind component.
- `trait`: a trait emits it and no component does; Type is the trait `type`.
- `missing`: authorable, with no component yet. go-kure/launcher#790 adds these group by group.
- `held`: authorable, with no component until something named is in place; Notes gives the
  reason. A component for it would not do what the kind is authored for, or would act
  where no check of the build reaches.
- `not authorable`: no component is planned; Notes gives the reason.

**Cluster-wide admission and API registration** (go-kure/launcher#943). The
ValidatingWebhookConfiguration, MutatingWebhookConfiguration, APIService,
ValidatingAdmissionPolicy, ValidatingAdmissionPolicyBinding, MutatingAdmissionPolicy and
MutatingAdmissionPolicyBinding kinds are `kind` rows. No dimension of the environment policy
reads what they match, where they send a request or what a policy checks. They are gated as every emitted object is, by the policy's object kind rules
(`ObjectKindPolicy`): an environment that must not emit them forbids their group, their kind,
or cluster-scoped objects, and the build refuses them on every path. What they do once
applied is out of the build's reach, and each kind's entry states it:

- **They act on every other document's objects, or on the API itself.** The API server
  applies a webhook configuration, or a bound admission policy, to the requests its rules
  match, in any namespace and from any author; an APIService hands a group and version of the
  API to the Service it names.
- **A mutating webhook or a mutating admission policy changes objects after the build,** so
  what the build checked need not hold of the object that is stored.
- **Where they send a request is not resolved at build:** a webhook's `clientConfig` (a URL
  or a Service) and an APIService's `service` are written as authored.
- **What a binding names is not resolved at build:** neither the policy of its `policyName`
  nor the parameter resources of its `paramRef`. A policy does nothing until a binding names
  it.
- **CEL is compiled by the API server,** not at build: a match condition's expression, and an
  admission policy's validations, audit annotations, variables and mutations.
- **An admission policy kind is served from a Kubernetes version:** ValidatingAdmissionPolicy
  and its binding at `admissionregistration.k8s.io/v1` from 1.30, MutatingAdmissionPolicy and
  its binding from 1.36. A cluster below that refuses the object at apply. The build has no
  target version and does not check it; each kind's schema and entry state it.

An RBAC grant, which launcher also emits, differs: it names its subjects, and the document
that makes the grant states it. These kinds act on objects no document of the build names.
What `manifests`, `passthrough` and template delivery do with such a document is said in
their own entries.

Decode is how the properties become the object: `hand-written parser` (a schema and parser this
package or `../traits` maintains) or a strict decode into the named upstream type, where an
unknown or wrongly typed key is an error at every depth the decoder reaches. It does not reach
inside an upstream type that unmarshals itself, where an unknown nested key is dropped unless
the row says the type is checked separately, as the CiliumNetworkPolicy row does.

| Constructor | Kind | Status | Type | Decode | Notes |
|---|---|---|---|---|---|
| `kubernetes.CreateAPIService` | apiregistration.k8s.io/v1 APIService (cluster-scoped) | kind | `apiservice` | strict decode of `APIServiceSpec` | `group`, `version`, `groupPriorityMinimum` and `versionPriority` must be written, and a `service` its `namespace` and `name`. The object is named after the component unless `objectName` names it, and the kind refuses any name but `<version>.<group>`, which the API requires. It hands a group and version of the API to the Service it names, which is not resolved at build. The priorities' ranges, the port and how `caBundle` and `insecureSkipTLSVerify` combine are left to the API server. Only the object kind rules of the environment policy apply. See "Cluster-wide admission and API registration" above. |
| `kubernetes.CreateBackendTLSPolicy` | gateway.networking.k8s.io/v1 BackendTLSPolicy | kind | `backendtlspolicy` | strict decode of `BackendTLSPolicySpec` | `targetRefs`, at least one, and `validation` with its `hostname` must be written; of a target, a CA certificate reference and a subject alternative name that are authored, the fields the API requires that the type would write empty. The hostnames it validates are not held to the allowed registries. No capability is required. No environment policy applies. |
| `kubernetes.CreateBinding` | v1 Binding | not authorable | - | - | A request body for a pod's `binding` subresource, not a stored object. |
| `kubernetes.CreateCSIDriver` | storage.k8s.io/v1 CSIDriver (cluster-scoped) | kind | `csidriver` | strict decode of `CSIDriverSpec` | The object's name, the component's or its `objectName`, is the CSI driver's name. The API documents a limit of 63 characters for it and the API server does not hold the object to that limit. Its labels and annotations are the `labels` and `annotations` properties. No environment policy applies. |
| `kubernetes.CreateCSINode` | storage.k8s.io/v1 CSINode (cluster-scoped) | not authorable | - | - | Written by the kubelet for the CSI drivers on its node. |
| `kubernetes.CreateCSIStorageCapacity` | storage.k8s.io/v1 CSIStorageCapacity | not authorable | - | - | Written by a CSI driver's provisioner. |
| `kubernetes.CreateClusterRole` | rbac.authorization.k8s.io/v1 ClusterRole (cluster-scoped) | kind | `clusterrole` | strict decode of the object, less `kind`, `apiVersion` and `metadata` | **Ungated: no capability and no environment-policy check restricts what a role grants.** A rule's `verbs` must be written, and either its `apiGroups` and `resources` or its `nonResourceURLs`; an `aggregationRule` needs a selector. The `rbac` trait emits one too, through its own hand-written parser. |
| `kubernetes.CreateClusterRoleBinding` | rbac.authorization.k8s.io/v1 ClusterRoleBinding (cluster-scoped) | kind | `clusterrolebinding` | strict decode of the object, less `kind`, `apiVersion` and `metadata` | **Ungated: no capability and no environment-policy check restricts what a role grants.** `roleRef` with its `kind` and `name` must be written, a subject's `kind` and `name`, and a ServiceAccount subject's `namespace`. The names are the author's literals. The `rbac` trait emits one too, through its own hand-written parser. |
| `kubernetes.CreateComponentStatus` | v1 ComponentStatus (cluster-scoped) | not authorable | - | - | Read-only: the API server computes it. |
| `kubernetes.CreateConfigMap` | v1 ConfigMap | kind | `configmap` | hand-written parser | The `configmap` trait builds through the same path. |
| `kubernetes.CreateControllerRevision` | apps/v1 ControllerRevision | not authorable | - | - | Written by the StatefulSet and DaemonSet controllers. |
| `kubernetes.CreateCronJob` | batch/v1 CronJob | kind | `cronjob` | hand-written parser | - |
| `kubernetes.CreateCustomResourceDefinition` | apiextensions.k8s.io/v1 CustomResourceDefinition (cluster-scoped) | component | `crd` | the manifest parser, CustomResourceDefinition documents only | The stated exception: an application takes its CRDs from upstream files (`inline` or `url`), so no kind component projects the spec. |
| `kubernetes.CreateDaemonSet` | apps/v1 DaemonSet | kind | `daemonset` | hand-written parser | - |
| `kubernetes.CreateDeployment` | apps/v1 Deployment | kind | `deployment` | hand-written parser | `webservice` and `worker` lower onto it. |
| `kubernetes.CreateEndpointSlice` | discovery.k8s.io/v1 EndpointSlice | kind | `endpointslice` | strict decode of the object, less `kind`, `apiVersion` and `metadata` | A slice belongs to a Service only through its `kubernetes.io/service-name` label, authored under `labels` as a literal that does not follow the Service's `objectName`. `addressType` and an endpoint's `addresses` must be written. The addresses and FQDNs of its endpoints are not artifact sources. No environment policy applies. |
| `kubernetes.CreateEndpoints` | v1 Endpoints | not authorable | - | - | Not offered: deprecated upstream in favour of EndpointSlice. |
| `kubernetes.CreateEvent` | v1 Event | not authorable | - | - | A record the system writes at run time. |
| `kubernetes.CreateEviction` | policy/v1 Eviction | not authorable | - | - | A request body for a pod's `eviction` subresource, not a stored object. |
| `kubernetes.CreateGRPCRoute` | gateway.networking.k8s.io/v1 GRPCRoute | kind | `grpcroute` | strict decode of `GRPCRouteSpec` | On the `httproute` kind's recipe: nothing required or filled, no environment policy, parent and backend references carried as authored, their namespaces included. The API requires no rule, backend or hostname of a GRPCRoute, as of an HTTPRoute, unlike the TCP, UDP and TLS routes. No type under `GRPCRouteSpec` unmarshals itself, so the decode reaches every depth. |
| `kubernetes.CreateGateway` | gateway.networking.k8s.io/v1 Gateway | kind | `gateway` | strict decode of `GatewaySpec` | `gatewayClassName` and `listeners` must be written, and of a listener its `name`, `port` and `protocol`; of what is authored below them, the fields the API requires that the type would write empty. `defaultScope` is an experimental-channel field. The pods a controller starts for a Gateway are not the object's to size. No capability is required: the Gateway an `httproute` trait takes from a capability is not this component. No environment policy applies. |
| `kubernetes.CreateGatewayClass` | gateway.networking.k8s.io/v1 GatewayClass (cluster-scoped) | kind | `gatewayclass` | strict decode of `GatewayClassSpec` | `controllerName` must be written, and of a `parametersRef` that is authored its `group`, `kind` and `name`. No capability is required. No environment policy applies. |
| `kubernetes.CreateHTTPRoute` | gateway.networking.k8s.io/v1 HTTPRoute | kind | `httproute` | strict decode of `HTTPRouteSpec` | No type under `HTTPRouteSpec` unmarshals itself, so the decode reaches every depth. The `httproute` trait, which `expose` lowers onto, builds its own HTTPRoute with a hand-written parser. Only the trait feeds the NetworkPolicy synthesis, takes its parent from a capability and is held to the policy's capability lists. |
| `kubernetes.CreateHorizontalPodAutoscaler` | autoscaling/v2 HorizontalPodAutoscaler | kind | `horizontalpodautoscaler` | strict decode of `HorizontalPodAutoscalerSpec` | `scaleTargetRef` and `maxReplicas` must be written. `maxReplicas` is held to the environment policy's replica maximum. The `scaler` trait emits one for its workload too, through its own parser. |
| `kubernetes.CreateIPAddress` | networking.k8s.io/v1 IPAddress (cluster-scoped) | not authorable | - | - | Allocated by the API server for a Service. |
| `kubernetes.CreateIngress` | networking.k8s.io/v1 Ingress | kind | `ingress` | strict decode of `IngressSpec` | The `ingress` trait, which `expose` lowers onto, builds its own Ingress with a hand-written parser. Only the trait feeds the NetworkPolicy synthesis and is held to the platform's hostname constraint and the policy's capability lists. |
| `kubernetes.CreateIngressClass` | networking.k8s.io/v1 IngressClass (cluster-scoped) | kind | `ingressclass` | strict decode of `IngressClassSpec` | The object is named after the component unless `objectName` names it. Its labels and annotations are the `labels` and `annotations` properties, the default-class annotation (`ingressclass.kubernetes.io/is-default-class`) included. `controller` must be written. No environment policy applies. |
| `kubernetes.CreateJob` | batch/v1 Job | kind | `job` | hand-written parser | - |
| `kubernetes.CreateLease` | coordination.k8s.io/v1 Lease | not authorable | - | - | Written at run time by its holder: a leader-election client, or the kubelet for its node's heartbeat. |
| `kubernetes.CreateLimitRange` | v1 LimitRange | kind | `limitrange` | strict decode of `LimitRangeSpec` | - |
| `kubernetes.CreateListenerSet` | gateway.networking.k8s.io/v1 ListenerSet | kind | `listenerset` | strict decode of `ListenerSetSpec` | `parentRef` with its `name` and `listeners`, at least one, must be written, and of each listener its `name`, `port` and `protocol`, as on a Gateway's. No capability is required. No environment policy applies. |
| `kubernetes.CreateMutatingAdmissionPolicy` | admissionregistration.k8s.io/v1 MutatingAdmissionPolicy (cluster-scoped) | kind | `mutatingadmissionpolicy` | strict decode of `MutatingAdmissionPolicySpec` | As the `validatingadmissionpolicy` row for `matchConstraints`, `paramKind`, match conditions and variables, its rules taking only CREATE, UPDATE and CONNECT; at least one of `mutations` must be written, each with its `patchType` and, for ApplyConfiguration or JSONPatch, the patch of that type with its `expression`, and `reinvocationPolicy`, which the API server does not default. It changes objects of any namespace after the build, so what the build checked need not hold of what is stored. Served from Kubernetes 1.36. Only the object kind rules of the environment policy apply. See "Cluster-wide admission and API registration" above. |
| `kubernetes.CreateMutatingAdmissionPolicyBinding` | admissionregistration.k8s.io/v1 MutatingAdmissionPolicyBinding (cluster-scoped) | kind | `mutatingadmissionpolicybinding` | strict decode of `MutatingAdmissionPolicyBindingSpec` | As the `validatingadmissionpolicybinding` row, without `validationActions`. It puts a MutatingAdmissionPolicy into effect. Served from Kubernetes 1.36. See "Cluster-wide admission and API registration" above. |
| `kubernetes.CreateMutatingWebhookConfiguration` | admissionregistration.k8s.io/v1 MutatingWebhookConfiguration (cluster-scoped) | kind | `mutatingwebhookconfiguration` | strict decode of the object, less `kind`, `apiVersion` and `metadata` | As the `validatingwebhookconfiguration` row, and it changes objects of any namespace after the build, so what the build checked need not hold of what is stored. `reinvocationPolicy` is written as authored. See "Cluster-wide admission and API registration" above. |
| `kubernetes.CreateNamespace` | v1 Namespace (cluster-scoped) | kind | `namespace` | strict decode of `NamespaceSpec` | The component name is the Namespace's name. Its labels and annotations are the `labels` and `annotations` properties. |
| `kubernetes.CreateNetworkPolicy` | networking.k8s.io/v1 NetworkPolicy | kind | `networkpolicy` | strict decode of `NetworkPolicySpec` | No type under `NetworkPolicySpec` unmarshals itself except `intstr.IntOrString` (a port), a scalar with no nested key to drop. The `networkpolicy` trait builds its own NetworkPolicy with a hand-written parser and scopes it to its component's pods; the kind selects what the author wrote. The transform's NetworkPolicy synthesis in `pkg/oam` emits NetworkPolicies of its own and reads neither. |
| `kubernetes.CreateNode` | v1 Node (cluster-scoped) | not authorable | - | - | Registered by the kubelet. |
| `kubernetes.CreatePersistentVolume` | v1 PersistentVolume (cluster-scoped) | kind | `persistentvolume` | strict decode of `PersistentVolumeSpec` | Held to environment policy on every path that produces one. |
| `kubernetes.CreatePersistentVolumeClaim` | v1 PersistentVolumeClaim | kind | `persistentvolumeclaim` | hand-written parser | The `pvc` trait builds through the same path. |
| `kubernetes.CreatePod` | v1 Pod | kind | `pod` | strict decode of `PodSpec` | Held to environment policy by the check the rendered paths run on a Pod; `ephemeralContainers`, `priority` and `overhead` refused. |
| `kubernetes.CreatePodDisruptionBudget` | policy/v1 PodDisruptionBudget | kind | `poddisruptionbudget` | strict decode of `PodDisruptionBudgetSpec` | No top-level field is required; a defective match expression of `selector` is refused. No environment policy applies. The `scaler` trait emits one for its workload too (`enablePDB`), through its own parser. |
| `kubernetes.CreatePodTemplate` | v1 PodTemplate | kind | `podtemplate` | strict decode of the object's `template` (`PodTemplateSpec`) | Held to environment policy by the check the rendered paths run on a PodTemplate; the pod spec is held to the `pod` kind's refusals, and `activeDeadlineSeconds` is allowed. Stored, not run: no `app` label, not a trait target. |
| `kubernetes.CreatePriorityClass` | scheduling.k8s.io/v1 PriorityClass (cluster-scoped) | kind | `priorityclass` | strict decode of the object, less `kind`, `apiVersion` and `metadata` | The object is named after the component unless `objectName` names it. Its labels and annotations are the `labels` and `annotations` properties. An unauthored `value` is emitted as `0`. No environment policy applies. |
| `kubernetes.CreateRangeAllocation` | v1 RangeAllocation (cluster-scoped) | not authorable | - | - | The API server's own allocation record. |
| `kubernetes.CreateReferenceGrant` | gateway.networking.k8s.io/v1 ReferenceGrant | kind | `referencegrant` | strict decode of `ReferenceGrantSpec` | `from` and `to` must be written, and of a source its `group`, `kind` and `namespace`, of a target its `group` and `kind`. The grant is emitted in the build namespace, which is the namespace it allows references into. No capability is required. No environment policy applies. |
| `kubernetes.CreateReplicaSet` | apps/v1 ReplicaSet | kind | `replicaset` | strict decode of `ReplicaSetSpec` | Held to environment policy by the check the rendered paths run on a ReplicaSet; the pod template is held to the `pod` kind's refusals, `activeDeadlineSeconds` is refused, and the template gains the `app` label. |
| `kubernetes.CreateReplicationController` | v1 ReplicationController | kind | `replicationcontroller` | strict decode of `ReplicationControllerSpec` | Held to environment policy by the check the rendered paths run on a ReplicationController; the pod template is held as the `replicaset` kind's is, `activeDeadlineSeconds` is refused, and the template gains the `app` label. `selector` is optional. |
| `kubernetes.CreateResourceQuota` | v1 ResourceQuota | kind | `resourcequota` | strict decode of `ResourceQuotaSpec` | - |
| `kubernetes.CreateRole` | rbac.authorization.k8s.io/v1 Role | kind | `role` | strict decode of the object, less `kind`, `apiVersion` and `metadata` | **Ungated: no capability and no environment-policy check restricts what a role grants.** A rule's `verbs`, `apiGroups` and `resources` must be written; `nonResourceURLs` are refused. The `rbac` trait emits one too, through its own hand-written parser. |
| `kubernetes.CreateRoleBinding` | rbac.authorization.k8s.io/v1 RoleBinding | kind | `rolebinding` | strict decode of the object, less `kind`, `apiVersion` and `metadata` | **Ungated: no capability and no environment-policy check restricts what a role grants.** `roleRef` with its `kind` and `name` must be written, and a subject's `kind` and `name`. The names are the author's literals. The `rbac` trait emits one too, through its own hand-written parser. |
| `kubernetes.CreateRuntimeClass` | node.k8s.io/v1 RuntimeClass (cluster-scoped) | kind | `runtimeclass` | strict decode of the object, less `kind`, `apiVersion` and `metadata` | The object is named after the component unless `objectName` names it. Its labels and annotations are the `labels` and `annotations` properties. No environment policy applies. |
| `kubernetes.CreateSecret` | v1 Secret | kind | `secret` | hand-written parser | The `secret` trait builds through the same path. The `helm` component's `secretValues` synthesizes the trait. Refused under a policy that forbids explicit secrets, on either path. |
| `kubernetes.CreateService` | v1 Service | kind | `service` | hand-written parser | - |
| `kubernetes.CreateServiceAccount` | v1 ServiceAccount | kind | `serviceaccount` | hand-written parser | - |
| `kubernetes.CreateServiceCIDR` | networking.k8s.io/v1 ServiceCIDR (cluster-scoped) | kind | `servicecidr` | strict decode of `ServiceCIDRSpec` | The object is named after the component unless `objectName` names it. Its labels and annotations are the `labels` and `annotations` properties. At least one of `cidrs` must be written. No environment policy applies. |
| `kubernetes.CreateStatefulSet` | apps/v1 StatefulSet | kind | `statefulset` | hand-written parser | - |
| `kubernetes.CreateStorageClass` | storage.k8s.io/v1 StorageClass (cluster-scoped) | kind | `storageclass` | strict decode of the object, less `kind`, `apiVersion` and `metadata` | The object is named after the component unless `objectName` names it. Its labels and annotations are the `labels` and `annotations` properties, the default-class annotation (`storageclass.kubernetes.io/is-default-class`) included. No environment policy applies. |
| `kubernetes.CreateTCPRoute` | gateway.networking.k8s.io/v1 TCPRoute | kind | `tcproute` | strict decode of `TCPRouteSpec` | No type under `TCPRouteSpec` unmarshals itself, so the decode reaches every depth. The kind refuses a route without `rules`, a rule without `backendRefs` and a Service backend without its `port`, which the API server refuses too; of a parent and a backend that are authored, the `name` must be written. A parent and a backend of another namespace are written as authored, as the `httproute` kind writes them. No capability is required. No environment policy applies. |
| `kubernetes.CreateTLSRoute` | gateway.networking.k8s.io/v1 TLSRoute | kind | `tlsroute` | strict decode of `TLSRouteSpec` | As the `tcproute` row, and the kind refuses a route without `hostnames` too, which the API requires of a TLSRoute. The host names are not held to the allowed registries, and their form is left to the API server. |
| `kubernetes.CreateUDPRoute` | gateway.networking.k8s.io/v1 UDPRoute | kind | `udproute` | strict decode of `UDPRouteSpec` | As the `tcproute` row: the decode reaches every depth, the kind refuses a route without `rules`, a rule without `backendRefs` and a Service backend without its `port`, and a parent and a backend of another namespace are written as authored. No capability is required. No environment policy applies. |
| `kubernetes.CreateValidatingAdmissionPolicy` | admissionregistration.k8s.io/v1 ValidatingAdmissionPolicy (cluster-scoped) | kind | `validatingadmissionpolicy` | strict decode of `ValidatingAdmissionPolicySpec` | The object is named after the component unless `objectName` names it. `matchConstraints` must be written with at least one rule, and a rule names at least one operation, API group, API version and resource; at least one of `validations` and `auditAnnotations`; a validation its `expression`, an audit annotation its `key` and `valueExpression`, a match condition and a variable their `name` and `expression`, and an authored `paramKind` its `apiVersion` and `kind`; a selector's match expression is checked as every selector's is. Once bound, it can deny writes for the whole cluster. CEL is compiled by the API server, and the form of every other value (the enumerations, names and their uniqueness) is left to it. Served from Kubernetes 1.30. Only the object kind rules of the environment policy apply. See "Cluster-wide admission and API registration" above. |
| `kubernetes.CreateValidatingAdmissionPolicyBinding` | admissionregistration.k8s.io/v1 ValidatingAdmissionPolicyBinding (cluster-scoped) | kind | `validatingadmissionpolicybinding` | strict decode of `ValidatingAdmissionPolicyBindingSpec` | `policyName` and at least one of `validationActions` must be written; an authored `paramRef` holds exactly one of `name` and `selector`, and its `parameterNotFoundAction`; an authored `matchResources` is checked as a policy's `matchConstraints`, without requiring a rule. It puts a ValidatingAdmissionPolicy into effect. Neither the policy nor the parameter is resolved at build. Served from Kubernetes 1.30. Only the object kind rules of the environment policy apply. See "Cluster-wide admission and API registration" above. |
| `kubernetes.CreateValidatingWebhookConfiguration` | admissionregistration.k8s.io/v1 ValidatingWebhookConfiguration (cluster-scoped) | kind | `validatingwebhookconfiguration` | strict decode of the object, less `kind`, `apiVersion` and `metadata` | The object is named after the component unless `objectName` names it. A webhook's `name`, `clientConfig`, `sideEffects` and at least one of `admissionReviewVersions` must be written; `clientConfig` holds exactly one of `url` and `service`, and a service its `namespace` and `name`; a rule names at least one operation, API group, API version and resource; a match condition its `name` and `expression`; a selector's match expression is checked as every selector's is. The API server calls the webhooks on the requests of any namespace their rules match. Neither the URL nor the Service is resolved at build, and a match condition's CEL expression is compiled by the API server. The form of every other value (a webhook's name, the enumerations, the timeout's range) is left to the API server. Only the object kind rules of the environment policy apply. See "Cluster-wide admission and API registration" above. |
| `kubernetes.CreateVolumeAttachment` | storage.k8s.io/v1 VolumeAttachment (cluster-scoped) | not authorable | - | - | Written by the attach/detach controller. |
| `kubernetes.CreateVolumeAttributesClass` | storage.k8s.io/v1 VolumeAttributesClass (cluster-scoped) | kind | `volumeattributesclass` | strict decode of the object, less `kind`, `apiVersion` and `metadata` | The object is named after the component unless `objectName` names it. Its labels and annotations are the `labels` and `annotations` properties. `driverName` and at least one of `parameters` must be written. No environment policy applies. |
| `certmanager.CreateCertificate` | cert-manager.io/v1 Certificate | kind | `certificate` | strict decode of `CertificateSpec` | Its labels and annotations are the `labels` and `annotations` properties. `secretName` and `issuerRef` with its `name` must be written. A keystore password in the object is refused under a policy that forbids explicit secrets. No capability is required. The Secret `secretName` names is the one the late-maker check sees it make (go-kure/launcher#1086). The `certificate` trait builds a Certificate for a workload through the same constructor, from a hand-written parser. |
| `certmanager.CreateCertificateRequest` | cert-manager.io/v1 CertificateRequest | not authorable | - | - | A one-shot request cert-manager creates for a Certificate. |
| `certmanager.CreateChallenge` | acme.cert-manager.io/v1 Challenge | not authorable | - | - | Created by cert-manager's ACME issuer. |
| `certmanager.CreateClusterIssuer` | cert-manager.io/v1 ClusterIssuer (cluster-scoped) | kind | `clusterissuer` | strict decode of `IssuerSpec` | Its labels and annotations are the `labels` and `annotations` properties. No top-level field must be written; of an issuer type that is authored, the fields the API requires that the type would write empty. The cpu and memory of an ACME HTTP01 solver's pod template are held to the policy's maxima. No capability is required. |
| `certmanager.CreateIssuer` | cert-manager.io/v1 Issuer | kind | `issuer` | strict decode of `IssuerSpec` | As `clusterissuer`, in the build namespace. Its labels and annotations are the `labels` and `annotations` properties. |
| `certmanager.CreateOrder` | acme.cert-manager.io/v1 Order | not authorable | - | - | Created by cert-manager's ACME issuer. |
| `cilium.CreateCiliumBGPAdvertisement` | cilium.io/v2 CiliumBGPAdvertisement (cluster-scoped) | kind | `cilium-bgpadvertisement` | strict decode of `CiliumBGPAdvertisementSpec` | The object is named after the component unless `objectName` names it. Its labels and annotations are the `labels` and `annotations` properties; a peer configuration selects it by its labels. The CRD's five rules on an entry's type are checked. No environment policy applies. |
| `cilium.CreateCiliumBGPClusterConfig` | cilium.io/v2 CiliumBGPClusterConfig (cluster-scoped) | kind | `cilium-bgpclusterconfig` | strict decode of `CiliumBGPClusterConfigSpec` | The object is named after the component unless `objectName` names it. Its labels and annotations are the `labels` and `annotations` properties. A peer's address is not held to the allowed registries. No environment policy applies. |
| `cilium.CreateCiliumBGPNodeConfig` | cilium.io/v2 CiliumBGPNodeConfig (cluster-scoped) | not authorable | - | - | Generated by the Cilium operator from a CiliumBGPClusterConfig. |
| `cilium.CreateCiliumBGPNodeConfigOverride` | cilium.io/v2 CiliumBGPNodeConfigOverride (cluster-scoped) | kind | `cilium-bgpnodeconfigoverride` | strict decode of `CiliumBGPNodeConfigOverrideSpec` | The object overrides the CiliumBGPNodeConfig of the same name: `objectName` sets it where the component name is not that name. Its labels and annotations are the `labels` and `annotations` properties. No environment policy applies. |
| `cilium.CreateCiliumBGPPeerConfig` | cilium.io/v2 CiliumBGPPeerConfig (cluster-scoped) | kind | `cilium-bgppeerconfig` | strict decode of `CiliumBGPPeerConfigSpec` | The object is named after the component unless `objectName` names it. Its labels and annotations are the `labels` and `annotations` properties. The CRD's rule on `timers` is checked on its two fields as authored or as the CRD defaults them. `authSecretRef` names a Secret and holds no secret. No environment policy applies. |
| `cilium.CreateCiliumCIDRGroup` | cilium.io/v2 CiliumCIDRGroup (cluster-scoped) | kind | `cilium-cidrgroup` | strict decode of `CiliumCIDRGroupSpec` | The object is named after the component unless `objectName` names it, and a Cilium network policy refers to it by that name. Its labels and annotations are the `labels` and `annotations` properties; a policy selects a group by its labels. No environment policy applies. |
| `cilium.CreateCiliumClusterwideEnvoyConfig` | cilium.io/v2 CiliumClusterwideEnvoyConfig (cluster-scoped) | held | - | - | As the CiliumEnvoyConfig row, which shares its spec type, and cluster-scoped: its `resources` are raw Envoy configuration no build-time rule can read, and its `services` forward the traffic of the Services they name to an Envoy listener (go-kure/launcher#790). |
| `cilium.CreateCiliumClusterwideNetworkPolicy` | cilium.io/v2 CiliumClusterwideNetworkPolicy (cluster-scoped) | kind | `cilium-clusterwidenetworkpolicy` | strict decode of `spec` and `specs` into the Cilium `Rule` | The decode and the unknown-key check of the `cilium-networkpolicy` kind (`builtin.UnknownCiliumKeyPath`). A rule selects endpoints or nodes, exactly one of the two. The fields a rule requires are read from the CRD of the linked module. Its labels and annotations are the `labels` and `annotations` properties. No environment policy applies. |
| `cilium.CreateCiliumEgressGatewayPolicy` | cilium.io/v2 CiliumEgressGatewayPolicy (cluster-scoped) | kind | `cilium-egressgatewaypolicy` | strict decode of `CiliumEgressGatewayPolicySpec` | The object is named after the component unless `objectName` names it. Its labels and annotations are the `labels` and `annotations` properties. The CRD's rule on `egressIP` is a value rule and the API server's. No address or CIDR is held to the allowed registries. No environment policy applies. |
| `cilium.CreateCiliumEndpoint` | cilium.io/v2 CiliumEndpoint | not authorable | - | - | Written by the Cilium agent. |
| `cilium.CreateCiliumEnvoyConfig` | cilium.io/v2 CiliumEnvoyConfig | held | - | - | Its `resources` are raw Envoy configuration, in the API's words "Envoy xDS resources" (listeners, routes, clusters, endpoints and TLS secrets), each an opaque `Any` the CRD keeps unpruned, and its `services` forward the traffic of the Services they name to an Envoy listener. No build-time rule can read that configuration: the kind would be a way round every rule a policy holds a route, a network policy or a Secret to (go-kure/launcher#790). |
| `cilium.CreateCiliumIdentity` | cilium.io/v2 CiliumIdentity (cluster-scoped) | not authorable | - | - | Written by Cilium when it allocates an identity. |
| `cilium.CreateCiliumLoadBalancerIPPool` | cilium.io/v2 CiliumLoadBalancerIPPool (cluster-scoped) | kind | `cilium-loadbalancerippool` | strict decode of `CiliumLoadBalancerIPPoolSpec` | The object is named after the component unless `objectName` names it. Its labels and annotations are the `labels` and `annotations` properties. An authored `disabled: false` is the API's default and is not written. No environment policy applies. |
| `cilium.CreateCiliumLocalRedirectPolicy` | cilium.io/v2 CiliumLocalRedirectPolicy | kind | `cilium-localredirectpolicy` | strict decode of `CiliumLocalRedirectPolicySpec` | The object is named after the component unless `objectName` names it. Its labels and annotations are the `labels` and `annotations` properties. The CRD's choice of one frontend matcher is checked; its three rules against a change are the API server's. No environment policy applies. |
| `cilium.CreateCiliumNetworkPolicy` | cilium.io/v2 CiliumNetworkPolicy | kind | `cilium-networkpolicy` | strict decode of `spec` and `specs` into the Cilium `Rule` | The endpoint selector, the ICMP field and the rule label unmarshal themselves and drop an unknown key; the kind refuses one by its path at every position that holds any of them (`builtin.UnknownCiliumKeyPath`), and a test holds the list of such types to the Cilium API. The `cilium-networkpolicy` trait builds its own CiliumNetworkPolicy from one rule's `endpointSelector`, `ingress` and `egress`, with the same decode, the same check and the kind's required list cut to those three fields. |
| `cilium.CreateCiliumNode` | cilium.io/v2 CiliumNode (cluster-scoped) | not authorable | - | - | Written by the Cilium agent for its node. |
| `cilium.CreateCiliumNodeConfig` | cilium.io/v2 CiliumNodeConfig | kind | `cilium-nodeconfig` | strict decode of `CiliumNodeConfigSpec` | The object is named after the component unless `objectName` names it. Its labels and annotations are the `labels` and `annotations` properties. The keys and the values of `defaults` are not checked. No environment policy applies. |
| `cnpg.CreateBackup` | postgresql.cnpg.io/v1 Backup | kind | `cnpg-backup` | strict decode of `BackupSpec` | The object is named after the component unless `objectName` names it. Its labels and annotations are the `labels` and `annotations` properties. A one-shot request: the API refuses every change of its spec, and applied again it runs nothing. No environment policy applies. |
| `cnpg.CreateCluster` | postgresql.cnpg.io/v1 Cluster | kind | `cnpg-cluster` | strict decode of `ClusterSpec` | `postgresql` lowers onto it. |
| `cnpg.CreateClusterImageCatalog` | postgresql.cnpg.io/v1 ClusterImageCatalog (cluster-scoped) | kind | `cnpg-clusterimagecatalog` | strict decode of `ImageCatalogSpec` | As `cnpg-imagecatalog`, with no namespace. Its labels and annotations are the `labels` and `annotations` properties. |
| `cnpg.CreateDatabase` | postgresql.cnpg.io/v1 Database | kind | `cnpg-database` | strict decode of `DatabaseSpec` | `postgresql` lowers onto it. |
| `cnpg.CreateDatabaseRole` | postgresql.cnpg.io/v1 DatabaseRole | kind | `cnpg-databaserole` | strict decode of `DatabaseRoleSpec` | The object is named after the component unless `objectName` names it. Its labels and annotations are the `labels` and `annotations` properties. The CRD's eight rules on a new object are checked; its two against a change are the API server's. `passwordSecret` names a Secret and holds no secret. No environment policy applies. |
| `cnpg.CreateFailoverQuorum` | postgresql.cnpg.io/v1 FailoverQuorum | not authorable | - | - | Written by the CloudNativePG operator. |
| `cnpg.CreateImageCatalog` | postgresql.cnpg.io/v1 ImageCatalog | kind | `cnpg-imagecatalog` | strict decode of `ImageCatalogSpec` | The object is named after the component unless `objectName` names it, and a Cluster refers to it by that name. Its labels and annotations are the `labels` and `annotations` properties. The CRD's two rules (one image per major version, one component image per key) are checked. Every image it names is held to the policy's allowed registries. |
| `cnpg.CreateObjectStore` | barmancloud.cnpg.io/v1 ObjectStore | kind | `cnpg-objectstore` | strict decode of `ObjectStoreSpec` | `postgresql` lowers onto it. |
| `cnpg.CreatePooler` | postgresql.cnpg.io/v1 Pooler | kind | `cnpg-pooler` | strict decode of `PoolerSpec` | `postgresql` lowers onto it. |
| `cnpg.CreatePublication` | postgresql.cnpg.io/v1 Publication | kind | `cnpg-publication` | strict decode of `PublicationSpec` | The object is named after the component unless `objectName` names it. Its labels and annotations are the `labels` and `annotations` properties. The CRD's three rules on a target are checked; its four against a change are the API server's. No environment policy applies. |
| `cnpg.CreateScheduledBackup` | postgresql.cnpg.io/v1 ScheduledBackup | kind | `cnpg-scheduledbackup` | strict decode of `ScheduledBackupSpec` | The object is named after the component unless `objectName` names it. Its labels and annotations are the `labels` and `annotations` properties. The schedule is not parsed (`postgresql`, which lowers onto it, parses its `backup.schedules`). No environment policy applies. |
| `cnpg.CreateSubscription` | postgresql.cnpg.io/v1 Subscription | kind | `cnpg-subscription` | strict decode of `SubscriptionSpec` | The object is named after the component unless `objectName` names it. Its labels and annotations are the `labels` and `annotations` properties. The CRD's three rules refuse a change and are the API server's. No environment policy applies. |
| `externalsecrets.CreateClusterExternalSecret` | external-secrets.io/v1 ClusterExternalSecret (cluster-scoped) | kind | `clusterexternalsecret` | strict decode of `ClusterExternalSecretSpec` | The object is named after the component unless `objectName` names it. Its labels and annotations are the `labels` and `annotations` properties; those of the ExternalSecrets it creates are `externalSecretMetadata`. `externalSecretSpec` must be written, with what an `externalsecret` requires. `externalSecretSpec.target.manifest` is held to the environment policy as on an `externalsecret`. |
| `externalsecrets.CreateClusterSecretStore` | external-secrets.io/v1 ClusterSecretStore (cluster-scoped) | kind | `clustersecretstore` | strict decode of `SecretStoreSpec` | The object is named after the component unless `objectName` names it. Its labels and annotations are the `labels` and `annotations` properties. `provider` must be written with exactly one provider; of that provider, the fields the API requires that the type would write empty, and the four it would default. A credential written as a `value`, and the data of the `fake` provider, are refused under a policy that forbids explicit secrets. No capability is required. |
| `externalsecrets.CreateExternalSecret` | external-secrets.io/v1 ExternalSecret | kind | `externalsecret` | strict decode of `ExternalSecretSpec` | Its labels and annotations are the `labels` and `annotations` properties. No top-level field must be written. A generator named as the source of one key of `data` is refused. A `target.manifest` whose `apiVersion` is no API version is refused. A `target.manifest` of a kind the environment policy checks (a workload, a claim, a PersistentVolume, a HorizontalPodAutoscaler) is refused, and a core Secret there under a policy that forbids explicit secrets. No capability is required. Its target Secret (`target.name`, else the object's name), under a `creationPolicy` that creates it and with no `target.manifest`, is the one the late-maker check sees it make (go-kure/launcher#1086). The `external-secret` trait builds an ExternalSecret for a workload through the same constructor, from a hand-written parser. |
| `externalsecrets.CreateSecretStore` | external-secrets.io/v1 SecretStore | kind | `secretstore` | strict decode of `SecretStoreSpec` | As `clustersecretstore`, in the build namespace. Its labels and annotations are the `labels` and `annotations` properties. |
| `fluxcd.CreateAlert` | notification.toolkit.fluxcd.io/v1 Alert | kind | `fluxcd-alert` | strict decode of `AlertSpec` | `providerRef` with its `name` and `eventSources` must be written, and of a source its `kind` and `name`: the fields the linked Go source marks required, not held to a CRD. A source's `namespace` is written as authored. It lands in the Flux namespace when one is set. No environment policy applies. The type name carries a prefix: an alert, in this package, also reads as an alerting rule. |
| `fluxcd.CreateArtifactGenerator` | source.extensions.fluxcd.io/v1beta1 ArtifactGenerator | kind | `artifactgenerator` | strict decode of `ArtifactGeneratorSpec` | `sources` and `artifacts` must be written; of a source its `alias`, `kind` and `name`, of an artifact its `name` and `copy`, of a copy its `from` and `to`: the fields the linked Go source marks required, not held to a CRD. A source's `namespace`, an artifact's `namespace` and `serviceAccountName` are written as authored. The API's expression rules, which hold an artifact's `name` to an object name and its `namespace` to a namespace name where no `pathPattern` is set, are not checked. Of the five fields the v2.3 source-watcher API adds, four are written as authored, and `managedNamespaces` is refused whatever its value: it hands the controller the lifecycle of cluster-scoped Namespaces, which it creates, adopts and, with `prune`, deletes with everything in them (below). It lands in the Flux namespace when one is set. No environment policy applies. |
| `fluxcd.CreateBucket` | source.toolkit.fluxcd.io/v1 Bucket | kind | `bucket` | strict decode of `BucketSpec` | - |
| `fluxcd.CreateExternalArtifact` | source.toolkit.fluxcd.io/v1 ExternalArtifact | not authorable | - | - | Written by the controller that produces the artifact. |
| `fluxcd.CreateFluxInstance` | fluxcd.controlplane.io/v1 FluxInstance | held | - | - | A FluxInstance is the installation of Flux itself, not an application's object: its spec lists "the controllers to install", and the API accepts one name for it, `flux`. A component type would let an application author the cluster's Flux (go-kure/launcher#790). |
| `fluxcd.CreateFluxReport` | fluxcd.controlplane.io/v1 FluxReport | not authorable | - | - | Written by the Flux operator. |
| `fluxcd.CreateGitRepository` | source.toolkit.fluxcd.io/v1 GitRepository | kind | `gitrepository` | strict decode of `GitRepositorySpec` | - |
| `fluxcd.CreateHelmChart` | source.toolkit.fluxcd.io/v1 HelmChart | kind | `helmchart` | strict decode of `HelmChartSpec` | - |
| `fluxcd.CreateHelmRelease` | helm.toolkit.fluxcd.io/v2 HelmRelease | kind | `helmrelease` | strict decode of `HelmReleaseSpec` | `helm` lowers onto it. Against an existing source, or an OCI chart helm-controller fetches itself (`chart.kind: OCIRepository`), whose `chart.spec.url` is held to the allowed registries. |
| `fluxcd.CreateHelmRepository` | source.toolkit.fluxcd.io/v1 HelmRepository | kind | `helmrepository` | strict decode of `HelmRepositorySpec` | - |
| `fluxcd.CreateImagePolicy` | image.toolkit.fluxcd.io/v1 ImagePolicy | kind | `imagepolicy` | strict decode of `ImagePolicySpec` | `imageRepositoryRef` with its `name` and `policy` must be written, and of a `semver` policy its `range`: the fields the linked Go source marks required, not held to a CRD. The repository's `namespace` is written as authored. `interval` is held to the pattern of a Flux duration; the API's two expression rules, which tie it to `digestReflectionPolicy: Always`, are not checked. It lands in the Flux namespace when one is set. No environment policy applies. |
| `fluxcd.CreateImageRepository` | image.toolkit.fluxcd.io/v1 ImageRepository | kind | `imagerepository` | strict decode of `ImageRepositorySpec` | `image` and `interval` must be written, and of an authored `secretRef`, `proxySecretRef` or `certSecretRef` its `name`, of an authored `accessFrom` its `namespaceSelectors`: the fields the linked Go source marks required, not held to a CRD. `interval` and `timeout` are each held to the pattern of their field; `timeout` takes no `h`, and one of an hour or more is written in minutes. It lands in the Flux namespace when one is set. The registry of `image` is held to the allowed registries of the environment policy; no tag rule applies. |
| `fluxcd.CreateImageUpdateAutomation` | image.toolkit.fluxcd.io/v1 ImageUpdateAutomation | kind | `imageupdateautomation` | strict decode of `ImageUpdateAutomationSpec` | `sourceRef` with its `kind` and `name` and `interval` must be written, and of an authored `git` its `commit` with the author's `email`: the fields the linked Go source marks required, not held to a CRD. `sourceRef.kind` is defaulted by the API and required here, since the type writes it empty. The source's `namespace` is written as authored, and so is where the automation pushes. `interval` and `policyChangeDelay` are each held to the pattern of a Flux duration. It lands in the Flux namespace when one is set. No environment policy applies. |
| `fluxcd.CreateKustomization` | kustomize.toolkit.fluxcd.io/v1 Kustomization | kind | `fluxcd-kustomization` | strict decode of `KustomizationSpec` | `oci` lowers onto it. `targetNamespace` is never defaulted. |
| `fluxcd.CreateOCIRepository` | source.toolkit.fluxcd.io/v1 OCIRepository | kind | `ocirepository` | strict decode of `OCIRepositorySpec` | `oci` lowers onto it. |
| `fluxcd.CreateProvider` | notification.toolkit.fluxcd.io/v1 Provider | kind | `fluxcd-provider` | strict decode of `ProviderSpec` | `type` must be written, and of an authored `secretRef`, `proxySecretRef` or `certSecretRef` its `name`: the fields the linked Go source marks required, not held to a CRD. `timeout` is held to the pattern of its field: it takes no `h`, and one of an hour or more is written in minutes. It lands in the Flux namespace when one is set. A user or a password in `address` is refused. Under a policy that forbids explicit secrets an `address` that is the credential is refused too: that of `discord`, `generic`, `generic-hmac`, `googlechat`, `lark`, `msteams`, `rocket` and `slack`, and an `azureeventhub` address that holds `SharedAccessKey`. |
| `fluxcd.CreateReceiver` | notification.toolkit.fluxcd.io/v1 Receiver | kind | `fluxcd-receiver` | strict decode of `ReceiverSpec` | `type` and `resources` must be written, of each resource its `kind` and `name`, of an authored `secretRef` its `name`, and of an OIDC provider its `issuerURL` and `validations` with the fields of each validation and variable: the fields the linked Go source marks required, not held to a CRD. A resource's `apiVersion` must be written unless the controller defaults it for the kind. `interval` is held to the pattern of a Flux duration. It lands in the Flux namespace when one is set. No environment policy applies. |
| `fluxcd.CreateResourceSet` | fluxcd.controlplane.io/v1 ResourceSet | held | - | - | Its `resourcesTemplate` is, in the API's words, "a Go template that generates the list of Kubernetes resources to reconcile". The operator renders it on the cluster, so no build sees the objects and none can be held to a rule: the kind would be a way round every rule a policy holds a workload or a Secret to (go-kure/launcher#790). |
| `fluxcd.CreateResourceSetInputProvider` | fluxcd.controlplane.io/v1 ResourceSetInputProvider | kind | `resourcesetinputprovider` | strict decode of `ResourceSetInputProviderSpec` | `type` must be written, of an authored `secretRef` or `certSecretRef` its `name` and of a schedule its `cron`: the fields the linked Go source marks required. A schedule's `window` is held to the pattern of a Flux duration by its authored text; `filter.limit` cannot be authored as 0, nor a schedule's `timeZone` as empty. It lands in the Flux namespace when one is set. A user or a password in `url` is refused, and the host of `url` is held to the allowed registries of the environment policy. |
| `metallb.CreateBFDProfile` | metallb.io/v1beta1 BFDProfile | kind | `metallb-bfdprofile` | strict decode of `BFDProfileSpec` | The object is named after the component unless `objectName` names it, and a MetalLB BGPPeer refers to it by that name. No field is required. Every field is a pointer, so an authored 0 or false is written; the bounds of the numbers are not checked. It is written in the build namespace; MetalLB reads its objects in the one namespace it is configured to watch, by default the one it runs in. No capability is required. No environment policy applies. |
| `metallb.CreateBGPAdvertisement` | metallb.io/v1beta1 BGPAdvertisement | kind | `metallb-bgpadvertisement` | strict decode of `BGPAdvertisementSpec` | The object is named after the component unless `objectName` names it. No field is required: one that authors nothing is the widest advertisement, of every pool, to every peer, for every Service, with no node excluded. A service selector beside an aggregation length other than the API's default is refused, as the CRD's expression rule refuses it. It is written in the build namespace; MetalLB reads its objects in the one namespace it is configured to watch, by default the one it runs in. The communities and the pool and peer names are not read. No capability is required. No environment policy applies. |
| `metallb.CreateBGPPeer` | metallb.io/v1beta2 BGPPeer | kind | `metallb-bgppeer` | strict decode of `BGPPeerSpec` | The object is named after the component unless `objectName` names it, and a MetalLB BGPAdvertisement refers to it by that name. `myASN` must be written. A `peerPort: 0` is refused: the type would leave it out and the API server would fill 179. A `connectTime` is held to the CRD's two expression rules; the CRD's rule against a change of `enableGracefulRestart` is the API server's. A `password` is refused under a policy that forbids explicit secrets; `passwordSecret` names a Secret and holds no secret, and is written as `{}` where it is not authored. It is written in the build namespace; MetalLB reads its objects in the one namespace it is configured to watch, by default the one it runs in. The addresses and the profile's name are not read. No capability is required. |
| `metallb.CreateCommunity` | metallb.io/v1beta1 Community | kind | `metallb-community` | strict decode of `CommunitySpec` | The object is named after the component unless `objectName` names it; a MetalLB BGPAdvertisement refers to an alias by the alias's own `name`, not the object's. No field is required, of the spec or of an alias. The form of a value is not read, and neither is a name defined twice. It is written in the build namespace; MetalLB reads its objects in the one namespace it is configured to watch, by default the one it runs in. No capability is required. No environment policy applies. |
| `metallb.CreateConfigurationState` | metallb.io/v1beta1 ConfigurationState | not authorable | - | - | Status MetalLB writes. |
| `metallb.CreateIPAddressPool` | metallb.io/v1beta1 IPAddressPool | kind | `metallb-ipaddresspool` | strict decode of `IPAddressPoolSpec` | The object is named after the component unless `objectName` names it, and a MetalLB advertisement refers to it by that name or selects it by its labels, the `labels` property. It is written in the build namespace; MetalLB reads its objects in the one namespace it is configured to watch, by default the one it runs in. The addresses are not read. No capability is required. No environment policy applies. |
| `metallb.CreateL2Advertisement` | metallb.io/v1beta1 L2Advertisement | kind | `metallb-l2advertisement` | strict decode of `L2AdvertisementSpec` | The object is named after the component unless `objectName` names it. No field is required: one that authors nothing is the widest advertisement, of every pool, on every interface, for every Service, with no node excluded. It is written in the build namespace; MetalLB reads its objects in the one namespace it is configured to watch, by default the one it runs in. The pool and interface names are not read. No capability is required. No environment policy applies. |
| `metallb.CreateServiceBGPStatus` | metallb.io/v1beta1 ServiceBGPStatus | not authorable | - | - | Status MetalLB writes. |
| `metallb.CreateServiceL2Status` | metallb.io/v1beta1 ServiceL2Status | not authorable | - | - | Status MetalLB writes. |
| `prometheus.CreateAlertmanager` | monitoring.coreos.com/v1 Alertmanager | kind | `alertmanager` | strict decode of `AlertmanagerSpec` | Held to the environment policy as a workload is, for what the spec says of the pods the operator runs: `image`, `replicas`, `resources`, the storage of the claim the operator makes, and `containers`, `initContainers`, `volumes`, `securityContext` and `hostNetwork` as a pod's; the replica count and memory request the operator fills where they are unset are held, not written. `baseImage`, `tag` and `sha` are refused when not empty, and so are a `retention` and a cluster duration of 0 or less. An image the operator chooses, for `alertmanager` or one of its two reloaders, is refused under a policy with allowed registries. `podMetadata` is read for reserved keys and takes no label: the operator's pods carry the component label only where the author writes it there. No capability is required. |
| `prometheus.CreatePodMonitor` | monitoring.coreos.com/v1 PodMonitor | kind | `podmonitor` | strict decode of `PodMonitorSpec` | `selector` must be written, and the three required fields of an endpoint's `oauth2`. No environment policy applies, and no capability is required. |
| `prometheus.CreateProbe` | monitoring.coreos.com/v1 Probe | kind | `prometheus-probe` | strict decode of `ProbeSpec` | `prober.url` must be written, and the three required fields of an `oauth2`. No environment policy applies, and no capability is required. The type name carries a prefix: a probe, in this package, is a container's. |
| `prometheus.CreatePrometheus` | monitoring.coreos.com/v1 Prometheus | kind | `prometheus` | strict decode of `PrometheusSpec` | Held to the environment policy as a workload is, for what the spec says of the pods the operator runs: `image` and `thanos.image`, `replicas` times `shards`, `resources` and `thanos.resources`, the storage a claim template requests, and `containers`, `initContainers`, `volumes`, `securityContext` and `hostNetwork` as a pod's; and the deprecated `bearerToken` of `remoteWrite[]`, `remoteRead[]` and `apiserverConfig` under a policy that forbids explicit secrets. A credential in a header or a URL is not read. `baseImage`, `tag` and `sha` are refused when not empty, and the same three of `thanos` whenever set. The pod count the operator runs, an unset replica or shard count read as 1, is held, not written. An image the operator chooses, for `prometheus`, its two reloaders or, where `thanos` is set, `thanos-sidecar`, is refused under a policy with allowed registries. `podMetadata` is read for reserved keys and takes no label: the operator's pods carry the component label only where the author writes it there. No capability is required. |
| `prometheus.CreatePrometheusRule` | monitoring.coreos.com/v1 PrometheusRule | kind | `prometheusrule` | strict decode of `PrometheusRuleSpec` | A group's `name` and a rule's `expr` must be written. No environment policy applies, and no capability is required. |
| `prometheus.CreateServiceMonitor` | monitoring.coreos.com/v1 ServiceMonitor | kind | `servicemonitor` | strict decode of `ServiceMonitorSpec` | `endpoints` and `selector` must be written, and the three required fields of an endpoint's `oauth2`. No environment policy applies, and no capability is required. |
| `prometheus.CreateThanosRuler` | monitoring.coreos.com/v1 ThanosRuler | kind | `thanosruler` | strict decode of `ThanosRulerSpec`; its `labels` is the `externalLabels` property | Held to the environment policy as a workload is, for what the spec says of the pods the operator runs: `image`, `replicas`, `resources`, the storage of the claim the operator makes, and `containers`, `initContainers`, `volumes` and `securityContext` as a pod's; the replica count and memory request the operator fills where they are unset are held, not written; and the deprecated `remoteWrite[].bearerToken` under a policy that forbids explicit secrets. A credential in a header or a URL is not read. An image the operator chooses, for `thanos-ruler` or its config-reloader, is refused under a policy with allowed registries. `podMetadata` is read for reserved keys and takes no label: the operator's pods carry the component label only where the author writes it there. No capability is required. |
| `volsync.CreateReplicationDestination` | volsync.backube/v1alpha1 ReplicationDestination | kind | `replicationdestination` | strict decode of `ReplicationDestinationSpec` | As `replicationsource`, without a Syncthing mover. Its labels and annotations are the `labels` and `annotations` properties. |
| `volsync.CreateReplicationSource` | volsync.backube/v1alpha1 ReplicationSource | kind | `replicationsource` | strict decode of `ReplicationSourceSpec` | No top-level field must be written; of a volume mounted into a mover that is authored, its `mountPath` and `volumeSource`, of a Syncthing peer its `address`, `ID` and `introducer`, and of a match expression in a label selector of a mover's `moverAffinity` its `key` and `operator`. An authored capacity is held to the policy's storage maximum, a mover's cpu and memory to its maxima, and a mover's `hostProcess` switch is refused unless privileged workloads are allowed. An `rsync` mover is held to the policy's container capabilities for the seven the linked operator version adds to its container. No capability is required. The `volsync` trait builds a ReplicationSource for a workload's claim through the same constructor, from a hand-written parser. Its labels and annotations are the `labels` and `annotations` properties. |

## Parts

The rest of this page is split into parts. Each part holds the full text of its sections and entries.

- [Common config](https://www.gokure.dev/launcher/api-reference/oam-components/common-config/): The properties every workload kind shares: containers, volumes, pod-level and deployment-level properties, and the StatefulSet-level properties.
- [Policy defaults, endpoints and outputs](https://www.gokure.dev/launcher/api-reference/oam-components/policy-and-endpoints/): Policy defaults and their order, refusal classes, the pod view of a monitoring object, declared endpoints and declared outputs.
- [Kinds: core workloads](https://www.gokure.dev/launcher/api-reference/oam-components/kinds-core-workloads/): webservice, worker, deployment, service, the core objects, statefulset, daemonset, cronjob, job, autoscaling and passthrough.
- [Kinds: networking](https://www.gokure.dev/launcher/api-reference/oam-components/kinds-networking/): Ingress, Gateway API, network policies and the Cilium kinds.
- [Kinds: access and admission](https://www.gokure.dev/launcher/api-reference/oam-components/kinds-access-and-admission/): API services, admission webhooks and policies, and the RBAC kinds.
- [Kinds: Flux](https://www.gokure.dev/launcher/api-reference/oam-components/kinds-flux/): The Flux notification and image kinds, the Flux Kustomization and the Flux source kinds.
- [Kinds: Helm](https://www.gokure.dev/launcher/api-reference/oam-components/kinds-helm/): helm, helmchart, helmrelease, helmtemplate and oci.
- [Kinds: monitoring](https://www.gokure.dev/launcher/api-reference/oam-components/kinds-monitoring/): Alertmanager, Thanos ruler, Prometheus and the monitoring objects.
- [Kinds: MetalLB](https://www.gokure.dev/launcher/api-reference/oam-components/kinds-metallb/): The MetalLB kinds.
- [Kinds: PostgreSQL and CloudNativePG](https://www.gokure.dev/launcher/api-reference/oam-components/kinds-postgres/): postgresql and the CloudNativePG kinds.
- [Kinds: secrets, certificates and backup](https://www.gokure.dev/launcher/api-reference/oam-components/kinds-secrets-certs-backup/): Issuers and certificates, the External Secrets kinds and the VolSync kinds.
- [Adding a kind component: where its lines go](https://www.gokure.dev/launcher/api-reference/oam-components/adding-a-kind/): Where the lines of a new kind component go.
- [Upstream fields a hand-parsed kind does not read](https://www.gokure.dev/launcher/api-reference/oam-components/upstream-fields-not-read/): The upstream fields a hand-parsed kind refuses or reads in another shape.
- [Extending](https://www.gokure.dev/launcher/api-reference/oam-components/extending/): How to extend the handler set.
- [The null contract and two spellings of one field](https://www.gokure.dev/launcher/api-reference/oam-components/null-and-spellings/): What an explicit null means and the fields that have two spellings.
- [Conventions](https://www.gokure.dev/launcher/api-reference/oam-components/conventions/): The conventions every kind follows: the app label, object names, metadata, selectors and unauthored required fields.

The headings below stay so that a link to an old section still lands here. Each one links to the part that holds the text. Some also keep the opening lines of the section or of an entry.

## Adding a kind component: where its lines go

See [Adding a kind component: where its lines go](https://www.gokure.dev/launcher/api-reference/oam-components/adding-a-kind/#adding-a-kind-component-where-its-lines-go).

## Common config

`env`, `command`, `args`, `initContainers`, `sidecars` and `affinity` each read
their property with a bare comma-ok type assertion and, when that failed,
returned the zero value with no error. So a property authored with the **wrong
container type** — a mapping where the schema wants a list, a list or scalar
where it wants an object — built cleanly and emitted nothing for the property,
and nothing in the output said it had been seen. Closed in go-kure/launcher#423
by routing those six through presence-reporting helpers (`common.go`), which
report presence separately from value, so each of the six now rejects a
mistyped value by name instead of discarding it. (go-kure/launcher#444 later
folded the dedicated `optionalObject`/`optionalObjectList`/`optionalStringList`
wrappers those six originally used into the base `parseObjectField`/
`parseStringField`/etc. helpers themselves — callers now use the base helpers
directly, and the null-as-absence behavior applies uniformly rather than only
through the wrappers.) Those helpers were already how several other properties on the
same components are read — `updateStrategy` and `ordinals` on `statefulset`
(`statefulset_spec.go`), `successPolicy` and `podFailurePolicy` on `job` and
`cronjob` (`common.go`) — so the six behaved differently from properties an
author writes beside them in the same document. This covers those six parsers;
it is not a claim that every property in this package is read through the
helpers.

Full text: [Common config](https://www.gokure.dev/launcher/api-reference/oam-components/common-config/#common-config).

### Container fields

See [Common config](https://www.gokure.dev/launcher/api-reference/oam-components/common-config/#container-fields).

### Referencing an existing claim (`pvc.claimName`)

See [Common config](https://www.gokure.dev/launcher/api-reference/oam-components/common-config/#referencing-an-existing-claim-pvcclaimname).

### Raw block volumes (`volumeMode: Block`)

See [Common config](https://www.gokure.dev/launcher/api-reference/oam-components/common-config/#raw-block-volumes-volumemode-block).

### Pod-level properties

The seven kind components also share one pod-level property surface
(go-kure/launcher#342, ADR-036 L1), parsed by `parsePodSpec` (`podspec.go`)
straight into a `corev1.PodSpec` and rendered by the shared `buildPodSpec`,
which every kind assigns to its pod template. Property names are the
`corev1.PodSpec` JSON names, except three that carry a `pod` prefix because
the bare name is already a property at some kind: `podSecurityContext`
(container `securityContext` exists everywhere), `podResources` (container
`resources`), and `podActiveDeadlineSeconds` (cronjob's job-level
`activeDeadlineSeconds`). Each accepted value is validated the way real
admission validates it when that check is deterministic from the document
alone (DNS-1123 names, enums, IP literals, duplicate names, the cross-field
exclusions listed below, and the `os.name` contract: a `windows` pod may not
set the Linux-only pod and container fields, a `linux` pod may not set
`windowsOptions`); checks needing cluster state (host ports under
`hostNetwork`, RuntimeClass existence) are left to the cluster, and so are feature
gates, with one exception: a `ClusterProfile` that states `spec.kubernetesVersion` has
a property whose gate is off by default at that release refused (go-kure/launcher#1183):
`hostnameOverride` below 1.35 (`HostnameOverride`) and `podResources` below 1.34
(`PodLevelResources`), and a container's `env[].valueFrom.fileKeyRef` below 1.35
(`EnvFiles`) and an init container's `restartPolicyRules` below 1.35
(`ContainerRestartRules`), on the kinds whose schema declares them. The gate and the
release are in the property's published schema (`gate`). With no version stated nothing
is checked. The `Never` and `OnFailure` values of an init container's `restartPolicy`
are refused below 1.35 by the same gate, and `service`'s `trafficDistribution` values
`PreferSameZone` and `PreferSameNode` below 1.34 (`PreferSameTrafficDistribution`); the
other values are accepted at every version, and the gate is named per value in the
schema (`valueGates`, go-kure/launcher#1230). The raw-PodSpec kinds (`pod` and the kinds that take a pod
template: `podtemplate`, `replicaset`, `replicationcontroller`) leave their containers and
template spec open, as does the `template` of `cnpg-pooler` (go-kure/launcher#1321), and the check reads declared schema, so their open nodes name the
gated paths in `gatedPaths` instead: `env[].valueFrom.fileKeyRef` and `restartPolicyRules` of `containers[]` and
`initContainers[]`, the `restartPolicy` of `containers[]` (Kubernetes forbids the field on a regular
container with the gate off, `Always` included), and `hostnameOverride` and
`resources` of the pod spec (`template.spec.*` on a pod template and on the `cnpg-pooler` template). A path is refused the
same way, naming the authored path (go-kure/launcher#1245). Field names match
case-insensitively, as in the decode, so a key spelled in another case is refused too and
is named as written; property validation does not read `gatedPaths`, so what a document is
accepted as does not change. A path that gates a value, not the field, sits in
`valueGatedPaths`: a map from a gate-relative path to a map from a value to its gate. The
`restartPolicy` of `initContainers[]` is there, so `Never` and `OnFailure` are refused
below 1.35 on these kinds as on the workload kinds, and `Always` is accepted at every
version. The match is exact and only a string is looked up, so a null, a number or a
string that the table does not name is not refused. The workload kinds and the raw-PodSpec
kinds share one table, so the two cannot differ. The refusal names the authored path and
the value (go-kure/launcher#1330). This check does not read whether a value such as
`never` reaches a cluster. It only refuses the values the table names.

Full text: [Common config](https://www.gokure.dev/launcher/api-reference/oam-components/common-config/#pod-level-properties).

### Deployment-level properties (`deployment`, `webservice`, `worker`)

See [Common config](https://www.gokure.dev/launcher/api-reference/oam-components/common-config/#deployment-level-properties-deployment-webservice-worker).

#### Main container ports

See [Common config](https://www.gokure.dev/launcher/api-reference/oam-components/common-config/#main-container-ports).

#### Raw scheduling properties

See [Common config](https://www.gokure.dev/launcher/api-reference/oam-components/common-config/#raw-scheduling-properties).

##### What `tolerations` changed for `daemonset`

See [Common config](https://www.gokure.dev/launcher/api-reference/oam-components/common-config/#what-tolerations-changed-for-daemonset).

## Policy defaults & enforcement ordering

A container's effective cpu/memory requests and limits are assembled in three
tiers, in precedence order:

1. **Authored** — whatever the component's `resources` property set explicitly.
   The string `unlimited` under `limits.cpu` or `limits.memory` is an authored
   opt-out (go-kure/launcher#1181): the container gets no limit for that
   resource, because neither a policy default limit (tier 2) nor, for memory,
   the limit mirror (tier 3) is applied to it. It is accepted where a built-in
   default exists to opt out of: the main container of the five controller
   kinds, `webservice` and `worker`, and their `initContainers` and `sidecars`
   entries. It is refused by name under `requests`, under `podResources` and on
   `postgresql` ("no intrinsic default to opt out of"), under any resource name
   other than cpu and memory, and, with the `resource-maximum` refusal class,
   when the policy sets `MaxCPU()`/`MaxMemory()` for that resource: an absent
   limit is no bound, so it is never read as under the maximum. `null` is still
   absence, not an opt-out. A policy sub-option to accept `unlimited` as a
   policy default is not provided.

Full text: [Policy defaults, endpoints and outputs](https://www.gokure.dev/launcher/api-reference/oam-components/policy-and-endpoints/#policy-defaults--enforcement-ordering).

## Policy refusal classes

See [Policy defaults, endpoints and outputs](https://www.gokure.dev/launcher/api-reference/oam-components/policy-and-endpoints/#policy-refusal-classes).

## Pod view of a monitoring workload object (`MonitoringPodSpec`)

See [Policy defaults, endpoints and outputs](https://www.gokure.dev/launcher/api-reference/oam-components/policy-and-endpoints/#pod-view-of-a-monitoring-workload-object-monitoringpodspec).

## Declared endpoints of a chart

See [Policy defaults, endpoints and outputs](https://www.gokure.dev/launcher/api-reference/oam-components/policy-and-endpoints/#declared-endpoints-of-a-chart).

## Declared outputs

See [Policy defaults, endpoints and outputs](https://www.gokure.dev/launcher/api-reference/oam-components/policy-and-endpoints/#declared-outputs).

## Per-type highlights

Wrong-type handling for the optional top-level properties go-kure/launcher#405
moved onto the presence-reporting helpers: `port` on `webservice`, `daemonset`
and `statefulset` (the latter two have since dropped it, go-kure/launcher#690;
`parseInt32Field`; on `webservice` the `Endpoints` declaration runs the
rule's whole parse, so a wrong type is not declared as an endpoint on port 80
either, and neither is a component without an `image`; what the rule's
lowering refuses after the parse is one of the four things an endpoint entry
does not see, `pkg/oam`, "Name roles and the `Naming` hook"), `topologySpread` on `webservice` and
`worker` and `prune` on `oci` (`parseBoolField`), `serviceName` on
`statefulset` and `path`, `interval` and `targetNamespace` on `oci`
(`parseStringField`, so an authored `""` still reads as absent), and
`restartPolicy` on `cronjob` (read raw, as `job` reads it, so `""` still
reaches the enum refusal). A present value of the wrong type is rejected by
name (`topologySpread: must be a boolean, got string`) and a null reads as
absence. Before, each fell back to its default as though the key had been
omitted — `topologySpread: "false"` kept the spread constraints and
`prune: "false"` pruned — for a handler called directly; schema validation
already refused these shapes in an authored document. One authored shape does
change: `port` is declared `integer` with no maximum, so an integer outside the
int32 range (`port: 5000000000`) passed validation and built as though no port
were authored (80 on `webservice`, none on `daemonset`/`statefulset`); it is
now refused by value, not by type (`port: must be an integer within int32
range, got 5000000000`). `parseInt32Field` words that case apart from a wrong
type for every field it reads, so an out-of-range `replicas` or
`minReadySeconds` gets the same range message. `postgresql`'s other top-level
reads (`storageSize`, `backup`, `pooler`, …) were converted separately, in
go-kure/launcher#512 (see the `postgresql` entry below).

Opening lines of an entry:

- **cnpg-imagecatalog**, **cnpg-clusterimagecatalog**, **cnpg-backup**,
  **cnpg-scheduledbackup**, **cnpg-databaserole**, **cnpg-publication**,
  **cnpg-subscription** (go-kure/launcher#790) are the kind-named projections
  of seven objects of the CloudNativePG API, `postgresql.cnpg.io/v1`, beyond
  those of **cnpg-cluster** and **cnpg-pooler**, below: an
  ImageCatalog, a ClusterImageCatalog, a Backup, a ScheduledBackup, a
  DatabaseRole, a Publication and a Subscription. Each emits that one object,
  named after the component unless `objectName` names it; the handler adds no
  label, no annotation and no default of its own. **The ClusterImageCatalog
  is cluster-scoped**: the object carries no namespace, whatever namespace the
  application is built for, and its name is claimed cluster-wide. The other
  six are namespaced and are written in the build namespace. One rule lowers
  onto one of them: `postgresql` emits a `cnpg-scheduledbackup` for each
  `backup.schedules` entry (go-kure/launcher#1001, below); it emits none of
  the other six.

Full entry: [Kinds: PostgreSQL and CloudNativePG](https://www.gokure.dev/launcher/api-reference/oam-components/kinds-postgres/).

Opening lines of an entry:

- **alertmanager** (go-kure/launcher#790) is the kind-named projection of an
  Alertmanager of the Prometheus operator's `monitoring.coreos.com/v1` API. It
  is built on `policyHeldKind` (`policyFreeKind` with a policy check, see
  **storageclass**, below) and emits that one object in the build namespace,
  named after the component unless `objectName` names it; the handler adds no
  label, no annotation and no default, and declares the object as namespaced. Launcher
  emits no pod, no StatefulSet, no Service and no Secret for it: the operator
  builds a StatefulSet from the object and runs the pods. What the spec says
  of those pods is held to the environment policy as a workload kind's own
  fields are.

Full entry: [Kinds: monitoring](https://www.gokure.dev/launcher/api-reference/oam-components/kinds-monitoring/).

Opening lines of an entry:

- **webservice / worker** — `image`, `replicas` (default 1), `port` (webservice),
  plus the full `DeploymentSpec`-level surface they share with `deployment` —
  `strategy`, `minReadySeconds`, `revisionHistoryLimit`, `paused` and
  `progressDeadlineSeconds` (go-kure/launcher#341, see "Deployment-level
  properties" above). What still separates them from `deployment` is launcher's
  own opinions, which these two keep: `topologySpread` and the four-key
  `affinity` shorthand, and on `webservice` the `port` that drives the Service.
  Note the shorthand is what they keep — neither publishes the raw `corev1`
  `affinity` that `deployment` does. Both publish `tolerations` and
  `topologySpreadConstraints` as `deployment` does (go-kure/launcher#1473, see
  "Raw scheduling properties" above); authored constraints need
  `topologySpread: false`.
  The `webservice` rule implements the optional `oam.EndpointProvider`: it declares its own
  pods (`app: <component-name>`) on the declared `port` (its single `port` property drives both
  the container port and the Service port), letting a downstream platform synthesize generic
  app→app connections targeting a webservice. `worker` declares no in-cluster port and emits no
  Service, so it deliberately advertises no endpoint (not an `EndpointProvider`).

Full entry: [Kinds: core workloads](https://www.gokure.dev/launcher/api-reference/oam-components/kinds-core-workloads/).

Opening lines of an entry:

- **ingress** (go-kure/launcher#790) is the kind-named projection of a
  networking.k8s.io/v1 Ingress, on the recipe of `resourcequota`: one
  schema key per json field of `networkingv1.IngressSpec` (`ingressClassName`,
  `defaultBackend`, `tls`, `rules`), the property map decoded strictly into
  that type under the null contract, two spellings of one field refused, and
  `TestCoreKindSchemas_CoverSpec` holding the schema to the linked type. It
  emits one Ingress named after the component in the build namespace, with the
  authored spec and nothing else. No field is required by the decode and none
  is filled: an Ingress with neither a `defaultBackend` nor a rule, a path
  without a `pathType` and the API's other value rules are left to the API
  server. A null list element (`rules: [null]`, a null path or TLS entry) is
  refused by its path.

Full entry: [Kinds: networking](https://www.gokure.dev/launcher/api-reference/oam-components/kinds-networking/).

Opening lines of an entry:

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

Full entry: [Kinds: secrets, certificates and backup](https://www.gokure.dev/launcher/api-reference/oam-components/kinds-secrets-certs-backup/).

Opening lines of an entry:

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

Full entry: [Kinds: Helm](https://www.gokure.dev/launcher/api-reference/oam-components/kinds-helm/).

Opening lines of an entry:

- **postgresql** — `provider: cnpg`, `version` (default `16`), `storageSize`
  (precedence: authored > policy default `storageSize` > `1Gi`), `replicas`,
  `backup` (`objectStore`, `objectStoreObjectName`, `retentionPolicy`,
  `serverName`, `schedules`; see "Backup" below), `monitoring.enabled`,
  `pooler.enabled`, `poolerName`, `managedRoles`, `databases` (each with an
  optional `objectName`), `clusterObjectName`.
  **`clusterObjectName` names the Cluster in place of the component name.
  CloudNativePG derives the Cluster's Services and Secrets from the Cluster's
  name, the default backup path moves with it, and renaming an existing
  Cluster creates a new one: the old one is pruned with its data unless it is
  protected** (see "The Cluster's and the ObjectStore's names" below).
  **A component lowering rule** (`PostgresqlRule`, `postgresql_lowering.go`,
  go-kure/launcher#281), not a dispatchable handler: it reads the properties
  as before (`Parse`; the published schema is pinned byte for byte,
  `testdata/postgresql-property-schema.json`: the former handler's, with
  `poolerName` and `clusterObjectName` added and, since go-kure/launcher#1001,
  `backup` typed and the top-level `objectStore` and `objectStoreObjectName`
  removed, the store's name now `backup.objectStoreObjectName`) and emits
  the CNPG kind components that build the same objects — a `cnpg-cluster`
  under the component's name, a `cnpg-objectstore` under the same name (one
  same-name sibling group) when `backup` is set, a `cnpg-pooler`
  `<name>-pooler` when `pooler.enabled`, a `cnpg-database` `<name>-<db>`
  per `databases` entry (a Database given the Pooler's name, by default the
  one named `pooler` beside an enabled pooler, joins its same-name sibling
  group, as two objects of different kinds), and a `cnpg-scheduledbackup`
  `<name>-<schedule>` per `backup.schedules` entry, in that order. `replicas` and `storageSize` are written to the
  Cluster only when authored, so the policy applies to them exactly as
  before. The two values postgresql derived from the policy are set after
  it by a post-policy step the rule attaches to the Cluster
  (`oam.Component.AfterPolicy`), which runs before the authored traits:
  `enablePDB` (`instances > 1`) and the `1Gi` storage fallback, under the
  policy maximum with postgresql's text (`storageSize "1Gi" exceeds enforced
  maximum "512Mi"`). Authored traits go to the Cluster, and those that
  covered every object postgresql generated are forwarded to the other
  members so they still do: `prune-protection` and `force-replace` to each
  member, and `fluxcd-patches` and `fluxcd-postbuild` (not built in; a consumer
  that delivers through Flux registers them) to the first member that is
  ordered after the Cluster (below): the members share one group, so one of
  them carries the trait for that group's bundle. For that reason a document
  that authors one of these two traits on the postgresql component may not
  order or place a member on its own (a `dependency` rule or a `placement`
  whose component is the Pooler's, a Database's or a ScheduledBackup's
  generated name): the
  member could leave the group the trait reaches, so the rule refuses it and
  asks for the postgresql component's name instead (the rule for every
  lowering rule, and the message, are under "Traits a lowering rule
  forwards"). Policies that name the
  postgresql component are extended to the members the same way. A `placement` of it is repeated
  for each member, so they stay in the Cluster's tier and, without a
  dependency policy, in its bundle. When the document orders its components
  with a `dependency` policy that has a rule, the rule adds one making the
  Pooler, the Databases and the ScheduledBackups depend on the Cluster, and every component the
  document makes depend on the postgresql component depend on them too:
  the members are then in a later group than the Cluster's. Without one it adds
  no edge, since a dependency edge would order a document that never asked
  for ordering. The policies it adds are named `<name>-dependencies` and
  `<name>-placement-<i>` (one per member), or, when a policy of the document
  already uses the name, the first free one with `-<n>` appended.
  **Behavior-changing** under `launcher.gokure.dev/v1alpha1`
  (go-kure/launcher#281): with both `objectStore` (now `backup`) and `pooler`, the objects
  now come in the order Cluster, ObjectStore, Pooler (was Cluster, Pooler,
  ObjectStore; the objects are the same), and a Database named `pooler`
  comes right after the Pooler, ahead of the other Databases. Under a
  `dependency` policy the Pooler, the Databases and the ScheduledBackups
  are deployed from one bundle they share, after the Cluster's, unless the document orders
  or places a generated member on its own (was one bundle for all of
  postgresql's objects). A Pooler, Database or ScheduledBackup name,
  generated or chosen, that is already the name of another component in the
  document is refused, naming both. A database name repeated in
  `databases` is refused, naming both entries: each entry is one Database
  object, so the Flux build already failed on it, and the plain manifest
  output carried duplicate Database documents (kubectl kept the last). The Cluster kind's policy checks now
  apply, under its field names: the registry allowlist on the image
  (`imageName`, derived or authored), and `instances`/`storage.size` in the
  policy messages. Refusals the API server or the operator gave at apply
  now come at build: a resource request above its limit, a database named
  `postgres`, `template0`, `template1` or not a DNS-1123 subdomain, and an
  authored `storageSize: ""` (`storageSize: must not be empty; omit it to
  take the policy default or 1Gi`), which built a Cluster CloudNativePG's
  webhook refuses ("Size not configured").
  The component name becomes the Cluster's name and its `cnpg.io/cluster`
  endpoint selector unless the Cluster is named apart (`clusterObjectName`
  or the `Naming` hook, below), so it carries cnpg-cluster's name rule: a DNS-1035
  label (no leading digit, no dot) of at most 50 characters. Any other name
  is refused when the rule lowers it and when endpoints are collected
  (`postgresql name "db.main": must be a DNS-1035 label of at most 50
  characters …`). The cap also keeps both endpoint selector values, the
  name and the pooler's default `<name>-pooler`, within the 63-character label-value
  limit (a `poolerName` or a hook-given name is held to it as a DNS-1123
  label, and so is the default where the Cluster is named apart and the
  component name is not bounded by the Cluster's rule). **Behavior-changing** under `launcher.gokure.dev/v1alpha1`:
  such a name used to build and was then refused by CloudNativePG at apply,
  or, over 63 characters (56 with the pooler), gave a network-policy
  selector the API server refuses; it is now refused at build
  (go-kure/launcher#615, go-kure/launcher#589).
  An authored `version: ""` is refused by name (`version: must not be empty;
  omit it to default to "16"`), with or without `imageName`: it used to be
  kept as a value, so without `imageName` the cluster image was
  `ghcr.io/cloudnative-pg/postgresql:` — an empty tag that only the image
  pull rejected (go-kure/launcher#539). Only an omitted or null `version`
  takes the default. `storageSize: ""` is refused (above).
  The image is held to the tag rule (`ValidateImageRef`), with or without a
  policy (**breaking**, go-kure/launcher#790). Without `imageName` the image
  is `ghcr.io/cloudnative-pg/postgresql:<version>`, so a `version` that makes
  a `:latest` reference, or none that parses, is refused under the property
  the author wrote, with the image it made: `version: "latest" is refused as
  the tag of the default image: image
  "ghcr.io/cloudnative-pg/postgresql:latest" rejected: :latest tag not
  allowed; use an explicit version tag or digest`. An authored `imageName`
  is refused under `imageName`; `version` is read for the default image only
  and is not checked beside one. The default `version` builds.
  `pooler.image` is the PgBouncer image, written to the Pooler's
  `pgbouncer.image` and held to the tag rule under its own name
  (`pooler.image: image "…" rejected: …`) and, by the `cnpg-pooler` the pooler
  lowers to, to the registry allowlist. Like the other pooler settings it
  needs `pooler.enabled`. Without it the CloudNativePG operator chooses the
  PgBouncer image, so under a non-empty allowlist an enabled pooler without
  `pooler.image` is refused (**breaking**, go-kure/launcher#790: it built
  before), reported under the Pooler's component name:
  `pgbouncer.image (pooler.image on a postgresql component): unset, so the
  CloudNativePG operator chooses the image the pods run, which the allowed
  registries [...] cannot hold; name an image from one of them`. Set
  `pooler.image` to an image from the list. With no allowlist nothing
  changes.
  Any other storage size the Cluster would carry, authored or from the
  policy default, must parse and be positive (`storageSize: quantity must be
  positive, got "0"`): CloudNativePG's webhook parses only the size, so a
  zero or negative one was admitted and its claims failed the API server's
  positive storage-request check. A policy storage-size default is checked
  when it is applied (`policy default for storage.size: invalid quantity
  "lots"`), as the cpu/memory defaults are. The instance count must be at
  least 1, the CRD's minimum: a replicas policy default of 0 is refused
  (`instances: must be >= 1, got 0 from the policy default`), and so is an
  authored `replicas: 0` when the rule lowers it (`replicas: must be >= 1,
  got 0`).
  **Behavior-changing** under `launcher.gokure.dev/v1alpha1`: an authored
  `replicas: 0` or a non-positive `storageSize` used to build and was then
  refused at apply; it is now refused at build (go-kure/launcher#623).
  `resources` forwards every name the shared parser admits — `cpu`, `memory`,
  `ephemeral-storage`, `hugepages-<size>` and qualified extended resources
  such as `nvidia.com/gpu`, under the same validation as the other seven
  workload kinds — onto the Cluster's `spec.resources` `requests`/`limits`
  unchanged (go-kure/launcher#484; until then any name other than
  `cpu`/`memory` was rejected, so this is an additive change). Only
  `cpu`/`memory` carry a policy default and maximum; CNPG-specific sizing
  such as `ephemeralVolumesSizeLimit` is not derived from these entries.
  CNPG copies this block onto the instance pods unchanged and nothing
  defaults cpu/memory into it except a policy, so generation refuses a block
  that names `hugepages-<size>` without `cpu` or `memory` on either side
  after policy defaults (`resources: hugepages require cpu or memory in
  requests or limits`) — admission would refuse every instance pod.
  `affinity` takes the same four keys as the shared `affinity` property
  (`enablePodAntiAffinity`, `topologyKey`, `podAntiAffinityType`,
  `nodeSelector`) with the same defaults — `kubernetes.io/hostname` and
  `preferred` — but the handler parses it itself into the CNPG
  `AffinityConfiguration` instead of calling `parseAffinity`, because CNPG
  carries its own affinity shape rather than a `corev1.Affinity`. The block
  and each of its four sub-fields are read with the presence-reporting
  helpers, so a value authored with the wrong type is rejected by name
  (`affinity.topologyKey: must be a string, got float64`) rather than
  discarded while a default reaches the emitted cluster, and
  `podAntiAffinityType` must be `preferred` or `required` — an explicit empty
  string is an error here, not a fallback to the default
  (go-kure/launcher#448). Rejection reaches the `nodeSelector` **values**, not
  just its envelope: `nodeSelector: {rack: 3}` is refused by key rather than
  dropped, so a selector cannot silently reach the cluster narrower than
  authored. When more than one entry is wrong, the **alphabetically first**
  offending key is the one reported: Go randomises map iteration order, so
  without an explicit sort the same document names a different key run to run
  and its diagnostic cannot be reproduced or asserted in a test. The same
  ordering rule applies to the other three parsers in this package that report
  a bad map entry by key — `parseResourceList`, `rejectUnknownKeys` and
  `parseManifestSource`. An explicit **null** `affinity` is **absence** —
  Launcher supplies no explicit affinity settings, exactly as when the
  property is omitted — matching what `pkg/oam`'s own property validation
  already does with a null under an optional property; without that,
  `affinity:` with no value validated against the published schema and then
  failed to convert, while the same value built in Go turned pod
  anti-affinity on with every default. A null on a sub-field *inside* a
  present block is also absence — it takes the same default an omitted
  sub-field takes (go-kure/launcher#444 established this nested-null contract
  in the shared presence primitive `authoredValue`, which this block's four
  sub-fields go through; a nested key read raw elsewhere, such as a probe
  handler key, does not follow it); a non-null wrong-typed sub-field, and an
  explicitly empty `podAntiAffinityType`, remain errors.
  The shared `parseAffinity` (`common.go`) applies the same rules to its
  four sub-fields since go-kure/launcher#452, so the two readers agree; a
  change to one belongs in the other.
  The handler's other string→string maps — `inheritedMetadata.labels`,
  `inheritedMetadata.annotations`, `postgresql.parameters`,
  `pooler.parameters` and `externalClusters[].connectionParameters` — take
  string values only: a non-string value (`tier: 3`,
  `max_connections: 100`, `enabled: true`) is refused by field and key
  (`postgresql.parameters["max_connections"]: must be a string, got int`)
  instead of being dropped from the emitted resources, with the same
  alphabetically-first rule as `nodeSelector` when several are wrong
  (go-kure/launcher#466). Quote such a value to keep it
  (`max_connections: "100"`), and declare a Package parameter substituted as
  a whole value there as `type: string`, since whole-value substitution keeps
  the parameter's type; a number is not converted to its string form,
  because YAML's rendering of a number is not always the text the author
  typed (`1.10` decodes to `1.1`). An explicit null value is absence, per
  "The null contract" below: the key is left out, not refused. The same
  holds for `nodeSelector` values in both affinity readers, which share the
  helper.
  **Backup** (go-kure/launcher#1001). `backup` takes backups and archives WAL
  to an S3-compatible object store through the Barman Cloud plugin, the one
  path CloudNativePG keeps once its in-tree Barman support is removed. It
  lowers into the base kinds and adds nothing to them:
  - `backup.objectStore` (required) is the configuration of a barman-cloud
    `ObjectStore` the component emits, named after the component (or by
    `backup.objectStoreObjectName` or the `Naming` hook, below). It reads
    `destinationPath` (required, not empty), `endpointURL`, `endpointCA`
    (`{name, key}` of a Secret), `s3Credentials` (required: `accessKeyId` and
    `secretAccessKey`, each `{name, key}` of a Secret, with an optional
    `region` and `sessionToken` alike, or `inheritFromIAMRole: true`; exactly
    one of the two ways), `wal` (`compression`, `encryption`, `maxParallel`)
    and `data` (`compression`, `encryption`, `jobs`, `immediateCheckpoint`).
    It is decoded strictly into the vendored Barman Cloud type, and the
    values the ObjectStore CRD bounds are checked at build, since the Go type
    alone does not hold them: a `compression` or `encryption` outside the
    CRD's enum (`data` takes no `xz` or `zstd`), and a `maxParallel` or `jobs`
    under 1, an authored `0` included (`backup.objectStore.wal.compression
    "brotli": must be one of …`). A Secret reference without its `name` or
    `key` is refused. `serverName` there is refused
    (the ObjectStore CRD forbids it; use `backup.serverName`), and so are
    `azureCredentials` and `googleCredentials` (S3-compatible storage only; the
    `cnpg-objectstore` kind takes them), with that reason also where no
    `s3Credentials` is written: the schema does not mark the store's required
    keys Required, so the document check gives a refused key's reason first.
  - `backup.retentionPolicy` is the ObjectStore's, a positive count of days,
    weeks or months (`30d`, `4w`, `6m`); anything else is refused
    (`backup.retentionPolicy "30 days": must be a positive count …`).
  - The Cluster gets one plugin entry, `barman-cloud.cloudnative-pg.io` with
    `isWALArchiver: true`, whose `barmanObjectName` parameter names the store
    by the name it took. `backup.serverName` becomes its `serverName`
    parameter, the folder the plugin writes under (it defaults to the Cluster
    name). The Cluster has no in-tree `spec.backup`.
  - Each `backup.schedules` entry emits a `cnpg-scheduledbackup` of the
    Cluster, `method: plugin` through the same plugin (no parameters: the
    plugin reads the store from the Cluster's entry). `name` (required,
    unique among the schedules) makes its name, `<component>-<name>`;
    `schedule` (required) is a cron expression of six fields, seconds first
    (`0 0 3 * * *`), refused unless it parses as CloudNativePG's webhook parses
    it (`robfig/cron` at the version the vendored CloudNativePG requires).
    Five fields parse there too, seconds first, with only a warning at the
    webhook that Flux does not show: a crontab line `0 3 * * *` would back up
    at minute 3 of every hour. It is refused, naming each six-field form that
    parses, read as a crontab line and as the operator reads it
    (`backup.schedules[0].schedule "0 3 * * *": five fields, … write six
    fields, seconds first ("0 0 3 * * *" as a crontab line, or "0 3 * * * *"
    as the operator reads it)`). A descriptor (`@daily`) is accepted.
    A schedule must also find a time within the operator's look-ahead from
    every instant (go-kure/launcher#1032): the operator asks for the next time
    after now, searching through the end of the year five years on, and on
    none records NoSchedule and takes no further backup for that
    ScheduledBackup, not even the immediate one, with no retry. The search is
    computed in UTC, exactly, over one 400-year Gregorian cycle (fixed years,
    so a build does not depend on the clock): February the 30th
    (`0 0 3 30 2 *`) is refused as a time that never comes, and February the
    29th (`0 0 3 29 2 *`) is refused too, since 2100 is no leap year
    (`… no time from the end of 2096 through the end of 2101 (UTC), longer
    than the five years the operator looks ahead …`). A seconds, minutes,
    hours or months field that selects no value (`,`) is refused outright,
    naming the field. An empty day of month or day of week can still fire:
    `0 0 3 , * MON` (ORed with Mondays) is accepted, `0 0 0 , * *` (ANDed with
    a starred day of week) is refused. `@every` is accepted, however long its
    delay; how often a backup is taken is not judged.
    `immediate` defaults to `true` (a new Cluster gets a base backup at once),
    `backupOwnerReference` to `self` (`none`, `self` or `cluster`), `target`
    is `primary` or `prefer-standby` and `suspend` is written only when
    authored.

  **Prerequisites, and what happens at apply.** `backup` needs, in the
  cluster: the CloudNativePG operator 1.26 or later (the emitted objects follow
  the vendored 1.30.1 API), the Barman Cloud plugin v0.15.x with its
  `ObjectStore` CRD, and cert-manager, which the plugin's install recommends.
  The bucket, and the Secrets `s3Credentials` and `endpointCA` name, must exist
  beforehand: launcher neither creates nor checks them. The build cannot see
  the cluster: a missing CRD fails the apply, while an older CRD can prune a
  field it does not know without an error. Check the installed versions, and
  read the applied object back. The backup and recovery guide
  (`docs/postgresql-backup-recovery.md`) walks through both, with the checks
  that show a backup and a recovery working. The rule's contract metadata
  carries the same list for a tool to read (`ContractMetadata.Prerequisites`,
  go-kure/launcher#1027; the oam package README, "Contract metadata"), each
  entry with the use that needs it, plus the ObjectStore a recovery's
  `source.objectStoreName` names and the Secret its `secret.name` names, with
  the owner's credentials. cert-manager is not an entry: what launcher writes
  does not depend on it, as the plugin's TLS certificates can come from
  elsewhere.
  **Behavior-changing**: the former `backup` keys `destinationPath`,
  `endpointURL` and `secretName`, which built the Cluster's in-tree
  `barmanObjectStore`, and the top-level `objectStore` and
  `objectStoreObjectName` are refused, whatever their value, naming where each
  went (`objectStore: moved to backup.objectStore (…)`,
  `backup.secretName: removed; use backup.objectStore.s3Credentials (…)`),
  by the document check and by the lowering alike. `backup.retentionPolicy`
  stays, and now sets the ObjectStore's retention. `backup: {}` is refused
  too (`backup.objectStore: required (…)`). For a library consumer that
  registers handlers itself: the rule now also lowers into
  `cnpg-scheduledbackup`, so a registry with `PostgresqlRule` and without a
  `cnpg-scheduledbackup` handler fails every transform at `Seal`, whatever the
  document (`registry incomplete: … lowers into component type
  "cnpg-scheduledbackup", which is not registered`); the built-in set registers it.
  Until go-kure/launcher#643 the plugin entry named a plugin CloudNativePG does not
  register (`barman-cloud.barmancloud.cnpg.io`) under a key the plugin does
  not read (`objectStoreName`), and `serverName` went on the ObjectStore,
  which the API server refused.
  Every other optional property the handler reads goes through the same
  presence-reporting helpers (go-kure/launcher#512): `provider`, `version`,
  `storageSize`, `imageName`, the `backup`, `monitoring`, `pooler`,
  `bootstrap` and `replication.synchronous` blocks and their
  fields, and each entry of `managedRoles`, `databases` (with its
  `extensions`), `externalClusters` and `monitoring.customQueries`. A value of
  the wrong type is refused by its path
  (`managedRoles[0].login: must be a boolean, got string`,
  `databases[0].extensions[1]: must be an object, got string`) where it used
  to be dropped, so `pooler.enabled: "true"` no longer builds a cluster with
  no pooler and `backup: "s3"` no longer builds one with no backup; an
  explicit null is absence, typed or untyped, so a typed-nil `storageSize`
  takes the policy default and a typed-nil `pooler.instances` or
  `replication.synchronous.number` takes the handler default rather than
  being refused as a bad number. Two further shapes that used to vanish are now
  errors: a non-string `managedRoles[].inRoles` entry, and an
  `externalClusters` entry without a `name`. So is a managed role's
  `connectionLimit: 0` (go-kure/launcher#659), as `cnpg-cluster` refuses it:
  CloudNativePG omits a zero `connectionLimit` from the Cluster and applies its
  default `-1`, so the role would deploy with no limit. A role that must not
  connect sets `login: false` instead. The lowering repeats the refusal for a
  `PostgresqlConfig` built directly rather than parsed. `Endpoints` runs the
  rule's lowering on the component, so what the lowering refuses of it has no
  endpoint, in the same words. What a kind it lowers into refuses of a member
  (a database named `postgres`) is not seen there: one of the four things an
  endpoint entry does not see (`pkg/oam`, "Name roles and the `Naming`
  hook").
  **Recovery** (go-kure/launcher#1001). `recovery` bootstraps a new Cluster
  from the base backups and WAL another cluster wrote to an object store
  through the Barman Cloud plugin, to the end of the WAL or to a target
  (point-in-time recovery). It lowers into the base kinds and adds nothing to
  them. A bootstrap is read only when the Cluster is created: recover into a
  new component beside the source one; set on a component whose Cluster
  exists already, it changes nothing. Recovering by renaming the source
  component's Cluster instead (`clusterObjectName`) creates a new Cluster and
  prunes the original with its data unless it is protected. It is not
  combined with
  `bootstrap.pg_basebackup` (`recovery: not with bootstrap.pg_basebackup …`)
  or `bootstrap.initdb` (`recovery: not with bootstrap.initdb …`).
  - `recovery.source` (required) is where the backups are read from:
    `serverName` (required, not empty), the folder the source cluster wrote
    within the store (its `backup.serverName`, else its Cluster's name), and
    exactly one of `objectStoreName`, an existing ObjectStore in the namespace,
    and `objectStore`, one defined here. The inline store reads as
    `backup.objectStore` does, with the same checks and refusals under its own
    path (`recovery.source.objectStore.serverName: not allowed …; use
    recovery.source.serverName`), and the component emits it as a
    `cnpg-objectstore` named `<component>-recovery-source` (or by the `Naming`
    hook, below).
  - The Cluster gets `bootstrap.recovery` whose `source` is one
    `externalClusters` entry the rule writes, `<component>-recovery-source`:
    the plugin `barman-cloud.cloudnative-pg.io` with the parameters
    `barmanObjectName`, the store by the name it took, and `serverName`, always
    written, since the plugin would otherwise read the new Cluster's own folder.
    An authored `externalClusters` entry of that name is refused.
  - `recovery.target` is where the recovery stops, the vendored
    `RecoveryTarget`: `time`, `lsn`, `xid`, `name` and `immediate`, at most one
    of them; `backupID`; `timeline` (`latest` or a positive integer, written as
    a string, and emitted in plain decimal: the plugin matches it against the
    backups' timelines as text, so `"01"` is written `"1"`); `exclusive`. What CloudNativePG's webhook refuses without
    looking at a cluster is refused at build: two targets (an authored
    `immediate`, `false` included, counts), a `time` or `lsn` that does not
    parse as the operator parses it, an `xid` that is no 32-bit integer, a
    `name` of 64 bytes or more or with a control character, an `xid`, `name`
    or `immediate` without `backupID` (the plugin finds the base backup from
    the catalog by time or LSN only), and a `timeline` that is neither. An
    authored `""` in a target, `database` or `owner` is refused: the Cluster
    omits an empty value, which would recover to the end of the WAL, or take
    the operator's default.
  - `recovery.database`, `recovery.owner` and `recovery.secret.name` (the
    owner's credentials) are the Cluster's, written only when authored.
  - A cluster that would archive its WAL into the folder it is recovered from
    is refused where launcher sees both (`recovery.source: the cluster would
    archive its WAL into the folder it is recovered from …`): the endpoint,
    the destination path (one trailing `/` aside, as Barman joins it with the
    server name) and the server name of `backup` (its `serverName`,
    else the Cluster's name) against those of the source, read from the inline
    store, from the component's own backup store, or from the store another
    component of the document emits (a `postgresql` component's backup store
    or a `cnpg-objectstore`, its `labels` and `annotations` aside, known by
    the name its author wrote, else by its component name; or a `postgresql`
    component's inline recovery store, by its default name
    `<component>-recovery-source`, shortened as every generated name is;
    go-kure/launcher#1044), an unauthored name only where no `Naming` hook is
    set. A store outside the document, or another component's store the
    author did not name while a hook is set (its name may be the hook's
    answer, asked by that component's lowering), is left to the plugin's own
    check before it archives.
  - **Ordering.** The recovering Cluster reads its source store before it can
    become ready, so the store never waits for the Cluster as the other members
    do. Where the document orders its components (a `dependency` policy), the
    Cluster waits for the store: the store is in a group before the Cluster's,
    with the placements and the bundle traits the members get, and waits for
    what the document's dependency rules make the component wait for, as all
    of its objects do (`Component.WaitsAs`). That includes the rules another
    component's lowering adds: made to wait for another `postgresql`
    component, the store waits for its Pooler, Databases and ScheduledBackups too
    (go-kure/launcher#1041). Where it does not, nothing is ordered, and the
    store is applied with the Cluster.
  - **`bootstrap`** is declared key by key (go-kure/launcher#1269), and a key
    under it that the component does not read is refused naming it
    (`bootstrap: unrecognized key "…"`), where it used to be dropped. It reads
    `initdb` and `pg_basebackup` (whose `source` is its only key; another
    was dropped before and is refused now), at most one of them and neither with
    `recovery`, and refuses the former `bootstrap.recovery` with its
    replacement. `bootstrap.initdb` creates the Cluster with an application
    database and its owner in place of the operator's default, `app`: both
    `database` and `owner` are required, not empty (the CloudNativePG webhook
    refuses one without the other), and they are written to the Cluster's
    `spec.bootstrap.initdb`. An empty `initdb` is refused the same way; to get
    the operator's default (database `app`, owned by `app`), leave `bootstrap`
    out. Any other CloudNativePG initdb key (`secret`,
    `encoding`, `postInitSQL`, `import`, …) is refused
    (`bootstrap.initdb: unrecognized key "…"`), never dropped; each can be
    modelled later. The owner's connection Secret is the operator's, named
    after the Cluster, and this component creates none. A bootstrap is read
    only when the Cluster is created; set on a component whose Cluster exists
    already, it changes nothing. `cnpg-cluster` takes the whole CloudNativePG
    `bootstrap.initdb`, strictly decoded.
  **Behavior-changing**: the former `bootstrap.recovery` is refused, whatever
  its value (`bootstrap.recovery: moved to recovery (…)`), and so is an
  external cluster's in-tree `barmanObjectStore`
  (`externalClusters[0].barmanObjectStore: removed (in-tree Barman Cloud, gone
  in CloudNativePG 1.31); use recovery.source`). `externalClusters` keeps its
  `name` and `connectionParameters`, the source of `bootstrap.pg_basebackup`.
  **Two object store paths are required** (go-kure/launcher#790):
  `backup.objectStore.destinationPath` (go-kure/launcher#1001; until then
  `backup.destinationPath`, where another value of `backup` was set), and
  `recovery.source.objectStore.destinationPath` for an inline recovery store
  (`backup.objectStore.destinationPath: required (…)`). The ObjectStore CRD
  requires it. An authored empty path is refused in each (`must not be empty
  (…)`). Until go-kure/launcher#1001 the second was the path of an external
  cluster's in-tree `barmanObjectStore`, now refused (above). Of the strings
  `cnpg-cluster` refuses unauthored, the lowering fills the parent of two,
  `bootstrap.pg_basebackup.source` and the Cluster's
  `postgresql.synchronous.method` (authored here as
  `replication.synchronous.method`), only from the string itself, and of the
  other nine (the Cluster's `backup.barmanObjectStore.destinationPath` and
  `externalClusters[].barmanObjectStore.destinationPath` among them, since
  `backup` and `recovery` write no in-tree Barman configuration, only plugin
  entries) never. `TestPostgresqlRule_UnauthoredRequiredStrings` reads all
  eleven in the lowered component.
  **A block that would not be built is refused, not dropped**
  (go-kure/launcher#790). Five blocks are built only when one field of
  theirs is set, and a document that authored other values of such a block
  without that field used to build with none of them in the result. It is
  refused by the name of the missing field:
  - `backup`: built from `objectStore`, so any `backup`, an empty one
    included, without one is refused (`backup.objectStore: required (…)`);
    the store itself needs a `destinationPath` that is not empty and
    `s3Credentials` (go-kure/launcher#1001, above; until then the Cluster's
    backup was built from `backup.destinationPath`).
  - `bootstrap.pg_basebackup`: the bootstrap is built from a non-empty
    `source`, so the block without one is refused
    (`bootstrap.pg_basebackup.source: required (…)`, or `must not be empty
    (…)` for an authored `""`). `recovery`, which replaced
    `bootstrap.recovery`, is refused without its `source` likewise
    (`recovery.source: required (…)`). `bootstrap.initdb` is built from
    `database` and `owner` together, so the block without either is refused
    by that field's name (`bootstrap.initdb.owner: required (…)`).
  - `replication.synchronous`: built from `method`, so the block without one
    is refused (`replication.synchronous.method: required (any or first)`),
    after a wrongly typed `number` or `dataDurability`, which is still
    refused by its own path.
  - `monitoring`: built when `enabled` is true, so `customQueries` beside an
    `enabled` that is not authored is refused (`monitoring.enabled: required
    where monitoring.customQueries is set (…)`).
  - `pooler`: the Pooler is emitted when `enabled` is true, so `instances`,
    `type`, `poolMode`, `parameters` or `image` beside an `enabled` that is not
    authored is refused (`pooler.enabled: required where pooler.instances is
    set (…)`).

  **An authored `enabled: false` keeps building**, in `pooler` and in
  `monitoring`: `enabled` is the block's own switch, the author wrote "off",
  and the settings beside it are kept in the document for when it is
  switched on. `poolerName` differs (refused whenever `pooler.enabled` is not
  true, an authored false included) because it is a property outside the
  block that names an object which does not exist while the pooler is off.
  A block with nothing in it (`bootstrap: {}`, `replication:
  {}`, `monitoring: {}`, `pooler: {}`), or with an empty `customQueries` list
  or `parameters` map only, authors no value and builds as before; `backup:
  {}` is the exception, refused since go-kure/launcher#1001 (above).
  `TestPostgresqlRule_BlockNotBuilt` holds each case. Breaking for a
  document that authored one of these blocks without its field, among them
  a `backup` with an `endpointURL` or an empty `retentionPolicy` and no
  path, a `bootstrap.pg_basebackup: {}` and a `replication.synchronous`
  with a `number` only. Such a document built,
  without what the author wrote.
  **An authored `pooler.instances` is written as authored**
  (go-kure/launcher#790), a 0 or a negative count included. The Pooler CRD
  gives `instances` a default of 1 and no minimum, and a count of 0 or less
  used to be left out of the Pooler, so the operator ran the one pod the
  author had switched off. The upstream type carries a 0, so it is written;
  the CRD does not bound the count, so a negative one is written too and
  refusing it is left to the cluster. A count that is not authored is still
  3 (`TestPostgresqlRule_PoolerInstances`). Behavior-changing for a document
  with `pooler.instances: 0` or less: its Pooler now holds that count. A
  document with `instances: 0` got one pod before and gets none now (the
  `postgresql-bootstrap-external` fixture shows it).
  **An additional Service's name** is the `serviceTemplate.metadata.name` of a
  `managed.services.additional[]` entry, and CloudNativePG creates the Service
  under it: a name with a leading digit is refused when `spec.kubernetesVersion`
  is below 1.36, RelaxedServiceNameValidation, naming the entry by its index
  (go-kure/launcher#1230; `docs/delivery-scope.md` §3.5).
  **The Pooler's and the Databases' names** (go-kure/launcher#787) are
  resolved in the order of every name role (see "Name roles and the `Naming`
  hook" in `site/content/api-reference/oam/names-and-overrides.md`): the author's property, else the consumer's
  `Naming` hook, else the default. `poolerName` names the Pooler (role
  `pooler`, default `<name>-pooler`) and must be a DNS-1123 label, since
  CloudNativePG names the pooler's Service after it (a leading digit is refused when
  `spec.kubernetesVersion` is below 1.36, RelaxedServiceNameValidation, the default
  `<name>-pooler` included; `docs/delivery-scope.md` §3.5); it is refused when
  `pooler.enabled` is not true. `databases[].objectName` names that entry's
  Database object (role `database`, default `<name>-<db>`) and must be a
  DNS-1123 subdomain; the entry's `name` stays the database created in
  PostgreSQL. A database whose `name` cannot be part of an object name
  (`app_data`) has no default and needs an `objectName`: without one it is
  refused, and the hook is not asked for it. A name is used as written or
  refused, never shortened. Each is
  also the name of the component the rule emits, so one that is already a
  component of the document, the postgresql component's own name included, is
  refused, naming both; two Databases given one name are refused, naming
  both entries; a Database given the Pooler's name is its sibling, as above.
  The hook is asked only inside `Transform`: a rule driven directly keeps
  the author's names and the defaults. With neither an authored name nor a
  hook the output is what it was.
  **The Cluster's and the ObjectStore's names** (go-kure/launcher#787) are
  resolved in the same order. `clusterObjectName` names the Cluster (role
  `postgresql-cluster`) and `backup.objectStoreObjectName` the ObjectStore (role
  `postgresql-objectstore`; a top-level property until
  go-kure/launcher#1001); the default of each is the component name. The
  two end in `ObjectName` as `objectName` does on a kind component, while
  `poolerName`, the older property, keeps the name it has.
  Without `backup` there is no store, and the hook is not asked for one.
  The Cluster's name is held to the
  Cluster's rule whoever chose it, a DNS-1035 label of at most 50 characters:
  a name that is no label is refused as every role's is, and a label over 50
  characters by the rule, naming both places it can come from
  (`clusterObjectName (or the Naming hook's answer for role
  "postgresql-cluster"): postgresql Cluster name "…": must be a DNS-1035
  label of at most 50 characters …`). A component whose own name is no such
  label is accepted once its Cluster is named apart. The ObjectStore's name is a
  DNS-1123 subdomain. Each is used as written or refused, never shortened.
  Each names its object alone: the two member components, their same-name
  sibling group, the policies the rule adds and the component label keep the
  component name. What the rule writes to the two objects follows the names
  they took: the Pooler's `cluster.name`, each Database's and each
  ScheduledBackup's `cluster.name`, the
  `cnpg.io/cluster` endpoint selector, and the `barmanObjectName` parameter
  of the Cluster's backup plugin. **The default names of the Pooler, the
  Databases and the ScheduledBackups keep deriving from the component name**
  (`<name>-pooler`, `<name>-<db>`, `<name>-<schedule>`), whatever the Cluster
  is named.
  **The recovery ObjectStore's name** (go-kure/launcher#1001) is the `Naming`
  hook's answer for role `postgresql-recovery-objectstore`, else
  `<name>-recovery-source`, with no property of its own; the hook is not asked
  where `recovery.source.objectStoreName` names an existing store. It is a
  DNS-1123 subdomain, a component of its own, and the Cluster's recovery
  source names the store by it; a name another component of the document
  holds is refused, naming both.
  **The ScheduledBackups' names** (go-kure/launcher#1001) are resolved the
  same way, with no property of their own: the `Naming` hook's answer for
  role `postgresql-scheduledbackup`, else `<name>-<schedule>`, where the
  schedule's `name` is the suffix. Whichever it is, it is held to 63
  characters, since CloudNativePG copies it into a label of each Backup it
  creates (`backup.schedules[0] "nightly": ScheduledBackup name "…" is 64
  characters long, …; shorten the component or the schedule name`, or, for the
  hook's answer, `…; shorten the Naming hook's answer for role
  "postgresql-scheduledbackup"`), and,
  as every member's, a name another component of the document or another
  member already holds is refused, naming both. With the Cluster named
  apart the component name is no longer bounded by the Cluster's rule, so
  the Pooler's default is held to the Pooler's own, by the lowering and where
  endpoints are collected alike, and the refusal says what settles it
  (`pooler: cnpg-pooler name "db.main-pooler": must be a DNS-1123 label of
  at most 63 characters (CloudNativePG names the pooler's Service after it);
  the default derives from the component name: set poolerName to name the
  Pooler otherwise`). A Pooler cannot carry its Cluster's name, and with the
  Cluster named apart the two can meet (a `clusterObjectName` of `db-pooler`
  on component `db`): it is refused where the Pooler is built, and where
  endpoints are collected in the same words (`cluster.name "db-pooler": a
  pooler cannot have the same name as its cluster`). A Pooler or a Database
  named like the `postgresql` component itself (a `poolerName` of `db` on
  component `db`, a database's `objectName`, or the hook's answer) is a name
  the document already holds: it is refused by the lowering, and where
  endpoints are collected in the same words (`pooler: generates component
  "db", which is already the name of component "db" (type "postgresql") in
  the document; rename one of them`). One named like another component of
  the document is refused by the lowering only: endpoints are collected for
  one component (`pkg/oam`, "Name roles and the `Naming` hook").
  **CloudNativePG derives the Cluster's Services (`<cluster>-rw`,
  `<cluster>-ro`, `<cluster>-r`) and Secrets (`<cluster>-app` and the others)
  from the Cluster's name, and the default backup path moves with it: the
  server name a backup is stored under is the Cluster's name unless
  `backup.serverName` sets it. Renaming an existing Cluster creates a
  new one: the old one is pruned with its data unless it is protected. Every
  address or Secret reference written with the old name is the author's to
  change.**
  Its rule implements the optional `oam.EndpointProvider`: it declares the CNPG cluster's
  data-plane endpoint (`cnpg.io/cluster: <Cluster name>`, the component name unless the
  Cluster is named apart, on port `5432`) so a downstream
  platform can synthesize the target-side ingress allow (`{comp}-allow-endpoint-ingress`)
  without hardcoding the operator selector. When `pooler.enabled` is set it declares a **second**
  endpoint for the pooler (PgBouncer) pods (`cnpg.io/poolerName: <pooler name>` on port
  `5432`), so a consumer that dials the pooler — whose pods carry a different label set and are not
  matched by the direct-cluster selector — also gets its connection synthesized. The pooler
  name there is the authored `poolerName`, else `<component-name>-pooler`, and the Cluster
  name the authored `clusterObjectName`, else the component name:
  `Transformer.ComponentEndpoints` asks no naming hook. The rule also implements
  `oam.NamedEndpointProvider`, through which `Transformer.ComponentEndpointsNamed` resolves each
  name with the consumer's hook, asked the request the transform asks, so a consumer that
  names the Pooler or the Cluster through the hook gets a selector for the name the object has.
- **cnpg-cluster** — the operator-CR kind component for a CloudNativePG
  `Cluster` (design: `docs/oam/design-operator-cr-components.md`). The
  component name becomes the Cluster's name and its `cnpg.io/cluster`
  endpoint selector, so it must be what CloudNativePG's admission webhook
  admits: a DNS-1035 label (no leading digit, no dot) of at most 50
  characters. Any other name is refused at parse time, when endpoints are
  collected and by `Generate` (`cnpg-cluster name "db.main": must be a
  DNS-1035 label of at most 50 characters …`), although other kinds accept
  DNS-1123 subdomains of up to 253. Its
  properties are the `spec` of a `postgresql.cnpg.io/v1` `Cluster`, one schema
  key per `ClusterSpec` json field: scalars are typed, and every structured
  field is an open `object` or an `array` of open objects whose description
  points at the upstream type. `ToApplicationConfig` decodes the whole property
  map into `cnpgv1.ClusterSpec` with `builtin.DecodeStrictJSON`, so a key the
  linked CNPG version does not declare, at any depth (`storage.sise`,
  `bootstrap.initdb.databse`), and a value of the wrong type
  (`storage.size: 5`) are refused rather than dropped. Two limits come from
  `encoding/json` itself: the unknown-field error names the key but not its
  path, and a key differing from a declared one only in letter case is
  accepted as that field (two spellings of one field in the same object are
  refused, since the decoder would keep only one: `storage.size: sets the
  same field as storage.Size …`). The policy defaults below read what was authored
  from the decoded spec as well as from the property map, where every
  spelling the decoder accepts is consulted, so such a key still counts as
  authored and is not overwritten by a default — including an empty
  `storage.size`, which the decoded spec cannot tell from absence.
  It carries **no launcher opinions**: `Generate` emits the Cluster named after
  the component in the build namespace with exactly the authored spec, so an
  unauthored field is left for the operator's own default. `postgresql`'s
  choices — an image from `version`, `primaryUpdateStrategy: unsupervised`,
  `enablePDB` from the replica count, a `1Gi` storage fallback, pod
  anti-affinity — are not made here: its lowering rule writes them as this
  kind's properties, and the trait it attaches sets the two that depend on
  the policy (see `postgresql`). The one value it writes unasked is
  `instances: 1`, the CRD default, because `ClusterSpec.Instances` has no
  `omitempty` and would otherwise serialize as `0`. The non-pointer blocks
  appear even when unauthored, as they do for `postgresql`: `affinity` and
  `resources` as empty objects, and `postgresql` as
  `syncReplicaElectionConstraint: {enabled: false}`, because that nested
  field's `enabled` has no `omitempty`.
  Nulls follow "The null contract" below, and a null is whatever serializes
  to JSON null: the properties are marshalled with `encoding/json` and read
  back before anything else looks at them, so a null map value is left out
  rather than decoded to an empty string, and a null array element is
  refused by path (`env[0]: null is not a valid array element`). The
  contract covers declared keys only: a key the CNPG type does not declare
  is refused as an unknown field even when its value is null
  (`storage: {sise: null}`), not dropped with the null. A lowering
  rule emits JSON-shaped values (string-keyed maps, slices, scalars) as for
  every component; that is the supported contract, and the engine's schema
  check refuses a typed API struct where it checks an object or array item
  (for example an `env` entry), though not inside an open object's
  undeclared keys. Because the reading is the serialization itself, a
  direct caller of the handler additionally has a value built in Go read
  exactly as `encoding/json` writes it — a concrete collection type, a nil
  behind a pointer, a `json.RawMessage` and a custom encoder included. A
  value that does not serialize is refused. An `instances` below 1, in
  any spelling, is refused before policy runs (`instances: must be >= 1,
  got 0`): the CRD's minimum is 1, so the API server would refuse the
  Cluster. A policy instance-count default below 1 is refused the same way.
  `Generate` repeats the name and instance-count refusals on what it emits,
  the count's upper bound (`instances: must be <= 2147483647, got …`)
  included, so a `CnpgClusterConfig` built in Go without
  `ToApplicationConfig` cannot produce a Cluster the operator or the API
  server rejects for either. It also refuses any storage request the
  Cluster carries that does not parse or is not positive, authored or from
  a policy default, on every claim the storage maximum below covers
  (`storage.size: quantity must be positive, got "0"`;
  `walStorage.pvcTemplate.resources.requests.storage: …`): CloudNativePG's
  webhook parses only `storage.size`, so such a Cluster was admitted and its
  claims then failed the API server's positive storage-request check
  (go-kure/launcher#623). An unset size is still left to the operator.
  An authored `0` or `false` that the typed spec cannot carry, on a field
  whose CRD default is not zero, is refused by path
  (`managed.roles[0].connectionLimit: 0 cannot be carried by the
  CloudNativePG API types (the field is omitted when zero, so the operator
  would apply its default -1)`). These fields are non-pointer and
  `omitempty`, so the value is omitted when the Cluster is encoded and the
  operator applies its CRD default instead. For the linked CNPG version they
  are `managed.roles[].connectionLimit` (default `-1`, unlimited),
  `postgresUID` and `postgresGID` (`26`), `startDelay` (`3600`),
  `stopDelay` (`1800`), `switchoverDelay` (`3600`),
  `probes.liveness.isolationCheck.requestTimeout` and `.connectionTimeout`
  (`1000`) and `replicationSlots.updateInterval` (`30`); a test derives the
  set from the CNPG module's CRD and Go types, so a CNPG bump that changes it
  fails CI. An explicit zero default on any other such field, such as
  `managed.roles[].login: false`, `superuser: false`, `minSyncReplicas: 0`
  or `monitoring.enablePodMonitor: false`, is accepted: it is omitted too,
  and the field's absence means the same value.
  Two fields the Cluster CRD requires, which the Go type leaves out when they
  are empty, are refused when unauthored or empty: the `signerName` and the
  `keyType` of a pod certificate source of the projected volume template
  (`projectedVolumeTemplate.sources[0].podCertificate.signerName: required
  (…)`). The object would show the omission and the API server refuse it.
  Two more that the CRD requires, which the Go type writes as `null` when
  they are unauthored, are refused when unauthored: the `nodeSelectorTerms`
  of a required node affinity
  (`affinity.nodeAffinity.requiredDuringSchedulingIgnoredDuringExecution.nodeSelectorTerms:
  required (…)`) and the `databases` of an import
  (`bootstrap.initdb.import.databases: required (…)`). The API server drops a
  null of a field that is not nullable before it validates, so the required
  field is then missing. An authored empty list is written as one and is not
  refused here. A third such field is not refused:
  `replicationSlots.synchronizeReplicas.enabled`, which the CRD defaults, so
  the API server puts the default (`true`) in the place of the null.
  `TestKindComponents_NullRequired` derives the three from the CRD and shows
  each answer by running the CRD's schema validator on the object.
  **Four fields the linked Go type holds and the Cluster CRD does not are
  refused when authored**, under `backup.barmanObjectStore` and under each
  `externalClusters[].barmanObjectStore`: `s3Credentials.sseCustomerKey`
  (with its `name` and `key`) and `s3AddressingStyle` (for example
  `backup.barmanObjectStore.s3AddressingStyle: no field of the
  postgresql.cnpg.io/v1 Cluster API: its CRD has no such property`). The
  barman-cloud type the strict decode reads is newer than the Cluster CRD of
  cloudnative-pg v1.30.1 (its `clusters.yaml`), which has no property for
  either, so the API server would name each as unknown and prune it. A `null`
  or an empty `s3AddressingStyle` is not authored and builds. The kind's row
  in `TestKindComponents_OmittedRequiredAndWrittenDefaults` derives the eight
  paths from the CRD in both directions, and
  `TestCnpgClusterHandler_AbsentFromCRD` shows each refusal. Remove the
  refusals when cloudnative-pg ships a CRD that carries the fields.
  **The kind's required list is generated** (go-kure/launcher#883; see
  "Required fields a kind writes unauthored and does not refuse"): every
  field the Cluster CRD requires that the Go type writes whether or not it
  was authored (`""`, `0`, `{}` or `null`) is refused when unauthored, each
  where its parent is authored (`replica.source: required (…)`), the fields
  of the Kubernetes types the spec embeds included. Among them are the eleven
  strings the kind refused by hand before, which the CRD bounds with a
  minimum length or an enumeration:
  `backup.barmanObjectStore.destinationPath`,
  `bootstrap.initdb.import.type` (`microservice` or `monolith`),
  `bootstrap.pg_basebackup.source`,
  `externalClusters[].barmanObjectStore.destinationPath`,
  `managed.services.additional[].selectorType` (`rw`, `r` or `ro`),
  `podSelectorRefs[].name`, the `name` of a `postgresql.extensions[]` entry
  and the `name` and the `value` of its `env[]` entries,
  `postgresql.synchronous.method` (`any` or `first`) and `replica.source`;
  and, refused since go-kure/launcher#883 (**breaking** for a document that
  leaves one out under an authored parent), the `name` and `key` of a Secret
  or ConfigMap reference, an `imageCatalogRef`'s `kind`, `name` and `major`,
  `postgresql.synchronous.number`, an external cluster's `name`, a plugin's
  `name`, a tablespace's `name` and `storage`, a `podSelectorRefs` entry's
  `selector`, the `topologyKey` of a pod affinity term and the `weight` of a
  weighted one, the
  `maxSkew`, `topologyKey` and `whenUnsatisfiable` of a topology spread
  constraint and the `type` of a seccomp or AppArmor profile. A field the
  kind refused by hand keeps its text byte for byte; any other reads `the
  CloudNativePG API requires this field` (or `requires the name of what this
  refers to`, for a reference's `name`). Most such objects the API server
  refused; an `imageCatalogRef` written with `name: ""` or `major: 0` it
  accepted, and it reached the operator. An authored empty one is a value: it is
  written, and refusing it is left to the API server. The refusal reads the authored
  properties, so `Generate` does not repeat it: a `CnpgClusterConfig` built
  in Go is not held to it. Nor is the component a `postgresql` component is
  lowered to, whose properties are written from a typed spec, so that a
  string left out there reaches the kind as an authored `""`: `postgresql`
  refuses the two it could leave out itself, under the property its author
  wrote (above). The same test
  derives every field
  the type writes unauthored as a zero value the CRD's own rule refuses, and
  shows each answer with the validator; the twelfth of a Cluster is
  `instances`, which is not refused (below).
  `TestKindComponents_OmittedRequiredAndWrittenDefaults` derives the two from
  the CRD and shows the refusals. It also holds the two fields the CRD
  defaults and the type writes unauthored to a reason they are harmless:
  `instances`, of which the kind always writes a count (the authored one, the
  policy's default, or the CRD's own `1`), and
  `backup.volumeSnapshot.onlineConfiguration`, written `{}` under an authored
  `volumeSnapshot`. The CRD's default for the object is `waitForArchive: true`
  and `immediateCheckpoint: false`; in a `{}` it fills `waitForArchive` with
  the field's own default `true`, and an absent `immediateCheckpoint` is
  `false`.
  A `false` or `0` the type keeps (a pointer such as `enablePDB: false`,
  a quantity `cpu: 0`) is emitted as authored. An authored
  empty string the type omits on a field the CRD defaults (`primaryUpdateStrategy:
  ""`, `logLevel: ""`) is refused as a defaulted `0` is
  (`cnpgClusterDefaultedZeroFields`); elsewhere it is not refused —
  `storage.size: ""`, which the CRD does not default, keeps its meaning
  above. The two pod-certificate fields above are refused too: an empty
  `signerName` or `keyType` is refused as an unauthored one is.
  A match expression authored in one of the spec's label selectors without
  its `key` or its `operator` is refused by path
  (`podSelectorRefs[0].selector.matchExpressions[0].operator: required (…)`):
  the CRD requires both, and the type would write them empty; the generated
  list holds them with the sentences of every kind's selectors. Presence only;
  "A label selector's match expressions" lists the selectors and says what is
  left to the API server and to the operator's webhook.
  `ApplyPolicy` enforces the policy `postgresql` enforces (postgresql lowers
  onto this kind, so it is the same code), in this order:
  the instance-count default when `instances` is not authored (an authored
  value wins even when it equals the fallback) and its maximum; the cpu and
  memory request and limit defaults on `resources`, filling only unset
  entries, and their maxima; and the storage-size default on `storage.size`
  when neither `storage.size` nor `storage.pvcTemplate`'s storage request is
  authored. With no policy default the size is left to the operator; the
  `1Gi` fallback is postgresql's, applied after the policy by the post-policy
  step its rule attaches
  (`ApplyPostgresqlDefaults`: the fallback is held to the policy maximum, and
  `enablePDB` is set from the instance count; a fallback over the maximum is the
  component's `oam.ViolationError` of class `oam.RefusalStorageMaximum`, as a
  refusal of `ApplyPolicy` is). That method is for that step,
  not an authoring surface. A storage-size default that is
  not a quantity is refused (`policy default for storage.size: invalid
  quantity "lots"`), as an invalid cpu or memory default is, since with no
  maximum set nothing else would parse it. So is a zero or negative one
  (`quantity must be positive, got "0"`): CloudNativePG's webhook only
  parses the size, and the claims it creates would then fail the API
  server's positive storage-request check. The storage maximum applies to
  every claim the Cluster creates — `storage`, `walStorage`, each
  `tablespaces[i].storage` (either `size` or the `pvcTemplate` request) and
  `ephemeralVolumeSource.volumeClaimTemplate` — and the error names the one
  that exceeds it. Because this kind exposes the two security contexts, it
  also refuses `securityContext.privileged` and a `securityContext` or
  `podSecurityContext` `windowsOptions.hostProcess` unless the policy allows
  privileged workloads, and an added capability the policy forbids or leaves
  off a non-empty allowlist. The registry allowlist the workload kinds apply
  to their image applies to an authored `imageName`
  (`imageName: image "…" is not from an allowed registry [...]`). When
  `imageName` is unset, the operator takes the image from the catalog
  `imageCatalogRef` names, which this kind does not check, or, without one,
  chooses an image of its own, which no allowlist can hold: under a non-empty
  allowlist a Cluster with neither `imageName` nor `imageCatalogRef` is
  refused, with the registry class (**breaking**, go-kure/launcher#790: it
  built before): `imageName: unset, so the CloudNativePG operator chooses the
  image the pods run, which the allowed registries [...] cannot hold; name an
  image from one of them`. Set `imageName`, or `imageCatalogRef` to take the
  image from a catalog. With no allowlist nothing changes. The operator's own
  image, which it runs in the instance pods' bootstrap init container, is not
  covered: no field of the Cluster names it. A `postgresql` component always
  writes `imageName` (its derived `ghcr.io/cloudnative-pg/postgresql:<version>` or the authored one),
  so the allowlist applies to its image too. The allowlist applies as well to
  the image of each `postgresql.extensions[]` entry, which the operator mounts
  into the instance pods as an image volume (**breaking**,
  go-kure/launcher#790: an extension image from a registry outside the list
  built before): `postgresql.extensions[0].image.reference: image "…" is not
  from an allowed registry [...]`, with the registry class, the reference read
  as `imageName` is (see *Image volumes* under **pod**). An entry
  without a reference is not checked: the operator takes that image from the
  catalog `imageCatalogRef` names (and refuses the Cluster without one), and
  a catalog's images are not checked by this kind, as for an unset
  `imageName`. A build reads the objects a document brings, not an object a
  reference points at: a catalog's images are held where the catalog is
  authored, by the kind that builds it. `cnpg-imagecatalog` and
  `cnpg-clusterimagecatalog` hold every image of a catalog they build to the
  same list (see their entry above). Nothing in this library holds the images
  of any other catalog: not of a raw catalog on `manifests`, `passthrough` or
  a chart, and not of one that already exists in the cluster. A `postgresql`
  component writes no extension entry. `imageName` and the extension images are every image a
  Cluster names today; the test that walks the pod spec for image fields
  walks the Cluster and the Pooler spec the same way.
  Both are also held to the tag rule (`ValidateImageRef`) when the component
  is read and again at generation, with or without a policy (**breaking**,
  go-kure/launcher#790: an untagged or `:latest` `imageName` or extension
  reference built before): `imageName: image "…" rejected: no tag or digest
  specified; use an explicit version tag or digest`,
  `postgresql.extensions[0].image.reference: image "…" rejected: …`. An unset
  `imageName` and an entry without a reference name no image and are not
  checked, as for the allowlist. A digest without a tag passes, here as
  everywhere the rule runs; on `imageName` CloudNativePG's webhook refuses
  one, and a tag that is no PostgreSQL version, and those are the operator's
  value rules, left to the operator.
  Generation refuses `hugepages-<size>`
  in `resources` without `cpu` or `memory` after policy defaults. It also
  applies the shared parser's request/limit cross-check there
  (`resources: cpu: request 2 must not exceed limit 1`; hugepages and
  extended resources need a limit equal to the request), so a policy default
  limit below an authored request is refused as well. The other
  resource-name rules of the shared parser are left to the API server.
  `Endpoints` declares the same primary endpoint as `postgresql`
  (`cnpg.io/cluster: <Cluster name>` on port `5432`), and only for a
  component the parse accepts: what `ToApplicationConfig` refuses of the
  name or of the properties is refused where endpoints are collected, in the
  same words (`instances: must be >= 1, got 0`). What `Generate` refuses
  once the policy is applied is not repeated there.
  `TestCnpgClusterSchema_CoversClusterSpec` pins the schema to
  `ClusterSpec` by reflection: each json field is published with its type or
  listed with a reason in `cnpgClusterExcludedFields` (empty today), and a
  schema key with no field or a stale exclusion also fails, so a CNPG bump
  that adds, removes or retypes a field names it.
- **cnpg-pooler**, **cnpg-database**, **cnpg-objectstore** — the operator-CR
  kind components for a CloudNativePG `Pooler` and `Database` and a Barman
  Cloud plugin `ObjectStore` (`barmancloud.cnpg.io/v1`), built on the
  `cnpg-cluster` recipe: one schema key per json field of `PoolerSpec`,
  `DatabaseSpec` or `ObjectStoreSpec`, the whole property map decoded strictly
  into that type under the same null contract, the same refusal of an
  authored `0` or `false` the type cannot carry (for the linked versions only
  `instanceSidecarConfiguration.retentionPolicyIntervalSeconds`, default
  `1800`, on the ObjectStore) and of two spellings of one field, and the same
  reflection tests pinning the schema and both derived lists to the linked
  modules. `Generate` emits one object named after the component in the build
  namespace with the authored spec, and repeats the parse-time refusals made
  on the decoded spec. The
  fields each CRD requires are refused when
  unauthored or empty, by path: `cluster.name` and `pgbouncer` on the Pooler
  (`pgbouncer: {}` selects PgBouncer's defaults); `cluster.name`, `name` and
  `owner` on the Database; `configuration.destinationPath` on the ObjectStore.
  The published schema marks the top-level property of each as required
  (go-kure/launcher#790): `cluster` and `pgbouncer` of a `cnpg-pooler`;
  `cluster`, `name` and `owner` of a `cnpg-database`; `configuration` of a
  `cnpg-objectstore`. That refuses no component that built before, since the
  kind refused each without it already. The `template` of a `cnpg-pooler` is an
  open object, so its schema names the gated pod fields in `gatedPaths`, as the
  pod template kinds do: a gated field under `template.spec` is refused when the
  ClusterProfile's `kubernetesVersion` is below the release where its feature
  gate is on by default (go-kure/launcher#1321). A document with no
  `kubernetesVersion` is unchanged.
  **The required lists of the three kinds are generated**
  (go-kure/launcher#883), as `cnpg-cluster`'s is: every field each CRD
  requires that the Go type writes whether or not it was authored is refused
  when unauthored under an authored parent, with the text the kind gave it by
  hand where it had one. **Breaking** for a document that leaves one out
  under an authored parent: on the Pooler, among others, the `name` of a
  `pgbouncer` Secret reference, `pgbouncer.imageCatalogRef`'s `kind` and
  `name`, a service port's `port`, and under `template.spec` a container's
  `name`, the `topologyKey` of a pod affinity term, a topology spread
  constraint's `maxSkew`, `topologyKey` and `whenUnsatisfiable` and a seccomp
  profile's `type`; on the Database, the `name` of a `schemas`, `extensions`,
  `fdws` or `servers` entry, the `name` and `value` of an option, the `name`
  of a usage and a server's `fdw`; on the ObjectStore, the `name` and `key`
  of a credential's or `endpointCA`'s Secret reference and the fields of the
  sidecar's `env` the pod kinds require. The texts the kinds refused with
  before (`cluster.name: required (…)`, `pgbouncer: required (…)`) are kept
  byte for byte. `template.spec.containers` is not refused: the kind writes
  `containers: []` itself, which the CRD accepts (below).
  A `cluster.name` must be a name CloudNativePG admits for a Cluster (a DNS-1035
  label of at most 50 characters).
  On the Pooler, a match expression authored in a label selector of
  `template.spec` without its `key` or its `operator` is refused too, by
  path, by the generated list; an authored empty one is not (presence only;
  see "A label selector's match expressions").
  `cnpg-pooler` writes no type or instance count of its own, so the operator's
  defaults (`rw`, `1`) apply. Its component name is the Pooler's name and its
  Service's, so it must be a DNS-1123 label of at most 63 characters (a
  leading digit is refused when `spec.kubernetesVersion` is below 1.36, RelaxedServiceNameValidation; CloudNativePG's Pooler
  webhook checks no name syntax, go-kure/launcher#959,
  `docs/delivery-scope.md` §3.5), and a pooler named like its cluster is refused, as CloudNativePG's webhook does:
  by the build, and where its endpoint is collected in the same words
  (`cluster.name "main": a pooler cannot have the same name as its cluster`).
  `ApplyPolicy` applies the workload kinds' pod gates to `template.spec` (host
  namespaces, hostPath volumes, privilege, host-process, capabilities, the
  registry allowlist on each authored container image, cpu and memory maxima;
  errors name the container, as in `template.spec.containers[0] "pgbouncer": cpu
  limit "2" exceeds enforced maximum "1"`), caps a generic ephemeral volume's
  claim at the policy storage maximum, holds an image volume's reference to the
  registry allowlist (`template.spec: volume "ext" image.reference: image "…" is
  not from an allowed registry [...]`; **breaking**, go-kure/launcher#790, see
  *Image volumes* under **pod**), and applies the registry allowlist to an
  authored `pgbouncer.image`. With or without a policy, when the component is
  read and again at generation, an authored `pgbouncer.image`, the image of
  each init and regular container of the template and the reference of each
  of its image volumes are held to the tag rule (`ValidateImageRef`;
  **breaking**, go-kure/launcher#790: an untagged or `:latest` one built
  before): `pgbouncer.image: image "…" rejected: …`,
  `template.spec.containers[0] "pgbouncer": image "…" rejected: …`. A template
  container that names no image is not checked: the operator supplies the
  PgBouncer image, so that is the ordinary form here, where the pod kinds
  refuse it. The operator takes the PgBouncer image, in order, from the
  template's regular container named `pgbouncer`, from `pgbouncer.image`, from
  the catalog `pgbouncer.imageCatalogRef` names (not checked by this kind, as
  for `cnpg-cluster`), and otherwise chooses one of its own, which no allowlist
  can hold. Under a non-empty allowlist a Pooler that names its image none of
  these three ways is refused, with the registry class (**breaking**,
  go-kure/launcher#790: it built before): `pgbouncer.image (pooler.image on a
  postgresql component): unset, so the CloudNativePG operator chooses the
  image the pods run, which the allowed registries [...] cannot hold; name an
  image from one of them`. An image on an init container or on a container of
  another name does not count. With no allowlist nothing changes. The
  operator's own image, which it runs in the pod's bootstrap init container,
  is not covered: no field of the Pooler names it. Generation runs the shared
  parser's request/limit and hugepages checks on the template's pod and container
  resources (`template.spec.containers[0] "pgbouncer": resources: cpu: request
  2 must not exceed limit 1`), as `cnpg-cluster` does on its `resources`; the
  other resource-name rules of the shared parser are left to the API server. A
  template declaring `ephemeralContainers`, `activeDeadlineSeconds`, `priority`
  or `overhead` is refused, as the workload kinds refuse them: the operator
  copies the template into a Deployment, whose pod template cannot carry the
  first two and whose pods get the last two from the default Priority and
  RuntimeClass admission controllers, which refuse an authored value that
  differs from theirs. Four fields the Pooler CRD requires under
  `template.spec`, which the pod spec's type leaves out when they are empty,
  are refused when unauthored or empty: the `action` of a restart rule of a
  container or an init container and the `operator` of its exit codes
  (`template.spec.containers[0].restartPolicyRules[0].action: required (…)`),
  and the `signerName` and `keyType` of a pod certificate source of a
  projected volume
  (`template.spec.volumes[0].projected.sources[0].podCertificate.signerName:
  required (…)`). The object would show the omission and the API server
  refuse it; the CRD of the linked module settles that, which is what the
  built-in pod kinds lack.
  `TestKindComponents_OmittedRequiredAndWrittenDefaults` derives the fields
  from the CRD and shows the refusals. Five more that the CRD requires under
  `template.spec`, which the pod spec's type writes as `null` when they are
  unauthored, are refused when unauthored: the `nodeSelectorTerms` of a
  required node affinity, the `priority` of an eviction responder, the
  `monitors` of a `cephfs` or an `rbd` volume, and the `secretRef` of a
  `scaleIO` volume (`template.spec.volumes[0].cephfs.monitors: required (…)`).
  The API server drops a null of a field that is not nullable before it
  validates, so the required field is then missing. An authored empty list is
  written as one and is not refused here. `template.spec.containers` is such
  a field too and is not refused: the kind writes `containers: []` itself
  (below). `TestKindComponents_NullRequired` derives these fields and
  `pgbouncer` from the CRD and shows each answer by running the CRD's schema
  validator on the object. One string the CRD requires and bounds with a
  pattern, which the type writes as `""` when it is unauthored, is refused
  when unauthored under an authored `pgbouncer.imageCatalogRef`: its `key`
  (`pgbouncer.imageCatalogRef.key: required (…)`). An authored empty one is
  written, and refusing it is left to the API server. The refusal reads the
  authored properties, so `Generate` does not repeat it: a `CnpgPoolerConfig`
  built in Go is not held to it. The same test derives
  it, as the one field of a Pooler the type writes unauthored as a zero value
  the CRD's own rule refuses. The instance count is deliberately not policed:
  `postgresql`, which lowers its pooler onto this kind, never applied a
  policy to the pooler's count, so a maximum here would refuse a document
  that built before.
  Pod-level errors name the template's own fields, as in `template.spec:
  resources cpu limit "2" exceeds enforced maximum "1"`. A `template` that lists
  no containers (labels only, say) is written with `containers: []`: the Go type
  cannot omit the template's `spec`, the CRD requires `containers` once it is
  present, and the operator adds its `pgbouncer` container either way.
  `Endpoints` declares the PgBouncer pods (`cnpg.io/poolerName:
  <Pooler object name>` on port `5432`: the component name unless
  `objectName` or the naming hook names the Pooler), byte-identical to `postgresql`'s pooler
  endpoint for a pooler of that name (`<component-name>-pooler` by default,
  of the `postgresql` component), and
  only for a component the parse accepts: what `ToApplicationConfig` refuses
  is refused where endpoints are collected, in the same words (`pgbouncer:
  required (an empty object selects PgBouncer's defaults)`).
  `cnpg-database` also refuses the names the CRD reserves (`postgres`,
  `template0`, `template1`). The `ensure` of each schema, extension, fdw and
  server has no `omitempty` but a CRD default of `present`, so `Generate` writes
  `present` where it is unauthored, as the API server would have; a
  test derives that list from the CRD too. An authored `ensure: ""` there is
  refused by its path (`schemas[0].ensure: "" is refused by the Database CRD,
  …`) rather than written as `present`, since the CRD's enum refuses it as
  written; an explicit null is absence and takes the default. A Database runs nothing, so it has
  no policy and no endpoint.
  `cnpg-objectstore` caps the cpu and memory requests and limits of the plugin
  sidecar it adds to every instance pod (`instanceSidecarConfiguration.resources`)
  at the policy maxima, filling no default, and declares no endpoint;
  generation runs the same request/limit and hugepages checks on them, leaving
  the other resource-name rules to the API server. It refuses
  `configuration.serverName`, which the shared Barman type carries but the
  plugin's CRD forbids on an ObjectStore: the server name is a plugin parameter
  of the Cluster that uses the store.
- **passthrough** — `object` (full apiVersion/kind/metadata/spec), `clusterScoped`.
  Its config exposes `ComponentName() string` (the `oam.ComponentNamed` interface) so
  consumers can attribute the emitted resource to its owning OAM component.
  **`object` must be a single object; a list is rejected.** `Generate` emits the map
  verbatim as one resource and fills in metadata it finds missing: `metadata.name`
  defaults to the component name when it is absent, empty, **or not a string**, and
  on a namespaced object `metadata.namespace` likewise — so an inline *non-empty
  string* namespace survives untouched, while a non-string one is replaced rather
  than emitted. The values of the emitted object are JSON values (`int64`, `float64`,
  `json.Number`, `map[string]any`, `[]any`), whatever Go types the authored object
  held, so the emitted `Unstructured` can be deep-copied (go-kure/launcher#1162); the
  config's own `Object` keeps the authored values. A number is an `int64` where it
  fits, a `float64` where a `float64` holds it exactly, and otherwise a `json.Number`
  with its own digits (an integer beyond the `int64` range, `9.223372036854775809e18`,
  a `1e-9999999`), so no digit is rounded; an authored `json.Number` takes the type its
  text has, and an empty one is `0`, as `encoding/json` writes it
  (go-kure/launcher#1167, go-kure/launcher#1170). A value with no JSON form (a func, a
  chan, a complex number, a NaN or infinity, a `json.Number` that is not a number) is refused,
  naming the field, instead of being emitted for a deep copy to panic on
  (go-kure/launcher#1166). An object with a key given twice in one JSON object, at any
  depth (a `json.RawMessage` a Go caller built), is refused, naming the field: the
  policy check and the emitted object would otherwise read it differently. The
  field is named by the path every passthrough error uses (`fieldPath`): keys
  joined by dots, and a key that is empty or holds a dot, a bracket or a quote
  written as a quoted index, `labels[""]` or `labels["a.b"]`, so that two different
  fields never share a path. A number
  beyond `float64` range in that object is read as text and is not refused
  (go-kure/launcher#1176).

  **Scope** (go-kure/launcher#794, item 13). Whether the object is namespaced is
  resolved with the precedence `manifests` gives a `scopeOverrides` entry,
  with `clusterScoped` in the entry's place (`resolveClusterScoped`,
  `passthrough.go`):

  - **A kind whose scope the Kubernetes API governs** (a built-in kind, a
    `CustomResourceDefinition`; `isAPIGovernedScope`): kure's scope table decides. A
    `PriorityClass`, a `ClusterRole` or a `Namespace` gets no namespace without the
    property. A `clusterScoped` that contradicts the table is refused: `true` on a
    namespaced built-in, an explicit `false` on a cluster-scoped one (`passthrough
    component "batch-low": clusterScoped is false, but the Kubernetes API defines
    scheduling.k8s.io/v1 PriorityClass as cluster-scoped; remove clusterScoped`).
    **This is the one difference from `manifests`**, which ignores a `scopeOverrides`
    entry for such a kind: its list may name kinds the source does not hold, while
    `clusterScoped` is a statement about the one object, so a wrong one is an error
    in the document and not a spare entry.
  - **Any other kind:** an authored `clusterScoped` decides, `true` or `false`, also
    against the table. A custom resource kure registers as cluster-scoped that the
    target cluster serves namespaced takes `clusterScoped: false`.
  - **Not authored** (absent or `null`): the table decides for a kind kure registers,
    so a cert-manager `ClusterIssuer` gets no namespace. A kind nothing knows is
    treated as namespaced, the default this component always had. `manifests` fails
    closed on an unknown scope; `passthrough` does not, since every custom resource
    passed through without the property relies on that default.

  No `CustomResourceDefinition` is consulted: the component holds one object. A
  cluster-scoped object, however it was resolved, gets no namespace default, and an
  inline namespace on it is refused with the reason named (`passthrough component
  "ca": object.metadata.namespace must not be set ("other"): cert-manager.io/v1
  ClusterIssuer is registered as cluster-scoped (set clusterScoped: false if the
  cluster serves it namespaced); the Kubernetes API rejects a namespace on a
  cluster-scoped object`). That rejection is keyed on a non-empty string, so a
  non-string `namespace` on a cluster-scoped object is neither rejected nor
  defaulted and reaches the output as authored. The resolution runs in `Generate`
  too, on the object about to be emitted, so a `PassthroughConfig` built directly is
  held to it; its `ClusterScoped` field states `true` or nothing, since an explicit
  `false` exists only for an authored property.

  **Breaking** (pre-GA): an object of a cluster-scoped kind the table knows loses the
  application namespace it was stamped with when `clusterScoped` was not set;
  `clusterScoped: true` on a built-in namespaced kind and `clusterScoped: false` on a
  built-in cluster-scoped kind stop building; and an inline namespace on a
  cluster-scoped kind the table knows is refused without the property too, where it
  was emitted as authored.

  **Lists.** `Generate` emits the map as one resource, so a list would arrive
  downstream as one *named*
  envelope whose `items` never see per-object label mutation, namespace stamping or
  ownership checks — while Flux unwraps it at apply time into N objects that
  do reach the cluster. One envelope bypasses every per-object rule at once, which is why
  the rejection lives here and not in each consumer. Declare one component per object.
  The check is apimachinery's own `Unstructured.IsList` — `items` present **and** a
  sequence — never the kind name, so a typed `ConfigMapList` is caught and a CRD whose
  kind merely *ends* in `List` with no `items` still compiles.

  This arm is **deliberately stricter than Kustomize** for a kind that does *not* end in
  `List` but carries a top-level `items` array, such as `{kind: Widget, items: [...]}`.
  Kustomize's build keeps that as one resource, because it consults `items` only on a
  `List`-suffixed kind. Flux's kustomize-controller then decodes the build output with
  the same `IsList` predicate and no kind check, and applies the array's members *instead
  of* the object: a `Widget` whose `items` holds a `ConfigMap` puts that `ConfigMap` on
  the cluster, in whatever namespace it names, and never applies the `Widget`. With
  scalar members the Flux apply fails outright. Either way it is not one object, so it is
  rejected.

  A second arm catches what `IsList` structurally cannot. It requires `items` to be
  exactly a `[]interface{}`, so an authored `items: null` — an untyped nil — passed
  every check and then expanded to **zero** objects at apply time with no error at
  all; with pruning enabled an empty desired result also removes whatever the previous
  inventory held. That case is rejected on its own diagnostic ("expands to zero
  objects"), and it is keyed on a **null** `items` **on a kind ending in `List`**
  rather than a present one — a CRD may legitimately carry an object-valued `items`
  field that must keep compiling, and an ordinary, non-`List` kind may equally carry
  a null-valued `items` field (a plausible spec-field collision) without being an
  envelope at all. Matches Kustomize's own `inlineAnyEmbeddedLists`, which checks the
  kind suffix before ever consulting `items`; Flux agrees, since a null `items` is not
  a list to `IsList` and the object is applied as one.

  The validated object is deep-copied when the config is built, not aliased, so a
  caller that keeps mutating the map it passed in cannot change what was validated.
  The copy detaches nested maps and slices whatever their concrete type — the authored
  path only ever yields `map[string]any` and `[]any`, but a Go-assembled body can hold
  a `map[string]string` or a `[]string`, and those used to alias straight through. A
  null survives as a null rather than becoming an empty `{}`/`[]`. Two things it
  deliberately does not do: it does not chase pointers (a `*Location` inside a
  `time.Time`, `resource.Quantity`'s `*inf.Dec`), and it does not reject the shapes it
  cannot fully detach.

  The copy is **not** what makes the rejection binding, and this section used to claim
  it was. `Object` is an exported field, so a caller holding the config can assign a
  fresh map over it, and a struct literal or a JSON decode never runs
  `ToApplicationConfig` at all — three routes reaching `Generate` with a body the arms
  never saw. `Generate` therefore re-runs, on the map it is about to emit, every check
  that must hold of an emitted body: a non-empty `apiVersion` and `kind`, and both list
  arms. Checking the bytes being emitted, rather than trusting a check that ran on some
  earlier map, is what closes those routes.

  Those checks live in one function precisely so the two call sites cannot disagree
  about what a valid body is. While they were split — the constructor checking identity,
  `Generate` checking only list shape — a `PassthroughConfig` holding an *empty* map
  passed both: non-nil, so it cleared the no-object guard, and carrying no `items`, so
  it cleared both arms, leaving `Generate` to emit a document consisting of nothing but
  the metadata it had just stamped on.

  **Policy.** `ApplyPolicy` holds the object to the environment policy
  (go-kure/launcher#794), with the check template delivery runs on the objects a chart
  renders (`helmtemplate`'s *Every emitted workload*); the transform calls it with
  `NoopPolicy` when no policy is passed. A Pod, PodTemplate, ReplicationController,
  Deployment, StatefulSet, DaemonSet, ReplicaSet, Job or CronJob is checked as an authored
  workload is: host namespaces, hostPath volumes, the cpu/memory maxima, the storage
  maximum on a generic ephemeral volume's claim, the registry allowlist on an image
  volume's reference (**breaking**, go-kure/launcher#790: an object with one from a
  registry outside the list built before; see *Image volumes* under **pod**), and
  for every init and regular container
  the registry allowlist, the privileged, HostProcess and capability gates, and
  `ValidateImageRef` (no untagged image, no `:latest`), which holds the reference of an
  image volume too (**breaking**, go-kure/launcher#790: an object with an untagged or
  `:latest` one built before). Ephemeral containers are refused.
  The storage a PersistentVolumeClaim, or a StatefulSet's claim template, requests is held
  to the storage maximum (`MaxStorageSize`); the replica count of a Deployment,
  StatefulSet, ReplicaSet or ReplicationController (one when the object sets none) and the
  `maxReplicas` of a HorizontalPodAutoscaler, in any API version, to the replica maximum
  (`MaxReplicas`). A PersistentVolume is held to what the `persistentvolume` kind holds
  its own to: a `hostPath` or `local` source needs `AllowHostPathVolumes()`, and
  `spec.capacity.storage` is held to the storage maximum (breaking for a document that
  holds one; see **persistentvolume**). The error is the component's policy violation and names the object and
  the field (`passthrough: object Deployment "demo/web":
  spec.template.spec.containers[0] "app": …`).

  A core Secret is refused under a policy that forbids explicit secrets
  (`oam.ExplicitSecretPolicy`, see the `pkg/oam` README), as the `secret` trait is
  (go-kure/launcher#794, item 2): `passthrough: object Secret "demo/creds": the object is a
  Secret, and the environment policy forbids explicit secrets; …`. The check does not read
  what the Secret holds, so one with no entry is refused too, and the message quotes nothing
  of the object. A policy that does not implement the interface, and no policy, allow it.
  **Breaking** only under a policy that answers `false`: a `passthrough` Secret built under
  it before.

  The object is authored as a map and the check reads Go types, so an object whose group,
  version and kind kure's scheme registers is decoded as that kind **for the check only**.
  What is emitted is the authored map in JSON types, so a field the Go type does not declare
  is emitted with it, and a large integer keeps its digits, which kure's manifest writer
  writes as they are (`helmtemplate`'s *Rendered objects*). The check
  cannot read such a field, so a workload or a claim that sets one is refused, as template
  delivery and `manifests` refuse it (`helmtemplate`'s *Undeclared fields*): when the
  component is built and again by `Generate`, under any policy and with none
  (`passthrough component "web": object Deployment "demo/web": undeclared field
  spec.template.spec.fieldOfALaterVersion: …`). A workload or a claim in which a value
  that serializes itself (a Go caller's `json.RawMessage`) repeats enough keys to fill the
  strict decode's record is refused on the same ground. An object of any other kind is
  emitted as authored, the field with it, whatever the strict decode says of it: nothing
  it sets is dropped, so the full-record refusal of `manifests` has no part here. The
  repeated keys that would fill the record are refused all the same, for the repeat itself
  (see the key given twice above).
  **Breaking** (go-kure/launcher#794, item 7): a workload or claim with an
  undeclared field was emitted unchecked before and no longer builds.
  `Generate` runs the check again on the object it is about to emit, once
  `ApplyPolicy` has supplied a policy, because `Object` is an exported field and the map
  checked need not be the map emitted; a refusal there is the same
  `*oam.ViolationError`, naming the component.

  What cannot be read is refused, not passed: an object of a registered kind that does not
  decode as that kind (`replicas: three`), a workload kind, a claim or a PersistentVolume
  in an API version the scheme does not register (`batch/v1beta1`, `apps/v1beta2`), a
  HorizontalPodAutoscaler in such a
  version whose `maxReplicas` is not an integer, and an object that does not serialize
  (a float or bool mapping key; an integer key is a string name, so `80: http` on a
  custom resource is checked and emitted).
  So is an object the decoder of its kind panics on, which crashed the build before: a
  `CiliumNetworkPolicy` or `CiliumClusterwideNetworkPolicy` whose `icmps` field leaves its
  `type` out, for one (`passthrough: object CiliumNetworkPolicy "demo/p": the object cannot
  be read, …: the decoder panicked on CiliumNetworkPolicy "demo/p": …`, the parse error kure
  gives such an object since go-kure/kure#1009). A MetalLB `BGPPeer` at `metallb.io/v1beta2`
  is a registered kind since go-kure/kure#1007, so one that does not decode as that kind
  (`holdTime: [1]`) is refused here too; it was emitted unread. Since go-kure/kure#1014 an
  object of a registered kind that states `Kind` or `apiversion` beside the exact key is
  refused here as well, with the parser's text for the cause (`the key "Kind" equals "kind"
  only after case folding; …`). **Breaking**: the `Kind` case was read as its Go type and
  checked, and so was the `apiversion` case where that key states a version the kind is
  registered in (`apiversion: v1` beside `apiVersion: v1`); with another version there it
  was unreadable before, for another cause. Every build
  decodes the object, since the transform applies `NoopPolicy` when no policy is passed;
  only a config no policy was applied to, one a Go caller builds
  outside the transform, emits the object undecoded, as authored.

  **Behaviour change:** before go-kure/launcher#794 a `passthrough` object reached the
  output unchecked. A document that relied on that no longer builds when its object is a
  workload with a privileged container, a host namespace, a hostPath volume, an image
  outside the registry allowlist, an untagged or `:latest` image, or a value over a
  maximum — and with no policy passed the first three are always refused, since
  `NoopPolicy` denies them. A consumer allows what it wants allowed through its policy,
  not per component: `AllowPrivileged()`, `AllowHostNetwork()`, `AllowHostPID()`,
  `AllowHostIPC()` and `AllowHostPathVolumes()` for the security flags, the registry in
  `AllowedRegistries()` (or no allowlist), the capability lists, and the maxima
  (`MaxReplicas()`, `MaxCPU()`, `MaxMemory()`, `MaxStorageSize()`). An untagged or
  `:latest` image has to be pinned in the object, and a workload in an API version the
  build cannot read has to be authored in the one it can (`batch/v1`, `apps/v1`).

  Not checked: an object of a kind not named above; a custom resource, the pods its controller
  creates and the replica count it sets; what a `Secret` holds (a core Secret is told by its
  group and kind alone, and refused only under a policy that forbids explicit secrets). A nil
  policy (a direct `ApplyPolicy(nil)`, or `Generate` on a config no policy was applied
  to) checks nothing, and `ApplyPolicy(nil)` withdraws no policy applied before.
- **crd / manifests** — `inline` xor `url`; `manifests` adds `scopeOverrides`
  (`apiVersion`/`kind`/`scope`), the author's explicit statement of a kind's scope.
  An override outranks kure's own non-API-governed scope-table entry — a kind
  nothing else can scope, e.g. a cluster-scoped custom resource whose CRD is
  installed out of band. It does **not** outrank a `CustomResourceDefinition`
  bundled in the same source: that CRD is the definition being applied, so it
  must agree with the override rather than be overridden by it — a disagreement
  is rejected (`manifests.go`'s `stampManifestNamespaces`, an error naming both
  the override's and the CRD's declared scope; the resolution itself is
  `resolveObjectScope`, which the `helmtemplate` component's `scopeOverrides`
  goes through too); an agreeing override still
  applies, and a kind with no same-source CRD falls back to the non-API-governed
  case above. It is also ignored
  for a kind the Kubernetes API itself governs (`isAPIGovernedScope`,
  `manifests.go`: a `CustomResourceDefinition` document; any kind whose kure
  scope-table entry comes from `ScopeSourceBuiltin` — i.e. from the generated
  upstream types, `PriorityClass`, `APIService` and the two webhook
  configurations among them; and any cluster-scoped API built-in kure fixes the
  scope of without registering it, a set that is empty at the kure version in
  `go.mod`. Such a kind is detected by
  asking `manifest.Scope` itself with no CRD context, not by copying kure's
  list, so kure stays the single answer); a manifest cannot redefine those. A kind with no override and
  no other scope source still fails closed when it carries no
  `metadata.namespace`. Conversely, a cluster-scoped object (either component,
  any scope source) that authors `metadata.namespace` is rejected rather than
  emitted as-is — the Kubernetes API forbids a namespace on a cluster-scoped
  object, so letting it through would only defer the failure to apply time.
  Both components decode a document of a registered kind into its Go type, with the
  decode `helmtemplate` gives a rendered chart, and treat a field that type does not
  declare — one a newer Kubernetes version added, say — as it does (`helmtemplate`'s
  *Undeclared fields*): a workload or a claim that sets one is refused, under any policy
  and with none (`manifest source: parse manifests: Deployment "demo/web": undeclared
  field spec.template.spec.fieldOfALaterVersion: …`), an inline source when the component
  is built and a url source when it is fetched; an object of any other registered kind is
  emitted as written, unstructured, the field kept, and a `CustomResourceDefinition` kept
  so still passes `crd` and still gives its scope to the resources beside it. **Breaking**
  (go-kure/launcher#794, item 7), in the same two ways: a source with such a workload or
  claim no longer builds, and the output of other kinds gains the fields that had been
  dropped. The same known limit applies (a key inside a type that unmarshals itself, a
  CRD's `items` schema for one, is still dropped). A document the decoder of its kind
  panics on is a build error naming the object, as it is there (`manifest source: parse
  manifests: parse error in Kubernetes object: failed to decode object: the decoder
  panicked on CiliumNetworkPolicy "demo/p": …`), and so is malformed JSON the decoder
  cannot read past, on which the build did not return before go-kure/kure#1012. A MetalLB
  `BGPPeer` at `metallb.io/v1beta2` is a registered kind since go-kure/kure#1007 and is
  read as one, with what that changes (`helmtemplate`'s *Rendered objects*).
  An integer the object holds as one is written with its own digits, here as everywhere
  (`9007199254740993` as `9007199254740993`; `helmtemplate`'s *Rendered objects*). In an
  unstructured object an integer an int64 cannot hold is a float from the decode on, and is
  written as that float.

  **Policy.** Every object a `manifests` or `crd` source yields is checked against the
  environment policy with the check template delivery (`helmtemplate`) and `passthrough`
  use: each Pod, PodTemplate, ReplicationController, Deployment, StatefulSet, DaemonSet,
  ReplicaSet, Job and CronJob is checked as an authored workload is (host namespaces,
  hostPath volumes, the cpu/memory maxima, the registry allowlist on every init and
  regular container's image and on an image volume's reference, the privileged,
  HostProcess and capability gates, `ValidateImageRef` on both, no ephemeral containers), a
  PersistentVolumeClaim and a StatefulSet's claim template are held to the storage
  maximum, and a replica count and a HorizontalPodAutoscaler's `maxReplicas` to the
  replica maximum. A PersistentVolume is held to what the `persistentvolume` kind holds
  its own to: a `hostPath` or `local` source needs `AllowHostPathVolumes()`, and
  `spec.capacity.storage` is held to the storage maximum (breaking for a source that
  holds one; see **persistentvolume**). An image volume from a registry outside
  the list is refused since go-kure/launcher#790 (**breaking** for a source that holds
  one; see *Image volumes* under **pod**). The error names the object and the field (`manifest source: object
  Deployment "demo/web": spec.template.spec.containers[0] "app": …`). A `crd` source
  holds only CustomResourceDefinitions, none of which the check reads, so `crd` builds as
  before.

  A core Secret a `manifests` source yields, `inline` or fetched, is refused under a policy
  that forbids explicit secrets (`oam.ExplicitSecretPolicy`), as the `secret` trait and a
  `passthrough` Secret are (go-kure/launcher#794, item 10): `manifest source: object Secret
  "demo/creds": the object is a Secret, and the environment policy forbids explicit
  secrets; …`. The check does not read what the Secret holds, and the message quotes
  nothing of it. **Breaking** only under a policy that answers `false`. **Not covered:** a
  Secret a chart renders under template delivery (`helmtemplate`, `helm` with
  `delivery: template`), whose content comes from the chart and its values; the document's
  own sensitive values are refused there where they are set (`secretValues`).

  When the check runs depends on the source. An `inline` source is checked by
  `ApplyPolicy`, the transform's policy step, and the refusal is that step's
  `*oam.ViolationError`. A `url` source is checked in `Generate`, on the objects it is
  about to emit: `ApplyPolicy` checks its host against the allowed registries and does
  not fetch, so **a caller that runs the transform and never generates does not see an
  object refusal for a `url` source**. A refusal in `Generate` is the same
  `*oam.ViolationError`, naming the component; a fetch or parse failure there keeps its
  own error and is not a policy violation. `Generate` also checks an `inline` source
  again, on what it emits.

  What cannot be read is refused, not passed: a workload, claim or PersistentVolume in
  an API version kure's scheme does not register (`batch/v1beta1`, `apps/v1beta2`),
  inside a list as outside one. A list is replaced by its items, each decoded and checked
  as a document of its own is, for a field its type does not declare too (`manifest
  source: parse manifests: item 0 of List: Deployment "demo/web": undeclared field …`):
  a `v1` `List`, a typed list (`DeploymentList`) and, since go-kure/kure#1014, a list of
  a kind the scheme does not register (a kind ending in `List` that states `items`),
  whose items of a registered kind are their Go types and are checked for real, a list
  among them opened in turn. The list handling is the one `helmtemplate`'s *Rendered
  objects* describes, with the same errors under `manifest source: parse manifests: …`:
  a label or an annotation on a list's own metadata, a `Kind`, `apiversion` or `Items`
  key, a `null` item, and an object of a kind that is no list with a top-level `items`
  array, which is refused with a policy or with none and is no policy violation.
  **Breaking** (go-kure/launcher#790): the table under `helmtemplate` lists what built
  before and is refused now.

  **Behaviour change:** before go-kure/launcher#794 the objects of a `manifests` source
  reached the output unchecked. A document that relied on that no longer builds when its
  source holds a workload with a privileged container, a host namespace, a hostPath
  volume, an image outside the registry allowlist, an untagged or `:latest` image, or a
  value over a maximum, or an object the check cannot read (above) — and with no policy
  passed the first three are always refused, since `NoopPolicy` denies them. A consumer
  allows what it wants allowed through its policy, not per component, exactly as for
  `passthrough`. An untagged or `:latest` image has to be pinned in the source,
  and a workload in an API version the build cannot read has to be authored in the one
  it can (`batch/v1`, `apps/v1`).

  Not checked: an object of a kind not named above; a custom resource, the pods its controller
  creates and the replica count it sets; what a `Secret` holds (a core Secret is told by its
  group and kind alone, and refused only under a policy that forbids explicit secrets). A nil
  policy (a direct `ApplyPolicy(nil)`, or `Generate` on a config no policy was applied
  to) checks nothing, and `ApplyPolicy(nil)` withdraws no policy applied before.

  A `url` that does not parse is refused without the URL or the parser's
  error, and a fetch error names the URL by scheme and host only
  (`manifestsource.go`'s `displayURL`; a URL with no host is not named): its
  userinfo, path or query may carry a credential, and an IPv6 zone in the host is
  dropped too. A refused redirect hop reads fixed text (`redirect to a url that is
  not http(s) refused`, `redirect to a host not in allowed registries refused`,
  `too many redirects`), since its target is the server's. A failed request or body read is
  named by fixed text only (`failureCause`): a timeout, a failed host lookup,
  or the failing network operation and its system error, such as
  `dial failed: connection refused`; anything else reads `request failed` or
  `response body could not be read`. The underlying error's own text is never
  shown, since it can quote the URL, a server-sent header or trailer, or a TLS
  certificate, but it stays the error's cause: `errors.Is` and `errors.As` still
  find it, so a timeout matches `context.DeadlineExceeded`.

## StatefulSet-level and claim-template properties

The `statefulset` kind projects the whole `appsv1.StatefulSetSpec` field set it
owns, and each `volumeClaimTemplates` entry projects the whole
`corev1.PersistentVolumeClaimSpec`. Every field below is written only when
authored, so an unauthored StatefulSet emits no `podManagementPolicy` at all and
an empty `updateStrategy: {}` (a non-pointer struct, which `omitempty` does not
suppress), leaving the apiserver to default both. Until go-kure/launcher#361
kure's `CreateStatefulSet` wrote `podManagementPolicy: OrderedReady` into the
object itself; the release-1 builder contract makes that constructor
identity-only, so the line is gone from the emitted manifest. The *effective*
policy is unchanged — `OrderedReady` is also the API default — but the manifest
text moved, which is why three goldens lost a `podManagementPolicy:
OrderedReady` line. The claim entry's five
pre-existing keys (`name`, `mountPath`, `size`, `storageClass`, `accessModes`)
now sit inside a closed key set, so a typo in an entry is reported instead of
being silently dropped. `devicePath` joined that set with go-kure/launcher#385.

Columns match the pod-level table above: **additive** means no document that
built before builds differently now; **behavior-changing** means an existing
document's meaning or acceptance moved.

| Property | Type | Effect | Kind |
|----------|------|--------|------|
| `podManagementPolicy` | enum | `OrderedReady` (also the apiserver's default for an unauthored StatefulSet, so authoring it is a no-op that only pins the value in the manifest) or `Parallel`. | additive |
| `updateStrategy{type, rollingUpdate{partition, maxUnavailable}}` | object | `type` is `RollingUpdate` or `OnDelete`; `rollingUpdate` is rejected under `OnDelete`, mirroring `ValidateStatefulSetSpec`. `type` is required only for an otherwise empty `updateStrategy: {}`, whose whole meaning would come from apiserver defaulting; when `rollingUpdate` is authored, `RollingUpdate` is inferred, since it is both the API default and the only type that reads the field. `partition` is `>= 0`. `maxUnavailable` takes a positive integer or a 1–100% string; the schema leaf is the `Types: [integer, string]` union (go-kure/launcher#383), so a schema-validating consumer accepts both forms. | additive |
| `revisionHistoryLimit` | int ≥ 0 | Retained controller revisions. | additive |
| `minReadySeconds` | int ≥ 0 | Readiness settling time before a pod counts as available. | additive |
| `persistentVolumeClaimRetentionPolicy{whenDeleted, whenScaled}` | object | Each is `Retain` or `Delete`. | additive |
| `ordinals{start}` | object | `start` shifts the replica ordinal range and is required once `ordinals` is authored; `>= 0`. | additive |
| `selector` (claim) | object | `matchLabels`/`matchExpressions`, with the operator arity rule — `In`/`NotIn` need at least one value, `Exists`/`DoesNotExist` none — and every `values` entry validated as a label value, the check `ValidateLabelSelectorRequirement` runs on a newly created claim template. An entirely empty `selector` is rejected: the apiserver would accept it as matching every volume, which is never what an author who wrote the key meant. **Authoring a selector opts the claim out of dynamic provisioning**: a claim with a non-empty selector is never provisioned from its `StorageClass` and stays `Pending` until a pre-provisioned PV matches ([Kubernetes: persistent volumes — Selector](https://kubernetes.io/docs/concepts/storage/persistent-volumes/#selector)). | additive |
| `resources{requests,limits}` (claim) | object | `storage` is the only accepted resource name — `ValidatePersistentVolumeClaimSpec` reads `requests[storage]` and nothing else, so any other name would be silently ignored. Quantities must be positive. `apply` *merges* `requests` onto the claim-template literal `createStatefulSet` already built (`statefulset.go`, which writes `requests.storage` from `size`), so `size` survives when only `limits` is authored. `requests.storage` is the long spelling of `size`; authoring both is an error. | additive |
| `volumeMode` (claim) | enum | `Filesystem` or `Block`. `Filesystem` pairs with `mountPath`; `Block` pairs with `devicePath` and renders a main-container `volumeDevices` entry instead of a `volumeMount` (go-kure/launcher#385, see "Raw block volumes"). Until then `Block` was rejected, because every claim was mounted as a filesystem and the apiserver would have accepted the mismatched claim/pod pair. | additive |
| `devicePath` (claim) | string | Where a `volumeMode: Block` claim appears in the main container as a raw block device. Not a claim-spec field. Exactly one of `mountPath` and `devicePath` is required, and `devicePath` only with `volumeMode: Block`. | additive |
| `dataSourceRef{apiGroup,kind,name,namespace}` (claim) | object | Mirrors upstream `validateDataSourceRef`: `kind` and `name` are required non-empty with no format rule (a Kind is a CamelCase identifier, not a DNS name), `apiGroup` must be a DNS-1123 subdomain when non-empty, an omitted or empty `apiGroup` pins `kind` to `PersistentVolumeClaim` (the core group holds no other populator), and `namespace`, when set, is a DNS-1123 *label* (`ValidateNamespaceName`), not a subdomain. | additive |
| `volumeAttributesClassName` (claim) | string | DNS-1123 subdomain naming a `VolumeAttributesClass`. | additive |
| `storageClass` (claim) | string | **Behavior-changing.** A non-empty value is now validated as a DNS-1123 subdomain (`ValidateClassName`, the check `ValidatePersistentVolumeClaimSpec` runs and the one the `volumes[].pvc` path already applied). An invalid class name previously built a claim and was refused later by the apiserver; it is now refused here. A present-but-non-string value is likewise rejected rather than read as absent and provisioned through the cluster default class. | behavior-changing |
| `storageClass` (claim) — `""` and the `pvc` capability | string | **Pre-GA output change** (go-kure/launcher#761). An authored `storageClass: ""` now requests no class: the claim template carries `storageClassName: ""`, as a `volumes[].pvc` claim and the `persistentvolumeclaim` kind do. It used to read as absent, so the template carried no class and the cluster's default applied. An unauthored entry (absent or `null`) now takes the ClusterProfile `pvc` capability's `storageClassName`, as an authored `pvc` trait does: the handler implements `ComponentCapabilityFiller`, reads the binding through `LoweringContext.Capability` once the component has an entry, so `pvc` is in `ConsumedCapabilities` exactly then, and fills each unauthored entry. An authored class, `""` included, wins; a non-string rendering value is refused. Either change alters an existing StatefulSet's `volumeClaimTemplates`, which the apiserver refuses to update: the StatefulSet fails to apply until it is recreated (the `force-replace` trait recreates it on apply, restarting its pods). The claims it already made keep their class, unless the retention policy deletes them with it. | behavior-changing |
| `size` (claim) | quantity | **Behavior-changing.** `size` is now the short spelling of `resources.requests.storage` rather than a key of its own, and it is no longer unconditionally required — either spelling satisfies the requirement, and authoring both is an error. It must also now be **positive**: `0` was accepted before and is rejected now, matching upstream's `ValidatePositiveQuantityValue` on `requests[storage]`. (The `volumes[].pvc.size` surface on every kind applies the same positivity rule since go-kure/launcher#384.) Because "authored" is now read through `parseStringField`, an explicitly empty `size: ""` no longer counts as authoring the short spelling, so pairing it with `resources.requests.storage` is accepted rather than rejected as a double spelling. | behavior-changing |
| any unrecognized key on a claim entry | — | **Behavior-changing.** A `volumeClaimTemplates` entry is now a closed key set. The previous parser called no `rejectUnknownKeys` at all on the entry map, so a misspelled `storageClassName` was dropped and the claim was provisioned through the cluster default class with nothing said. Such a document built before and errors now, which is a break of the additive test in `docs/oam/design-gvk.md` under an unchanged `launcher.gokure.dev/v1alpha1`. It is taken deliberately and on precedent: `6aed090` (`feat(format): enforce policy and support securityContext on init/sidecar containers`) shipped the same tightening on init/sidecar container entries. The `format` commit scope is what signals it — `cliff.toml` groups those commits under a **Document Format** changelog heading. | behavior-changing |
| any optional field above, authored as null | — | **Not a wrong type — absence.** Every optional StatefulSet-level and claim-level field added here reads an explicit null as omission, so `updateStrategy:` with nothing after it builds exactly as omitting the key does. This matters beyond hand-written YAML: a lowering rule may emit a nil, `pkg/oam`'s emission validation accepts it ("a nil under an optional field constrains nothing"), and a parser that answered "must be an object, got `<nil>`" would let a component satisfy the published schema and then fail conversion. Where the null lands on a field that is *required* once its parent is authored (`ordinals.start`, a claim's `name`), it surfaces as the requiredness error, never as a type error. This covers the optional lists as well as the scalars and objects (`selector.matchExpressions`, a match expression's `values`), and it covers a **typed** nil — `map[string]any(nil)` or `[]any(nil)` inside an `any`, which is what a lowering rule assembled in Go produces for an unset optional and is not `== nil` — because `pkg/oam`'s `isNullValue` classifies those as null too. A key the schema rejects outright is unaffected: authoring it as null still earns the explanatory refusal rather than silence. go-kure/launcher#394 extended the same treatment to the shared field helpers and to the parsers it converted, so it reaches well beyond these fields — but not the whole package. The lifecycle and probe handler keys, where presence itself is the rule being enforced, refuse a null; since go-kure/launcher#570 a container's resource quantities under any name and the top-level keys once read with a bare lookup (listed with the `null` row of the `deployment` table above) read it as omission. | additive |
| `volumeClaimTemplates` itself — wrong type | list | **Behavior-changing.** The previous parser answered "is it present" and "is it a list" with one type assertion, so `volumeClaimTemplates: {name: data}` or a stray scalar was indistinguishable from an absent key: the StatefulSet was built with no claim templates and nothing was reported. A present-but-non-list value is now rejected naming the type received. **An explicit null is absence, not a wrong type** — `volumeClaimTemplates:` with no value decodes to nil, the key is optional in the schema, and `pkg/oam`'s own requiredness check (`isNullValue`) already reads nil as absent, so a document written that way is valid `launcher.gokure.dev/v1alpha1` today and keeps building. | behavior-changing |
| `name`, `mountPath` (claim) — collisions | string | **Behavior-changing.** A claim template's `name` must differ from every other claim template's and every `volumes` entry's, and its `mountPath` from every other main-container `mountPath`, claim templates and `volumes` together. Each parser used to check only its own entries, and the apiserver does not catch the overlap: StatefulSet validation skips the pod template's volumes. The controller keys claim templates by name, so of two sharing one only one claim was created, and it replaced a pod volume named like a template with that template's claim, so the authored volume was never mounted. A repeated `mountPath` built and then failed pod creation. Such documents now fail at build time. The `configmap` and `external-secret` traits refuse a volume named like a claim template for the same reason. | behavior-changing |
| `name`, `mountPath`, `size` (claim) — wrong type | string | **Behavior-changing.** All three previously read through a bare type assertion, so a present-but-non-string value (YAML parses `size: 10e9` as a float and `mountPath: 7` as an int) collapsed to the empty string and was reported as the *requiredness* error — "missing required field 'size'" for a field the author had written. Each now rejects with an explicit type error naming the field and the type received. Acceptance does not widen: every one of these documents failed before and fails now, but with a message that points at the real defect. | behavior-changing |

Deliberately **not** accepted — each is rejected with an error naming the
reason rather than silently ignored:

- `spec.selector` (StatefulSet-level) — the builder derives it from the
  component name, and a StatefulSet's selector is immutable after creation.
- `spec.template` (StatefulSet-level, go-kure/launcher#790) — the pod template
  is projected from the component's own container and pod-level properties, as
  on `deployment`. The parser dropped an authored one before.
- `volumeName` (claim) — pre-binding a claim *template* to one named
  PersistentVolume would point every replica at the same volume.
- `volumeMount` (claim) — not a claim-spec field at all; the container mount
  is authored as `mountPath` (or, for a `volumeMode: Block` claim,
  `devicePath`) on the same entry.
- `dataSource` (claim) — when `dataSourceRef` carries no `namespace` the
  apiserver mirrors it into the superseded `dataSource` field, so authoring
  both is redundant; when it does carry a `namespace` the apiserver does not
  mirror, and `dataSource` must stay empty. Either way the field is authored
  through `dataSourceRef` alone.

The three claim-level refusals sit inside a `volumeClaimTemplates` entry, so
their reason reaches a caller of the handler; in a document the
authored-property check refuses the key first, with its own text and no reason
(see the section below).

## Upstream fields a hand-parsed kind does not read

Ten kinds read their properties key by key instead of decoding them into the
Kubernetes type: `deployment`, `statefulset`, `daemonset`, `job`, `cronjob`,
`service`, `persistentvolumeclaim`, `configmap`, `secret` and `serviceaccount`. The first
five build pods and are "the five pod-building kinds" below. For such
a kind an upstream field its parser does not know is no decode error; it is a
key the parser never looks at. The rule that closes that gap
(go-kure/launcher#790):

> Every field of the upstream type that such a kind does not read, under its
> name or in another shape, is an entry of one of the kind's refusal maps with
> its reason. No upstream field is dropped in silence.

`webservice` and `worker` lower to a `deployment` and refuse what it refuses;
the `pvc` trait reads a claim through the `persistentvolumeclaim` kind's
parser and refuses what that kind refuses, and the `secret` trait reads its
Secret through the `secret` kind's parser (`ParseSecretProperties`).

### The two paths

An authored key reaches a kind on one of two paths, and both give the reason:

- **A caller of the handler** (`ToApplicationConfig`, or `Transform` without
  the check below) gets the reason alone:
  `scheduling: not read by this component — alpha upstream, behind the WorkloadWithJob feature gate`.
- **A document** goes through the authored-property check first
  (`Transformer.ValidateAuthoredProperties`, which `kurel build` calls). The
  check refuses a key the kind does not declare before the parser runs, and
  appends the kind's reason to its own text:
  `component "batch" (type "job"): properties: unsupported field "scheduling" (allowed: …); scheduling: not read by this component — …`.
  The kind gives the reason through `UnsupportedFieldHint`
  (`pkg/oam`'s README, "A reason on a refused key").

### Below the top level

`UnsupportedFieldHint` answers for a kind's **top-level** keys. A refusal that
sits inside a property the kind declares reaches a caller of the handler with
its reason. In a document, the check refuses the key at that position with its
own text and appends the same reason, which the kind gives through
`NestedUnsupportedFieldHint` (go-kure/launcher#790):
`properties.affinity: unsupported field "nodeAffinity" (allowed: …); affinity.nodeAffinity: not read — …`.
The positions are:

| Position | On | Keys |
|----------|----|------|
| a container's `resources`: the main container's, and an `initContainers` or `sidecars` entry's | every workload kind | `claims` |
| the pod's `podResources` | every workload kind | `claims` (upstream validation forbids claims at pod level: "claims may not be set for Resources at pod-level") |
| the `affinity` shorthand | `webservice`, `worker` | `nodeAffinity`, `podAffinity`, `podAntiAffinity` |
| a `volumeClaimTemplates` entry | `statefulset` | `volumeName`, `dataSource`, `volumeMount` |

A key that is **no field of the upstream type** is not in a refusal map. The
check refuses it as it refuses any undeclared key, with nothing appended; a
caller that skips the check has it dropped, as `pkg/oam`'s README states for
every handler. The one exception is the six `CronJobSpec` keys `job` refuses by
name, which a `cronjob` document retyped to `job` leaves behind.

### Refused, with the reason

| Key | On | Why |
|-----|----|-----|
| `selector` | `deployment`, `statefulset`, `daemonset` (`webservice`, `worker`) | Builder-managed (`app: <component>`), equal to the template labels, immutable once created. |
| `selector`, `manualSelector` | `job`, `cronjob` | The job controller generates the selector from a per-job label; a hand-written one adopts another job's pods. |
| `scheduling` | `job`, `cronjob` | Alpha upstream, behind the `WorkloadWithJob` feature gate. |
| `restartPolicy`, `activeDeadlineSeconds` (the pod's) | `deployment`, `statefulset`, `daemonset` (`webservice`, `worker`) | apps/v1 validation accepts only `Always`, and no deadline, on these pod templates. On `job` and `cronjob` both names are read: the pod's `restartPolicy`, and the JobSpec's `activeDeadlineSeconds`. |
| `ephemeralContainers`, `priority`, `overhead`, `serviceAccount` | every workload kind | See [Pod-level properties](#pod-level-properties). |
| `evictionResponders` | every workload kind | Alpha upstream, behind the `EvictionRequestAPI` feature gate. |
| `restartPolicyRules` (the main container's) | every workload kind | Upstream accepts a container's restart rules only with the container's own `restartPolicy`, which no kind reads on the main container. An `initContainers` entry reads both (see [Container fields](#container-fields)). |
| `externalIPs`, `clusterIPs` | `service` | See the `service` kind. |
| `dataSource` | `persistentvolumeclaim`, the `pvc` trait | Authored through `dataSourceRef`. |
| `secrets` | `serviceaccount` | The list limits mountable Secrets only under an annotation upstream deprecates since Kubernetes 1.32; it is no way to find or create a token. |
| `resources.claims` | a container's `resources`, on every kind that reads them | An entry names one of the pod's `resourceClaims`, and a container's resources are read without them; upstream puts the field behind the `DynamicResourceAllocation` feature gate. See [Below the top level](#below-the-top-level). |
| `podResources.claims` | every workload kind | Upstream validation forbids claims in a pod's own resources ("claims may not be set for Resources at pod-level"); the pod's claims are its `resourceClaims`. See [Below the top level](#below-the-top-level). |

### Read in another shape

These upstream names are free on the kind and are refused too, with a reason
that names the properties the field is authored as:

| Upstream field | On | Authored as |
|----------------|----|-------------|
| the pod `template` | the five pod-building kinds | The component's own container and pod-level properties. Its metadata is not authored either: the kind's builder writes the `app: <component>` label and nothing else, the transform then adds the component label (`<domain>/component`, see [The `app` label](#the-app-label)), and a trait may add more. |
| `jobTemplate` | `cronjob` | The component's own job-level, pod-level and container properties. |
| `containers` | the five pod-building kinds | The main container is the component's container properties; further ones are `sidecars` entries on the kinds that have them. |
| the main container's `name` | the five pod-building kinds | Not authored: the container is named after the component. |
| `livenessProbe`, `readinessProbe`, `startupProbe` | the five pod-building kinds | `probes.liveness`, `probes.readiness`, `probes.startup`. |
| `volumeMounts`, `volumeDevices` | the five pod-building kinds | A `volumes` entry's `mountPath`, or its `devicePath` for a `volumeMode: Block` claim. |
| `resources` (the claim's) | `persistentvolumeclaim`, the `pvc` trait | `size`, the storage request. A claim's limits are not read. |

Three pod fields are read under another name, because their upstream name is a
property of the kind already: `securityContext` as `podSecurityContext` and
`resources` as `podResources` (both names are the main container's), and on
`job` and `cronjob` the pod's `activeDeadlineSeconds` as
`podActiveDeadlineSeconds` (the name is the JobSpec's).

### One level down

Inside a container, go-kure/launcher#790 reads `securityContext.windowsOptions`
on every container, and an init container's own `restartPolicy` and
`restartPolicyRules` (see [Container fields](#container-fields)). Three upstream
fields one level down stay unread, and are refused at their position as an
unknown key:

- **`lifecycle.stopSignal`**: upstream puts it behind the `ContainerStopSignals`
  feature gate, alpha and off by default through Kubernetes 1.37, and holds the
  signal to the pod's `os.name`. A container's `lifecycle` reads `postStart`
  and `preStop`.
- **`ports[].hostPort` and `ports[].hostIP`**: they bind a port on the node.
  No environment-policy switch covers that (`AllowHostNetwork()` gates the
  node's network namespace, not a port bound from the pod's own), and whether
  the port is free is the scheduler's question, on cluster state. A `ports`
  entry reads `containerPort`, `name` and `protocol`.

### Not authorable, and not refusable by name

One case has no key a refusal could sit on: a name taken on this kind by
another upstream field of the same name. On `job` and `cronjob`,
`restartPolicy` is the pod's, so the main container's own `restartPolicy`
cannot be authored and cannot be refused. On `cronjob`, `suspend` is the
CronJob's, so the job template's `suspend` cannot either (see "One JobSpec
field is deliberately not shared" above).

### Outside the properties

The object's envelope is not a property of the kind's own schema: its
`apiVersion` and `kind` are the component's type, its `metadata` is the
component's name and the `objectName`, `labels` and `annotations` properties
every kind takes, and `status` is the cluster's. `spec` is the level whose
fields the properties are, and has no key of its own.

### An explicit null on a refused key

A null carries no content, and the kinds differ on what the parser makes of it
on a key they refuse. This is each kind's behavior from before
go-kure/launcher#790, left as it was:

- **Read as unauthored:** `service` and `persistentvolumeclaim` on every key
  they refuse, and `deployment` on the pod-level and container-level keys (not
  on `selector` and `template`).
- **Refused, as any value is:** `statefulset`, `daemonset`, `job`, `cronjob`,
  `serviceaccount`, and `webservice` and `worker`; and every kind on
  `resources.claims`, on `podResources.claims` and on the keys refused under
  the `affinity` shorthand.

In a document the difference does not show: the authored-property check
refuses an undeclared key whatever its value, null included.

### What holds this

- `TestHandParsedKinds_CoverEveryUpstreamField` walks the upstream type of
  each of the ten kinds, from the object down every level whose fields are
  the component's properties, and fails on a field that is neither read, nor
  refused, nor placed in one of the classes above — so a field a later
  `k8s.io/api` adds cannot be dropped in silence. It stops at the properties:
  what a property holds inside is held to the upstream type only for a
  container's `resources` and the pod's `podResources`.
- `TestRefusedKeys_BothPathsGiveTheReason` holds every entry of every refusal
  map to the two texts above; `TestRefusedKeys_OneLevelDown` holds the two
  refusals inside a declared property.
- `TestHandParsedKinds_MatchExpressionsAtEverySelector` runs a match
  expression through each of the 47 label selectors these kinds read, to the
  generated object, and five defective ones to their refusal.
- `TestHandParsedKinds_TemplateMetadataAndContainerName` and
  `TestHandParsedKinds_ServicePortFieldsReachTheObject` pin the template's
  metadata, the main container's name and the six fields of a Service port.

## Extending

Custom component types implement `oam.ComponentHandler` (`CanHandle` +
`ToApplicationConfig`) and are registered alongside the built-ins. Exported helpers:
`ValidateImageRef` (image policy), `BuildPVC` (PVC from a `PVCConfig`), and the
`persistentvolumeclaim` kind's claim path, `ParseClaimProperties`,
`ApplyClaimPolicy` and `GenerateClaim`, which the `pvc` trait builds through. A custom
`Generate()` that builds standalone PVCs from a `PVCConfig` list should qualify their
names with the component name the way the role kinds do — see `roleClaims`
(unexported, `role_members.go`) — to avoid two components colliding on the same
pod-local volume name.

See [pkg.go.dev](https://pkg.go.dev/github.com/go-kure/launcher/pkg/oam/builtin/components)
for the full type/field reference, the [OAM model](https://pkg.go.dev/github.com/go-kure/launcher/pkg/oam)
for the handler interfaces, and `examples/` for runnable applications.

## The null contract

Every table above answers "what does an explicit `null` mean here?" one field at a
time. This is the rule they are all instances of:

> A value that serializes to JSON null is absent, at every depth, on every path; a
> null is never a member of any `Items` type, so a null array element is a type
> error; reservation is about the KEY being written.

It is written down because "null" has several readers — the emission validator's
requiredness check and its optional-key strip, its array-element guard,
`enforcePlatformReserved`, this package's parsers, and the Kubernetes API itself —
and each was aligned separately, so each round of review found the reader the last
round had not touched.

What it means for a handler parser in this package: read a null under an optional
property as omission, never as a present value of the wrong type. `authoredValue` —
the presence primitive `parseStringField`, `parseObjectField`, `parseInt32Field`,
`parseInt64Field`, `parseObjectList` and `parseStringList` all go through — does
exactly that, so calling any of them directly already complies. They classify a
**typed** nil as null too — `map[string]any(nil)` or `[]any(nil)` inside an `any`,
which is what a lowering rule assembled in Go produces for an unset optional and is
not `== nil`.

This used to require a separate `optional*` wrapper family (`optionalString`,
`optionalObject`, `optionalInt32`, `optionalInt64`, `optionalObjectList`,
`optionalStringList`) layered in front of the plain parsers. `go-kure/launcher#394`
moved the handling into the shared helpers themselves, which made every wrapper an
exact no-op forward; they were removed and every caller now uses the `parse*` helper
directly.

`TestNoOptionalFieldWrappers` (`no_optional_wrappers_internal_test.go`) keeps them
removed: it fails on any function or method whose name starts with `optional`. It
parses every production `.go` file directly in this package's directory — every
file, not only `common.go`, and never a `_test.go` file or a subdirectory — and
fails if it parsed none, so a wrong working directory cannot pass it vacuously. A
new optional field is read with the helper for its type instead: `parseStringField`
(or `requiredStringField` when the field is required), `parseBoolField`,
`parseInt32Field`, `parseInt64Field`, `parseObjectField`, `parseObjectList` or
`parseStringList`. Each already reads an explicit null as absence through
`authoredValue`, so a wrapper would only repeat that check.

Two limits worth knowing before relying on the rule:

- **A parser that answers presence with a bare `v, ok := props[key]` does not comply
  on its own.** It reads a present null as present. Emission validation deletes such
  keys before a rule-emitted component is converted, so on that path those parsers
  agree with the contract *because of the strip*, not independently of it. The
  authored path has no top-level strip: `ValidateAuthoredProperties` checks shape and
  leaves a top-level null in place, so there the null reaches the parser. What it
  does next depends on how the parser consumes the lookup: one that type-checks the
  value refuses it as a wrong type, while one that discards a failed assertion treats
  it as absent without saying so. `go-kure/launcher#423`, `go-kure/launcher#394` and `go-kure/launcher#570`
  converted the parsers they covered; the last of them are listed with the `null`
  row of the `deployment` table above.
- **An empty object is not a null and is not absent.** `{}` and an absent key mean
  different things to Kubernetes wherever a `LabelSelector` is involved, so the
  contract must never be read as licence to collapse them. What each one *means* is
  per field, not a property of selectors in general — the two selectors in a single
  `corev1.PodAffinityTerm` disagree, in upstream's own words:

  | field | null | `{}` |
  |---|---|---|
  | `labelSelector` | "matches with no Pods" | matches every pod in scope |
  | `namespaceSelector` | "this pod's namespace" | "matches all namespaces" |

  So "empty matches everything, nil matches nothing" holds for `labelSelector` and is
  wrong for `namespaceSelector`, where nil is the *narrowest* answer rather than the
  emptiest one.

Both limits met on one key, and it was the second row of that table.
`namespaceSelector` is parsed twice in this repository: here, for pod affinity,
through `parseSchedulingSelector` (`parseObjectField`), where any null is
omission — which upstream reads as "this pod's namespace"; and in the
networkpolicy trait, through `parseNPPeer`. A *typed* nil used to satisfy the
networkpolicy trait's own type assertion and yield an empty selector — all
namespaces, the widest possible answer — while an untyped nil already read as
omission there, so one input landed on opposite ends of the range depending on
which Go nil shape produced it. Both parsers now route their presence check
through `oam.IsNullValue`'s reflect-based classification, which treats a typed
nil the same as an untyped one, so the two halves converge: a null
`namespaceSelector`, however it was constructed, is omission on both paths.
Fixed as `go-kure/launcher#430`; `networkpolicy_internal_test.go`'s
`TestParseNPPeer_NamespaceSelectorPresenceCases` pins the typed-nil case
directly.

The same typed-nil shape reached **list elements**. `parseObjectList` read a
null key as absence, but a typed-nil *element* (`[]any{map[string]any(nil)}`)
passed its per-element assertion as an empty object, where an untyped null
element is refused (`tolerations[0]: must be an object, got <nil>`). Most
callers then failed on a missing required field anyway. `tolerations` did not:
an empty entry became an `Exists` toleration with no key, which tolerates
**every** taint. The element is now normalised to a null first, so every caller
refuses both shapes identically (go-kure/launcher#465). The list readers that
do not go through `parseObjectList` (`envFrom`, `httpHeaders`, `volumes`,
`volumeMounts`, sidecar `ports`, the Job `successPolicy`/`podFailurePolicy` rules and
`onPodConditions`, `volumeClaimTemplates`, `valuesFrom`, `scopeOverrides`) pass each
element through `nullElem` first, for the same untyped refusal and message.

## Two spellings of one field

`encoding/json` matches field names ignoring case, so a property map that writes one field in
two spellings (`suspend` and `Suspend`) would build with one value and drop the other without a
message. Every kind that decodes through `builtin.DecodeStrictJSON` refuses such a map, at any
depth, naming both keys: `<later>: sets the same field as <earlier> (field names match
case-insensitively, so one value would be dropped)`. Keys compare in byte order, at the first
byte where they differ. An upper-case ASCII letter sorts before its lower-case letter, and a
non-ASCII character sorts after every ASCII character. So in `Suspend` and `suspend` the later key
is `suspend`, and with the Kelvin sign (U+212A) in place of `k` the Kelvin spelling is the later
key. A field tagged `case:strict` is filled only by a key spelled exactly as its name, so the
refusal pairs no other spelling with it. The v1 decoder (`GOEXPERIMENT=nojsonv2`) ignores that
option. The refusal comes after the strict decode, so an unknown
key or a wrong type is still the first error, and a null never forms a pair.

The same decode refuses a key given twice in one JSON object, at any depth, with `<path> is given
more than once in the same object; give each key once`. A map cannot hold a key twice, but a
value that serializes itself, such as a `json.RawMessage`, can, and the decoder would merge its
occurrences of an object member or keep the last scalar without a message. This refusal comes
before the pair walk.

`kurel build` reaches the decode only after the authored-property check, which holds each
top-level key to the kind's schema by exact name. A top-level pair therefore stops earlier there:
the spelling the schema does not declare is refused as `unsupported field "Suspend" (allowed: ...)`,
and the "sets the same field" sentence is the refusal of a pair below the top level, inside an
object the schema leaves open (`ref.branch` and `ref.Branch`). Both refusals are build errors.
A caller that decodes without that check (a test, a library use) meets the sentence at the top
level too.

The kinds that go through `decodeKindSpec` and `cnpg-cluster` return that sentence alone, as
they did when their own check found the pair. A kind that decodes by itself keeps the wrapper it
adds to a decode error: `gitrepository: properties do not decode as a GitRepositorySpec: <sentence>`,
`helmrelease: properties do not decode as a HelmReleaseSpec: <sentence>`, and the same for
`bucket`, `helmchart`, `helmrepository`, `ocirepository`, `fluxcd-kustomization`, `helm` and
`helmtemplate` below their guarded levels. Before, those documents built. The `cilium-networkpolicy`
trait refuses a rule that writes one field in two spellings too, **even when both spellings are
complete**: it used to hold each spelling to the required list and build. The `helmrelease` verify
check still refuses a pair in which one spelling is null, which the decode lets through
(`foldedKey`).

The required-field check that `requiredfields` runs reads every spelling of a field, as before; the
decode refuses a pair before it is reached on the paths above, and a path that does not decode
through `DecodeStrictJSON` keeps its own reading.

## Conventions

Handlers use `k8s.io/api` constants for well-known Kubernetes enum values (access
modes, restart policies, etc.) rather than string literals — never re-define values
that already exist upstream.

Every generated object owns its label maps. `Generate` hands back objects the caller
owns and routinely edits — stamping ownership, environment or version labels onto the
workloads it emits — so no two fields and no two objects ever share one
`map[string]string`. Concretely: the object's `metadata.labels`, the pod template's
`metadata.labels`, the Service selector and the `labelSelector` of every topology
spread constraint and pod anti-affinity term are separate maps with equal contents.
Inside this package `appLabels` and `selectorFrom` (both unexported, `common.go`) are
what enforce that: call `appLabels` once per assignment rather than hoisting its result
into a variable used twice, and let `selectorFrom` copy what it is handed rather than
store it. A custom handler outside this package cannot call either and does not need to
— the rule is to build a fresh `map[string]string` at each assignment site, and to
`maps.Clone` any map before storing it in a selector.

This is a correctness rule, not tidiness. A selector that aliases the pod template's
labels follows the caller's edits, and a topology spread constraint's `labelSelector`
defines the pod set over which skew is computed — so a version label leaking into it
makes a rollout spread each version separately instead of spreading the workload.

The same rule runs in the other direction: **a generated object never shares a pointer
or a map with the config that produced it.** A handler config is reusable — the same one
can be rendered more than once — so every value a `*SpecConfig.apply` projects onto a
generated object is deep-copied on the way out: the claim template's `selector`,
`resources.limits`, `dataSourceRef`, `volumeMode` and `volumeAttributesClassName`, and at
the workload level `updateStrategy` (whose struct holds a pointer, so `*c.UpdateStrategy`
alone would not be enough), `revisionHistoryLimit`, `persistentVolumeClaimRetentionPolicy`
and `ordinals`. The deployment kind projects the same two shapes and follows the same rule:
`strategy` (holding a `*RollingUpdateDeployment`), `revisionHistoryLimit` and
`progressDeadlineSeconds`. `applyJobSpec` follows the same rule for the `job` and `cronjob`
kinds: its ten scalar pointers go through the generic `copyPtr`, and `successPolicy` and
`podFailurePolicy` — whose structs each own a slice of rules — through `DeepCopy`.
The pod template every workload kind shares follows it too: `buildPodSpec` starts from a
`DeepCopy` of the authored pod-level fields (`nodeSelector`, `podSecurityContext`,
`terminationGracePeriodSeconds`, `imagePullSecrets`, …) and deep-copies each volume,
toleration, topology spread constraint, the affinity, and the main, init and sidecar
containers as it adds them — an `append` alone copies the element structs but shares the
pointers, maps and slices they carry. The `postgresql` Cluster follows it for
`spec.inheritedMetadata`: its `labels` and `annotations` are clones of the config's
`InheritedLabels`/`InheritedAnnotations`, not the maps themselves
(go-kure/launcher#396). Without that, editing the first rendered object — the same in-place
customization the label rule above assumes — writes back into the config and reappears in
every later render, with the symptom surfacing on a different object than the one that was
edited.

### The `app` label

Every `app` label and `app` selector this package generates to identify a component is
valued at `oam.ComponentLabelValue(<component>)`, never the raw component name —
`appLabels` and `deploymentComponentLabels` call it, and nothing else writes the value.
Authored values are emitted as written: an authored `selector` on a `service` component
replaces the generated one and is not projected, and a type that emits authored objects
(`passthrough`, for one) adds no `app` label. The one authored value that is checked is
an `app` label on the pod template of a `replicaset` or `replicationcontroller` component, which gets the generated
label beside its authored ones: the component's own label value is kept, another is
refused. A component name
is a DNS-1123 subdomain (up to 253 characters), a label value at most 63: the function
returns a name of 63 characters or fewer unchanged, so output for those names is
byte-identical, and projects a longer one onto a readable prefix of at most 52 characters
(its first 52, with trailing `-` and `.` trimmed) plus `-` and
a 10-hex-character sha256 digest (go-kure/launcher#572). The workload kinds and `service` never reach
the projection, since their container name or Service name already refuses a name over 63
characters; the `helm` values ConfigMap and values Secret do (a values
ConfigMap that previously omitted `app` past 63 characters now carries the projected value). Object names are not projected. A custom handler
that labels its objects by component uses the same function, so its selectors and the
built-in traits' selectors (a PodDisruptionBudget, a NetworkPolicy `podSelector`) agree.

The component label (`<domain>/component`) is not this package's to write: the transform
adds it to every object a component's config generates, and to its pod templates, with the
authored component's value, where the key is absent (go-kure/launcher#788). Where the key is
there with another value, the transform's output is refused when it is generated
(go-kure/launcher#790): that holds for what a component type of this package emits from
authored objects or a rendered chart (`passthrough`, `manifests`, `helmtemplate`, `helm`
under template delivery) as for any other, so the key is one nothing else writes. A lowered part
named differently from its component (the `postgresql` pooler, a database, an object store)
carries the component's value, not its own name. A source a lowering rule generates and the
application bundle holds (the repository a `helm` component generates) carries none. A
`HelmRelease` also gets a post-renderer that sets the label on the chart's pod templates.
See "Component label and ownership" in the
[OAM model](https://pkg.go.dev/github.com/go-kure/launcher/pkg/oam).

### The object name (`objectName`)

Every kind component takes `objectName`, which names its one object in place of the component
name (go-kure/launcher#787): every component type with a handler
(`builtinComponentHandlers`) but four, as `TestObjectName_EveryComponentTypeChooses`
(`pkg/cmd/kurel`) holds; a type that is lowered to others has none. `helmtemplate`,
`manifests`, `crd` and `passthrough` generate no
single object named after the component and refuse it. The rules for the name, the `Naming`
hook's role `object` and the references that follow it are under "`objectName`: the object of
a kind component" in the
[OAM model](https://pkg.go.dev/github.com/go-kure/launcher/pkg/oam).

The property is not in any handler's `PropertySchema`: the engine adds it to the schema of
every type whose handler declares its object (`ComponentObject`, in `object_name.go` or, for
a kind added since, in the kind's own file),
reads it, and hands `ToApplicationConfig` the resolved name as `Component.ObjectName()`. A
handler's config carries it as `ObjectName`, empty when it is the component's name, and
`Generate` names the object with it. Everything else the handler writes keeps the component
name: the `app` label and selectors, the pod template's labels, the main container's name;
the one exception is the pod selector of a `cnpg-cluster` or `cnpg-pooler` endpoint, below.
A handler's own check of the name (a Service's DNS-1123 label, a CloudNativePG Cluster's
length, a CronJob's 52 characters, a Job's 63 and the room an Indexed job's last index
needs) runs on the object name, since that is what the object carries.

What a config tells a trait or the transform about its object follows the object name: a
`service` component's `BackendServiceName`, a `serviceaccount` component's
`ServiceAccountName`, and the pod selector of a `cnpg-cluster` or `cnpg-pooler` endpoint. Two
consequences are the kind's own: a `statefulset`'s pods and claims take the StatefulSet's
name, so they follow `objectName`; a `helmrelease`'s `spec.releaseName` default stays the
component name, so the release does not.

### The object's labels and annotations (`labels`, `annotations`)

Every kind component, the same types that take `objectName`, takes two optional properties,
`labels` and `annotations`, each a map of strings (go-kure/launcher#790). They go on the
metadata of the component's one object and nowhere else. On a workload kind the pod template
keeps the labels of the kind, its `app` label and what `template.metadata` authors, and no
selector reads an authored label. `helmtemplate`, `manifests`, `crd` and `passthrough`
generate no single object named after the component and refuse both. A top-level spec
field named `labels` therefore cannot keep its name: a `thanosruler`'s Prometheus external
labels are its `externalLabels` property (see **thanosruler**); a `prometheus`'s are its
spec's own `externalLabels` field, under its upstream name.

```yaml
- name: web
  type: deployment
  properties:
    image: ghcr.io/example/web:1.0.0
    labels:
      team: payments
    annotations:
      example.com/owner: "payments@example.com"
```

Refused, naming the component, the property and the key:

- a value that is no map, and an entry that is no string (quote a number or a boolean); a
  null value or entry is an absent one;
- a key or value the API server refuses: a label key or value that is no valid label, an
  annotation key that is no valid annotation key, annotations whose keys and values hold more
  than 262144 bytes together;
- the `app` label with another value than the component's (its name, or the hashed form of a
  name over 63 characters), which is what the kinds that set `app` select by;
- the component label key with another value than the component's (see "Component label and
  ownership" above);
- a key the kind already sets on its object with another value;
- a key the consumer reserved for itself (`ReservedMetadataKeys`), as on every generated
  object.

A label that names another object is the author's literal: it does not follow that object's
`objectName` or the `Naming` hook's answer for it. An object that belongs to another through
a label (an EndpointSlice's `kubernetes.io/service-name`) is written with the name the other
object takes.

An annotation a delivery engine acts on (a Flux annotation such as
`kustomize.toolkit.fluxcd.io/force`, for one) is written as authored too: launcher does not
refuse it and does not carry out what the engine does with it. `Transformer.WarnForcedVolumes`
warns about a PersistentVolume or PersistentVolumeClaim that has the force key enabled, and
changes nothing. A consumer that keeps such keys to itself reserves them with
`TransformContext.ReservedMetadataKeys`.

As `objectName`, the two are in no handler's `PropertySchema`: the engine adds them to the
schema of every type whose handler declares its object, reads and checks them, removes them
from the properties and hands `ToApplicationConfig` the result as
`Component.ObjectMetadata()`. A handler's config carries it as `Metadata` and `Generate` puts
it on the object through `kindObject`, the one helper every kind returns its object through
(the Flux sources through `emitFluxSource`, which does the same before it writes a long
`spec.timeout`). `TestObjectMetadata_EveryKindComponentTakesIt` holds every registered kind
to the two properties, so a kind added later cannot ship without them.

### The pod template's labels and annotations (`podMetadata`)

`labels` and `annotations` above go on the object and never on a pod template. The five kinds
whose spec holds a pod template the author does not write whole (`deployment`, `statefulset`,
`daemonset`, `job` and `cronjob`) take a fourth property, `podMetadata`, for the pod template's
own metadata (go-kure/launcher#1178). On a `cronjob` it is the pod template inside the job
template, not the CronJob's or the job template's metadata. `webservice` and `worker` take it
too (go-kure/launcher#1216), with the same name and shape as `deployment`: the rule hands it to
its deployment member, so it lands on that Deployment's pod template and nowhere else. The
Service a `webservice` generates selects on the component's `app` label alone, so an authored
label is on the pods and not in the Service's selector. A document moves a workload between
`webservice`, `worker` and `deployment` without renaming it.

```yaml
properties:
  image: ghcr.io/example/web:v1.0.0
  podMetadata:
    labels:
      example.com/team: payments
    annotations:
      example.com/owner: payments team
```

- **What it does.** The kind writes its pod template labels first and the authored ones are added
  beside them. A change to either map is a pod template change, so the controller's update
  strategy decides whether it reaches the pods that exist (a rolling update replaces them,
  `OnDelete` waits for a deletion, and a `cronjob` only changes the Jobs it creates later). A
  `job` has no strategy: the API server refuses a change to the pod template of a Job that is
  not suspended.
- **What it never does.** It does not reach the object's metadata, the selector or any other
  object. The selector is a map of its own (`appLabels`, `deploymentComponentLabels`), so an
  authored label cannot change which pods a workload owns.
- **Refused where the component is read.** A key beside `labels` and `annotations`; a label key
  or value that is no valid label; a value that is no string; an annotation key that is no valid
  annotation key; annotations over the size the API server accepts (counted again with the
  annotations the kind sets); and, on `job` and `cronjob` only, the four labels the Job controller
  owns (`batch.kubernetes.io/controller-uid`, `batch.kubernetes.io/job-name`, `controller-uid`,
  `job-name`), since the API server generates the selector of a Job from them. That last refusal
  can be loosened later without invalidating a document.
- **Refused where the object is generated.** `app`, or any other key the kind sets, with another
  value than the one the kind writes; writing the kind's own value is accepted.
- **Checked as on any pod template.** A key the consumer reserves
  (`TransformContext.ReservedMetadataKeys`) is refused, and the component label is stamped and
  held, by the checks that already read every generated pod template. A kubernetes.io or
  k8s.io key is not refused here: the API server and the Job controller own what they own.
- **Traits.** The traits written so far change the pod spec only, so they leave the authored keys
  as they are.

The property is an addition to the document format: a document that wrote none is unchanged, and
the keys refused above were not expressible before.

### A label selector's match expressions

A kind component whose spec holds a Kubernetes label selector (`metav1.LabelSelector`) decodes
it strictly, and the strict decode refuses nothing of a match expression: the Go type writes
`key` and `operator` whether or not they were authored, and reads any string as an operator.
Three defects of an expression are therefore refused where the component is read, with or
without an environment policy, each named by its path (go-kure/launcher#790):

- **An expression without its `key` or its `operator`**
  (`selector.matchExpressions[0].operator: required (the operator of the expression: In, NotIn,
  Exists or DoesNotExist)`). The object would carry `key: ""` or `operator: ""` and not show
  the omission.
- **An `operator` that is none of the four**
  (`selector.matchExpressions[0].operator: "Equals" is not a label selector operator (In, NotIn,
  Exists or DoesNotExist)`).
- **An operator and `values` that do not go together**: `In` or `NotIn` without a value
  (`selector.matchExpressions[0].values: required with the operator In (at least one value)`),
  `Exists` or `DoesNotExist` with one (`…values: not allowed with the operator Exists (it takes
  no value)`).

The API server refuses each of the three on every selector listed below, through one function,
`ValidateLabelSelectorRequirement` of `k8s.io/apimachinery`, and under every option it passes
that function. `TestValidateLabelSelector_MatchesAPIMachinery` holds launcher's check to the
linked function on those three.

**That function judges more, and these kinds do not.** It also refuses an expression `key` that
is no label name, an expression value that is no label value (on the objects that still check
it), and a `matchLabels` key or value that is no label name or value. Those are value rules,
left to the API server as for every typed kind: a selector with `key: "not a label"` builds
here and is refused at apply. An authored empty `key` (`key: ""`) is one of them: it differs
from an unauthored key only in that the unauthored one is refused here, and the API server
refuses both.

**Where it holds:**

| Kind | Selectors |
|---|---|
| `networkpolicy` | `podSelector`; the `podSelector` and `namespaceSelector` of every `ingress[].from[]` and `egress[].to[]` peer |
| `poddisruptionbudget` | `selector` |
| `clusterrole` | every entry of `aggregationRule.clusterRoleSelectors` |
| `validatingwebhookconfiguration`, `mutatingwebhookconfiguration` | the `namespaceSelector` and `objectSelector` of every webhook |
| `validatingadmissionpolicy`, `mutatingadmissionpolicy` | the `namespaceSelector` and `objectSelector` of `matchConstraints` |
| `validatingadmissionpolicybinding`, `mutatingadmissionpolicybinding` | `paramRef.selector`; the `namespaceSelector` and `objectSelector` of `matchResources` |
| `pod`, and under `template.spec` `podtemplate`, `replicaset` and `replicationcontroller` | the `labelSelector` and `namespaceSelector` of every `podAffinity` and `podAntiAffinity` term, required or preferred; a topology spread constraint's `labelSelector`; a projected `clusterTrustBundle` source's `labelSelector`; the `selector` of a generic ephemeral volume's claim template |

A `replicaset`'s own `selector` is refused for the same defects by the check that compares it
with the `app` label, with apimachinery's reason and not the texts above (see its entry);
`TestReplicaSetSelector_RefusedAsAPIMachineryReadsIt` holds that to each of them. The Cilium
kinds hold the `key` and `operator` of Cilium's own selector type through the same required
list, with the same wording; their entries say what else Cilium requires.

**Presence only, where a CRD defines the object.** On the kinds below an expression without its
`key` or its `operator` is refused, by the same required list and in the same words. The CRD
requires both fields, and the Go type writes each whether or not it was authored, so the object
would not show the omission: the document as authored is one the API server refuses, and the
kind refuses the omission by presence, as it does every required field its type writes
unauthored. An authored empty string is a value, left to the API server. Nothing else of the
expression is refused: an operator that is none of the four and a mismatched `values` are left
to the API server and to the operator's own admission, since a CRD's schema requires the two
fields and says nothing of the pair.

| Kind | Selectors | Ground |
|---|---|---|
| `servicemonitor`, `podmonitor` | `selector` | the linked type, by the generator's rule (below) |
| `prometheus-probe` | `targets.ingress.selector` | the same |
| `alertmanager` | the selectors a `pod` holds, at the top of the spec; the `selector` of the claim template of `storage.volumeClaimTemplate` and of `storage.ephemeral`; `alertmanagerConfigSelector`; `alertmanagerConfigNamespaceSelector` | the same |
| `thanosruler` | the selectors a `pod` holds, at the top of the spec; the `selector` of the claim template of `storage.volumeClaimTemplate` and of `storage.ephemeral`; `ruleSelector`; `ruleNamespaceSelector` | the same |
| `prometheus` | the selectors a `pod` holds, at the top of the spec; the `selector` of the claim template of `storage.volumeClaimTemplate` and of `storage.ephemeral`; the selector and the namespace selector of the ServiceMonitors, PodMonitors, Probes, ScrapeConfigs and rules it reads (`serviceMonitorSelector`, `serviceMonitorNamespaceSelector`, `podMonitorSelector`, `podMonitorNamespaceSelector`, `probeSelector`, `probeNamespaceSelector`, `scrapeConfigSelector`, `scrapeConfigNamespaceSelector`, `ruleSelector`, `ruleNamespaceSelector`) | the same |
| `cnpg-cluster` | the `labelSelector` and `namespaceSelector` of every `affinity.additionalPodAffinity` and `affinity.additionalPodAntiAffinity` term, required or preferred; `topologySpreadConstraints[].labelSelector`; a `projectedVolumeTemplate` `clusterTrustBundle` source's `labelSelector`; the `selector` of `ephemeralVolumeSource.volumeClaimTemplate.spec` and of the `pvcTemplate` of `storage`, `walStorage` and each tablespace; `podSelectorRefs[].selector` | the Cluster CRD of the linked module |
| `cnpg-pooler` | under `template.spec`, the selectors a `pod` holds | the Pooler CRD of the linked module |
| `issuer`, `clusterissuer` | the `labelSelector` and `namespaceSelector` of every pod affinity and anti-affinity term of an HTTP01 solver's `podTemplate.spec.affinity`, under `ingress` and under `gatewayHTTPRoute` | the Issuer and ClusterIssuer CRDs of the linked module |
| `gateway`, `listenerset` | a listener's `allowedRoutes.namespaces.selector`; a Gateway's `allowedListeners.namespaces.selector` | the CRDs of both Gateway API channels |
| `cilium-nodeconfig` | `nodeSelector` | the CiliumNodeConfig CRD of the linked module |
| `secretstore`, `clustersecretstore` | `conditions[].namespaceSelector` | the linked type, by the generator's rule (below) |
| `clusterexternalsecret` | `namespaceSelector`; every entry of `namespaceSelectors` | the same |
| `imageupdateautomation` | `policySelector` | the same |
| `resourcesetinputprovider` | every entry of `selectors` | the ResourceSetInputProvider CRD of the linked module, and the linked type, by the generator's rule |
| `replicationsource`, `replicationdestination` | the `labelSelector` and `namespaceSelector` of every pod affinity and anti-affinity term of a mover's `moverAffinity`, required or preferred: `rclone`, `restic`, `rsyncTLS`, and on a source `syncthing` | the ReplicationSource and ReplicationDestination CRDs of the linked module |

The Prometheus operator's module, the External Secrets Operator's and Flux's image automation
module ship no CRD to read, so the ground of their kinds is the source of the linked
`metav1.LabelSelectorRequirement`; the Flux Operator's module ships its CRDs, and its kind is
held to both, to its CRD and, as the other Flux kinds are, to that source: the schema generators
require a field that carries no optional marker and whose json tag keeps it when empty, and
`key` and `operator` are such fields
where `values` is not. `TestExternalSecretsKinds_RequiredMatchSource` derives the two from
that source by that rule, beside the fields the operator's own types mark required, and holds
each kind's list to them. The monitoring and Flux kinds' lists are generated from the same
source by the same rule
([Required fields a kind writes unauthored and does not refuse](#required-fields-a-kind-writes-unauthored-and-does-not-refuse)),
which also requires every other field of an embedded Kubernetes type the rule requires (a
container's `name`, a Secret key reference's `key`). CloudNativePG's admission
webhook runs apimachinery's whole selector validation on `podSelectorRefs[].selector`; that
fuller check is the operator's, not launcher's.

**Not held: the metric selectors of a `horizontalpodautoscaler`**
(`metrics[].object.metric.selector`, `metrics[].pods.metric.selector`,
`metrics[].external.metric.selector`). The API server's validation of an autoscaler does not
read them, and a kind does not refuse what the API admits.

A config that is exported (`NetworkPolicyConfig`, `PodConfig`, `PodTemplateConfig`,
`ReplicaSetConfig`, `ReplicationControllerConfig`) repeats at `Generate` the two checks its
typed value can show: the operator, an empty one included, and the pair. A missing `key` shows
only in what was authored, so it is refused where the component is read.
`TestLabelSelectorKinds_CoverEverySelector` walks the type of every strictly decoded kind and
fails on a selector path that is neither held nor listed as left out with its reason, so a
selector added by a dependency bump or a new kind cannot go unread. For a presence-only kind it
also reads the CRDs named above, fails where one does not require `key` and `operator` at a
held path, and asks the API server's own validation of a custom resource at each: the object
the kind emits for a whole expression is accepted, and the same object with the `key` or the
`operator` taken out is refused for that field alone. With the field as the empty string the
type writes, the object is accepted again, which is why the omission is refused where the
component is read.

The walk covers the kinds that decode a type. The hand-parsed kinds (`deployment`, `daemonset`,
`job`, `cronjob`, `statefulset`, `persistentvolumeclaim`) hold their selectors through their own
parser (`parseLabelSelectorOpts`), which refuses the same and more, so their absence from the
test's list is not a gap.

### Required fields a kind writes unauthored and does not refuse

A field the API requires can still reach the object unauthored. Where the Go type writes the
field whatever was authored (`""`, `0`, `{}`, `null`), the object carries a value the author
did not write and does not show the omission. A kind refuses the omission of such a field only
where its required list or its own validation names it (`helmrelease` checks a `valuesFrom`
entry's `kind` and `name` by hand); every other one is written and left to the API server and to
the operator. The section above and each kind's entry list the ones that are refused. This
section states the rule that refuses them where a kind's list is generated, and the ones that
are not refused, as they are at this pin.

**Generated lists.** The required list of `issuer`, `clusterissuer`, `certificate`,
`secretstore`, `clustersecretstore`, `externalsecret`, `clusterexternalsecret`, `cnpg-cluster`,
`cnpg-pooler`, `cnpg-objectstore`, `cnpg-database`, `prometheus`, `alertmanager`,
`thanosruler`, `servicemonitor`, `podmonitor`, `prometheus-probe`, `prometheusrule`, the
fourteen Flux kinds (`helmrelease`, `fluxcd-kustomization`, `bucket`, `ocirepository`,
`gitrepository`, `helmchart`, `helmrepository`, `artifactgenerator`, `fluxcd-alert`,
`fluxcd-provider`, `fluxcd-receiver`, `imagepolicy`, `imagerepository`,
`imageupdateautomation`), `resourcesetinputprovider`, the ten Gateway API kinds
(`gatewayclass`, `gateway`, `listenerset`, `referencegrant`, `backendtlspolicy`, `httproute`,
`grpcroute`, `tcproute`, `udproute`, `tlsroute`), `replicationsource`,
`replicationdestination`, the four MetalLB kinds that require a field (`metallb-ipaddresspool`,
`metallb-l2advertisement`, `metallb-bgpadvertisement`, `metallb-bgppeer`), the seven further
cnpg kinds (`cnpg-imagecatalog`, `cnpg-clusterimagecatalog`, `cnpg-backup`,
`cnpg-scheduledbackup`, `cnpg-databaserole`, `cnpg-publication`, `cnpg-subscription`) and the
nine Cilium kinds other than the two network policies (`cilium-bgpadvertisement`,
`cilium-bgpclusterconfig`, `cilium-bgpnodeconfigoverride`, `cilium-bgppeerconfig`,
`cilium-cidrgroup`, `cilium-egressgatewaypolicy`, `cilium-loadbalancerippool`,
`cilium-localredirectpolicy`, `cilium-nodeconfig`) is generated:
every field the kind's API requires and its Go type writes unauthored is refused where the
author leaves it out under an authored parent, whatever type declares it. The paths are in
`zz_generated_kind_required.go` (`kindRequiredPaths`), read through the same derivation as the
measurement below (`deriveRequiredWritten`), so the two cannot drift.
`TestKindComponents_RequiredIsGenerated` fails where the file differs from the derivation, a
path added or gone, so a dependency bump that moves a field fails CI until the file is
regenerated with `UPDATE_KIND_REQUIRED=1` (refused where `CI` is set). Left out, each by a named
rule and never by the type that declares it:

- a field whose written value the API takes as authored (the valid values below);
- a field the schema gives a default, that is not nullable and that the type writes `null`:
  the API server drops that null and fills the default (`cnpg-cluster`'s
  `replicationSlots.synchronizeReplicas.enabled`);
- a field under a path the kind refuses whatever is authored there (see Method);
- an entry of `requiredWrittenExceptions`, the test's named exceptions, each with the API
  server's answer for the object without it, in one of two classes. **Accepted**: the API
  server accepts the omission as the kind writes it, and the operator handles it. At this pin:
  `cnpg-pooler`'s `template.spec.containers`, which the kind writes `[]` and the Pooler CRD
  accepts, the template's `spec` authored or left out; the operator adds its `pgbouncer`
  container. **Pruned**: the Go source requires the field and the kind's CRD does not declare
  it, so the API server prunes it, authored or not, and requires nothing of it. At this pin: on
  `prometheus`, `alertmanager` and `thanosruler`, the `apiVersion`, `kind`, `name` and `uid` of
  an owner reference in the metadata of an ephemeral volume's claim template
  (`storage.ephemeral` and `volumes[].ephemeral`), 8 per kind. The Go source declares the whole
  `ObjectMeta` there; the operator's CRDs declare that metadata as an object with no field, so
  the API server prunes an owner reference there (strict field validation refuses it as an
  unknown field) and requires none of its fields.

A refusal reads the kind's own text for the field where it has one, else `the <API> API
requires this field` (or `requires the name of what this refers to`, for a reference's
`name`). A field under a parent the type writes unauthored and the API does not require is
asked for only where that parent is authored; the generated file names each such field with
what the API server answers for the object with the parent left out, and the test holds every
one to an answer. A monitoring basic auth (`basicAuth`, wherever a kind holds one) is such a
parent: the API server accepts it without a `username` or `password`, and the Prometheus
operator reads both keys and refuses the basic auth without either, so the kind refuses an
authored basic auth that leaves either out; a web or cluster TLS server's `keySecret` is asked
for only where it is authored, `keyFile` being the alternative. The test shows what the kind
does with each, and what the API server answers on the operator's CRD: the object without the
parent where the kind builds one, else the object with the parent as the type writes it
unauthored (a `prometheus-probe`'s `prober` without a `url`, which the API server refuses as
the kind does). Of the stores, the fields whose API default never applies because the type
writes them (`secretStoreUnappliedDefaults`) are generated by the same test.

The Flux kinds that decode their properties themselves (`helmrelease`, `fluxcd-kustomization`,
`bucket`, `ocirepository`, `gitrepository`, `helmchart`, `helmrepository`) run the generated
refusal after their own checks, so a field one of those checks requires (a `bucket`'s
`bucketName`, a `helmrelease`'s `valuesFrom[].kind`) is refused in the words it always was;
the test names those fields (`generatedRefusedFirst`). An `httproute` and a `grpcroute` run it
after their decode checks too. A refusal of these kinds that their list did not name before
says its class's sentence. No path of a Flux or Gateway kind needs a recorded answer for a
parent left out. The Gateway API's module ships its CRDs, and so does the Flux Operator's: the
test runs a row of each Gateway kind and of `resourcesetinputprovider` through the API server.

**Vendored CRDs.** The Prometheus operator's module, the Flux controllers' API modules and the
External Secrets Operator's API module ship no CRD, so the test reads copies, vendored for the
tests only and each checked against its git blob id by `TestVendoredCRDs_MatchSource`, which
also holds each directory to the tag of the module `go.mod` links:

- the seven `monitoring.coreos.com` CRDs of the monitoring kinds, from
  `example/prometheus-operator-crd/` of prometheus-operator `v0.95.1`, beside the operator
  excerpt in `testdata/upstream/prometheus-operator/` (`scripts/vendor-prometheus-operator-gates.sh`);
- the CRDs of the fourteen Flux kinds, from `config/crd/bases/` of each controller's repository
  at the tag of its API module (`api/v1.7.1` of helm-controller, `api/v1.10.1` of
  kustomize-controller and source-controller, `api/v1.10.0` of notification-controller,
  `api/v1.3.0` of image-reflector-controller and image-automation-controller, `api/v2.3.1` of
  source-watcher), one directory per repository under `testdata/upstream/` with its `LICENSE`
  (`scripts/vendor-flux-crds.sh`). The one addition to their bytes is an `allow-term` pragma
  comment line above an upstream line that names a term the repository's forbidden-terms guard
  lists (a receiver type of the `Receiver` CRD), as a whole word or inside an identifier: the
  script reads the guard's own matcher (`scripts/forbidden-terms-lib.sh`, self-tested by `make
  test-forbidden-terms`), so it marks every line the guard would refuse, under the C locale or
  C.UTF-8. Each `file` pin in `SOURCE` is the blob id of
  the committed file; a file carrying pragma lines also has an `# upstream` line naming
  upstream's blob id, which the file must hash to with those lines stripped. A file with an
  `# upstream` line and no pragma, a pragma in a file with none, and a pragma whose next line
  does not name its term all fail the test;
- the CRDs of the four External Secrets kinds (`secretstore`, `clustersecretstore`,
  `externalsecret`, `clusterexternalsecret`), unmodified from `config/crds/bases/` of
  external-secrets `v2.14.0`, under the same path in `testdata/upstream/external-secrets/` with
  its `LICENSE` (`scripts/vendor-external-secrets-crds.sh`, `mise run
  vendor-external-secrets-crds vX.Y.Z`; go-kure/launcher#1108). `go.mod` links the API module
  at a pseudo-version of a commit, not at a tag of its own, and that commit is the release tag
  `v2.14.0`, a lightweight tag: its ref names the commit, with no tag object. So the
  `tag-object` line of `SOURCE` is the object the tag ref names, the commit, and the test
  requires its first 12 hex digits to be the pseudo-version's; the script takes the tag, checks
  with `git ls-remote` that it names that commit, and refuses an annotated tag or another
  commit. Copied unmodified, these files take no pragma: the script refuses one with a line the
  forbidden-terms guard would refuse, read with the guard's own matcher as above.

The kinds' lists stay derived from the markers of the Go source; the CRDs answer for them. Every
generated kind's CRD is read, shipped or vendored, and a kind whose CRD is not read fails the
test.
Of every type that holds a generated path, the test creates the object with one such path
authored, which the API server accepts, and without it, which the API server refuses as
missing a required field (a path whose CRD gives it a default is passed over for another of
its type: the API server fills a default only where the field is left out of the object, not
where the type writes it empty). A type whose every generated path the CRD defaults (a Vault
store's JWT authentication, whose one generated path is its mount `path`, defaulted to `jwt`)
is answered instead with the field authored and left out, both accepted, the default filled
in the second, and written `""` as the type writes it unauthored, which the API server keeps:
the default never applies, which is why the kind refuses it. Every generated path is one the
CRD declares. The rows author what the marker schema cannot: a URL, duration or enumerated
value where the filled control's would break a pattern or an enum, and, of the External
Secrets stores, one authentication method where a provider's property count or expression rule
requires exactly one (`generatedHarnessSamples`).

The VolSync, MetalLB, further cnpg and Cilium kinds are derived from the CRDs their linked
modules ship, and the test runs a row of each of their types that holds a generated field
through the API server, on those CRDs. `replicationsource` and `replicationdestination` run the
generated refusal after their own checks, as the Flux kinds do; their generated list adds the
required fields of the Kubernetes types their movers embed to what their hand-kept lists held.
The MetalLB, further cnpg and Cilium kinds' generated lists hold exactly what their hand-kept
lists held, and are read where those were, before the kinds' own checks, so no document is
refused that was not and every refusal reads as it did. None of these needs a recorded answer
for a parent left out.

**Kept by hand.** The `cilium-networkpolicy` and `cilium-clusterwidenetworkpolicy` kinds keep
their hand-kept list: it is one rule's list (`requiredfields.CiliumRule`), shared with the
`cilium-networkpolicy` trait in another package; it is read under the `spec` and `specs`
prefixes, which the generated lists' API server harness does not take; two of its fields
(`labels[].key`, `icmps[].fields[].type`) the decode refuses first, which that harness's
both-ways rows do not handle, though the derivation lists both; and it holds fields no
generated list has a place for (a listener's `priority` and Envoy configuration `kind`, which
the API does not require and refuses written). Both refuse
every field the derivation finds, and `TestCiliumNetworkPolicy_RequiredMatchCRD` and
`TestCiliumClusterwideNetworkPolicy_RequiredMatchCRD` hold the list to their CRDs.

**Families.** The test names each field by the type that holds it:

| Family | Owner type | Typical fields |
|---|---|---|
| `AFF` | the `core/v1` affinity terms: `NodeSelectorRequirement`, `PodAffinityTerm`, `PreferredSchedulingTerm`, `WeightedPodAffinityTerm` | a node selector requirement's `key` and `operator`; a pod affinity term's `topologyKey`; a weighted or preferred term's `weight`, `podAffinityTerm` and `preference` |
| `SEC` | `SeccompProfile`, `AppArmorProfile`, `Sysctl` | a profile's `type`; a sysctl's `name` and `value` |
| `POD` | any other `core/v1` type | the `key` of a secret or config map key reference; a container's `name`; a pooler template's `containers` |
| `KIND` | a type of the kind's own API, or of `metav1` other than a label selector's match expression | the `ownerReferences[]` fields of a Prometheus operator workload kind's ephemeral claim template |

A fifth family of the test, `SEL` (the `key` and `operator` of a `metav1` label selector's
match expression), has no member: those are refused on every kind
([A label selector's match expressions](#a-label-selectors-match-expressions)).

**Per kind.**

| Kind | Not refused | AFF | SEC | POD | KIND |
|---|---|---|---|---|---|
| `cnpg-pooler` | 1 | | | 1 | |
| `prometheus`, `alertmanager`, `thanosruler` | 8 each | | | | 8 |

Every remaining member is an exception. None is left on the other kinds whose list is
generated (`cnpg-pooler` keeps its accepted exception, and `prometheus`, `alertmanager` and
`thanosruler` their pruned owner reference fields), nor on the two Cilium network policy
kinds, whose list is kept by hand. `metallb-bfdprofile` and `metallb-community` are not
measured: their CRDs require no field, and `TestMetalLBKinds_RequiredMatchCRD` holds each kind's
empty list to its CRD. Nor is `apiservice`: no CRD serves it, the API server's aggregation layer
does.
`TestRequiredWrittenKinds_CoverEveryCRDKind` holds the measured kinds to the kind inventory:
every `kind` row whose API group client-go's scheme does not register, so a CRD serves it, is
measured or named as not measured with its reason, so a new kind component of such an API
fails there until it is one or the other. Most members are written
`""`; the rest are `0` (mostly a preferred term's `weight` or a `port`), `{}`, `[]`, `null` or
an object of such values.

**What the API server makes of them** is a value rule of each field, not read here. The
exceptions are shown: `TestKindComponents_GeneratedRequiredBothWays` creates the Pooler without
`template.spec.containers`, and without the template's `spec`, and the API server accepts both.
It creates each workload kind of the Prometheus operator with an owner reference in an
ephemeral claim template's metadata authored whole, and with the field taken out: the API
server names that `ownerReferences` an unknown field in both, and nothing else, and accepts
the object it prunes it from, so it drops the owner reference whatever it holds.
Before go-kure/launcher#883 generated their lists, the same was shown of `imageCatalogRef` on
`cnpg-cluster` and `pgbouncer.imageCatalogRef` on `cnpg-pooler`: a `kind` written `""` refused
by the CRD's own rule on the reference, a `name` written `""` and a `major` written `0` accepted.
All three are refused now.

**Excluded as valid values.** A member whose written value the API takes as authored is not
listed: `cnpg-cluster`'s `instances` (`1`, the CRD's default) and
`postgresql.syncReplicaElectionConstraint.enabled` (`false`, which the CRD requires and gives
no default; the Go type's zero value is a valid one); of the Flux kinds, `interval` of
`bucket`, `fluxcd-kustomization`, `gitrepository`, `helmchart` and `helmrelease`
(`"1h0m0s"`, the kind's own default), a verification's `provider` on `helmchart`,
`helmrelease` (`chart.spec.verify.provider`) and `ocirepository` (`"cosign"`, the API's
default, which the kind writes), and `fluxcd-kustomization`'s `prune` (`false`, a valid zero
value). The test names the eleven (`requiredWrittenValidDefaults`) and checks that their
omission builds and writes exactly that value. Nor is a field the schema gives a default, that
is not nullable and that the type writes `null` (`cnpg-cluster`'s
`replicationSlots.synchronizeReplicas.enabled`): the API server drops the null and fills the
default.

**Method.** For each field the kind's API requires and its Go type writes unauthored,
`TestKindComponents_RequiredWrittenNotRefused` builds the kind with the field authored, with
the required fields around it filled, then builds it again with the field taken out. The field
is a member when the second build succeeds and the object still holds the field. A
`helmrelease`'s fields of the OCIRepository its chart template inlines are measured under a
template of `chart.kind` OCIRepository, the only kind that reads them. A field under
a path the kind refuses whatever is authored there is skipped, and the test names those paths
with their reason: a `cilium-networkpolicy`'s `nodeSelector`, a `cnpg-pooler`'s
`template.spec.ephemeralContainers`, and the `generatorRef` of an `externalsecret`'s or a
`clusterexternalsecret`'s `data[].sourceRef`. A refused omission counts only where the error
names the field or one under it, by its full path; the two Cilium policy kinds refuse a label's
`key` and an ICMP field's `type` in their decode instead, with an error naming no field, and the
test names those with their reason. Any other field that cannot be measured fails the test. The
required lists are those of the CRDs of the linked modules. The Prometheus operator, Flux and
External Secrets kinds are read through the schema markers of their linked sources, by the same
reader as their own required-list tests; `resourcesetinputprovider` is read from the CRD the
flux-operator module ships. The members are pinned in
`testdata/required-written-not-refused.txt`, one line per field (kind, path, family, written
value). The test fails on any difference, a new member or one that is gone, so a dependency
bump or a kind change that moves the set fails CI; `UPDATE_REQUIRED_WRITTEN_PIN=1` rewrites
the file, and is refused where `CI` is set.

At this pin the file holds 25 members, every one an exception: the accepted
`cnpg-pooler template.spec.containers` and the 24 pruned owner reference fields. The External
Secrets kinds' generated paths are held to the API server too, on their vendored CRDs
(go-kure/launcher#1108).

### Every spec field is this package's to write

Since go-kure/launcher#361 this package builds against kure's release-1 builder
contract (`go-kure/kure` ≥ `v0.2.0-beta.11`). Under it a `Create<Kind>`
constructor returns an object carrying TypeMeta plus `metadata.name` and
`metadata.namespace` and **nothing else** — no labels, no annotations, no
selector, no injected defaults. Everything else is the handler's own literal or
an explicit field assignment. Two consequences a reader needs:

**`spec.selector` is written explicitly, and must agree with the pod template.**
Deployment, StatefulSet and DaemonSet all require `spec.selector` and get no
server-side default for it, so each handler assigns
`&metav1.LabelSelector{MatchLabels: …}` from the same helper that produced
`spec.template.metadata.labels` — `deploymentComponentLabels` for `deployment`
and for `worker`, which lowers to a `deployment` component (its affinity selector
uses `appLabels`), and `appLabels` for `webservice`, `statefulset` and `daemonset` (both
return a fresh `{"app": <label value>}` map, per the ownership rule above, valued
as [The `app` label](#the-app-label) describes). This is
the one field the compiler cannot check: a selector that disagrees with the
template labels compiles and is refused by the apiserver at apply time. The labels an
author adds with `podMetadata` go on the pod template only, so they cannot make the two
disagree. `job` and
`cronjob` are the deliberate exception — the Job controller fills `spec.selector`
and its matching pod labels itself.

**Two injected defaults are gone from the emitted manifests.** The constructors
used to write `imagePullPolicy: IfNotPresent` onto every container and
`podManagementPolicy: OrderedReady` onto every StatefulSet; neither is emitted
now. `OrderedReady` is the apiserver's own default for that field, so that one
is unchanged in effect. `imagePullPolicy` is written only when a component
authors it (see [Container fields](#container-fields)); what follows is about
the omitted case. Kubernetes defaults an omitted
value from the image reference and keys that decision on the `latest` tag, so
what matters here is that no accepted image can carry one: `ValidateImageRef`
(`common.go`) refuses an untagged reference and an explicit `:latest` tag on
every main, init and sidecar image — including a digest reference that still
carries the tag (`repo:latest@sha256:...`), which it rejects for exactly this
reason. A digest reference with *no* tag is the case this argument does not
reach, and no golden covers that form; it is pinned by digest either way, and
this package does not assert what a cluster defaults for it. The argument is
about the *parsed* path, which is where `ValidateImageRef` runs
(`ToApplicationConfig`): a handler config a library consumer builds directly
carries whatever image it was given, so `:latest` with no pull policy remains
reachable that way. That is also the only other path with a delta here — raw
manifests, `passthrough`, Helm-rendered objects and Flux patches never went
through the constructors, so nothing was ever injected into them to lose.

A third delta from the same contract change is metadata rather than a default, but
belongs in the same inventory: under `valuesMode:
configMap` the generated values ConfigMap used to take an `app` label *and* an
`app` annotation from the constructor, both valued at the ConfigMap's own name
(`<component>-values`). It now carries the label only, valued at the component's
label value (`<component>`, projected when the name exceeds 63 characters — see
[The `app` label](#the-app-label)), matching every other object this package emits. A consumer
selecting that ConfigMap by `app=<component>-values` must be repointed.

The `obj.Annotations = nil`
assignments scattered through the handlers, which existed to strip the `app:`
annotation the constructors used to stamp, are now no-ops — kept so the field
stays at a known value regardless of what a future constructor does.

**`postgresql` writes every CNPG value itself (kure `v0.2.0-beta.13`).** kure's
release-2 builder contract retired the CNPG config-struct layer
(`cnpg.Cluster`/`Pooler`/`ObjectStore`/`Database` and their `*Options` types) that
`postgresql` used to go through. The handler then called the generated
`CreateCluster`/`CreatePooler`/`CreateObjectStore`/`CreateDatabase` and assigned the
upstream `cnpgv1` / barman-cloud structs directly. Since go-kure/launcher#281 the
`postgresql` rule builds those structs as the properties of the `cnpg-*` kind
components it lowers onto, whose `Generate` calls the same constructors, and
`enablePDB` comes from the post-policy step the rule attaches to the Cluster.
The values that layer used to inject are written explicitly, so the emitted
manifests are unchanged byte for byte: `enablePDB` (true only for more than one
instance), `primaryUpdateStrategy: unsupervised`, the `ACCESS_KEY_ID` /
`SECRET_ACCESS_KEY` key names on backup and objectStore credentials, the
barman-cloud WAL-archiver plugin entry when an `objectStore` is declared (its
plugin name and parameters were corrected afterwards by go-kure/launcher#643),
the pooler `type` (`rw` unless `ro` was authored) and its always-present
`pgbouncer` block, and `ensure: present` on every extension not authored `absent`.
The layer's guards are kept too: `inheritedMetadata`, `managed`, `bootstrap`,
`postgresql.synchronous` and the credential references are omitted when their input
is empty, a pooler `instances` of zero or less is omitted when nobody authored
it (an authored count is written as authored since go-kure/launcher#790, see
the postgresql entry), and a role's or database's `ensure` /
`databaseReclaimPolicy` is written only for `absent` / `delete`.
