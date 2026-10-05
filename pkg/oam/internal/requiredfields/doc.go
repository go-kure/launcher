// Package requiredfields refuses a field the API requires that a document left
// out, where the upstream Go type would hide the omission, and holds the
// required list of a Cilium policy rule.
//
// An upstream API type writes some of its fields whether or not they were
// authored: a struct that is no pointer, a list or a scalar without omitempty.
// A document that leaves such a field out decodes cleanly and is emitted with
// the type's empty value in its place. Where the API requires the field, the
// object no longer shows that it was left out, and the API server either
// refuses the empty value at apply or accepts it with a meaning nobody wrote.
// Refuse reads what was authored instead of what was decoded, against a list of
// the paths that must be authored wherever their parent is.
//
// The package is internal because two packages read the same list: the kind
// components under pkg/oam/builtin/components and the cilium-networkpolicy
// trait under pkg/oam/builtin/traits. Both decode into Cilium's rule type, so
// both need its required list, CiliumRule; the trait publishes three of a
// rule's fields and reads CiliumTraitRule. The kinds' own lists stay with the
// kinds.
package requiredfields
