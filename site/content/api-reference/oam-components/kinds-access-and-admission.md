---
title: "Kinds: access and admission"
weight: 5
---

Each group of kinds below has the sections it needs from this list: Properties,
Outputs, Validation and Example. Its implementation, history and tests are on the
[contributor page](../../contributing/oam-reference/kinds-access-and-admission.md).

## apiservice, validatingwebhookconfiguration, mutatingwebhookconfiguration

`apiservice`, `validatingwebhookconfiguration`,
`mutatingwebhookconfiguration` are the kind-named
projections of three cluster-scoped objects: an `apiregistration.k8s.io/v1`
APIService and an `admissionregistration.k8s.io/v1`
ValidatingWebhookConfiguration and MutatingWebhookConfiguration. Each emits
that one object, named after the component unless `objectName` names it,
with no namespace, holding exactly what was authored, on `policyFreeKind`.
No dimension of the environment policy reads them; its object kind rules
(`oam.ObjectKindPolicy`) gate them as every emitted object, so a policy
that forbids their group, their kind or cluster-scoped objects refuses
them. What they do once applied is out of the build's reach (see ["Kind
inventory"](https://www.gokure.dev/launcher/api-reference/oam-components/#kind-inventory), "Cluster-wide admission and API registration").

### Validation

**apiservice** decodes `APIServiceSpec` strictly. From the markers,
`groupPriorityMinimum` and `versionPriority` are required, which the type
writes unauthored. By hand, from the API server's validation: `version`
must be written, `group` too (the API exempts `v1`, the core API, whose
APIService is named `v1.`; that is no DNS-1123 subdomain, so no component
name or `objectName` can give it, and the kind requires the group), and an
authored `service` its `namespace` and `name`. The API requires the object
to be named `<version>.<group>`, so the kind refuses any other name at
generation (`the APIService is named "metrics", and the API requires the
name "v1beta1.metrics.example.com" (<version>.<group>): name the component
so, or set objectName`). A naming hook that renames the object is held to
the same rule. The Service is not resolved at build. The priorities'
ranges (1 to 20000, 1 to 1000), the port, and how `caBundle` and
`insecureSkipTLSVerify` combine are left to the API server. `caBundle` is
base64, as the API's JSON writes bytes.

**validatingwebhookconfiguration** and **mutatingwebhookconfiguration**
have no spec: their one property is the object's `webhooks`, strictly
decoded, and `kind`, `apiVersion` and `metadata` are refused. From the
markers, a webhook's `name`, `clientConfig`, `sideEffects` and
`admissionReviewVersions`, a Service's `namespace` and `name`, and a match
condition's `name` and `expression` must be written. By hand, from the API
server's validation at Kubernetes v1.37.1: `clientConfig` holds exactly one
of `url` and `service`
(`webhooks[0].clientConfig: exactly one of url and service is required …`),
`admissionReviewVersions` holds at least one entry, and a rule names at
least one operation, API group, API version and resource
(`webhooks[0].rules[0].resources: required …`). Each webhook's
`namespaceSelector` and `objectSelector` are held to the shared check (see
["A label selector's match expressions"](conventions.md#a-label-selectors-match-expressions)). Not checked here: the form of a
webhook's name and its uniqueness, the values of `failurePolicy`,
`matchPolicy`, `sideEffects`, `reinvocationPolicy` and a rule's operations
and scope, the timeout's range, a URL's form, the admission review
versions the API server recognises, and how wildcards combine in a rule.
A match condition's CEL expression is compiled by the API server, not at
build. Neither the URL nor the Service is resolved at build. A mutating
webhook changes objects after the build, so what the build checked need
not hold of what is stored. An authored `webhooks: []` emits a
configuration with no webhook.

### Example

- `apiservice`: The component in `pkg/cmd/kurel/testdata/apiservice-component/app.yaml`, built
  against the `cluster.yaml` beside it, gives the `expected.yaml` beside it. The fixture test
  compares the two.
- `validatingwebhookconfiguration`: The component in
  `pkg/cmd/kurel/testdata/validatingwebhookconfiguration-component/app.yaml`, built against
  the `cluster.yaml` beside it, gives the `expected.yaml` beside it. The fixture test compares
  the two.
- `mutatingwebhookconfiguration`: The component in
  `pkg/cmd/kurel/testdata/mutatingwebhookconfiguration-component/app.yaml`, built against the
  `cluster.yaml` beside it, gives the `expected.yaml` beside it. The fixture test compares the
  two.

## validatingadmissionpolicy, validatingadmissionpolicybinding, mutatingadmissionpolicy, mutatingadmissionpolicybinding

`validatingadmissionpolicy`, `validatingadmissionpolicybinding`,
`mutatingadmissionpolicy`, `mutatingadmissionpolicybinding` are the kind-named projections of four
cluster-scoped `admissionregistration.k8s.io/v1` objects. Each decodes its
spec strictly and emits that one object, named after the component unless
`objectName` names it, with no namespace, holding exactly what was
authored, on `policyFreeKind`, gated as the webhook configurations above.

### Validation

**Minimum Kubernetes version.** The API server serves
ValidatingAdmissionPolicy and ValidatingAdmissionPolicyBinding at `v1`
from Kubernetes 1.30, and MutatingAdmissionPolicy and
MutatingAdmissionPolicyBinding from 1.36. A cluster below that refuses the
object at apply. The build does not check it, even for a `ClusterProfile`
that states `spec.kubernetesVersion`: that version reaches only the
feature-gated properties, never the version a kind is served from. The
description of each kind's `matchConstraints` or `policyName` states the
same.

From the markers, a validation's `expression`, an audit annotation's `key`
and `valueExpression`, a match condition's and a variable's `name` and
`expression`, a mutation's `patchType`, a binding's `policyName` and a
validating binding's `validationActions` must be written. By hand, from
the API server's validation at Kubernetes v1.37.1: a policy holds
`matchConstraints` with at least one rule
(`matchConstraints.resourceRules: required …`), a rule names at least one
operation, API group, API version and resource, an authored `paramKind`
its `apiVersion` and `kind`. A validating policy holds at least one of
`validations` and `auditAnnotations`. A mutating policy holds at least one
mutation, each with the patch its `patchType` names and that patch's
`expression` (`mutations[0].jsonPatch: required with the patchType
JSONPatch …`), and a `reinvocationPolicy`, which the API server does not
default. A validating binding holds at least one of `validationActions`.
A binding's authored `paramRef` holds exactly one of `name` and `selector`
(`paramRef: exactly one of name and selector is required …`) and its
`parameterNotFoundAction`, which the API server does not default; its
authored `matchResources` is checked as a policy's match, without
requiring a rule. The selectors of a match and a `paramRef` are held to
the shared check (see ["A label selector's match expressions"](conventions.md#a-label-selectors-match-expressions)).

Not checked here: the values of `failurePolicy`, `matchPolicy`, a rule's
operations and scope (a mutating policy takes only CREATE, UPDATE and CONNECT),
`validationActions` and how they combine, `parameterNotFoundAction`,
`reinvocationPolicy`, `patchType` and a validation's `reason`; the form
and uniqueness of names and keys; a mutation holding the other patch
beside its own; and every CEL expression, which the API server compiles.
Neither the policy a binding names nor its parameter resources are
resolved at build. A policy does nothing until a binding names it. A
mutating policy changes objects after the build, so what the build
checked need not hold of what is stored.

### Example

- `validatingadmissionpolicy`: The component in
  `pkg/cmd/kurel/testdata/validatingadmissionpolicy-component/app.yaml`, built against the
  `cluster.yaml` beside it, gives the `expected.yaml` beside it. The fixture test compares the
  two.
- `validatingadmissionpolicybinding`: The component in
  `pkg/cmd/kurel/testdata/validatingadmissionpolicybinding-component/app.yaml`, built against
  the `cluster.yaml` beside it, gives the `expected.yaml` beside it. The fixture test compares
  the two.
- `mutatingadmissionpolicy`: The component in
  `pkg/cmd/kurel/testdata/mutatingadmissionpolicy-component/app.yaml`, built against the
  `cluster.yaml` beside it, gives the `expected.yaml` beside it. The fixture test compares the
  two.
- `mutatingadmissionpolicybinding`: The component in
  `pkg/cmd/kurel/testdata/mutatingadmissionpolicybinding-component/app.yaml`, built against
  the `cluster.yaml` beside it, gives the `expected.yaml` beside it. The fixture test compares
  the two.

## role, rolebinding, clusterrole, clusterrolebinding

`role`, `rolebinding`, `clusterrole`, `clusterrolebinding` are the kind-named projections of the four objects
of the `rbac.authorization.k8s.io/v1` API, built on `policyFreeKind`. Each
emits that one object, named after the component unless `objectName` names
it, holding exactly what was authored: the handler adds no label, no
annotation and no default of its own. They are for the grants an
application needs that are not those of one workload's own ServiceAccount,
which is what the `rbac` trait (`pkg/oam/builtin/traits/README.md`)
writes: a role several bindings share, a binding to a role that already
exists, a grant to a user or a group, an aggregated ClusterRole. What the
four have in common is stated here once; each kind's entry below holds what
is its own.

### Properties

- **Authored:** none of the four has a spec, so the properties are the
  object's own top-level fields, decoded strictly at every depth. The
  object's `kind`, `apiVersion` and `metadata` are launcher's and are
  refused under any spelling.
- **The names are the author's literals.** `roleRef.name` and a subject's
  `name` and `namespace` are written as authored. Launcher points none of
  them at a component: a `roleRef.name` does not follow the `objectName`
  of a `role` or `clusterrole` component, and a ServiceAccount subject's
  name does not follow a `serviceaccount` component's, so write the name
  the object takes. Whether the role or the subject exists is not checked.
- **Labels and annotations** are the `labels` and `annotations` properties.

### Outputs

- **`roleRef.apiGroup` may be left out.** The object then carries
  `apiGroup: ""`, which the type always writes, and the API server fills
  the RBAC group before it validates. An authored group that is not the
  RBAC group is written as authored and is the API server's to refuse. A
  User or Group subject may leave its `apiGroup` out too: the key is then
  not written, and the API server fills the RBAC group the same way.

### Validation

- **Presence rules read by hand.** Beyond the `+required` markers of the
  linked `k8s.io/api` module, these four kinds check presence rules read by
  hand from the API server's validation at Kubernetes v1.37.1
  (`pkg/apis/rbac/validation/validation.go` of `k8s.io/kubernetes`, which
  no module launcher links holds). No test derives them, and a later
  Kubernetes that changes one is not noticed by a dependency bump.
- **Required from the markers** are the fields the API's source marks
  required and the Go type writes whether or not they were authored.
The API
  marks no field of the four required that the type leaves out when
  unauthored.
These checks are of
  presence: an authored empty value (`verbs: []`, `name: ""`) builds and is
  the API server's to refuse.
- **A rule** (`rules[]` of a `role` or a `clusterrole`) must write its
  `verbs` (`rules[0].verbs: required …`, from the markers). By hand: it
  must name at least one of `apiGroups` and at least one of `resources`
  (`rules[0].apiGroups: required …`; `""` is the core group), so a rule
  that lost its resources and grants nothing is refused. A `clusterrole`
  rule may instead name `nonResourceURLs`, and then names no `apiGroups`,
  `resources` or `resourceNames`
  (`rules[0].nonResourceURLs: not allowed beside …`). These two hand-read
  checks count what was decoded, so an authored empty `apiGroups: []` or
  `resources: []` is refused too.
- **A binding** (`rolebinding`, `clusterrolebinding`) must write `roleRef`
  with its `kind` and `name`, and each subject's `kind` and `name`
  (`roleRef: required …`, `subjects[0].name: required …`, from the
  markers). `subjects` may be left out: the API server's validation
  requires none, and the key is then not written.
- **Policy.** No field of the four is one an `oam.Policy` method speaks
  to, so `ApplyPolicy` enforces nothing and fills nothing.
- **Beside the `rbac` trait.** A trait's object and a component's of one
  kind, name and namespace (or one cluster-scoped name) are refused: the
  trait resolves the name of each object it generates, and the transform
  reports a name collision that names both.
- **Not covered.** Every rule on the form of a value is left to the API
  server: that a `roleRef` names the RBAC group and a kind the binding
  may grant, the kind and API group of a subject, the form of its name,
  and whether the `values` of a match expression of an aggregation
  selector fit its `operator`. Whether a verb, a resource or an API group
  exists, and the form of a non-resource URL, are checked by neither
  launcher nor the API server's validation: a rule that names none that
  exists builds and passes that validation. The API server refuses a change to the `roleRef` of an
  existing binding. Launcher does not compare a build with the cluster, so
  a changed one builds here and is refused at apply. Launcher does not
  compare what a role grants with what its author, or whoever applies the
  object, may grant.
- **The object kind policy** (`oam.ObjectKindPolicy`)
  is what restricts these kinds: it keeps any of the four, or the whole
  `rbac.authorization.k8s.io` group, out of a build, and a policy that
  does not allow cluster-scoped objects refuses the ClusterRole and the
  ClusterRoleBinding. It holds the `rbac` trait's objects too. What an
  allowed role grants stays unchecked here; the API server's escalate and
  bind checks hold it to the identity that applies it.

### Example

- `role`: The component in `pkg/cmd/kurel/testdata/role-component/app.yaml`, built against the
  `cluster.yaml` beside it, gives the `expected.yaml` beside it. The fixture test compares the
  two.
- `rolebinding`: The component in `pkg/cmd/kurel/testdata/rolebinding-component/app.yaml`,
  built against the `cluster.yaml` beside it, gives the `expected.yaml` beside it. The fixture
  test compares the two.
- `clusterrole`: The component in `pkg/cmd/kurel/testdata/clusterrole-component/app.yaml`,
  built against the `cluster.yaml` beside it, gives the `expected.yaml` beside it. The fixture
  test compares the two.
- `clusterrolebinding`: The component in
  `pkg/cmd/kurel/testdata/clusterrolebinding-component/app.yaml`, built against the
  `cluster.yaml` beside it, gives the `expected.yaml` beside it. The fixture test compares the
  two.

## role

`role` emits its Role in the build namespace; the handler declares its
object as namespaced, so the object name is claimed in the object's
namespace. **It is ungated: no capability and no environment-policy check
restricts what a role grants.** Its one property is `rules`.
`nonResourceURLs` are refused in a rule
(`rules[0].nonResourceURLs: not allowed in a Role …`): a non-resource URL
is not namespaced, and the API server refuses one in a Role. The API
requires no rule, and the type always writes the list: a role that authors
none carries `rules: null` and grants nothing, and an authored empty list
is written as `[]`.

## rolebinding

`rolebinding` emits its RoleBinding in the build namespace; the handler
declares its object as namespaced, so the object name is claimed in the
object's namespace. **It is ungated: no capability and no
environment-policy check restricts what a role grants**, nor to whom: a
binding to any role the cluster holds, `cluster-admin` included, builds.
Its properties are `subjects` and `roleRef`, which names a Role of the
binding's namespace or a ClusterRole. A ServiceAccount subject may leave
its `namespace` out: the API server's validation takes it, the key is not
written, and the authorizer of that version reads the subject as one of
the binding's own namespace.

## clusterrole

`clusterrole` emits its ClusterRole with no namespace; the handler
declares its object as cluster-scoped, so the object name is claimed
cluster-wide. **It is ungated: no capability and no environment-policy
check restricts what a role grants.** Its properties are `rules` and
`aggregationRule`. By hand: an authored `aggregationRule` must hold at
least one entry of `clusterRoleSelectors`
(`aggregationRule.clusterRoleSelectors: required …`); an empty selector
(`{}`) is an entry. From the markers, a selector's match expression must
write its `key` and `operator`. By hand again, an expression that writes
both is refused for an operator that is none of the four and for `values`
that do not go with the operator
(`aggregationRule.clusterRoleSelectors[0].matchExpressions[0].values:
required with the operator In (at least one value)`): the API server
validates each selector as a label selector (see ["A label selector's match
expressions"](conventions.md#a-label-selectors-match-expressions)). An aggregated ClusterRole builds: it is
emitted with `rules: null` when `rules` is not authored and with
`rules: []` when authored so, and the control plane fills the rules.

## clusterrolebinding

`clusterrolebinding` emits its ClusterRoleBinding with no namespace; the
handler declares its object as cluster-scoped, so the object name is
claimed cluster-wide. **It is ungated: no capability and no
environment-policy check restricts what a role grants**, nor to whom: a
binding of `cluster-admin` to any subject builds. Its properties are
`subjects` and `roleRef`, which names a ClusterRole. By hand: a
ServiceAccount subject must write its `namespace`
(`subjects[0].namespace: required …`), since the binding is in none.
