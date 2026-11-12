---
title: "Kinds: networking"
weight: 4
---

Each kind below has the same sections: Properties, Outputs, Validation and
Example. Its implementation, history and tests are on the
[contributor page](../../contributing/oam-reference/kinds-networking.md).

## ingress

`ingress` is the kind-named projection of a networking.k8s.io/v1 Ingress, on the
recipe of `resourcequota`: one schema key per json field of
`networkingv1.IngressSpec` (`ingressClassName`, `defaultBackend`, `tls`,
`rules`), the property map decoded strictly into that type under the null
contract, two spellings of one field refused, and
`TestCoreKindSchemas_CoverSpec` holding the schema to the linked type.

### Properties

No field is required by the decode and none is filled: an Ingress with neither a
`defaultBackend` nor a rule, a path without a `pathType` and the API's other
value rules are left to the API server.

**It is an authored object, not the `ingress` trait**, although the two share
the type name. The trait attaches to a component and routes to that component's
Service; this kind is a component of its own, and a backend is a Service
reference carried as written. Three things the trait has do not reach it:
- **No NetworkPolicy allow rule.** The NetworkPolicy synthesis opens a backend's
  port to the ingress controller from what a routing trait reports (the traffic
  sources capability rendering gives it, and the component it targets). This
  kind reports neither, so the synthesis reads nothing from it and allows
  nothing for it, whatever Service it names. An author who wants the allow rule
  puts the `expose` or `ingress` trait on the backend component, or authors the
  NetworkPolicy (the `networkpolicy` trait or kind).
- **No hostname constraint.** The platform's `allowedHostnameWildcard`, which
  the trait holds every hostname to, is a trait rendering input. A host authored
  here is not checked against it.
- **No environment policy.** `ApplyPolicy` is a no-op: the policy has no rule
  for an Ingress, and its capability lists (`AllowedCapabilities`,
  `ForbiddenCapabilities`, `RequiredCapabilities`) gate trait types, so a policy
  that forbids the `ingress` trait does not refuse an `ingress` component.
  `passthrough`, `manifests` and template delivery emit an Ingress under the
  same terms. A consumer that restricts routing restricts the component types it
  registers.

### Outputs

It emits one Ingress named after the component in the build namespace, with the
authored spec and nothing else.

**The name** is the component's, or its `objectName` (["The object
name"](conventions.md#the-object-name-objectname) below). An `ingress` trait
names its own Ingress (`<component>-ingress`, or its `name`); a component and a
trait whose Ingress would carry one name are refused, with both named.

**Labels and annotations** are the `labels` and `annotations` properties, a
controller's `nginx.ingress.kubernetes.io/…` annotations among them.

### Validation

A null list element (`rules: [null]`, a null path or TLS entry) is refused by
its path.

**Not covered:** its `status`, which the controller writes.

### Example

The component in `pkg/cmd/kurel/testdata/ingress-component/app.yaml`, built
against the `cluster.yaml` beside it, gives the `expected.yaml` beside it. The
fixture test compares the two.

## httproute

`httproute` is the kind-named projection of a gateway.networking.k8s.io/v1
HTTPRoute, on the same recipe as `ingress`: one schema key per json field of
`gatewayv1.HTTPRouteSpec`, those of the `CommonRouteSpec` it inlines included
(`parentRefs`, `useDefaultGateways`, `hostnames`, `rules`), decoded strictly
into that type.

### Properties

No top-level field is required and none is filled; the API's value rules (a
filter's `type` matching the member set, a rule's `matches` and `backendRefs`
limits) are left to the API server.

**Required** is a field the HTTPRoute CRD requires that the Go type writes
whether or not it was authored, under a parent that is authored, by the rule of
the Gateway API's other kinds: the `name` of a parent and of a backend
(`parentRefs[0].name: required (the Gateway API requires the name of what this
refers to)`), a filter's `type`, the `name` and `value` of a header or query
parameter match and of a header modifier, a mirror's `backendRef`, the `name` of
a header session persistence's `header`, 45 paths at v1.7.0. The list is
generated from the CRD (see ["Required fields a kind writes unauthored and does
not
refuse"](conventions.md#required-fields-a-kind-writes-unauthored-and-does-not-refuse)),
and the refusal runs after the decode's own checks. Each says the generic
sentence of its class: the kind kept no list by hand before.
`useDefaultGateways` is a field of the Gateway API's experimental channel: the
standard channel's HTTPRoute CRD does not hold it, so a cluster on that channel
does not keep it.

**A rule's `sessionPersistence` changed shape at Gateway API v1.7.0**
(**breaking**): `sessionName` is gone and `cookieConfig` is now `cookie`, so a
document that authors either key is refused by the strict decode. The cookie's
name moves to `cookie.name`, with `type: Cookie` (the API's default); a header
session is `type: Header` with `header.name`, which is then required (above).
The API requires the object of the `type` and refuses the other, so a
`sessionPersistence` that authors neither is a Cookie one without its `cookie`
and is refused by the API server (`cookie: {}` asks for nothing more); the kind
leaves those rules to it. Unset, `cookie.path` is `/` by the API's
documentation.

**It is an authored object, not the `httproute` trait**, although the two share
the type name. The trait attaches to a component and routes to that component's
Service; this kind is a component of its own, and a backendRef is a reference
carried as written, its `namespace` included. Three things the trait has do not
reach it:
- **No NetworkPolicy allow rule.** As for `ingress`: the NetworkPolicy synthesis
  reads a routing trait's traffic sources and target component, which this kind
  does not report, so it allows nothing for the route's backends. An author who
  wants the allow rule puts the `expose` or `httproute` trait on the backend
  component, or authors the NetworkPolicy.
- **No parent from a capability.** The trait builds `parentRefs` from the
  Gateway its capability rendering names when the author writes none. Here
  `parentRefs` is what the author wrote; unwritten, the route has no parent.
- **No environment policy.** `ApplyPolicy` is a no-op, and the policy's
  capability lists gate trait types, so a policy that forbids the `httproute`
  trait does not refuse an `httproute` component. `passthrough`, `manifests` and
  template delivery emit an HTTPRoute under the same terms. A consumer that
  restricts routing restricts the component types it registers.

### Outputs

It emits one HTTPRoute named after the component in the build namespace, with
the authored spec and nothing else.

**The name** is the component's, or its `objectName`. An `httproute` trait names
its own HTTPRoute (`<component>-httproute`, or its `name`); a component and a
trait whose HTTPRoute would carry one name are refused, with both named.

**Labels and annotations** are the `labels` and `annotations` properties.

### Validation

No type under `HTTPRouteSpec` unmarshals itself, so an unknown key is refused at
every depth.

A null list element (`rules: [null]`, a null parent or backendRef) is refused by
its path.

**Not covered:** its `status`, which the Gateway controller writes.

### Example

The component in `pkg/cmd/kurel/testdata/httproute-component/app.yaml`, built
against the `cluster.yaml` beside it, gives the `expected.yaml` beside it. The
fixture test compares the two.

## grpcroute

`grpcroute` is the kind-named projection of a gateway.networking.k8s.io/v1
GRPCRoute, on the `httproute` kind's recipe and not on `policyFreeKind` as the
TCP, UDP and TLS routes are: one schema key per json field of
`gatewayv1.GRPCRouteSpec` (`parentRefs`, `useDefaultGateways`, `hostnames`,
`rules`), decoded strictly into that type, and one GRPCRoute emitted in the
build namespace, named after the component unless `objectName` names it.

### Properties

The API requires no rule, backend or hostname of a GRPCRoute, as of an
HTTPRoute, so the decode requires none and fills none; the API's value rules are
left to the API server.

Parent and backend references are carried as written, their `namespace`
included.

As for `httproute`, no NetworkPolicy allow rule is synthesized for its backends,
no parent comes from a capability, and `ApplyPolicy` is a no-op.

A field the GRPCRoute CRD requires that the type writes unauthored is refused
under an authored parent, as on an `httproute` (`rules[0].backendRefs[0].name:
required (the Gateway API requires the name of what this refers to)`), 35 paths
at v1.7.0; the list is generated (see [Required fields a kind writes unauthored
and does not
refuse](conventions.md#required-fields-a-kind-writes-unauthored-and-does-not-refuse)).

A rule's `sessionPersistence` changed shape at v1.7.0 as on an `httproute`
(**breaking**): `sessionName` and `cookieConfig` are refused, and `cookie.name`
or `header.name` takes their place.

### Outputs

**Labels and annotations** are the `labels` and `annotations` properties.

### Validation

A null list element is refused by its path.

**Not covered:** its `status`, which the Gateway controller writes.

### Example

The component in `pkg/cmd/kurel/testdata/grpcroute-component/app.yaml`, built
against the `cluster.yaml` beside it, gives the `expected.yaml` beside it. The
fixture test compares the two.

## networkpolicy

`networkpolicy` is the kind-named projection of a networking.k8s.io/v1
NetworkPolicy, on the same recipe as `ingress`: one schema key per json field of
`networkingv1.NetworkPolicySpec` (`podSelector`, `ingress`, `egress`,
`policyTypes`), decoded strictly into that type.

### Properties

No top-level field is required and none is filled.

**The spec reads as the API reads it, which is not how the `networkpolicy` trait
reads the same keys.** The two share the type name; the trait attaches to a
component and scopes the policy to that component's pods, and this kind is a
component of its own that scopes nothing:
- **`podSelector` is not defaulted.** The trait has no such property: it always
  selects its component's pods. Here an unwritten `podSelector`, like `{}`, is
  the empty selector: **the policy applies to every pod of the namespace**. An
  author moving a policy from the trait to the kind writes the selector out.
- **`policyTypes` is not derived.** The trait has no such property either: it
  lists a direction when that direction's key is present, so its `egress: []`
  denies all egress. Here `policyTypes` is what the author wrote; unwritten, the
  API server derives it: `Ingress` always, `Egress` only when `egress` holds a
  rule. So on the kind `egress: []` alone isolates no egress; a policy that
  denies all egress writes `policyTypes: [Egress]`. An authored `policyTypes:
  []` is refused: the type omits it, and the API server would derive it as above
  (`policyTypes: [] cannot be carried by the Kubernetes API types (…)`).
- **An empty rule allows everything in its direction.** `ingress: [{}]` is the
  API's allow-all, and is carried when written. It is never the result of a
  null: a null rule, peer or port (`ingress: [null]`, `from: [null]`) is refused
  by its path, since decoded it would be the empty, allow-all one. An empty
  `ingress` or `egress` list is carried as an unwritten one, which the API reads
  the same way.
- **A selector picks pods by label.** A peer's `podSelector` is not a component
  reference and nothing resolves it; the label a workload component's pods carry
  is described under ["The `app` label"](conventions.md#the-app-label).
- **No environment policy.** `ApplyPolicy` is a no-op, and the policy's
  capability lists gate trait types, so a policy that forbids the
  `networkpolicy` trait does not refuse a `networkpolicy` component.
  `passthrough`, `manifests` and template delivery emit a NetworkPolicy under
  the same terms. A consumer that restricts network policy restricts the
  component types it registers.

The transform's NetworkPolicy synthesis does not read this kind: it neither
counts an authored policy as covering a component nor merges into it, and the
policies it emits (`{comp}-allow-ingress-traffic` and the others) are additive
beside it.

### Outputs

It emits one NetworkPolicy named after the component in the build namespace,
with the authored spec and nothing else.

**The name** is the component's, or its `objectName`; a component whose policy
would carry the name of a `networkpolicy` trait's (`<component>-allow`, or its
`name`) or of a synthesized one is refused, with both named.

The NetworkPolicy's own labels and annotations are the `labels` and
`annotations` properties.

### Validation

A match expression of `podSelector`, or of a peer's `podSelector` or
`namespaceSelector`, is refused without its `key` or `operator`, with an
operator that is none of the four, or with `values` that do not go with the
operator (see ["A label selector's match
expressions"](conventions.md#a-label-selectors-match-expressions)); the config
is exported, so `Generate` repeats what the typed spec can show.

The API's other value rules (a peer naming at least one of `podSelector`,
`namespaceSelector` and `ipBlock`, an `ipBlock` beside no selector, a valid
CIDR, a label key's syntax) are left to the API server.

### Example

The component in `pkg/cmd/kurel/testdata/networkpolicy-component/app.yaml`,
built against the `cluster.yaml` beside it, gives the `expected.yaml` beside it.
The fixture test compares the two.

## cilium-networkpolicy

`cilium-networkpolicy` is the kind-named projection of a cilium.io/v2
CiliumNetworkPolicy. The object has no spec type of its own: it holds one rule
under `spec`, a list of rules under `specs`, or both, each a Cilium `api.Rule`.

### Properties

The component's properties are those two fields, decoded strictly into that type
under the null contract, and `TestCoreKindSchemas_CoverSpec` holds the two keys
to the linked `CiliumNetworkPolicy` type.

The whole rule is authorable: `endpointSelector`, `ingress`, `ingressDeny`,
`egress`, `egressDeny`, `labels`, `enableDefaultDeny`, `description`, `log`.

**Required.** Inside a rule the CRD requires 59 fields that the Cilium type
writes whether or not they were authored, so that the object would not show the
omission: a match expression without its `operator` was emitted as `operator:
""`, an `authentication: {}` as `mode: ""`, a `terminatingTLS: {}` as `secret:
null`, each of which the API server refuses. Each is refused where its parent is
authored, by its path (`spec.ingress[0].authentication.mode: required (…)`):
- the `key` and `operator` of a match expression, in every selector of a rule
  (`endpointSelector`, `fromEndpoints`, `toEndpoints`, `fromNodes`, `toNodes`, a
  CIDR entry's `cidrGroupSelector`, a `k8sServiceSelector`'s `selector`), under
  the deny lists too;
- a `k8sServiceSelector`'s `selector`, the `key` of a rule label and the `type`
  of an `icmps` field;
- under an allow list only: an `authentication`'s `mode`; a port's `listener`
  `name`, its `envoyConfig` and that configuration's `name`; the `secret` of a
  port's `terminatingTLS` and `originatingTLS` and that Secret's `name`; an HTTP
  header match's `name`, and the `name` of its `secret` where one is authored.

Two more are optional to the API and required here: a listener's `priority` and
the `kind` of its `envoyConfig`. The type writes an unauthored one as `0` and as
the empty string, and the API refuses both (a priority is 1 to 100, a kind is
`CiliumEnvoyConfig` or `CiliumClusterwideEnvoyConfig`), so a listener that
leaves one out cannot be emitted. The CRD names a `nodeSelector`'s match
expressions as well, and so does the list: an incomplete one is refused by this
rule, a complete one by the rule above. A rule label without its `key` is
refused earlier, by Cilium's own decoding, as an `icmps` field without its
`type` is (below). An authored empty value in a required field is not checked:
it is the API server's to refuse.

**The list is kept by hand, not generated**, as the
`cilium-clusterwidenetworkpolicy` kind's is, while the other Cilium kinds' lists
are generated: it is one rule's list, shared with the trait; it is read under
two prefixes, `spec` and `specs`, which the generated lists' API server harness
does not take; two of its fields (`labels[].key`, `icmps[].fields[].type`) the
decode refuses first, which that harness's both-ways rows do not handle, though
the derivation lists both; and it holds fields no generated list has a place for
(the listener's `priority` and Envoy configuration `kind`, which the API does
not require). It refuses every field the derivation finds all the same: neither
kind has a field in the pin (see ["Required fields a kind writes unauthored and
does not
refuse"](conventions.md#required-fields-a-kind-writes-unauthored-and-does-not-refuse)).

**It is an authored object, not the `cilium-networkpolicy` trait**, although the
two share the type name. The trait attaches to a component and publishes one
rule's `name`, `endpointSelector`, `ingress` and `egress`; this kind is a
component of its own, named after the component or by its `objectName`, and
publishes the whole rule and `specs`. Neither fills a selector.

### Outputs

It emits one CiliumNetworkPolicy named after the component in the build
namespace, with the authored rules and nothing else.

A component and a trait whose policy would carry one name (the trait's `name`)
are refused, with both named.

The object's own labels and annotations are the top-level `labels` and
`annotations` properties; a `labels` under `spec` or a `specs` entry is the
rule's own field.

### Validation

**Refused at build, because Cilium rejects the policy.** Its CRD schema refuses
some of these at admission (a rule with no selector, or without any of the four
rule keys); the rest the API server stores and the agent rejects when it reads
the object, so that nothing enforces it. Launcher refuses all of them instead of
emitting the object:
- no rule at all: neither `spec` nor an entry in `specs`. An empty `specs` holds
  none; beside a `spec` it is accepted and not emitted;
- a rule with no `endpointSelector`. None is filled in: `{}` selects every
  endpoint of the namespace and is carried as written, and a null or absent one
  is refused;
- a rule with a `nodeSelector`, which belongs to a
  CiliumClusterwideNetworkPolicy and which Cilium rejects in a namespaced
  policy;
- a rule with no entry in any of `ingress`, `ingressDeny`, `egress` and
  `egressDeny`. A deny list counts as much as an allow list; a null or empty
  list holds no entry.

These are the checks Cilium's own reader makes that need no agent configuration;
the code names the Cilium functions they mirror. The rest of a rule's value
rules (a port's range, an entity's name, which peers may be combined) are left
to Cilium. A null list element (`specs: [null]`, a null ingress entry or peer)
is refused by its path.

**An unknown key inside a selector is refused**, by its path
(`spec.ingress[0].fromEndpoints[1].matchLabel: unknown field`). Cilium's
endpoint selector unmarshals itself and drops a key it does not know, which the
strict decode cannot see: `endpointSelector: {matchLabel: {...}}` would be the
empty selector, which selects every endpoint of the namespace, and a peer
misspelt the same way would match every endpoint. The check covers every
position that holds a selector (`endpointSelector`, `fromEndpoints`,
`toEndpoints`, `fromNodes`, `toNodes`, a CIDR entry's `cidrGroupSelector`, under
the deny lists too) and the two other types under a rule that unmarshal
themselves: an `icmps` field (`family`, `type`) and an entry of `labels` in its
object form (`key`, `value`, `source`).

**An `icmps` field needs its `type`.** Cilium's own decoding of the field panics
when `type` is absent or null; the component is refused with an error that names
the panic (`… the decoder of … panicked on this value: …`) and not the field's
position, so look for an `icmps` field without a `type`.

**No environment policy applies:** `ApplyPolicy` is a no-op, and the policy's
capability lists gate trait types, so a policy that forbids the
`cilium-networkpolicy` trait does not refuse a `cilium-networkpolicy` component.
`passthrough`, `manifests` and template delivery emit a CiliumNetworkPolicy
under the same terms. A consumer that restricts network policy restricts the
component types it registers. **No literal secret is checked:** an HTTP header
match's `value` is written into the object as authored and is not refused, since
a header value cannot be told from a credential; a header match's `secret`
refers to a Secret by name and keeps the value out of the document.

**Not covered:** its `status`, which the Cilium agent writes.

### Example

The component in
`pkg/cmd/kurel/testdata/cilium-networkpolicy-component/app.yaml`, built against
the `cluster.yaml` beside it, gives the `expected.yaml` beside it. The fixture
test compares the two.

## servicecidr

`servicecidr` is the kind-named projection of a cluster-scoped
`networking.k8s.io/v1` ServiceCIDR: a range the API server assigns Service
cluster IPs from, beside the one it was started with.

### Properties

- **Authored:** the one field of `ServiceCIDRSpec`, `cidrs`, a list of IP blocks
  in CIDR notation, decoded strictly. **Required:** at least one block (`cidrs:
  required …`); the API server refuses a ServiceCIDR without one. Every other
  value rule is left to the API server: that a block is a valid CIDR, that there
  are at most two, and that two are of different IP families.

### Outputs

It is built on `policyFreeKind`, as the six kinds of the **storageclass** entry
are, and what that entry says of them holds here: one object, named after the
component unless `objectName` names it, with no namespace, declared
cluster-scoped, holding exactly what was authored, the same under every policy
and under none.

**Labels and annotations** are the `labels` and `annotations` properties.

### Validation

- **Not covered.** The API server refuses a change to a block once the object
  exists; a ServiceCIDR of one block may only gain a second. Launcher does not
  compare a build with the cluster, so a changed block builds here and is
  refused at apply. The object's status is the API server's and is not written.

### Example

The component in `pkg/cmd/kurel/testdata/servicecidr-component/app.yaml`, built
against the `cluster.yaml` beside it, gives the `expected.yaml` beside it. The
fixture test compares the two.

## poddisruptionbudget

`poddisruptionbudget` is the kind-named projection of a `policy/v1`
PodDisruptionBudget, built on `policyFreeKind`.

### Properties

- **Authored:** the four fields of `PodDisruptionBudgetSpec`, decoded strictly:
  `minAvailable` and `maxUnavailable` (each a count or a percentage string such
  as `"50%"`), `selector` (a label selector) and `unhealthyPodEvictionPolicy`. A
  count is emitted as a number and a percentage as a string, and an authored `0`
  is kept (`maxUnavailable: 0` allows no voluntary eviction).
- **Required:** no top-level field. The API server accepts a budget with an
  empty spec. Inside `selector`, a match expression is refused without its `key`
  or `operator`, with an operator that is none of the four, or with `values`
  that do not go with the operator (see ["A label selector's match
  expressions"](conventions.md#a-label-selectors-match-expressions)). The API's
  other value rules are left to it, the one that `minAvailable` and
  `maxUnavailable` exclude each other included: a component that authors both
  builds here and is refused at apply.
- **The selector is the author's.** Launcher points it at no component: an
  unauthored `selector` selects no pod, and `selector: {}` selects every pod of
  the namespace. To cover a workload component, select its `app` label
  (`matchLabels: {app: <component>}`), which holds the component name up to 63
  characters and its label projection beyond.
- **Beside the `scaler` trait.** `scaler` with `enablePDB: true` derives a
  budget for its own workload, selector included, and refuses one that would
  block every eviction. This kind is the authored object and applies none of
  that: it is for a budget the trait does not express (the trait always writes
  `minAvailable: 50%`; here the count or percentage is the author's, as are
  `maxUnavailable`, an eviction policy and pods launcher does not own).

### Outputs

It emits that one object in the build namespace, named after the component
unless `objectName` names it, holding exactly what was authored: the handler
adds no label, no annotation and no default of its own. The handler declares its
object as namespaced, so the object name is claimed in the object's namespace.

- **Labels and annotations** are the `labels` and `annotations` properties.

### Validation

- **Policy.** No field of the spec is one an `oam.Policy` method speaks to, so
  `ApplyPolicy` enforces nothing and fills nothing.
- **Not covered.** Whether the selector matches any pod. The object's status is
  the disruption controller's and is not written.

### Example

The component in
`pkg/cmd/kurel/testdata/poddisruptionbudget-component/app.yaml`, built against
the `cluster.yaml` beside it, gives the `expected.yaml` beside it. The fixture
test compares the two.

## endpointslice

`endpointslice` is the kind-named projection of a `discovery.k8s.io/v1`
EndpointSlice, built on `policyFreeKind`.

### Properties

- **Authored:** an EndpointSlice has no spec, so the properties are the object's
  own top-level fields, decoded strictly at every depth: `addressType`,
  `endpoints` and `ports`. The object's `kind`, `apiVersion` and `metadata` are
  launcher's and are refused under any spelling. An authored `false` is kept
  (`conditions: {ready: false}`), and an endpoint's `deprecatedTopology` is
  written as authored, though the type documents that the v1 API ignores a write
  to it.
- **Its Service is a label, and the label is the author's literal.** A slice
  belongs to a Service only through its `kubernetes.io/service-name` label,
  authored under `labels`. Launcher derives the label from nothing and points it
  at no component: it does not follow a `service` component's `objectName`, so
  write the name the Service object takes. A slice without the label builds and
  belongs to no Service. A `service` component of a type other than
  `ExternalName` always carries a selector (the kind refuses an empty one, and
  an `ExternalName` Service has no endpoints), so a Service whose endpoints are
  all written by hand comes from elsewhere: a `manifests` component, or outside
  the document.
- **Required** are the fields the API's source marks required and the Go type
  writes whether or not they were authored, so that the built object would not
  show the omission: `addressType` (`addressType: required …`), an endpoint's
  `addresses` (`endpoints[0].addresses: required …`), and the `name` of an entry
  of an endpoint's `hints.forZones` or `hints.forNodes`.
  `TestBuiltinMarkerKinds_RequiredMatchMarkers` derives the list from the
  `+required` and `+optional` markers of the linked `k8s.io/api` module and
  fails on a dependency bump that changes it. The API marks no field of the kind
  required that the type leaves out when unauthored; the same test fails if one
  appears. The check is one of presence: an authored empty value (`addressType:
  ""`, `addresses: []`) builds and is the API server's to refuse.

### Outputs

It emits that one object in the build namespace, named after the component
unless `objectName` names it, holding exactly what was authored: the handler
adds no label, no annotation and no default of its own. The handler declares its
object as namespaced, so the object name is claimed in the object's namespace.

- **Emitted empty.** The type writes three fields the API does not require
  whether or not they were authored. A slice that authors no `endpoints` or no
  `ports` carries `endpoints: null` or `ports: null`, an authored empty list is
  written as `[]`, and an endpoint that authors no condition carries
  `conditions: {}`. A port that authors no `protocol` or no `name` is written
  without it, and the API server fills `TCP` and the empty name.
- **Labels and annotations** are the `labels` and `annotations` properties.

### Validation

- **Not artifact sources.** The addresses of an endpoint, FQDNs included, are
  where traffic goes, not an artifact source, and none is held to the policy's
  allowed registries.
- **Policy.** No field of the object is one an `oam.Policy` method speaks to, so
  `ApplyPolicy` enforces nothing and fills nothing.
- **Not covered.** Every rule on the form of a value is left to the API server:
  that an address is of the slice's `addressType`, the limits the type documents
  on how many endpoints, addresses, ports and hints a slice holds, and the names
  of ports. The type documents `addressType` as immutable. Launcher does not
  compare a build with the cluster, so a changed one builds here and is refused
  at apply. Whether a Service of the labelled name exists is not checked.

### Example

The component in `pkg/cmd/kurel/testdata/endpointslice-component/app.yaml`,
built against the `cluster.yaml` beside it, gives the `expected.yaml` beside it.
The fixture test compares the two.

## cilium-bgpadvertisement, cilium-bgpclusterconfig, cilium-bgpnodeconfigoverride, cilium-bgppeerconfig

`cilium-bgpadvertisement`, `cilium-bgpclusterconfig`,
`cilium-bgpnodeconfigoverride`, `cilium-bgppeerconfig` are the kind-named
projections of four objects of Cilium's BGP control plane, in its `cilium.io/v2`
API: a CiliumBGPAdvertisement, a CiliumBGPClusterConfig, a
CiliumBGPNodeConfigOverride and a CiliumBGPPeerConfig. The type names carry the
`cilium-` prefix: other products have a BGPAdvertisement and a BGP peer of their
own. The fifth object of that API, the CiliumBGPNodeConfig, is generated by the
Cilium operator and has no component.

### Properties

**Authored.** The properties are the top-level json fields of the spec type,
decoded strictly at every depth: an unknown key is refused wherever it sits (an
advertisement, an instance, a peer, a family).
- `cilium-bgpadvertisement` (`CiliumBGPAdvertisementSpec`): `advertisements`,
  each with its `advertisementType` (`PodCIDR`, `CiliumPodIPPool`, `Service` or
  `Interface`), `service` (`addresses`, `aggregationLengthIPv4`,
  `aggregationLengthIPv6`), `interface` (`name`), `selector` and `attributes`
  (`communities`, `localPreference`).
- `cilium-bgpclusterconfig` (`CiliumBGPClusterConfigSpec`): `nodeSelector` and
  `bgpInstances`, each with its `name`, `localASN`, `localPort` and `peers`; a
  peer has a `name`, a `peerAddress` or an `autoDiscovery`, a `peerASN` and a
  `peerConfigRef`.
- `cilium-bgpnodeconfigoverride` (`CiliumBGPNodeConfigOverrideSpec`):
  `bgpInstances`, each with its `name`, `routerID`, `localPort`, `localASN` and
  `peers`; a peer has a `name`, a `localAddress` and a `localPort`.
- `cilium-bgppeerconfig` (`CiliumBGPPeerConfigSpec`): `transport` (`peerPort`,
  `sourceInterface`), `timers` (`connectRetryTimeSeconds`, `holdTimeSeconds`,
  `keepAliveTimeSeconds`), `authSecretRef`, `gracefulRestart` (`enabled`,
  `restartTimeSeconds`), `ebgpMultihop` and `families`, each with its `afi`, its
  `safi` and the `advertisements` it sends. A peer configuration that authors
  nothing builds, and carries nothing.
- An authored `0` or `false` is kept: every number these specs hold is a pointer
  in the Go type (`peerASN: 0`, which accepts any ASN the peer opens with;
  `aggregationLengthIPv4: 0`), and `gracefulRestart.enabled` is always written.
  **No default is filled.** The API's defaults (a peer's `peerASN`, the three
  `timers`, `transport.peerPort`, `ebgpMultihop`,
  `gracefulRestart.restartTimeSeconds`) are the API server's to fill where the
  field was not authored; `TestCiliumBGPKinds_DefaultsSitOnPointers` holds each
  to a pointer field, from the CRDs of the linked module.

**Required** follows the rule of the Prometheus operator's kinds: a field the
API requires that the Go type writes whether or not it was authored. Each must
be authored (`bgpInstances[0].peers[1].name: required (…)`):
- a `cilium-bgpadvertisement`'s `advertisements` and an entry's
  `advertisementType`;
- a `cilium-bgpclusterconfig`'s `bgpInstances`, an instance's `name`, a peer's
  `name`, an `autoDiscovery`'s `mode`, the `addressFamily` of its
  `defaultGateway`, and the `name` of a `peerConfigRef`;
- a `cilium-bgpnodeconfigoverride`'s `bgpInstances`, an instance's `name` and a
  peer's `name`;
- a `cilium-bgppeerconfig` family's `afi` and `safi`, and the `enabled` of a
  `gracefulRestart`, which has no default: unauthored, the type would write
  `enabled: false`;
- the `key` and the `operator` of a match expression, in every selector of the
  four: `nodeSelector`, an advertisement's `selector`, a family's
  `advertisements`. The selector is Cilium's own type and its fields are in the
  CRD, so these are derived with the rest; on the Prometheus operator's kinds,
  whose selector is the Kubernetes one, they are not.

These lists are generated from the fields the linked module's `v2` CRDs require
and the types write unauthored (see ["Required fields a kind writes unauthored
and does not
refuse"](conventions.md#required-fields-a-kind-writes-unauthored-and-does-not-refuse)),
so a dependency bump that adds, drops or moves one fails until they are
regenerated; `TestCiliumBGPKinds_RequiredMatchCRD` holds them to those CRDs as
well.

**Two required fields the type leaves out when they are empty are refused too,**
on an advertisement: the `name` of an `interface`
(`advertisements[0].interface.name: required (…)`) and the `addresses` of a
`service`, an authored `addresses: []` included
(`advertisements[0].service.addresses: required (…)`). The object would show the
omission (`service: {}`) and the API server refuse it.

### Outputs

**All four are cluster-scoped**: the object carries no namespace, whatever
namespace the application is built for, and its name is claimed cluster-wide.

**Labels and annotations** are the `labels` and `annotations` properties. **A
peer configuration selects advertisements by label.** A family's
`advertisements` is a label query over CiliumBGPAdvertisement objects: write the
labels it names under the `labels` of the `cilium-bgpadvertisement`. The object
also carries the component label, whose value is the component's (see "Component
label and ownership" in the OAM model). `nodeSelector` and an advertisement's
`selector` query nodes, pools and Services, whose labels are their owners'.

**An override takes effect by its name.** A CiliumBGPNodeConfigOverride
overrides the CiliumBGPNodeConfig of the same name, the per-node object the
Cilium operator generates; Cilium's API says the two names must match exactly.
Name the component so, or set `objectName`. Launcher does not know the cluster's
objects and checks neither.

### Validation

**No capability is required, and none gates these kinds.** Launcher does not ask
whether the cluster runs Cilium or has its BGP control plane enabled: where the
CRDs are not installed the component builds, and the object is refused at apply.
Whoever may author a component may author these, and with them what the
cluster's nodes announce to its routers. A policy can keep the kinds out of a
build through the object kind policy (`oam.ObjectKindPolicy`).

**The CRDs' expression rules.** A rule is checked where it is one comparison of
fields, as authored or as the CRD defaults them, and the refusal can name what
it compared; every rule the four CRDs declare is classified in
`TestCiliumBGPKinds_ExpressionRules`, which fails on one that is added or
reworded. **Checked:**
- the five rules on an advertisement, each the entry's `advertisementType`
  against the presence of one sibling: `service` is required with `Service` and
  refused with another type, `interface` is required with `Interface` and
  refused with another type, and `selector` is refused with `PodCIDR`
  (`advertisements[1].service: not allowed with advertisementType
  "CiliumPodIPPool", only with "Service"`). An authored `{}` is a present field
  and an authored `null` an absent one, as the API server reads them;
- a peer configuration's `timers.keepAliveTimeSeconds` no larger than its
  `timers.holdTimeSeconds` (`timers.keepAliveTimeSeconds: 90 is larger than
  timers.holdTimeSeconds (30)`). The API server evaluates the rule after it has
  filled the defaults, so **with one of the two authored it compares that one
  with the default of the other, and so does the kind**: a keepalive of 30 and a
  hold time of 90, the linked CRD's, which the kind states and the test holds to
  that CRD. `keepAliveTimeSeconds: 100` alone is refused
  (`timers.keepAliveTimeSeconds: 100 is larger than the hold time the API fills
  where timers.holdTimeSeconds is not set (90)`), and so is `holdTimeSeconds:
  20` alone (`timers.holdTimeSeconds: 20 is smaller than the keepalive time the
  API fills where timers.keepAliveTimeSeconds is not set (30)`). A cluster whose
  installed CRD fills other defaults than the linked one is not known here.

**Not checked**, and the API server's to refuse:
- an authored empty value in a required field (`bgpInstances: []`, an instance's
  `name: ""`). It is a value. The two fields above are the exception: the type
  leaves an empty one out;
- every other value rule of the API: the enumerations (`advertisementType`,
  `afi`, `safi`, a service address type, a well-known community), the ranges of
  an ASN, a port, a timer, an aggregation length and `ebgpMultihop`, the forms
  of an address, a router ID and a community, the number of instances (one to
  sixteen in a cluster configuration, at least one in an override), an instance
  name unique in its list and a peer name unique in its instance;
- what Cilium itself decides of an object the API server has admitted.

**Policy.** No field of these specs is one an `oam.Policy` method speaks to, so
`ApplyPolicy` enforces nothing and fills nothing, and each builds the same under
every policy and under none.
- **Hosts are not checked.** An address these objects name is a BGP peer's or
  the node's own, not an artifact source, and none is held to the policy's
  allowed registries: a peer's `peerAddress` in a `cilium-bgpclusterconfig`, and
  a peer's `localAddress` and an instance's `routerID` in a
  `cilium-bgpnodeconfigoverride`.
- **No field holds a literal secret, and none is checked.** `authSecretRef` is
  the name of a Secret Cilium fetches the session's TCP authentication password
  from. The field holds a name and no namespace: where Cilium looks the Secret
  up is its own configuration. Launcher does not emit that Secret for it and
  does not check that one exists.

**Not covered.** The served `v2alpha1` version of these objects: the kinds emit
`v2`. Whether what is selected or referred to exists (a node, a pool, a Service,
an advertisement, a peer configuration, a Secret), and whether the cluster's
Cilium runs its BGP control plane. A `cilium-bgpclusterconfig`'s and a
`cilium-bgppeerconfig`'s status is the operator's and is not written.

### Example

- `cilium-bgpadvertisement`: The component in
  `pkg/cmd/kurel/testdata/cilium-bgpadvertisement-component/app.yaml`, built
  against the `cluster.yaml` beside it, gives the `expected.yaml` beside it. The
  fixture test compares the two.
- `cilium-bgpclusterconfig`: The component in
  `pkg/cmd/kurel/testdata/cilium-bgpclusterconfig-component/app.yaml`, built
  against the `cluster.yaml` beside it, gives the `expected.yaml` beside it. The
  fixture test compares the two.
- `cilium-bgpnodeconfigoverride`: The component in
  `pkg/cmd/kurel/testdata/cilium-bgpnodeconfigoverride-component/app.yaml`,
  built against the `cluster.yaml` beside it, gives the `expected.yaml` beside
  it. The fixture test compares the two.
- `cilium-bgppeerconfig`: The component in
  `pkg/cmd/kurel/testdata/cilium-bgppeerconfig-component/app.yaml`, built
  against the `cluster.yaml` beside it, gives the `expected.yaml` beside it. The
  fixture test compares the two.

## cilium-cidrgroup, cilium-loadbalancerippool, cilium-egressgatewaypolicy, cilium-localredirectpolicy, cilium-nodeconfig

`cilium-cidrgroup`, `cilium-loadbalancerippool`, `cilium-egressgatewaypolicy`,
`cilium-localredirectpolicy`, `cilium-nodeconfig` are the kind-named projections
of five more objects of Cilium's `cilium.io/v2` API: a CiliumCIDRGroup, a
CiliumLoadBalancerIPPool, a CiliumEgressGatewayPolicy, a
CiliumLocalRedirectPolicy and a CiliumNodeConfig.

### Properties

**Authored.** The properties are the top-level json fields of the spec type,
decoded strictly at every depth: an unknown key is refused wherever it sits (a
block, a selector, a gateway, a matcher, a port).
- `cilium-cidrgroup` (`CiliumCIDRGroupSpec`): `externalCIDRs`.
- `cilium-loadbalancerippool` (`CiliumLoadBalancerIPPoolSpec`):
  `serviceSelector`, `allowFirstLastIPs` (`Yes` or `No`), `blocks`, each a
  `cidr` or a `start` with an optional `stop`, and `disabled`.
- `cilium-egressgatewaypolicy` (`CiliumEgressGatewayPolicySpec`): `selectors`,
  each with a `namespaceSelector`, a `podSelector` and a `nodeSelector`;
  `destinationCIDRs`; `excludedCIDRs`; `egressGateway` and `egressGateways`, a
  gateway being a `nodeSelector`, an `interface` and an `egressIP`.
- `cilium-localredirectpolicy` (`CiliumLocalRedirectPolicySpec`):
  `redirectFrontend`, an `addressMatcher` (`ip`, `toPorts`) or a
  `serviceMatcher` (`serviceName`, `namespace`, `toPorts`); `redirectBackend`
  (`localEndpointSelector`, `toPorts`); `skipRedirectFromBackend`;
  `description`. A port is a `port`, a `protocol` and a `name`.
- `cilium-nodeconfig` (`CiliumNodeConfigSpec`): `defaults`, a map of
  configuration keys to string values, and `nodeSelector`.
- **No default is filled, and an authored value that is the API's default is not
  written where the Go type cannot hold it.** Three fields of these specs are no
  pointers and are omitted when empty: a pool's `disabled`, a redirect policy's
  `skipRedirectFromBackend` and an egress gateway policy's `egressGateways`. An
  authored `disabled: false`, `skipRedirectFromBackend: false` or
  `egressGateways: []` is left out of the object, and the API server fills the
  same value back: each of the three has that empty value as its default in the
  CRD.

**Required** follows the rule of the BGP kinds: a field the API requires that
the Go type writes whether or not it was authored. Each must be authored
(`egressGateways[1].nodeSelector: required (…)`):
- a `cilium-cidrgroup`'s `externalCIDRs`;
- a `cilium-egressgatewaypolicy`'s `selectors`, `destinationCIDRs` and
  `egressGateway`, and the `nodeSelector` of `egressGateway` and of every entry
  of `egressGateways`;
- a `cilium-localredirectpolicy`'s `redirectFrontend` and `redirectBackend`; an
  `addressMatcher`'s `ip` and `toPorts`; a `serviceMatcher`'s `serviceName` and
  `namespace`; the backend's `localEndpointSelector` and `toPorts`; the `port`
  and the `protocol` of every port, in each of the three lists;
- a `cilium-nodeconfig`'s `defaults` and `nodeSelector`;
- the `key` and the `operator` of a match expression, in every selector of the
  five. A `cilium-loadbalancerippool` requires nothing else.

An authored empty value satisfies the rule: `externalCIDRs: []` is a group that
selects no peer, `nodeSelector: {}` every node and `defaults: {}` no key. These
lists are generated from the fields the linked module's `v2` CRDs require and
the types write unauthored (see ["Required fields a kind writes unauthored and
does not
refuse"](conventions.md#required-fields-a-kind-writes-unauthored-and-does-not-refuse)),
so a dependency bump that adds, drops or moves one fails until they are
regenerated; `TestCiliumPlainKinds_RequiredMatchCRD` holds them to those CRDs as
well.

### Outputs

**The first three are cluster-scoped**: the object carries no namespace,
whatever namespace the application is built for, and its name is claimed
cluster-wide. **The redirect policy and the node configuration are namespaced**
and are written in the build namespace.

**Labels and annotations** are the `labels` and `annotations` properties. **A
CIDR group is referred to by its name, or selected by its labels.** A Cilium
network policy's `cidrGroupRef` names the group: name the component so, or set
`objectName`. Its `cidrGroupSelector` is a label query over groups: write the
labels it names under the `labels` of the `cilium-cidrgroup`. The object also
carries the component label, whose value is the component's (see "Component
label and ownership" in the OAM model). Every other selector in the five specs
queries objects launcher does not write the labels of here (Services, nodes,
namespaces, pods) and is the author's.

### Validation

**No capability is required, and none gates these kinds**, as for the BGP kinds:
where the CRDs are not installed, or the feature the object configures is not
enabled in the cluster's Cilium (egress gateway, local redirect policies, load
balancer address management), the component builds. Whoever may author a
component may author these, and with them the address a workload's traffic
leaves the cluster with, where a node sends traffic meant for an address or a
Service, and the configuration of the Cilium agent on a node. A policy can keep
the kinds out of a build through the object kind policy
(`oam.ObjectKindPolicy`).

**A redirect frontend takes exactly one matcher.** The CRD's schema of
`redirectFrontend` is a choice between `addressMatcher` and `serviceMatcher`,
and the API server refuses neither and both. It is one comparison of two
authored fields, so the kind refuses it too (`redirectFrontend: one of
addressMatcher and serviceMatcher is required`; `redirectFrontend:
addressMatcher and serviceMatcher are both set; the API takes exactly one`).

**The CRDs' expression rules.** The five CRDs declare five, and none is checked;
`TestCiliumPlainKinds_ExpressionRules` lists each with why, and fails on one
that is added or reworded.

**Not checked**, and the API server's to refuse:
- a gateway's `egressIP` being an IP address, the CRD's rule on
  `egressGateway.egressIP` and on an `egressGateways` entry's: the form of one
  field's value, not a comparison of authored fields;
- the three rules that refuse a change of a redirect policy's
  `redirectFrontend`, `redirectBackend` and `skipRedirectFromBackend` once the
  object exists. Each compares the object with the stored one, and a build has
  none: such a change builds here and is refused at apply, until the object is
  deleted and created again;
- a `cilium-nodeconfig`'s `defaults`: no key and no value is read. A key Cilium
  does not know builds;
- an authored empty value in a required field (`selectors: []`, a `serviceName:
  ""`). It is a value;
- every other value rule of the API: the enumerations (`allowFirstLastIPs`, a
  port's `protocol`), the forms of a CIDR, an address, a port and a port name,
  and the number of `egressGateways` (at most 64);
- what Cilium itself decides of an object the API server has admitted: a
  `serviceMatcher` whose `namespace` is not the policy's own, which Cilium's API
  says must match, and two pools whose blocks overlap, which the Cilium operator
  reports in the pool's status.

**Policy.** No field of these specs is one an `oam.Policy` method speaks to, so
`ApplyPolicy` enforces nothing and fills nothing, and each builds the same under
every policy and under none.
- **Hosts are not checked.** No field names a host. An address or a CIDR these
  objects hold is a network's, not an artifact source, and none is held to the
  policy's allowed registries: a group's `externalCIDRs`, a pool's `blocks`, an
  egress policy's `destinationCIDRs`, `excludedCIDRs` and `egressIP`, and a
  redirect frontend's `ip`.
- **No field holds a literal secret or refers to a Secret, and none is
  checked.** A `cilium-nodeconfig`'s `defaults` is free text: a value written
  there is written into the object as authored.

**Not covered.** The served `v2alpha1` version of the CIDR group, of the pool
and of the node configuration: the kinds emit `v2`. Whether what is selected or
referred to exists (a Service, a node, a namespace, a pod, an interface). A
pool's and a redirect policy's status is the operator's and is not written.

### Example

- `cilium-cidrgroup`: The component in
  `pkg/cmd/kurel/testdata/cilium-cidrgroup-component/app.yaml`, built against
  the `cluster.yaml` beside it, gives the `expected.yaml` beside it. The fixture
  test compares the two.
- `cilium-loadbalancerippool`: The component in
  `pkg/cmd/kurel/testdata/cilium-loadbalancerippool-component/app.yaml`, built
  against the `cluster.yaml` beside it, gives the `expected.yaml` beside it. The
  fixture test compares the two.
- `cilium-egressgatewaypolicy`: The component in
  `pkg/cmd/kurel/testdata/cilium-egressgatewaypolicy-component/app.yaml`, built
  against the `cluster.yaml` beside it, gives the `expected.yaml` beside it. The
  fixture test compares the two.
- `cilium-localredirectpolicy`: The component in
  `pkg/cmd/kurel/testdata/cilium-localredirectpolicy-component/app.yaml`, built
  against the `cluster.yaml` beside it, gives the `expected.yaml` beside it. The
  fixture test compares the two.
- `cilium-nodeconfig`: The component in
  `pkg/cmd/kurel/testdata/cilium-nodeconfig-component/app.yaml`, built against
  the `cluster.yaml` beside it, gives the `expected.yaml` beside it. The fixture
  test compares the two.

## cilium-clusterwidenetworkpolicy

`cilium-clusterwidenetworkpolicy` is the kind-named projection of a
cluster-scoped cilium.io/v2 CiliumClusterwideNetworkPolicy. The object holds
what a CiliumNetworkPolicy holds: one rule under `spec`, a list of rules under
`specs`, or both, each a Cilium `api.Rule`.

### Properties

The component's properties are those two fields, decoded as the
`cilium-networkpolicy` kind decodes them: strictly, under the null contract,
with an unknown key inside a selector, an `icmps` field or a rule label refused
by its path (see that entry).

It differs from the namespaced kind in two things: its rules reach every
namespace of the cluster, and a rule selects endpoints or nodes, by
`endpointSelector` or by `nodeSelector`.

**Required.** Inside a rule the CRD requires 59 fields that the Cilium type
writes whether or not they were authored, so that the object would not show the
omission. Each is refused where its parent is authored, by its path
(`spec.ingress[0].toPorts[0].terminatingTLS.secret: required (…)`):
- the `key` and `operator` of a match expression, in every selector of a rule
  (`endpointSelector`, `nodeSelector`, `fromEndpoints`, `toEndpoints`,
  `fromNodes`, `toNodes`, a CIDR entry's `cidrGroupSelector`, a
  `k8sServiceSelector`'s `selector`), under the deny lists too;
- a `k8sServiceSelector`'s `selector`, the `key` of a rule label and the `type`
  of an `icmps` field;
- under an allow list only: an `authentication`'s `mode`; a port's `listener`
  `name`, its `envoyConfig` and that configuration's `name`; the `secret` of a
  port's `terminatingTLS` and `originatingTLS` and that Secret's `name`; an HTTP
  header match's `name`, and the `name` of its `secret` where one is authored.

Two more are optional to the API and required here: a listener's `priority` and
the `kind` of its `envoyConfig`. The type writes an unauthored one as `0` and as
the empty string, and the API refuses both (a priority is 1 to 100, a kind is
`CiliumEnvoyConfig` or `CiliumClusterwideEnvoyConfig`), so a listener that
leaves one out cannot be emitted. An `icmps` field without its `type` and a rule
label without its `key` are refused earlier, by Cilium's own decoding, with the
decode error (for the `type`, one that names a panic and not the field's
position). The list is kept by hand, not generated, for the reasons the
`cilium-networkpolicy` kind gives.

### Outputs

It emits one object, named after the component unless `objectName` names it,
with no namespace, declared cluster-scoped, holding the authored rules and, as
on every kind component, the labels and annotations written under `labels` and
`annotations`.

**What the object carries that was not written.** An `icmps` field's `family` is
left out when it is not authored, and the API fills `IPv4`, the one default the
CRD declares in a rule. An authored `family: ""` is left out as well and reads
the same, to Cilium too. A rule label in its object form without a `source`
carries `source: ""`, which the type writes and the API admits.

### Validation

**Refused at build: what the CRD's schema refuses of the two fields, and nothing
wider.** Three rules of that schema, each one comparison of authored fields:
- no rule at all, neither `spec` nor an entry in `specs` (the object's
  expression rule `has(self.spec) || has(self.specs)`). An empty `specs` is not
  emitted, so it holds none; beside a `spec` it is accepted.
- a rule that authors both `endpointSelector` and `nodeSelector`, or neither:
  the schema takes exactly one. None is filled in. `endpointSelector: {}`
  selects every endpoint of the cluster and `nodeSelector: {}` every node, and
  each is carried as written; a null one is an absent one;
- a rule with no entry in any of `ingress`, `ingressDeny`, `egress` and
  `egressDeny`. A deny list counts as much as an allow list; a null or empty
  list is not emitted and holds no entry.

A null entry of `specs` is refused by its path.

**Not checked.** These are the API server's to refuse, or Cilium's:
- the choices the schema makes deeper in a rule: a CIDR entry (`toCIDRSet`,
  `fromCIDRSet`) holds exactly one of `cidr`, `cidrGroupRef` and
  `cidrGroupSelector`; a DNS entry (`toFQDNs`, a port's `rules.dns`) exactly one
  of `matchName` and `matchPattern`; a port's `rules` exactly one of `http` and
  `dns`.
- every value rule: enumerations, patterns, ranges, lengths and formats (an
  `authentication` mode, a port, a CIDR, an ICMP family), and an authored empty
  value in a required field;
- what Cilium's own reader refuses of an object the API server has admitted
  (which peers may be combined, an entity's name, a port's range). The
  `cilium-networkpolicy` kind leaves these too;
- whether what a rule refers to exists: a CIDR group, a Secret, an Envoy
  configuration, a Service.

**Policy.** `ApplyPolicy` is a no-op and no capability is required, as for the
`cilium-networkpolicy` kind: the environment policy holds no rule for the
object, and its capability lists gate trait types.
- **Hosts are not checked.** A rule names hosts and networks: the names and
  patterns of `toFQDNs` and of a port's DNS rules, an HTTP rule's `host`, a
  port's `serverNames`, and every CIDR. None is an artifact source, and none is
  held to the policy's allowed registries.
- **No literal secret is checked.** An HTTP header match's `value` is written
  into the object as authored. A header match's `secret` and a TLS context's
  `secret` refer to a Secret by name and namespace; the reference is written as
  authored, and the Secret is not looked for in the document.

**Not covered.** The object's metadata beyond its name, its labels and its
annotations (`objectName`, `labels`, `annotations`), and its `status`, which the
Cilium agent writes.

### Example

The component in
`pkg/cmd/kurel/testdata/cilium-clusterwidenetworkpolicy-component/app.yaml`,
built against the `cluster.yaml` beside it, gives the `expected.yaml` beside it.
The fixture test compares the two.

## gatewayclass, gateway, listenerset, referencegrant, backendtlspolicy

`gatewayclass`, `gateway`, `listenerset`, `referencegrant`, `backendtlspolicy`
are the kind-named projections of the infrastructure objects of the Gateway
API's `gateway.networking.k8s.io/v1`: a GatewayClass, a Gateway, a ListenerSet,
a ReferenceGrant and a BackendTLSPolicy. The routes of the same API are their
own kinds (`httproute`, `grpcroute`, `tcproute`, `udproute`, `tlsroute`).

### Properties

**Authored.** The properties are the top-level json fields of the spec type,
decoded strictly at every depth: an unknown key is refused wherever it sits (a
listener, a certificate reference, a validation).
- `gatewayclass` (`GatewayClassSpec`): `controllerName`, `parametersRef` and
  `description`.
- `gateway` (`GatewaySpec`): `gatewayClassName`, `listeners`, `addresses`,
  `infrastructure`, `allowedListeners`, `tls` and `defaultScope`.
  **`defaultScope`, an address's `routability` and a listener's `filters` are
  experimental-channel fields:** the Go type holds them and the standard
  channel's Gateway CRD does not, so a cluster on that channel does not keep
  them. A listener's `filters` holds `requests`, each an `ExternalAuth` or an
  `ExtensionRef` filter by its `type`. They are the only such fields of these
  five specs at v1.7.0, and `TestGatewayKinds_WrittenEmptyAccepted` holds that.
- `listenerset` (`ListenerSetSpec`): `parentRef` and `listeners`, whose entries
  take the fields of a Gateway's listener, `filters` included, an
  experimental-channel field here too.
- `referencegrant` (`ReferenceGrantSpec`): `from` and `to`.
- `backendtlspolicy` (`BackendTLSPolicySpec`): `targetRefs`, `validation` and
  `options`.
- **No default is filled.** The defaults are the CRDs', and the API server
  applies them to what the object leaves out: a listener's `allowedRoutes`
  (routes of the Gateway's own namespace), a listener's `tls.mode`
  (`Terminate`), an address's `type` (`IPAddress`), the `group` and `kind` of a
  certificate reference (a core Secret) and of a parent reference (a Gateway),
  the `group` of an allowed route kind (the Gateway API's),
  `allowedListeners.namespaces.from` (`None`), a client certificate validation's
  `mode` (`AllowValidOnly`).
- One number of these types is omitted when zero, a ListenerSet listener's
  `port`, and the CRD gives it no default: an authored `port: 0` is left out as
  an unset one is, the API accepts neither, and the kind refuses both (below).
  Of the strings the types omit when empty, the CRDs default one: the `mode` of
  a Gateway's frontend TLS validation (`AllowValidOnly`), so an authored `mode:
  ""` would be left out and defaulted, and it is refused
  (`tls.frontend.default.validation.mode: "" cannot be carried by the Gateway
  API types (…)`). A listener request filter's
  `externalAuth.forwardBody.maxSize` is omitted when zero too; the CRD gives it
  no default, and by the API unset and `0` both forward no body.

**Required** is a field the API requires that the Go type writes whether or not
it was authored, so that the object would not show the omission: the rule every
kind follows (see the Prometheus operator's kinds). Each must be authored
(`gatewayClassName: required (…)`, `listeners[1].port: required (…)`); an
authored empty value is a value, and the API server's to refuse.
- A `gatewayclass`: `controllerName`.
- A `gateway`: `gatewayClassName` and `listeners`, and of each listener its
  `name`, `port` and `protocol`. Of a Gateway-wide `tls.frontend` that is
  authored: its `default` (`default: {}` asks for no client certificate), and of
  a `perPort` entry its `port` and `tls`; of a `validation` that is authored,
  its `caCertificateRefs`.
- A `listenerset`: `parentRef` with its `name`.
- Of a listener request filter that is authored, on a `gateway` and a
  `listenerset`: its `type`; of its `externalAuth`, the `backendRef` with its
  `name`; of its `extensionRef`, the `group`, `kind` and `name`.
- A `referencegrant`: `from` and `to`; of a source its `group`, `kind` and
  `namespace`, of a target its `group` and `kind`. The core group is authored as
  `group: ""`.
- A `backendtlspolicy`: `validation` with its `hostname`; of a subject
  alternative name that is authored, its `type`.
- Of a reference that is authored, on every kind, what the type would write
  empty: the `name` of a certificate reference (a listener's
  `tls.certificateRefs`, the Gateway's `tls.backend.clientCertificateRef`); the
  `group`, `kind` and `name` of a parameters reference (`parametersRef`,
  `infrastructure.parametersRef`), of a CA certificate reference
  (`caCertificateRefs`) and of a policy target (`targetRefs`); the `kind` of an
  allowed route kind (`allowedRoutes.kinds`).
- Of a match expression that is authored in a namespace `selector`, on a
  `gateway` and a `listenerset` (a listener's
  `allowedRoutes.namespaces.selector`, a Gateway's
  `allowedListeners.namespaces.selector`): its `key` and `operator`, which the
  CRDs of both channels require. Presence only: the operator's value and its
  `values` are left to the API server (see ["A label selector's match
  expressions"](conventions.md#a-label-selectors-match-expressions)). The
  selector is the one Kubernetes type these specs embed; the test below derives
  these entries with the others, and `TestLabelSelectorKinds_CoverEverySelector`
  shows what the API server answers for them.
- A required field under a parent the author left out is not asked for: the list
  follows what was authored.

**A required field the type omits is refused by the kind itself,** since no
required list can name it: the type leaves it out where it is not authored and
where it is authored empty alike, and the API server refuses the object either
way. There are seven at v1.7.0:
- a `listenerset` with no `listeners` and a `backendtlspolicy` with no
  `targetRefs`, absent or empty (`listeners: required (…)`), as a `servicecidr`
  with no `cidrs` is;
- a ListenerSet listener without its `name`, its `port` or its `protocol`,
  absent or empty, a `port: 0` included (`listeners[1].port: required (…)`). A
  Gateway's listener must author the same three, by the list above: the two
  listener types carry different json tags, and the answer is the same on both
  kinds;
- the `protocol` of a listener request filter's `externalAuth` (HTTP or GRPC),
  on a `gateway` and a `listenerset`, absent or empty
  (`listeners[0].filters.requests[0].externalAuth.protocol: required (…)`).
  Since go-kure/launcher#1117 (**breaking** for a document that leaves it out;
  the API server refuses that document too).

**The lists are generated** (see ["Required fields a kind writes unauthored and
does not
refuse"](conventions.md#required-fields-a-kind-writes-unauthored-and-does-not-refuse))
from the CRDs the linked module ships: 4 paths for a gatewayclass, 32 for a
gateway, 12 for a listenerset, 7 for a referencegrant, 9 for a backendtlspolicy.
The module ships two sets, one per channel of the API. The experimental one
holds every field the Go types do and is the one each path is read in;
`TestGatewayKinds_WrittenEmptyAccepted` holds the standard one to require the
same wherever it holds the property, and every field the type writes empty
unauthored that is not listed to a schema that accepts it. Every field the CRDs
require and the type writes unauthored is listed, the `key` and `operator` of a
namespace selector's expressions included, and nothing else is. A dependency
bump that adds, drops or moves one fails
`TestKindComponents_RequiredIsGenerated`;
`TestKindComponents_GeneratedRequiredBothWays` runs a field of each type that
declares one through the API server.

**References are the author's.** A Gateway names its class (`gatewayClassName`),
a ListenerSet its Gateway (`parentRef`), a BackendTLSPolicy its backends
(`targetRefs`), and each of them Secrets or ConfigMaps; launcher points none at
a component and does not look for the target in the document. To refer to
another component's object, name it: the component name, or its `objectName`. A
ListenerSet attaches only where its Gateway's `allowedListeners` lets its
namespace, and a reference across namespaces works only where a ReferenceGrant
of the target's namespace allows it; launcher checks neither. A `referencegrant`
allows references into the namespace it is emitted in, so it belongs to the
application of the objects referred to.

### Outputs

A `gatewayclass` is emitted with no namespace and declares its object as
cluster-scoped, so its name is claimed in no namespace. The other four are
emitted in the build namespace and declare their object as namespaced.

**Labels and annotations** are the `labels` and `annotations` properties. A
Gateway's `infrastructure.labels` and `infrastructure.annotations` are not
these: they are what the controller puts on the resources it creates for the
Gateway, which may be pods. They are held as a pod's are: the component label's
key holds the component's value there or the build is refused, and a key the
consumer reserved is refused (`spec.infrastructure label "…" may not be set`);
see "Component label and ownership" and "Reserved metadata keys" in the OAM
model. Launcher writes nothing there.

### Validation

**No capability is required, and none gates these kinds.** Launcher does not ask
whether the cluster serves `gateway.networking.k8s.io/v1`: where the Gateway
API's CRDs are not installed the component builds, and the object is refused at
apply. Whoever may author a component may author these: a GatewayClass, which is
cluster-scoped, and a ReferenceGrant, which lets objects of another namespace
refer to objects of the build namespace, included. A policy can keep the kinds
out of a build through the object kind policy (`oam.ObjectKindPolicy`). The
`httproute` trait may take the Gateway it attaches to from a capability
(`gatewayName`, `gatewayNamespace`); the `gateway` kind does not read it, and a
`gateway` component is not what that capability names unless the platform says
so.

**Not refused:**
- a list the API wants at least one item of that is authored empty, where the
  type writes it (`listeners: []` on a `gateway`, `from: []`): an authored empty
  value is a value;
- every other value rule of the CRDs: enumerations, lengths, patterns, minima
  and item limits, and the rules the CRDs write as expressions (a listener's
  `tls` and `hostname` against its `protocol`, the uniqueness of a listener's
  name and of its port, protocol and hostname, of an address and of a `perPort`
  port, one of `caCertificateRefs` and `wellKnownCACertificates`, a subject
  alternative name's field against its `type`, a target's `sectionName` among
  several targets, a GatewayClass's `controllerName` not changing, a listener
  request filter's `externalAuth` or `extensionRef` against its `type` and its
  `http` or `grpc` against its `protocol`, and the `port` of its `backendRef`,
  which a Service must name and an `XBackend` must not);
- what a Gateway API controller refuses when it reads the object, which shows in
  the object's status, not at creation.

**Policy.** No dimension of the environment policy reaches these objects: they
run no pod, hold no image, request no storage and have no replica count. A nil
policy and a strict one build the same object.
- **The pods of a Gateway are not sized here.** A controller may start a proxy
  for a Gateway; the Gateway object holds no pod template and no resources, so
  the policy's maxima and defaults have nothing to hold. `infrastructure.labels`
  and `infrastructure.annotations` are written as authored, and what a
  controller reads into them is its own; they are held to the component label
  and the reserved keys with or without a policy (see "Labels and annotations"
  below).
- **No field holds a literal secret, and none is checked.** A certificate is a
  reference: to a Secret on a listener and for the Gateway's client certificate,
  to a ConfigMap or another object for CA certificates. The free maps a
  controller defines, a listener's `tls.options` and a BackendTLSPolicy's
  `options`, are written to the object as authored, under a policy that forbids
  explicit secrets too.
- **Hosts are not checked.** A host these objects name is one a Gateway serves
  or a backend is checked against, not an artifact source, and none is held to
  the policy's allowed registries: a listener's `hostname`, an address's
  `value`, a BackendTLSPolicy's `validation.hostname` and the `hostname` and
  `uri` of its subject alternative names.

**Not covered.** Whether what is referred to exists (a class, a Gateway, a
Secret, a ConfigMap, a Service and its port), and whether a controller of the
cluster implements the class. The object's status is the controller's and is not
written.

### Example

- `gatewayclass`: The component in
  `pkg/cmd/kurel/testdata/gatewayclass-component/app.yaml`, built against the
  `cluster.yaml` beside it, gives the `expected.yaml` beside it. The fixture
  test compares the two.
- `gateway`: The component in
  `pkg/cmd/kurel/testdata/gateway-component/app.yaml`, built against the
  `cluster.yaml` beside it, gives the `expected.yaml` beside it. The fixture
  test compares the two.
- `listenerset`: The component in
  `pkg/cmd/kurel/testdata/listenerset-component/app.yaml`, built against the
  `cluster.yaml` beside it, gives the `expected.yaml` beside it. The fixture
  test compares the two.
- `referencegrant`: The component in
  `pkg/cmd/kurel/testdata/referencegrant-component/app.yaml`, built against the
  `cluster.yaml` beside it, gives the `expected.yaml` beside it. The fixture
  test compares the two.
- `backendtlspolicy`: The component in
  `pkg/cmd/kurel/testdata/backendtlspolicy-component/app.yaml`, built against
  the `cluster.yaml` beside it, gives the `expected.yaml` beside it. The fixture
  test compares the two.

## tcproute, udproute, tlsroute

`tcproute`, `udproute`, `tlsroute` are the kind-named projections of the routes
of the Gateway API's `gateway.networking.k8s.io/v1` that carry no HTTP: a
TCPRoute, a UDPRoute and a TLSRoute.

### Properties

**Authored.** The properties are the top-level json fields of the spec type,
those of the `CommonRouteSpec` it inlines included, decoded strictly at every
depth: an unknown key is refused wherever it sits (a parent, a rule, a backend).
- `tcproute` (`TCPRouteSpec`) and `udproute` (`UDPRouteSpec`): `parentRefs`,
  `useDefaultGateways` and `rules`. The API accepts exactly one rule.
- `tlsroute` (`TLSRouteSpec`): the same three and `hostnames`, the server names
  (SNI) of the TLS handshakes the route takes. A name may start with the
  wildcard label `*.`. The route holds no certificate and no key: by the Gateway
  API's documentation the listener it attaches to passes the connection through
  or terminates it.
- **`useDefaultGateways` is an experimental-channel field:** the Go types hold
  it and the standard channel's CRDs do not, so a cluster on that channel does
  not keep it. It is the only such field of these specs at v1.7.0, and
  `TestGatewayKinds_WrittenEmptyAccepted` holds that.
- **No default is filled.** The defaults are the CRDs', and the API server
  applies them to what the object leaves out: the `group` and `kind` of a parent
  (a Gateway), and the `group`, `kind` and `weight` of a backend (a core
  Service, weight 1).

**Required,** by the rule of the infrastructure kinds above, is the `name` of a
parent and of a backend that are authored (`parentRefs[0].name: required (…)`):
the API requires it and the type would write it empty.

**A required field the type omits is refused by the kind itself,** absent or
authored empty, as the API server refuses the object:
- a route with no `rules` (`rules: required (…)`);
- a rule with no `backendRefs` (`rules[0].backendRefs: required (…)`);
- a `tlsroute` with no `hostnames` (`hostnames: required (…)`).

The `httproute` kind refuses none of these: the HTTPRoute API requires no rule,
no backend and no host name.

Each required list (2 paths) is generated from the CRDs the linked module ships,
and the refused omissions are held to them, as for the infrastructure kinds.

### Outputs

The object is written at `v1`, the version the CRDs of both channels store; the
older versions (`v1alpha2`, and `v1alpha3` of a TLSRoute) are served by the
experimental channel's CRDs only, as deprecated.

**Labels and annotations** are the `labels` and `annotations` properties.

### Validation

**No capability is required, and none gates these kinds,** on the terms of the
infrastructure kinds above: launcher does not ask whether the cluster serves the
API, and where the CRD is not installed the component builds and the object is
refused at apply. Whoever may author a component may author a route, one that
attaches to a Gateway of another namespace or sends traffic to a backend of
another namespace included (below).

**A route is an authored object,** as an `httproute` is. A parent and a backend
are references carried as written; launcher points neither at a component and
does not look for the target in the document. To refer to another component's
object, name it: the component name, or its `objectName`.
- **References across namespaces are not gated.** `parentRefs[].namespace` and
  `backendRefs[].namespace` are written as authored, on the terms of the
  infrastructure kinds above and as the `httproute` kind writes them: by the
  Gateway API's documentation a route attaches only where the Gateway's listener
  allows routes of its namespace (`allowedRoutes`), and a backend of another
  namespace is used only where a ReferenceGrant of that namespace allows it;
  launcher checks neither.
- **No NetworkPolicy allow rule.** As for `httproute` above: the NetworkPolicy
  synthesis reads a routing trait's traffic sources and target component, which
  these kinds do not report, so it allows nothing for a route's backends. An
  author who wants the allow rule authors the NetworkPolicy.
- **No parent from a capability.** `parentRefs` is what the author wrote;
  unwritten, the route has no parent.

**A Service backend must name its port** (`rules[0].backendRefs[1].port:
required (…)`). The CRDs write that as an expression rule, `(size(self.group) ==
0 && self.kind == 'Service') ? has(self.port) : true`, which the API server
evaluates after it has filled the defaults: a backend that names no `group` and
no `kind` is a Service, and so is one that names the core group or the kind
alone. A backend of another group or kind needs no port. It is the one
expression rule the kinds hold, since it asks for a field that was left out, and
the rule reads the same in both channels and on the three kinds.

**Not refused:**
- more than one rule, more than 16 backends in a rule, and every other value
  rule of the CRDs: lengths, patterns, minima, maxima and item limits;
- the two expression rules on `parentRefs`, which tell two references to one
  parent apart (by `sectionName`, and in the experimental channel by `port`
  too). The channels write them differently, so a refusal by the kind would be
  wider than one of them;
- the experimental channel's rule that a backend of the `XBackend` kind (group
  `gateway.networking.x-k8s.io`) names no `port`: the standard channel does not
  declare it, so a refusal by the kind would be wider than that channel;
- the three expression rules on a TLSRoute's `hostnames` (a name is no IP
  address, it is a DNS name, and a wildcard is its first label alone): they hold
  the form of a value that is authored, as a pattern does;
- what a Gateway API controller refuses when it reads the object, which shows in
  the object's status, not at creation.

**Policy.** No dimension of the environment policy reaches these objects: they
run no pod, hold no image, request no storage and have no replica count, and no
field of one holds a literal secret. A nil policy and a strict one build the
same object. A TLSRoute's `hostnames` are names the route serves, not artifact
sources, and are not held to the policy's allowed registries, as an HTTPRoute's
are not. The policy's capability lists gate trait types, so none of them refuses
a component of these types; a consumer that restricts routing restricts the
component types it registers.

**Not covered:** whether what is referred to exists (a Gateway, a listener, a
Service and its port), whether the cluster's controller implements the route
kind, and the object's `status`, which the controller writes.

### Example

- `tcproute`: The component in
  `pkg/cmd/kurel/testdata/tcproute-component/app.yaml`, built against the
  `cluster.yaml` beside it, gives the `expected.yaml` beside it. The fixture
  test compares the two.
- `udproute`: The component in
  `pkg/cmd/kurel/testdata/udproute-component/app.yaml`, built against the
  `cluster.yaml` beside it, gives the `expected.yaml` beside it. The fixture
  test compares the two.
- `tlsroute`: The component in
  `pkg/cmd/kurel/testdata/tlsroute-component/app.yaml`, built against the
  `cluster.yaml` beside it, gives the `expected.yaml` beside it. The fixture
  test compares the two.
