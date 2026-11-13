---
title: "Kinds: core workloads"
weight: 3
---

Each kind below with a heading has the sections it needs from this list: Properties, Outputs,
Validation and Example. Its implementation, history and tests are on the
[contributor page](../../contributing/oam-reference/kinds-core-workloads.md).

## webservice, worker

### Properties

**webservice / worker** — `image`, `replicas` (default 1), `port` (webservice),
plus the full `DeploymentSpec`-level surface they share with `deployment` —
`strategy`, `minReadySeconds`, `revisionHistoryLimit`, `paused` and
`progressDeadlineSeconds` (see ["Deployment-level
properties"](common-config.md#deployment-level-properties-deployment-webservice-worker)). What still separates them from `deployment` is launcher's
own opinions, which these two keep: `topologySpread` and the four-key
`affinity` shorthand, and on `webservice` the `port` that drives the Service.
Note the shorthand is what they keep — neither publishes the raw `corev1`
`affinity` that `deployment` does. Both publish `tolerations` and
`topologySpreadConstraints` as `deployment` does (see
["Raw scheduling properties"](common-config.md#raw-scheduling-properties)); authored constraints need
`topologySpread: false`.

### Outputs

The `webservice` rule implements the optional `oam.EndpointProvider`: it declares its own
pods (`app: <component-name>`) on the declared `port` (its single `port` property drives both
the container port and the Service port), letting a downstream platform synthesize generic
app→app connections targeting a webservice. `worker` declares no in-cluster port and emits no
Service, so it deliberately advertises no endpoint (not an `EndpointProvider`).

**`worker` is a component lowering rule, not a handler** (`WorkerRule`). It runs worker's own parse, then re-expresses the
component as a `deployment` of the same name: the authored properties are
forwarded, the four-key `affinity` shorthand is evaluated exactly as before
and forwarded as the raw `corev1` `affinity` (omitted when it evaluates to
nothing), and `topologySpread` becomes a synthesized `topology-spread`
trait placed before the authored traits (none when `topologySpread: false`).
Authored traits and annotations are forwarded unchanged. Worker's published
schema, its generated output and the `app: <component-name>` selector are
unchanged — every example and golden fixture builds byte-identically — and
the emitted component carries `Origin.Rule` `component/worker@v1alpha1`. Two
differences are deliberate. A worker refused by its own parse now reads
`component "w" (type "worker") in document …: <cause>` (the lowering
engine's prefix) where it read `component "w": <cause>`; the cause is
unchanged. A trait-lowering error on a worker's trait gains one chain line
naming the `component/worker@v1alpha1` step; a trait handler's error reads as before.
The former handler's affinity label-syntax check (see [Common config](common-config.md#common-config)) runs
in the rule, before the raw `affinity` is forwarded, with the same
`affinity: the shorthand evaluates to an affinity the API server would
refuse: …` text. A key worker's parse refuses with a reason is refused
there: an upstream field of its refusal maps, or `podActiveDeadlineSeconds`,
which only Job pods may set. Any other key worker does not declare is dropped
rather than forwarded to `deployment`; `kurel build` refuses both before
lowering anyway.

**Both rules emit the component's ServiceAccount as a `serviceaccount`
member** unless `serviceAccountName` is authored. A
`worker` therefore lowers to a same-name sibling group of a `deployment` and
a `serviceaccount`; a `webservice` to a `deployment`, a `service` and a
`serviceaccount`. The member carries `automountServiceAccountToken: false`,
the component's annotations, and the authored `prune-protection` and
`force-replace` traits. The `deployment` member is handed the account's
object name as `serviceAccountName` (the component name, unless
`serviceAccountObjectName` or the `Naming` hook names the account, below),
so it runs as that account and generates none of its own. The output is unchanged: the same ServiceAccount,
in the same place, so every golden builds byte-identically. One consequence
for a library caller: a registry that lowers `webservice` or `worker` must
register a `serviceaccount` component handler too, or the build fails with
`member type "serviceaccount" has no component handler`.

**Both rules turn each claim a `pvc` volume describes into a synthesized
`pvc` trait** on the `deployment` member, named
`<component>-<volume>` as before unless the volume's `claimObjectName` or
the `Naming` hook names it (below), and rewrite the volume to reference it
by `claimName`, keeping `accessModes` so the non-RWX constraints still see it.
The claims are generated as before, after the component's own objects. A
registry that lowers `webservice` or `worker` must register the `pvc` trait
handler too.

**`webservice` is a component lowering rule, not a handler**
(`WebserviceRule`). It runs webservice's own parse,
then re-expresses the component as a same-name sibling group (see `pkg/oam`
"Same-name sibling groups"): a `deployment` and then a `service`, both named
after the component, deployed as one component — one tier, one
bundle. The `deployment` member gets the authored properties
webservice declares except `port`, `topologySpread` and `affinity`, plus
the main container's one port `{name: http, containerPort: <port>}`; the
`affinity` shorthand and `topologySpread` are handled as on `worker`. The
`service` member gets one port `{name: http, port: <port>}` and the default
selector `app: <component-name>`. Annotations go to both members. Each
authored trait is forwarded unchanged, in authored order, to the member it
acts on: `expose`, `ingress` and `httproute` go to the `service`;
`prune-protection` and `force-replace` go to both; every other trait goes
to the `deployment`. That includes `fluxcd-patches` and `fluxcd-postbuild`
(not built in; a consumer that delivers through Flux registers them) and any
trait type an extension registered, since the `deployment` member holds the
pods. Webservice's published schema and its generated objects are
unchanged, in the handler's order (Deployment, Service, ServiceAccount,
claims). Each emitted component carries `Origin.Rule`
`component/webservice@v1alpha1`. Four differences are deliberate. A refusal from
webservice's own parse gains the lowering engine's prefix, as on `worker`;
the cause is unchanged. The synthesized inbound NetworkPolicy opens an
ingress `portName: http` as the port's number, where the handler opened the
name `http`, which the container port also carried; the pods admitted are
the same. Likewise, an ingress path on another component whose `backend`
names a webservice's Service by `portName` opens that port's number on the
webservice's pods, translated as on a `service` component. A route on
another component whose `backend` names a webservice's Service on a port
that Service does not expose (`port: 9090` on a webservice whose `port` is
8080) no longer opens that port on the webservice's pods: the `service`
member opens only its own declared ports, as a `service` component does.
The route was already broken, since the Service has no such port to
forward. Webservice is no longer a handler a library caller converts with
directly, so the nameless-config fallback the handlers keep does not apply
to it: the engine only lowers a named component.

**A `webservice` component name must be a valid Service name.** It always
emits a Service named after the component, and the API server validates
a Service's `metadata.name` as a DNS-1123 label: at most 63 characters,
lowercase letters, digits and `-`, starting and ending with a letter or
digit. That is stricter than the DNS-1123 subdomain every component name
already passes, so `api.v1` and a 64-character name are refused at
conversion (`name: "api.v1" is not a valid Service name, which must be a
DNS-1123 label`) instead of building a manifest the cluster rejects on
apply. A leading digit (`1api`) is accepted: that
is the rule under RelaxedServiceNameValidation, on by default from Kubernetes 1.36, and a
build whose `spec.kubernetesVersion` is below 1.36 refuses such a Service at generation,
unless its `spec.featureGates` sets the gate on (`docs/delivery-scope.md` §3.5). `Generate` applies the
same rule to the Application name it is handed. `worker` emits no Service
and keeps accepting such a name, within the container-name rule above.
The rule holds for the component name whatever the Service is named:
`serviceObjectName` (below) does not lift it.

**`deploymentObjectName`, `serviceObjectName` and
`serviceAccountObjectName` name the objects the component generates**, each in place of the component name and each on its
own: the Deployment, the Service (`webservice` only; `worker` generates
none and does not declare the property) and the ServiceAccount. A name is
used as written, never shortened. The Deployment's and the ServiceAccount's
are DNS-1123 subdomains, the Service's a DNS-1123 label; anything else is
refused (`naming the Service: serviceObjectName "web.v1" cannot be the name
for role "workload-service": not a valid DNS-1123 label: …`). Without the
property the `Naming` hook is asked (roles `workload-deployment`,
`workload-service`, `workload-serviceaccount`), and without an answer the
object keeps the component name, so a document that sets none builds
byte-identically. Each name is held against every other resolved name of
its kind and namespace: two components given one `serviceObjectName`, or a
`service` component whose object carries a `webservice`'s Service name, are
refused as a name collision with both named.
- *They name the objects alone.* The members keep the component's name, so
  the `app` label, the pod template's labels, the Deployment's and the
  Service's selectors, the main container, the claims' default names
  (`<component>-<volume>`) and every name a trait derives (`<component>-hpa`,
  `<component>-httproute`) stay as they were.
- *A `pvc` volume's `claimObjectName` names the claim that volume
  generates* (role `workload-volume-claim`), in place of
  `<component>-<volume>`, used as written: a DNS-1123 subdomain, never
  shortened. The volume mounts the claim by that name. Without it the
  `Naming` hook is asked for each such volume. A volume with `claimName`
  references an existing claim and generates none, so `claimObjectName`
  beside it is refused and the hook is not asked for it. Two volumes, or
  a volume and another component, that give one claim name are refused as
  a name collision.
- *What launcher writes to a renamed object follows it.* The `scaler`
  trait's `scaleTargetRef` names the Deployment, whatever the Service and
  the ServiceAccount are named. A routing trait's own backend (`expose`,
  `ingress`, `httproute`) is the Service, and a route naming that Service
  still resolves to the component for the synthesized NetworkPolicy. The
  pods' `serviceAccountName` and the `rbac` trait's subject name the
  ServiceAccount.
- **A renamed Service changes its DNS name: the Service's name is the name
  it is reached at in the cluster. Launcher builds no such address, so every
  address written with the component name (an `env` value, a URL in another
  component's properties, a backend another component's route names) is the
  author's to change.**
- *`serviceAccountObjectName` names the account the component generates,
  `serviceAccountName` an account that exists.* With `serviceAccountName`
  the component generates none, so the two together are refused
  (`serviceAccountObjectName and serviceAccountName are both set: …`), and
  the hook is not asked for an account.

### Validation

**An ingress `portName` on a `webservice` must be `http`.** The Service's
one port is named `http`, so an `ingress` path whose implicit backend is
this component's Service (no `backend`, or `backend` naming that Service)
and that addresses it by any other `portName` is refused at build time
(`cannot route implicit backend to port "name"`), just as a numbered
`port` other than the component's `port` is.

### Example

- `worker`: The component in `pkg/cmd/kurel/testdata/worker-minimal/app.yaml`, built against
  the `cluster.yaml` beside it, gives the `expected.yaml` beside it. The fixture test compares
  the two.
- `webservice`: No example yet.

## deployment

### Properties

**deployment** — the kind-named Deployment (see ["Deployment-level
properties"](common-config.md#deployment-level-properties-deployment-webservice-worker)): the shared container-level, pod-level and
`DeploymentSpec`-level surface, the last of which it now shares with
`webservice` and `worker` rather than owning. What distinguishes it is mostly
what it leaves out: no `topologySpread`, no four-key `affinity` shorthand, no
`port`. It is not `worker` minus
a few things either — it reads nulls as omissions across its whole surface,
which the role kinds do not. The one thing it
adds is the raw `corev1` scheduling surface the role kinds lack: `affinity`,
`tolerations` and `topologySpreadConstraints` as the API shapes
(see ["Raw scheduling properties"](common-config.md#raw-scheduling-properties)). That is the
same distinction in the other direction — the role kinds carry the opinion,
this kind carries the API.

### Outputs

It emits no Service, and declares one endpoint per main-container
`ports` entry (see ["Main container ports"](common-config.md#main-container-ports)).

### Validation

None stated in this section.

### Example

The component in `pkg/cmd/kurel/testdata/deployment-ports/app.yaml`, built against the
`cluster.yaml` beside it, gives the `expected.yaml` beside it. The fixture test compares the
two.

## service

### Properties

**service** — the kind-named Service, an independent
component rather than half of a workload: it emits the Service and nothing
else, and the workload component whose pods it selects owns their
ServiceAccount. `selector` defaults to `app: <component name>`, the label
every workload kind puts on its pods; set it when the Service fronts a
workload named differently (a `deployment` named `api-server` behind a
`service` named `api`). `ports` is the full `corev1.ServicePort` list, at
least one entry unless the Service is headless or of type `ExternalName`: `port` (required), `targetPort` (a number or a container
port name, published as the `Types: [integer, string]` union, so a value of any other type is rejected by property
validation before the parser sees it; defaults to `port`), `protocol` (`TCP`, `UDP` or `SCTP`; defaults
to `TCP`), `name` (required once there is more than one port; names and
port/protocol pairs must be unique), `nodePort` (1-65535, only with `type`
`NodePort` or `LoadBalancer`, unique per protocol, as the API requires; two
refusals are launcher's own: `0`, which the API reads as "allocate one" —
leave the key out instead — and a node port on an `ExternalName` Service,
which the API accepts; the
cluster's own node port range is narrower and is checked on apply) and
`appProtocol` (a qualified name such as `http` or `kubernetes.io/h2c`).
`type` is `ClusterIP` (default), `NodePort`, `LoadBalancer` or
`ExternalName`. An empty `selector` is refused.

**ExternalName.** `type: ExternalName` emits a DNS
alias for `externalName`, which is then required: a DNS-1123 subdomain,
with one trailing dot allowed. The Service selects no pods, so `selector`
is refused with it (launcher's own rule; the API accepts and ignores one),
and so are `clusterIP: None`, `ipFamilies` and `ipFamilyPolicy`; `ports`
is optional. On any other type `externalName` is refused (launcher's own
rule; the API ignores it there). Whatever ports it lists, an ExternalName
Service has no first port for a routing trait and no NetworkPolicy
endpoint: an `ingress` or `httproute` trait refuses it as an implicit
backend (`component "db" has no service port`), a trait-level
`servicePort` is refused, and it declares no endpoint. As on a port-less
headless Service, the refusal is of the component as a backend, not of
the trait: a trait on it that names another Service explicitly is carried
as on any component, and that route's policy is the routed Service's. It
still owns its name
(`BackendServiceName`): a route from another component naming it is a
route to a Service of this application, so that route's `backendSelector`
is not trusted, and **no inbound NetworkPolicy is synthesized for that
route**, in a same-name sibling group either: the Service leads to no pods
of the cluster, and the group's component label is on its workload's
pods, which the Service does not lead to. Named apart from its component
(`objectName`), it owns that name instead: a route naming the Service's
name gets no policy, and one naming the component's name is a route to a
Service from elsewhere.

**Further `ServiceSpec` fields**. Each is written
only when authored; unauthored, the API server's own default applies. The
rules are the API server's (`validateService`), except where marked as
launcher's own.

| Property | Rules |
|----------|-------|
| `externalTrafficPolicy` | `Cluster` or `Local`. Only with `type` `NodePort` or `LoadBalancer`. |
| `internalTrafficPolicy` | `Cluster` or `Local`. |
| `trafficDistribution` | `PreferSameZone`, `PreferSameNode`, or `PreferClose` (the deprecated name of `PreferSameZone`). `PreferSameZone` and `PreferSameNode` need the `PreferSameTrafficDistribution` feature gate, on by default from Kubernetes 1.34: a build for a `ClusterProfile` whose `spec.kubernetesVersion` is below 1.34 refuses them. |
| `sessionAffinity` | `None` or `ClientIP`. |
| `sessionAffinityConfig` | Only with `sessionAffinity: ClientIP`. `clientIP.timeoutSeconds` is required once the key is authored (launcher's own: the API server would default it), 1-86400. |
| `publishNotReadyAddresses` | A boolean; written only when `true`. |
| `ipFamilies` | `IPv4` and/or `IPv6`, each once, at most two. |
| `ipFamilyPolicy` | `SingleStack`, `PreferDualStack` or `RequireDualStack`; `SingleStack` is refused with two `ipFamilies` (the API server's allocator refuses it). |
| `loadBalancerClass` | A qualified name. Only with `type: LoadBalancer`. |
| `loadBalancerSourceRanges` | CIDRs in canonical form (launcher's own: the API tolerates surrounding spaces and some non-canonical forms). Only with `type: LoadBalancer`. |
| `loadBalancerIP` | Only with `type: LoadBalancer` (launcher's own: the API ignores it elsewhere). The value is not validated, as in the API. |
| `allocateLoadBalancerNodePorts` | A boolean. Only with `type: LoadBalancer`. |
| `healthCheckNodePort` | 1-65535. Only with `type: LoadBalancer` and `externalTrafficPolicy: Local`. `0` is refused (launcher's own: the API reads it as "allocate one"; leave the key out instead). |

`externalIPs` and `clusterIPs` are refused, and so is a literal
`clusterIP` address. `externalIPs` routes traffic for addresses the
cluster does not manage and is deprecated by the API; a Service's cluster
IPs are the cluster's to allocate. In a document all three are refused by
property validation before the parser runs: `externalIPs` and `clusterIPs`
as unsupported fields, each with the kind's reason after the list of
allowed keys, and a literal `clusterIP` address as
a value outside the property's enum, which has `None` alone.

**Headless.** `clusterIP: None` emits
`spec.clusterIP: None`: no virtual IP, and cluster DNS resolves the name to
the selected pods, as a StatefulSet's governing Service needs. It requires
`type: ClusterIP` (the default), since the API server refuses a headless
`NodePort` or `LoadBalancer` Service. `None` is the only value: a literal
address is refused, and so is an empty string, rather than read as
absence. A headless Service may have no `ports` at all; without
`clusterIP`, at least one port is still required (`ports: at least one
port is required`), except on an `ExternalName` Service (above). A
port-less Service has no first port, so routing
traits refuse it as an implicit backend, refuse a trait-level
`servicePort` on it, and it declares no NetworkPolicy endpoint. It still
owns its name (`BackendServiceName`): a route from another component
naming it is a route to a Service of this application, not an external
backend, so that route's `backendSelector` is not trusted and, with no
TCP port to translate, no allow is synthesized. Without `clusterIP` the
Service carries no `clusterIP`, as before.

### Outputs

**Synthesized NetworkPolicy.** The `{component}-allow-ingress-traffic`
policy for traffic routed to a `service` selects its `selector` pods — not
the component label, which no pod carries — and opens the `targetPort` of
each routed TCP port. A route that reaches only a UDP or SCTP port
synthesizes no policy, and neither does a route to an `ExternalName`
Service, which has no pods to open a port on (above). In a same-name
sibling group whose `selector`
picks a sibling's own pods, the policy selects the component label
instead, but only when `IdentityTargetPorts` is true: the Service has at
least one port and every port, whatever its protocol, has a numeric
`targetPort` equal to its own `port`. Otherwise it keeps the `selector`.
Either way the routed ports are translated to their `targetPort` and
only TCP ones are opened (see `pkg/oam` "Same-name sibling groups").

It implements `oam.EndpointProvider`: one endpoint, the `selector` pods on
every TCP `targetPort` (deduplicated). A Service with no TCP port declares
none, and neither does an `ExternalName` Service.

### Validation

**The component name must be a valid Service name.** It becomes the
Service's `metadata.name`, which the API server validates as a DNS-1123
label: at most 63 characters, lowercase letters, digits and `-`, starting
and ending with a letter or digit. That is stricter than the DNS-1123
subdomain every component name already passes, so `api.v1` and a
64-character name are refused here (`name: "api.v1" is not a valid Service
name, which must be a DNS-1123 label`) instead of building a manifest the
cluster rejects on apply. `Generate` applies the same rule to the
Application name it is handed. A leading digit (`1api`) is accepted: that
is the rule under RelaxedServiceNameValidation, on by default from Kubernetes 1.36, and a
build whose `spec.kubernetesVersion` is below 1.36 refuses such a Service at generation,
unless its `spec.featureGates` sets the gate on (`docs/delivery-scope.md` §3.5).

**Routing traits use the first port.** `ingress`, `httproute` and `expose`
(which lowers to one of the two) on a `service` component resolve their
implicit backend to `ports[0].port` and refuse any other port on it,
whether it is named by number (`cannot route implicit backend to port N`)
or by an ingress path's `portName` (`cannot route implicit backend to port
"name"`); a `portName` must be the first port's own name, so an unknown
name is refused too. Naming the component's own Service as the backend
(`backend: api` on an ingress path, `backendRefs: [{name: api, port:
9000}]` on an httproute) is still the implicit backend and is refused the
same way. To reach a later port, put the routing trait on another
component and name this Service there as an explicit backend, with that
port.

**No policy check.** `service` has no `ApplyPolicy`, deliberately: none of its properties maps to an
environment-policy method, so there is nothing to enforce. The policy
makes no statement about a Service's `type`, so `NodePort` and
`LoadBalancer` build under every policy. **`type: ExternalName` has no
capability gate either: whoever may author a `service` component may give
an in-cluster name to any DNS name outside the cluster**, and workloads
that resolve the Service name are sent there. `nodePort`,
`loadBalancerSourceRanges`, `loadBalancerIP` and `externalTrafficPolicy`
are accepted the same way. The object kind policy
(`oam.ObjectKindPolicy`) holds kinds, not these
fields: it can keep Services out of a build, not one `type`.

### Example

No example yet.

## serviceaccount, persistentvolumeclaim, configmap

**serviceaccount**, **persistentvolumeclaim**, **configmap** are kind-named projections of one object each.

### Properties

`serviceaccount` publishes `automountServiceAccountToken` and
`imagePullSecrets` (`[{name}]`). An unauthored
`automountServiceAccountToken` stays unset, which Kubernetes reads as
true. The account a role kind generates sets it to false, so a
`serviceaccount` replacing that account authors `false` to keep the same
posture. A workload runs as it through `serviceAccountName`, which also
stops a role kind from generating its own account.

`persistentvolumeclaim` publishes `size`, `storageClassName`,
`accessModes` (default `[ReadWriteOnce]`) and `volumeMode`. The claim is
built by the same `BuildPVC` as a `pvc` volume, so the two agree on every
field they share, including the explicit-empty `storageClassName: ""`,
which requests no class. The `pvc` trait is this kind's twin: it runs the kind's own `ParseClaimProperties`,
`ApplyClaimPolicy` and `GenerateClaim`, so the same properties build the
same claim. Only the ownership fields differ: the claim's name, its `app`
label, namespace and bundle. An unauthored `storageClassName` (absent or
`null`) comes from the ClusterProfile `pvc` capability's
`storageClassName`, as the trait's does; with no
binding it stays unset, which is the cluster's default class. **Pre-GA
output change**: the kind used to ignore that binding, so an existing
claim built from it got the cluster's default class when it was created,
if the cluster had one. A claim's assigned `storageClassName` cannot
change: if the binding's class differs from it, the claim fails to apply
until it is recreated or the assigned class is authored. A claim created
with no class may take one later, so it applies. An unauthored `size`
comes from the EnvironmentPolicy storage default; with neither, the build
fails with `size: required …`. The policy's maximum storage size applies
either way. A workload mounts the claim through a `pvc` volume's
`claimName` (see ["Referencing an existing claim"](common-config.md#referencing-an-existing-claim-pvcclaimname)). A claim name
another component also generates (for example a `pvc` trait of the same
name) is refused as a generated-object collision.

Four more claim-spec fields are read, by the kind
and by the `pvc` trait alike (`claim_spec.go`). Each is written only when
authored, so a document that authors none builds the claim it built
before. A workload's `pvc` volume does not take them.

| Property | Rule |
|----------|------|
| `selector{matchLabels, matchExpressions[]}` | A label query over the PersistentVolumes that may back the claim, parsed as a `statefulset` claim template's is. A claim with a selector is never dynamically provisioned. An empty selector is refused: this is launcher's own rule, the API server reads it as "every volume". |
| `dataSourceRef{apiGroup, kind, name, namespace}` | The object to populate the volume from, with the rules of upstream `validateDataSourceRef` (the `dataSourceRef` row under ["StatefulSet-level and claim-template properties"](common-config.md#statefulset-level-and-claim-template-properties)). |
| `volumeName` | The name of the PersistentVolume to bind to. Must be a DNS-1123 subdomain: launcher's own rule, since `ValidatePersistentVolumeClaimSpec` does not read the field and a value that cannot name a PersistentVolume would leave the claim Pending for good. |
| `volumeAttributesClassName` | A DNS-1123 subdomain (`ValidateClassName`). |

`configmap` publishes `data` (string values only: a number or a boolean
is refused rather than stringified), `binaryData` (base64; a key may not
also appear in `data`) and `immutable`. The `data` values and the decoded
`binaryData` values together may hold at most 1,048,576 bytes, the limit
the API server enforces; a larger ConfigMap is refused at build time. That
check is the exported `CheckConfigMapSize(data, binaryData)`. A key the API
server refuses in a ConfigMap is refused by the exported
`ValidateConfigMapKey(field, key)`. Keys are checked in sorted order, so
with several bad entries the one reported is the same on every build. It is
a different type from the `configmap` trait, which attaches a ConfigMap to
another component. The trait is this kind's twin: it
reads `data`, `binaryData` and `immutable` through the exported
`ParseConfigMapProperties(props)` and builds the ConfigMap through
`GenerateConfigMap(config, name, namespace, labels)`, so the same properties
give the same ConfigMap and the same refusals on both paths.

### Outputs

Each
emits that object, named after the component, and nothing else, so another
component refers to it by the component name. They are the authorable forms
of the objects the workload kinds generate for themselves today, and they
carry no launcher opinions. Like every component, they are in no tier
unless a tier annotation or placement policy places them.

### Validation

`serviceaccount`
and `configmap` have no `ApplyPolicy`, deliberately: none of their properties maps to an environment-policy method
(`imagePullSecrets` names Secrets, not a registry), so there is nothing to
enforce. `persistentvolumeclaim` has one, for the storage default and
maximum.

**No policy check on `volumeName`.** It is accepted with no capability
gate and no EnvironmentPolicy method; the object kind policy
(`oam.ObjectKindPolicy`) holds kinds, not this
field. A claim that names a volume binds to
that PersistentVolume and to no other, with no dynamic provisioning, so
**whoever may author a claim may ask for any PersistentVolume of the
cluster by name**. The cluster still decides: the volume must be unbound
or reserved for this claim, and must satisfy the claim's size, access
modes, class, attributes class and volume mode (the PersistentVolume
controller's `checkVolumeSatisfyClaim`). The storage default and maximum
still apply to the claim's `size`.

`dataSource` is refused: it is the superseded spelling of `dataSourceRef`,
which the API server mirrors into it. The long `resources` spelling of
`size` is not read on a standalone claim.

### Example

- `serviceaccount`: The component in
  `pkg/cmd/kurel/testdata/serviceaccount-component/app.yaml`, built against the `cluster.yaml`
  beside it, gives the `expected.yaml` beside it. The fixture test compares the two.
- `persistentvolumeclaim`: The component in
  `pkg/cmd/kurel/testdata/persistentvolumeclaim-component/app.yaml`, built against the
  `cluster.yaml` beside it, gives the `expected.yaml` beside it. The fixture test compares the
  two.
- `configmap`: The component in `pkg/cmd/kurel/testdata/configmap-component/app.yaml`, built
  against the `cluster.yaml` beside it, gives the `expected.yaml` beside it. The fixture test
  compares the two.

## secret

**secret** is the kind-named projection of a v1
Secret, on the recipe of `configmap`: it emits the Secret, named after
the component (or its `objectName`) and carrying the component's `app`
label, and nothing else, so a workload's `secret` volume, `envFrom` or
`secretKeyRef` names it by that name.

### Properties

It publishes `stringData` (plain string values: a number, a boolean or a
nested object is refused, not stringified), `data` (base64; a key may not
also appear in `stringData`, where the API server would let `stringData`
win silently), `type` and `immutable`. An unauthored `type` and
`immutable` stay unset (the API server stores `Opaque` for an unset
type), and the keys a type requires are left to the API server.

It is a different type from the `secret` trait, which attaches a Secret
to another component. The trait is this kind's twin: it reads the same
four properties through the exported `ParseSecretProperties(props)` and
builds the Secret through `GenerateSecret(config, name, namespace,
labels)`, and its schema is the kind's plus `name`, so the same
properties give the same Secret and the same refusals on both paths. Only
the ownership fields differ: the Secret's name, its labels, its namespace
and the bundle it is placed in.

### Outputs

Every entry is emitted under `data`, base64-encoded as the API serializes
it, and never under `stringData`: the object written is the object the
API server stores. **base64 is an encoding, not encryption**, so the
build output is as sensitive as the document, and so is every place that
output is committed or pushed to. Prefer a Secret created out of band (an
`external-secret` trait, a sealed or externally managed Secret) for
anything that must not be in the output.

*Names.* The kind's Secret is claimed under role `object`. A `secret`
component and a `secret` trait naming one Secret are refused; the trait
itself claims no name. It names its own Secret under no role, so the two
meet among the generated objects (`generated-object collision: Secret
"shop/shared" is generated by both component "shared" and sub-application
"shared" of component "web"; …`), as a `configmap` component and trait
do. The Secret a `helm` component generates for
`secretValues` is claimed under role `values-secret`, as the same kind,
so a `secret` component under that name is refused as a name collision
with both named.

### Validation

The decoded values together may hold at most 1,048,576 bytes, the limit
the API server enforces (the exported `CheckSecretSize(data)`); a key the
API server refuses is refused by the exported `ValidateSecretKey(field,
key)`. Keys are checked in sorted order. **No refusal carries a value**:
an entry is named by its key, a wrong value by its type, and a base64
error by its key alone.

*Policy.* Unlike `configmap`, the kind has an `ApplyPolicy`: a policy
that forbids explicit secrets (`oam.ExplicitSecretPolicy`, see the
`pkg/oam` README) refuses the component with a violation naming it,
`secret: the environment policy forbids explicit secrets; reference a
Secret created out of band instead`, as it refuses the `secret` trait, a
`helm` component's `secretValues` and a Secret a `passthrough` or
`manifests` component carries. A policy that does not implement that
interface, and no policy, allow it.

### Example

No example yet.

## namespace, limitrange, resourcequota

**namespace**, **limitrange**, **resourcequota** are
kind-named projections of one Kubernetes core object each, built on the
recipe of the `cnpg-pooler`, `cnpg-database` and `cnpg-objectstore` kinds:
one schema key per json field of the object's spec type, the whole property
map decoded strictly into that type under the null contract, and two
spellings of one field refused.

### Properties

The properties are the spec fields, and `labels` and `annotations` for the
object's metadata (["The object's labels and annotations"](conventions.md#the-objects-labels-and-annotations-labels-annotations)).

`namespace` publishes `finalizers`, the one field of
`corev1.NamespaceSpec`. A Namespace is cluster-scoped: the object carries
no namespace, whatever namespace the application is built for. The
component name is the Namespace's name, which the API holds to a DNS-1123
label (at most 63 characters, no dot), narrower than a component name;
any other name is refused, naming it. A Namespace that needs the Pod
Security Admission labels (`pod-security.kubernetes.io/enforce` and its
siblings) writes them under `labels`.

`limitrange` publishes `limits`, the one field of `corev1.LimitRangeSpec`,
and emits the LimitRange in the build namespace. `limits` is required: the
Go type cannot omit it, so an unauthored one (absent or `null`) would be
written as `limits: null` and is refused with `limits: required …`. An
authored `limits: []` is kept: a LimitRange that enforces nothing. Each
limit's `type` is required for the same reason (`limits[1].type: required
…`), and two limits of one type are refused by path, as the API refuses
them. The other value rules (which type names exist, `min` at most `max`,
what a `Pod` limit may not set) are left to the API server. A quantity
written as a number is emitted in its canonical string form (`cpu: 2`
becomes `cpu: "2"`). The limits constrain the pods and claims of the
namespace at admission; the environment policy is not applied to them.

`resourcequota` publishes `hard`, `scopes` and `scopeSelector`, the
fields of `corev1.ResourceQuotaSpec`, and emits the ResourceQuota in the
build namespace. The API requires none of them, so a component with no
properties is a quota that limits nothing. Which resource names, scopes
and selector operators exist is left to the API server. A quantity written
as a number is emitted in its canonical string form
(`persistentvolumeclaims: 10` becomes `"10"`). The quota bounds what the
namespace may hold in total; the environment policy is not applied to it.

### Outputs

Each emits its object, named after
the component,
with the authored spec and nothing else: the handler adds no annotation and
no label of its own, not an `app` label either. The transform then sets the component
label on the object, as on every object a component owns.

Like every
component, they are in no tier unless a tier annotation or placement policy
places them.

### Validation

None
runs a pod or requests storage, so `ApplyPolicy` is a no-op.

### Example

- `namespace`: The component in `pkg/cmd/kurel/testdata/namespace-component/app.yaml`, built
  against the `cluster.yaml` beside it, gives the `expected.yaml` beside it. The fixture test
  compares the two.
- `limitrange`: The component in `pkg/cmd/kurel/testdata/limitrange-component/app.yaml`, built
  against the `cluster.yaml` beside it, gives the `expected.yaml` beside it. The fixture test
  compares the two.
- `resourcequota`: The component in `pkg/cmd/kurel/testdata/resourcequota-component/app.yaml`,
  built against the `cluster.yaml` beside it, gives the `expected.yaml` beside it. The fixture
  test compares the two.

## persistentvolume

### Properties

**persistentvolume** is the kind-named projection of
a v1 PersistentVolume, on the recipe of `namespace`, `limitrange` and
`resourcequota`: one schema key per json field of
`corev1.PersistentVolumeSpec`, the property map decoded strictly into that
type, two spellings of one field refused, and one object named after the
component, with the authored spec; the handler adds no label and no
annotation of its own, and the transform sets the component label. The spec
embeds `corev1.PersistentVolumeSource`, so each volume source (`nfs`, `csi`,
`hostPath`, …) is a top-level property, as it is a top-level field of the
object's spec.

### Outputs

A PersistentVolume is cluster-scoped: the object carries no
namespace, whatever namespace the application is built for. The API requires
`capacity`, `accessModes` and exactly one source; those and the other value
rules are left to the API server, so a component with no properties builds a
volume the server refuses. A quantity written as a number is emitted in its
canonical string form. The object's labels and annotations are the `labels`
and `annotations` properties.

### Validation

**Policy.** Unlike `namespace`, `limitrange` and `resourcequota`, a
PersistentVolume is held to the
environment policy (`enforcePersistentVolumePolicy`):
- A `hostPath` or `local` source is refused unless `AllowHostPathVolumes()`
  allows it. Both name a path on the node (`local` a disk, partition or
  directory there), and a pod that binds the volume through a claim reads
  and writes that path, as it does through a `hostPath` volume of its own.
- `capacity.storage` is held to the storage maximum (`MaxStorageSize()`), as
  the storage a claim requests is.

One check covers every path that can produce the object: this kind, and the
rendered-object check that template delivery (`helmtemplate`), `passthrough`
and `manifests` run, where the refusal names the object (`passthrough: object
PersistentVolume "data": spec.hostPath: …`). On those three paths a
PersistentVolume in an API version the build cannot read is refused, since
its source and capacity cannot be checked.

Of the other source types of `corev1.PersistentVolumeSource` (k8s.io/api
v0.37.1, `core/v1/types.go`), all but `csi` and `flexVolume` name a remote
endpoint or disk, not a path on the node, and are not gated. **Not covered:** a `csi` or
`flexVolume` source. Each names a driver and options only the driver
interprets, and no field says whether a node path is exposed; a pod's `csi`
and `flexVolume` volumes are unchecked for the same reason. A driver that
exposes a node path therefore passes the gate. This is a decided limit: the environment policy makes no statement
about driver-defined volumes, on pods or on PersistentVolumes. One (an
allowlist of driver names, for instance) would be a new part of the policy
every consumer implements, and no use case asked for it.

**Breaking:** before this kind, a PersistentVolume that a chart rendered, a
`passthrough` component held or a `manifests` source yielded reached the
output unchecked. One with a `hostPath` or `local` source, or with
`capacity.storage` over the storage maximum, is now refused where it built
before — and with no policy passed a `hostPath` or `local` one is always
refused, since `NoopPolicy` allows no hostPath volume. A consumer allows it
through its policy (`AllowHostPathVolumes()`, `MaxStorageSize()`), not per
component.

### Example

The component in `pkg/cmd/kurel/testdata/persistentvolume-component/app.yaml`, built against
the `cluster.yaml` beside it, gives the `expected.yaml` beside it. The fixture test compares
the two.

## pod

### Properties

**pod** is the kind-named projection of a v1 Pod, on
the same recipe: one schema key per json field of `corev1.PodSpec`, the
property map decoded strictly into that type, two spellings of one field
refused, and one Pod named after the component in the build namespace, with
the authored spec. The handler adds the `app` label and nothing else: no
annotation, no ServiceAccount, no `automountServiceAccountToken`, no
resources, no probe. The `app` label (`app: <component>`, see ["The `app`
label"](conventions.md#the-app-label)) is the one every workload kind gives its pods and the one launcher's
traits and Services select on: a `networkpolicy` trait's policy matches the
Pod by it, and a `service` component reaches it with `selector: {app:
<component>}`. The transform
then sets the component label, as on every object a component owns. It is a bare Pod: no controller recreates it, and it
is not the workload shape of `webservice`, `worker` or `deployment` — a
property of those (`image`, `ports`, `env` at the top level) is refused as
not a PodSpec field.

### Outputs

**Traits.** The pod is a trait target: `security-context`, a `configmap`
trait's `mountPath` and an `external-secret` trait's `envFrom`/`mountPath`
change its spec as they change a workload kind's pod template.
`topology-spread` is Deployment-only and refuses a `pod` component.

The config implements `oam.ServiceAccountNamer`: the account is the authored
`serviceAccountName`, or the deprecated `serviceAccount` where that one is
unset, as the API server reads the two; with neither, the namespace's
`default` account. The Pod carries the `app` label and the component label,
and what `labels` and `annotations` author beside them.

### Validation

**Refused when the component is read**, with or without a policy:
- `ephemeralContainers`, `priority` and `overhead`, with the texts the
  workload kinds give. A pod cannot be created with ephemeral containers,
  and the Priority and RuntimeClass admission controllers set the other two
  and reject a differing value; an author sets `priorityClassName` or
  `runtimeClassName`. The three are not in the schema, so a caller that
  validates the properties against it first, as `kurel build` does, refuses
  each as an unsupported field whatever its value, an empty one included. A
  caller that converts the component without that validation gets the texts
  above, and there an empty `ephemeralContainers` or `overhead` is read as
  unset. Cost: on a cluster that runs without those admission controllers
  the two fields cannot be authored through this kind.
- An unauthored `containers`. The API's other value rules (an empty list, a
  container without a name, …) are left to the API server.
- An image without a tag or digest, or tagged `:latest`, on every init and
  regular container (`ValidateImageRef`), so a container without an image
  too.
- The same on the `reference` of an image volume (`volumes[0] "ext"
  image.reference: image "…" rejected: …`); a volume that names no
  reference is not checked. See *Image volumes* below.
- A value the API server would replace with its default: the Go type omits
  a zero there, so the authored value cannot be carried
  (`containers[0].ports[0].protocol: "" cannot be carried by the Kubernetes
  API types (the field is omitted when zero, so the API server would apply
  its default "TCP")`). These are:
  - written as `0`: a probe's `timeoutSeconds`, `periodSeconds`,
    `successThreshold` or `failureThreshold` (defaults 1, 10, 1, 3), on the
    liveness, readiness and startup probes of every init and regular
    container;
  - written as `""`: `dnsPolicy` (`ClusterFirst`), `restartPolicy`
    (`Always`) and `schedulerName` (`default-scheduler`); on every init and
    regular container `imagePullPolicy`, `terminationMessagePath`
    (`/dev/termination-log`), `terminationMessagePolicy` (`File`), a port's
    `protocol` (`TCP`), an `env` entry's `valueFrom.fieldRef.apiVersion`
    (`v1`), and the `httpGet` `path` (`/`) and `scheme` (`HTTP`) of its
    probes and of its `postStart` and `preStop` hooks; on a volume, the
    `fieldRef.apiVersion` of a `downwardAPI` item, also inside a `projected`
    source (`v1`), an image volume's `pullPolicy`, `iscsi.iscsiInterface`
    (`default`), `rbd.pool`, `rbd.user` and `rbd.keyring` (`rbd`, `admin`,
    `/etc/ceph/keyring`) and `scaleIO.storageMode` and `scaleIO.fsType`
    (`ThinProvisioned`, `xfs`). An `imagePullPolicy` or image volume
    `pullPolicy` defaults to `Always` for an image tagged `latest` or naming
    neither a tag nor a digest, else to `IfNotPresent`; the image rule
    refuses the first two, so an accepted image gets `IfNotPresent`. The
    code defaults an image volume's `pullPolicy` only under the
    `ImageVolume` feature gate, which is on and locked since Kubernetes
    1.36;
  - written as `0` when `hostNetwork` is `true`: a container port's
    `hostPort`, which the API server sets to the port's `containerPort`. It
    does so on the Pod, so on a template it applies to each pod created
    from it.

  Two strings the API server sets are not refused. `serviceAccountName`
  and its deprecated spelling `serviceAccount` are kept equal, so an
  authored `""` with neither set stays `""`; the `default` service account
  is set by an admission plugin, not by defaulting. A string field that is
  a pointer in the Go type (a gRPC probe's `service`, an `httpGet`
  `protocol`, a container's `restartPolicy`, a `hostPath` `type`,
  `azureDisk`'s fields) carries an authored `""`, so nothing is lost there.

- A match expression without its `key` or `operator`, with an operator that
  is none of the four, or with `values` that do not go with the operator, in
  every label selector of the spec: a pod affinity or anti-affinity term's
  `labelSelector` and `namespaceSelector`, a topology spread constraint's
  `labelSelector`, a projected `clusterTrustBundle` source's `labelSelector`
  and a generic ephemeral volume's claim `selector` (see ["A label selector's
  match expressions"](conventions.md#a-label-selectors-match-expressions)). A node selector term is not a label selector and is
  left to the API server.

**Marked required upstream and not checked:** the `action` of a container
restart rule and the `operator` of its exit codes (`restartPolicyRules[]`),
and the `signerName` and `keyType` of a pod certificate source of a
projected volume. The Go type leaves each out when it is empty, so the Pod
would show the omission, and launcher does not refuse it: the built-in types
ship markers and no schema, so no linked module shows that the API server
refuses a Pod without one, and both fields are behind a feature gate of the
cluster.

**Policy.** `ApplyPolicy` runs the check the rendered-object check runs on a
Pod (`enforcePodTemplatePolicy`): host namespaces, hostPath volumes, the
storage maximum on a generic ephemeral volume's claim, the registry
allowlist on an image volume's reference, the pod-level cpu and
memory maxima, and for every init and regular container the registry
allowlist, the cpu and memory maxima and the privileged, HostProcess and
capability gates. It fills no policy default: a container without resources
stays without. The kind names a field as the property it is (`hostNetwork is
not allowed by environment policy`, `containers[0] "app": …`); the three
rendered paths name it under the object (`passthrough: object Pod "runner":
spec.containers[0] "app": …`).

**Image volumes**. An image volume (`volumes[].image`)
mounts an OCI image the kubelet pulls as it pulls a container's, so its
`reference` is held to the allowed registries (`AllowedRegistries`), refused
with the registry class: `volume "ext" image.reference: image
"other.example/team/ext:1.0.0" is not from an allowed registry
[registry.example]`. The reference is read as a container image is: one
that names no registry host is Docker Hub's (`docker.io`), a tag or a digest
is not part of the host, and the host must equal a list entry exactly, a
trailing `/` on the entry aside (`registry.example:5000` is not
`registry.example`). A volume that names no
reference is not checked, and no list, or an empty one, allows every
registry. The one check holds it on every path by which a document brings
a pod spec: this kind, the pod template of `replicaset`, `replicationcontroller`,
`podtemplate` and `cnpg-pooler`, and the pod spec of a workload that
template delivery (`helmtemplate`), `passthrough` or a `manifests` source
carries. The components with a volume schema of their own (`webservice`,
`worker`, `deployment`, `statefulset`, `daemonset`, `job`, `cronjob`) take
an image volume only in the nested form of a `volumes` entry (`type: image`,
`image: {reference: ...}`), where the registry allowlist
holds it (`enforceVolumeSourcePolicy`) and `ValidateImageRef` holds its
reference when the component is read. With the container images these
are every field of a pod spec that names an image the kubelet pulls today.
**Not covered:** a custom
resource that `passthrough`, a `manifests` source or a chart carries passes
whatever it holds, the pod template of a CloudNativePG Pooler and the
extension images of a Cluster included; only the kind that builds the
object (`cnpg-pooler`, `cnpg-cluster`) holds its fields. **Breaking:** an
image volume naming a registry outside the list built before, on each of
those paths, and is refused now.

**The tag rule on an image the document names.** The reference of an image
volume is also held to `ValidateImageRef`, as a container's image is: one
without a tag or digest, or tagged `:latest` (with a digest too), or one
that is no image reference, is refused, on each of the paths above
(`volumes[0] "ext" image.reference: image "registry.example/team/ext"
rejected: no tag or digest specified; use an explicit version tag or
digest`; a rendered path names it under the object,
`spec.template.spec.volumes[0] "ext" …`). The rule is the library's, not the
environment's: the kinds apply it when the component is read and again at
generation, with or without a policy, and the refusal carries no policy
class. A reference by digest alone passes, and a volume that names no
reference is not checked. The kinds that name an image outside a pod spec
hold it to the same rule: `cnpg-pooler` its `pgbouncer.image` and the images
of its template, `cnpg-cluster` its `imageName` and the reference of each
extension, `postgresql` the image it composes from `version` (each in its entry).
**Breaking**: a document, a
chart or a source naming an untagged or `:latest` image in one of those
fields built before, and is refused now. The two catalog kinds
(`cnpg-imagecatalog`, `cnpg-clusterimagecatalog`) hold each image, component
image and extension reference of a catalog to the rule from the release
that adds them, so nothing that built is refused there.

**Known difference:** template delivery (`helmtemplate`), `passthrough` and
`manifests` do not refuse `priority` or `overhead` on a Pod they emit; the
admission controllers decide there. They do refuse ephemeral containers and
an untagged or `:latest` image, an image volume's included.

### Example

The component in `pkg/cmd/kurel/testdata/pod-component/app.yaml`, built against the
`cluster.yaml` beside it, gives the `expected.yaml` beside it. The fixture test compares the
two.

## replicaset

### Properties

**replicaset** is the kind-named projection of an
apps/v1 ReplicaSet, on the same recipe: one schema key per json field of
`appsv1.ReplicaSetSpec` (`replicas`, `minReadySeconds`, `selector`,
`template`), the property map decoded strictly into that type, and one
ReplicaSet named after the component in the build namespace, with the
authored spec. It is the bare controller: no rollout, which a `deployment`
component gives, and none of that kind's shorthand — a property of the
workload kinds (`image`, `ports`, `strategy`) is refused as not a
ReplicaSetSpec field.

### Outputs

**The `app` label.** The handler adds one thing: `app: <component>` on the
pod template's labels, beside the authored ones (see ["The `app` label"](conventions.md#the-app-label)). It
is the label every workload kind gives its pods and the one launcher's
traits and Services select on, so a `networkpolicy` trait's policy and a
`service` component with `selector: {app: <component>}` reach the pods. The
selector is emitted as authored, and the ReplicaSet itself gets no label of
the handler's; the transform then sets the component label on the object and
its template, as on every object a component owns.
- An authored `template.metadata.labels.app` with the component's own label
  value is kept. Any other value is refused, naming the component
  (``template.metadata.labels.app: "frontend" is not the `app` label of
  component "web" ("web")``): the label cannot say something else than what
  the traits select.
- A selector that matches the authored template labels and would stop
  matching once the label is added (`app` under `DoesNotExist`, or `NotIn`
  the label value) is refused (``selector: rules out the label `app: web`
  …``). A selector on `app: <component>` is satisfied by the added label, so
  the template needs no label of its own for it. A selector that cannot be
  read as one (an expression without its `key` or `operator`, an unknown
  operator, `values` that do not go with the operator, a key or value that
  is no label) cannot be compared and is refused with apimachinery's reason
  (`selector: …`). That covers what ["A label selector's match expressions"](conventions.md#a-label-selectors-match-expressions)
  lists, so this selector is not read a second time.

**Traits.** A trait target as the `pod` kind is: `security-context`, a
`configmap` trait's `mountPath` and an `external-secret` trait's
`envFrom`/`mountPath` change its pod template. `topology-spread` and
`scaler` are not available on it.

The config implements `oam.ServiceAccountNamer`, reading the template's
`serviceAccountName` as the `pod` kind reads its own. The ReplicaSet's own
labels and annotations are the `labels` and `annotations` properties; the
template's metadata is carried as authored and takes neither.

### Validation

**Refused when the component is read**, with or without a policy:
- An unauthored `selector`. The Go type cannot omit it, so it would be
  written as `null`, and launcher derives none. Whether it matches the
  template's labels, like the API's other value rules (an empty selector,
  `restartPolicy` other than `Always`, a negative `replicas`), is left to
  the API server.
- Under `template.spec`, what the `pod` kind refuses of its own spec, by the
  same checks and named by path (`template.spec.containers: required`,
  `template.spec.priority: not authorable …`, the image rule, a probe timing
  written as `0`, a defaulted string written as `""`, a `hostPort` of `0`
  under `hostNetwork`, a defective match expression in a label selector of
  the pod spec).
- `template.spec.activeDeadlineSeconds`. The API server forbids it on a
  ReplicaSet's pod template, whose pods are replaced for as long as the
  controller exists; a `pod` or a Job may set it.

The two restart-rule fields and the two pod-certificate fields that are
marked required upstream are not checked under `template.spec` either, for
the reason the `pod` kind gives: no linked module shows that the API server
refuses the omission.

**Policy.** `ApplyPolicy` holds `replicas` to the replica maximum, an unset
one as the 1 the API server defaults it to (`replicas 4 exceeds enforced
maximum 3`), and the pod template to the check the `pod` kind runs
(`enforcePodTemplatePolicy`), naming a field by its path in the properties
(`template.spec: hostNetwork is not allowed by environment policy`,
`template.spec.containers[0] "app": …`). It fills no policy default: an
unset `replicas` stays unset and a container without resources stays
without. The three rendered paths run the same checks on a ReplicaSet they
emit and name the field under the object (`spec.replicas`,
`spec.template.spec…`).

**Known difference:** template delivery (`helmtemplate`), `passthrough` and
`manifests` do not refuse `priority`, `overhead` or `activeDeadlineSeconds`
on a ReplicaSet they emit, do not require a selector and add no `app` label;
the API server decides there. They do refuse ephemeral containers and an
untagged or `:latest` image, an image volume's included.

### Example

The component in `pkg/cmd/kurel/testdata/replicaset-component/app.yaml`, built against the
`cluster.yaml` beside it, gives the `expected.yaml` beside it. The fixture test compares the
two.

## replicationcontroller

### Properties

**replicationcontroller** is the kind-named
projection of a v1 ReplicationController, on the `replicaset` kind's recipe:
one schema key per json field of `corev1.ReplicationControllerSpec`
(`replicas`, `minReadySeconds`, `selector`, `template`), the property map
decoded strictly into that type, and one ReplicationController named after
the component in the build namespace, with the authored spec. What the
`replicaset` kind states holds here — the `app` label on the pod template
and the refusal of an authored one with another value, the three traits, the
refusals under `template.spec` (`activeDeadlineSeconds` included), the two
restart-rule fields and the two pod-certificate fields that are marked
required upstream and are not checked, because no linked module shows that
the API server refuses the omission, the
replica maximum and the pod-template policy check, no policy default, the
known difference of the three rendered paths, `oam.ServiceAccountNamer`, and
`labels` and `annotations` for the object's own metadata — with these
differences:
- `selector` is a plain map of label to value, not a label selector: a
  `matchLabels` or `matchExpressions` key is refused as not a
  ReplicationControllerSpec field.
- `selector` is optional. An unset one stays unset, and the API server
  defaults it to the pod template's labels, the `app` label included.
- `template` is required (`template: required`). The API type holds it by
  pointer, so an unauthored one would be emitted without pods to create.
- No selector is refused against the `app` label: a map selector asks only
  for labels to be present, which a further label on the pods cannot break.

### Outputs

As for `replicaset`.

### Validation

As for `replicaset`, with the refusals among the differences listed under Properties.

### Example

The component in `pkg/cmd/kurel/testdata/replicationcontroller-component/app.yaml`, built
against the `cluster.yaml` beside it, gives the `expected.yaml` beside it. The fixture test
compares the two.

## podtemplate

### Properties

**podtemplate** is the kind-named projection of a v1
PodTemplate. The object has no spec: beside its identity it holds one field,
`template`, and that is the component's one property, decoded strictly into
`corev1.PodTemplateSpec`. It emits one PodTemplate named after the component
in the build namespace, with the authored template. Any other property is
refused, the object's own `kind`, `apiVersion` and `metadata` included
(`properties do not decode into a v1 PodTemplate …`).

### Outputs

**It is stored, not run.** No controller creates pods from a PodTemplate, so
the handler adds no `app` label to the template (an authored `app` label is
carried as written), the config reports no ServiceAccount
(`oam.ServiceAccountNamer` is not implemented, so an `rbac` trait binds the
component name), and it is not a trait target: a `configmap` trait's
`mountPath`, an `external-secret` trait's `envFrom`/`mountPath` and a
`security-context` trait's pod-spec properties (`runAsUser` and the five
others) are refused on it, naming the component and the kinds they apply
to. `security-context` with `psaLevel` alone is accepted, as a declaration
of the level, and writes nothing to the template
(`pkg/oam/builtin/traits/README.md`,
"Pod-spec traits on a component without a workload"). The transform still sets the
component label on the object and its template, as on every object a
component owns.

### Validation

**Refused when the component is read**, with or without a policy: under
`template.spec`, what the `pod` kind refuses of its own spec, by the same
checks and named by path (`template.spec.containers: required`, which an
unauthored `template` also gives; `template.spec.priority: not authorable
…`; the image rule; a probe timing written as `0`; a defaulted string
written as `""`; a `hostPort` of `0` under `hostNetwork`, which the API
server would replace in each pod created from the template; a defective
match expression in a label selector of the pod spec).
`template.spec.activeDeadlineSeconds` is allowed, unlike on a
`replicaset` or `replicationcontroller`: the API server accepts it on a
PodTemplate. The two restart-rule fields and the two pod-certificate fields
that are marked required upstream are not checked, for the reason the `pod`
kind gives: no linked module shows that the API server refuses the
omission. The API's other value rules are left to the API server.

**Policy.** `ApplyPolicy` holds the template to the check the `pod` kind
runs (`enforcePodTemplatePolicy`), naming a field by its path in the
properties (`template.spec: hostNetwork is not allowed by environment
policy`), since whatever reads the template creates pods from it. It fills
no policy default. The three rendered paths run the same check on a
PodTemplate they emit and name the field the same way (`template.spec…`).

**Known difference:** template delivery (`helmtemplate`), `passthrough` and
`manifests` do not refuse `priority` or `overhead` on a PodTemplate they
emit; the API server decides there. They do refuse ephemeral containers and
an untagged or `:latest` image, an image volume's included. The PodTemplate's
own labels and annotations
are the `labels` and `annotations` properties; the template's metadata is
carried as authored and takes neither.

### Example

The component in `pkg/cmd/kurel/testdata/podtemplate-component/app.yaml`, built against the
`cluster.yaml` beside it, gives the `expected.yaml` beside it. The fixture test compares the
two.

## storageclass, volumeattributesclass, priorityclass, runtimeclass, ingressclass, csidriver

**storageclass**, **volumeattributesclass**, **priorityclass**,
**runtimeclass**, **ingressclass**, **csidriver** are
the kind-named projections of six cluster-scoped objects: a
`storage.k8s.io/v1` StorageClass, VolumeAttributesClass and CSIDriver, a
`scheduling.k8s.io/v1` PriorityClass, a `node.k8s.io/v1` RuntimeClass and a
`networking.k8s.io/v1` IngressClass. Each emits that one object, named after
the component unless `objectName` names it, with no namespace, holding
exactly what was authored: the handler adds no label, no annotation and no
default of its own, and the transform sets the component label, as on every
object a component owns.

### Properties

**What is authored.**
- `ingressclass` and `csidriver` have a spec type, and the properties are
  its json fields: `controller` and `parameters` (`IngressClassSpec`), and
  the eleven fields of `CSIDriverSpec` (`attachRequired`, `podInfoOnMount`,
  `volumeLifecycleModes`, `storageCapacity`, `fsGroupPolicy`,
  `tokenRequests`, `requiresRepublish`, `seLinuxMount`,
  `nodeAllocatableUpdatePeriodSeconds`, `serviceAccountTokenInSecrets`,
  `preventPodSchedulingIfMissing`). A `csidriver` requires no property; an
  `ingressclass` requires `controller` (see **Required** below).
- The four classes have none: their fields sit on the object, beside its
  identity. The properties are those fields, decoded strictly into the
  object type, and the object's own `kind`, `apiVersion` and `metadata` are
  refused by name under any spelling the decoder would match (`metadata:
  not authorable: launcher sets the object's kind, apiVersion and metadata
  (its name is the component's, or the one objectName gives it; its labels
  and annotations are the labels and annotations properties)`).
  `storageclass`: `provisioner`,
  `parameters`, `reclaimPolicy`, `mountOptions`, `allowVolumeExpansion`,
  `volumeBindingMode`, `allowedTopologies`. `volumeattributesclass`:
  `driverName`, `parameters`. `priorityclass`: `value`, `globalDefault`,
  `description`, `preemptionPolicy`. `runtimeclass`: `handler`, `overhead`,
  `scheduling`.
- **Required** is a top-level field the API server refuses an object
  without: `provisioner` (`storageclass`), `handler` (`runtimeclass`),
  `driverName` (`volumeattributesclass`) and `controller` (`ingressclass`)
  must be a non-empty string (`provisioner: required …`), and a
  `volumeattributesclass` must carry at least one of `parameters`
  (`parameters: required …`). The upstream type marks `controller`
  optional; the API server's validation requires it all the same. The API
  does not require a PriorityClass `value`: the type always encodes one, so
  a `priorityclass` that authors none is emitted with `value: 0`. Every
  other value rule (which reclaim policies exist, a required field of a
  nested object, the names a cluster-scoped object may carry) is left to
  the API server.
- A quantity written as a number is emitted in its canonical string form.
  An authored `false` or `0` is kept where the API tells it from an unset
  field (`allowVolumeExpansion: false`, `attachRequired: false`,
  `value: 0`). Where the API type omits a zero, the field is left out of
  the object, which the API reads as the same value: `globalDefault: false`
  and an empty `description`.

### Outputs

**The object's name** is the component's unless `objectName`, or the
`Naming` hook under role `object`, names it otherwise (see ["The object name"](conventions.md#the-object-name-objectname)
below). Each handler declares its object as cluster-scoped, so the name is
claimed in no namespace: two `storageclass` components given one object
name are refused, whatever namespace the application is built for. What
refers to the object names it by its object name: a claim's
`storageClassName`, a pod's `runtimeClassName` or `priorityClassName`. The
API identifies a CSI driver by the CSIDriver's name, so a `csidriver`
component's object name must be the driver's (`csi.example.com`), written as
the component name or as `objectName`. The build namespace does not apply to
these objects. The handlers check no name rule of their own on either name:
one the API server refuses for the kind builds here and is refused at
apply. **A CSIDriver's name is not held to the limit the API documents for
it.** The type's documentation gives a driver name at most 63 characters;
the API server does not hold the CSIDriver object to that, and accepts any
DNS subdomain name. It does refuse a PersistentVolume that names a driver
longer than 63 characters, so such a CSIDriver is created and no volume can
use it. The `csidriver` kind refuses what the API server refuses of the
object and no more, so it builds that name too.

**Labels and annotations** are the `labels` and `annotations` properties,
on all six. That includes the annotations that mark a default class
(`storageclass.kubernetes.io/is-default-class`,
`ingressclass.kubernetes.io/is-default-class`): a default StorageClass or
IngressClass writes its annotation there. Whether a second default exists
is the cluster's to decide.

### Validation

A kind may list its defaulted zeros (`defaultedZeros`), and an authored
`0`, `false` or `""` on a listed field is refused, as the secret-store kinds do
(see the **secretstore** entry) and `alertmanager` does for the probes of the
containers it lists and three strings of its own.

**Authored empty lists the API defaults.** A defaulted-zero list may also
hold a list the type omits when empty, and an authored `[]` there is
refused the same way (`metrics: [] cannot be carried by the Kubernetes API
types (the field is omitted when empty, so the API server would apply its
default …)`). Three lists are refused: a `horizontalpodautoscaler`'s
`metrics` and its scaling rules' `policies`, and a `networkpolicy`'s
`policyTypes`. Two lists the API defaults are not refused, because an
empty list already means their default: `csidriver`'s
`volumeLifecycleModes` (an empty list means `Persistent`) and
`imagerepository`'s `exclusionList` (the image-reflector controller
filters tags through `GetExclusionList`, which returns the default list
when the list is empty, so an API client's `[]` means the default too).

**Policy.** None of the six has a field an `oam.Policy` method speaks to,
so `ApplyPolicy` enforces nothing and fills nothing, and each builds the
same under every policy and under none. A consumer that does not want an
author to create one of these objects does not register its handler; the
transform then refuses the component (`no handler for component type …`).

**Not covered.**
- An `ingressclass` whose `parameters` leaves `scope` out is emitted with
  `scope: null`: the API type always encodes the field, and documents
  `Cluster` as its default. Author `scope` to emit a value.
- Whether a referenced object exists: the `runtimeclass` a pod names, the
  `storageclass` a claim names, the parameters object of an `ingressclass`.

### Example

- `storageclass`: The component in `pkg/cmd/kurel/testdata/storageclass-component/app.yaml`,
  built against the `cluster.yaml` beside it, gives the `expected.yaml` beside it. The fixture
  test compares the two.
- `volumeattributesclass`: The component in
  `pkg/cmd/kurel/testdata/volumeattributesclass-component/app.yaml`, built against the
  `cluster.yaml` beside it, gives the `expected.yaml` beside it. The fixture test compares the
  two.
- `priorityclass`: The component in `pkg/cmd/kurel/testdata/priorityclass-component/app.yaml`,
  built against the `cluster.yaml` beside it, gives the `expected.yaml` beside it. The fixture
  test compares the two.
- `runtimeclass`: The component in `pkg/cmd/kurel/testdata/runtimeclass-component/app.yaml`,
  built against the `cluster.yaml` beside it, gives the `expected.yaml` beside it. The fixture
  test compares the two.
- `ingressclass`: The component in `pkg/cmd/kurel/testdata/ingressclass-component/app.yaml`,
  built against the `cluster.yaml` beside it, gives the `expected.yaml` beside it. The fixture
  test compares the two.
- `csidriver`: The component in `pkg/cmd/kurel/testdata/csidriver-component/app.yaml`, built
  against the `cluster.yaml` beside it, gives the `expected.yaml` beside it. The fixture test
  compares the two.

## horizontalpodautoscaler

**horizontalpodautoscaler** is the kind-named
projection of an `autoscaling/v2` HorizontalPodAutoscaler. It emits that
one object in the build namespace, named after the component unless
`objectName` names it, holding exactly what was authored: the handler adds
no label, no annotation and no default of its own. The handler declares its
object as namespaced, so the object name is claimed in the object's namespace.

### Properties

**Authored:** the five fields of `HorizontalPodAutoscalerSpec`, decoded
strictly at every depth: `scaleTargetRef`, `minReplicas`, `maxReplicas`,
`metrics` and `behavior`. An unknown key is refused wherever it sits (a
metric source, a scaling rule), a quantity written as a number is
emitted in its canonical string form, and an authored `0` is kept
(`stabilizationWindowSeconds: 0`).

### Outputs

**Beside the `scaler` trait.** The trait derives an autoscaler for the
workload it is attached to, from a target CPU or memory utilization.
This kind is the authored object, for what the trait does not express:
other metric sources, scaling behavior, a target launcher does not own.
The two name their objects apart (`<component>-hpa` for the trait), and
two that are given one name in one namespace are refused as a name
collision.

**Labels and annotations** are the `labels` and `annotations` properties.

### Validation

**An empty `metrics` or `policies` list is refused.** The type omits
`metrics` and a scaling rule's `policies` when empty, and the API server
then applies its default: 80% average CPU utilization for `metrics`, and
its default policies for `behavior.scaleUp` and `behavior.scaleDown`. So
an authored `[]` cannot be carried (`metrics: [] cannot be carried by the
Kubernetes API types (the field is omitted when empty, so the API server
would apply its default …)`). See *Authored empty lists the API defaults*
above.

**Required** are the two top-level fields the API server refuses an
autoscaler without: `scaleTargetRef` (`scaleTargetRef: required …`) and
`maxReplicas` (`maxReplicas: required …`; an authored `0` is refused
the same way, since the type cannot tell it from an unset one). The
config is exported, so `Generate` repeats both. Every other value rule
is left to the API server: a negative `maxReplicas`, the `kind` and
`name` inside `scaleTargetRef`, `minReplicas` against `maxReplicas`,
which metric source goes with which `type`. The label selector of a
metric (`metrics[].object.metric.selector`, and under `pods` and
`external`) is not held as other kinds hold theirs: the API server
validates nothing of it, and a kind does not refuse what the API admits.

**Policy.** `maxReplicas` is held to the environment policy's replica
maximum (`MaxReplicas()`), as the `scaler` trait holds its own and as a
HorizontalPodAutoscaler a chart renders, a `passthrough` component holds
or a `manifests` source yields is held: `component "web": maxReplicas:
replicas 4 exceeds enforced maximum 3`. Nothing else is held, and no
default is filled: the policy's `scalerMinReplicas` and
`scalerMaxReplicas` defaults belong to the `scaler` trait. A nil policy
checks nothing.

**The target is the author's, and is not checked.** `scaleTargetRef`
names an object by `kind`, `name` and `apiVersion`; launcher points it
at no component and does not look for the object in the document. To
scale a workload component, name its object: the component name, or its
`objectName`. **The guards the `scaler` trait applies to its own
workload do not see this kind:** a `deployment`, `webservice` or
`worker` with a claim that is not ReadWriteMany refuses a `scaler` trait
whose `maxReplicas` is above 1, and builds beside a
`horizontalpodautoscaler` component that scales it further. That is the
state of an autoscaler emitted through `passthrough`, `manifests` or
template delivery.

**Not covered.** Whether the target exists or can be scaled. The
object's status is the controller's and is not written.

### Example

The component in `pkg/cmd/kurel/testdata/horizontalpodautoscaler-component/app.yaml`, built
against the `cluster.yaml` beside it, gives the `expected.yaml` beside it. The fixture test
compares the two.

## statefulset

### Properties

**statefulset** — `serviceName` and `volumeClaimTemplates`
(`name`, `mountPath` or — for a `volumeMode: Block` claim — `devicePath`,
`size`, `storageClass`, `accessModes`, plus the rest of
`corev1.PersistentVolumeClaimSpec`; see ["Raw block volumes"](common-config.md#raw-block-volumes-volumemode-block)). The
StatefulSetSpec-level and
claim-template field sets are classified in ["StatefulSet-level and
claim-template properties"](common-config.md#statefulset-level-and-claim-template-properties). `ports` declares the main container's
ports (see ["Main container ports"](common-config.md#main-container-ports)). For scheduling it takes `affinity`, `tolerations`
and `topologySpreadConstraints` as the raw `corev1` shapes
(see ["Raw scheduling properties"](common-config.md#raw-scheduling-properties)); the four-key
`affinity` shorthand is not published on this kind.

**`serviceName` names the governing Service, which the component does
not emit.** Author it as a headless `service` component (`clusterIP:
None`, selecting `app: <component>`) and name it here. It has no default:
unset, the StatefulSet carries `serviceName: ""`. An authored value must be
a valid Service name, a DNS-1123 label (at most 63 characters, lowercase
letters, digits and `-`, starting and ending with a letter or digit), and
is refused at conversion otherwise (`serviceName: "api.v1" is not a valid
Service name, which must be a DNS-1123 label`);
`Generate` applies the same rule to the config's `ServiceName`. A leading
digit is accepted at every Kubernetes release: the API validates a StatefulSet's
`serviceName` as a DNS-1123 label, not as a Service name, and the `statefulset` emits no
Service, so `spec.kubernetesVersion` does not change it. The governing Service is a
`service` component's, and that name is checked (`docs/delivery-scope.md` §3.5). Previously `serviceName` defaulted to the
component name and the component emitted that headless Service itself;
["Main container ports"](common-config.md#main-container-ports) has the migration, including why an existing
StatefulSet must be recreated.

### Outputs

It emits no Service.

### Validation

**A route to the pods names the authored Service.** With no Service of
its own, the component is no implicit backend: an `ingress`, `httproute`
or `expose` without a `backend` on it is refused (`component "<name>"
has no service port`). Put the trait on the authored `service`, or name
that Service as the trait's `backend`.

### Example

The component in `pkg/cmd/kurel/testdata/statefulset-ports/app.yaml`, built against the
`cluster.yaml` beside it, gives the `expected.yaml` beside it. The fixture test compares the
two.

## daemonset

**daemonset** — `tolerations` (`key`/`operator`/`value`/`effect`/`tolerationSeconds`;
`tolerationSeconds` and the toleration cross-field rules arrived via the shared parser — see ["What `tolerations` changed
for `daemonset`"](common-config.md#what-tolerations-changed-for-daemonset), which is the only place in this work that is not
additive); `ports` declares the main container's ports (see ["Main container
ports"](common-config.md#main-container-ports)). It emits no Service: author a `service`
component selecting `app: <component>` to expose the pods, and put routing
traits on it. It also publishes the raw `affinity`
and `topologySpreadConstraints` (see ["Raw scheduling properties"](common-config.md#raw-scheduling-properties)) and
`sidecars`, read, checked and enforced exactly as on `statefulset`: the same
closed entry, the same pod-wide port-name check against the main container's
`ports`, and the same `ApplyPolicy` loop. A sidecar here runs on every node
the DaemonSet schedules to.
DaemonSetSpec-level (`daemonset_spec.go`): `updateStrategy`,
`minReadySeconds`, `revisionHistoryLimit`. `appsv1.DaemonSetSpec` has five
fields; `template` is the pod projection above and `selector` is
builder-managed, which leaves these three.

### Properties

Previously `port > 0` added a ClusterIP Service named after
the component, which held the component name to the Service-name rule and an
ingress `portName` to `http`.
With no Service, only the container-name rule applies (see
**webservice / worker**).

| Property | Type | Effect | Compatibility |
|----------|------|--------|---------------|
| `updateStrategy` | object | `type` (**required**, `RollingUpdate`\|`OnDelete`) and `rollingUpdate` (`maxUnavailable`, `maxSurge`), only accepted under `type: RollingUpdate`. `type: RollingUpdate` alone is accepted — the apiserver defaults the `rollingUpdate` object that upstream validation then requires. | additive |
| `minReadySeconds` | int ≥ 0 | Seconds a new pod must stay ready before it counts as available. | additive |
| `revisionHistoryLimit` | int ≥ 0 | Superseded ControllerRevisions kept for rollback. | additive |
| `selector` | — | **Rejected outright**, not silently dropped: the selector is builder-managed (`app: <component>`), must equal the generated template labels, and is immutable once created. | **Behavior-changing** |
| `template` | — | **Rejected outright**, as on `deployment`: the pod template is projected from the component's own container and pod-level properties. A document was refused already, by the authored-property check; the parser now refuses it too, where a caller that skips that check had it dropped. | **Behavior-changing** for such a caller |

### Outputs

Every accepted property is presence-gated: a document authoring none of them
produces byte-identical output to before, because `DaemonSetSpecConfig.apply`
writes only the fields that were authored. The one behavior change is
`selector`: a `selector:` on a daemonset used to be silently ignored — authored-document validation checked type
names and identity, not property shape — and now fails the build, both
because `daemonset` does not declare the key and because `ValidateAuthoredProperties`
(`pkg/cmd/kurel/build.go:149`) now rejects an undeclared authored key on its
own. That is the point — a silently dropped selector reads as applied.

### Validation

`maxUnavailable` and `maxSurge` each accept a non-negative integer or a `"N%"`
string with N ≤ 100 (the integer form is a pod count and is deliberately
uncapped, matching upstream's `IsNotMoreThan100Percent`, which inspects only
percentages). A leading sign (`"+50%"`) is rejected — upstream's own
`IsValidPercent` form check is used rather than a `TrimSuffix`/`Atoi` pair,
which would accept it. Unlike the statefulset kind, whose `maxUnavailable`
"cannot be 0", either DaemonSet knob may be zero on its own; it is the
**pair** that must have exactly one non-zero member. That rule is enforced
against the *effective* pair, counting the API defaults for whichever half the
document leaves out — `maxUnavailable` 1, `maxSurge` 0
(`k8s.io/api/apps/v1/types.go`, the `RollingUpdateDaemonSet` field docs). So
`maxSurge: 2` on its own is rejected here rather than at apply time (the
defaulted `maxUnavailable: 1` makes both non-zero), and using surge means
writing `maxUnavailable: 0` alongside it. The error names which half was
defaulted, since that is the half absent from the author's YAML.

Both knobs are published as the `Types: [integer, string]` union, so the integer and the percentage form both pass
property validation on the authored and the emitted path alike, and a value of
any other type is rejected there before the parser sees it. The union leaves
`Type` empty, so a schema consumer that does not read `Types` still accepts
both forms.

**Two rules here are deliberately stricter than upstream.** The API accepts
both shapes; what it does with them differs. `updateStrategy.type` is required
here, where the API defaults it to `RollingUpdate` and acts on that — so a bare
`updateStrategy: {}` is a legal document whose entire meaning comes from
defaulting rather than from anything written. `updateStrategy.rollingUpdate` is
refused under `type: OnDelete`, where `ValidateDaemonSetUpdateStrategy`'s
`OnDelete` branch is empty and the field is accepted and never read — the
silently-ignored knob this projection exists to remove. Both are still
*additive*: `updateStrategy` is a new
property, so no document that built before this change can carry either shape.
In the other direction the parser is laxer in exactly one place — upstream
requires a non-nil `rollingUpdate` under `RollingUpdate`, but apiserver
defaulting satisfies that, not the author, so `type: RollingUpdate` alone is
accepted (the same reasoning the statefulset kind applies to its own optional
`rollingUpdate`).

It is a behavior change against what the code did, not against what the format
promised. `docs/oam/design-gvk.md` § Parser Strictness already states the
contract — "Launcher rejects unknown fields in all launcher-native documents.
An `app.yaml`, `kurel.yaml`, or `cluster.yaml` with unrecognised keys is a
build error" — so a daemonset carrying `selector:` was never a valid
`launcher.gokure.dev/v1alpha1` document, and the same-version stability promise
("every document that was **valid** under it remains valid") never covered it.
The authored path simply had not implemented that strictness for this key; the
rejection implements it, with a message that says why rather than the generic
unknown-key error.

### Example

The component in `pkg/cmd/kurel/testdata/daemonset-ports/app.yaml`, built against the
`cluster.yaml` beside it, gives the `expected.yaml` beside it. The fixture test compares the
two.

## cronjob

### Properties

**cronjob** — `schedule` accepts a standard 5-field cron expression (e.g.
`0 2 * * *`; not 6-field — a seconds field is rejected), one of the fixed
`@`-descriptors (`@yearly`, `@annually`, `@monthly`, `@weekly`, `@daily`,
`@midnight`, `@hourly`; `@reboot` is rejected, meaningless for a CronJob),
or `@every <duration>` (e.g. `@every 1h30m`, validated via Go's
`time.ParseDuration` — a malformed duration is rejected, not merely
regex-matched). `restartPolicy` (default `OnFailure`),
`successfulJobsHistoryLimit`/`failedJobsHistoryLimit`. `ports` declares
container ports, and emits no Service (see ["Main container ports"](common-config.md#main-container-ports)). No
`sidecars` schema key (init containers only): a plain sidecar keeps running
after the main container exits, so the Job's pod would never complete. The
container that fits is the restartable init container, an `initContainers`
entry with `restartPolicy: Always` (see ["Container fields"](common-config.md#container-fields)). It publishes the raw `affinity`, `tolerations` and `topologySpreadConstraints`
(see ["Raw scheduling properties"](common-config.md#raw-scheduling-properties)), written onto the Job template's pod
template.
CronJobSpec-level: `concurrencyPolicy` (`Allow`|`Forbid`|`Replace`; the API's
own default is `Allow`, but this is only ever written when authored —
`ConcurrencyPolicy` has no `omitempty`, so writing it unconditionally would
add the key to every generated CronJob), `suspend`, `startingDeadlineSeconds`
(`>= 0`), `timeZone` (a real IANA zone name, e.g. `Europe/Brussels`; an
authored empty string is rejected outright, `Local` is rejected
case-insensitively even though Go's own `time.LoadLocation("Local")`
succeeds — Kubernetes' CronJob validation rejects it as server-dependent —
and any other value is checked via `time.LoadLocation`, which this binary
resolves from an embedded IANA database rather than the host's zoneinfo).
JobSpec-level (projected onto `spec.jobTemplate.spec`, shared with the `job`
component — the full table is under **job**, and the shared trio is
described in ["Common config"](common-config.md#common-config)): `backoffLimit`, `completions`,
`parallelism`, `activeDeadlineSeconds`, `ttlSecondsAfterFinished`,
`completionMode`, `backoffLimitPerIndex`, `maxFailedIndexes`,
`podReplacementPolicy`, `managedBy`, `successPolicy`, `podFailurePolicy`.
`suspend` on a cronjob
is the **CronJobSpec** field, not the JobSpec one — see the `suspend` note in
["Common config"](common-config.md#common-config).

### Outputs

Every CronJobSpec-level and JobSpec-level field above except
`restartPolicy` and the two history limits is presence-gated: omitting it
never adds a key to the generated output, even where the corresponding
Kubernetes default (e.g. `concurrencyPolicy: Allow`) would otherwise appear
to have been authored. Those three are always written, taking `OnFailure`, 3
and 1 when omitted.

### Validation

The CronJob's name is at most 52 characters, the longest the API server
creates one under (`ValidateCronJobCreate`: the controller names each Job
after the CronJob plus an 11-character suffix, and that Job's name is held to
63). The rule is held on the name the CronJob takes, the component name or
its `objectName` (or the `Naming` hook's answer), when the component is read
and again at `Generate`: `cronjob "<name>": the component name is the
CronJob's name, which must be at most 52 characters: it has 53`, or
`objectName (or the Naming hook's answer for role "object"): "<name>" is not
a valid CronJob name, which must be at most 52 characters: it has 53`. A
component named with 53 to 63 characters built before this rule and is
refused now; a longer one was already refused by the
container-name rule. With an Indexed job template the name must also leave
room for the index in each pod's hostname, a limit that applies when the
controller creates the Job, not when the CronJob is admitted; the build does
not check it.
Known limitation: the plain 5-field `schedule` form accepts any 5
whitespace-separated tokens with no per-field semantic check (e.g.
`99 99 99 99 99` builds successfully here and is only rejected later, by
Kubernetes' own API server) — this is deliberate, not an oversight: tightening
it would reject documents that build successfully today, which is a breaking
change under this project's additive-compatibility rule (see
`docs/oam/design-gvk.md`).

### Example

The component in `pkg/cmd/kurel/testdata/cronjob-full/app.yaml`, built against the
`cluster.yaml` beside it, gives the `expected.yaml` beside it. The fixture test compares the
two.

## job

**job** (`job.go`) — a run-to-completion workload:
`image`, `restartPolicy` (default `OnFailure`; the Job API rejects `Always`,
which is what pod defaulting would otherwise supply, and a present-but-
non-string or empty value is refused rather than read as an omission that
would silently restore that default), and the shared container
and pod-level surface above. No `port` and no Service; `ports` declares
container ports (see ["Main container ports"](common-config.md#main-container-ports)), and only then may a probe or
hook name a port; no `sidecars` schema key (init containers only), matching
`cronjob` and for the reason given there. It publishes the raw `affinity`, `tolerations` and `topologySpreadConstraints`
(see ["Raw scheduling properties"](common-config.md#raw-scheduling-properties)), written onto the Job's pod template.
It emits a `Job` and nothing else.

### Properties

The twelve JobSpec-level properties are the ones `cronjob` projects onto its
job template, projected here onto `spec` directly. Every one is
presence-gated, so a document authoring none of them emits no key for them.

| Property | Type | Effect | Compatibility |
|----------|------|--------|---------------|
| `backoffLimit` | int ≥ 0 | Retries before the job is marked failed. API default 6, or 2147483647 when `backoffLimitPerIndex` is set. | additive |
| `completions` | int ≥ 0 | Successful pods required. Required under `completionMode: Indexed`. | additive |
| `parallelism` | int ≥ 0 | Pods running at once. ≤ 100000 under `Indexed`; ≤ 10000 when `completions` > 100000 and `backoffLimitPerIndex` is set. `0` runs no pods at all. | additive |
| `activeDeadlineSeconds` | int > 0 | Seconds the job may run. Must be **positive** — `0` is rejected, not just negatives. Distinct from the pod-level `podActiveDeadlineSeconds`. | additive |
| `ttlSecondsAfterFinished` | int ≥ 0 | Seconds after finishing before the job and its pods are deleted. | additive |
| `completionMode` | `NonIndexed`\|`Indexed` | How completions are counted. `Indexed` requires `completions`. | additive |
| `backoffLimitPerIndex` | int ≥ 0 | Retries within one index. Requires `Indexed`. | additive |
| `maxFailedIndexes` | int ≥ 0 | Failed indexes tolerated before the whole job fails. Requires `backoffLimitPerIndex` (and so `Indexed`); ≤ `completions` and ≤ 100000, or ≤ 10000 (and required) when `completions` > 100000. | additive |
| `podReplacementPolicy` | `Failed`\|`TerminatingOrFailed` | When a replacement pod is created. An empty string is rejected rather than treated as unset, for the same reason as `managedBy` below. | additive |
| `managedBy` | string | Controller reconciling this job instead of the built-in one. A domain-prefixed path (`example.com/controller`), ≤ 63 characters. An empty string is rejected rather than treated as unset. | additive |
| `successPolicy` | object | `rules[]` (1..20) of `succeededIndexes` (increasing comma-separated intervals, every index < `completions`) and/or `succeededCount` (≤ `completions`, and ≤ the number of indexes named alongside it). Requires `Indexed`. An empty `succeededIndexes` is rejected rather than treated as unset: it denotes no indexes at all, so it would otherwise satisfy the at-least-one-field rule while naming nothing. | additive |
| `podFailurePolicy` | object | `rules[]` (0..20), each with an `action` (`FailJob`\|`FailIndex`\|`Ignore`\|`Count`) and **exactly one** of `onExitCodes` (`operator` `In`\|`NotIn`, `values[]` of 1..255 exit codes in increasing order without duplicates, optional `containerName`) or `onPodConditions[]` (up to 20 `type`/`status` patterns; an omitted or null `status` defaults to `True`, an empty one is rejected). An empty `onPodConditions: []` alone is neither; beside `onExitCodes` it is both, as the rule's published `exclusive` group counts it. Requires `restartPolicy: Never`, and pins `podReplacementPolicy` to `Failed` when that is also authored. `FailIndex` additionally requires `backoffLimitPerIndex`. | **Behavior-changing** on `cronjob` (see below); additive on `job` |
| `suspend` | bool | **`JobSpec.Suspend`** — create the job with no pods. Not the same field as `cronjob`'s `suspend`; see the `suspend` note in ["Common config"](common-config.md#common-config). | additive |
| `selector`, `manualSelector`, `template` | — | **Rejected outright**, not silently dropped: the Job selector is generated by the job controller from a unique per-job label, and a hand-written one adopts other jobs' pods. `manualSelector` only has meaning alongside one. `template` is replaced wholesale from the component's own container and pod-level properties, so an authored one is discarded rather than merged — the same rejection the deployment kind makes. `cronjob` refuses the three as well: its job is the same JobSpec, and its parser read none of them and said nothing. | **Behavior-changing** (see below) |
| `scheduling` | — | **Rejected outright** on `job` and `cronjob`: alpha upstream, behind the `WorkloadWithJob` feature gate, and read by neither. | **Behavior-changing** for a caller that skips the authored-property check |
| `schedule`, `timeZone`, `concurrencyPolicy`, `startingDeadlineSeconds`, `successfulJobsHistoryLimit`, `failedJobsHistoryLimit` | — | **Rejected outright**: these are the CronJobSpec-only keys, and they are exactly what a `cronjob` document retyped to `job` leaves behind. Dropping them silently would run at apply time the work a schedule deferred (see below). | additive (no valid `job` document ever carried them) |

### Outputs

**Updating a `job` in place needs it force-replaced, which the `force-replace`
trait asks for.** A Job's pod
template is immutable: `ValidateJobSpecUpdate` runs `validatePodTemplateUpdate`
on every update, and the only carve-out is for scheduling directives on a
suspended Job. By default, changing this component's `image`, `command`, `env`
or any other pod-level property therefore produces an apply failure on the
*second* delivery, not a re-run — the first apply creates the Job, and every
later one is rejected while the Job object still exists. That default is
deliberate and unchanged: replacing a Job re-runs it, and
for long batch work a re-run should be the author's explicit choice.

To opt in, add the **`force-replace` trait** (no properties) to the component.
It sets the `ForceReplace` delivery intent on the component's application. kure's Flux workflow turns that into
`kustomize.toolkit.fluxcd.io/force: enabled` on every object the
component emits — the Job — which kustomize-controller
reads as its apply `ForceSelector`: on an immutable-field error it deletes and
recreates the object, so an update re-runs the Job, **stopping any run in
progress**. **Annotating the component itself still does not reach the
Job**: a component's own annotations, the ones beside its `type` and
`properties`, are read only for the tier annotation. The `annotations`
property is another matter: it goes on the Job's own metadata, as on every
kind component (["The object's labels and annotations"](conventions.md#the-objects-labels-and-annotations-labels-annotations)). The Flux force
annotation written there is on the Job as authored, and kustomize-controller
reads it as it reads the workflow's; the trait is the way that names no
delivery engine. See the
[Trait Handlers](https://pkg.go.dev/github.com/go-kure/launcher/pkg/oam/builtin/traits)
catalogue.

Outside the package, `spec.force: true` on the enclosing Flux Kustomization
has the same effect for every resource it reconciles, and deleting the Job
before redelivering avoids the error without either. `cronjob` does not have
this problem: a CronJob's `jobTemplate` is mutable, and each run creates a
fresh Job.

The generated `Job` carries `app: <component>` as its own labels and its pod
template's, carries no annotations of the handler's, and leaves `spec.selector`
unset for the job controller to fill. What the `labels` and `annotations`
properties author is added to the Job's own metadata, not to its pod template.
The empty selector is deliberate and is the one workload kind where it stays
that way: the Job controller generates
`spec.selector` plus its matching `controller-uid`/`job-name` pod labels
server-side, so writing one here would fight it. Deployment, StatefulSet and
DaemonSet get no such server-side defaulting and so are written explicitly —
see [Conventions](conventions.md#conventions).

### Validation

The Job's name is at most 63 characters: the API server writes it as the
value of the pod template's `job-name` and `batch.kubernetes.io/job-name`
labels (`generateSelector`), and a label value is at most 63. The rule is
held on the name the Job takes, the component name or its `objectName` (or
the `Naming` hook's answer), when the component is read and again at
`Generate`, in the words the `cronjob` rule uses. A component name over 63
characters was already refused at generation, by the container-name rule; it
is now refused when the component is read, by this one.

On a job with `completionMode: Indexed` and `completions` above 0 the name is
held to one more rule of the API server's (`validateNameAllowsCompletions`):
the pod of each index takes the hostname `<name>-<index>`, so
`<name>-<completions-1>` must be a DNS-1123 label, at most 63 characters and
without a dot. It is held at the same two places, and refused as `job
"<name>": the component name is the Job's name, and with completionMode
Indexed and completions 11 the pod of the last index takes the hostname
"<name>-10", which must be a DNS-1123 label: …`. An Indexed job whose name
leaves no room for its last index built before this rule and is refused now.
A document cannot leave `completions` unset on an Indexed job, but a
`JobConfig` built in Go can: with `Parallelism` unset too the API server
reads both as 1, so `Generate` holds the name to `<name>-0` there.

Every cross-field rule above is ported from `ValidateJobSpec`
(`k8s.io/kubernetes/pkg/apis/batch/validation`) at the pinned
`k8s.io/api` version, so a document this parser accepts is one the API server
accepts too — the rejections move an apply-time failure to build time. One
upstream constraint is deliberately **not** enforced: `BackoffLimitPerIndex`'s
field doc says per-index backoff only applies when the pod restart policy is
`Never`, but `ValidateJobSpec` does not check it and neither does this parser
— the schema description carries the constraint instead. Refusing a
combination the API server accepts would be this projection inventing a rule,
which is a different thing from the daemonset kind's `updateStrategy`
strictness, where upstream accepts a field and then provably never reads it.

Two rejections go the other way — stricter than `ValidateJobSpec`, because the
field's own documented contract in `k8s.io/api/batch/v1` is stricter than the
validator that is supposed to enforce it. `activeDeadlineSeconds: 0` is refused
although the validator only requires non-negative, because the field doc says
"value must be positive integer". An empty `successPolicy.rules[].succeededIndexes`
is refused although the validator's index parser returns "no indexes, no error"
for it, because the field doc says "At least one element is required". Both
follow the documented contract rather than the gap in its enforcement. A third,
`podFailurePolicy.rules[].onPodConditions[].status: ""`, is covered with
`podFailurePolicy` below. These three are called out here because they are the
only places a document this parser refuses would in fact have applied.

`podFailurePolicy` is published by both `job` and
`cronjob`, through the same shared parser as the rest of the table. Its rules
are ported from `validatePodFailurePolicy` / `validatePodFailurePolicyRule`,
with the differences below.

**Two checks run at emission rather than at parse time**, in
`validateJobPodFailurePolicyAgainstTemplate`, because both read the built pod
template rather than the property map: upstream requires
`restartPolicy: Never` alongside a `podFailurePolicy` (these components
default it to `OnFailure`, so a document authoring only the policy is refused
with a message naming the restart policy), and `onExitCodes.containerName`
must name a container the template actually carries — the component's own
container or one of its `initContainers`. Checking the emitted template rather
than the config is what keeps the check from drifting from what is written,
the same reason the component-name check in `createJob` reads the emitted
name.

**One place this parser is deliberately no stricter than upstream**: an empty
`rules` list is accepted. `validatePodFailurePolicy` has no "at least one
rule" check — unlike `validateSuccessPolicy`, which does — and
`PodFailurePolicy.Rules`' own field doc states only the cap, so unlike
`activeDeadlineSeconds` and `succeededIndexes` above there is no documented
contract to follow past the validator. An empty list is not inert either: a
non-nil policy pins `podReplacementPolicy` and `restartPolicy` whatever its
rules say.

**`onPodConditions[].status` defaults to `True`**: a
pattern that omits the key, or sets it to null, compiles to an explicit
`status: "True"`, the value upstream's
`SetDefaults_PodFailurePolicyOnPodConditionsPattern` fills in before
validation. It is written out rather than left to the API server because the
field carries no `omitempty`, so an unset one would be emitted as
`status: ""`. An authored empty string is still refused, which is
deliberately **stricter than upstream** — its defaulter fills `""` too — on
the reading that an empty value is more likely a templating slip than a
request for `True`. The default is additive: it only makes previously
refused documents valid, and every previously valid document compiles
identically.

**Compatibility.** On `job` this is additive — the component type is newer
than the key. On `cronjob` it is **behavior-changing**, and the reason is
worth stating precisely: before this change an authored `podFailurePolicy` on
a cronjob was not refused, it was **ignored**. At the time, property-schema
validation ran on emitted elements only, never on authored documents, so a
key no handler read was dropped in silence. A cronjob document that was
already authoring `podFailurePolicy` therefore either compiles to a CronJob
that now carries the policy, or stops building — most likely on the
`restartPolicy: Never` requirement, since the component defaults to
`OnFailure`. Under the additive test in `docs/oam/design-gvk.md` ("every
previously valid document remains valid *and* compiles to the same output")
that was not additive. The gap that made this true of *every* property ever
added to an existing kind — the authored path not enforcing the Parser
Strictness that document promises — belonged to the repo rather than to this
change, and is fixed: `kurel build` now
calls `ValidateAuthoredProperties` (`pkg/cmd/kurel/build.go:149`), which
checks declared keys and shapes on the authored document directly.

The `selector`/`manualSelector` rejection is the same class of change the
daemonset kind's `selector` rejection is, and rests on the same reasoning:
`docs/oam/design-gvk.md` § Parser Strictness already promises that unknown
fields in a launcher-native document are a build error, so neither key was
ever part of a valid `launcher.gokure.dev/v1alpha1` job. Here it is not even a
tightening — `job` is a new component type, so no document that built before
this change can carry either key. The explicit rejection exists so the
message says *why*: to a caller of the handler, which gets the reason alone,
and in a document too, where the authored-property
check appends the reason to its refusal of the undeclared key.

The six CronJobSpec-only keys — `schedule`, `timeZone`, `concurrencyPolicy`,
`startingDeadlineSeconds`, `successfulJobsHistoryLimit` and
`failedJobsHistoryLimit` — are rejected for the same reason `selector` is, but
the case they guard is sharper. A `job` component and a `cronjob`
component differ by exactly these six properties, so retyping an existing
`cronjob` document to `job` leaves every one of them behind. Left undeclared
each would be dropped in silence, turning a schedule into a Job that runs
once, immediately, at apply time — not a missing field, but work executed at a
time nobody asked for. Each message names the job-level property that does the
nearest equivalent job where one exists (`activeDeadlineSeconds` for
`startingDeadlineSeconds`, `ttlSecondsAfterFinished` for both history limits),
so the refusal is a redirection rather than only a "no".

The properties this component introduces read an **explicit `null` as an
omission**: `backoffLimitPerIndex: null` means "leave this unset", not "this is
a value of the wrong type". That matches `pkg/oam`'s own
`validatePropertyValue`. This is not special to this component: every
optional-field helper in `common.go` routes its presence check through
`authoredValue`, so each JobSpec field shared with `cronjob` that is read
through one of them reads a null the same way on both kinds, and so does a
field any other kind reads through one. The change to read an explicit `null` as an omission was
one-directional — a null that used to be refused is now absence, and nothing
that parsed before parses differently — so no document that built before
stopped building.

**The component name must be a DNS-1123 *label*, not merely a subdomain** — a
rule this component shares with every workload kind; see "The main container
is named after the component" under ["Common config"](common-config.md#common-config).

### Example

The component in `pkg/cmd/kurel/testdata/job-full/app.yaml`, built against the `cluster.yaml`
beside it, gives the `expected.yaml` beside it. The fixture test compares the two.

## passthrough

### Properties

**passthrough** — `object` (full apiVersion/kind/metadata/spec), `clusterScoped`.

**`object` must be a single object; a list is rejected.** `Generate` emits the map
verbatim as one resource and fills in metadata it finds missing: `metadata.name`
defaults to the component name when it is absent, empty, **or not a string**, and
on a namespaced object `metadata.namespace` likewise — so an inline *non-empty
string* namespace survives untouched, while a non-string one is replaced rather
than emitted. The values of the emitted object are JSON values (`int64`, `float64`,
`json.Number`, `map[string]any`, `[]any`), whatever Go types the authored object
held, so the emitted `Unstructured` can be deep-copied; the
config's own `Object` keeps the authored values. A number is an `int64` where it
fits, a `float64` where a `float64` holds it exactly, and otherwise a `json.Number`
with its own digits (an integer beyond the `int64` range, `9.223372036854775809e18`,
a `1e-9999999`), so no digit is rounded; an authored `json.Number` takes the type its
text has, and an empty one is `0`, as `encoding/json` writes it. A value with no JSON form (a func, a
chan, a complex number, a NaN or infinity, a `json.Number` that is not a number) is refused,
naming the field, instead of being emitted for a deep copy to panic on. An object with a key given twice in one JSON object, at any
depth (a `json.RawMessage` a Go caller built), is refused, naming the field: the
policy check and the emitted object would otherwise read it differently. The
field is named by the path every passthrough error uses (`fieldPath`): keys
joined by dots, and a key that is empty or holds a dot, a bracket or a quote
written as a quoted index, `labels[""]` or `labels["a.b"]`, so that two different
fields never share a path. A number
beyond `float64` range in that object is read as text and is not refused.

### Outputs

**Scope**. Whether the object is namespaced is
resolved with the precedence `manifests` gives a `scopeOverrides` entry,
with `clusterScoped` in the entry's place (`resolveClusterScoped`,
`passthrough.go`):

**A kind whose scope the Kubernetes API governs** (a built-in kind, a
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

**Any other kind:** an authored `clusterScoped` decides, `true` or `false`, also
against the table. A custom resource kure registers as cluster-scoped that the
target cluster serves namespaced takes `clusterScoped: false`.

**Not authored** (absent or `null`): the table decides for a kind kure registers,
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

### Validation

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

**Policy.** `ApplyPolicy` holds the object to the environment policy, with the check template delivery runs on the objects a chart
renders (`helmtemplate`'s *Every emitted workload*); the transform calls it with
`NoopPolicy` when no policy is passed. A Pod, PodTemplate, ReplicationController,
Deployment, StatefulSet, DaemonSet, ReplicaSet, Job or CronJob is checked as an authored
workload is: host namespaces, hostPath volumes, the cpu/memory maxima, the storage
maximum on a generic ephemeral volume's claim, the registry allowlist on an image
volume's reference (**breaking**: an object with one from a
registry outside the list built before; see *Image volumes* under **pod**), and
for every init and regular container
the registry allowlist, the privileged, HostProcess and capability gates, and
`ValidateImageRef` (no untagged image, no `:latest`), which holds the reference of an
image volume too (**breaking**: an object with an untagged or
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
(`oam.ExplicitSecretPolicy`, see the `pkg/oam` README), as the `secret` trait is: `passthrough: object Secret "demo/creds": the object is a
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
**Breaking**: a workload or claim with an
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
gives such an object). A MetalLB `BGPPeer` at `metallb.io/v1beta2`
is a registered kind, so one that does not decode as that kind
(`holdTime: [1]`) is refused here too; it was emitted unread. An object of a registered kind that states `Kind` or `apiversion` beside the exact key is
refused here as well, with the parser's text for the cause (`the key "Kind" equals "kind"
only after case folding; …`). **Breaking**: the `Kind` case was read as its Go type and
checked, and so was the `apiversion` case where that key states a version the kind is
registered in (`apiversion: v1` beside `apiVersion: v1`); with another version there it
was unreadable before, for another cause. Every build
decodes the object, since the transform applies `NoopPolicy` when no policy is passed;
only a config no policy was applied to, one a Go caller builds
outside the transform, emits the object undecoded, as authored.

**Behaviour change:** previously a `passthrough` object reached the
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

### Example

No example yet.

## crd, manifests

### Properties

**crd / manifests** — `inline` xor `url`; `manifests` adds `scopeOverrides`
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

### Outputs

Both components decode a document of a registered kind into its Go type, with the
decode `helmtemplate` gives a rendered chart, and treat a field that type does not
declare — one a newer Kubernetes version added, say — as it does (`helmtemplate`'s
*Undeclared fields*): a workload or a claim that sets one is refused, under any policy
and with none (`manifest source: parse manifests: Deployment "demo/web": undeclared
field spec.template.spec.fieldOfALaterVersion: …`), an inline source when the component
is built and a url source when it is fetched; an object of any other registered kind is
emitted as written, unstructured, the field kept, and a `CustomResourceDefinition` kept
so still passes `crd` and still gives its scope to the resources beside it. **Breaking**, in the same two ways: a source with such a workload or
claim no longer builds, and the output of other kinds gains the fields that had been
dropped. The same known limit applies (a key inside a type that unmarshals itself, a
CRD's `items` schema for one, is still dropped). A document the decoder of its kind
panics on is a build error naming the object, as it is there (`manifest source: parse
manifests: parse error in Kubernetes object: failed to decode object: the decoder
panicked on CiliumNetworkPolicy "demo/p": …`), and so is malformed JSON the decoder
cannot read past. A MetalLB
`BGPPeer` at `metallb.io/v1beta2` is a registered kind and is read as one, with what that changes (`helmtemplate`'s *Rendered objects*).
An integer the object holds as one is written with its own digits, here as everywhere
(`9007199254740993` as `9007199254740993`; `helmtemplate`'s *Rendered objects*). In an
unstructured object an integer an int64 cannot hold is a float from the decode on, and is
written as that float.

### Validation

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
the list is refused (**breaking** for a source that holds
one; see *Image volumes* under **pod**). The error names the object and the field (`manifest source: object
Deployment "demo/web": spec.template.spec.containers[0] "app": …`). A `crd` source
holds only CustomResourceDefinitions, none of which the check reads, so `crd` builds as
before.

A core Secret a `manifests` source yields, `inline` or fetched, is refused under a policy
that forbids explicit secrets (`oam.ExplicitSecretPolicy`), as the `secret` trait and a
`passthrough` Secret are: `manifest source: object Secret
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
a `v1` `List`, a typed list (`DeploymentList`) and a list of
a kind the scheme does not register (a kind ending in `List` that states `items`),
whose items of a registered kind are their Go types and are checked for real, a list
among them opened in turn. The list handling is the one `helmtemplate`'s *Rendered
objects* describes, with the same errors under `manifest source: parse manifests: …`:
a label or an annotation on a list's own metadata, a `Kind`, `apiversion` or `Items`
key, a `null` item, and an object of a kind that is no list with a top-level `items`
array, which is refused with a policy or with none and is no policy violation.
**Breaking**: the table under `helmtemplate` lists what built
before and is refused now.

**Behaviour change:** previously the objects of a `manifests` source reached the output unchecked. A document that relied on that no longer builds when its
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

### Example

- `crd`: No example yet.
- `manifests`: No example yet.
