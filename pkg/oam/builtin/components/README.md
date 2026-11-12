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
a property whose gate is off by default at that release (or set off in `spec.featureGates`) refused (go-kure/launcher#1183):
`hostnameOverride` below 1.35 (`HostnameOverride`) and `podResources` below 1.34
(`PodLevelResources`), and a container's `env[].valueFrom.fileKeyRef` below 1.35
(`EnvFiles`) and an init container's `restartPolicyRules` below 1.35
(`ContainerRestartRules`), on the kinds whose schema declares them. The gate and the
release are in the property's published schema (`gate`). With no version stated nothing
is checked, and a profile's `spec.featureGates` replaces the release default for a gate it
names: set on, the property is accepted below its release; set off, it is refused at any
release (go-kure/launcher#1231). The `Never` and `OnFailure` values of an init container's `restartPolicy`
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

Full entry: [Kinds: PostgreSQL and CloudNativePG](https://www.gokure.dev/launcher/api-reference/oam-components/kinds-postgres/).

Opening lines of an entry:

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

Full entry: [Kinds: core workloads](https://www.gokure.dev/launcher/api-reference/oam-components/kinds-core-workloads/).

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

Full text: [Common config](https://www.gokure.dev/launcher/api-reference/oam-components/common-config/#statefulset-level-and-claim-template-properties).

## Upstream fields a hand-parsed kind does not read

See [Upstream fields a hand-parsed kind does not read](https://www.gokure.dev/launcher/api-reference/oam-components/upstream-fields-not-read/#upstream-fields-a-hand-parsed-kind-does-not-read).

### The two paths

See [Upstream fields a hand-parsed kind does not read](https://www.gokure.dev/launcher/api-reference/oam-components/upstream-fields-not-read/#the-two-paths).

### Below the top level

See [Upstream fields a hand-parsed kind does not read](https://www.gokure.dev/launcher/api-reference/oam-components/upstream-fields-not-read/#below-the-top-level).

### Refused, with the reason

See [Upstream fields a hand-parsed kind does not read](https://www.gokure.dev/launcher/api-reference/oam-components/upstream-fields-not-read/#refused-with-the-reason).

### Read in another shape

See [Upstream fields a hand-parsed kind does not read](https://www.gokure.dev/launcher/api-reference/oam-components/upstream-fields-not-read/#read-in-another-shape).

### One level down

See [Upstream fields a hand-parsed kind does not read](https://www.gokure.dev/launcher/api-reference/oam-components/upstream-fields-not-read/#one-level-down).

### Not authorable, and not refusable by name

See [Upstream fields a hand-parsed kind does not read](https://www.gokure.dev/launcher/api-reference/oam-components/upstream-fields-not-read/#not-authorable-and-not-refusable-by-name).

### Outside the properties

See [Upstream fields a hand-parsed kind does not read](https://www.gokure.dev/launcher/api-reference/oam-components/upstream-fields-not-read/#outside-the-properties).

### An explicit null on a refused key

See [Upstream fields a hand-parsed kind does not read](https://www.gokure.dev/launcher/api-reference/oam-components/upstream-fields-not-read/#an-explicit-null-on-a-refused-key).

### What holds this

See [Upstream fields a hand-parsed kind does not read](https://www.gokure.dev/launcher/api-reference/oam-components/upstream-fields-not-read/#what-holds-this).

## Extending

See [Extending](https://www.gokure.dev/launcher/api-reference/oam-components/extending/#extending).

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

Full text: [The null contract and two spellings of one field](https://www.gokure.dev/launcher/api-reference/oam-components/null-and-spellings/#the-null-contract).

## Two spellings of one field

See [The null contract and two spellings of one field](https://www.gokure.dev/launcher/api-reference/oam-components/null-and-spellings/#two-spellings-of-one-field).

## Conventions

See [Conventions](https://www.gokure.dev/launcher/api-reference/oam-components/conventions/#conventions).

### The `app` label

See [Conventions](https://www.gokure.dev/launcher/api-reference/oam-components/conventions/#the-app-label).

### The object name (`objectName`)

See [Conventions](https://www.gokure.dev/launcher/api-reference/oam-components/conventions/#the-object-name-objectname).

### The object's labels and annotations (`labels`, `annotations`)

See [Conventions](https://www.gokure.dev/launcher/api-reference/oam-components/conventions/#the-objects-labels-and-annotations-labels-annotations).

### The pod template's labels and annotations (`podMetadata`)

See [Conventions](https://www.gokure.dev/launcher/api-reference/oam-components/conventions/#the-pod-templates-labels-and-annotations-podmetadata).

### A label selector's match expressions

See [Conventions](https://www.gokure.dev/launcher/api-reference/oam-components/conventions/#a-label-selectors-match-expressions).

### Required fields a kind writes unauthored and does not refuse

See [Conventions](https://www.gokure.dev/launcher/api-reference/oam-components/conventions/#required-fields-a-kind-writes-unauthored-and-does-not-refuse).

### Every spec field is this package's to write

See [Conventions](https://www.gokure.dev/launcher/api-reference/oam-components/conventions/#every-spec-field-is-this-packages-to-write).
