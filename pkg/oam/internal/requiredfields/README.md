# Required Fields

Package `requiredfields` (internal to `pkg/oam`) refuses a field the API requires
that a document left out, where the upstream Go type would hide the omission. It
also holds the required list of a Cilium policy rule, which the Cilium policy
kinds and the `cilium-networkpolicy` trait both read.

An upstream API type writes some of its fields whether or not they were authored:
a struct that is no pointer, a list or a scalar without `omitempty`. A document
that leaves such a field out decodes cleanly and is emitted with the type's empty
value in its place (`operator: ""`, `secret: null`, `priority: 0`). Where the API
requires the field, the object no longer shows the omission: the API server
refuses the empty value at apply, or accepts it with a meaning nobody wrote. The
decoded value cannot tell an authored empty value from none, so the check reads
what was authored.

- `Refuse(authored, required)` walks `authored`, a property tree of string-keyed
  maps, lists and scalars in which a field authored as null is absent, along each
  path of `required`, and returns `<path>: required (<what the field is>)` for
  the first field left out where its parent is authored. A path is a json path
  with `[]` for a list element (`ingress[].authentication.mode`); the error names
  the element by its index (`ingress[0].authentication.mode`). Under a parent the
  author left out nothing is required. Keys match case-insensitively, as the
  decode's do. Where a document writes one field in two spellings
  (`fromEndpoints` and `FromEndpoints`) the decode keeps one or merges both, so
  each spelling is held to the list and the error names the one that leaves the
  field out. Paths are read in sorted order, the spellings of a field in theirs
  and a list in its own, so the field reported is the same on every build.
- `CiliumSelector(at)` is the required list of one Cilium label selector under
  `at`: the `key` and `operator` of each match expression.
- `CiliumRule(at)` is the required list of one Cilium policy rule under `at`
  (`spec`, `specs[]`, or `""` for paths from the rule itself): every field the
  CiliumNetworkPolicy and CiliumClusterwideNetworkPolicy CRDs require inside a
  rule that the rule type writes unauthored, and two optional ones it writes with
  a value the API refuses (a listener's `priority` as 0, the `kind` of its Envoy
  configuration as the empty string).
- `CiliumTraitFields()` names the rule fields the `cilium-networkpolicy` trait
  publishes (`endpointSelector`, `ingress`, `egress`), and `CiliumTraitRule()` is
  `CiliumRule("")` cut to the paths under them.

The lists are not hand-kept against the API: tests beside the component kinds
derive them from the CRDs the linked Cilium module ships and from the rule type,
and fail when a CRD requires a field a list lacks
(`TestCiliumNetworkPolicy_RequiredMatchCRD`,
`TestCiliumClusterwideNetworkPolicy_RequiredMatchCRD`,
`TestCiliumNetworkPolicyTrait_RequiredMatchCRD`).

The other kinds' required lists stay with the kinds, in
`pkg/oam/builtin/components`; they read `Refuse` through that package's own
wrapper.
