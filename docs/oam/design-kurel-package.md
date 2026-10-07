# Design: Kurel Package Spec

*Status: Final | Issue: [go-kure/launcher#36](https://github.com/go-kure/launcher/issues/36)*

| Version | Date | Summary |
|---|---|---|
| 1.3 | 2026-10-01 | §6.1/§6.3: `array`/`object` parameter types with node substitution (shape only; string default refused). go-kure/launcher#421 |
| 1.2 | 2026-07-10 | §6.1: unify parameter schema onto the shared `PropertySchema` vocabulary (flat subset; rich fields rejected at decode). |
| 1.1 | 2026-05-14 | Complete §6 (parameter syntax — Option A); fix GVK references; remove `backup` from Phase 1 trait table; fix §5 diagram label |
| 1.0 | 2026-04-19 | Initial draft — parameter syntax section omitted pending decision |

---

## 1. Purpose

A kurel package is a distributable, reusable OAM application pattern. It bundles:

- an OAM Application document (`app.yaml`) describing what workloads to run and what
  platform capabilities they need
- a package metadata file (`kurel.yaml`) with identity and parameter declarations
- optionally, example value files for common deployment scenarios

Packages are designed to be shared: a team defines a `webservice-with-ingress` package
once and any project instantiates it by supplying their image, domain, and values.

---

## 2. Package Directory Layout

```
my-app/
├── kurel.yaml        # package identity and parameter schema
├── app.yaml          # launcher Application (launcher.gokure.dev/v1alpha1)
└── examples/
    ├── production.yaml   # example values for a production deployment
    └── staging.yaml      # example values for a staging deployment
```

The OAM Application format replaces the prototype's `parameters.yaml + resources/ + patches/`
layout. No coexistence or backward-compatible bridging is required.

---

## 3. kurel.yaml

`kurel.yaml` declares the package identity and the parameter schema.

```yaml
apiVersion: launcher.gokure.dev/v1alpha1
kind: Package
metadata:
  name: webservice        # package identifier
  version: "1.0.0"        # semver
  description: "A stateless web service with ingress, TLS, and optional autoscaling."
  # Future: home, keywords, maintainers (informational only)
spec:
  parameters:
  - name: image
    type: string
    required: true
    description: "Container image with tag, e.g. registry/app:v1.2.3"
  - name: domain
    type: string
    required: true
    description: "Primary hostname, e.g. app.example.com"
  - name: replicas
    type: integer
    required: false
    default: 1
```

---

## 4. app.yaml — OAM Application

`app.yaml` is a launcher Application document (`launcher.gokure.dev/v1alpha1`, kind
`Application`). It contains `${var}` parameter placeholders that the resolver substitutes
using the values supplied at build time. The component and trait types must match the
handler registry that the runtime is configured with. See `docs/oam/design-gvk.md` for
the GVK rationale and `docs/oam/options-param-syntax.md` for the parameter syntax spec.

### 4.1 Basic structure

```yaml {check="build" profile="examples/cluster-profiles/minimal.yaml"}
apiVersion: launcher.gokure.dev/v1alpha1
kind: Application
metadata:
  name: my-app
spec:
  components:
  - name: web
    type: webservice        # must match a registered ComponentHandler
    properties:
      image: myregistry/myapp:1.0.0
      port: 8080
      replicas: 1
    traits:
    - type: expose          # must match a registered TraitHandler
      properties:
        rules:
        - host: my-app.example.com
          paths:
          - path: /
            port: 8080
```

### 4.2 Supported component types (Phase 1)

Ported from the downstream runtime. Each type maps to a `ComponentHandler` implementation in
`pkg/oam/builtin/`:

| type | description |
|---|---|
| `alertmanager` | Kind-named Prometheus operator Alertmanager (launcher-native): the whole `AlertmanagerSpec`, strictly decoded, less the deprecated `baseImage`, `tag` and `sha`, which are not in the schema and are refused as unsupported fields, an empty one included; no top-level field is required. Named after the component unless `objectName` names it. The operator runs the pods: `image`, `replicas`, `resources`, the storage a claim requests, and `containers`, `initContainers`, `volumes`, `securityContext` and `hostNetwork` are held to the EnvironmentPolicy as a workload's are, and no default is filled; the replica count and memory request the operator fills where they are unset are held. An image the operator chooses (`alertmanager`'s where neither `image` nor a patch names one, a reloader's where no patch names one) is refused under a policy with allowed registries. A listed container that names no image and patches none the operator generates is refused, and so is a `retention` or cluster duration of 0 or less. `podMetadata` takes no label: the pods carry the component label only where the author writes it. No capability is required. |
| `apiservice` | Kind-named APIService (launcher-native): the whole `APIServiceSpec`, strictly decoded; `group`, `version`, `groupPriorityMinimum` and `versionPriority` are required, and a `service` names its `namespace` and `name`. Cluster-scoped, and named `<version>.<group>`, which the API requires, by the component's name or `objectName`; any other name is refused. The Service is not resolved at build. Only the EnvironmentPolicy's object kind rules apply. |
| `artifactgenerator` | Kind-named Flux `ArtifactGenerator` (launcher-native): the whole `ArtifactGeneratorSpec`, strictly decoded; `sources` and `artifacts` are required, of a source its `alias`, `kind` and `name`, of an artifact its `name` and `copy`, and of a copy its `from` and `to`, the fields the linked Go source marks required. The API's expression rule, which holds an artifact's `name` to an object name where no `pathPattern` is set, is not checked: the linked module ships no CRD to hold a check to. Emits only the ArtifactGenerator, named after the component unless `objectName` names it, in the Flux namespace when one is set; it reads no ConfigMap and no Secret by name. The generator copies files out of the sources it names into its artifacts: a source's `namespace` is written as authored, and nothing gates it. `commonMetadata` is part of the spec, not the object's own labels and annotations. No EnvironmentPolicy dimension applies and no capability is required. |
| `backendtlspolicy` | Kind-named Gateway API BackendTLSPolicy (launcher-native): the whole `BackendTLSPolicySpec`, strictly decoded; at least one of `targetRefs`, and `validation` with its `hostname`, are required. Named after the component unless `objectName` names it. No EnvironmentPolicy dimension applies, no host is held to the allowed registries and no capability is required. |
| `bucket` | Kind-named Flux `Bucket`: the top-level keys of `BucketSpec`, decoded strictly. `bucketName` and `endpoint` are required; `interval` defaults to `60m`. The `endpoint` host is constrained by `AllowedRegistries` (its `sts.endpoint` is not); under `provider: gcp`, whose client ignores `endpoint`, the host constrained is `storage.googleapis.com`; and a non-empty `AllowedRegistries` refuses an Amazon S3 `endpoint` (any containing `amazonaws`) under the `generic`, `aws` or unset provider, whose S3 client picks the host it contacts from `region` at runtime. Emits only the Bucket, in the Flux namespace when one is set. |
| `certificate` | Kind-named cert-manager Certificate (launcher-native): the whole `CertificateSpec`, strictly decoded; `secretName` and `issuerRef` with its `name` are required. Named after the component unless `objectName` names it. The issuer is the author's. A keystore password written into the object is refused under an EnvironmentPolicy that forbids explicit secrets. No capability is required. Shares its name with the `certificate` trait, which derives a Certificate for a workload. |
| `cilium-bgpadvertisement` | Kind-named Cilium CiliumBGPAdvertisement (launcher-native): the whole `cilium.io/v2` `CiliumBGPAdvertisementSpec` (`advertisements`), strictly decoded; `advertisements` and an entry's `advertisementType` are required, `service` goes with type `Service` and `interface` with type `Interface`, and only with it. Cluster-scoped and named after the component unless `objectName` names it. No EnvironmentPolicy dimension applies, and no capability is required. |
| `cilium-bgpclusterconfig` | Kind-named Cilium CiliumBGPClusterConfig (launcher-native): the whole `CiliumBGPClusterConfigSpec` (`nodeSelector`, `bgpInstances`), strictly decoded; `bgpInstances`, an instance's and a peer's `name`, and a `peerConfigRef`'s `name` are required. Cluster-scoped and named after the component unless `objectName` names it. The node selector is the author's; no peer address is held to the allowed registries. No EnvironmentPolicy dimension applies, and no capability is required. |
| `cilium-bgpnodeconfigoverride` | Kind-named Cilium CiliumBGPNodeConfigOverride (launcher-native): the whole `CiliumBGPNodeConfigOverrideSpec` (`bgpInstances`), strictly decoded; `bgpInstances` and an instance's and a peer's `name` are required. Cluster-scoped; it overrides the CiliumBGPNodeConfig of the same name, which `objectName` sets where the component name is not that name. No EnvironmentPolicy dimension applies, and no capability is required. |
| `cilium-bgppeerconfig` | Kind-named Cilium CiliumBGPPeerConfig (launcher-native): the whole `CiliumBGPPeerConfigSpec`, strictly decoded; no top-level field is required, a family's `afi` and `safi` and `gracefulRestart.enabled` are. A `keepAliveTimeSeconds` larger than the `holdTimeSeconds` is refused, each as authored or as the CRD defaults it (30 and 90). Cluster-scoped and named after the component unless `objectName` names it. `authSecretRef` is a Secret's name; launcher emits no Secret for it. No EnvironmentPolicy dimension applies, and no capability is required. |
| `cilium-cidrgroup` | Kind-named Cilium CiliumCIDRGroup (launcher-native): the whole `CiliumCIDRGroupSpec`, strictly decoded; `externalCIDRs` is required, and an empty list is a group that selects no peer. Cluster-scoped and named after the component unless `objectName` names it: a Cilium network policy refers to the group by that name. No EnvironmentPolicy dimension applies, and no capability is required. |
| `cilium-clusterwidenetworkpolicy` | Kind-named Cilium CiliumClusterwideNetworkPolicy (launcher-native): `spec`, `specs` or both, each a Cilium rule, strictly decoded as the `cilium-networkpolicy` kind decodes them. A rule takes exactly one of `endpointSelector` and `nodeSelector` and at least one of `ingress`, `ingressDeny`, `egress` and `egressDeny`; the fields the API requires inside a rule are required where their parent is authored. Cluster-scoped and named after the component unless `objectName` names it. No EnvironmentPolicy dimension applies, and no capability is required. |
| `cilium-egressgatewaypolicy` | Kind-named Cilium CiliumEgressGatewayPolicy (launcher-native): the whole `CiliumEgressGatewayPolicySpec`, strictly decoded; `selectors`, `destinationCIDRs`, `egressGateway` and a gateway's `nodeSelector` are required. That an `egressIP` is an IP address is left to the API server. Cluster-scoped and named after the component unless `objectName` names it. No address or CIDR is held to the allowed registries. No EnvironmentPolicy dimension applies, and no capability is required. |
| `cilium-loadbalancerippool` | Kind-named Cilium CiliumLoadBalancerIPPool (launcher-native): the whole `CiliumLoadBalancerIPPoolSpec`, strictly decoded; no top-level field is required. An authored `disabled: false` is the API's default and is not written. Cluster-scoped and named after the component unless `objectName` names it. No EnvironmentPolicy dimension applies, and no capability is required. |
| `cilium-localredirectpolicy` | Kind-named Cilium CiliumLocalRedirectPolicy (launcher-native): the whole `CiliumLocalRedirectPolicySpec`, strictly decoded; `redirectFrontend`, `redirectBackend`, the backend's `localEndpointSelector` and `toPorts`, and a port's `port` and `protocol` are required. A frontend with neither or both of `addressMatcher` and `serviceMatcher` is refused. Namespaced and named after the component unless `objectName` names it. The API refuses a change of the frontend, the backend and `skipRedirectFromBackend` once the object exists; a build does not see that. No EnvironmentPolicy dimension applies, and no capability is required. |
| `cilium-networkpolicy` | Kind-named CiliumNetworkPolicy (launcher-native): its `spec` (one rule) and `specs` (a list of rules), each the whole Cilium rule, strictly decoded. Emits identity and the authored rules. Refuses a policy Cilium rejects, at admission by its CRD schema or when the agent reads an object the API server stored, unenforced: no rule at all, a rule with no `endpointSelector` (none is filled; `{}` selects every endpoint), a rule with a `nodeSelector`, and a rule with no `ingress`, `ingressDeny`, `egress` or `egressDeny` entry. Distinct from the `cilium-networkpolicy` trait, which publishes one rule's `endpointSelector`, `ingress` and `egress`; the EnvironmentPolicy's capability lists do not refuse it. |
| `cilium-nodeconfig` | Kind-named Cilium CiliumNodeConfig (launcher-native): the whole `CiliumNodeConfigSpec`, strictly decoded; `defaults` and `nodeSelector` are required. The keys and values of `defaults` are written as authored and are not checked. Namespaced and named after the component unless `objectName` names it. No EnvironmentPolicy dimension applies, and no capability is required. |
| `clusterexternalsecret` | Kind-named External Secrets Operator ClusterExternalSecret (launcher-native): the whole `ClusterExternalSecretSpec`, strictly decoded; `externalSecretSpec` is required, with what an `externalsecret` requires, and the `key` and `operator` of a match expression of `namespaceSelector` and `namespaceSelectors`. Cluster-scoped and named after the component unless `objectName` names it. The ExternalSecrets are the operator's to create. `externalSecretSpec.target.manifest` is held to the EnvironmentPolicy as on an `externalsecret`. No capability is required. |
| `clusterissuer` | Kind-named cert-manager ClusterIssuer (launcher-native): the `IssuerSpec` of `issuer` and its policy check, cluster-scoped. Named after the component unless `objectName` names it. No capability is required. |
| `clusterrole` | Kind-named RBAC ClusterRole (launcher-native): the object's own fields (`rules`, `aggregationRule`), strictly decoded; a rule's `verbs` are required and either its `apiGroups` and `resources` or its `nonResourceURLs`, and an `aggregationRule` needs a selector, a match expression of which is refused without its `key` or `operator`, with an unknown operator, or with `values` that do not go with the operator. Cluster-scoped and named after the component unless `objectName` names it. **Ungated: no capability and no EnvironmentPolicy check restricts what a role grants.** |
| `clusterrolebinding` | Kind-named RBAC ClusterRoleBinding (launcher-native): the object's own fields (`subjects`, `roleRef`), strictly decoded; `roleRef` with its `kind` and `name`, a subject's `kind` and `name` and a ServiceAccount subject's `namespace` are required, and the names are the author's literals. Cluster-scoped and named after the component unless `objectName` names it. **Ungated: no capability and no EnvironmentPolicy check restricts what a role grants.** |
| `clustersecretstore` | Kind-named External Secrets Operator ClusterSecretStore (launcher-native): the `SecretStoreSpec` of `secretstore` and its policy check, cluster-scoped. Named after the component unless `objectName` names it. No capability is required. |
| `cnpg-backup` | Kind-named CloudNativePG Backup (launcher-native): the whole `BackupSpec`, strictly decoded; `cluster` with its `name` is required. A one-shot request: the operator takes the backup once and the API refuses every change of the spec, so the object applied again runs nothing. Namespaced and named after the component unless `objectName` names it. No EnvironmentPolicy dimension applies, and no capability is required. |
| `cnpg-cluster` | CloudNativePG `Cluster` (launcher-native): the whole `postgresql.cnpg.io/v1` `ClusterSpec` as properties, strictly decoded, with no launcher defaults beyond the CRD's `instances: 1`. Environment policy and the primary endpoint match `postgresql`. See the operator CR components design. |
| `cnpg-clusterimagecatalog` | Kind-named CloudNativePG ClusterImageCatalog (launcher-native): the same `ImageCatalogSpec` and the same policy check, cluster-scoped. Named after the component unless `objectName` names it. No capability is required. |
| `cnpg-database` | CloudNativePG `Database` (launcher-native): the whole `DatabaseSpec`, strictly decoded; writes only the CRD's `ensure: present` the Go type cannot omit. |
| `cnpg-databaserole` | Kind-named CloudNativePG DatabaseRole (launcher-native): the whole `DatabaseRoleSpec`, strictly decoded; `cluster` with its `name`, and `name`, are required. The names the CRD reserves (`postgres`, `streaming_replica`, a `pg_` or `cnpg_` prefix), `ensure: absent`, `passwordSecret` beside `disablePassword: true`, an enabled client certificate without `login: true`, `connectionLimit: 0` and an empty `ensure` or `databaseRoleReclaimPolicy` are refused. Namespaced and named after the component unless `objectName` names it. `passwordSecret` is a Secret's name; launcher emits no Secret for it. No EnvironmentPolicy dimension applies, and no capability is required. |
| `cnpg-imagecatalog` | Kind-named CloudNativePG ImageCatalog (launcher-native): the whole `ImageCatalogSpec` (`images`, `componentImages`), strictly decoded; `images`, an image's `image` and `major`, and a component image's `key` and `image` are required. Two images of one major version, and two component images of one key, are refused. Namespaced and named after the component unless `objectName` names it: a Cluster refers to the catalog by that name. Every image the catalog names is held to the EnvironmentPolicy allowed registries. No capability is required. |
| `cnpg-objectstore` | Barman Cloud plugin `ObjectStore` (launcher-native): the whole `barmancloud.cnpg.io/v1` `ObjectStoreSpec`, strictly decoded. Policy caps the plugin sidecar's cpu and memory. |
| `cnpg-pooler` | CloudNativePG `Pooler` (launcher-native): the whole `PoolerSpec`, strictly decoded, with no launcher defaults. Pod-template policy; no instance policy. The pooler endpoint matches `postgresql`'s. |
| `cnpg-publication` | Kind-named CloudNativePG Publication (launcher-native): the whole `PublicationSpec`, strictly decoded; `cluster` with its `name`, `name`, `dbname` and `target` are required. A target takes exactly one of `allTables: true` and `objects`, an object exactly one of `tablesInSchema` and `table`, and no table lists its columns beside a schema's tables. Namespaced and named after the component unless `objectName` names it. The API refuses a change of the Cluster, the name and the database once the object exists, and of `allTables` from one authored value to another (a target moved between `allTables` and `objects` is not refused by that rule); a build does not see that. No EnvironmentPolicy dimension applies, and no capability is required. |
| `cnpg-scheduledbackup` | Kind-named CloudNativePG ScheduledBackup (launcher-native): the whole `ScheduledBackupSpec`, strictly decoded; `schedule` and `cluster` with its `name` are required. The schedule, a cron expression of six fields, is read by the operator and not parsed. Namespaced and named after the component unless `objectName` names it. No EnvironmentPolicy dimension applies, and no capability is required. |
| `cnpg-subscription` | Kind-named CloudNativePG Subscription (launcher-native): the whole `SubscriptionSpec`, strictly decoded; `cluster` with its `name`, `name`, `dbname`, `publicationName` and `externalClusterName` are required. Namespaced and named after the component unless `objectName` names it. The publisher is an external cluster the subscriber Cluster defines; that it does is not checked. No EnvironmentPolicy dimension applies, and no capability is required. |
| `configmap` | Kind-named ConfigMap (launcher-native): `data` (strings only), `binaryData`, `immutable`. Distinct from the `configmap` trait. |
| `crd` | Emits `CustomResourceDefinition` manifests from a multi-doc YAML source — `inline:` (offline) or `url:` (http/https only; `oci://` not yet supported). Rejects any non-CRD document, and rejects a CRD document that authors `metadata.namespace` (a `CustomResourceDefinition` is always cluster-scoped; the Kubernetes API forbids a namespace on a cluster-scoped object). Emitted CRDs are auto-staged early by stack-compile's CRD inference. URL hosts are constrained by the policy registry allowlist (`AllowedRegistries`), re-checked on every redirect. |
| `cronjob` | Scheduled task: CronJob |
| `csidriver` | Kind-named CSIDriver (launcher-native): the whole `CSIDriverSpec`, strictly decoded; no field is required. Cluster-scoped, and the object's name (the component's, or its `objectName`) is the CSI driver's name. No EnvironmentPolicy dimension applies. |
| `daemonset` | DaemonSet for node-level agents. `ports` declares the main container's ports; it emits no Service (go-kure/launcher#690), so a `service` component authored beside it exposes the pods and carries any ingress, httproute or expose trait. |
| `deployment` | Kind-named Deployment: the shared container-level and pod-level surface plus the rest of `DeploymentSpec` (`strategy`, `minReadySeconds`, `revisionHistoryLimit`, `paused`, `progressDeadlineSeconds`). Not a superset of `worker`, which publishes `topologySpread` and an `affinity` shorthand that `deployment` does not; conversely `deployment` publishes the raw `corev1` `affinity`, `tolerations` and `topologySpreadConstraints` that `worker` does not, since those are API fields rather than launcher opinions. No `port` and no Service: use `webservice` when launcher should create one, or pair it with a `service` component. The routing traits (`expose`, `ingress`, `httproute`) are accepted here but not self-sufficient — with no service port, an implicitly-backed route fails the build, so they need `servicePort` (and `serviceName` when it differs from the component name) naming a Service that exists independently. See the component handler README for the full rule. |
| `endpointslice` | Kind-named EndpointSlice (launcher-native): the object's own fields (`addressType`, `endpoints`, `ports`), strictly decoded; `addressType`, an endpoint's `addresses` and the `name` of a zone or node hint are required. It belongs to a Service through the `kubernetes.io/service-name` label, authored under `labels` as a literal that does not follow a Service's `objectName`. Namespaced and named after the component unless `objectName` names it. No address or FQDN is held to the allowed registries. No EnvironmentPolicy dimension applies, and no capability is required. |
| `externalsecret` | Kind-named External Secrets Operator ExternalSecret (launcher-native): the whole `ExternalSecretSpec` (`secretStoreRef`, `target`, `refreshPolicy`, `refreshInterval`, `syncWindows`, `data`, `dataFrom`), strictly decoded; no top-level field is required, a `data` entry's `secretKey` and `remoteRef.key` are. Named after the component unless `objectName` names it. The store is the author's; launcher emits no store and no Secret for it. A generator named as the source of a `data` entry is refused. A `target.manifest` (an object of another kind the operator writes instead of a Secret) gets no more than the EnvironmentPolicy gives that kind on `passthrough`: a kind the policy checks is refused, since its content is not known at build, and a core Secret under a policy that forbids explicit secrets; any other kind passes. An `apiVersion` there that is no API version is refused under every policy. No capability is required. Beside the `external-secret` trait, which derives an ExternalSecret for a workload. |
| `fluxcd-alert` | Kind-named Flux `Alert` (launcher-native): the whole `AlertSpec`, strictly decoded; `providerRef` with its `name` and `eventSources`, each with its `kind` and `name`, are required, the fields the linked Go source marks required. Emits only the Alert, named after the component unless `objectName` names it, in the Flux namespace when one is set. A source's `namespace` is written as authored: nothing gates the events of another namespace's objects. No EnvironmentPolicy dimension applies and no capability is required. |
| `fluxcd-provider` | Kind-named Flux `Provider` (launcher-native): the whole `ProviderSpec` of `notification.toolkit.fluxcd.io/v1beta3`, strictly decoded; `type` is required, and of an authored `secretRef`, `proxySecretRef` or `certSecretRef` its `name`, the fields the linked Go source marks required. `interval` and `timeout` are each held to the pattern of their field: `timeout` takes no `h`, and one of an hour or more is written in minutes. A user or a password in `address` or `proxy` is refused under every policy and under none, by the rule that refuses one in an inline chart source's URL; the hosts of both are written as authored and not held, since the Provider sends events there and fetches no artifact. Under an EnvironmentPolicy that forbids explicit secrets an `address` that is the credential is refused: that of `discord`, `generic`, `generic-hmac`, `googlechat`, `lark`, `msteams`, `rocket` and `slack`, and an `azureeventhub` address that holds `SharedAccessKey`; the URL belongs under the `address` key of the Secret `secretRef` names. The API's expression rule, which allows `commitStatusExpr` only on the git provider types, is not checked; `commitStatusExpr` is a CEL expression the controller evaluates. Emits only the Provider, named after the component unless `objectName` names it, in the Flux namespace when one is set; its three Secrets are reported as read there. `serviceAccountName` is written as authored. No capability is required. |
| `fluxcd-receiver` | Kind-named Flux `Receiver` (launcher-native): the whole `ReceiverSpec` of `notification.toolkit.fluxcd.io/v1`, strictly decoded; `type` and `resources`, each with its `kind` and `name`, are required, and of an authored `secretRef` its `name`, of an OIDC provider its `issuerURL` and `validations`, of a validation its `expression` and `message` and of a variable its `name` and `expression`, the fields the linked Go source marks required. `interval` is held to the pattern of a Flux duration. The API's four expression rules, which tie `secretRef` and `oidcProviders` to `type`, are not checked; `resourceFilter`, a resource's `filter` and the OIDC expressions are CEL the controller evaluates. Emits only the Receiver, named after the component unless `objectName` names it, in the Flux namespace when one is set; the Secret of `secretRef` is reported as read there. The Receiver opens an inbound path on the notification controller, and a resource's `namespace` is written as authored: nothing gates a webhook that reconciles the objects of another namespace. No EnvironmentPolicy dimension applies and no capability is required. |
| `gateway` | Kind-named Gateway API Gateway (launcher-native): the whole `GatewaySpec`, strictly decoded; `gatewayClassName` and `listeners` are required, and of a listener its `name`, `port` and `protocol`. Named after the component unless `objectName` names it. The class is the author's. No EnvironmentPolicy dimension applies, no host is held to the allowed registries and no capability is required: it is not the Gateway a capability names for the `httproute` trait. |
| `gatewayclass` | Kind-named Gateway API GatewayClass (launcher-native): the whole `GatewayClassSpec`, strictly decoded; `controllerName` is required. Cluster-scoped. Named after the component unless `objectName` names it. No EnvironmentPolicy dimension applies and no capability is required. |
| `gitrepository` | Kind-named Flux `GitRepository`: the top-level keys of `GitRepositorySpec`, decoded strictly. `url` is required and must start with `http://`, `https://` or `ssh://` (an scp-style `git@host:path` is refused). `interval` defaults to `60m`. The `url` host, with the user of an `ssh://` URL dropped, is constrained by `AllowedRegistries`. Emits only the GitRepository, in the Flux namespace when one is set. |
| `grpcroute` | Kind-named Gateway API GRPCRoute (launcher-native): the whole `GRPCRouteSpec` (`parentRefs`, `useDefaultGateways`, `hostnames`, `rules`), strictly decoded; no field is required or filled, as on an `httproute`. Named after the component unless `objectName` names it. The parents and the backends are the author's, those of another namespace included. No EnvironmentPolicy dimension applies, no NetworkPolicy allow rule is synthesized and no capability is required. |
| `helm` | Role-named Helm component, lowered by a component-position rule to the kind-named terminals. **`delivery: flux` (default):** a `helmrelease` under the component's name, referencing its source through `chart.spec.sourceRef` (HelmRepository, or a referenced GitRepository or Bucket, with `chart` as the chart's path in the artifact; `version` is refused there and `reconcileStrategy: Revision` is set) or `chartRef` (OCIRepository, HelmChart). **Generated source:** an inline `source.url` also emits a `helmrepository` (http(s)://) or an `ocirepository` (oci://, `ref.tag` = `version`); with `kind: GitRepository` a `gitrepository` (the URL plus exactly one `source.ref` field), and `kind: Bucket` with `endpoint` and `bucketName` (no URL) a `bucket`. It is named `<application>-source-<digest of the content identity>`, where the identity is `helm:<url>`, `oci:<url>:<version>`, `git:` plus the JSON of URL and ref, or `bucket:` plus the JSON of the keys that locate the bucket. Components of one application with the same identity share one source; other applications never do. `source.name` beside the inline source names it instead, and the consumer's `Naming` hook may name the shared one (role `helm-source`); `valuesConfigMapName` and `valuesSecretName` name the values ConfigMap and Secret, and `helmReleaseName` names the HelmRelease object (not the Helm release, which is `releaseName`; role `helm-release`; refused under `delivery: template`) (go-kure/launcher#787). The source keeps its terminal's default interval and is applied with the application bundle, ahead of every ordered group, so no consumer can be ordered ahead of it. An inline HelmRepository or OCIRepository URL carrying a user or password is refused under either delivery, since it would be copied into the output in plain text; credentials go in an authored source's `secretRef`, referenced by name. **Reference form:** `source.name` with `kind` and an optional `namespace` references an existing source and emits nothing else. **`delivery: template`:** a `helmtemplate` with the URL inline and any authored `releaseName`; a source reference, an inline GitRepository or Bucket, `valuesMode: configMap`, an OCI source without `version`, and every HelmRelease-only key (`interval`, `targetNamespace`, `driftDetection`, `install`, `upgrade`, `valuesFrom`) are refused. **Decoding:** properties are decoded strictly, and `valuesMode` is never forwarded. **`valuesMode: configMap`** (go-kure/launcher#702) moves non-empty `values` into a `configmap` trait appended to the `helmrelease`, named `<component>-values-<hash of its content>`, and references it ahead of the authored `valuesFrom` entries. It replaces the `helmchart` composite, removed by go-kure/launcher#350 (the name now belongs to the kind-named Flux `HelmChart` source, go-kure/launcher#351). See the component handler README for the full rules. |
| `helmchart` | Kind-named Flux `HelmChart`: the top-level keys of `HelmChartSpec`, decoded strictly. `chart`, `sourceRef.kind` and `sourceRef.name` are required; `interval` defaults to `60m`; a `verify` that names no `provider` is written with `provider: cosign`, the API's default, and an authored `provider: ""` is refused; enum and CEL constraints are left to the CRD, and `sourceRef` is not resolved at build time. No policy check applies: a HelmChart fetches through the source it references. Emits only the HelmChart, in the Flux namespace when one is set. A top-level key of the removed `helmchart` composite (e.g. `source`, `valuesMode`) is refused with a hint pointing to `helm`. |
| `helmrelease` | Kind-named Flux `HelmRelease`: its properties are exactly the top-level keys of `HelmReleaseSpec`, decoded strictly so an unknown or wrongly typed key at any depth is a build error. Creates no source: `chart.spec.sourceRef` or `chartRef` (exactly one of `chart` and `chartRef`) names an existing one; a `chart.spec.sourceRef` without `kind` is refused. `interval` defaults to `60m`. A `chart.spec.verify` that names no `provider` is written with `provider: cosign`, the API's default, and an authored `provider: ""` is refused. Lands in the Flux namespace when one is set, and then defaults `spec.targetNamespace` to the application namespace. `spec.releaseName` is always written: the authored value, else the component name, shortened as helm-controller shortens a name over 53 characters, so Flux never derives `<targetNamespace>-<name>` (go-kure/launcher#785). Emits only the HelmRelease: `valuesMode` is refused with a pointer to `helm`'s `valuesMode: configMap`, or a `configmap` trait plus a `valuesFrom` entry (go-kure/launcher#702). See the component handler README for the full rules. |
| `helmrepository` | Kind-named Flux `HelmRepository`: its properties are exactly the top-level keys of `HelmRepositorySpec`, decoded strictly so an unknown or wrongly typed key at any depth is a build error. `url` is required and must start with `http://`, `https://` or `oci://` (only `oci://` under `type: oci`). `interval` defaults to `60m`, except under `type: oci`, which Flux does not poll. The `url` host is constrained by the policy registry allowlist (`AllowedRegistries`); under a non-empty allowlist an `oci://` URL must also name its registry explicitly (`localhost`, or a host containing `.` or `:`), since Flux resolves any other first segment against Docker Hub. Emits only the HelmRepository, named after the component, with no source dedup; it lands in the Flux namespace when one is set, where its `secretRef` then resolves. See the component handler README, which also covers the source the `helm` rule generates. |
| `helmtemplate` | Kind-named client-side Helm render: fetches a chart at build time from `source.url` — an `http(s)://` Helm repository with `chart`, or an `oci://` chart reference with `version` (required there); `source.kind` is inferred from the scheme and checked against it, and a URL carrying a user or password is refused — renders it with `values` into the application namespace (`.Release.Namespace`) under `releaseName` (`.Release.Name`; must be a valid Helm release name, a DNS-1123 subdomain of at most 53 characters), and emits the rendered manifests in Helm hook order, registered kinds as their Go types. An unset `releaseName` defaults to the name the `helmrelease` terminal writes to `spec.releaseName` — the component name, shortened as helm-controller shortens a name over 53 characters (its first 40 characters, `-`, 12 hex digits of its SHA-256) — so `helm` releases a chart under the same name under either delivery (go-kure/launcher#785); a default Helm would refuse is a build error naming `releaseName` as the remedy. Two renders given the same `releaseName` in one namespace generate colliding objects when the chart names its objects after the release, as most do, and the build refuses that collision. A namespaced rendered object without `metadata.namespace` is given the application namespace; a namespace the chart wrote is kept, and an object whose scope is not known (a custom resource whose definition the chart does not render) is left as rendered (go-kure/launcher#794). The environment policy is applied to the render: the chart source host is checked against the registry allowlist before any fetch, and every rendered workload is held to the image and pod-security checks an authored one is (go-kure/launcher#791). No `HelmRelease`, no source CR. It is what `helm` lowers to under `delivery: template`, authorable directly. Properties are decoded strictly: any other key, including `targetNamespace`, every property only a Flux-reconciled release reads (`interval`, `driftDetection`, `install`, `upgrade`, `valuesFrom`, `valuesMode`) and `source.name`, is refused. See the component handler README. |
| `horizontalpodautoscaler` | Kind-named HorizontalPodAutoscaler (launcher-native): the whole `autoscaling/v2` `HorizontalPodAutoscalerSpec`, strictly decoded; `scaleTargetRef` and `maxReplicas` are required. Named after the component unless `objectName` names it. `maxReplicas` is held to the EnvironmentPolicy's replica maximum; no default is filled. The target is the author's and is not checked. Distinct from the `scaler` trait. |
| `httproute` | Kind-named HTTPRoute (launcher-native): the whole `HTTPRouteSpec` (`parentRefs`, `useDefaultGateways`, `hostnames`, `rules`), strictly decoded; no field is required or filled. Emits identity and the authored spec. Distinct from the `httproute` trait: a backendRef is a reference as written, no parent is synthesized from a capability's Gateway, no NetworkPolicy allow rule is synthesized for it, and the EnvironmentPolicy's capability lists do not refuse it. |
| `imagepolicy` | Kind-named Flux `ImagePolicy` (launcher-native): the whole `ImagePolicySpec`, strictly decoded; `imageRepositoryRef` with its `name` and `policy` are required, and of a `semver` policy its `range`, the fields the linked Go source marks required. `interval` is held to the pattern of a Flux duration; the API's two expression rules, which tie `interval` to `digestReflectionPolicy: Always`, are not checked: the linked module ships no CRD to hold a check to, so a component that breaks one builds and is refused at apply. Emits only the ImagePolicy, named after the component unless `objectName` names it, in the Flux namespace when one is set. The repository's `namespace` is written as authored: nothing gates a policy over the ImageRepository of another namespace. No EnvironmentPolicy dimension applies and no capability is required. |
| `imagerepository` | Kind-named Flux `ImageRepository` (launcher-native): the whole `ImageRepositorySpec`, strictly decoded; `image` and `interval` are required, of an authored `secretRef`, `proxySecretRef` or `certSecretRef` its `name` and of an authored `accessFrom` its `namespaceSelectors`, the fields the linked Go source marks required. `interval` and `timeout` are each held to the pattern of their field: `timeout` takes no `h`, and one of an hour or more is written in minutes. Emits only the ImageRepository, named after the component unless `objectName` names it, in the Flux namespace when one is set; its three Secrets are reported as read there. The registry of `image` is held to the EnvironmentPolicy's allowed registries, by the rule that holds the image of a pod; no tag rule applies, since the field names a repository. `accessFrom`, which opens the scanned tags to other namespaces, `serviceAccountName` and `insecure` are written as authored, and nothing gates them. No capability is required. |
| `imageupdateautomation` | Kind-named Flux `ImageUpdateAutomation` (launcher-native): the whole `ImageUpdateAutomationSpec`, strictly decoded; `sourceRef` with its `kind` and `name` and `interval` are required, and of an authored `git` its `commit` with the author's `email`, the fields the linked Go source marks required; a match expression of `policySelector` is refused without its `key` or `operator`. `sourceRef.kind` is defaulted by the API and required here: the Go type writes it empty when it is left out. `interval` is held to the pattern of a Flux duration. Emits only the ImageUpdateAutomation, named after the component unless `objectName` names it, in the Flux namespace when one is set; the Secret of `git.commit.signingKey` is reported as read there. The automation commits and pushes to the repository of the GitRepository `sourceRef` names: its `namespace`, the branch and the refspec are written as authored, and nothing gates them. No EnvironmentPolicy dimension applies and no capability is required. |
| `ingress` | Kind-named Ingress (launcher-native): the whole `IngressSpec` (`ingressClassName`, `defaultBackend`, `tls`, `rules`), strictly decoded; no field is required or filled. Emits identity and the authored spec. Distinct from the `ingress` trait: a backend is a Service reference as written, no NetworkPolicy allow rule is synthesized for it, the platform's hostname constraint is not applied, and the EnvironmentPolicy's capability lists, which gate trait types, do not refuse it. |
| `ingressclass` | Kind-named IngressClass (launcher-native): the whole `IngressClassSpec` (`controller`, required, and `parameters`), strictly decoded. Cluster-scoped and named after the component unless `objectName` names it. Its labels and annotations are the `labels` and `annotations` properties, the default-class annotation included. No EnvironmentPolicy dimension applies. |
| `issuer` | Kind-named cert-manager Issuer (launcher-native): the whole `IssuerSpec` (`acme`, `ca`, `vault`, `selfSigned`, `venafi`), strictly decoded; no top-level field is required, and of an issuer type that is authored the fields the API requires that the type would write empty. Named after the component unless `objectName` names it. The cpu and memory of an ACME HTTP01 solver's pod template are held to the EnvironmentPolicy maxima; no host is held to the allowed registries. No capability is required. |
| `job` | Run-to-completion task: Job. Same JobSpec-level properties as `cronjob`'s job template, plus its own `suspend` (`JobSpec.Suspend`, not the CronJobSpec field of the same name); `selector`/`manualSelector` are refused because the job controller generates the selector. |
| `limitrange` | Kind-named LimitRange (launcher-native): the whole `LimitRangeSpec`, strictly decoded. `limits` and each limit's `type` are required; `limits: []` enforces nothing. Emits identity and the authored spec. |
| `listenerset` | Kind-named Gateway API ListenerSet (launcher-native): the whole `ListenerSetSpec`, strictly decoded; `parentRef` with its `name` and at least one of `listeners`, each with its `name`, `port` and `protocol`, are required. Named after the component unless `objectName` names it. The Gateway is the author's. No EnvironmentPolicy dimension applies and no capability is required. |
| `manifests` | Emits arbitrary Kubernetes manifests from the same sources as `crd` (`inline` / `url`). Each object's scope is resolved (built-in kinds plus any CRD in the same source); namespaced objects that omit `metadata.namespace` are stamped with the build namespace, cluster-scoped objects are left untouched unless they author `metadata.namespace` themselves (rejected, for the same reason `crd` rejects it), and an unknown-scope object with no namespace fails closed. Optional `scopeOverrides: [{apiVersion, kind, scope: Cluster\|Namespaced}]` supplies an explicit scope for a kind, taking precedence over kure's own non-API-governed guess — e.g. a cluster-scoped custom resource whose CRD is installed out of band. It must agree with a CRD bundled in the same source rather than override it: a conflicting same-source CRD is rejected, naming both values, so a stale bundled CRD is a build failure to fix, not something an override can silently paper over. Overrides are also ignored for a kind whose scope the Kubernetes API itself governs (a manifest cannot redefine that), and the fail-closed default is kept for a kind with no override and no other scope source. Same URL allowlist as `crd`. The objects a source yields are held to the environment policy an authored workload is held to, as `passthrough` objects are: those of an `inline` source when the policy is applied, those of a `url` source when they are generated (go-kure/launcher#794). |
| `metallb-bfdprofile` | Kind-named MetalLB BFDProfile (launcher-native): the whole `BFDProfileSpec`, strictly decoded; no field is required. It is the timers of the BFD session of the BGP peers that name it. Namespaced, written in the build namespace, and named after the component unless `objectName` names it. No EnvironmentPolicy dimension applies and no capability is required. |
| `metallb-bgpadvertisement` | Kind-named MetalLB BGPAdvertisement (launcher-native): the whole `BGPAdvertisementSpec`, strictly decoded; no field is required. It says which pools' addresses MetalLB announces to which BGP peers, for which Services, and with which route attributes, and one that authors nothing limits none of them. `serviceSelectors` is refused beside an aggregation length other than 32 (IPv4) or 128 (IPv6), the CRD's expression rule. Namespaced, written in the build namespace, and named after the component unless `objectName` names it. No EnvironmentPolicy dimension applies and no capability is required. |
| `metallb-bgppeer` | Kind-named MetalLB BGPPeer (launcher-native), at `metallb.io/v1beta2`: the whole `BGPPeerSpec`, strictly decoded; `myASN` is required. It is a router the cluster's nodes hold a BGP session with, to which MetalLB announces addresses. A `connectTime` outside 1 to 65535 seconds, or not a whole number of seconds read in whole milliseconds, is refused, the CRD's two expression rules on a create, and so is a `peerPort: 0`, which the API would turn into 179. A `password` written into the object is refused under an EnvironmentPolicy that forbids explicit secrets; `passwordSecret` names a Secret, and launcher emits none for it. Namespaced, written in the build namespace, and named after the component unless `objectName` names it. No address is held to the allowed registries and no capability is required. |
| `metallb-community` | Kind-named MetalLB Community (launcher-native): the whole `CommunitySpec`, strictly decoded; no field is required. It gives names to BGP community values, which a BGP advertisement may attach by name. Namespaced, written in the build namespace, and named after the component unless `objectName` names it. No EnvironmentPolicy dimension applies and no capability is required. |
| `metallb-ipaddresspool` | Kind-named MetalLB IPAddressPool (launcher-native): the whole `IPAddressPoolSpec`, strictly decoded; `addresses` is required. It says which Services, in which namespaces, MetalLB gives an address of which range. Namespaced, written in the build namespace, and named after the component unless `objectName` names it. No EnvironmentPolicy dimension applies, no address is held to the allowed registries and no capability is required. |
| `metallb-l2advertisement` | Kind-named MetalLB L2Advertisement (launcher-native): the whole `L2AdvertisementSpec`, strictly decoded; no field is required. It says which pools' addresses MetalLB announces on the local network, from which nodes and interfaces, for which Services, and one that authors nothing limits none of them. Namespaced, written in the build namespace, and named after the component unless `objectName` names it. No EnvironmentPolicy dimension applies and no capability is required. |
| `mutatingwebhookconfiguration` | Kind-named MutatingWebhookConfiguration (launcher-native): its `webhooks`, strictly decoded, required as on a `validatingwebhookconfiguration`. It changes objects of any namespace after the build, so what the build checked need not hold of what is stored. Cluster-scoped and named after the component unless `objectName` names it. Only the EnvironmentPolicy's object kind rules apply. |
| `namespace` | Kind-named Namespace (launcher-native): the whole `NamespaceSpec` (`finalizers`), strictly decoded. Cluster-scoped and named after the component, which must be a DNS-1123 label. Emits identity and the authored spec; its labels and annotations are the `labels` and `annotations` properties, as on every kind component. |
| `networkpolicy` | Kind-named NetworkPolicy (launcher-native): the whole `NetworkPolicySpec` (`podSelector`, `ingress`, `egress`, `policyTypes`), strictly decoded; no top-level field is required or filled. A match expression of a selector is refused without its `key` or `operator`, with an unknown operator, or with `values` that do not go with the operator. Emits identity and the authored spec. Distinct from the `networkpolicy` trait, which always selects its component's pods and lists a direction when its key is present: here an unwritten `podSelector` selects every pod of the namespace, `policyTypes` is the author's (so `egress: []` alone isolates no egress), and the EnvironmentPolicy's capability lists do not refuse it. |
| `oci` | Reconciles an OCI artifact: emits an `OCIRepository` source CR plus a per-component Flux `Kustomization`. Properties: `source.url` (required, `oci://`), `version` (required; a tag, or `sha256:<digest>`), `path` (default `./`), `prune` (default `true`), `interval` (default `60m`), `targetNamespace` (optional), `wait` (optional boolean, no default; `true` sets the Kustomization's `spec.wait`), `healthChecks` (optional list of `{apiVersion, kind, name, namespace}` copied in order into `spec.healthChecks`; `namespace` may be omitted for a cluster-scoped kind). `wait: true` with a non-empty `healthChecks` is rejected, since Flux ignores `healthChecks` when `wait` is true; with `wait` absent or `false` and `healthChecks` absent or empty, the Kustomization is unchanged. The OCIRepository participates in source dedup keyed on URL+version, among `oci` components only (the component that comes first in the declared order emits it; a `helm` component's generated OCI source on the same artifact copies the chart layer and is separate); the Kustomization is always emitted, one per component. Both land in the Flux namespace. `kustomizationName` names the Kustomization and `source.objectName` the OCIRepository a component keeps to itself (each a DNS-1123 subdomain used as written, the component name by default; `source.objectName` keeps the source out of the dedup and is refused beside `source.name`; go-kure/launcher#787). OCI registry host is constrained by the policy registry allowlist (`AllowedRegistries`): under a non-empty allowlist `source.url` must name its registry explicitly (`oci://<registry>/<repository>`, registry `localhost` or containing `.` or `:`), since without an explicit registry Flux resolves an otherwise valid repository reference against Docker Hub, and that registry must match an entry exactly. |
| `ocirepository` | Kind-named Flux `OCIRepository`: the top-level keys of `OCIRepositorySpec`, decoded strictly. `url` is required and must start with `oci://`; `interval` defaults to `60m`; a `verify` that names no `provider` is written with `provider: cosign`, the API's default, and an authored `provider: ""` is refused. Registry host constrained by `AllowedRegistries`; under a non-empty allowlist the `url` must be `oci://<registry>/<repository>` with an explicit registry (`localhost`, or a host containing `.` or `:`), since Flux reads `oci://ghcr.io` or `oci://registry/app` as a Docker Hub repository. Emits only the OCIRepository — no Kustomization, unlike `oci` — with no source dedup, in the Flux namespace when one is set. |
| `passthrough` | Generic escape hatch (launcher-native, not ported from the downstream runtime): emits an arbitrary Kubernetes object — CRD or non-standard type — declared inline under `object:`. An object of a built-in cluster-scoped kind gets no namespace. For any other kind an authored `clusterScoped` decides, `true` or `false`; unset, a kind the scope table registers as cluster-scoped gets no namespace and an unknown kind is treated as namespaced. A value that contradicts a built-in kind's scope is refused. `object.metadata.name` defaults to the component name. For namespaced objects `object.metadata.namespace` defaults to the build namespace when unset, but an inline value is respected (intentional cross-namespace). No standard trait/port integration. The emitted object is held to the environment policy an authored workload is held to, on the kinds that check reads (workloads, claims, autoscalers and PersistentVolumes): a workload authored this way is refused where a `deployment` would be. A core Secret is refused under a policy that forbids explicit secrets (go-kure/launcher#786). An object of any other kind passes, a custom resource included, and an object of a checked kind that cannot be read is refused (go-kure/launcher#794). |
| `persistentvolume` | Kind-named PersistentVolume (launcher-native): the whole `PersistentVolumeSpec`, strictly decoded, each volume source (`nfs`, `csi`, `hostPath`, …) a top-level property. Cluster-scoped. A `hostPath` or `local` source needs an EnvironmentPolicy that allows hostPath volumes, and `capacity.storage` is held to its storage maximum. Emits identity and the authored spec. |
| `persistentvolumeclaim` | Kind-named PersistentVolumeClaim (launcher-native): `size` (or the EnvironmentPolicy storage default), `storageClassName` (`""` requests no class), `accessModes`, `volumeMode`. A workload mounts it with a `pvc` volume's `claimName`, which generates no claim and keeps `accessModes` so the workload can still read the claim's modes. |
| `pod` | Kind-named bare Pod (launcher-native): the whole `PodSpec` less `ephemeralContainers`, `priority` and `overhead`, strictly decoded; `containers` is required. Held to the EnvironmentPolicy as a rendered Pod is (host namespaces, hostPath volumes, registries, resource maxima, privilege and capability gates); no default is filled. Emits identity, the `app` label and the authored spec; `security-context`, a `configmap` mount and an `external-secret` injection apply to it. |
| `poddisruptionbudget` | Kind-named PodDisruptionBudget (launcher-native): the whole `PodDisruptionBudgetSpec` (`minAvailable`, `maxUnavailable`, `selector`, `unhealthyPodEvictionPolicy`), strictly decoded; no top-level field is required, and a match expression of `selector` is refused without its `key` or `operator`, with an unknown operator, or with `values` that do not go with the operator. Named after the component unless `objectName` names it. The selector is the author's: it is pointed at no component. Distinct from the `scaler` trait's `enablePDB`. No EnvironmentPolicy dimension applies. |
| `podmonitor` | Kind-named Prometheus operator PodMonitor (launcher-native): the whole `PodMonitorSpec`, strictly decoded; `selector` is required, the three required fields of an endpoint's `oauth2`, and the `key` and `operator` of a match expression of the selector. Named after the component unless `objectName` names it. The selector is the author's. No EnvironmentPolicy dimension applies, and no capability is required. |
| `podtemplate` | Kind-named PodTemplate (launcher-native): its one field, `template`, strictly decoded into `PodTemplateSpec`; the object's own labels and annotations are the `labels` and `annotations` properties. The pod spec is held to what the `pod` kind holds its spec to and to the EnvironmentPolicy as a rendered PodTemplate is; no default is filled. Emits identity and the authored template. A PodTemplate is stored, not run: no `app` label is added and no trait changes it. |
| `postgresql` | PostgreSQL instance (CNPG): a Cluster, with an ObjectStore, a Pooler and Databases as its properties ask. `clusterObjectName` and `objectStoreObjectName` name the Cluster and the ObjectStore otherwise than after the component, `poolerName` and `databases[].objectName` the Pooler and the Databases; the references launcher writes follow (go-kure/launcher#787). **CloudNativePG derives the Cluster's Services and Secrets from the Cluster's name, and renaming an existing Cluster creates a new one: the old one is pruned with its data unless it is protected.** |
| `priorityclass` | Kind-named PriorityClass (launcher-native): `value` (`0` when unauthored), `globalDefault`, `description`, `preemptionPolicy`, strictly decoded. Cluster-scoped and named after the component unless `objectName` names it; its labels and annotations are the `labels` and `annotations` properties. No EnvironmentPolicy dimension applies. |
| `prometheus` | Kind-named Prometheus operator Prometheus (launcher-native): the whole `PrometheusSpec`, strictly decoded, every field under its upstream name (`externalLabels` included; the spec has no top-level `labels`). Named after the component unless `objectName` names it. The pods of every shard are held to the EnvironmentPolicy as the Alertmanager's are: `replicas` times `shards` against the replica maximum, the Thanos sidecar's `image` and `resources` as the prometheus container's; the deprecated `baseImage`, `tag` and `sha` are not authorable: the spec's when not empty (an empty one writes nothing), the sidecar's whenever set, the empty string included; and no default is filled. A literal `bearerToken` of a `remoteWrite` or `remoteRead` entry or of `apiserverConfig` is refused under a policy that forbids explicit secrets. An `excludedFromEnforcement` entry that leaves `group` out is written with `monitoring.coreos.com`; an authored empty group is refused. An unset `image` or `thanos.image` is not held to the allowed registries: the operator chooses the image. No capability is required. |
| `prometheus-probe` | Kind-named Prometheus operator Probe (launcher-native): the whole `ProbeSpec`, strictly decoded; `prober.url` is required, the three required fields of an `oauth2`, and the `key` and `operator` of a match expression of `targets.ingress.selector`. Named after the component unless `objectName` names it. The prober and the targets are the author's; no host is held to the allowed registries. No EnvironmentPolicy dimension applies, and no capability is required. |
| `prometheusrule` | Kind-named Prometheus operator PrometheusRule (launcher-native): the whole `PrometheusRuleSpec` (`groups`), strictly decoded; a group's `name` and a rule's `expr` are required. Named after the component unless `objectName` names it. No EnvironmentPolicy dimension applies, and no capability is required. |
| `referencegrant` | Kind-named Gateway API ReferenceGrant (launcher-native): the whole `ReferenceGrantSpec`, strictly decoded; `from` and `to` are required, with the group, kind and namespace of a source and the group and kind of a target. It allows references into the namespace it is built in. Named after the component unless `objectName` names it. No EnvironmentPolicy dimension applies and no capability is required. |
| `replicaset` | Kind-named bare ReplicaSet (launcher-native): the whole `ReplicaSetSpec`, strictly decoded; `selector` and `template` are required. The pod template is held to what the `pod` kind holds its spec to, and `activeDeadlineSeconds` is refused on it. `replicas` and the template are held to the EnvironmentPolicy as a rendered ReplicaSet is; no default is filled. Emits identity and the authored spec, with the `app` label added to the template's labels (an authored `app` with another value is refused); `security-context`, a `configmap` mount and an `external-secret` injection apply to it. |
| `replicationcontroller` | Kind-named bare ReplicationController (launcher-native): the whole `ReplicationControllerSpec`, strictly decoded; `template` is required, `selector` (a plain label map) is optional and left to the API server's default when unset. The pod template and `replicas` are held as the `replicaset` kind's are; no default is filled. Emits identity and the authored spec, with the `app` label added to the template's labels (an authored `app` with another value is refused); `security-context`, a `configmap` mount and an `external-secret` injection apply to it. |
| `replicationdestination` | Kind-named VolSync ReplicationDestination (launcher-native): the whole `ReplicationDestinationSpec`, strictly decoded; no top-level field is required, and the `key` and `operator` of a match expression in a label selector of a mover's `moverAffinity` are. Named after the component unless `objectName` names it. The same EnvironmentPolicy checks as `replicationsource`; no host is held to the allowed registries and no capability is required. |
| `replicationsource` | Kind-named VolSync ReplicationSource (launcher-native): the whole `ReplicationSourceSpec`, strictly decoded; no top-level field is required, and the `key` and `operator` of a match expression in a label selector of a mover's `moverAffinity` are. Named after the component unless `objectName` names it. An authored capacity is held to the EnvironmentPolicy storage maximum, a mover's cpu and memory to its maxima, and a mover's `hostProcess` switch is refused unless privileged workloads are allowed. An `rsync` mover is held to the policy's container capabilities for the seven the linked operator version adds to its container. No host is held to the allowed registries and no capability is required. The `volsync` trait builds an object of the same kind for a workload's claim. |
| `resourcequota` | Kind-named ResourceQuota (launcher-native): the whole `ResourceQuotaSpec` (`hard`, `scopes`, `scopeSelector`), strictly decoded. No field is required. Emits identity and the authored spec. |
| `resourcesetinputprovider` | Kind-named Flux Operator `ResourceSetInputProvider` (launcher-native): the whole `ResourceSetInputProviderSpec` of `fluxcd.controlplane.io/v1`, strictly decoded; `type` is required, of an authored `secretRef` or `certSecretRef` its `name` and of a schedule its `cron`, the fields the linked Go source marks required. A schedule's `window` is held to the pattern of a Flux duration by its authored text, and `filter.limit: 0`, which the type omits and the API server would default to 100, is refused, as is a schedule's empty `timeZone` (default `UTC`). The API's seventeen expression rules, which tie `url`, the Secret references, `serviceAccountName`, `insecure` and `selectors` to `type`, are deliberately not checked, although the module ships the CRD; a test holds their list to that CRD. A user or a password in `url` is refused under every policy and under none, and the host of `url` is held to the EnvironmentPolicy's allowed registries whatever the type (an `oci://` url by the rule of an `ocirepository`). Emits only the ResourceSetInputProvider, named after the component unless `objectName` names it, in the Flux namespace when one is set; the Secrets of `secretRef` and `certSecretRef` are reported as read there. An `ExternalArtifact` provider's `selectors[].namespace`, `*` for every namespace, and `serviceAccountName`, without which the list is made with the operator's own client (or as its default account, when the operator is started with one), are written as authored, and nothing gates them. No capability is required. |
| `role` | Kind-named RBAC Role (launcher-native): the object's own field (`rules`), strictly decoded; a rule's `verbs`, `apiGroups` and `resources` are required and `nonResourceURLs` are refused. Namespaced and named after the component unless `objectName` names it. **Ungated: no capability and no EnvironmentPolicy check restricts what a role grants.** |
| `rolebinding` | Kind-named RBAC RoleBinding (launcher-native): the object's own fields (`subjects`, `roleRef`), strictly decoded; `roleRef` with its `kind` and `name` and a subject's `kind` and `name` are required, and the names are the author's literals. Namespaced and named after the component unless `objectName` names it. **Ungated: no capability and no EnvironmentPolicy check restricts what a role grants.** |
| `runtimeclass` | Kind-named RuntimeClass (launcher-native): `handler` (required), `overhead`, `scheduling`, strictly decoded. Cluster-scoped and named after the component unless `objectName` names it; its labels and annotations are the `labels` and `annotations` properties. No EnvironmentPolicy dimension applies. |
| `secret` | Kind-named Secret (launcher-native): `stringData` (strings only), `data` (base64), `type`, `immutable`. Every entry is emitted under `data`, base64 and not encrypted. Refused under an environment policy that forbids explicit secrets. Distinct from the `secret` trait, its twin. |
| `secretstore` | Kind-named External Secrets Operator SecretStore (launcher-native): the whole `external-secrets.io/v1` `SecretStoreSpec` (`provider`, `controller`, `retrySettings`, `refreshInterval`, `conditions`), strictly decoded; `provider` with exactly one provider is required, and of that provider the fields the API requires that the type would write empty (a Vault provider's `version` too, which the API would default), and the `key` and `operator` of a match expression of a condition's `namespaceSelector`. Named after the component unless `objectName` names it. A credential written as a `value`, and the data of the `fake` provider, are refused under an EnvironmentPolicy that forbids explicit secrets; no host is held to the allowed registries. No capability is required. |
| `service` | Kind-named Service (launcher-native): an independent component that emits only a `Service`, fronting pods another component owns. `selector` defaults to `app: <component name>`; `ports` is the full port list (`targetPort` defaults to `port`, `protocol` to `TCP`); `type` is `ClusterIP`, `NodePort` or `LoadBalancer`. The fronted workload owns its ServiceAccount. Routing traits on it default to, and accept only, the first port; a later port needs an explicit backend. Its synthesized ingress NetworkPolicy selects the `selector` pods on the routed TCP `targetPort`s. |
| `serviceaccount` | Kind-named ServiceAccount (launcher-native): `automountServiceAccountToken` and `imagePullSecrets`, and nothing else. An unauthored `automountServiceAccountToken` stays unset. A workload names it with `serviceAccountName`. |
| `servicecidr` | Kind-named ServiceCIDR (launcher-native): the whole `ServiceCIDRSpec` (`cidrs`, at least one required), strictly decoded. Cluster-scoped and named after the component unless `objectName` names it; its labels and annotations are the `labels` and `annotations` properties. No EnvironmentPolicy dimension applies. |
| `servicemonitor` | Kind-named Prometheus operator ServiceMonitor (launcher-native): the whole `monitoring.coreos.com/v1` `ServiceMonitorSpec`, strictly decoded; `endpoints` and `selector` are required, the three required fields of an endpoint's `oauth2`, and the `key` and `operator` of a match expression of the selector. Named after the component unless `objectName` names it. The selector is the author's: it is pointed at no component. No EnvironmentPolicy dimension applies, and no capability is required. |
| `statefulset` | StatefulSet for ordered, persistent workloads. `serviceName` (no default) names its governing Service, a headless `service` component authored beside it; the statefulset emits no Service (go-kure/launcher#690). |
| `storageclass` | Kind-named StorageClass (launcher-native): the object's fields beside its identity (`provisioner`, required, `parameters`, `reclaimPolicy`, `mountOptions`, `allowVolumeExpansion`, `volumeBindingMode`, `allowedTopologies`), strictly decoded. Cluster-scoped and named after the component unless `objectName` names it. Its labels and annotations are the `labels` and `annotations` properties, the default-class annotation included. No EnvironmentPolicy dimension applies. |
| `tcproute` | Kind-named Gateway API TCPRoute (launcher-native): the whole `TCPRouteSpec`, strictly decoded; `rules` and a rule's `backendRefs` are required, with the `name` of a backend and the `port` of one that is a Service. Named after the component unless `objectName` names it. The parents and the backends are the author's, those of another namespace included, as on an `httproute`. No EnvironmentPolicy dimension applies, no NetworkPolicy allow rule is synthesized and no capability is required. |
| `thanosruler` | Kind-named Prometheus operator ThanosRuler (launcher-native): the whole `ThanosRulerSpec`, strictly decoded, with one field renamed: the spec's `labels`, the Prometheus external labels of the rules it evaluates, is the `externalLabels` property, since `labels` is the object's own labels on every kind component. The reserved-key and component-label checks do not apply to `externalLabels`. Named after the component unless `objectName` names it. The pods are held to the EnvironmentPolicy as the Alertmanager's are (no `hostNetwork` in this spec), and no default is filled; the replica count and memory request the operator fills where they are unset are held. An image the operator chooses (`thanos-ruler`'s where neither `image` nor a patch names one, the config-reloader's where no patch names one) is refused under a policy with allowed registries. A listed container that names no image and patches none the operator generates is refused, and so is any init container that names none. A literal `remoteWrite[].bearerToken` is refused under a policy that forbids explicit secrets. An `excludedFromEnforcement` entry that leaves `group` out is written with `monitoring.coreos.com`, the one group the API admits; an authored empty group is refused. No capability is required. |
| `tlsroute` | Kind-named Gateway API TLSRoute (launcher-native): the whole `TLSRouteSpec`, strictly decoded; `hostnames` is required, and the rest as on a `tcproute`. An authored object on the same terms; no host is held to the allowed registries. Named after the component unless `objectName` names it. |
| `udproute` | Kind-named Gateway API UDPRoute (launcher-native): the whole `UDPRouteSpec`, strictly decoded; required as on a `tcproute`, and an authored object on the same terms. Named after the component unless `objectName` names it. |
| `validatingwebhookconfiguration` | Kind-named ValidatingWebhookConfiguration (launcher-native): its `webhooks`, strictly decoded; a webhook's `name`, `clientConfig` (exactly one of `url` and `service`), `sideEffects` and `admissionReviewVersions` are required, a rule names its operations, API groups, versions and resources, and a selector's match expression is checked as every selector's. The API server calls it on the requests of any namespace its rules match; neither the URL nor the Service is resolved at build, and a match condition's CEL is compiled by the API server. Cluster-scoped and named after the component unless `objectName` names it. Only the EnvironmentPolicy's object kind rules apply. |
| `volumeattributesclass` | Kind-named VolumeAttributesClass (launcher-native): `driverName` and `parameters` (both required, `parameters` with at least one entry), strictly decoded. Cluster-scoped and named after the component unless `objectName` names it; its labels and annotations are the `labels` and `annotations` properties. No EnvironmentPolicy dimension applies. |
| `webservice` | Long-running HTTP service: Deployment + Service + ServiceAccount, each named after the component. `deploymentObjectName`, `serviceObjectName` and `serviceAccountObjectName` name them otherwise, each on its own and used as written (the Service's a DNS-1035 label); the labels, the selectors and the trait-derived names keep the component name, and the references launcher writes follow (go-kure/launcher#787). **A renamed Service has another DNS name in the cluster, and launcher builds no such address: every address written with the component name is the author's to change.** |
| `worker` | Long-running background worker: Deployment + ServiceAccount (no Service). `deploymentObjectName` and `serviceAccountObjectName` name them otherwise, as on `webservice` (go-kure/launcher#787). |

### 4.3 Supported trait types (Phase 1)

Each type maps to a `TraitHandler` in `pkg/oam/builtin/traits/` — `expose` to a
`TraitLoweringRule` there. Rows marked launcher-native were not ported from the downstream
runtime:

| type | requires capability | description |
|---|---|---|
| `expose` | yes — `controllerType` | Dispatches to `ingress` or `httproute` based on platform |
| `ingress` | no | Kubernetes Ingress |
| `httproute` | no | Gateway API HTTPRoute |
| `certificate` | yes — `issuerRef` | cert-manager Certificate |
| `external-secret` | no — the store comes from an authored `secretStoreRef`, an optional `secretStoreRef` rendering, or an authored `provider` fallback | ExternalSecrets ExternalSecret |
| `networkpolicy` | no | Kubernetes NetworkPolicy |
| `cilium-networkpolicy` | no | CiliumNetworkPolicy |
| `rbac` | no | Role/RoleBinding (or ClusterRole/ClusterRoleBinding) bound to the ServiceAccount the pods run as: the authored `serviceAccountName`, or the account a `webservice`/`worker` generates; refused on a pod kind with no `serviceAccountName` (go-kure/launcher#702) |
| `security-context` | no | Sets the pod and container security context for a PSA level |
| `pvc` | no | PersistentVolumeClaim |
| `volsync` | no | VolSync ReplicationSource |
| `configmap` | no | ConfigMap with optional volume mount |
| `topology-spread` | no | Launcher-native (not ported from the downstream runtime): stamps launcher's default topology spread constraints — the `webservice`/`worker` `topologySpread` opinion — onto the component's Deployment from its post-policy replica count. Takes no properties and no capability rendering. |
| `force-replace` | no | Launcher-native (not ported from the downstream runtime): opt-in; sets the `ForceReplace` delivery intent on the component's applications, its trait sub-applications included (go-kure/launcher#782). kure's Flux workflow turns it into `kustomize.toolkit.fluxcd.io/force: enabled` on their objects, so Flux deletes and recreates an object whose update fails on an immutable field (a Job's pod template). Replacing a Job re-runs it. Takes no properties and no capability rendering. |
| `scaler` | no | HPA + optional PDB |
| `prune-protection` | no | Sets the `PruneProtection` delivery intent on the component's applications, its trait sub-applications included (go-kure/launcher#782). kure's Flux workflow turns it into `kustomize.toolkit.fluxcd.io/prune: disabled` on their objects, so Flux never garbage-collects them. Takes no properties. |

`pkg/oam/builtin/traits/README.md` is the authoritative trait catalog; its tables list each
trait's key properties, not every accepted field. The one
downstream trait with no launcher counterpart is `backup`, which depends on the downstream
delivery pipeline and has no meaning in a static manifest build. `fluxcd-patches` and
`fluxcd-postbuild` configure the Flux `Kustomization` that delivers a bundle and are not
built in either (go-kure/launcher#781): a consumer that delivers through Flux registers its
own handlers for them.

### 4.4 OAM policies

Each `spec.policies` entry is dispatched by `type` to a `PolicyHandler` in
`pkg/oam/builtin/policies/`; a type with no handler fails the build with
`no handler for policy type`. A policy shapes how the application is grouped and
ordered, not which objects it emits:

| type | description |
|---|---|
| `dependency` | Orders components of this application: `rules[]` of `{component, dependsOn[]}`. Referenced components must exist; self-dependencies and cycles are rejected. A rule splits the application bundle into ordered groups, the levels of the declared order, each depending on the group before it; an edge against the tier order of `placement` is refused. A source shared by several `oci` components is emitted by the one that comes first, so it never waits on another component that needs it. |
| `placement` | Places a component in a tier: `component`, `tier` (`infra`, `services` or `apps`); a second placement of the same component in a different tier is an error. Tiers deploy in order (`infra`, `services`, `apps`). No component type has a default tier: a component nothing places is ordered after no tier, so a single placement orders nothing. |

`app-dependency` (ordering one application after others) is not built in: `kurel build`
builds a single application and has nothing to order it against. A caller that
orchestrates several applications registers its own handler for it. `reconciliation` and
`health-checks` are not built in either: they configure delivery, which launcher leaves to
the consumer that delivers the application (go-kure/launcher#781; see
`docs/delivery-scope.md`). Such a consumer registers its own handler and returns what it
read through `PolicyResult.Extensions`.

```yaml
spec:
  # ...
  policies:
  - name: deploy-order
    type: dependency
    properties:
      rules:
      - component: web
        dependsOn: [db]
```

These are distinct from the `Policy` interface (`options-policy-interface.md`), the
environment constraints a caller passes in the transform context; `kurel build`
uses `NoopPolicy` for that.

---

## 5. Two-Parameter-Set Model

Every kurel build receives exactly two parameter sets:

**Set 1 — Platform profile (`--profile cluster.yaml`)**

Describes how the platform implements each trait. This is an environment-level input,
supplied by the platform operator and shared across all applications on a cluster.
Represented as a `ClusterProfile` document. See `docs/oam/design-cluster-profile.md`.

**Set 2 — Application values**

Describes what this specific deployment needs: image, replica count, domain names, etc.
This is a per-deployment input, supplied by the application team at build time.

The two sets are merged at different stages:
- Platform profile rendering is merged into trait properties before handler invocation
  (capability resolution, see ClusterProfile design)
- Application values are merged into component and trait properties
  (`${var}` placeholder substitution — see §6)

### Separation of concerns

```
┌─────────────────────────────────────────────────────────────────┐
│  Application team provides:                                     │
│  - app.yaml (launcher Application — what to run, what capabilities)  │
│  - values  (image, replicas, domains — per deployment)          │
└─────────────────────┬───────────────────────────────────────────┘
                      │
┌─────────────────────▼───────────────────────────────────────────┐
│  kurel build                                                    │
│  1. Resolve application values into OAM Application             │
│  2. Load ClusterProfile (platform profile)                         │
│  3. For each trait: merge capability rendering into properties     │
│  4. Dispatch to component and trait handlers                    │
│  5. Output: static Kubernetes manifests                         │
└─────────────────────┬───────────────────────────────────────────┘
                      │
┌─────────────────────▼───────────────────────────────────────────┐
│  Platform operator provides:                                    │
│  - cluster.yaml (ClusterProfile — how traits are implemented)   │
└─────────────────────────────────────────────────────────────────┘
```

---

## 6. Parameter Syntax

Application values are expressed as `${name}` placeholders in `app.yaml`. The resolver
substitutes all placeholders using the values supplied at build time before the Application
is parsed or dispatched to handlers. For the full design rationale see
`docs/oam/options-param-syntax.md`.

### 6.1 Parameter declarations in kurel.yaml

Each parameter has a name, type, required flag, optional default, and optional description.

> **Schema vocabulary.** Internally a parameter is `ParameterDecl` = `name` plus the
> shared `PropertySchema` vocabulary (the same type used by handler properties and capability
> rendering). Parameters remain an *ordered list* — a default may reference only earlier
> parameters — and are restricted to the flat subset: the rich `PropertySchema` fields (`enum`,
> nested `properties`, `items`, `additionalProperties`) are rejected at decode time. Unifying the
> type does not change the accepted wire format.

```yaml
spec:
  parameters:
  - name: image
    type: string
    required: true
    description: "Container image with tag, e.g. registry/app:v1.2.3"
  - name: replicas
    type: integer
    required: false
    default: 1
  - name: domain
    type: string
    required: true
  - name: tlsSecret
    type: string
    required: false
    default: "${name}-tls"   # may reference other parameters
```

Supported types: `string`, `integer`, `boolean`, `array`, `object`. An `array` or `object`
parameter carries a YAML list or map and is substituted as a whole node (6.3). Its default,
if any, must be a list or map; a string default is refused, not parsed as YAML.

### 6.2 Placeholder syntax in app.yaml

```yaml
spec:
  components:
  - name: web
    type: webservice
    properties:
      image: "${image}"          # scalar substitution — resolves to string
      replicas: ${replicas}      # scalar substitution — resolves to integer
    traits:
    - type: expose
      properties:
        rules:
        - host: "${domain}"
    - type: certificate
      properties:
        secretName: "${tlsSecret}"
        dnsNames:
        - "${domain}"
```

### 6.3 Resolver behaviour

**Scalar substitution** — when `${name}` is the entire value of a YAML field, the resolver
replaces it with the typed value from the parameter declaration:
- `image: "${image}"` → `image: "myregistry/app:v1.2.3"` (string)
- `replicas: ${replicas}` → `replicas: 3` (integer, not string `"3"`)

**Node substitution** — for an `array` or `object` parameter, a whole-value `${name}` is
replaced by the value's YAML list or map:
- `env: ${env}` → `env: [{name: LOG_LEVEL, value: info}]`
- Only the shape (list or map) is checked against the parameter, since a parameter declares
  no `items` or `properties`; the substituted value is then validated by the consuming
  component's or trait's schema like any authored property
- The replacement is not scanned again: a `${…}` inside a supplied value stays literal
- Embedding an `array`/`object` placeholder inside a larger string is an error, in the
  application template and in another parameter's string default alike; an anchor on the
  placeholder stays on the substituted node

**Inline string embedding** — when `${name}` is embedded inside a larger string value:
- `secretName: "${name}-tls"` → `secretName: "webservice-tls"` (always a string)

### 6.4 Supplying values at build time

```sh
kurel build . --profile cluster.yaml --values values.yaml

# --set flags — scalars only; supply array/object parameters with --values
kurel build . --profile cluster.yaml \
    --set image=myregistry/app:v1.2.3 \
    --set replicas=3 \
    --set domain=app.example.com
```

```yaml
# values.yaml
image: myregistry/app:v1.2.3
replicas: 3
domain: app.example.com
```

### 6.5 Validation

- Missing required parameter → build error naming the parameter before any resolution
- Optional parameter with no value → default value used
- `default` may itself contain `${name}` references to other parameters; these are
  resolved in declaration order
- `default` values are type-checked at `ParsePackage` time; a string default that is
  not parseable as the declared type (e.g. `default: foo` for `type: integer`) is
  rejected immediately

---

## 7. Build Invocation

```sh
kurel build <package-dir> \
    --profile cluster.yaml \
    [--values values.yaml | --set key=value]
```

Output is static Kubernetes manifests on stdout (YAML, multi-document). Pipe into
`kubectl apply`, a GitOps repo, or a CI artifact store.

The `--profile` flag is required. Without a profile, capability-aware traits will fail
with `ErrMissingCapability`.
