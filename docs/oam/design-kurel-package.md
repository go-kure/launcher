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
| `webservice` | Long-running HTTP service: Deployment + Service |
| `worker` | Long-running background worker: Deployment (no Service) |
| `deployment` | Kind-named Deployment: the shared container-level and pod-level surface plus the rest of `DeploymentSpec` (`strategy`, `minReadySeconds`, `revisionHistoryLimit`, `paused`, `progressDeadlineSeconds`). Not a superset of `worker`, which publishes `topologySpread` and an `affinity` shorthand that `deployment` does not; conversely `deployment` publishes the raw `corev1` `affinity`, `tolerations` and `topologySpreadConstraints` that `worker` does not, since those are API fields rather than launcher opinions. No `port` and no Service: use `webservice` when launcher should create one, or pair it with a `service` component. The routing traits (`expose`, `ingress`, `httproute`) are accepted here but not self-sufficient — with no service port, an implicitly-backed route fails the build, so they need `servicePort` (and `serviceName` when it differs from the component name) naming a Service that exists independently. See the component handler README for the full rule. |
| `service` | Kind-named Service (launcher-native): an independent component that emits only a `Service`, fronting pods another component owns. `selector` defaults to `app: <component name>`; `ports` is the full port list (`targetPort` defaults to `port`, `protocol` to `TCP`); `type` is `ClusterIP`, `NodePort` or `LoadBalancer`. The fronted workload owns its ServiceAccount. Routing traits on it default to, and accept only, the first port; a later port needs an explicit backend. Its synthesized ingress NetworkPolicy selects the `selector` pods on the routed TCP `targetPort`s. |
| `serviceaccount` | Kind-named ServiceAccount (launcher-native): `automountServiceAccountToken` and `imagePullSecrets`, and nothing else. An unauthored `automountServiceAccountToken` stays unset. A workload names it with `serviceAccountName`. |
| `persistentvolumeclaim` | Kind-named PersistentVolumeClaim (launcher-native): `size` (or the EnvironmentPolicy storage default), `storageClassName` (`""` requests no class), `accessModes`, `volumeMode`. A workload mounts it with a `pvc` volume's `claimName`, which generates no claim and keeps `accessModes` so the workload can still read the claim's modes. |
| `configmap` | Kind-named ConfigMap (launcher-native): `data` (strings only), `binaryData`, `immutable`. Distinct from the `configmap` trait. |
| `namespace` | Kind-named Namespace (launcher-native): the whole `NamespaceSpec` (`finalizers`), strictly decoded. Cluster-scoped and named after the component, which must be a DNS-1123 label. Emits identity and the authored spec; its labels are not authorable. |
| `limitrange` | Kind-named LimitRange (launcher-native): the whole `LimitRangeSpec`, strictly decoded. `limits` and each limit's `type` are required; `limits: []` enforces nothing. Emits identity and the authored spec. |
| `resourcequota` | Kind-named ResourceQuota (launcher-native): the whole `ResourceQuotaSpec` (`hard`, `scopes`, `scopeSelector`), strictly decoded. No field is required. Emits identity and the authored spec. |
| `persistentvolume` | Kind-named PersistentVolume (launcher-native): the whole `PersistentVolumeSpec`, strictly decoded, each volume source (`nfs`, `csi`, `hostPath`, …) a top-level property. Cluster-scoped. A `hostPath` or `local` source needs an EnvironmentPolicy that allows hostPath volumes, and `capacity.storage` is held to its storage maximum. Emits identity and the authored spec. |
| `pod` | Kind-named bare Pod (launcher-native): the whole `PodSpec` less `ephemeralContainers`, `priority` and `overhead`, strictly decoded; `containers` is required. Held to the EnvironmentPolicy as a rendered Pod is (host namespaces, hostPath volumes, registries, resource maxima, privilege and capability gates); no default is filled. Emits identity, the `app` label and the authored spec; `security-context`, a `configmap` mount and an `external-secret` injection apply to it. |
| `replicaset` | Kind-named bare ReplicaSet (launcher-native): the whole `ReplicaSetSpec`, strictly decoded; `selector` and `template` are required. The pod template is held to what the `pod` kind holds its spec to, and `activeDeadlineSeconds` is refused on it. `replicas` and the template are held to the EnvironmentPolicy as a rendered ReplicaSet is; no default is filled. Emits identity and the authored spec, with the `app` label added to the template's labels (an authored `app` with another value is refused); `security-context`, a `configmap` mount and an `external-secret` injection apply to it. |
| `replicationcontroller` | Kind-named bare ReplicationController (launcher-native): the whole `ReplicationControllerSpec`, strictly decoded; `template` is required, `selector` (a plain label map) is optional and left to the API server's default when unset. The pod template and `replicas` are held as the `replicaset` kind's are; no default is filled. Emits identity and the authored spec, with the `app` label added to the template's labels (an authored `app` with another value is refused); `security-context`, a `configmap` mount and an `external-secret` injection apply to it. |
| `podtemplate` | Kind-named PodTemplate (launcher-native): its one field, `template`, strictly decoded into `PodTemplateSpec`; the object's own `metadata` is not authorable. The pod spec is held to what the `pod` kind holds its spec to and to the EnvironmentPolicy as a rendered PodTemplate is; no default is filled. Emits identity and the authored template. A PodTemplate is stored, not run: no `app` label is added and no trait changes it. |
| `storageclass` | Kind-named StorageClass (launcher-native): the object's fields beside its identity (`provisioner`, required, `parameters`, `reclaimPolicy`, `mountOptions`, `allowVolumeExpansion`, `volumeBindingMode`, `allowedTopologies`), strictly decoded. Cluster-scoped and named after the component unless `objectName` names it. Its `metadata` is not authorable, so the default-class annotation cannot be set. No EnvironmentPolicy dimension applies. |
| `volumeattributesclass` | Kind-named VolumeAttributesClass (launcher-native): `driverName` and `parameters` (both required, `parameters` with at least one entry), strictly decoded. Cluster-scoped and named after the component unless `objectName` names it; its `metadata` is not authorable. No EnvironmentPolicy dimension applies. |
| `priorityclass` | Kind-named PriorityClass (launcher-native): `value` (`0` when unauthored), `globalDefault`, `description`, `preemptionPolicy`, strictly decoded. Cluster-scoped and named after the component unless `objectName` names it; its `metadata` is not authorable. No EnvironmentPolicy dimension applies. |
| `runtimeclass` | Kind-named RuntimeClass (launcher-native): `handler` (required), `overhead`, `scheduling`, strictly decoded. Cluster-scoped and named after the component unless `objectName` names it; its `metadata` is not authorable. No EnvironmentPolicy dimension applies. |
| `ingressclass` | Kind-named IngressClass (launcher-native): the whole `IngressClassSpec` (`controller`, required, and `parameters`), strictly decoded. Cluster-scoped and named after the component unless `objectName` names it. Its labels and annotations are not authorable, so the default-class annotation cannot be set. No EnvironmentPolicy dimension applies. |
| `csidriver` | Kind-named CSIDriver (launcher-native): the whole `CSIDriverSpec`, strictly decoded; no field is required. Cluster-scoped, and the object's name (the component's, or its `objectName`) is the CSI driver's name. No EnvironmentPolicy dimension applies. |
| `cronjob` | Scheduled task: CronJob |
| `job` | Run-to-completion task: Job. Same JobSpec-level properties as `cronjob`'s job template, plus its own `suspend` (`JobSpec.Suspend`, not the CronJobSpec field of the same name); `selector`/`manualSelector` are refused because the job controller generates the selector. |
| `postgresql` | PostgreSQL instance (CNPG) |
| `cnpg-cluster` | CloudNativePG `Cluster` (launcher-native): the whole `postgresql.cnpg.io/v1` `ClusterSpec` as properties, strictly decoded, with no launcher defaults beyond the CRD's `instances: 1`. Environment policy and the primary endpoint match `postgresql`. See the operator CR components design. |
| `cnpg-pooler` | CloudNativePG `Pooler` (launcher-native): the whole `PoolerSpec`, strictly decoded, with no launcher defaults. Pod-template policy; no instance policy. The pooler endpoint matches `postgresql`'s. |
| `cnpg-database` | CloudNativePG `Database` (launcher-native): the whole `DatabaseSpec`, strictly decoded; writes only the CRD's `ensure: present` the Go type cannot omit. |
| `cnpg-objectstore` | Barman Cloud plugin `ObjectStore` (launcher-native): the whole `barmancloud.cnpg.io/v1` `ObjectStoreSpec`, strictly decoded. Policy caps the plugin sidecar's cpu and memory. |
| `helm` | Role-named Helm component, lowered by a component-position rule to the kind-named terminals. **`delivery: flux` (default):** a `helmrelease` under the component's name, referencing its source through `chart.spec.sourceRef` (HelmRepository, or a referenced GitRepository or Bucket, with `chart` as the chart's path in the artifact; `version` is refused there and `reconcileStrategy: Revision` is set) or `chartRef` (OCIRepository, HelmChart). **Generated source:** an inline `source.url` also emits a `helmrepository` (http(s)://) or an `ocirepository` (oci://, `ref.tag` = `version`); with `kind: GitRepository` a `gitrepository` (the URL plus exactly one `source.ref` field), and `kind: Bucket` with `endpoint` and `bucketName` (no URL) a `bucket`. It is named `<application>-source-<digest of the content identity>`, where the identity is `helm:<url>`, `oci:<url>:<version>`, `git:` plus the JSON of URL and ref, or `bucket:` plus the JSON of the keys that locate the bucket. Components of one application with the same identity share one source; other applications never do. The source keeps its terminal's default interval and is applied with the application bundle, ahead of every ordered group, so no consumer can be ordered ahead of it. An inline HelmRepository or OCIRepository URL carrying a user or password is refused under either delivery, since it would be copied into the output in plain text; credentials go in an authored source's `secretRef`, referenced by name. **Reference form:** `source.name` with `kind` and an optional `namespace` references an existing source and emits nothing else. **`delivery: template`:** a `helmtemplate` with the URL inline and any authored `releaseName`; a source reference, an inline GitRepository or Bucket, `valuesMode: configMap`, an OCI source without `version`, and every HelmRelease-only key (`interval`, `targetNamespace`, `driftDetection`, `install`, `upgrade`, `valuesFrom`) are refused. **Decoding:** properties are decoded strictly, and `valuesMode` is never forwarded. **`valuesMode: configMap`** (go-kure/launcher#702) moves non-empty `values` into a `configmap` trait appended to the `helmrelease`, named `<component>-values-<hash of its content>`, and references it ahead of the authored `valuesFrom` entries. It replaces the `helmchart` composite, removed by go-kure/launcher#350 (the name now belongs to the kind-named Flux `HelmChart` source, go-kure/launcher#351). See the component handler README for the full rules. |
| `helmrelease` | Kind-named Flux `HelmRelease`: its properties are exactly the top-level keys of `HelmReleaseSpec`, decoded strictly so an unknown or wrongly typed key at any depth is a build error. Creates no source: `chart.spec.sourceRef` or `chartRef` (exactly one of `chart` and `chartRef`) names an existing one. `interval` defaults to `60m`. Lands in the Flux namespace when one is set, and then defaults `spec.targetNamespace` to the application namespace. `spec.releaseName` is always written: the authored value, else the component name, shortened as helm-controller shortens a name over 53 characters, so Flux never derives `<targetNamespace>-<name>` (go-kure/launcher#785). Emits only the HelmRelease: `valuesMode` is refused with a pointer to `helm`'s `valuesMode: configMap`, or a `configmap` trait plus a `valuesFrom` entry (go-kure/launcher#702). See the component handler README for the full rules. |
| `helmtemplate` | Kind-named client-side Helm render: fetches a chart at build time from `source.url` — an `http(s)://` Helm repository with `chart`, or an `oci://` chart reference with `version` (required there); `source.kind` is inferred from the scheme and checked against it, and a URL carrying a user or password is refused — renders it with `values` into the application namespace (`.Release.Namespace`) under `releaseName` (`.Release.Name`; must be a valid Helm release name, a DNS-1123 subdomain of at most 53 characters), and emits the rendered manifests in Helm hook order, registered kinds as their Go types. An unset `releaseName` defaults to the name the `helmrelease` terminal writes to `spec.releaseName` — the component name, shortened as helm-controller shortens a name over 53 characters (its first 40 characters, `-`, 12 hex digits of its SHA-256) — so `helm` releases a chart under the same name under either delivery (go-kure/launcher#785); a default Helm would refuse is a build error naming `releaseName` as the remedy. Two renders given the same `releaseName` in one namespace generate colliding objects when the chart names its objects after the release, as most do, and the build refuses that collision. A namespaced rendered object without `metadata.namespace` is given the application namespace; a namespace the chart wrote is kept, and an object whose scope is not known (a custom resource whose definition the chart does not render) is left as rendered (go-kure/launcher#794). The environment policy is applied to the render: the chart source host is checked against the registry allowlist before any fetch, and every rendered workload is held to the image and pod-security checks an authored one is (go-kure/launcher#791). No `HelmRelease`, no source CR. It is what `helm` lowers to under `delivery: template`, authorable directly. Properties are decoded strictly: any other key, including `targetNamespace`, every property only a Flux-reconciled release reads (`interval`, `driftDetection`, `install`, `upgrade`, `valuesFrom`, `valuesMode`) and `source.name`, is refused. See the component handler README. |
| `daemonset` | DaemonSet for node-level agents. `ports` declares the main container's ports; it emits no Service (go-kure/launcher#690), so a `service` component authored beside it exposes the pods and carries any ingress, httproute or expose trait. |
| `statefulset` | StatefulSet for ordered, persistent workloads. `serviceName` (no default) names its governing Service, a headless `service` component authored beside it; the statefulset emits no Service (go-kure/launcher#690). |
| `passthrough` | Generic escape hatch (launcher-native, not ported from the downstream runtime): emits an arbitrary Kubernetes object — CRD or non-standard type — declared inline under `object:`. An object of a built-in cluster-scoped kind gets no namespace. For any other kind an authored `clusterScoped` decides, `true` or `false`; unset, a kind the scope table registers as cluster-scoped gets no namespace and an unknown kind is treated as namespaced. A value that contradicts a built-in kind's scope is refused. `object.metadata.name` defaults to the component name. For namespaced objects `object.metadata.namespace` defaults to the build namespace when unset, but an inline value is respected (intentional cross-namespace). No standard trait/port integration. The emitted object is held to the environment policy an authored workload is held to, on the kinds that check reads (workloads, claims, autoscalers and PersistentVolumes): a workload authored this way is refused where a `deployment` would be. A core Secret is refused under a policy that forbids explicit secrets (go-kure/launcher#786). An object of any other kind passes, a custom resource included, and an object of a checked kind that cannot be read is refused (go-kure/launcher#794). |
| `crd` | Emits `CustomResourceDefinition` manifests from a multi-doc YAML source — `inline:` (offline) or `url:` (http/https only; `oci://` not yet supported). Rejects any non-CRD document, and rejects a CRD document that authors `metadata.namespace` (a `CustomResourceDefinition` is always cluster-scoped; the Kubernetes API forbids a namespace on a cluster-scoped object). Emitted CRDs are auto-staged early by stack-compile's CRD inference. URL hosts are constrained by the policy registry allowlist (`AllowedRegistries`), re-checked on every redirect. |
| `manifests` | Emits arbitrary Kubernetes manifests from the same sources as `crd` (`inline` / `url`). Each object's scope is resolved (built-in kinds plus any CRD in the same source); namespaced objects that omit `metadata.namespace` are stamped with the build namespace, cluster-scoped objects are left untouched unless they author `metadata.namespace` themselves (rejected, for the same reason `crd` rejects it), and an unknown-scope object with no namespace fails closed. Optional `scopeOverrides: [{apiVersion, kind, scope: Cluster\|Namespaced}]` supplies an explicit scope for a kind, taking precedence over kure's own non-API-governed guess — e.g. a cluster-scoped custom resource whose CRD is installed out of band. It must agree with a CRD bundled in the same source rather than override it: a conflicting same-source CRD is rejected, naming both values, so a stale bundled CRD is a build failure to fix, not something an override can silently paper over. Overrides are also ignored for a kind whose scope the Kubernetes API itself governs (a manifest cannot redefine that), and the fail-closed default is kept for a kind with no override and no other scope source. Same URL allowlist as `crd`. The objects a source yields are held to the environment policy an authored workload is held to, as `passthrough` objects are: those of an `inline` source when the policy is applied, those of a `url` source when they are generated (go-kure/launcher#794). |
| `oci` | Reconciles an OCI artifact: emits an `OCIRepository` source CR plus a per-component Flux `Kustomization`. Properties: `source.url` (required, `oci://`), `version` (required; a tag, or `sha256:<digest>`), `path` (default `./`), `prune` (default `true`), `interval` (default `60m`), `targetNamespace` (optional), `wait` (optional boolean, no default; `true` sets the Kustomization's `spec.wait`), `healthChecks` (optional list of `{apiVersion, kind, name, namespace}` copied in order into `spec.healthChecks`; `namespace` may be omitted for a cluster-scoped kind). `wait: true` with a non-empty `healthChecks` is rejected, since Flux ignores `healthChecks` when `wait` is true; with `wait` absent or `false` and `healthChecks` absent or empty, the Kustomization is unchanged. The OCIRepository participates in source dedup keyed on URL+version, among `oci` components only (the component that comes first in the declared order emits it; a `helm` component's generated OCI source on the same artifact copies the chart layer and is separate); the Kustomization is always emitted, one per component. Both land in the Flux namespace. OCI registry host is constrained by the policy registry allowlist (`AllowedRegistries`): under a non-empty allowlist `source.url` must name its registry explicitly (`oci://<registry>/<repository>`, registry `localhost` or containing `.` or `:`), since without an explicit registry Flux resolves an otherwise valid repository reference against Docker Hub, and that registry must match an entry exactly. |
| `helmrepository` | Kind-named Flux `HelmRepository`: its properties are exactly the top-level keys of `HelmRepositorySpec`, decoded strictly so an unknown or wrongly typed key at any depth is a build error. `url` is required and must start with `http://`, `https://` or `oci://` (only `oci://` under `type: oci`). `interval` defaults to `60m`, except under `type: oci`, which Flux does not poll. The `url` host is constrained by the policy registry allowlist (`AllowedRegistries`); under a non-empty allowlist an `oci://` URL must also name its registry explicitly (`localhost`, or a host containing `.` or `:`), since Flux resolves any other first segment against Docker Hub. Emits only the HelmRepository, named after the component, with no source dedup; it lands in the Flux namespace when one is set, where its `secretRef` then resolves. See the component handler README, which also covers the source the `helm` rule generates. |
| `ocirepository` | Kind-named Flux `OCIRepository`: the top-level keys of `OCIRepositorySpec`, decoded strictly. `url` is required and must start with `oci://`; `interval` defaults to `60m`. Registry host constrained by `AllowedRegistries`; under a non-empty allowlist the `url` must be `oci://<registry>/<repository>` with an explicit registry (`localhost`, or a host containing `.` or `:`), since Flux reads `oci://ghcr.io` or `oci://registry/app` as a Docker Hub repository. Emits only the OCIRepository — no Kustomization, unlike `oci` — with no source dedup, in the Flux namespace when one is set. |
| `gitrepository` | Kind-named Flux `GitRepository`: the top-level keys of `GitRepositorySpec`, decoded strictly. `url` is required and must start with `http://`, `https://` or `ssh://` (an scp-style `git@host:path` is refused). `interval` defaults to `60m`. The `url` host, with the user of an `ssh://` URL dropped, is constrained by `AllowedRegistries`. Emits only the GitRepository, in the Flux namespace when one is set. |
| `bucket` | Kind-named Flux `Bucket`: the top-level keys of `BucketSpec`, decoded strictly. `bucketName` and `endpoint` are required; `interval` defaults to `60m`. The `endpoint` host is constrained by `AllowedRegistries` (its `sts.endpoint` is not); under `provider: gcp`, whose client ignores `endpoint`, the host constrained is `storage.googleapis.com`; and a non-empty `AllowedRegistries` refuses an Amazon S3 `endpoint` (any containing `amazonaws`) under the `generic`, `aws` or unset provider, whose S3 client picks the host it contacts from `region` at runtime. Emits only the Bucket, in the Flux namespace when one is set. |
| `helmchart` | Kind-named Flux `HelmChart`: the top-level keys of `HelmChartSpec`, decoded strictly. `chart`, `sourceRef.kind` and `sourceRef.name` are required; `interval` defaults to `60m`; enum and CEL constraints are left to the CRD, and `sourceRef` is not resolved at build time. No policy check applies: a HelmChart fetches through the source it references. Emits only the HelmChart, in the Flux namespace when one is set. A top-level key of the removed `helmchart` composite (e.g. `source`, `valuesMode`) is refused with a hint pointing to `helm`. |

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
