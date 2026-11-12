---
title: "Kinds: networking"
weight: 5
---

Implementation, history and tests of the entries on the reference page
[Kinds: networking](../../api-reference/oam-components/kinds-networking.md). Every test
named here is in `pkg/oam/builtin/components/`, in the file the note beside it names.

## ingress

- **History:** go-kure/launcher#790 (the kind-named projection).

## httproute

- **History:**
  - go-kure/launcher#790 (the kind-named projection).
  - go-kure/launcher#883 (the Required list of the HTTPRoute is generated from
    the CRD).
  - go-kure/launcher#1117 (a rule's `sessionPersistence` changed shape at
    Gateway API v1.7.0, **breaking**).

## grpcroute

- **History:**
  - go-kure/launcher#790 (the kind-named projection).
  - go-kure/launcher#883 (the list of the fields the GRPCRoute CRD requires is
    generated).

## networkpolicy

- **History:** go-kure/launcher#790 (the kind-named projection).

## cilium-networkpolicy

- **Implementation:** The list of a rule is held in the internal
  `pkg/oam/internal/requiredfields` (`CiliumRule`), since the
  `cilium-networkpolicy` trait reads it too, cut to the three fields of a rule
  it publishes. ("The list of a rule" is the reference's "Required" list of the
  fields the CRD requires inside a rule, with the two more it names; "it" after
  "reads" is that list, and "it publishes" is the `cilium-networkpolicy` trait.)
- **History:**
  - go-kure/launcher#790 (the kind-named projection).
  - go-kure/launcher#883 (the list of a rule's required fields is kept by hand,
    not generated).
- **Tests:**
  - `TestCiliumNetworkPolicy_RequiredMatchCRD` holds the whole list to the
    CiliumNetworkPolicy CRD of the linked module, its own file and not the
    cluster-wide policy's, and to the rule type, under `spec` and under `specs`,
    so a dependency bump that adds, drops or moves one fails there. ("the whole
    list" is the reference's "Required" list, the 59 fields and the two more it
    names, and "one" is a field of that list; the test is in
    `cilium_networkpolicy_internal_test.go`.)
  - The positions are found from the Cilium types, and a test fails when a
    Cilium bump adds a type that unmarshals itself and is not checked. ("The
    positions" are the positions the reference's previous sentence lists, those
    that hold a selector and the two other types under a rule that unmarshal
    themselves; the sentence names no test.)

## servicecidr

- **History:** go-kure/launcher#790 (the kind-named projection).

## poddisruptionbudget

- **History:** go-kure/launcher#790 (the kind-named projection).

## endpointslice

- **History:** go-kure/launcher#790 (the kind-named projection).
- **Tests:**
  - `TestKindComponents_OmittedRequiredAndWrittenDefaults` holds the kind in its
    table as well, from the same markers: to that, and to no default marked on a
    field the type writes unauthored. ("as well" is as
    `TestBuiltinMarkerKinds_RequiredMatchMarkers` holds the kind, which the
    reference names; "the same markers" are the `+required` and `+optional`
    markers of the linked `k8s.io/api` module named there; "that" is the
    reference's "The API marks no field of the kind required that the type
    leaves out when unauthored"; the test is in
    `kind_api_sets_internal_test.go`.)

## cilium-bgpadvertisement, cilium-bgpclusterconfig, cilium-bgpnodeconfigoverride, cilium-bgppeerconfig

- **Implementation:** Each is built on `policyFreeKind` and emits that one
  object, named after the component unless `objectName` names it; the handler
  adds no label, no annotation and no default. ("Each" is each of the four
  kinds, `cilium-bgpadvertisement`, `cilium-bgpclusterconfig`,
  `cilium-bgpnodeconfigoverride` and `cilium-bgppeerconfig`, and "that one
  object" is the Cilium object the kind is the projection of.)
- **History:**
  - go-kure/launcher#790 (the kind-named projections).
  - go-kure/launcher#922 (the object kind policy, `oam.ObjectKindPolicy`).
  - go-kure/launcher#883 (the required lists are generated from the linked
    module's `v2` CRDs).
- **Tests:**
  - The same test holds the two to the CRD, and
    `TestKindComponents_OmittedRequiredAndWrittenDefaults` shows the refusals.
    ("The same test" is `TestCiliumBGPKinds_RequiredMatchCRD`, which the
    reference's "These lists are generated" sentence names and which is in
    `cilium_bgp_kinds_internal_test.go`; "the two" are the two required fields
    the reference names, the `name` of an `interface` and the `addresses` of a
    `service`, and "the refusals" are the refusals of those two;
    `TestKindComponents_OmittedRequiredAndWrittenDefaults` is in
    `kind_api_sets_internal_test.go`.)
  - The same test holds each checked rule to the API server's own expression
    validator, run over the linked CRD after the defaults the API server fills:
    it refuses the properties that break the rule, by that rule alone, and
    accepts the ones next to them, and the kind answers the same on both. ("The
    same test" is `TestCiliumBGPKinds_ExpressionRules`, which the reference's
    previous sentence names and which is in `cilium_bgp_kinds_internal_test.go`;
    "it" is the API server's own expression validator, and "both" are the
    properties that break the rule and the ones next to them.)

## cilium-cidrgroup, cilium-loadbalancerippool, cilium-egressgatewaypolicy, cilium-localredirectpolicy, cilium-nodeconfig

- **Implementation:** Each is built on `policyFreeKind` and emits that one
  object, named after the component unless `objectName` names it; the handler
  adds no label, no annotation and no default of its own. ("Each" is each of the
  five kinds, `cilium-cidrgroup`, `cilium-loadbalancerippool`,
  `cilium-egressgatewaypolicy`, `cilium-localredirectpolicy` and
  `cilium-nodeconfig`, and "that one object" is the Cilium object the kind is
  the projection of.)
- **History:**
  - go-kure/launcher#790 (the kind-named projections).
  - go-kure/launcher#922 (the object kind policy, `oam.ObjectKindPolicy`).
  - go-kure/launcher#883 (the required lists are generated from the linked
    module's `v2` CRDs).
- **Tests:**
  - `TestCiliumPlainKinds_NoDefaultIsLost` holds the defaults of the linked CRDs
    to that: one that is not the field's empty value, on a field that is no
    pointer, fails there. ("that" is the reference's "each of the three has that
    empty value as its default in the CRD", "one" is a default of the linked
    CRDs, and the test is in `cilium_plain_kinds_internal_test.go`.)
  - `TestCiliumPlainKinds_RedirectFrontendTakesOneMatcher` holds the check to
    the CRD, and fails where another schema of the five gains such a choice.
    ("the check" is the reference's refusal of a `redirectFrontend` that authors
    neither or both of `addressMatcher` and `serviceMatcher`, "the five" are the
    five kinds of this entry, and the test is in
    `cilium_plain_kinds_internal_test.go`.)

## cilium-clusterwidenetworkpolicy

- **History:** go-kure/launcher#790 (the kind-named projection).
- **Tests:**
  - `TestCoreKindSchemas_CoverSpec` holds the two keys to the linked
    `CiliumClusterwideNetworkPolicy` type. ("the two keys" are `spec` and
    `specs`, the two fields the reference names as the component's properties;
    the test is in `core_kinds_test.go`.)
  - `TestCiliumClusterwideNetworkPolicy_ObjectRule` holds the refusal to the API
    server's own expression validator, which accepts an authored `specs: []` and
    would refuse the object written without it; ("the refusal" is the refusal of
    no rule at all that the reference's bullet states, and "it" is `specs: []`;
    the test is in `cilium_clusterwidenetworkpolicy_internal_test.go`.)
  - `TestCiliumClusterwideNetworkPolicy_RequiredMatchCRD` holds the whole list
    to the CRD of the linked module and to the rule type, under `spec` and under
    `specs`, so a dependency bump that adds, drops or moves one fails there.
    ("the whole list" is the reference's "Required" list, the 59 fields and the
    two more it names, and "one" is a field of that list; the test is in
    `cilium_clusterwidenetworkpolicy_internal_test.go`.)
  - `TestCiliumClusterwideNetworkPolicy_SchemaChoices` lists every choice and
    expression rule of the linked CRD as checked or left, and fails on one that
    is neither; ("one" is a choice or an expression rule of the linked CRD that
    is neither checked nor left; the test is in
    `cilium_clusterwidenetworkpolicy_internal_test.go`.)

## gatewayclass, gateway, listenerset, referencegrant, backendtlspolicy

- **Implementation:** Each is built on `policyFreeKind` (see the
  **storageclass** entry) and emits that one object, named after the component
  unless `objectName` names it; the handler adds no label, no annotation and no
  default. ("Each" is each of the five kinds, `gatewayclass`, `gateway`,
  `listenerset`, `referencegrant` and `backendtlspolicy`, "that one object" is
  the Gateway API object the kind is the projection of, and "the
  **storageclass** entry" is on the reference page [Kinds: core
  workloads](../../api-reference/oam-components/kinds-core-workloads.md).)
- **History:**
  - go-kure/launcher#790 (the kind-named projections).
  - go-kure/launcher#922 (the object kind policy, `oam.ObjectKindPolicy`).
  - go-kure/launcher#883 (the required lists are generated from the CRDs the
    linked module ships).
- **Tests:**
  - `TestGatewayKinds_DefaultedZeros` holds each kind's list of such fields to
    the numbers, booleans and strings that are omitted when zero and that a CRD
    of either channel defaults to something else. ("such fields" are the fields
    of these types that are omitted when zero or empty, as the reference's
    previous sentences name them; the test is in
    `gateway_kinds_internal_test.go`.)
  - `TestKindComponents_OmittedRequiredAndWrittenDefaults` derives that set from
    the CRDs, at every depth of the five specs, and holds each member to one of
    two answers: the kind's refusal, which it runs, or a stated reason the
    decoded value cannot show the omission. ("that set" is the seven required
    fields the type omits that the reference lists under "A required field the
    type omits is refused by the kind itself", and "the five specs" are the
    specs of the five kinds; the test is in `kind_api_sets_internal_test.go`.)
  - None has the second answer. ("None" is none of the members of that set, and
    "the second answer" is the stated reason the decoded value cannot show the
    omission.)
  - `TestGatewayKinds_RefusedOmissions` holds each refused field to being
    required in both channels, or, for a field only the experimental channel
    holds (the filter's `protocol`), in that channel. ("each refused field" is
    each of those seven fields; the test is in
    `gateway_kinds_internal_test.go`.)

## tcproute, udproute, tlsroute

- **Implementation:** Each is built on `policyFreeKind` (above), as the kinds of
  the API's infrastructure objects are, and emits that one object in the build
  namespace, named after the component unless `objectName` names it; the handler
  adds no label, no annotation and no default. ("Each" is `tcproute`, `udproute`
  and `tlsroute`; "(above)" is the **storageclass** entry that the entry for
  `gatewayclass`, `gateway`, `listenerset`, `referencegrant` and
  `backendtlspolicy` above names, and which is on the reference page [Kinds:
  core workloads](../../api-reference/oam-components/kinds-core-workloads.md);
  "the kinds of the API's infrastructure objects" are those five kinds; "that
  one object" is the Gateway API object the kind is the projection of.)
- **History:** go-kure/launcher#790 (the kind-named projections).
- **Tests:**
  - `TestGatewayRouteKinds_OmissionsAreTheAPIServers` and
    `TestGatewayRouteKinds_ExpressionRules` go one step further: they run the
    CRD of each channel through the API server's own creation path (defaulting,
    the schema, the expression rules), and hold each refusal of a kind to a
    refusal of that path, and the object the kind emits for an accepted route to
    being accepted there. ("one step further" is further than the reference's
    "the refused omissions are held to them", "they" are the two tests this
    sentence names, and "that path" and "there" are the API server's creation
    path; both tests are in `gateway_route_kinds_internal_test.go`.)
  - The second names every expression rule a CRD declares: the one above, which
    it shows with routes that break it and routes that keep it, or a rule left
    to the API server with its reason. ("The second" is
    `TestGatewayRouteKinds_ExpressionRules`, "a CRD" is a CRD of either channel,
    "the one above" is the reference's rule that a Service backend must name its
    port, and "it" is that test.)
  - A dependency bump that adds or rewrites a rule fails there. ("there" is
    `TestGatewayRouteKinds_ExpressionRules`, the test the previous sentence
    calls "The second".)
