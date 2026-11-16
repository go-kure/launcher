package registry_test

import (
	"bytes"
	"maps"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/registry"
)

// These tests hold the provenance report to the fixed values the trait handlers
// fill when the properties they are handed hold none (go-kure/launcher#1580):
// reported as SourceBuiltin at the path the author would have written, under
// the trait's owner, one source per path. They share the helpers of
// policy_default_provenance_test.go.
//
// Not every value a trait supplies is reported: a name or a backend a trait
// derives from the component, the fields security-context and topology-spread
// write into the workload, and the expose rule are not
// (traitDefaultExemptions holds the schema defaults among them).

// traitDoc is the properties of one trait and the capability bindings the
// transform runs with.
type traitDoc struct {
	props map[string]any
	caps  map[string]oam.CapabilityBinding
}

// traitFillCase is one fill site of a trait.
type traitFillCase struct {
	trait string
	// schemaPath is the place of the fill in the trait's schema, a list item
	// written "[]"; "" for a fill the schema declares no Default at.
	schemaPath string
	// path is the path of the report entry in the documents below.
	path string
	// omitted leaves the value out, so the handler fills it; written holds it,
	// authored or rendered by a capability.
	omitted, written traitDoc
	// extra is added to the properties of the deployment the trait sits on.
	extra  map[string]any
	policy *defaultsPolicy
}

func (c traitFillCase) name() string { return c.trait + "/" + c.path }

func (c traitFillCase) ref() string { return c.trait + "." + c.path }

// with returns a copy of base with the value at keys set to v: a key names a
// property of an object, and a list is entered at its first item.
func with(base map[string]any, v any, keys ...string) map[string]any {
	out := make(map[string]any, len(base)+1)
	for k, bv := range base {
		out[k] = bv
	}
	if len(keys) == 1 {
		out[keys[0]] = v
		return out
	}
	switch child := out[keys[0]].(type) {
	case []any:
		first, _ := child[0].(map[string]any)
		out[keys[0]] = append([]any{with(first, v, keys[1:]...)}, child[1:]...)
	case map[string]any:
		out[keys[0]] = with(child, v, keys[1:]...)
	default:
		out[keys[0]] = with(nil, v, keys[1:]...)
	}
	return out
}

func traitFillCases() []traitFillCase {
	var cases []traitFillCase
	// add appends a case whose written document is the omitted one with value
	// authored at keys.
	add := func(trait, schemaPath, path string, base map[string]any, caps map[string]oam.CapabilityBinding, value any, keys ...string) {
		cases = append(cases, traitFillCase{
			trait: trait, schemaPath: schemaPath, path: path,
			omitted: traitDoc{props: base, caps: caps},
			written: traitDoc{props: with(base, value, keys...), caps: caps},
		})
	}

	issuer := map[string]oam.CapabilityBinding{"certificate": {Rendering: map[string]any{
		"issuerRef": map[string]any{"name": "letsencrypt", "kind": "ClusterIssuer"},
	}}}
	cert := map[string]any{"secretName": "tls", "dnsNames": []any{"a.example.com"}}
	add("certificate", "duration", "duration", cert, issuer, "100h", "duration")
	add("certificate", "renewBefore", "renewBefore", cert, issuer, "10h", "renewBefore")
	// A binding that went through EvaluateProfile holds the issuer's kind; one
	// handed to the transform without it leaves the handler to fill it.
	add("certificate", "issuerRef.kind", "issuerRef.kind", cert,
		map[string]oam.CapabilityBinding{"certificate": {Rendering: map[string]any{"issuerRef": map[string]any{"name": "letsencrypt"}}}},
		map[string]any{"name": "own", "kind": "Issuer"}, "issuerRef")

	es := map[string]any{
		"secretName":     "db",
		"secretStoreRef": map[string]any{"name": "vault"},
		"data":           []any{map[string]any{"secretKey": "password"}},
	}
	add("external-secret", "refreshInterval", "refreshInterval", es, nil, "30m", "refreshInterval")
	add("external-secret", "secretStoreRef.kind", "secretStoreRef.kind", es, nil, "SecretStore", "secretStoreRef", "kind")

	vs := map[string]any{"sourcePVC": "data", "schedule": "0 3 * * *"}
	add("volsync", "copyMethod", "copyMethod", vs, nil, "Direct", "copyMethod")
	add("volsync", "pruneIntervalDays", "pruneIntervalDays", vs, nil, 7, "pruneIntervalDays")
	add("volsync", "retain.daily", "retain.daily", vs, nil, 1, "retain", "daily")
	add("volsync", "retain.weekly", "retain.weekly", vs, nil, 1, "retain", "weekly")
	add("volsync", "retain.monthly", "retain.monthly", vs, nil, 1, "retain", "monthly")

	add("scaler", "cpuUtilization", "cpuUtilization", map[string]any{"minReplicas": 2, "maxReplicas": 5}, nil, 70, "cpuUtilization")

	peer := map[string]any{"podSelector": map[string]any{"matchLabels": map[string]any{"app": "client"}}}
	add("networkpolicy", "ingress[].ports[].protocol", "ingress[0].ports[0].protocol",
		map[string]any{"ingress": []any{map[string]any{"from": []any{peer}, "ports": []any{map[string]any{"port": 8080}}}}},
		nil, "UDP", "ingress", "ports", "protocol")
	add("networkpolicy", "egress[].ports[].protocol", "egress[0].ports[0].protocol",
		map[string]any{"egress": []any{map[string]any{"to": []any{peer}, "ports": []any{map[string]any{"port": 5432}}}}},
		nil, "UDP", "egress", "ports", "protocol")

	ing := map[string]any{"servicePort": 8080, "rules": []any{map[string]any{"host": "a.example.com", "paths": []any{map[string]any{}}}}}
	add("ingress", "rules[].paths[].path", "rules[0].paths[0].path", ing, nil, "/api", "rules", "paths", "path")
	add("ingress", "rules[].paths[].pathType", "rules[0].paths[0].pathType", ing, nil, "Exact", "rules", "paths", "pathType")

	parent := []any{map[string]any{"name": "gw", "namespace": "infra"}}
	route := func(match map[string]any) map[string]any {
		return map[string]any{"servicePort": 8080, "parentRefs": parent, "rules": []any{map[string]any{"matches": []any{match}}}}
	}
	pathMatch := route(map[string]any{"path": map[string]any{}})
	add("httproute", "", "rules[0].matches[0].path.type", pathMatch, nil, "Exact", "rules", "matches", "path", "type")
	add("httproute", "", "rules[0].matches[0].path.value", pathMatch, nil, "/api", "rules", "matches", "path", "value")
	add("httproute", "", "rules[0].matches[0].headers[0].type",
		route(map[string]any{"headers": []any{map[string]any{"name": "x-tenant", "value": "a"}}}),
		nil, "RegularExpression", "rules", "matches", "headers", "type")
	// The namespace of the gateway a capability names: filled when the rendering
	// names the gateway alone, rendered when it names the namespace too.
	gateway := func(rendering map[string]any) traitDoc {
		return traitDoc{
			props: map[string]any{"servicePort": 8080, "rules": []any{map[string]any{}}},
			caps:  map[string]oam.CapabilityBinding{"httproute": {Rendering: rendering}},
		}
	}
	cases = append(cases, traitFillCase{
		trait: "httproute", schemaPath: "gatewayNamespace", path: "gatewayNamespace",
		omitted: gateway(map[string]any{"gatewayName": "gw"}),
		written: gateway(map[string]any{"gatewayName": "gw", "gatewayNamespace": "infra"}),
	})

	add("pvc", "accessModes", "accessModes", map[string]any{"name": "data", "size": "1Gi"}, nil, []any{"ReadWriteMany"}, "accessModes")
	return cases
}

// traitDefaultExemptions are the Default declarations of the trait schemas that
// no case above reports, keyed "<trait>/<schema path>", each with its reason.
var traitDefaultExemptions = map[string]string{
	"scaler/enablePDB":        "no fill: left out, the trait generates no PodDisruptionBudget, and no object holds a value for it",
	"expose/gatewayNamespace": "expose is a lowering rule: what it writes into the trait it emits is reported as rendered by the rule, and its own fills are not reported",
}

func traitProvenance(t *testing.T, c traitFillCase, doc traitDoc) (*oam.Provenance, []byte) {
	t.Helper()
	app, ctx := traitFillApplication(t, c, doc)
	cluster, _, prov, err := newTransformer().TransformWithProvenance(app, ctx)
	if err != nil {
		t.Fatalf("TransformWithProvenance: %v", err)
	}
	return prov, manifestsOf(t, cluster)
}

func traitFillApplication(t *testing.T, c traitFillCase, doc traitDoc) (*oam.Application, oam.TransformContext) {
	t.Helper()
	comp := workload("app", "deployment", c.extra)
	comp.Traits = []oam.Trait{{Type: c.trait, Properties: doc.props}}
	app := &oam.Application{
		APIVersion: oam.SupportedAPIVersion,
		Kind:       "Application",
		Metadata:   oam.Metadata{Name: "pkg", Namespace: "default"},
		Spec:       oam.ApplicationSpec{Components: []oam.Component{comp}},
	}
	validated, err := newTransformer().ValidateAuthoredProperties(app)
	if err != nil {
		t.Fatalf("ValidateAuthoredProperties: %v", err)
	}
	policy := c.policy
	if policy == nil {
		policy = &defaultsPolicy{}
	}
	return validated, oam.TransformContext{Namespace: "default", Policy: policy, Capabilities: doc.caps}
}

// A value the handler filled is reported as built in, at the path the author
// would have written, under the trait.
func TestTraitFills_OmittedIsBuiltin(t *testing.T) {
	for _, c := range traitFillCases() {
		t.Run(c.name(), func(t *testing.T) {
			prov, _ := traitProvenance(t, c, c.omitted)
			got := entriesOf(prov, owner{component: "app", trait: c.trait})
			if want := (oam.Source{Kind: oam.SourceBuiltin, Ref: c.ref()}); got[c.path] != want {
				t.Errorf("%s = %+v, want %+v\nentries: %v", c.path, got[c.path], want, got)
			}
		})
	}
}

// A value the author wrote, or a capability rendered, is not: the entry at its
// path, or each entry below it where the value is a list, names that source.
func TestTraitFills_WrittenIsNotBuiltin(t *testing.T) {
	for _, c := range traitFillCases() {
		t.Run(c.name(), func(t *testing.T) {
			prov, _ := traitProvenance(t, c, c.written)
			found := 0
			for path, got := range entriesOf(prov, owner{component: "app", trait: c.trait}) {
				if path != c.path && !strings.HasPrefix(path, c.path+"[") {
					continue
				}
				found++
				if got.Kind == oam.SourceBuiltin {
					t.Errorf("%s = %+v, want the source that wrote it", path, got)
				}
			}
			if found == 0 {
				t.Fatalf("%s has no entry though the document holds a value for it", c.path)
			}
		})
	}
}

// Reading the fills changes no object: the transform that reports generates what
// the transform that does not report generates.
func TestTraitFills_ObjectsUnchanged(t *testing.T) {
	for _, c := range traitFillCases() {
		t.Run(c.name(), func(t *testing.T) {
			_, reported := traitProvenance(t, c, c.omitted)
			app, ctx := traitFillApplication(t, c, c.omitted)
			cluster, err := newTransformer().Transform(app, ctx)
			if err != nil {
				t.Fatalf("Transform: %v", err)
			}
			if plain := manifestsOf(t, cluster); !bytes.Equal(reported, plain) {
				t.Errorf("the objects differ with the report read:\nwith:\n%s\nwithout:\n%s", reported, plain)
			}
		})
	}
}

// A path the policy filled is reported as the policy's, never also as built in,
// beside a built-in fill of the same trait.
func TestTraitFills_PolicyFilledPathIsThePolicys(t *testing.T) {
	cases := []struct {
		trait   string
		props   map[string]any
		policy  *defaultsPolicy
		policys map[string]string
		builtin string
	}{
		{"scaler", map[string]any{}, &defaultsPolicy{scalerMin: i32(2), scalerMax: i32(6)},
			map[string]string{"minReplicas": "DefaultScalerMinReplicas", "maxReplicas": "DefaultScalerMaxReplicas"}, "cpuUtilization"},
		{"pvc", map[string]any{"name": "data"}, &defaultsPolicy{store: "20Gi"},
			map[string]string{"size": "DefaultStorageSize"}, "accessModes"},
	}
	for _, tc := range cases {
		t.Run(tc.trait, func(t *testing.T) {
			c := traitFillCase{trait: tc.trait, policy: tc.policy}
			prov, _ := traitProvenance(t, c, traitDoc{props: tc.props})
			got := entriesOf(prov, owner{component: "app", trait: tc.trait})
			for path, accessor := range tc.policys {
				if want := (oam.Source{Kind: oam.SourcePolicy, Ref: accessor}); got[path] != want {
					t.Errorf("%s = %+v, want %+v", path, got[path], want)
				}
			}
			if want := (oam.Source{Kind: oam.SourceBuiltin, Ref: tc.trait + "." + tc.builtin}); got[tc.builtin] != want {
				t.Errorf("%s = %+v, want %+v\nentries: %v", tc.builtin, got[tc.builtin], want, got)
			}
			seen := map[string]int{}
			for _, e := range prov.Entries {
				if e.Component == "app" && e.Trait == tc.trait {
					seen[e.Path]++
				}
			}
			for path, n := range seen {
				if n != 1 {
					t.Errorf("%s has %d entries, want one source per path", path, n)
				}
			}
		})
	}
}

// The scaler fills a CPU target only where the author wrote no target at all: a
// memory target alone leaves no CPU target, so none is reported.
func TestTraitFills_ScalerMemoryTargetAloneFillsNoCPUTarget(t *testing.T) {
	c := traitFillCase{trait: "scaler"}
	prov, _ := traitProvenance(t, c, traitDoc{props: map[string]any{"minReplicas": 2, "maxReplicas": 5, "memoryUtilization": 70}})
	if got, ok := entriesOf(prov, owner{component: "app", trait: "scaler"})["cpuUtilization"]; ok {
		t.Errorf("cpuUtilization = %+v, want no entry: the HorizontalPodAutoscaler has no CPU target", got)
	}
}

// The provider shorthand of external-secret names a ClusterSecretStore: the kind
// is what the shorthand means, not a value filled at secretStoreRef.kind.
func TestTraitFills_ExternalSecretProviderShorthandFillsNoKind(t *testing.T) {
	c := traitFillCase{trait: "external-secret"}
	prov, _ := traitProvenance(t, c, traitDoc{props: map[string]any{
		"secretName": "db", "provider": "vault", "data": []any{map[string]any{"secretKey": "password"}},
	}})
	if got, ok := entriesOf(prov, owner{component: "app", trait: "external-secret"})["secretStoreRef.kind"]; ok {
		t.Errorf("secretStoreRef.kind = %+v, want no entry: the author wrote no secretStoreRef", got)
	}
}

// An httproute match type the generator does not know, the empty string
// included, is not refused: the HTTPRoute gets the default type. The value that
// stands is the handler's, so the path reports the fill.
func TestTraitFills_HTTPRouteUnknownMatchTypeIsBuiltin(t *testing.T) {
	route := func(match map[string]any) map[string]any {
		return map[string]any{
			"servicePort": 8080,
			"parentRefs":  []any{map[string]any{"name": "gw", "namespace": "infra"}},
			"rules":       []any{map[string]any{"matches": []any{match}}},
		}
	}
	for _, tc := range []struct {
		name, path string
		match      map[string]any
	}{
		{"path/empty", "rules[0].matches[0].path.type", map[string]any{"path": map[string]any{"type": "", "value": "/api"}}},
		{"path/unknown", "rules[0].matches[0].path.type", map[string]any{"path": map[string]any{"type": "Prefix", "value": "/api"}}},
		{"header/empty", "rules[0].matches[0].headers[0].type", map[string]any{"headers": []any{map[string]any{"name": "x-tenant", "value": "a", "type": ""}}}},
		{"header/unknown", "rules[0].matches[0].headers[0].type", map[string]any{"headers": []any{map[string]any{"name": "x-tenant", "value": "a", "type": "Regex"}}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := traitFillCase{trait: "httproute"}
			prov, _ := traitProvenance(t, c, traitDoc{props: route(tc.match)})
			got := entriesOf(prov, owner{component: "app", trait: "httproute"})
			if want := (oam.Source{Kind: oam.SourceBuiltin, Ref: "httproute." + tc.path}); got[tc.path] != want {
				t.Errorf("%s = %+v, want %+v", tc.path, got[tc.path], want)
			}
		})
	}
}

// listIndex matches the index of a list item in a report path.
var listIndex = regexp.MustCompile(`\[\d+\]`)

// schemaDefaults lists the paths under schema that declare a Default, a list
// item written "[]".
func schemaDefaults(schema map[string]oam.PropertySchema, prefix string) []string {
	var out []string
	for key, p := range schema {
		path := key
		if prefix != "" {
			path = prefix + "." + key
		}
		for node := &p; node != nil; node, path = node.Items, path+"[]" {
			if node.Default != nil {
				out = append(out, path)
			}
			out = append(out, schemaDefaults(node.Properties, path)...)
		}
	}
	return out
}

// Every Default a built-in trait schema declares is a fill a case above reports,
// or a row of traitDefaultExemptions with its reason: a new trait default cannot
// ship without a report or a stated reason for none.
func TestTraitFills_EverySchemaDefaultIsReportedOrExempt(t *testing.T) {
	declared := map[string]bool{}
	schemaOf := func(name string, h any) {
		p, ok := h.(oam.PropertySchemaProvider)
		if !ok {
			return
		}
		for _, path := range schemaDefaults(p.PropertySchema(), "") {
			declared[name+"/"+path] = true
		}
	}
	for name, h := range registry.TraitHandlers() {
		schemaOf(name, h)
	}
	for name, r := range registry.TraitLoweringRules() {
		schemaOf(name, r)
	}
	if len(declared) == 0 {
		t.Fatal("no trait schema declares a Default: the walk reads nothing")
	}

	reported := map[string]bool{}
	for _, c := range traitFillCases() {
		if c.schemaPath == "" {
			continue
		}
		// The case's report path is its schema path at the first items, so the
		// row a Default is held by is a row that reports that Default.
		if at := listIndex.ReplaceAllString(c.path, "[]"); at != c.schemaPath {
			t.Errorf("%s: the case reports %s, which is not its schema path %s", c.name(), c.path, c.schemaPath)
		}
		reported[c.trait+"/"+c.schemaPath] = true
	}
	for _, key := range slices.Sorted(maps.Keys(declared)) {
		_, exempt := traitDefaultExemptions[key]
		switch {
		case reported[key] && exempt:
			t.Errorf("%s is both a reported fill and an exemption", key)
		case !reported[key] && !exempt:
			t.Errorf("%s declares a Default that no case of traitFillCases reports and traitDefaultExemptions does not list", key)
		}
	}
	for _, key := range slices.Sorted(maps.Keys(reported)) {
		if !declared[key] {
			t.Errorf("traitFillCases names %s, which declares no Default", key)
		}
	}
	for _, key := range slices.Sorted(maps.Keys(traitDefaultExemptions)) {
		if !declared[key] {
			t.Errorf("traitDefaultExemptions lists %s, which declares no Default", key)
		}
		if strings.TrimSpace(traitDefaultExemptions[key]) == "" {
			t.Errorf("traitDefaultExemptions lists %s with no reason", key)
		}
	}
}
