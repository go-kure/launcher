package components

import (
	"slices"
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	"github.com/go-kure/launcher/pkg/oam"
)

// deploymentSchemaKeysAt returns the property names the schema publishes at a
// dot-separated path, e.g. "strategy.rollingUpdate".
func deploymentSchemaKeysAt(t *testing.T, root map[string]oam.PropertySchema, path string) []string {
	t.Helper()
	cur := root
	for _, seg := range strings.Split(path, ".") {
		s, ok := cur[seg]
		if !ok {
			t.Fatalf("schema has no property at %q (missing segment %q)", path, seg)
		}
		cur = s.Properties
	}
	keys := make([]string, 0, len(cur))
	for k := range cur {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

func assertDeploymentKeysAt(t *testing.T, root map[string]oam.PropertySchema, path string, want []string) {
	t.Helper()
	sorted := slices.Clone(want)
	slices.Sort(sorted)
	if got := deploymentSchemaKeysAt(t, root, path); !slices.Equal(got, sorted) {
		t.Errorf("schema keys at %q = %v, want %v", path, got, sorted)
	}
}

// TestDeploymentSpecSchemaMatchesParser pins the published schema to the key
// slices the parser rejects against, at every nesting level. Without it a key
// added to one half and not the other reads as correct at both: the schema
// consumer accepts a document the parser then refuses, or the parser accepts a
// key no schema documents.
func TestDeploymentSpecSchemaMatchesParser(t *testing.T) {
	s := schemaDeploymentSpec()

	top := make([]string, 0, len(s))
	for k := range s {
		top = append(top, k)
	}
	slices.Sort(top)
	want := slices.Clone(deploymentSpecPropertyKeys)
	slices.Sort(want)
	if !slices.Equal(top, want) {
		t.Errorf("schemaDeploymentSpec keys = %v, want %v", top, want)
	}

	assertDeploymentKeysAt(t, s, "strategy", deploymentStrategyKeys)
	assertDeploymentKeysAt(t, s, "strategy.rollingUpdate", deploymentRollingUpdateKeys)

	// Pinning the key SETS leaves the per-key contract unpinned: dropping
	// Required from strategy.type would publish a schema saying the key is
	// optional while the parser still demands it, and the key-set checks above
	// would stay green.
	typ := s["strategy"].Properties["type"]
	if !typ.Required {
		t.Error("strategy.type.Required = false, want true — the parser demands it")
	}
	wantEnum := []any{
		string(appsv1.RecreateDeploymentStrategyType),
		string(appsv1.RollingUpdateDeploymentStrategyType),
	}
	if !slices.Equal(typ.Enum, wantEnum) {
		t.Errorf("strategy.type.Enum = %v, want %v", typ.Enum, wantEnum)
	}

	// Neither rolling-update knob may declare a Type. parseDeploymentIntOrPercent
	// takes a non-negative integer as well as a percentage string, and
	// validatePropertyValue rejects a non-string outright once Type is
	// PropertyTypeString — so a declared type does not merely understate what is
	// accepted, it makes the parser's own integer branch unreachable through a
	// schema-validating consumer. Type "" is the only declaration that leaves
	// both forms reachable.
	for _, k := range deploymentRollingUpdateKeys {
		if got := s["strategy"].Properties["rollingUpdate"].Properties[k].Type; got != "" {
			t.Errorf("strategy.rollingUpdate.%s declares Type %q; want it unset so the integer form the parser accepts is not rejected before the handler runs", k, got)
		}
	}

	// strategy.type is the ONLY required leaf anywhere in the fragment. Walked
	// recursively rather than spelled out: a hand-listed set silently stops
	// covering a leaf added later, and a Required leaf the parser does not
	// enforce rejects documents the parser accepts.
	var walkRequired func(prefix string, m map[string]oam.PropertySchema)
	walkRequired = func(prefix string, m map[string]oam.PropertySchema) {
		for k, v := range m {
			path := prefix + k
			if v.Required && path != "strategy.type" {
				t.Errorf("%s is marked Required; the parser enforces requiredness only on strategy.type", path)
			}
			walkRequired(path+".", v.Properties)
		}
	}
	walkRequired("", s)
}

// TestParseDeploymentSpec_Empty asserts the zero document leaves every field
// unauthored, which is what keeps output for existing-shaped documents
// unchanged: apply writes nothing.
func TestParseDeploymentSpec_Empty(t *testing.T) {
	cfg, err := parseDeploymentSpec(map[string]any{})
	if err != nil {
		t.Fatalf("parseDeploymentSpec: %v", err)
	}
	if cfg.Strategy != nil || cfg.MinReadySeconds != nil || cfg.RevisionHistoryLimit != nil ||
		cfg.Paused != nil || cfg.ProgressDeadlineSeconds != nil {
		t.Errorf("parseDeploymentSpec({}) = %+v, want every field nil", cfg)
	}

	dep := &appsv1.Deployment{}
	dep.Spec.MinReadySeconds = 7
	cfg.apply(dep)
	if dep.Spec.MinReadySeconds != 7 {
		t.Errorf("apply on an empty config overwrote MinReadySeconds: got %d, want 7", dep.Spec.MinReadySeconds)
	}
}

// TestParseDeploymentSpec_RoundTrip walks every field from document to applied
// Deployment.
func TestParseDeploymentSpec_RoundTrip(t *testing.T) {
	cfg, err := parseDeploymentSpec(map[string]any{
		"strategy": map[string]any{
			"type": "RollingUpdate",
			"rollingUpdate": map[string]any{
				"maxUnavailable": 1,
				"maxSurge":       "50%",
			},
		},
		"minReadySeconds":         10,
		"revisionHistoryLimit":    3,
		"paused":                  true,
		"progressDeadlineSeconds": 900,
	})
	if err != nil {
		t.Fatalf("parseDeploymentSpec: %v", err)
	}

	dep := &appsv1.Deployment{}
	cfg.apply(dep)

	if got := dep.Spec.Strategy.Type; got != appsv1.RollingUpdateDeploymentStrategyType {
		t.Errorf("Strategy.Type = %q, want RollingUpdate", got)
	}
	ru := dep.Spec.Strategy.RollingUpdate
	if ru == nil {
		t.Fatal("Strategy.RollingUpdate is nil")
	}
	if got := *ru.MaxUnavailable; got != intstr.FromInt32(1) {
		t.Errorf("MaxUnavailable = %v, want the integer 1", got)
	}
	if got := *ru.MaxSurge; got != intstr.FromString("50%") {
		t.Errorf("MaxSurge = %v, want the string \"50%%\"", got)
	}
	if dep.Spec.MinReadySeconds != 10 {
		t.Errorf("MinReadySeconds = %d, want 10", dep.Spec.MinReadySeconds)
	}
	if dep.Spec.RevisionHistoryLimit == nil || *dep.Spec.RevisionHistoryLimit != 3 {
		t.Errorf("RevisionHistoryLimit = %v, want 3", dep.Spec.RevisionHistoryLimit)
	}
	if !dep.Spec.Paused {
		t.Error("Paused = false, want true")
	}
	if dep.Spec.ProgressDeadlineSeconds == nil || *dep.Spec.ProgressDeadlineSeconds != 900 {
		t.Errorf("ProgressDeadlineSeconds = %v, want 900", dep.Spec.ProgressDeadlineSeconds)
	}
}

// TestParseDeploymentSpec_StrategyRecreate covers the branch where
// rollingUpdate is absent and the type is the non-rolling one.
func TestParseDeploymentSpec_StrategyRecreate(t *testing.T) {
	cfg, err := parseDeploymentSpec(map[string]any{
		"strategy": map[string]any{"type": "Recreate"},
	})
	if err != nil {
		t.Fatalf("parseDeploymentSpec: %v", err)
	}
	if cfg.Strategy.Type != appsv1.RecreateDeploymentStrategyType {
		t.Errorf("Strategy.Type = %q, want Recreate", cfg.Strategy.Type)
	}
	if cfg.Strategy.RollingUpdate != nil {
		t.Errorf("Strategy.RollingUpdate = %+v, want nil", cfg.Strategy.RollingUpdate)
	}
}

// TestParseDeploymentSpec_RollingUpdateOmitted asserts `type: RollingUpdate`
// alone is accepted, matching parseDeploymentStrategy's documented laxity:
// ValidateDeploymentStrategy wants a non-nil rollingUpdate under RollingUpdate,
// but apiserver defaulting supplies it, not the author.
func TestParseDeploymentSpec_RollingUpdateOmitted(t *testing.T) {
	cfg, err := parseDeploymentSpec(map[string]any{
		"strategy": map[string]any{"type": "RollingUpdate"},
	})
	if err != nil {
		t.Fatalf("parseDeploymentSpec: %v", err)
	}
	if cfg.Strategy.RollingUpdate != nil {
		t.Errorf("Strategy.RollingUpdate = %+v, want nil so the API defaults apply", cfg.Strategy.RollingUpdate)
	}
}

// TestParseDeploymentSpec_SingleAuthoredZeroIsAccepted is the counterpart to the
// both-zero rejection below, and pins the reason the rejection is narrow.
// SetDefaults_Deployment guards MaxUnavailable and MaxSurge with independent
// `== nil` checks, so authoring one knob does not suppress the other's 25%
// default: a lone zero always leaves the pair with one non-zero half, and
// upstream accepts it. Widening the rule to "any authored zero" would refuse
// documents the apiserver takes.
func TestParseDeploymentSpec_SingleAuthoredZeroIsAccepted(t *testing.T) {
	for _, key := range deploymentRollingUpdateKeys {
		t.Run(key, func(t *testing.T) {
			cfg, err := parseDeploymentSpec(map[string]any{
				"strategy": map[string]any{
					"type":          "RollingUpdate",
					"rollingUpdate": map[string]any{key: 0},
				},
			})
			if err != nil {
				t.Fatalf("parseDeploymentSpec with a lone %s: 0 = %v, want it accepted", key, err)
			}
			if cfg.Strategy.RollingUpdate == nil {
				t.Fatal("Strategy.RollingUpdate is nil")
			}
		})
	}
}

// TestParseDeploymentSpec_MaxSurgeIsNotCappedAt100 pins the asymmetry against
// the DaemonSet kind: ValidateRollingUpdateDeployment calls
// IsNotMoreThan100Percent on maxUnavailable only, so a surge above 100% is a
// legal Deployment and must not be refused here.
func TestParseDeploymentSpec_MaxSurgeIsNotCappedAt100(t *testing.T) {
	cfg, err := parseDeploymentSpec(map[string]any{
		"strategy": map[string]any{
			"type":          "RollingUpdate",
			"rollingUpdate": map[string]any{"maxSurge": "200%"},
		},
	})
	if err != nil {
		t.Fatalf("parseDeploymentSpec with maxSurge 200%%: %v, want it accepted", err)
	}
	if got := *cfg.Strategy.RollingUpdate.MaxSurge; got != intstr.FromString("200%") {
		t.Errorf("MaxSurge = %v, want the string \"200%%\"", got)
	}
}

// TestParseDeploymentSpec_PercentageMustBeRepresentable covers the other half of
// that asymmetry. Uncapped is not unbounded: IsValidPercent's ^[0-9]+%$ accepts
// any run of digits, so a percentage no strconv.Atoi can read back passes the
// form check and is carried into the object verbatim, where the rollout
// controller's own conversion is the first thing to see it — long after this
// component is out of the message. The ceiling stays on maxUnavailable alone;
// the representability check applies to both, at exactly the boundary
// getIntOrPercentValueSafely hits, so nothing upstream accepts is refused.
func TestParseDeploymentSpec_PercentageMustBeRepresentable(t *testing.T) {
	const overlong = "999999999999999999999999999%"

	for _, key := range []string{"maxSurge", "maxUnavailable"} {
		t.Run(key, func(t *testing.T) {
			_, err := parseDeploymentSpec(map[string]any{
				"strategy": map[string]any{
					"type":          "RollingUpdate",
					"rollingUpdate": map[string]any{key: overlong},
				},
			})
			if err == nil {
				t.Fatalf("%s: %s was accepted, want it refused as unrepresentable", key, overlong)
			}
			if !strings.Contains(err.Error(), "too large to be represented") {
				t.Errorf("%s: error = %q, want it to name the value as unrepresentable", key, err.Error())
			}
			if !strings.Contains(err.Error(), "strategy.rollingUpdate."+key) {
				t.Errorf("%s: error = %q, want it to name the field", key, err.Error())
			}
		})
	}

	// The accepting side, so a helper that refused every percentage could not
	// pass: a very large but convertible value, on the uncapped knob.
	t.Run("a large representable percentage is accepted", func(t *testing.T) {
		cfg, err := parseDeploymentSpec(map[string]any{
			"strategy": map[string]any{
				"type":          "RollingUpdate",
				"rollingUpdate": map[string]any{"maxSurge": "4294967295%"},
			},
		})
		if err != nil {
			t.Fatalf("maxSurge 4294967295%%: %v, want it accepted", err)
		}
		if got := *cfg.Strategy.RollingUpdate.MaxSurge; got != intstr.FromString("4294967295%") {
			t.Errorf("MaxSurge = %v, want the authored string", got)
		}
	})

	// And the ceiling is still the ceiling, with its own message: a 101%
	// maxUnavailable is representable and refused for a different reason.
	t.Run("the 100% ceiling still applies to maxUnavailable", func(t *testing.T) {
		_, err := parseDeploymentSpec(map[string]any{
			"strategy": map[string]any{
				"type":          "RollingUpdate",
				"rollingUpdate": map[string]any{"maxUnavailable": "101%"},
			},
		})
		if err == nil {
			t.Fatal("maxUnavailable 101% was accepted, want the ceiling refusal")
		}
		if !strings.Contains(err.Error(), "must not be greater than 100%") {
			t.Errorf("error = %q, want the ceiling message, not the representability one", err.Error())
		}
	})
}

// TestParseDeploymentSpec_MinReadySecondsBoundary pins both sides of the
// defaulted-deadline comparison at the boundary. 599 is accepted because the
// API default 600 still exceeds it; 600 is not, and the rejection is covered in
// the error table. Without the accepting half a guard that simply refused every
// authored minReadySeconds would leave the suite green.
func TestParseDeploymentSpec_MinReadySecondsBoundary(t *testing.T) {
	cfg, err := parseDeploymentSpec(map[string]any{"minReadySeconds": 599})
	if err != nil {
		t.Fatalf("parseDeploymentSpec(minReadySeconds: 599) error = %v, want it accepted below the API default 600", err)
	}
	if cfg.MinReadySeconds == nil || *cfg.MinReadySeconds != 599 {
		t.Errorf("MinReadySeconds = %v, want 599", cfg.MinReadySeconds)
	}
	if cfg.ProgressDeadlineSeconds != nil {
		t.Errorf("ProgressDeadlineSeconds = %v, want nil: the default is upstream's to supply, not launcher's to write", cfg.ProgressDeadlineSeconds)
	}

	// The same minReadySeconds becomes legal once the deadline is raised with
	// it, which is what makes the rule a comparison rather than a ceiling.
	if _, err := parseDeploymentSpec(map[string]any{"minReadySeconds": 900, "progressDeadlineSeconds": 901}); err != nil {
		t.Errorf("parseDeploymentSpec(minReadySeconds: 900, progressDeadlineSeconds: 901) error = %v, want it accepted", err)
	}
}

// TestParseDeploymentSpec_Errors covers every rejection the parser makes.
func TestParseDeploymentSpec_Errors(t *testing.T) {
	cases := []struct {
		name  string
		props map[string]any
		want  string
	}{
		{
			"selector is not authorable",
			map[string]any{"selector": map[string]any{"matchLabels": map[string]any{"app": "x"}}},
			"selector: not authorable",
		},
		{
			"template is not authorable",
			map[string]any{"template": map[string]any{"spec": map[string]any{}}},
			"template: not authorable",
		},
		{
			"strategy.type is required",
			map[string]any{"strategy": map[string]any{}},
			"strategy.type: required",
		},
		{
			"strategy.type enum",
			map[string]any{"strategy": map[string]any{"type": "Rolling"}},
			"strategy.type: invalid value",
		},
		{
			"unknown key on strategy",
			map[string]any{"strategy": map[string]any{"type": "Recreate", "surge": 1}},
			"strategy: unrecognized key \"surge\"",
		},
		{
			"unknown key on rollingUpdate",
			map[string]any{"strategy": map[string]any{
				"type":          "RollingUpdate",
				"rollingUpdate": map[string]any{"partition": 1},
			}},
			"strategy.rollingUpdate: unrecognized key \"partition\"",
		},
		{
			"rollingUpdate under Recreate",
			map[string]any{"strategy": map[string]any{
				"type":          "Recreate",
				"rollingUpdate": map[string]any{"maxSurge": 1},
			}},
			"strategy.rollingUpdate: only allowed for strategy.type RollingUpdate",
		},
		{
			"both rolling-update knobs authored zero",
			map[string]any{"strategy": map[string]any{
				"type":          "RollingUpdate",
				"rollingUpdate": map[string]any{"maxUnavailable": 0, "maxSurge": 0},
			}},
			"may not be 0 when maxSurge is 0",
		},
		{
			"maxUnavailable above 100 percent",
			map[string]any{"strategy": map[string]any{
				"type":          "RollingUpdate",
				"rollingUpdate": map[string]any{"maxUnavailable": "150%"},
			}},
			"percentage must not be greater than 100%",
		},
		{
			"maxUnavailable with a signed percentage",
			map[string]any{"strategy": map[string]any{
				"type":          "RollingUpdate",
				"rollingUpdate": map[string]any{"maxUnavailable": "+50%"},
			}},
			"must be a percentage",
		},
		{
			"maxSurge negative integer",
			map[string]any{"strategy": map[string]any{
				"type":          "RollingUpdate",
				"rollingUpdate": map[string]any{"maxSurge": -1},
			}},
			"strategy.rollingUpdate.maxSurge: must be >= 0",
		},
		{
			"maxSurge wrong type",
			map[string]any{"strategy": map[string]any{
				"type":          "RollingUpdate",
				"rollingUpdate": map[string]any{"maxSurge": true},
			}},
			"must be a non-negative integer or a percentage string",
		},
		{
			"minReadySeconds negative",
			map[string]any{"minReadySeconds": -1},
			"minReadySeconds: must be >= 0",
		},
		{
			"revisionHistoryLimit negative",
			map[string]any{"revisionHistoryLimit": -1},
			"revisionHistoryLimit: must be >= 0",
		},
		{
			"paused wrong type",
			map[string]any{"paused": "yes"},
			"paused: must be a boolean",
		},
		{
			"progressDeadlineSeconds negative",
			map[string]any{"progressDeadlineSeconds": -1},
			"progressDeadlineSeconds: must be >= 0",
		},
		{
			"progressDeadlineSeconds not above the defaulted minReadySeconds",
			map[string]any{"progressDeadlineSeconds": 0},
			"must be greater than minReadySeconds (unset, so the API default 0 applies)",
		},
		{
			"progressDeadlineSeconds not above the authored minReadySeconds",
			map[string]any{"minReadySeconds": 30, "progressDeadlineSeconds": 30},
			"must be greater than minReadySeconds (authored as 30)",
		},
		{
			// The document mentions no deadline at all, so the failing
			// comparison is against the API default the apiserver will supply.
			// The message has to say so or the author cannot see the conflict.
			"authored minReadySeconds meets the defaulted deadline",
			map[string]any{"minReadySeconds": 600},
			"progressDeadlineSeconds (unset, so the API default 600 applies) must be greater than minReadySeconds (authored as 600)",
		},
		{
			"authored minReadySeconds above the defaulted deadline",
			map[string]any{"minReadySeconds": 900},
			"progressDeadlineSeconds (unset, so the API default 600 applies) must be greater than minReadySeconds (authored as 900)",
		},
		{
			"strategy is not an object",
			map[string]any{"strategy": "RollingUpdate"},
			"strategy: must be an object, got string",
		},
		{
			"rollingUpdate is not an object",
			map[string]any{"strategy": map[string]any{
				"type":          "RollingUpdate",
				"rollingUpdate": "25%",
			}},
			"strategy.rollingUpdate: must be an object, got string",
		},
		{
			"strategy.type is not a string",
			map[string]any{"strategy": map[string]any{"type": 1}},
			"strategy.type: must be a string",
		},
		{
			"minReadySeconds is not an integer",
			map[string]any{"minReadySeconds": "30"},
			"minReadySeconds: must be an integer, got string",
		},
		{
			"revisionHistoryLimit is not an integer",
			map[string]any{"revisionHistoryLimit": "10"},
			"revisionHistoryLimit: must be an integer, got string",
		},
		{
			"progressDeadlineSeconds is not an integer",
			map[string]any{"progressDeadlineSeconds": "600"},
			"progressDeadlineSeconds: must be an integer, got string",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := parseDeploymentSpec(tc.props)
			if err == nil {
				t.Fatalf("parseDeploymentSpec(%v) = nil error, want one containing %q", tc.props, tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %q, want it to contain %q", err.Error(), tc.want)
			}
		})
	}
}

// TestParseDeploymentSpec_NullIsOmission pins null-as-absence across every
// optional field this kind adds. A lowering rule may emit an unset optional as
// an explicit null; pkg/oam's property validator accepts that (a null under an
// optional property constrains nothing, and reads as absent for requiredness),
// so a component can satisfy the published schema and must not then fail during
// handler conversion.
func TestParseDeploymentSpec_NullIsOmission(t *testing.T) {
	t.Run("every optional field null", func(t *testing.T) {
		cfg, err := parseDeploymentSpec(map[string]any{
			"strategy":                nil,
			"minReadySeconds":         nil,
			"revisionHistoryLimit":    nil,
			"paused":                  nil,
			"progressDeadlineSeconds": nil,
		})
		if err != nil {
			t.Fatalf("parseDeploymentSpec: %v", err)
		}
		if cfg.Strategy != nil || cfg.MinReadySeconds != nil || cfg.RevisionHistoryLimit != nil ||
			cfg.Paused != nil || cfg.ProgressDeadlineSeconds != nil {
			t.Errorf("an all-null document produced %+v, want every field unset", cfg)
		}
	})

	// A typed nil is what a Go-constructed lowering rule produces when it
	// assigns a nil map or slice into an `any`. It is not `== nil`, so it needs
	// the reflection arm the property validator also uses; without it the
	// authored-vs-emitted disagreement survives in this narrower shape.
	t.Run("typed nil map", func(t *testing.T) {
		cfg, err := parseDeploymentSpec(map[string]any{"strategy": map[string]any(nil)})
		if err != nil {
			t.Fatalf("parseDeploymentSpec with a typed nil strategy: %v", err)
		}
		if cfg.Strategy != nil {
			t.Errorf("Strategy = %+v, want nil", cfg.Strategy)
		}
	})

	t.Run("rollingUpdate null leaves the API defaults", func(t *testing.T) {
		cfg, err := parseDeploymentSpec(map[string]any{
			"strategy": map[string]any{"type": "RollingUpdate", "rollingUpdate": nil},
		})
		if err != nil {
			t.Fatalf("parseDeploymentSpec: %v", err)
		}
		if cfg.Strategy == nil || cfg.Strategy.Type != appsv1.RollingUpdateDeploymentStrategyType {
			t.Fatalf("Strategy = %+v, want type RollingUpdate", cfg.Strategy)
		}
		if cfg.Strategy.RollingUpdate != nil {
			t.Errorf("Strategy.RollingUpdate = %+v, want nil so the API defaults apply", cfg.Strategy.RollingUpdate)
		}
	})

	t.Run("both knobs null", func(t *testing.T) {
		cfg, err := parseDeploymentSpec(map[string]any{
			"strategy": map[string]any{
				"type":          "RollingUpdate",
				"rollingUpdate": map[string]any{"maxSurge": nil, "maxUnavailable": nil},
			},
		})
		if err != nil {
			t.Fatalf("parseDeploymentSpec: %v", err)
		}
		ru := cfg.Strategy.RollingUpdate
		if ru == nil {
			t.Fatal("Strategy.RollingUpdate is nil, want the authored (if empty) object")
		}
		if ru.MaxSurge != nil || ru.MaxUnavailable != nil {
			t.Errorf("RollingUpdate = %+v, want both knobs unset — a null must not read as an authored zero, which would trip the both-zero rule", ru)
		}
	})

	// Where a null lands on a field that is required once its parent is
	// authored, it must surface as the requiredness error, not a type error.
	// This kind requires strategy.type unconditionally, so there is no
	// inference case here as there is on the statefulset kind.
	t.Run("strategy.type null is the requiredness error", func(t *testing.T) {
		_, err := parseDeploymentSpec(map[string]any{
			"strategy": map[string]any{"type": nil, "rollingUpdate": map[string]any{"maxSurge": 1}},
		})
		if err == nil {
			t.Fatal("expected strategy.type to be reported as required")
		}
		if !strings.Contains(err.Error(), "strategy.type: required") {
			t.Errorf("error = %q, want it to name strategy.type as required", err.Error())
		}
	})

	// A forbidden key is not an optional property, so naming it — even as a
	// null — still earns the explanatory refusal rather than silence.
	t.Run("a rejected key named as null is still rejected", func(t *testing.T) {
		for key := range deploymentSpecRejectedKeys {
			_, err := parseDeploymentSpec(map[string]any{key: nil})
			if err == nil {
				t.Errorf("%s: null, want the not-authorable refusal, got nil", key)
			}
		}
	})

	// No error produced along any of these paths may render the nil itself:
	// "got <nil>" is the signature of a null that reached a type check.
	t.Run("no error renders the nil", func(t *testing.T) {
		docs := []map[string]any{
			{"strategy": nil},
			{"minReadySeconds": nil},
			{"revisionHistoryLimit": nil},
			{"paused": nil},
			{"progressDeadlineSeconds": nil},
			{"strategy": map[string]any{"type": "RollingUpdate", "rollingUpdate": nil}},
			{"strategy": map[string]any{"type": nil}},
			{"strategy": map[string]any{"type": "RollingUpdate", "rollingUpdate": map[string]any{"maxSurge": nil}}},
		}
		for i, doc := range docs {
			if _, err := parseDeploymentSpec(doc); err != nil && strings.Contains(err.Error(), "<nil>") {
				t.Errorf("docs[%d] = %v produced %q, which renders the nil", i, doc, err.Error())
			}
		}
	})
}

// TestIntOrPercentIsZero pins the percentage branch. intstr.IntValue() returns 0
// for any string it cannot Atoi, "25%" included, so without that branch every
// percentage would read as zero and the both-zero rule would fire on documents
// that carry no zero at all.
func TestIntOrPercentIsZero(t *testing.T) {
	cases := []struct {
		v    intstr.IntOrString
		want bool
	}{
		{intstr.FromInt32(0), true},
		{intstr.FromInt32(1), false},
		{intstr.FromString("0%"), true},
		{intstr.FromString("25%"), false},
		{intstr.FromString("100%"), false},
	}
	for _, tc := range cases {
		if got := intOrPercentIsZero(tc.v); got != tc.want {
			t.Errorf("intOrPercentIsZero(%v) = %v, want %v", tc.v, got, tc.want)
		}
	}
}
