package registry_test

import (
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/registry"
)

// These tests hold the key map a lowered component type publishes
// (oam.LoweringKeyDeclarer, go-kure/launcher#1580) to what its rule does: for
// each key of the type's schema, what the rule emits for a component that
// authors the key changes at the declared places and at no other, and the
// provenance report of such a document speaks there.

// rulesWithoutKeyMap names the built-in component lowering rules that publish no
// key map, each with its reason (go-kure/launcher#1601 carries them). A rule's
// map comes from the function the rule forwards with where it has one; the
// places of the keys it builds are declared beside the rule and held to what it
// emits by the tests below, in both directions. That is what
// go-kure/launcher#1580 asks of a map, and its comment
// https://github.com/go-kure/launcher/issues/1580#issuecomment-6903630690
// rejects only "a hand-kept table with nothing behind it".
var rulesWithoutKeyMap = map[string]string{
	"postgresql": "no forwarding loop: it splits the component into five CloudNativePG member types and two policies, under their own property spellings",
}

// keyedRules returns the built-in component lowering rules that publish a key
// map, by component type.
func keyedRules() map[string]oam.LoweringKeyDeclarer {
	out := map[string]oam.LoweringKeyDeclarer{}
	for name, r := range registry.ComponentLoweringRules() {
		if d, ok := r.(oam.LoweringKeyDeclarer); ok {
			out[name] = d
		}
	}
	return out
}

// Every built-in component lowering rule publishes a key map or is named in
// rulesWithoutKeyMap, never both and never neither: a lowered type added to the
// registry cannot ship without a decision.
func TestLoweringKeys_EveryBuiltinRuleHasAMapOrAReason(t *testing.T) {
	keyed := keyedRules()
	for name := range registry.ComponentLoweringRules() {
		_, hasMap := keyed[name]
		reason, exempt := rulesWithoutKeyMap[name]
		switch {
		case hasMap && exempt:
			t.Errorf("rule %q publishes a key map and is listed as publishing none; remove its row", name)
		case !hasMap && !exempt:
			t.Errorf("rule %q publishes no key map (oam.LoweringKeyDeclarer) and has no row in rulesWithoutKeyMap", name)
		case exempt && strings.TrimSpace(reason) == "":
			t.Errorf("rule %q is listed with no reason", name)
		}
	}
	for name := range rulesWithoutKeyMap {
		if _, registered := registry.ComponentLoweringRules()[name]; !registered {
			t.Errorf("rulesWithoutKeyMap names %q, which is not a built-in component lowering rule", name)
		}
	}
	// The keyed set is the registered rules less the named exceptions, so a rule
	// leaves rulesWithoutKeyMap by publishing a map and by nothing else.
	want := slices.DeleteFunc(slices.Sorted(maps.Keys(registry.ComponentLoweringRules())), func(name string) bool {
		_, exempt := rulesWithoutKeyMap[name]
		return exempt
	})
	if got := slices.Sorted(maps.Keys(keyed)); !slices.Equal(got, want) {
		t.Errorf("rules that publish a key map = %v, want %v", got, want)
	}
}

// The map and the schema hold the same keys, so a key added to a rule's schema
// with no map entry fails here, and every place names a type the rule declares
// it lowers into.
func TestLoweringKeys_EverySchemaKeyHasAnEntry(t *testing.T) {
	keyed := keyedRules()
	for _, typ := range slices.Sorted(maps.Keys(keyed)) {
		t.Run(typ, func(t *testing.T) {
			d := keyed[typ]
			schema := d.(oam.PropertySchemaProvider).PropertySchema()
			keys := d.LoweringKeys()
			if got, want := slices.Sorted(maps.Keys(keys)), slices.Sorted(maps.Keys(schema)); !slices.Equal(got, want) {
				t.Errorf("key map keys = %v\nschema keys  = %v", got, want)
			}
			targets := d.(oam.LoweringTargetDeclarer).LoweringTargets()
			for key, places := range keys {
				for _, p := range places {
					if !slices.Contains(targets.ComponentTypes, p.Member) {
						t.Errorf("%s: member %q is not a component type the rule lowers into (%v)", key, p.Member, targets.ComponentTypes)
					}
					if p.Trait != "" && !slices.Contains(targets.TraitTypes, p.Trait) {
						t.Errorf("%s: trait %q is not a trait type the rule lowers into (%v)", key, p.Trait, targets.TraitTypes)
					}
				}
			}
			if got := newTransformer().ComponentLoweringKeys()[typ]; len(got) != len(keys) {
				t.Errorf("Transformer.ComponentLoweringKeys()[%q] has %d keys, the rule declares %d", typ, len(got), len(keys))
			}
		})
	}
}

// loweringKeySample is a value that authors one key of a lowered type.
type loweringKeySample struct {
	value any
	// with holds the other keys the value is only valid beside. They are in the
	// baseline too, so they are not what the comparison sees.
	with map[string]any
	// noEntry says why no declared place of the key holds a leaf the report can
	// name. Empty for every key whose value is readable from the report.
	noEntry string
	// lands holds the value the rule emits at a place it builds itself, for a key
	// the rule evaluates. A place on a member the type does not emit is skipped.
	lands map[reportedAt]any
	// alsoFills holds each place of another key that authoring this value makes
	// launcher fill, with the source the report gives it. The value does not land
	// there, so the key map declares no such place; the entry is listed here so
	// that one nobody listed still fails.
	alsoFills map[reportedAt]oam.SourceKind
	// removesMember names a member the rule no longer emits when this value is
	// authored, so its entries leave the report. Read both ways, as alsoFills is.
	removesMember string
	// notWritten names, for an object key whose schema declares sub-properties,
	// each one the value leaves out, with the reason: a sub-property the rule
	// refuses beside another one the value writes. Read both ways, as alsoFills is.
	notWritten map[string]string
	// needsPeers names each declared place the key moves only in a document
	// that holds another component, with the reason and the test that holds it.
	// The comparison here lowers one component alone, so it cannot see such a
	// place move: it skips the place, and fails if the place does move alone,
	// where the entry is stale.
	needsPeers map[reportedAt]string
}

// hostNamespacePolicy is defaultsPolicy allowing the host namespaces, so a
// sample can author true for them: a value no default supplies.
type hostNamespacePolicy struct{ defaultsPolicy }

func (*hostNamespacePolicy) AllowHostNetwork() bool { return true }
func (*hostNamespacePolicy) AllowHostPID() bool     { return true }
func (*hostNamespacePolicy) AllowHostIPC() bool     { return true }

// loweringKeySamples holds a sample for every key of the webservice and worker
// schemas. TestLoweringKeys_AuthoredKeyLandsWhereDeclared fails for a key with
// none.
func loweringKeySamples() map[string]loweringKeySample {
	stdin := map[string]any{"stdin": true}
	nameOnly := "names an object the component generates; it is no property of a member"
	return map[string]loweringKeySample{
		// Not the default topology key, so a rule that ignored it would show.
		"affinity": {value: map[string]any{"enablePodAntiAffinity": true, "topologyKey": "topology.kubernetes.io/zone"}, lands: map[reportedAt]any{
			{"role-deployment", "", "affinity.podAntiAffinity.preferredDuringSchedulingIgnoredDuringExecution[0].podAffinityTerm.topologyKey"}: "topology.kubernetes.io/zone",
		}},
		"args":                         {value: []any{"--verbose"}},
		"automountServiceAccountToken": {value: true},
		"command":                      {value: []any{"/app"}},
		"deploymentObjectName":         {value: "dep", noEntry: nameOnly},
		"dnsConfig":                    {value: map[string]any{"nameservers": []any{"10.0.0.10"}}},
		"dnsPolicy":                    {value: "Default"},
		"enableServiceLinks":           {value: false},
		"env":                          {value: []any{map[string]any{"name": "MODE", "value": "fast"}}},
		"envFrom":                      {value: []any{map[string]any{"configMapRef": map[string]any{"name": "settings"}}}},
		"hostAliases":                  {value: []any{map[string]any{"ip": "10.0.0.1", "hostnames": []any{"db.local"}}}},
		// true: what no default supplies (hostNamespacePolicy allows it).
		"hostIPC":          {value: true},
		"hostNetwork":      {value: true},
		"hostPID":          {value: true},
		"hostUsers":        {value: false},
		"hostname":         {value: "web"},
		"hostnameOverride": {value: "web.example.org"},
		// Not the image workload() writes, which the baseline holds.
		"image":              {value: "ghcr.io/org/other:v2"},
		"imagePullPolicy":    {value: "Always"},
		"imagePullSecrets":   {value: []any{map[string]any{"name": "registry"}}},
		"initContainers":     {value: []any{map[string]any{"name": "init", "image": "busybox:1"}}},
		"lifecycle":          {value: map[string]any{"preStop": map[string]any{"exec": map[string]any{"command": []any{"sleep", "5"}}}}},
		"minReadySeconds":    {value: 5},
		"nodeName":           {value: "node-1"},
		"nodeSelector":       {value: map[string]any{"disk": "ssd"}},
		"os":                 {value: map[string]any{"name": "linux"}},
		"paused":             {value: true},
		"podMetadata":        {value: map[string]any{"labels": map[string]any{"team": "a"}}},
		"podResources":       {value: map[string]any{"requests": map[string]any{"cpu": "1"}}},
		"podSecurityContext": {value: map[string]any{"runAsNonRoot": true}},
		"port": {value: 8080, lands: map[reportedAt]any{
			{"role-deployment", "", "ports[0].containerPort"}: 8080,
			{"service", "", "ports[0].port"}:                  8080,
		}},
		"preemptionPolicy":        {value: "Never"},
		"priorityClassName":       {value: "high"},
		"probes":                  {value: map[string]any{"readiness": map[string]any{"httpGet": map[string]any{"path": "/", "port": 8080}}}},
		"progressDeadlineSeconds": {value: 120},
		"readinessGates":          {value: []any{map[string]any{"conditionType": "example.org/ready"}}},
		// Three replicas make the topology-spread trait the rule attaches write
		// constraints, which the report holds under the trait.
		"replicas": {value: 3, alsoFills: map[reportedAt]oam.SourceKind{
			{"role-deployment", "topology-spread", "topologySpreadConstraints"}: oam.SourceBuiltin,
		}},
		"resizePolicy":         {value: []any{map[string]any{"resourceName": "cpu", "restartPolicy": "NotRequired"}}},
		"resourceClaims":       {value: []any{map[string]any{"name": "gpu", "resourceClaimName": "gpu-claim"}}},
		"resources":            {value: map[string]any{"requests": map[string]any{"cpu": "200m"}}},
		"revisionHistoryLimit": {value: 3},
		"runtimeClassName":     {value: "gvisor"},
		"schedulerName":        {value: "custom"},
		"schedulingGates":      {value: []any{map[string]any{"name": "example.org/gate"}}},
		"schedulingGroup":      {value: map[string]any{"podGroupName": "batch"}},
		"securityContext":      {value: map[string]any{"runAsNonRoot": true}},
		// An existing account: the rule generates none.
		"serviceAccountName": {value: "existing", removesMember: "serviceaccount"},
		// The deployment's pods run as the account under the name it was given.
		"serviceAccountObjectName": {value: "account", lands: map[reportedAt]any{
			{"role-deployment", "", "serviceAccountName"}: "account",
		}},
		"serviceObjectName":             {value: "svc", noEntry: nameOnly},
		"setHostnameAsFQDN":             {value: true},
		"shareProcessNamespace":         {value: true},
		"sidecars":                      {value: []any{map[string]any{"name": "side", "image": "busybox:1"}}},
		"stdin":                         {value: true},
		"stdinOnce":                     {value: true, with: stdin},
		"strategy":                      {value: map[string]any{"type": "Recreate"}},
		"subdomain":                     {value: "sub"},
		"terminationGracePeriodSeconds": {value: 10},
		"terminationMessagePath":        {value: "/tmp/termination"},
		"terminationMessagePolicy":      {value: "FallbackToLogsOnError"},
		"tolerations":                   {value: []any{map[string]any{"key": "dedicated", "operator": "Exists"}}},
		"topologySpread":                {value: false, noEntry: "the topology-spread trait the rule attaches holds no property, so the report has no leaf for it, present or absent"},
		"topologySpreadConstraints": {value: []any{map[string]any{"maxSkew": 1, "topologyKey": "zone", "whenUnsatisfiable": "DoNotSchedule", "labelSelector": map[string]any{"matchLabels": map[string]any{"app": "a"}}}},
			// Authored constraints and the rule's own spread are refused together.
			with: map[string]any{"topologySpread": false}},
		"tty": {value: true, with: stdin},
		"volumes": {value: []any{map[string]any{"name": "data", "type": "pvc", "size": "1Gi", "mountPath": "/data"}}, lands: map[reportedAt]any{
			{"role-deployment", "", "volumes[0].claimName"}: "app-data",
			{"role-deployment", "pvc", "size"}:              "1Gi",
		}, alsoFills: map[reportedAt]oam.SourceKind{
			// The claim names no access mode, so it is ReadWriteOnce, and no strategy
			// is authored: the deployment is given Recreate, at the place of the
			// `strategy` key.
			{"role-deployment", "", "strategy.type"}: oam.SourceBuiltin,
		}},
		"workingDir": {value: "/srv"},
	}
}

// loweredType is what the behavioural guard needs to know of one lowered type:
// how to build a component of it, the document every sample starts from, and a
// sample for every key of its schema.
type loweredType struct {
	// build makes the component the rule is handed from a set of properties.
	build func(props map[string]any) oam.Component
	// base holds the properties every document of the type carries: the required
	// keys. A sample of a required key replaces its value, so the baseline and
	// the authored document differ at that key alone.
	base    map[string]any
	samples map[string]loweringKeySample
	// forwardsWhole exempts the type from
	// TestLoweringKeys_SamplesWriteEverySubProperty, which every other type
	// meets. It is true for a role type only: it hands an object key to its
	// deployment member whole, by the function that decides which keys it
	// forwards, so no sub-property can land elsewhere; the keys it builds are
	// held by their `lands` rows.
	forwardsWhole bool
	// variants replaces samples, per key, for a type whose places are
	// conditional (LoweredKey.When), and switches names its keys that declare no
	// place because they choose what the rule emits, each with the reason.
	variants map[string][]keyVariant
	switches map[string]string
}

// lowerContext is the context a rule is handed to lower one document.
func lowerContext() oam.LoweringContext {
	return oam.LoweringContext{
		Namer:  oam.NewNameAllocator(),
		Origin: oam.Origin{Document: "doc", DocumentKind: "Application", Namespace: "default"},
	}
}

// loweredTypes holds a loweredType for every rule that publishes a key map.
// TestLoweringKeys_AuthoredValueReachesTheDeclaredPlaces fails for a rule with
// none.
func loweredTypes() map[string]loweredType {
	role := func(typ string) loweredType {
		return loweredType{
			// workload writes the `image` every role document carries.
			build:         func(props map[string]any) oam.Component { return workload("app", typ, props) },
			samples:       loweringKeySamples(),
			forwardsWhole: true,
		}
	}
	return map[string]loweredType{
		"helm": {
			build: func(props map[string]any) oam.Component {
				return oam.Component{Name: "app", Type: "helm", Properties: props}
			},
			variants: helmKeyVariants(),
			switches: helmSwitches,
		},
		"webservice": role("webservice"),
		"worker":     role("worker"),
		"oci": {
			build: func(props map[string]any) oam.Component {
				return oam.Component{Name: "app", Type: "oci", Properties: props}
			},
			base:    map[string]any{"source": map[string]any{"url": "oci://registry.example.org/base"}, "version": "1.0.0"},
			samples: ociKeySamples(),
		},
	}
}

// ociKeySamples holds a sample for every key of the oci schema. The values differ
// from the base document's and from each default, so a key the rule ignored would
// show.
func ociKeySamples() map[string]loweringKeySample {
	// A key of the source identity moves sourceRef through the identity a shared
	// source is named by, which a lowering of one component alone has no peer to
	// share.
	sourceRefNeedsPeers := map[reportedAt]string{
		{"fluxcd-kustomization", "", "sourceRef"}: "moves it through the identity a source is shared by, which needs another component; TestLoweringKeys_OCISharedSourceMovesSourceRef holds it",
	}
	return map[string]loweringKeySample{
		// Every key of source the rule hands on or builds from, together: the report
		// of each declared place needs the value that changes it.
		"source": {value: map[string]any{
			"url":                "oci://registry.example.org/other",
			"objectName":         "src",
			"secretRef":          map[string]any{"name": "creds"},
			"certSecretRef":      map[string]any{"name": "ca"},
			"proxySecretRef":     map[string]any{"name": "proxy"},
			"provider":           "aws",
			"insecure":           true,
			"serviceAccountName": "puller",
		}, notWritten: map[string]string{
			"name": "refused beside source.objectName, which the value writes; TestLoweringKeys_OCINamedSourceIsAComponentOfItsOwn writes it",
		}},
		"kustomizationName": {value: "delivery"},
		"version":           {value: "2.0.0", needsPeers: sourceRefNeedsPeers},
		"path":              {value: "./deploy"},
		"prune":             {value: false},
		"interval":          {value: "10m", needsPeers: sourceRefNeedsPeers},
		"targetNamespace":   {value: "apps"},
		"wait":              {value: true},
		"healthChecks":      {value: []any{map[string]any{"apiVersion": "apps/v1", "kind": "Deployment", "name": "app", "namespace": "apps"}}},
	}
}

// subPropertiesOf returns the sub-properties a schema declares for an object, or
// for each entry of a list of objects. An object that declares none (an open one)
// has none to read.
func subPropertiesOf(s oam.PropertySchema) []string {
	props := s.Properties
	if len(props) == 0 && s.Items != nil {
		props = s.Items.Properties
	}
	return slices.Sorted(maps.Keys(props))
}

// writtenSubProperties returns the keys v writes: the keys of an object, or the
// keys of every entry of a list of objects.
func writtenSubProperties(v any) map[string]bool {
	out := map[string]bool{}
	switch x := v.(type) {
	case map[string]any:
		for k := range x {
			out[k] = true
		}
	case []any:
		for _, e := range x {
			maps.Copy(out, writtenSubProperties(e))
		}
	}
	return out
}

// The guard above reads only what a sample writes: a sub-property the sample
// leaves out is a place nothing proved. So a sample of an object key writes every
// sub-property the schema declares, or lists it in notWritten with the reason,
// and an entry there that the value writes, or the schema does not declare, is
// stale. Sub-properties below the first level, and an object that declares none,
// are the limit: they are not read.
func TestLoweringKeys_SamplesWriteEverySubProperty(t *testing.T) {
	types := loweredTypes()
	keyed := keyedRules()
	for _, typ := range slices.Sorted(maps.Keys(keyed)) {
		lt, ok := types[typ]
		if !ok || lt.forwardsWhole {
			continue // a missing row is named by TestLoweringKeys_AuthoredValueReachesTheDeclaredPlaces
		}
		schema := keyed[typ].(oam.PropertySchemaProvider).PropertySchema()
		for _, key := range slices.Sorted(maps.Keys(schema)) {
			subs := subPropertiesOf(schema[key])
			sample, ok := lt.samples[key]
			if !ok && len(lt.variants[key]) == 0 {
				continue // the same test names a key with no sample
			}
			t.Run(typ+"/"+key, func(t *testing.T) {
				written := writtenSubProperties(sample.value)
				// The variants of a key write its sub-properties between them.
				for _, v := range lt.variants[key] {
					maps.Copy(written, writtenSubProperties(v.value))
				}
				for _, sub := range subs {
					_, listed := sample.notWritten[sub]
					switch {
					case !written[sub] && !listed:
						t.Errorf("the sample of %q leaves out the sub-property %q and gives no reason: write it, or list it in notWritten", key, sub)
					case written[sub] && listed:
						t.Errorf("the sample of %q writes %q and lists it in notWritten: remove the entry", key, sub)
					}
				}
				for sub, reason := range sample.notWritten {
					if !slices.Contains(subs, sub) {
						t.Errorf("notWritten of %q names %q, which the schema does not declare", key, sub)
					}
					if strings.TrimSpace(reason) == "" {
						t.Errorf("notWritten of %q lists %q with no reason", key, sub)
					}
				}
			})
		}
	}
}

// ociLowered lowers the oci components a and b of one document, in that order,
// through the oci rule with one context, as the engine does, and returns the
// source each Kustomization names, by component name, and the objects of the
// sources emitted. Every reference must name an emitted source.
func ociLowered(t *testing.T, a, b oam.Component) (refs map[string]string, sources []string) {
	t.Helper()
	ctx := oam.LoweringContext{
		Document: &oam.Application{Spec: oam.ApplicationSpec{Components: []oam.Component{a, b}}},
		Namer:    oam.NewNameAllocator(),
		Origin:   oam.Origin{Document: "doc", DocumentKind: "Application", Namespace: "default"},
	}
	refs = map[string]string{}
	for _, comp := range []oam.Component{a, b} {
		res, err := registry.ComponentLoweringRules()["oci"].LowerComponent(&comp, ctx)
		if err != nil {
			t.Fatalf("LowerComponent(%s): %v", comp.Name, err)
		}
		for _, c := range res.Components {
			switch c.Type {
			case "fluxcd-kustomization":
				ref, _ := c.Properties["sourceRef"].(map[string]any)
				refs[comp.Name], _ = ref["name"].(string)
			case "ocirepository":
				sources = append(sources, c.ObjectName())
			}
		}
	}
	for _, name := range []string{a.Name, b.Name} {
		if refs[name] == "" {
			t.Errorf("no source is named by a Kustomization of %s", name)
		}
	}
	for name, ref := range refs {
		if !slices.Contains(sources, ref) {
			t.Errorf("the Kustomization of %s names the source %q, and the sources emitted are %v", name, ref, sources)
		}
	}
	slices.Sort(sources)
	return refs, sources
}

// Two oci components of one document that name no source and have the same
// identity share one: the document holds one source and the Kustomization of
// each names it. The version and the interval are part of that identity, so
// changing either changes the source a Kustomization names. Authored on one of
// the two it ends the sharing, and each Kustomization names a source of its own.
// Authored on both they still share, under another name. The map declares the
// place, and the single-component guard above cannot see the move, because it
// lowers with no document.
func TestLoweringKeys_OCISharedSourceMovesSourceRef(t *testing.T) {
	lt := loweredTypes()["oci"]
	keys := keyedRules()["oci"].LoweringKeys()
	named := func(props map[string]any, name string) oam.Component {
		c := lt.build(props)
		c.Name = name
		return c
	}
	with := func(key string, value any) map[string]any {
		props := maps.Clone(lt.base)
		props[key] = value
		return props
	}
	refs, sources := ociLowered(t, named(lt.base, "a"), named(lt.base, "b"))
	shared := refs["a"]
	if shared == "" || shared == "a" || shared == "b" || refs["b"] != shared || len(sources) != 1 {
		t.Fatalf("two components of one identity: references %v, sources %v, want one shared source named by both", refs, sources)
	}
	for key, value := range map[string]any{"version": "2.0.0", "interval": "10m"} {
		t.Run(key, func(t *testing.T) {
			refs, sources := ociLowered(t, named(lt.base, "a"), named(with(key, value), "b"))
			if refs["a"] != "a" || refs["b"] != "b" || !slices.Equal(sources, []string{"a", "b"}) {
				t.Errorf("authoring %q on one of two sharing components: references %v, sources %v, want a source of its own for each", key, refs, sources)
			}
			// The same key on both leaves them sharing, under a name of the changed
			// identity: one source, named by both, and not the first shared name.
			refs, sources = ociLowered(t, named(with(key, value), "a"), named(with(key, value), "b"))
			if refs["a"] != refs["b"] || refs["a"] == shared || refs["a"] == "a" || refs["a"] == "b" || len(sources) != 1 {
				t.Errorf("authoring %q on both components: references %v, sources %v, want one shared source named differently from %q", key, refs, sources, shared)
			}
			if !slices.ContainsFunc(keys[key], func(p oam.LoweredKey) bool {
				return p.Member == "fluxcd-kustomization" && p.Path == "sourceRef"
			}) {
				t.Errorf("%q moves sourceRef when the identity of a source changes, and the key map does not declare it: %v", key, keys[key])
			}
		})
	}
}

// With source.name written, or a source another oci component shares, the
// ocirepository is a component of its own, named for the source, and the
// fluxcd-kustomization is no member of a group with it. The key map names member
// types, not instances, so its source places name that component and not a
// sibling of this one: the limit the docs state. This pins the member types and
// names and the Kustomization's sourceRef, so a rule that changes them fails here
// and the docs are read again. It does not pin the order the two apply in.
func TestLoweringKeys_OCINamedSourceIsAComponentOfItsOwn(t *testing.T) {
	lt := loweredTypes()["oci"]
	props := maps.Clone(lt.base)
	props["source"] = map[string]any{"url": "oci://registry.example.org/base", "name": "shared-source"}
	comp := lt.build(props)
	ctx := oam.LoweringContext{
		Document: &oam.Application{Spec: oam.ApplicationSpec{Components: []oam.Component{comp}}},
		Namer:    oam.NewNameAllocator(),
		Origin:   oam.Origin{Document: "doc", DocumentKind: "Application", Namespace: "default"},
	}
	res, err := registry.ComponentLoweringRules()["oci"].LowerComponent(&comp, ctx)
	if err != nil {
		t.Fatalf("LowerComponent: %v", err)
	}
	got := map[string]string{} // member type -> component name
	for _, c := range res.Components {
		got[c.Type] = c.Name
	}
	want := map[string]string{"ocirepository": "shared-source", "fluxcd-kustomization": "app"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("components by type = %v, want %v", got, want)
	}
	for _, c := range res.Components {
		if c.Type != "fluxcd-kustomization" {
			continue
		}
		if ref, _ := c.Properties["sourceRef"].(map[string]any); ref["name"] != "shared-source" {
			t.Errorf("sourceRef = %v, want it to name shared-source", ref)
		}
	}
	// The report names such a Kustomization with no member, for a named source
	// and for two components that share one.
	other := lt.build(lt.base)
	other.Name = "other"
	for name, doc := range map[string][]oam.Component{"named": {comp}, "shared": {lt.build(lt.base), other}} {
		prov, _ := policyProvenanceFor(t, &defaultsPolicy{}, doc...)
		for _, c := range doc {
			n := 0
			for _, e := range prov.Entries {
				if e.Component != c.Name {
					continue
				}
				n++
				if e.Member != "" {
					t.Errorf("%s: entry %s of %q is under member %q, want none", name, e.Path, c.Name, e.Member)
				}
			}
			if n == 0 {
				t.Errorf("%s: the report holds no entry of %q", name, c.Name)
			}
		}
	}
}

// reportedAt is where one entry of the report sits, in a key map's terms.
type reportedAt struct{ member, trait, path string }

// emitsOneComponent reports whether a component of type typ with props lowers to
// its deployment alone: a worker that names an existing account generates none.
// Every other webservice or worker lowers to a same-name sibling group.
func emitsOneComponent(typ string, props map[string]any) bool {
	// A null names no account: the rule reads it as absent.
	return typ == "worker" && props["serviceAccountName"] != nil
}

// reportOf returns the entries of component "app" by place. alone says the rule
// emitted its deployment alone (emitsOneComponent): the report then names no
// member, and the entries are read as that member's (LoweredKey.Member). In a
// group every entry names its member, and one that names none fails here, before
// it could be read as the deployment's.
func reportOf(t *testing.T, p *oam.Provenance, alone bool) map[reportedAt]oam.Source {
	t.Helper()
	out := map[reportedAt]oam.Source{}
	for _, e := range p.Entries {
		member := e.Member
		switch {
		case alone && member != "":
			t.Errorf("entry at %q is under member %q; a rule that emits one component leaves no member", e.Path, member)
		case alone:
			member = "role-deployment"
		case member == "":
			t.Errorf("entry at %q (trait %q) names no member; every entry of a sibling group names its member", e.Path, e.Trait)
		}
		out[reportedAt{member, e.Trait, e.Path}] = e.Source
	}
	return out
}

// within reports whether an entry at r is at or below the declared place p.
func within(r reportedAt, p oam.LoweredKey) bool {
	if r.member != p.Member || r.trait != p.Trait {
		return false
	}
	return p.Path == "" || r.path == p.Path ||
		strings.HasPrefix(r.path, p.Path+".") || strings.HasPrefix(r.path, p.Path+"[")
}

// For every key of a lowered type's schema: a document that authors the key is
// reported at each declared place, and at no place the map does not declare for
// that key. An Unchanged place holds the authored value as the rule received it
// (copied, from authored) and nothing the rule built. A fill the key causes at the
// place of another key is a row of its sample (alsoFills), read both ways: a fill
// with no row fails, and so does a row with no fill. An entry the key removes is
// read the same way: it sat at a declared place, or on the member the sample says
// the key removes (removesMember).
//
// The comparison is against the same document without the key, so only what the
// key moved is read. `image` is required and has no such baseline: for it every
// copied entry of the report must sit at its place.
//
// oci and helm are held on their Unchanged places only (unchangedPlacesAreCopied).
func TestLoweringKeys_AuthoredKeyLandsWhereDeclared(t *testing.T) {
	t.Run("unchanged", unchangedPlacesAreCopied)
	samples := loweringKeySamples()
	keyed := keyedRules()
	// Named, not ranged over keyed: a rule that lost its map must fail here,
	// never leave the test with nothing to check.
	for _, typ := range []string{"webservice", "worker"} {
		d, ok := keyed[typ]
		if !ok {
			t.Errorf("rule %q does not implement oam.LoweringKeyDeclarer", typ)
			continue
		}
		schema := d.(oam.PropertySchemaProvider).PropertySchema()
		keys := d.LoweringKeys()
		for _, key := range slices.Sorted(maps.Keys(schema)) {
			t.Run(typ+"/"+key, func(t *testing.T) {
				sample, ok := samples[key]
				if !ok {
					t.Fatalf("no sample for key %q; add a row to loweringKeySamples", key)
				}
				places, declared := keys[key]
				if !declared {
					t.Fatalf("the key map has no entry for %q", key)
				}

				authored := maps.Clone(sample.with)
				if authored == nil {
					authored = map[string]any{}
				}
				authored[key] = sample.value
				prov, _ := policyProvenanceFor(t, &hostNamespacePolicy{}, workload("app", typ, authored))
				report := reportOf(t, prov, emitsOneComponent(typ, authored))

				var baseline map[reportedAt]oam.Source
				if !schema[key].Required {
					base, _ := policyProvenanceFor(t, &hostNamespacePolicy{}, workload("app", typ, sample.with))
					baseline = reportOf(t, base, emitsOneComponent(typ, sample.with))
				}

				// At no undeclared place.
				for at, src := range report {
					if baseline != nil {
						if was, had := baseline[at]; had && was == src {
							continue
						}
					} else if src.Kind != oam.SourceCopied {
						continue
					}
					if slices.ContainsFunc(places, func(p oam.LoweredKey) bool { return within(at, p) }) {
						continue
					}
					if kind, listed := sample.alsoFills[at]; listed && kind == src.Kind {
						continue
					}
					t.Errorf("authoring %q moved the entry at member %q trait %q path %q (%s), which the key map does not declare for it (%v)",
						key, at.member, at.trait, at.path, src.Kind, places)
				}
				// A listed fill that no longer happens is a stale row.
				for at, kind := range sample.alsoFills {
					if src, ok := report[at]; !ok || src.Kind != kind || baseline[at] == src {
						t.Errorf("sample says authoring %q makes launcher fill member %q path %q (%s), but the report does not show that fill", key, at.member, at.path, kind)
					}
				}
				// An entry the key removes is moved too: it sat at a declared place,
				// or on the member the sample says the key removes.
				removedOnMember := false
				for at, was := range baseline {
					if _, still := report[at]; still {
						continue
					}
					if slices.ContainsFunc(places, func(p oam.LoweredKey) bool { return within(at, p) }) {
						continue
					}
					if sample.removesMember != "" && at.member == sample.removesMember {
						removedOnMember = true
						continue
					}
					t.Errorf("authoring %q removed the entry at member %q trait %q path %q (%s), which the key map does not declare for it (%v)",
						key, at.member, at.trait, at.path, was.Kind, places)
				}
				if sample.removesMember != "" && !removedOnMember {
					t.Errorf("sample says authoring %q removes member %q, but no entry of that member left the report", key, sample.removesMember)
				}

				// At every declared place.
				if sample.noEntry != "" {
					for at := range report {
						if slices.ContainsFunc(places, func(p oam.LoweredKey) bool { return within(at, p) }) && baseline[at] != report[at] {
							t.Errorf("sample says %q leaves no entry (%s), but the report holds one at %v", key, sample.noEntry, at)
						}
					}
					return
				}
				if len(places) == 0 {
					t.Fatalf("the key map declares no place for %q, and the sample gives no reason it leaves no entry", key)
				}
				for _, p := range places {
					var found, copied bool
					for at, src := range report {
						if !within(at, p) {
							continue
						}
						found = true
						if src.Kind == oam.SourceCopied && src.From == oam.SourceAuthored {
							copied = true
						}
						if p.Unchanged && src.Kind == oam.SourceRendered {
							t.Errorf("%q is declared unchanged at %v, but the rule built the value at %v", key, p, at)
						}
					}
					switch {
					case !found:
						t.Errorf("authoring %q left no entry at the declared place %v", key, p)
					case p.Unchanged && !copied:
						t.Errorf("%q is declared unchanged at %v, but no entry there is copied from the authored value", key, p)
					}
				}
			})
		}
	}
}

// loweredLeaves lowers a component of type typ with props through its rule
// alone, with no engine and no policy, and returns every leaf of what the rule
// emits by place, and each member's properties by member type. An attached trait
// is a place of its own (path ""), so one that holds no property is still seen.
func loweredLeaves(t *testing.T, typ string, comp oam.Component) (map[reportedAt]any, map[string]map[string]any) {
	t.Helper()
	res, err := registry.ComponentLoweringRules()[typ].LowerComponent(&comp, lowerContext())
	if err != nil {
		t.Fatalf("LowerComponent(%s, %v): %v", typ, comp.Properties, err)
	}
	leaves := map[reportedAt]any{}
	members := map[string]map[string]any{}
	for _, c := range res.Components {
		members[c.Type] = c.Properties
		flattenLeaves(leaves, c.Type, "", "", c.Properties)
		for _, tr := range c.Traits {
			leaves[reportedAt{c.Type, tr.Type, ""}] = true
			flattenLeaves(leaves, c.Type, tr.Type, "", tr.Properties)
		}
	}
	return leaves, members
}

// changedLeaves returns the places whose leaf differs from base to got: a leaf
// that is new or has another value, and one that is gone from a member got still
// holds.
func changedLeaves(got, base map[reportedAt]any, members map[string]map[string]any) []reportedAt {
	var changed []reportedAt
	for at, v := range got {
		if was, had := base[at]; !had || !reflect.DeepEqual(was, v) {
			changed = append(changed, at)
		}
	}
	for at := range base {
		_, emitted := members[at.member]
		if _, still := got[at]; emitted && !still {
			changed = append(changed, at)
		}
	}
	return changed
}

// flattenLeaves records every leaf below v under its path, in the spelling
// PropertySource.Path uses.
func flattenLeaves(out map[reportedAt]any, member, trait, path string, v any) {
	switch x := v.(type) {
	case map[string]any:
		for k, e := range x {
			sub := k
			if path != "" {
				sub = path + "." + k
			}
			flattenLeaves(out, member, trait, sub, e)
		}
		if len(x) > 0 || path == "" {
			return
		}
	case []any:
		for i, e := range x {
			flattenLeaves(out, member, trait, fmt.Sprintf("%s[%d]", path, i), e)
		}
		if len(x) > 0 {
			return
		}
	}
	out[reportedAt{member, trait, path}] = v
}

// For every key of a lowered type's schema, read from what the rule emits, not
// from the report (which names sources and holds no value): authoring the key
// changes a value at each declared place and at no other, an Unchanged place
// holds the authored value itself, and a place the rule builds holds the value
// the sample expects (lands).
//
// The comparison is against the same component without the key; `image` is
// required, so its baseline holds another image. The rule emits the same members
// with and without a key, except one: `serviceAccountName` names an existing
// account, so the generated one is gone, and that key lands nowhere on it.
func TestLoweringKeys_AuthoredValueReachesTheDeclaredPlaces(t *testing.T) {
	types := loweredTypes()
	keyed := keyedRules()
	// Ranged over the rules that publish a map, so a type with no sample table
	// fails here and never leaves the test with nothing to check.
	for _, typ := range slices.Sorted(maps.Keys(keyed)) {
		d := keyed[typ]
		lt, ok := types[typ]
		if !ok {
			t.Errorf("rule %q publishes a key map and has no row in loweredTypes", typ)
			continue
		}
		schema := d.(oam.PropertySchemaProvider).PropertySchema()
		keys := d.LoweringKeys()
		for _, key := range slices.Sorted(maps.Keys(schema)) {
			t.Run(typ+"/"+key, func(t *testing.T) {
				if lt.variants != nil {
					loweredVariants(t, typ, key, lt, keys[key])
					return
				}
				sample, ok := lt.samples[key]
				if !ok {
					t.Fatalf("no sample for key %q of %q; add a row to its samples", key, typ)
				}
				places := keys[key]
				declared := func(at reportedAt) bool {
					return slices.ContainsFunc(places, func(p oam.LoweredKey) bool { return within(at, p) })
				}

				// The baseline holds the type's base document and the keys the value is
				// only valid beside; the authored document adds the key under test to
				// it, over the base's own value for a required key.
				baseline := maps.Clone(lt.base)
				if baseline == nil {
					baseline = map[string]any{}
				}
				maps.Copy(baseline, sample.with)
				authored := maps.Clone(baseline)
				authored[key] = sample.value
				got, members := loweredLeaves(t, typ, lt.build(authored))
				base, baseMembers := loweredLeaves(t, typ, lt.build(baseline))

				// The members are the same with and without the key, except the one
				// the sample says the key removes (a serviceAccountName replaces the
				// generated account).
				wantMembers := slices.Sorted(maps.Keys(baseMembers))
				if sample.removesMember != "" {
					wantMembers = slices.DeleteFunc(wantMembers, func(m string) bool { return m == sample.removesMember })
				}
				if gotMembers := slices.Sorted(maps.Keys(members)); !slices.Equal(gotMembers, wantMembers) {
					t.Fatalf("authoring %q: the rule emits members %v, want %v", key, gotMembers, wantMembers)
				}

				changed := changedLeaves(got, base, members)
				for _, at := range changed {
					if !declared(at) {
						t.Errorf("authoring %q changed member %q trait %q path %q (%v, was %v), which the key map does not declare for it (%v)",
							key, at.member, at.trait, at.path, got[at], base[at], places)
					}
				}
				for _, p := range places {
					moved := slices.ContainsFunc(changed, func(at reportedAt) bool { return within(at, p) })
					if _, peers := sample.needsPeers[reportedAt{p.Member, p.Trait, p.Path}]; peers {
						if moved {
							t.Errorf("authoring %q moves %v on its own, and the sample lists it as needing peers: remove the entry", key, p)
						}
						continue
					}
					if !moved {
						t.Errorf("authoring %q changed nothing at the declared place %v", key, p)
					}
					if !p.Unchanged {
						continue
					}
					if v := members[p.Member][p.Path]; !reflect.DeepEqual(v, sample.value) {
						t.Errorf("%q is declared unchanged at %v, but the member holds %v, not the authored %v", key, p, v, sample.value)
					}
				}
				for at, want := range sample.lands {
					if _, emitted := members[at.member]; !emitted {
						continue
					}
					if v := got[at]; !reflect.DeepEqual(v, want) {
						t.Errorf("authoring %q: member %q trait %q path %q holds %v, want %v", key, at.member, at.trait, at.path, v, want)
					}
				}
			})
		}
	}
}

// A worker with an authored serviceAccountName generates no account, so its rule
// emits the deployment alone and the report names no member. The key map still
// names the deployment member: its places are read under the plain owner.
func TestLoweringKeys_SingleMemberWorkerIsReadUnderThePlainOwner(t *testing.T) {
	d, ok := keyedRules()["worker"]
	if !ok {
		t.Fatal(`rule "worker" does not implement oam.LoweringKeyDeclarer`)
	}
	prov, _ := policyProvenanceFor(t, &defaultsPolicy{}, workload("app", "worker", map[string]any{
		"serviceAccountName": "existing", "replicas": 3,
	}))
	for _, e := range prov.Entries {
		if e.Member != "" {
			t.Errorf("entry %s is under member %q; a worker that emits one component has no member", e.Path, e.Member)
		}
	}
	plain := entriesOf(prov, owner{component: "app"})
	for _, key := range []string{"image", "replicas", "serviceAccountName"} {
		want := []oam.LoweredKey{{Member: "role-deployment", Path: key, Unchanged: true}}
		if got := d.LoweringKeys()[key]; !reflect.DeepEqual(got, want) {
			t.Errorf("LoweringKeys()[%q] = %v, want %v", key, got, want)
		}
		if src, found := plain[key]; !found || src.Kind != oam.SourceCopied || src.From != oam.SourceAuthored {
			t.Errorf("plain owner entry at %q = %+v (found %v), want copied from authored", key, src, found)
		}
	}
}
