package components_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/go-kure/launcher/pkg/errors"
	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/components"
)

// The one-of property groups the built-in components declare
// (oam.ExclusiveGroup, go-kure/launcher#790). Each site's handler refuses both
// members and neither on its own; the group publishes that rule and, through
// Transform, its wording comes before the handler's. Every member counts as
// set when it is present and not null, an empty value included, so an empty
// member beside its set sibling is refused through Transform and through a
// direct call alike.

// exclusiveSite is one adopter: a component type, its handler (or lowering
// rule), and the property maps that author both members, an empty member
// beside a set one (only for a pair whose parser reads an empty value as
// absent), and neither.
type exclusiveSite struct {
	name     string
	compType string
	handler  oam.ComponentHandler
	rule     oam.ComponentLoweringRule
	keys     string // the group's keys as the validation errors render them
	setKeys  string // the keys both sets, as the mutually-exclusive error names them; keys when empty
	nested   bool   // the group sits on a nested object, so the schema also refuses neither

	both, emptyBeside, neither map[string]any

	// The handler's own refusal of both and of neither.
	handlerBoth, handlerNeither string
}

func exclusiveSites() []exclusiveSite {
	image := func(extra map[string]any) map[string]any {
		p := map[string]any{"image": "ghcr.io/org/app:v1"}
		for k, v := range extra {
			p[k] = v
		}
		return p
	}
	inline := "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: x\n"
	url := "https://example.com/manifests.yaml"
	chartRef := map[string]any{"kind": "OCIRepository", "name": "podinfo"}
	helm := func(ref map[string]any) map[string]any {
		return map[string]any{"chart": "a", "source": map[string]any{"url": "https://github.com/example/charts", "kind": "GitRepository", "ref": ref}}
	}
	pvc := func(extra map[string]any) map[string]any {
		v := map[string]any{"name": "disk", "type": "pvc", "claimName": "disk"}
		for k, val := range extra {
			v[k] = val
		}
		return image(map[string]any{"volumes": []any{v}})
	}
	vct := func(extra map[string]any) map[string]any {
		return image(map[string]any{"volumeClaimTemplates": []any{blockVCT(extra)}})
	}
	claim := func(extra map[string]any) map[string]any {
		c := map[string]any{"name": "c"}
		for k, v := range extra {
			c[k] = v
		}
		return image(map[string]any{"resourceClaims": []any{c}})
	}
	envFrom := func(entry map[string]any) map[string]any {
		return image(map[string]any{"envFrom": []any{entry}})
	}
	exitCodes := map[string]any{"operator": "In", "values": []any{1}}
	conditions := []any{map[string]any{"type": "DisruptionTarget", "status": "True"}}
	rule := func(extra map[string]any) map[string]any {
		r := map[string]any{"action": "FailJob"}
		for k, v := range extra {
			r[k] = v
		}
		return podFailurePolicyProps([]any{r}, map[string]any{"image": "ghcr.io/org/app:v1"})
	}

	return []exclusiveSite{
		{
			name: "crd", compType: "crd", handler: &components.CRDHandler{}, keys: `"inline" and "url"`,
			both: map[string]any{"inline": inline, "url": url}, neither: map[string]any{},
			handlerBoth: "exactly one of inline/url is required", handlerNeither: "exactly one of inline/url is required",
		},
		{
			name: "manifests", compType: "manifests", handler: &components.ManifestsHandler{}, keys: `"inline" and "url"`,
			both: map[string]any{"inline": inline, "url": url}, neither: map[string]any{},
			handlerBoth: "exactly one of inline/url is required", handlerNeither: "exactly one of inline/url is required",
		},
		{
			name: "helmrelease", compType: "helmrelease", handler: &components.HelmReleaseHandler{}, keys: `"chart" and "chartRef"`,
			both: map[string]any{"chart": hrChart(), "chartRef": chartRef}, neither: map[string]any{},
			handlerBoth: "exactly one of chart and chartRef", handlerNeither: "exactly one of chart and chartRef",
		},
		{
			name: "envFrom", compType: "deployment", handler: &components.DeploymentHandler{}, keys: `"configMapRef" and "secretRef"`, nested: true,
			both:        envFrom(map[string]any{"configMapRef": map[string]any{"name": "a"}, "secretRef": map[string]any{"name": "b"}}),
			neither:     envFrom(map[string]any{"prefix": "P_"}),
			handlerBoth: "must specify exactly one of configMapRef or secretRef", handlerNeither: "must specify exactly one of configMapRef or secretRef",
		},
		{
			name: "helm source.ref", compType: "helm", rule: components.HelmRule{}, keys: `"branch", "tag", "semver", "name" and "commit"`, setKeys: `"branch" and "tag"`, nested: true,
			both:        helm(map[string]any{"branch": "main", "tag": "v1.0.0"}),
			emptyBeside: helm(map[string]any{"branch": "main", "tag": ""}),
			neither:     helm(map[string]any{}),
			handlerBoth: "source.ref sets branch, tag; an inline GitRepository takes exactly one of", handlerNeither: "requires source.ref with exactly one of branch, tag, semver, name, commit",
		},
		{
			name: "volumes", compType: "deployment", handler: &components.DeploymentHandler{}, keys: `"mountPath" and "devicePath"`, nested: true,
			both:        pvc(map[string]any{"mountPath": "/data", "devicePath": "/dev/xvda", "volumeMode": "Block"}),
			emptyBeside: pvc(map[string]any{"mountPath": "", "devicePath": "/dev/xvda", "volumeMode": "Block"}),
			neither:     pvc(map[string]any{"volumeMode": "Block"}),
			handlerBoth: "mountPath and devicePath are mutually exclusive", handlerNeither: "mountPath is required (or devicePath",
		},
		{
			name: "volumeClaimTemplates", compType: "statefulset", handler: &components.StatefulsetHandler{}, keys: `"mountPath" and "devicePath"`, nested: true,
			both:        vct(map[string]any{"mountPath": "/data", "devicePath": "/dev/xvda", "volumeMode": "Block"}),
			emptyBeside: vct(map[string]any{"mountPath": "/data", "devicePath": ""}),
			neither:     vct(map[string]any{"volumeMode": "Block"}),
			handlerBoth: "mountPath and devicePath are mutually exclusive", handlerNeither: "missing required field 'mountPath' (or 'devicePath'",
		},
		{
			name: "resourceClaims", compType: "deployment", handler: &components.DeploymentHandler{}, keys: `"resourceClaimName" and "resourceClaimTemplateName"`, nested: true,
			both:        claim(map[string]any{"resourceClaimName": "x", "resourceClaimTemplateName": "y"}),
			emptyBeside: claim(map[string]any{"resourceClaimName": "x", "resourceClaimTemplateName": ""}),
			neither:     claim(nil),
			handlerBoth: "exactly one of resourceClaimName or resourceClaimTemplateName must be set", handlerNeither: "exactly one of resourceClaimName or resourceClaimTemplateName must be set",
		},
		{
			name: "podFailurePolicy rule", compType: "job", handler: &components.JobHandler{}, keys: `"onExitCodes" and "onPodConditions"`, nested: true,
			both:        rule(map[string]any{"onExitCodes": exitCodes, "onPodConditions": conditions}),
			emptyBeside: rule(map[string]any{"onExitCodes": exitCodes, "onPodConditions": []any{}}),
			neither:     rule(nil),
			handlerBoth: "onExitCodes and onPodConditions are mutually exclusive", handlerNeither: "exactly one of onExitCodes or onPodConditions is required",
		},
	}
}

// direct calls the site's handler or lowering rule on props, bypassing every
// schema check.
func (s exclusiveSite) direct(props map[string]any) error {
	comp := &oam.Component{Name: "app", Type: s.compType, Properties: props}
	if s.rule != nil {
		lctx := helmLowering("app.yaml")
		lctx.Origin.Component, lctx.Origin.ComponentType = comp.Name, comp.Type
		_, err := s.rule.LowerComponent(comp, lctx)
		return err
	}
	_, err := s.handler.ToApplicationConfig(comp, "default")
	return err
}

// transform runs what kurel build runs on an authored document,
// ValidateAuthoredProperties and then Transform, and returns the first error.
func (s exclusiveSite) transform(props map[string]any) error {
	tr := oam.NewTransformer(map[string]oam.ComponentHandler{}, nil)
	if s.rule != nil {
		tr.RegisterComponentLowering(s.rule)
	} else {
		tr.RegisterComponent(s.compType, s.handler)
	}
	app := &oam.Application{
		APIVersion: oam.SupportedAPIVersion,
		Kind:       "Application",
		Metadata:   oam.Metadata{Name: "myapp", Namespace: "default"},
		Spec:       oam.ApplicationSpec{Components: []oam.Component{{Name: "app", Type: s.compType, Properties: props}}},
	}
	if err := tr.ValidateAuthoredProperties(app); err != nil {
		return err
	}
	_, err := tr.Transform(app, oam.TransformContext{Namespace: "default"})
	return err
}

func TestExclusiveAdopters_RefuseBothAndNeither(t *testing.T) {
	for _, s := range exclusiveSites() {
		t.Run(s.name, func(t *testing.T) {
			setKeys := s.setKeys
			if setKeys == "" {
				setKeys = s.keys
			}
			type exclusiveCase struct {
				name               string
				props              map[string]any
				handler, transform string
			}
			cases := []exclusiveCase{
				{"both", s.both, s.handlerBoth, setKeys + " are mutually exclusive"},
				{"neither", s.neither, s.handlerNeither, "exactly one of " + s.keys + " is required"},
			}
			if s.emptyBeside != nil {
				cases = append(cases, exclusiveCase{"empty beside its sibling", s.emptyBeside, s.handlerBoth, setKeys + " are mutually exclusive"})
			}
			for _, c := range cases {
				if c.props == nil {
					t.Fatalf("case %s has no properties", c.name)
				}
				t.Run(c.name, func(t *testing.T) {
					wantErrContaining(t, s.direct(c.props), c.handler)
					// Authored validation checks a top-level group's upper
					// bound only, so neither reaches the handler there.
					want := c.transform
					if c.name == "neither" && !s.nested {
						want = c.handler
					}
					wantErrContaining(t, s.transform(c.props), want)
				})
			}
		})
	}
}

// TestExclusiveAdopters_PublishGroups pins each adopter's group in the schema
// it publishes, so the one-of rule a consumer reads names the pair the handler
// enforces.
func TestExclusiveAdopters_PublishGroups(t *testing.T) {
	for _, s := range exclusiveSites() {
		t.Run(s.name, func(t *testing.T) {
			tr := oam.NewTransformer(map[string]oam.ComponentHandler{}, nil)
			if s.rule != nil {
				tr.RegisterComponentLowering(s.rule)
			} else {
				tr.RegisterComponent(s.compType, s.handler)
			}
			published := tr.HandlerSchemas()
			found := false
			var walk func(schema oam.PropertySchema)
			check := func(groups []oam.ExclusiveGroup) {
				for _, g := range groups {
					if g.Required && quoted(g.Keys) == s.keys {
						found = true
					}
				}
			}
			walk = func(schema oam.PropertySchema) {
				check(schema.Exclusive)
				for _, p := range schema.Properties {
					walk(p)
				}
				if schema.Items != nil {
					walk(*schema.Items)
				}
			}
			if !s.nested {
				if published.Exclusive != nil {
					check(published.Exclusive.Components[s.compType])
				}
			} else {
				for _, p := range published.Components[s.compType] {
					walk(p)
				}
			}
			if !found {
				t.Errorf("no required exclusive group of %s published for %s", s.keys, s.compType)
			}
		})
	}
}

// helmSourceMap is a named map type a Go-built lowering rule may hand the helm
// rule as its source.
type helmSourceMap map[string]any

// onceSource encodes as data on its first call and fails on any later one,
// counting the calls.
type onceSource struct {
	data  string
	calls int
}

func (s *onceSource) MarshalJSON() ([]byte, error) {
	s.calls++
	if s.calls > 1 {
		return nil, errors.New("encoded more than once")
	}
	return []byte(s.data), nil
}

// TestExclusiveAdopters_HelmRefTypedMaps: the helm rule reads which source.ref
// fields are authored from the JSON its decode reads, so a typed, named or raw
// source authors an empty source.ref field as a map[string]any does, and the
// empty field beside a set one is refused on a direct call too.
func TestExclusiveAdopters_HelmRefTypedMaps(t *testing.T) {
	url := "https://github.com/example/charts"
	cases := map[string]map[string]any{
		"typed ref": {"chart": "a", "source": map[string]any{"url": url, "kind": "GitRepository",
			"ref": map[string]string{"branch": "main", "tag": ""}}},
		"named source": {"chart": "a", "source": helmSourceMap{"url": url, "kind": "GitRepository",
			"ref": map[string]any{"branch": "main", "tag": ""}}},
		"case-folded keys": {"chart": "a", "Source": map[string]any{"url": url, "kind": "GitRepository",
			"Ref": map[string]any{"Branch": "main", "TAG": ""}}},
	}
	for name, props := range cases {
		t.Run(name, func(t *testing.T) {
			site := exclusiveSite{compType: "helm", rule: components.HelmRule{}}
			wantErrContaining(t, site.direct(props), "source.ref sets branch, tag; an inline GitRepository takes exactly one of")
		})
	}
	// The decode merges every ref object it reads into one ref, and the ref is
	// authored by all of them: two spellings of ref in a named map, one ref
	// twice in raw JSON, or a field set and then nulled.
	twice := map[string]map[string]any{
		"named source, ref twice": {"chart": "a", "source": helmSourceMap{"url": url, "kind": "GitRepository",
			"Ref": map[string]any{"branch": "main"}, "ref": map[string]any{"tag": ""}}},
		"raw source, ref twice": {"chart": "a", "source": json.RawMessage(`{"url":"` + url +
			`","kind":"GitRepository","ref":{"branch":"main"},"ref":{"tag":""}}`)},
		"raw ref, branch then null": {"chart": "a", "source": map[string]any{"url": url, "kind": "GitRepository",
			"ref": json.RawMessage(`{"tag":"","branch":"main","branch":null}`)}},
	}
	for name, props := range twice {
		t.Run(name, func(t *testing.T) {
			site := exclusiveSite{compType: "helm", rule: components.HelmRule{}}
			wantErrContaining(t, site.direct(props), "source.ref sets branch, tag; an inline GitRepository takes exactly one of")
		})
	}
	// The authored fields are read from the bytes the decode reads, so a
	// source that encodes differently on a later call cannot show them a
	// different ref.
	t.Run("one encoding", func(t *testing.T) {
		src := &onceSource{data: `{"url":"` + url + `","kind":"GitRepository","ref":{"branch":"main","tag":""}}`}
		site := exclusiveSite{compType: "helm", rule: components.HelmRule{}}
		wantErrContaining(t, site.direct(map[string]any{"chart": "a", "source": src}), "source.ref sets branch, tag; an inline GitRepository takes exactly one of")
		if src.calls != 1 {
			t.Errorf("source encoded %d times, want 1", src.calls)
		}
	})
	// A null field is not authored, typed or not.
	props := map[string]any{"chart": "a", "source": map[string]any{"url": url, "kind": "GitRepository",
		"ref": map[string]any{"branch": "main", "tag": (*string)(nil)}}}
	if err := (exclusiveSite{compType: "helm", rule: components.HelmRule{}}).direct(props); err != nil {
		t.Errorf("a null tag beside a branch: %v", err)
	}
}

// quoted renders keys as the validation errors do: "a" and "b", or "a", "b"
// and "c".
func quoted(keys []string) string {
	q := make([]string, len(keys))
	for i, k := range keys {
		q[i] = `"` + k + `"`
	}
	if len(q) < 2 {
		return strings.Join(q, "")
	}
	return strings.Join(q[:len(q)-1], ", ") + " and " + q[len(q)-1]
}
