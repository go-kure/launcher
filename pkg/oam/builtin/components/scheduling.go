package components

import (
	"slices"
	"strconv"
	"strings"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/validation"

	"github.com/go-kure/launcher/pkg/errors"
	"github.com/go-kure/launcher/pkg/oam"
)

// This file projects the three raw corev1 pod-level scheduling shapes —
// `affinity`, `topologySpreadConstraints` and (via the pre-existing
// parseTolerations) `tolerations` — for the kind-named `deployment` component
// (go-kure/launcher#412).
//
// These are NOT alternatives to the four-key `affinity` shorthand that
// webservice, worker and statefulset publish (schemaAffinity/parseAffinity in
// schema.go and common.go). The two sit at different levels and the difference
// is the point:
//
//   - The shorthand is one of launcher's own opinions. It takes four keys and
//     evaluates them into a corev1.Affinity whose pod selector is the
//     component's own app label, which is a decision launcher makes on the
//     author's behalf (buildAffinity, common.go). `deployment` deliberately
//     does not carry it — see that kind's doc comment.
//   - What this file publishes is the corev1 field itself, unopinionated and
//     complete. `deployment` is the kind-named projection of appsv1.Deployment,
//     so projecting a PodSpec field as its own API shape is exactly what that
//     kind is for.
//
// The two therefore compose rather than compete: a component-position lowering
// rule evaluates the shorthand and emits a `deployment` component carrying the
// resulting explicit values, which is only possible because the raw shapes are
// published here. A rule may emit only Components and Policies
// (loweringPositionRules, pkg/oam/lowering.go), so there is no side channel
// that would let it attach scheduling state some other way.
//
// House style, per the rest of this package: hand-written per-field parsing
// with rejectUnknownKeys, not a strict decoder — this package has none, and
// this file does not introduce one. Every constraint below is taken from the
// field documentation of the pinned k8s.io/api (core/v1 types.go) and says so
// where it is not self-evident; a rejection that is launcher's own rather than
// the API's is labelled as such.

var (
	affinityKeys                 = []string{"nodeAffinity", "podAffinity", "podAntiAffinity"}
	affinityArmKeys              = []string{"requiredDuringSchedulingIgnoredDuringExecution", "preferredDuringSchedulingIgnoredDuringExecution"}
	nodeSelectorKeys             = []string{"nodeSelectorTerms"}
	nodeSelectorTermKeys         = []string{"matchExpressions", "matchFields"}
	nodeSelectorRequirementKeys  = []string{"key", "operator", "values"}
	preferredSchedulingTermKeys  = []string{"weight", "preference"}
	podAffinityTermKeys          = []string{"labelSelector", "namespaces", "topologyKey", "namespaceSelector", "matchLabelKeys", "mismatchLabelKeys"}
	weightedPodAffinityTermKeys  = []string{"weight", "podAffinityTerm"}
	topologySpreadConstraintKeys = []string{"maxSkew", "topologyKey", "whenUnsatisfiable", "labelSelector", "minDomains", "nodeAffinityPolicy", "nodeTaintsPolicy", "matchLabelKeys"}
)

// requiredArm and preferredArm name the two scheduling arms every affinity kind
// carries. Spelled once so the two long JSON names cannot drift between the
// node-affinity and pod-affinity parsers.
const (
	requiredArm  = "requiredDuringSchedulingIgnoredDuringExecution"
	preferredArm = "preferredDuringSchedulingIgnoredDuringExecution"

	// nodeFieldSelectorKey is the only key a node field selector may use. Upstream
	// spells it metav1.ObjectNameField and registers it as the sole entry of
	// nodeFieldSelectorValidators (pkg/apis/core/validation/validation.go,
	// release-1.36:4993-4995).
	nodeFieldSelectorKey = "metadata.name"
)

// parseRawAffinity parses the `affinity` property as a plain corev1.Affinity.
// An absent key — or an explicit null, this package's null-as-omission
// convention — yields nil, so an unauthored document emits no affinity at all.
func parseRawAffinity(props map[string]any) (*corev1.Affinity, error) {
	raw, present, err := optionalObject(props, "affinity", "affinity")
	if err != nil {
		return nil, err
	}
	if !present {
		return nil, nil
	}
	if err := rejectUnknownKeys(raw, affinityKeys, "affinity"); err != nil {
		return nil, err
	}

	affinity := &corev1.Affinity{}
	if m, present, err := optionalObject(raw, "nodeAffinity", "affinity.nodeAffinity"); err != nil {
		return nil, err
	} else if present {
		na, err := parseNodeAffinity(m, "affinity.nodeAffinity")
		if err != nil {
			return nil, err
		}
		affinity.NodeAffinity = na
	}
	if m, present, err := optionalObject(raw, "podAffinity", "affinity.podAffinity"); err != nil {
		return nil, err
	} else if present {
		pa, err := parsePodAffinityArms(m, "affinity.podAffinity")
		if err != nil {
			return nil, err
		}
		affinity.PodAffinity = &corev1.PodAffinity{
			RequiredDuringSchedulingIgnoredDuringExecution:  pa.required,
			PreferredDuringSchedulingIgnoredDuringExecution: pa.preferred,
		}
	}
	if m, present, err := optionalObject(raw, "podAntiAffinity", "affinity.podAntiAffinity"); err != nil {
		return nil, err
	} else if present {
		pa, err := parsePodAffinityArms(m, "affinity.podAntiAffinity")
		if err != nil {
			return nil, err
		}
		affinity.PodAntiAffinity = &corev1.PodAntiAffinity{
			RequiredDuringSchedulingIgnoredDuringExecution:  pa.required,
			PreferredDuringSchedulingIgnoredDuringExecution: pa.preferred,
		}
	}

	// launcher's rejection, not the API's: an Affinity with every arm unset is
	// accepted upstream and does nothing. Emitting `affinity: {}` into the pod
	// template would be indistinguishable from omitting the key, so an author
	// who wrote `affinity` and got silence has no way to notice the mistake.
	if affinity.NodeAffinity == nil && affinity.PodAffinity == nil && affinity.PodAntiAffinity == nil {
		return nil, errors.New("affinity: set nodeAffinity, podAffinity or podAntiAffinity, or omit the key")
	}
	return affinity, nil
}

func parseNodeAffinity(raw map[string]any, label string) (*corev1.NodeAffinity, error) {
	if err := rejectUnknownKeys(raw, affinityArmKeys, label); err != nil {
		return nil, err
	}
	na := &corev1.NodeAffinity{}
	if m, present, err := optionalObject(raw, requiredArm, label+"."+requiredArm); err != nil {
		return nil, err
	} else if present {
		ns, err := parseNodeSelector(m, label+"."+requiredArm)
		if err != nil {
			return nil, err
		}
		na.RequiredDuringSchedulingIgnoredDuringExecution = ns
	}
	list, present, err := optionalObjectList(raw, preferredArm)
	if err != nil {
		return nil, err
	}
	if present {
		for i, item := range list {
			itemLabel := indexedLabel(label+"."+preferredArm, i)
			if err := rejectUnknownKeys(item, preferredSchedulingTermKeys, itemLabel); err != nil {
				return nil, err
			}
			weight, err := parseSchedulingWeight(item, itemLabel)
			if err != nil {
				return nil, err
			}
			pref, present, err := optionalObject(item, "preference", itemLabel+".preference")
			if err != nil {
				return nil, err
			}
			if !present {
				return nil, errors.Errorf("%s.preference: required", itemLabel)
			}
			term, err := parseNodeSelectorTerm(pref, itemLabel+".preference")
			if err != nil {
				return nil, err
			}
			na.PreferredDuringSchedulingIgnoredDuringExecution = append(
				na.PreferredDuringSchedulingIgnoredDuringExecution,
				corev1.PreferredSchedulingTerm{Weight: weight, Preference: term},
			)
		}
	}
	if na.RequiredDuringSchedulingIgnoredDuringExecution == nil && len(na.PreferredDuringSchedulingIgnoredDuringExecution) == 0 {
		return nil, errors.Errorf("%s: set %s or %s, or omit the key", label, requiredArm, preferredArm)
	}
	return na, nil
}

func parseNodeSelector(raw map[string]any, label string) (*corev1.NodeSelector, error) {
	if err := rejectUnknownKeys(raw, nodeSelectorKeys, label); err != nil {
		return nil, err
	}
	list, present, err := optionalObjectList(raw, "nodeSelectorTerms")
	if err != nil {
		return nil, err
	}
	// NodeSelector.NodeSelectorTerms is documented "Required" in core/v1, and
	// the terms are ORed — an empty list ORs to nothing and matches no node.
	if !present || len(list) == 0 {
		return nil, errors.Errorf("%s.nodeSelectorTerms: at least one term is required", label)
	}
	ns := &corev1.NodeSelector{}
	for i, item := range list {
		term, err := parseNodeSelectorTerm(item, indexedLabel(label+".nodeSelectorTerms", i))
		if err != nil {
			return nil, err
		}
		ns.NodeSelectorTerms = append(ns.NodeSelectorTerms, term)
	}
	return ns, nil
}

func parseNodeSelectorTerm(raw map[string]any, label string) (corev1.NodeSelectorTerm, error) {
	if err := rejectUnknownKeys(raw, nodeSelectorTermKeys, label); err != nil {
		return corev1.NodeSelectorTerm{}, err
	}
	term := corev1.NodeSelectorTerm{}
	// matchExpressions selects on node LABELS, so its keys are qualified names.
	// matchFields selects on node FIELDS (`metadata.name`), which are field
	// paths and not qualified names — hence the different key rule for the two,
	// despite both being NodeSelectorRequirement.
	exprs, err := parseNodeSelectorRequirements(raw, "matchExpressions", label, true)
	if err != nil {
		return corev1.NodeSelectorTerm{}, err
	}
	term.MatchExpressions = exprs
	fields, err := parseNodeSelectorRequirements(raw, "matchFields", label, false)
	if err != nil {
		return corev1.NodeSelectorTerm{}, err
	}
	term.MatchFields = fields
	// launcher's rejection, not the API's: core/v1 documents "A null or empty
	// node selector term matches no objects", so an empty term can only be a
	// mistake — it silently makes the whole OR-ed list unsatisfiable.
	if len(term.MatchExpressions) == 0 && len(term.MatchFields) == 0 {
		return corev1.NodeSelectorTerm{}, errors.Errorf("%s: set matchExpressions or matchFields — an empty node selector term matches no nodes", label)
	}
	return term, nil
}

// parseNodeSelectorRequirements parses a matchExpressions/matchFields list.
// qualifiedKeys says whether the requirement key is a Kubernetes qualified name
// (node labels) or a free-form field path (node fields).
func parseNodeSelectorRequirements(raw map[string]any, key, label string, qualifiedKeys bool) ([]corev1.NodeSelectorRequirement, error) {
	list, present, err := optionalObjectList(raw, key)
	if err != nil {
		return nil, err
	}
	if !present {
		return nil, nil
	}
	var out []corev1.NodeSelectorRequirement
	for i, item := range list {
		itemLabel := indexedLabel(label+"."+key, i)
		if err := rejectUnknownKeys(item, nodeSelectorRequirementKeys, itemLabel); err != nil {
			return nil, err
		}
		var reqKey string
		if qualifiedKeys {
			reqKey, err = requireQualifiedName(item, "key", itemLabel)
			if err != nil {
				return nil, err
			}
		} else {
			var present bool
			reqKey, present, err = parseStringField(item, "key", itemLabel+".key")
			if err != nil {
				return nil, err
			}
			if !present {
				return nil, errors.Errorf("%s.key: required", itemLabel)
			}
			// Node FIELD selectors are not free-form paths, despite the shared
			// NodeSelectorRequirement type. ValidateNodeFieldSelectorRequirement
			// (pkg/apis/core/validation/validation.go, release-1.36:4998-5022) looks
			// the key up in nodeFieldSelectorValidators (:4993-4995), whose only
			// entry is metav1.ObjectNameField — "metadata.name". Anything else is
			// "not a valid field selector key" at admission.
			if reqKey != nodeFieldSelectorKey {
				return nil, errors.Errorf("%s.key: invalid value %q, want %q — it is the only node field selector key Kubernetes accepts",
					itemLabel, reqKey, nodeFieldSelectorKey)
			}
		}
		op, present, err := optionalString(item, "operator", itemLabel+".operator")
		if err != nil {
			return nil, err
		}
		if !present {
			return nil, errors.Errorf("%s.operator: required", itemLabel)
		}
		values, _, err := optionalStringList(item, "values", itemLabel+".values")
		if err != nil {
			return nil, err
		}
		// The two fields share NodeSelectorRequirement but not its rules, so each
		// gets its own operator/arity check — the same split upstream makes between
		// validateNodeSelectorRequirement and ValidateNodeFieldSelectorRequirement.
		// Running the matchExpressions rules on a matchFields entry would emit
		// advice ("want In, NotIn, Exists, …") that is wrong for the field.
		if qualifiedKeys {
			// Arity rules verbatim from NodeSelectorRequirement.Values' field doc:
			// "If the operator is In or NotIn, the values array must be non-empty.
			// If the operator is Exists or DoesNotExist, the values array must be
			// empty. If the operator is Gt or Lt, the values array must have a
			// single element, which will be interpreted as an integer."
			switch corev1.NodeSelectorOperator(op) {
			case corev1.NodeSelectorOpIn, corev1.NodeSelectorOpNotIn:
				if len(values) == 0 {
					return nil, errors.Errorf("%s.values: at least one value is required for operator %s", itemLabel, op)
				}
			case corev1.NodeSelectorOpExists, corev1.NodeSelectorOpDoesNotExist:
				if len(values) > 0 {
					return nil, errors.Errorf("%s.values: must be empty for operator %s", itemLabel, op)
				}
			case corev1.NodeSelectorOpGt, corev1.NodeSelectorOpLt:
				if len(values) != 1 {
					return nil, errors.Errorf("%s.values: exactly one value is required for operator %s, got %d", itemLabel, op, len(values))
				}
				if _, err := strconv.ParseInt(values[0], 10, 64); err != nil {
					return nil, errors.Errorf("%s.values[0]: operator %s requires an integer, got %q", itemLabel, op, values[0])
				}
			default:
				return nil, errors.Errorf("%s.operator: invalid value %q, want In, NotIn, Exists, DoesNotExist, Gt or Lt", itemLabel, op)
			}
		} else {
			// ValidateNodeFieldSelectorRequirement's own switch (validation.go,
			// release-1.36:5001-5009) accepts only In and NotIn, each with exactly
			// one value. Exists/DoesNotExist/Gt/Lt fall to its default branch as
			// "not a valid selector operator", so a matchFields entry the
			// matchExpressions rules would happily build is refused at admission.
			switch corev1.NodeSelectorOperator(op) {
			case corev1.NodeSelectorOpIn, corev1.NodeSelectorOpNotIn:
				if len(values) != 1 {
					return nil, errors.Errorf("%s.values: exactly one value is required for operator %s in matchFields, got %d", itemLabel, op, len(values))
				}
				// The key check above only proves the key RESOLVES in
				// nodeFieldSelectorValidators. Resolving is what arms the value
				// rule: :5011-5019 runs the mapped validator over every entry of
				// req.Values, and the validator mapped to metadata.name is
				// ValidateNodeName (:4993-4994) = apimachinery's
				// NameIsDNSSubdomain. So a syntactically fine key with a value
				// like "bad value" still fails admission. Arity above has already
				// forced exactly one value, which is why this indexes [0] rather
				// than looping as upstream does.
				if errs := validation.IsDNS1123Subdomain(values[0]); len(errs) > 0 {
					return nil, errors.Errorf("%s.values[0]: invalid node name %q: %s",
						itemLabel, values[0], strings.Join(errs, "; "))
				}
			default:
				return nil, errors.Errorf("%s.operator: invalid value %q for matchFields, want In or NotIn — node field selectors accept no other operator", itemLabel, op)
			}
		}
		out = append(out, corev1.NodeSelectorRequirement{
			Key:      reqKey,
			Operator: corev1.NodeSelectorOperator(op),
			Values:   values,
		})
	}
	return out, nil
}

// podAffinityArms is the required/preferred pair shared by podAffinity and
// podAntiAffinity, which are structurally identical.
type podAffinityArms struct {
	required  []corev1.PodAffinityTerm
	preferred []corev1.WeightedPodAffinityTerm
}

func parsePodAffinityArms(raw map[string]any, label string) (podAffinityArms, error) {
	if err := rejectUnknownKeys(raw, affinityArmKeys, label); err != nil {
		return podAffinityArms{}, err
	}
	var arms podAffinityArms

	list, present, err := optionalObjectList(raw, requiredArm)
	if err != nil {
		return podAffinityArms{}, err
	}
	if present {
		for i, item := range list {
			term, err := parsePodAffinityTerm(item, indexedLabel(label+"."+requiredArm, i))
			if err != nil {
				return podAffinityArms{}, err
			}
			arms.required = append(arms.required, term)
		}
	}

	list, present, err = optionalObjectList(raw, preferredArm)
	if err != nil {
		return podAffinityArms{}, err
	}
	if present {
		for i, item := range list {
			itemLabel := indexedLabel(label+"."+preferredArm, i)
			if err := rejectUnknownKeys(item, weightedPodAffinityTermKeys, itemLabel); err != nil {
				return podAffinityArms{}, err
			}
			weight, err := parseSchedulingWeight(item, itemLabel)
			if err != nil {
				return podAffinityArms{}, err
			}
			inner, present, err := optionalObject(item, "podAffinityTerm", itemLabel+".podAffinityTerm")
			if err != nil {
				return podAffinityArms{}, err
			}
			if !present {
				return podAffinityArms{}, errors.Errorf("%s.podAffinityTerm: required", itemLabel)
			}
			term, err := parsePodAffinityTerm(inner, itemLabel+".podAffinityTerm")
			if err != nil {
				return podAffinityArms{}, err
			}
			arms.preferred = append(arms.preferred, corev1.WeightedPodAffinityTerm{
				Weight:          weight,
				PodAffinityTerm: term,
			})
		}
	}

	if len(arms.required) == 0 && len(arms.preferred) == 0 {
		return podAffinityArms{}, errors.Errorf("%s: set %s or %s, or omit the key", label, requiredArm, preferredArm)
	}
	return arms, nil
}

func parsePodAffinityTerm(raw map[string]any, label string) (corev1.PodAffinityTerm, error) {
	if err := rejectUnknownKeys(raw, podAffinityTermKeys, label); err != nil {
		return corev1.PodAffinityTerm{}, err
	}
	term := corev1.PodAffinityTerm{}

	sel, err := parseSchedulingSelector(raw, "labelSelector", label)
	if err != nil {
		return corev1.PodAffinityTerm{}, err
	}
	term.LabelSelector = sel

	nsSel, err := parseSchedulingSelector(raw, "namespaceSelector", label)
	if err != nil {
		return corev1.PodAffinityTerm{}, err
	}
	term.NamespaceSelector = nsSel

	namespaces, _, err := optionalStringList(raw, "namespaces", label+".namespaces")
	if err != nil {
		return corev1.PodAffinityTerm{}, err
	}
	if err := validateNamespaceNames(namespaces, "namespaces", label); err != nil {
		return corev1.PodAffinityTerm{}, err
	}
	term.Namespaces = namespaces

	// "Empty topologyKey is not allowed" (PodAffinityTerm.TopologyKey), and it
	// carries no omitempty, so an unset key would emit `topologyKey: ""` into a
	// document the apiserver then refuses.
	topologyKey, err := requireQualifiedName(raw, "topologyKey", label)
	if err != nil {
		return corev1.PodAffinityTerm{}, err
	}
	term.TopologyKey = topologyKey

	matchLabelKeys, _, err := optionalStringList(raw, "matchLabelKeys", label+".matchLabelKeys")
	if err != nil {
		return corev1.PodAffinityTerm{}, err
	}
	mismatchLabelKeys, _, err := optionalStringList(raw, "mismatchLabelKeys", label+".mismatchLabelKeys")
	if err != nil {
		return corev1.PodAffinityTerm{}, err
	}
	// The two fields do NOT carry the same pair of rules, despite their docs
	// reading as mirrors of each other (k8s.io/api@v0.36.3 core/v1/types.go:3987
	// and :3999 both say "The same key is forbidden to exist in both … and
	// labelSelector"). Upstream validation enforces the selector-overlap rule for
	// matchLabelKeys only: ValidateMatchLabelKeysAndMismatchLabelKeys builds its
	// forbidden-key map from matchLabelKeys alone (pkg/apis/core/validation/
	// validation.go, release-1.36:8989-8992), so a mismatchLabelKeys key that also
	// appears in labelSelector is accepted — deliberately, per that function's own
	// comment at :8983-8985: the key is merged as `NotIn`, so further filtering on
	// the same key still makes sense. Enforcing the doc's wording here would refuse
	// a document the apiserver accepts.
	//
	// What both fields do share is the nil-selector rule, so that one is checked for
	// each.
	if err := checkMatchLabelKeysAgainstSelector(matchLabelKeys, term.LabelSelector, "matchLabelKeys", label); err != nil {
		return corev1.PodAffinityTerm{}, err
	}
	if err := requireLabelSelector(mismatchLabelKeys, term.LabelSelector, "mismatchLabelKeys", label); err != nil {
		return corev1.PodAffinityTerm{}, err
	}
	// Both lists are label KEYS, so both get the qualified-name rule regardless of
	// the selector-overlap asymmetry above. Upstream applies it to each entry of
	// either field via validateLabelKeys -> ValidateLabelName
	// (pkg/apis/core/validation/validation.go, release-1.36:9063-9064).
	if err := validateLabelKeyList(matchLabelKeys, "matchLabelKeys", label); err != nil {
		return corev1.PodAffinityTerm{}, err
	}
	if err := validateLabelKeyList(mismatchLabelKeys, "mismatchLabelKeys", label); err != nil {
		return corev1.PodAffinityTerm{}, err
	}
	term.MatchLabelKeys = matchLabelKeys
	term.MismatchLabelKeys = mismatchLabelKeys

	return term, nil
}

// parseSchedulingSelector parses an optional metav1.LabelSelector at key,
// permitting the empty selector that parseLabelSelector refuses for volume
// claims — see parseLabelSelectorOpts for why the two differ.
func parseSchedulingSelector(raw map[string]any, key, label string) (*metav1.LabelSelector, error) {
	m, present, err := optionalObject(raw, key, label+"."+key)
	if err != nil {
		return nil, err
	}
	if !present {
		return nil, nil
	}
	return parseLabelSelectorOpts(m, label+"."+key, false)
}

// requireLabelSelector enforces the one rule matchLabelKeys and mismatchLabelKeys
// genuinely share: neither may be set without a labelSelector. Upstream applies it
// to both through validateLabelKeys (pkg/apis/core/validation/validation.go,
// release-1.36:9053), which returns Forbidden("must not be specified when
// labelSelector is not set") for a nil selector on either field.
//
// The overlap rule is NOT shared, which is why it is not enforced here — see
// checkMatchLabelKeysAgainstSelector.
func requireLabelSelector(keys []string, sel *metav1.LabelSelector, field, label string) error {
	if len(keys) == 0 {
		return nil
	}
	if sel == nil {
		return errors.Errorf("%s.%s: cannot be set without labelSelector", label, field)
	}
	return nil
}

// checkMatchLabelKeysAgainstSelector rejects a matchLabelKeys entry whose key is
// already constrained by the selector — in matchLabels OR in matchExpressions.
//
// Scanning matchExpressions as well as matchLabels is what upstream rejects, though
// no single upstream function says so on its own; both paths this helper serves reach
// it differently (validation.go line numbers are release-1.36):
//
//   - Topology spread with MatchLabelKeysInPodTopologySpreadSelectorMerge off:
//     ValidateMatchLabelKeysInTopologySpread (:9022) seeds its forbidden set from
//     labelSelector.MatchLabels AND labelSelector.MatchExpressions[].Key, then rejects
//     any matchLabelKey in it. Unconditional.
//   - Topology spread with that gate on (Beta, default true since 1.34), and pod
//     affinity always (MatchLabelKeysInPodAffinity is GA + LockToDefault since 1.33):
//     ValidateMatchLabelKeysAndMismatchLabelKeys (:8973) flags a matchExpressions key
//     only when it was ALREADY in the forbidden set. That reads as permissive in
//     isolation, but it runs after PrepareForCreate has merged each matchLabelKey into
//     the selector as its own `In` requirement (registry/core/pod/strategy.go:860-925).
//     An authored duplicate is therefore the second occurrence of that key and is
//     rejected — whenever the merge happens, i.e. whenever the pod carries the label.
//
// The one configuration that accepts the duplicate is a key naming a label the pod
// does not carry, where no merge occurs — and there matchLabelKeys selects nothing at
// all, so refusing it costs no working document and catches the typo it almost always
// is.
//
// Both fields' own docs state the rule against the whole selector rather than against
// matchLabels: k8s.io/api@v0.36.3 core/v1/types.go:3987 and :4779.
func checkMatchLabelKeysAgainstSelector(keys []string, sel *metav1.LabelSelector, field, label string) error {
	if err := requireLabelSelector(keys, sel, field, label); err != nil {
		return err
	}
	for i, k := range keys {
		if _, clash := sel.MatchLabels[k]; clash {
			return errors.Errorf("%s: key %q is already constrained by labelSelector.matchLabels; the same key may not appear in both", indexedLabel(label+"."+field, i), k)
		}
		for _, req := range sel.MatchExpressions {
			if req.Key == k {
				return errors.Errorf("%s: key %q is already constrained by labelSelector.matchExpressions; the same key may not appear in both", indexedLabel(label+"."+field, i), k)
			}
		}
	}
	return nil
}

// validateNamespaceNames rejects a namespace entry the apiserver would refuse.
// Upstream validates every PodAffinityTerm.Namespaces entry with
// ValidateNamespaceName (pkg/apis/core/validation/validation.go, release-1.36:
// 5162-5163), which is apimachinery's NameIsDNSLabel — the same rule
// IsDNS1123Label applies, already used for namespace-shaped values elsewhere in
// this package (podspec.go:256).
//
// This is the accept-what-upstream-refuses direction, which is why it is enforced
// here while the matchExpressions label-VALUE check was deliberately declined: there
// upstream does not validate at all (it gates the analogous check behind
// AllowInvalidLabelValueInSelector), so refusing would be stricter than the API. Here
// upstream does validate, so accepting builds a document that is dead on arrival.
func validateNamespaceNames(names []string, field, label string) error {
	for i, ns := range names {
		if errs := validation.IsDNS1123Label(ns); len(errs) > 0 {
			return errors.Errorf("%s: invalid namespace name %q: %s",
				indexedLabel(label+"."+field, i), ns, strings.Join(errs, "; "))
		}
	}
	return nil
}

// validateLabelKeyList applies the qualified-name rule to a list of label keys.
// Upstream: validateLabelKeys -> unversionedvalidation.ValidateLabelName per entry
// (validation.go, release-1.36:9063-9064). Mirrors parseLabelMap's key rule
// (podspec.go:719), so both spellings of "this string is a label key" agree.
func validateLabelKeyList(keys []string, field, label string) error {
	for i, k := range keys {
		if errs := validation.IsQualifiedName(k); len(errs) > 0 {
			return errors.Errorf("%s: invalid label key %q: %s",
				indexedLabel(label+"."+field, i), k, strings.Join(errs, "; "))
		}
	}
	return nil
}

// parseSchedulingWeight reads the `weight` field both weighted affinity terms
// carry. Both document the same range: "in the range 1-100"
// (WeightedPodAffinityTerm.Weight, PreferredSchedulingTerm.Weight).
func parseSchedulingWeight(raw map[string]any, label string) (int32, error) {
	weight, present, err := optionalInt32(raw, "weight", label+".weight")
	if err != nil {
		return 0, err
	}
	if !present {
		return 0, errors.Errorf("%s.weight: required", label)
	}
	if weight < 1 || weight > 100 {
		return 0, errors.Errorf("%s.weight: must be between 1 and 100, got %d", label, weight)
	}
	return weight, nil
}

// parseTopologySpreadConstraints parses the `topologySpreadConstraints`
// property as plain []corev1.TopologySpreadConstraint. An absent key — or an
// explicit null — yields nil.
func parseTopologySpreadConstraints(props map[string]any) ([]corev1.TopologySpreadConstraint, error) {
	list, present, err := optionalObjectList(props, "topologySpreadConstraints")
	if err != nil {
		return nil, err
	}
	if !present {
		return nil, nil
	}
	var out []corev1.TopologySpreadConstraint
	for i, item := range list {
		label := indexedLabel("topologySpreadConstraints", i)
		if err := rejectUnknownKeys(item, topologySpreadConstraintKeys, label); err != nil {
			return nil, err
		}
		tsc := corev1.TopologySpreadConstraint{}

		// "It's a required field. Default value is 1 and 0 is not allowed."
		// Required here rather than defaulted: nothing in this package defaults
		// it, MaxSkew carries no omitempty, and an unset value would emit
		// `maxSkew: 0` — the one value the field doc rules out.
		maxSkew, present, err := optionalInt32(item, "maxSkew", label+".maxSkew")
		if err != nil {
			return nil, err
		}
		if !present {
			return nil, errors.Errorf("%s.maxSkew: required", label)
		}
		if maxSkew < 1 {
			return nil, errors.Errorf("%s.maxSkew: must be greater than 0, got %d", label, maxSkew)
		}
		tsc.MaxSkew = maxSkew

		// "It's a required field", and no omitempty.
		topologyKey, err := requireQualifiedName(item, "topologyKey", label)
		if err != nil {
			return nil, err
		}
		tsc.TopologyKey = topologyKey

		// "It's a required field", and no omitempty. The doc calls
		// DoNotSchedule the default, but that default is the apiserver's; an
		// unset value here would emit `whenUnsatisfiable: ""`, which is not it.
		action, present, err := optionalString(item, "whenUnsatisfiable", label+".whenUnsatisfiable")
		if err != nil {
			return nil, err
		}
		if !present {
			return nil, errors.Errorf("%s.whenUnsatisfiable: required", label)
		}
		switch corev1.UnsatisfiableConstraintAction(action) {
		case corev1.DoNotSchedule, corev1.ScheduleAnyway:
			tsc.WhenUnsatisfiable = corev1.UnsatisfiableConstraintAction(action)
		default:
			return nil, errors.Errorf("%s.whenUnsatisfiable: invalid value %q, want DoNotSchedule or ScheduleAnyway", label, action)
		}

		sel, err := parseSchedulingSelector(item, "labelSelector", label)
		if err != nil {
			return nil, err
		}
		tsc.LabelSelector = sel

		// "Valid values are integers greater than 0. When value is not nil,
		// WhenUnsatisfiable must be DoNotSchedule."
		if minDomains, present, err := optionalInt32(item, "minDomains", label+".minDomains"); err != nil {
			return nil, err
		} else if present {
			if minDomains < 1 {
				return nil, errors.Errorf("%s.minDomains: must be greater than 0, got %d", label, minDomains)
			}
			if tsc.WhenUnsatisfiable != corev1.DoNotSchedule {
				return nil, errors.Errorf("%s.minDomains: requires whenUnsatisfiable DoNotSchedule, got %s", label, tsc.WhenUnsatisfiable)
			}
			tsc.MinDomains = &minDomains
		}

		if policy, err := parseNodeInclusionPolicy(item, "nodeAffinityPolicy", label); err != nil {
			return nil, err
		} else if policy != nil {
			tsc.NodeAffinityPolicy = policy
		}
		if policy, err := parseNodeInclusionPolicy(item, "nodeTaintsPolicy", label); err != nil {
			return nil, err
		} else if policy != nil {
			tsc.NodeTaintsPolicy = policy
		}

		// "The same key is forbidden to exist in both MatchLabelKeys and
		// LabelSelector. MatchLabelKeys cannot be set when LabelSelector isn't
		// set." (k8s.io/api@v0.36.3 core/v1/types.go:4779.) Same rules as
		// matchLabelKeys on a PodAffinityTerm — a TopologySpreadConstraint has no
		// mismatchLabelKeys field, so only the one call is needed.
		matchLabelKeys, _, err := optionalStringList(item, "matchLabelKeys", label+".matchLabelKeys")
		if err != nil {
			return nil, err
		}
		if err := checkMatchLabelKeysAgainstSelector(matchLabelKeys, tsc.LabelSelector, "matchLabelKeys", label); err != nil {
			return nil, err
		}
		// Same label-KEY rule as the PodAffinityTerm lists: upstream runs every
		// entry through validateLabelKeys -> ValidateLabelName (validation.go,
		// release-1.36:9063-9064) regardless of which field carries it.
		if err := validateLabelKeyList(matchLabelKeys, "matchLabelKeys", label); err != nil {
			return nil, err
		}
		tsc.MatchLabelKeys = matchLabelKeys

		// The duplicate rule is on the PAIR, not on topologyKey alone:
		// ValidateSpreadConstraintNotRepeat (validation.go, release-1.36:8943-8951)
		// rejects only when BOTH TopologyKey and WhenUnsatisfiable match an earlier
		// entry. Two constraints on the same topologyKey with different
		// whenUnsatisfiable values are legal, so a topologyKey-uniqueness check here
		// would be stricter than the API — the same error that got the
		// matchExpressions label-value check rejected in an earlier round.
		for j, prev := range out {
			if prev.TopologyKey == tsc.TopologyKey && prev.WhenUnsatisfiable == tsc.WhenUnsatisfiable {
				return nil, errors.Errorf("%s: duplicate constraint {%s, %s}, already declared at %s",
					label, tsc.TopologyKey, tsc.WhenUnsatisfiable, indexedLabel("topologySpreadConstraints", j))
			}
		}

		out = append(out, tsc)
	}
	return out, nil
}

// nodeInclusionPolicies is the closed set both TopologySpreadConstraint policy
// fields take.
var nodeInclusionPolicies = []corev1.NodeInclusionPolicy{
	corev1.NodeInclusionPolicyHonor,
	corev1.NodeInclusionPolicyIgnore,
}

func parseNodeInclusionPolicy(raw map[string]any, key, label string) (*corev1.NodeInclusionPolicy, error) {
	v, present, err := optionalString(raw, key, label+"."+key)
	if err != nil {
		return nil, err
	}
	if !present {
		return nil, nil
	}
	policy := corev1.NodeInclusionPolicy(v)
	if !slices.Contains(nodeInclusionPolicies, policy) {
		return nil, errors.Errorf("%s.%s: invalid value %q, want Honor or Ignore", label, key, v)
	}
	return &policy, nil
}

// schemaRawAffinity describes the raw `affinity` property (see
// parseRawAffinity). Distinct from schemaAffinity, which describes the four-key
// shorthand the opinionated kinds publish — see this file's header.
func schemaRawAffinity() oam.PropertySchema {
	return oam.PropertySchema{
		Type:        oam.PropertyTypeObject,
		Description: "Pod scheduling affinity, as the corev1.Affinity API shape. Unlike the four-key `affinity` shorthand on webservice/worker/statefulset, nothing is inferred from the component: every selector is authored.",
		Properties: map[string]oam.PropertySchema{
			"nodeAffinity": {
				Type:        oam.PropertyTypeObject,
				Description: "Constrains which nodes the pod can be scheduled onto, by node label.",
				Properties: map[string]oam.PropertySchema{
					requiredArm: {
						Type:        oam.PropertyTypeObject,
						Description: "Hard requirement: the pod is not scheduled unless a node matches. Terms are ORed.",
						Properties: map[string]oam.PropertySchema{
							"nodeSelectorTerms": {
								Type:        oam.PropertyTypeArray,
								Required:    true,
								Description: "At least one term is required; a pod may schedule where any term matches.",
								Items:       ptrSchema(schemaNodeSelectorTerm()),
							},
						},
					},
					preferredArm: {
						Type:        oam.PropertyTypeArray,
						Description: "Soft preference: nodes matching more weight are preferred, but a non-matching node is still eligible.",
						Items: &oam.PropertySchema{
							Type:        oam.PropertyTypeObject,
							Description: "A weighted node selector term.",
							Properties: map[string]oam.PropertySchema{
								"weight":     schemaSchedulingWeight(),
								"preference": requiredSchema(schemaNodeSelectorTerm()),
							},
						},
					},
				},
			},
			"podAffinity":     schemaPodAffinityArms("Co-locates this pod with pods matching the term, in the same topology domain."),
			"podAntiAffinity": schemaPodAffinityArms("Keeps this pod away from pods matching the term, across topology domains."),
		},
	}
}

func schemaPodAffinityArms(description string) oam.PropertySchema {
	return oam.PropertySchema{
		Type:        oam.PropertyTypeObject,
		Description: description,
		Properties: map[string]oam.PropertySchema{
			requiredArm: {
				Type:        oam.PropertyTypeArray,
				Description: "Hard requirement: the pod is not scheduled unless every term is satisfied.",
				Items:       ptrSchema(schemaPodAffinityTerm()),
			},
			preferredArm: {
				Type:        oam.PropertyTypeArray,
				Description: "Soft preference: terms matching more weight are preferred, but an unsatisfied term does not block scheduling.",
				Items: &oam.PropertySchema{
					Type:        oam.PropertyTypeObject,
					Description: "A weighted pod affinity term.",
					Properties: map[string]oam.PropertySchema{
						"weight":          schemaSchedulingWeight(),
						"podAffinityTerm": requiredSchema(schemaPodAffinityTerm()),
					},
				},
			},
		},
	}
}

func schemaNodeSelectorTerm() oam.PropertySchema {
	return oam.PropertySchema{
		Type:        oam.PropertyTypeObject,
		Description: "A set of node requirements, ANDed. An empty term matches no nodes and is rejected.",
		Properties: map[string]oam.PropertySchema{
			"matchExpressions": {
				Type:        oam.PropertyTypeArray,
				Description: "Requirements against node labels.",
				Items:       ptrSchema(schemaNodeSelectorRequirement()),
			},
			"matchFields": {
				Type:        oam.PropertyTypeArray,
				Description: "Requirements against node fields. `metadata.name` is the only key Kubernetes accepts, with operator In or NotIn and exactly one value.",
				Items:       ptrSchema(schemaNodeFieldSelectorRequirement()),
			},
		},
	}
}

// schemaNodeSelectorRequirement is matchExpressions' item schema. matchFields has
// its own, narrower one — see schemaNodeFieldSelectorRequirement for why the two
// cannot share this despite sharing the NodeSelectorRequirement type.
func schemaNodeSelectorRequirement() oam.PropertySchema {
	return oam.PropertySchema{
		Type:        oam.PropertyTypeObject,
		Description: "A single node requirement.",
		Properties: map[string]oam.PropertySchema{
			"key":      {Type: oam.PropertyTypeString, Required: true, Description: "Node label key."},
			"operator": {Type: oam.PropertyTypeString, Required: true, Enum: []any{"In", "NotIn", "Exists", "DoesNotExist", "Gt", "Lt"}, Description: "How the key relates to values. In/NotIn need at least one value, Exists/DoesNotExist none, Gt/Lt exactly one integer."},
			"values":   schemaStringArray(),
		},
	}
}

// schemaNodeFieldSelectorRequirement is matchFields' own item schema, deliberately
// narrower than schemaNodeSelectorRequirement.
//
// The two fields share the NodeSelectorRequirement type but not its rules, and the
// parser enforces that split (parseNodeSelectorRequirements' qualifiedKeys=false
// branch): only key `metadata.name`, only operator In or NotIn, exactly one value,
// and that value a node name. Reusing the generic requirement schema here would
// leave the schema advertising `Exists`, `DoesNotExist`, `Gt` and `Lt` — and any key
// at all — as valid for matchFields.
//
// That gap is reachable, not theoretical. Emitted properties are validated against
// this schema before handler conversion, Enum included, recursively through arrays
// and objects (pkg/oam/property_validate.go:114-175). So a lowering rule emitting
// `matchFields.operator: Exists` would pass emission validation on the strength of
// the generic enum and then fail conversion in the parser — schema and parser
// disagreeing about the same document, which is the defect class this mirrors from
// the other direction.
//
// PropertySchema can express PRESENCE but not CARDINALITY, and the two must not be
// conflated: `values` is Required here — every operator matchFields accepts needs
// it, unlike matchExpressions where Exists and DoesNotExist take none — while
// "exactly one" is not expressible and lives in the descriptions, enforced by the
// parser alone. Leaving `values` optional because its arity rule had to go in the
// parser anyway would reopen the same divergence in a second place: an emitted
// `{key: metadata.name, operator: In}` with no values would clear emission
// validation (validateObjectProperties skips presence checks on non-required keys,
// pkg/oam/property_validate.go:53-66) and then fail conversion below.
func schemaNodeFieldSelectorRequirement() oam.PropertySchema {
	return oam.PropertySchema{
		Type:        oam.PropertyTypeObject,
		Description: "A single node field requirement.",
		Properties: map[string]oam.PropertySchema{
			"key": {
				Type: oam.PropertyTypeString, Required: true,
				Enum:        []any{nodeFieldSelectorKey},
				Description: "Node field path. `" + nodeFieldSelectorKey + "` is the only one Kubernetes accepts.",
			},
			"operator": {
				Type: oam.PropertyTypeString, Required: true,
				Enum:        []any{"In", "NotIn"},
				Description: "How the key relates to values. Node field selectors accept In and NotIn only, each with exactly one value.",
			},
			"values": {
				Type: oam.PropertyTypeArray, Required: true,
				Description: "Exactly one node name, validated as a DNS-1123 subdomain.",
				Items:       &oam.PropertySchema{Type: oam.PropertyTypeString, Description: "A node name."},
			},
		},
	}
}

func schemaPodAffinityTerm() oam.PropertySchema {
	return oam.PropertySchema{
		Type:        oam.PropertyTypeObject,
		Description: "Selects the pods this term is evaluated against, and the topology domain it is evaluated over.",
		Properties: map[string]oam.PropertySchema{
			"labelSelector":     schemaSchedulingSelector("Label query over pods. An empty selector matches every pod in scope; omitting the key matches none."),
			"namespaces":        schemaStringArray(),
			"topologyKey":       {Type: oam.PropertyTypeString, Required: true, Description: "Node label key defining the topology domain, e.g. `kubernetes.io/hostname`. May not be empty."},
			"namespaceSelector": schemaSchedulingSelector("Label query over namespaces, unioned with `namespaces`. An empty selector matches all namespaces."),
			"matchLabelKeys":    schemaStringArray(),
			"mismatchLabelKeys": schemaStringArray(),
		},
	}
}

// schemaTopologySpreadConstraints describes the raw
// `topologySpreadConstraints` property (see parseTopologySpreadConstraints).
func schemaTopologySpreadConstraints() oam.PropertySchema {
	return oam.PropertySchema{
		Type:        oam.PropertyTypeArray,
		Description: "How pods of this workload are spread across topology domains, as the corev1.TopologySpreadConstraint API shape.",
		Items: &oam.PropertySchema{
			Type:        oam.PropertyTypeObject,
			Description: "A single topology spread constraint.",
			Properties: map[string]oam.PropertySchema{
				"maxSkew":            {Type: oam.PropertyTypeInteger, Required: true, Description: "Maximum permitted difference between the number of matching pods in any domain and the global minimum. Must be greater than 0."},
				"topologyKey":        {Type: oam.PropertyTypeString, Required: true, Description: "Node label key defining the domain, e.g. `topology.kubernetes.io/zone`."},
				"whenUnsatisfiable":  {Type: oam.PropertyTypeString, Required: true, Enum: []any{"DoNotSchedule", "ScheduleAnyway"}, Description: "What the scheduler does with a pod that would violate the constraint."},
				"labelSelector":      schemaSchedulingSelector("Label query selecting the pods counted per domain."),
				"minDomains":         {Type: oam.PropertyTypeInteger, Description: "Minimum number of eligible domains. Must be greater than 0, and requires whenUnsatisfiable DoNotSchedule."},
				"nodeAffinityPolicy": {Type: oam.PropertyTypeString, Enum: []any{"Honor", "Ignore"}, Description: "Whether the pod's nodeAffinity/nodeSelector narrows the eligible domains. Unset behaves as Honor."},
				"nodeTaintsPolicy":   {Type: oam.PropertyTypeString, Enum: []any{"Honor", "Ignore"}, Description: "Whether node taints narrow the eligible domains. Unset behaves as Ignore."},
				"matchLabelKeys":     schemaStringArray(),
			},
		},
	}
}

func schemaSchedulingSelector(description string) oam.PropertySchema {
	return oam.PropertySchema{
		Type:        oam.PropertyTypeObject,
		Description: description,
		Properties: map[string]oam.PropertySchema{
			"matchLabels": {Type: oam.PropertyTypeObject, AdditionalProperties: true, Description: "Label key/value pairs that must all match."},
			"matchExpressions": {
				Type:        oam.PropertyTypeArray,
				Description: "Set-based label requirements, ANDed with matchLabels.",
				Items: &oam.PropertySchema{
					Type:        oam.PropertyTypeObject,
					Description: "A single label requirement.",
					Properties: map[string]oam.PropertySchema{
						"key":      {Type: oam.PropertyTypeString, Required: true, Description: "Label key."},
						"operator": {Type: oam.PropertyTypeString, Required: true, Enum: []any{"In", "NotIn", "Exists", "DoesNotExist"}, Description: "How the key relates to values. In/NotIn need at least one value, Exists/DoesNotExist none."},
						"values":   schemaStringArray(),
					},
				},
			},
		},
	}
}

func schemaSchedulingWeight() oam.PropertySchema {
	return oam.PropertySchema{Type: oam.PropertyTypeInteger, Required: true, Description: "Relative weight of this term, between 1 and 100."}
}

// ptrSchema returns a pointer to a copy of s, for the Items field.
func ptrSchema(s oam.PropertySchema) *oam.PropertySchema { return &s }

// requiredSchema marks a composed sub-schema required. Needed because the three
// call sites take their schema from a shared constructor, so `Required: true`
// cannot be written inline the way it is on the scalar fields in this file.
//
// It declares a rejection that already exists rather than adding one: the parser
// errors on each of these being absent (preference at scheduling.go:156-162,
// nodeSelectorTerms at :183-190, podAffinityTerm at :349-355). Requiredness is
// read only from PropertySchema.Required (property_validate.go:54-68), so
// leaving it unset advertised the field as optional to any external validator
// while the handler rejected it anyway — and `weight`, its sibling in the same
// struct literal, has always been Required.
func requiredSchema(s oam.PropertySchema) oam.PropertySchema { s.Required = true; return s }
