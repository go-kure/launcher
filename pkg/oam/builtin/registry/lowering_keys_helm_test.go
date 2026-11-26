package registry_test

import (
	"maps"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/registry"
)

// keyVariant is one document shape: the properties the comparison starts from,
// and the value the key is set to over them. moves names the declared places the
// key changes there, and idle the places that are live, on a member the rule
// emits, and that it leaves alone. The two are all of them.
type keyVariant struct {
	name  string
	base  map[string]any
	value any
	moves []reportedAt
	idle  []reportedAt
}

func place(member, path string) reportedAt { return reportedAt{member: member, path: path} }

func releaseAt(path string) reportedAt    { return place("helmrelease", path) }
func renderedAt(path string) reportedAt   { return place("helmtemplate", path) }
func traitAt(typ, path string) reportedAt { return reportedAt{"helmrelease", typ, path} }
func placeOf(p oam.LoweredKey) reportedAt { return reportedAt{p.Member, p.Trait, p.Path} }
func keyAt(r reportedAt) oam.LoweredKey {
	return oam.LoweredKey{Member: r.member, Trait: r.trait, Path: r.path}
}
func comparePlaces(a, b reportedAt) int {
	return strings.Compare(a.member+"\x00"+a.trait+"\x00"+a.path, b.member+"\x00"+b.trait+"\x00"+b.path)
}

// helmSwitches are the helm keys that choose what the rule emits and are no
// property of it, so they declare no place. The variants of the other keys under
// each of their values hold the effect.
var helmSwitches = map[string]string{
	"delivery":   "chooses the members the rule emits",
	"valuesMode": "chooses where values go",
}

// helmKeyVariants holds the variants of every helm schema key that is no switch.
func helmKeyVariants() map[string][]keyVariant {
	var (
		repo      = map[string]any{"url": "https://charts.example.org/base"}
		ociChart  = map[string]any{"url": "oci://registry.example.org/charts/base"}
		git       = map[string]any{"url": "https://git.example.org/base.git", "kind": "GitRepository", "ref": map[string]any{"branch": "main"}}
		bucket    = map[string]any{"kind": "Bucket", "endpoint": "minio.example.org", "bucketName": "base"}
		reference = map[string]any{"kind": "HelmRepository", "name": "charts"}
		chartRef  = map[string]any{"kind": "OCIRepository", "name": "chart"}
	)
	doc := func(source map[string]any, more ...any) map[string]any {
		props := map[string]any{"source": source}
		for i := 0; i < len(more); i += 2 {
			props[more[i].(string)] = more[i+1]
		}
		return props
	}
	flux := doc(repo, "chart", "web")
	template := doc(repo, "chart", "web", "delivery", "template")
	configMap := doc(repo, "chart", "web", "valuesMode", "configMap")
	values := map[string]any{"replicas": 2}
	sourceRef, byChartRef := releaseAt("chart.spec.sourceRef"), releaseAt("chartRef")

	// A key forwarded to the release under the same name.
	forwarded := func(key string, value any) keyVariant {
		return keyVariant{name: "flux", base: flux, value: value, moves: []reportedAt{releaseAt(key)}}
	}
	// A key the helmtemplate takes under the same name.
	rendered := func(key string, value any) keyVariant {
		return keyVariant{name: "template", base: template, value: value, moves: []reportedAt{renderedAt(key)}}
	}
	// A key that is no property of a member.
	nowhere := func(value any) []keyVariant {
		return []keyVariant{{name: "flux", base: flux, value: value}}
	}

	return map[string][]keyVariant{
		"chart": {
			{name: "flux", base: flux, value: "api", moves: []reportedAt{releaseAt("chart.spec.chart")}},
			rendered("chart", "api"),
		},
		"version": {
			{name: "repository", base: flux, value: "1.2.3",
				moves: []reportedAt{releaseAt("chart.spec.version")}, idle: []reportedAt{byChartRef}},
			{name: "oci", base: doc(ociChart), value: "1.2.3",
				moves: []reportedAt{place("ocirepository", "ref"), byChartRef}, idle: []reportedAt{releaseAt("chart.spec.version")}},
			rendered("version", "1.2.3"),
		},
		"source": {
			{name: "repository", base: flux, value: map[string]any{"url": "https://charts.example.org/other"},
				moves: []reportedAt{place("helmrepository", "url"), sourceRef}, idle: []reportedAt{byChartRef}},
			{name: "named", base: flux, value: map[string]any{"url": "https://charts.example.org/base", "name": "shared"},
				moves: []reportedAt{sourceRef}, idle: []reportedAt{place("helmrepository", "url"), byChartRef}},
			{name: "oci", base: doc(ociChart),
				value: map[string]any{"url": "oci://registry.example.org/charts/other", "digest": "sha256:" + strings.Repeat("a", 64)},
				moves: []reportedAt{place("ocirepository", "url"), place("ocirepository", "ref"), byChartRef}, idle: []reportedAt{sourceRef}},
			{name: "git", base: doc(git, "chart", "./chart"),
				value: map[string]any{"url": "https://git.example.org/other.git", "kind": "GitRepository", "ref": map[string]any{"tag": "v1"}},
				moves: []reportedAt{place("gitrepository", "url"), place("gitrepository", "ref"), sourceRef}, idle: []reportedAt{byChartRef}},
			{name: "bucket", base: doc(bucket, "chart", "./chart"),
				value: map[string]any{"kind": "Bucket", "endpoint": "s3.example.org", "bucketName": "other", "provider": "aws", "region": "eu-west-1", "prefix": "charts/"},
				moves: []reportedAt{
					place("bucket", "endpoint"), place("bucket", "bucketName"), place("bucket", "provider"),
					place("bucket", "region"), place("bucket", "prefix"), sourceRef,
				}, idle: []reportedAt{byChartRef}},
			{name: "reference", base: doc(reference, "chart", "web"),
				value: map[string]any{"kind": "HelmRepository", "name": "other", "namespace": "flux-system"},
				moves: []reportedAt{sourceRef}, idle: []reportedAt{byChartRef}},
			{name: "chartRef", base: doc(chartRef),
				value: map[string]any{"kind": "OCIRepository", "name": "other", "namespace": "flux-system"},
				moves: []reportedAt{byChartRef}, idle: []reportedAt{sourceRef}},
			{name: "template", base: template, value: map[string]any{"url": "https://charts.example.org/other"},
				moves: []reportedAt{renderedAt("source")}},
		},
		"values": {
			{name: "inline", base: flux, value: values, moves: []reportedAt{releaseAt("values")}},
			{name: "configMap", base: configMap, value: values,
				moves: []reportedAt{traitAt("configmap", ""), releaseAt("valuesFrom")}},
			rendered("values", values),
		},
		"valuesFrom": {forwarded("valuesFrom", []any{map[string]any{"kind": "Secret", "name": "extra"}})},
		"secretValues": {
			{name: "flux", base: flux, value: map[string]any{"password": "not-a-secret"},
				moves: []reportedAt{traitAt("secret", ""), releaseAt("valuesFrom")}},
			rendered("secretValues", map[string]any{"password": "not-a-secret"}),
		},
		"valuesConfigMapName": {{name: "configMap", base: doc(repo, "chart", "web", "valuesMode", "configMap", "values", values), value: "settings",
			moves: []reportedAt{traitAt("configmap", "name"), releaseAt("valuesFrom")}}},
		"valuesSecretName": {{name: "flux", base: doc(repo, "chart", "web", "secretValues", map[string]any{"password": "not-a-secret"}), value: "credentials",
			moves: []reportedAt{traitAt("secret", "name"), releaseAt("valuesFrom")}}},
		"interval":        {forwarded("interval", "10m")},
		"targetNamespace": {forwarded("targetNamespace", "apps")},
		"driftDetection":  {forwarded("driftDetection", map[string]any{"mode": "enabled"})},
		"install":         {forwarded("install", map[string]any{"createNamespace": true})},
		"upgrade":         {forwarded("upgrade", map[string]any{"cleanupOnFail": true})},
		"releaseName":     {forwarded("releaseName", "other"), rendered("releaseName", "other")},

		oam.HookGroupNamePrefixProperty:     {rendered(oam.HookGroupNamePrefixProperty, "hooks")},
		oam.LayoutKustomizationNameProperty: {rendered(oam.LayoutKustomizationNameProperty, "web-layout")},
		"scopeOverrides": {rendered("scopeOverrides", []any{
			map[string]any{"apiVersion": "example.org/v1", "kind": "Widget", "scope": "Cluster"},
		})},

		"helmReleaseName": nowhere("release-object"),
		"endpoints": nowhere([]any{map[string]any{
			"name": "http", "protocol": "TCP", "port": 8080,
			"selector": map[string]any{"matchLabels": map[string]any{"app": "web"}},
		}}),
	}
}

// loweredVariants is the guard of
// TestLoweringKeys_AuthoredValueReachesTheDeclaredPlaces for a type with
// variants. In each variant the key changes the places of moves and no
// other, each declared and live, and moves and idle are all the live places of
// the members emitted. Over the variants of a key, moves covers every declared
// place, so a place nothing reaches fails.
func loweredVariants(t *testing.T, typ, key string, lt loweredType, places []oam.LoweredKey) {
	t.Helper()
	if reason, ok := lt.switches[key]; ok {
		if len(places) != 0 || len(lt.variants[key]) != 0 {
			t.Errorf("%q is listed as a switch (%s), and has places %v or variants", key, reason, places)
		}
		return
	}
	variants := lt.variants[key]
	if len(variants) == 0 {
		t.Fatalf("no variant for key %q of %q; add one, or list it as a switch", key, typ)
	}
	reached := map[reportedAt]bool{}
	for _, v := range variants {
		t.Run(v.name, func(t *testing.T) {
			authored := maps.Clone(v.base)
			authored[key] = v.value
			got, members := loweredLeaves(t, typ, lt.build(authored))
			base, baseMembers := loweredLeaves(t, typ, lt.build(v.base))
			if g, w := slices.Sorted(maps.Keys(members)), slices.Sorted(maps.Keys(baseMembers)); !slices.Equal(g, w) {
				t.Fatalf("authoring %q: the rule emits members %v, want %v", key, g, w)
			}

			var live []reportedAt
			for _, p := range places {
				if !p.LiveFor(authored) {
					continue
				}
				if _, emitted := members[p.Member]; emitted {
					live = append(live, placeOf(p))
				} else if p.Member == "helmrelease" || p.Member == "helmtemplate" {
					t.Errorf("%v is live for %v, and the rule emits no %s", p, authored, p.Member)
				}
			}
			listed := slices.Concat(v.moves, v.idle)
			slices.SortFunc(live, comparePlaces)
			slices.SortFunc(listed, comparePlaces)
			if !slices.Equal(live, listed) {
				t.Errorf("live places of %q on the emitted members = %v, the variant lists %v", key, live, listed)
			}

			changed := changedLeaves(got, base, members)
			for _, at := range changed {
				if !slices.ContainsFunc(v.moves, func(m reportedAt) bool { return within(at, keyAt(m)) }) {
					t.Errorf("authoring %q changed member %q trait %q path %q (%v, was %v), which is none of %v",
						key, at.member, at.trait, at.path, got[at], base[at], v.moves)
				}
			}
			for _, m := range v.moves {
				if !slices.ContainsFunc(changed, func(at reportedAt) bool { return within(at, keyAt(m)) }) {
					t.Errorf("authoring %q changed nothing at %v", key, m)
				}
				i := slices.IndexFunc(places, func(p oam.LoweredKey) bool { return placeOf(p) == m })
				if i < 0 {
					t.Errorf("authoring %q: %v is not a declared place", key, m)
					continue
				}
				reached[m] = true
				if places[i].Unchanged && !reflect.DeepEqual(members[m.member][m.path], v.value) {
					t.Errorf("%q is declared unchanged at %v, but the member holds %v, not the authored %v", key, m, members[m.member][m.path], v.value)
				}
			}
		})
	}
	for _, p := range places {
		if !reached[placeOf(p)] {
			t.Errorf("no variant of %q moves the declared place %v", key, p)
		}
	}
}

// unchangedPlacesAreCopied is the report side for oci and helm: at every
// Unchanged place a key reaches, the report of a document that authors the key
// holds an entry copied from the authored value, and none the rule built. Every
// such place of the two maps is checked, so one that no document here reaches
// fails. The helmtemplate places are not read: transforming one renders its chart
// from a repository. Their values are held on what the rule emits.
func unchangedPlacesAreCopied(t *testing.T) {
	keyed := keyedRules()
	// Named, not ranged over keyed, as the caller's own types are.
	for _, typ := range []string{"helm", "oci"} {
		lt := loweredTypes()[typ]
		d, ok := keyed[typ]
		if !ok {
			t.Errorf("rule %q does not implement oam.LoweringKeyDeclarer", typ)
			continue
		}
		keys := d.LoweringKeys()
		for _, key := range slices.Sorted(maps.Keys(keys)) {
			var docs []map[string]any
			for _, v := range lt.variants[key] {
				props := maps.Clone(v.base)
				props[key] = v.value
				docs = append(docs, props)
			}
			if sample, ok := lt.samples[key]; ok {
				props := maps.Clone(lt.base)
				maps.Copy(props, sample.with)
				props[key] = sample.value
				docs = append(docs, props)
			}
			for _, p := range keys[key] {
				if !p.Unchanged || p.Member == "helmtemplate" {
					continue
				}
				checked := 0
				for _, props := range docs {
					if !p.LiveFor(props) {
						continue
					}
					checked++
					tr := newTransformer()
					app, err := tr.ValidateAuthoredProperties(&oam.Application{
						APIVersion: oam.SupportedAPIVersion,
						Kind:       "Application",
						Metadata:   oam.Metadata{Name: "doc", Namespace: "default"},
						Spec:       oam.ApplicationSpec{Components: []oam.Component{lt.build(props)}},
					})
					if err != nil {
						t.Fatalf("%s/%s: ValidateAuthoredProperties: %v", typ, key, err)
					}
					// No generate step: a helmtemplate renders its chart there.
					_, _, prov, err := tr.TransformWithProvenance(app, oam.TransformContext{Namespace: "default", Policy: &hostNamespacePolicy{}})
					if err != nil {
						t.Fatalf("%s/%s: TransformWithProvenance: %v", typ, key, err)
					}
					copied := false
					for _, e := range prov.Entries {
						if e.Component != "app" || (e.Member != "" && e.Member != p.Member) ||
							!within(reportedAt{p.Member, e.Trait, e.Path}, p) {
							continue
						}
						// helm forms no same-name group, so its entries name no member.
						if typ == "helm" && e.Member != "" {
							t.Errorf("helm: the entry at %s is under member %q, want none", e.Path, e.Member)
						}
						if e.Source.Kind == oam.SourceRendered {
							t.Errorf("%s: %q is declared unchanged at %v, but the rule built the value at %s", typ, key, p, e.Path)
						}
						copied = copied || e.Source.Kind == oam.SourceCopied && e.Source.From == oam.SourceAuthored
					}
					if !copied {
						t.Errorf("%s: %q is declared unchanged at %v, but no entry there is copied from the authored value", typ, key, p)
					}
				}
				if checked == 0 {
					t.Errorf("%s: no document reaches the unchanged place %v of %q", typ, p, key)
				}
			}
		}
	}
}

// The conditions of every helm place, value for value, and its unchanged places.
// The variants cannot see a condition that is too broad for a key their mode
// refuses, or an explicit default it leaves out, so each is stated here: a
// helmtemplate place applies under delivery: template; the configmap trait, and
// the valuesFrom entry of values and valuesConfigMapName, under flux with
// valuesMode: configMap; values on the release under flux with values inline;
// every other place under flux, the default spelled or left out.
func TestLoweringKeys_HelmConditionsAndUnchanged(t *testing.T) {
	flux := oam.LoweredWhen{Key: "delivery", Values: []string{"", "flux"}}
	var unchanged []string
	for key, places := range keyedRules()["helm"].LoweringKeys() {
		for _, p := range places {
			want := []oam.LoweredWhen{flux}
			switch {
			case p.Member == "helmtemplate":
				want = []oam.LoweredWhen{{Key: "delivery", Values: []string{"template"}}}
			case p.Trait == "configmap", p.Path == "valuesFrom" && (key == "values" || key == "valuesConfigMapName"):
				want = []oam.LoweredWhen{flux, {Key: "valuesMode", Values: []string{"configMap"}}}
			case key == "values" && p.Path == "values":
				want = []oam.LoweredWhen{flux, {Key: "valuesMode", Values: []string{"", "inline"}}}
			}
			if !reflect.DeepEqual(p.When, want) {
				t.Errorf("%s: place %s %s %q is conditional on %v, want %v", key, p.Member, p.Trait, p.Path, p.When, want)
			}
			if p.Unchanged {
				unchanged = append(unchanged, p.Member+" "+p.Path)
			}
		}
	}
	slices.Sort(unchanged)
	want := []string{
		"helmrelease driftDetection", "helmrelease install", "helmrelease interval", "helmrelease releaseName",
		"helmrelease targetNamespace", "helmrelease upgrade", "helmrelease values",
		"helmtemplate chart", "helmtemplate hookGroupNamePrefix", "helmtemplate layoutKustomizationName",
		"helmtemplate releaseName", "helmtemplate scopeOverrides", "helmtemplate secretValues",
		"helmtemplate values", "helmtemplate version",
	}
	if !slices.Equal(unchanged, want) {
		t.Errorf("unchanged places = %v, want %v", unchanged, want)
	}
}

// A generated helm source is a component of its own: named by source.name, or
// shared by the helm components of one identity. The map names member types, not
// instances, so its source places name that component. This pins the names and
// the references, as TestLoweringKeys_OCINamedSourceIsAComponentOfItsOwn does,
// and that helm forms no same-name group under either delivery.
func TestLoweringKeys_HelmSourceIsAComponentOfItsOwn(t *testing.T) {
	t.Run("no same-name group", func(t *testing.T) {
		for _, delivery := range []string{"flux", "template"} {
			comp := oam.Component{Name: "app", Type: "helm", Properties: map[string]any{
				"chart": "web", "delivery": delivery, "source": map[string]any{"url": "https://charts.example.org/base"},
			}}
			res, err := registry.ComponentLoweringRules()["helm"].LowerComponent(&comp, lowerContext())
			if err != nil {
				t.Fatalf("LowerComponent(%s): %v", delivery, err)
			}
			names := map[string]int{}
			for _, c := range res.Components {
				names[c.Name]++
			}
			for name, n := range names {
				if n != 1 {
					t.Errorf("delivery %s: %d components are named %q, want 1", delivery, n, name)
				}
			}
		}
		// The report of the flux document, its release and its source alike.
		prov, _ := policyProvenanceFor(t, &defaultsPolicy{}, oam.Component{Name: "app", Type: "helm", Properties: map[string]any{
			"chart": "web", "source": map[string]any{"url": "https://charts.example.org/base"},
		}})
		owners := map[string]bool{}
		for _, e := range prov.Entries {
			owners[e.Component] = true
			if e.Member != "" {
				t.Errorf("entry %s of %q is under member %q, want none", e.Path, e.Component, e.Member)
			}
		}
		if !owners["app"] || len(owners) < 2 {
			t.Errorf("the report holds entries of %v, want the release and its source", owners)
		}
	})
	lower := func(t *testing.T, ctx oam.LoweringContext, name string, source map[string]any) (sources []string, ref any) {
		t.Helper()
		comp := oam.Component{Name: name, Type: "helm", Properties: map[string]any{"chart": "web", "source": source}}
		res, err := registry.ComponentLoweringRules()["helm"].LowerComponent(&comp, ctx)
		if err != nil {
			t.Fatalf("LowerComponent(%s): %v", name, err)
		}
		for _, c := range res.Components {
			switch c.Type {
			case "helmrelease":
				chart, _ := c.Properties["chart"].(map[string]any)
				spec, _ := chart["spec"].(map[string]any)
				sr, _ := spec["sourceRef"].(map[string]any)
				ref = sr["name"]
			case "helmrepository":
				sources = append(sources, c.Name)
			}
		}
		return sources, ref
	}
	url := "https://charts.example.org/base"

	t.Run("named", func(t *testing.T) {
		sources, ref := lower(t, lowerContext(), "app", map[string]any{"url": url, "name": "shared-source"})
		if !slices.Equal(sources, []string{"shared-source"}) || ref != "shared-source" {
			t.Errorf("sources %v, sourceRef %v, want one source shared-source that the release names", sources, ref)
		}
	})
	t.Run("shared", func(t *testing.T) {
		ctx := lowerContext()
		first, refA := lower(t, ctx, "a", map[string]any{"url": url})
		second, refB := lower(t, ctx, "b", map[string]any{"url": url})
		if len(first) != 1 || len(second) != 0 || refA != refB || first[0] != refA || first[0] == "a" || first[0] == "b" {
			t.Errorf("sources %v then %v, references %v and %v, want one source of its own that both name", first, second, refA, refB)
		}
	})
}
