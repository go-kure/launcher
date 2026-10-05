package components

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"net/url"
	"strings"

	"github.com/go-kure/kure/pkg/kubernetes"
	"github.com/go-kure/kure/pkg/manifest"
	"github.com/go-kure/kure/pkg/stack/helm"
	"github.com/go-kure/kure/pkg/stack/layout"
	chartutil "helm.sh/helm/v4/pkg/chart/v2/util"
	apiextv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	yamlutil "k8s.io/apimachinery/pkg/util/yaml"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-kure/launcher/pkg/errors"
	"github.com/go-kure/launcher/pkg/oam"
)

// This file is the one implementation of client-side Helm chart rendering and
// of the Helm hook-group layout partition, run by HelmTemplateConfig (the
// kind-named helmtemplate terminal). It embeds chartRender, which renders once
// and caches the hook groups, and hands it its renderer, its chart source and
// the component type a render failure is prefixed with (a failure to parse the
// rendered output is not prefixed; see chartRender.render).

// renderChartFunc is the chart renderer: kure's helm.RenderChart, or a stub
// injected by a test. Variadic opts matches kure's RenderChart signature (kure
// v0.2.0-beta.10+, helm.RenderOption) so that helm.RenderChart itself
// satisfies it without a wrapper; chartRender.render passes the release
// identity through it (chartSource.renderOptions).
type renderChartFunc = func(chartURL, version string, values map[string]any, opts ...helm.RenderOption) ([]byte, error)

// chartSource is what one client-side render fetches: an inline chart source
// whose Kind inlineChartSourceKind has already resolved and checked against
// URL's scheme, the chart name within a HelmRepository, the chart version, the
// values tree handed to the render as-is, the sensitive values merged over it
// for the render (mergeSecretValues; the two share no path), the namespace the
// render uses as .Release.Namespace (empty leaves kure's default, "default"),
// the release name it uses as .Release.Name, which templateReleaseName has
// already resolved and checked (empty leaves kure's default, "release"), and
// the scope the document states for a rendered kind, which the namespace stamp
// reads (stampRenderedNamespaces).
type chartSource struct {
	URL            string
	Kind           string // "HelmRepository" or "OCIRepository"
	Chart          string
	Version        string
	Values         map[string]any
	SecretValues   map[string]any
	Namespace      string
	ReleaseName    string
	ScopeOverrides map[schema.GroupVersionKind]manifest.ScopeResult
}

// renderOptions turns the release name and the namespace into kure render
// options, each when set. The namespace only sets .Release.Namespace: kure
// stamps no metadata.namespace, so a chart that omits it still renders
// namespace-less objects. chartRender.render stamps those afterwards
// (stampRenderedNamespaces).
func (s chartSource) renderOptions() []helm.RenderOption {
	var opts []helm.RenderOption
	if s.ReleaseName != "" {
		opts = append(opts, helm.WithReleaseName(s.ReleaseName))
	}
	if s.Namespace != "" {
		opts = append(opts, helm.WithNamespace(s.Namespace))
	}
	return opts
}

// validHelmReleaseName reports whether name is a release name Helm accepts,
// by Helm's own rule (chartutil.ValidateReleaseName): a DNS-1123 subdomain of
// at most 53 characters. kure's render checks nothing, so a name Helm would
// refuse to install under would otherwise render.
func validHelmReleaseName(name string) bool {
	return chartutil.ValidateReleaseName(name) == nil
}

// helmReleaseNameRule completes a refused release name's message, in place of
// Helm's, which quotes its regular expression.
const helmReleaseNameRule = "must be a DNS-1123 subdomain of at most 53 characters, as a Helm release name is"

// templateReleaseName resolves the release name of a client-side render:
// releaseName when set, else the default every Helm release of the component
// gets (defaultHelmReleaseName), so a chart renders under the same name under
// either delivery of the helm rule. An authored name is never shortened; it
// must be a valid Helm release name (validHelmReleaseName). Every error is
// prefixed with owner, the component type.
func templateReleaseName(owner, releaseName, componentName string) (string, error) {
	if releaseName != "" {
		if !validHelmReleaseName(releaseName) {
			return "", errors.Errorf("%s: releaseName %q %s", owner, releaseName, helmReleaseNameRule)
		}
		return releaseName, nil
	}
	return defaultHelmReleaseName(owner, componentName)
}

// defaultHelmReleaseName is the release name of a Helm release whose component
// authors none, under either delivery (go-kure/launcher#785): the component
// name, shortened above 53 characters as Flux helm-controller shortens a
// release name (oam.ShortenName at oam.ShortenLimitHelmRelease). The helmrelease
// terminal writes it to spec.releaseName and the helmtemplate terminal renders
// under it, so a chart is released under one name whatever delivers it. It must
// be a valid Helm release name (validHelmReleaseName); one that is not (Flux's
// shortening can cut a dotted name just after a '.', leaving a label that
// starts with '-') is refused with the remedy to set releaseName. So is an
// empty component name, which only a config built directly can have: there is
// nothing to derive a default from. The error is prefixed with owner, the
// component type.
func defaultHelmReleaseName(owner, componentName string) (string, error) {
	if componentName == "" {
		return "", errors.Errorf("%s: no release name: releaseName is unset and the component has no name to derive it from; set releaseName or the component name", owner)
	}
	name := oam.ShortenName(componentName, oam.ShortenLimitHelmRelease)
	if !validHelmReleaseName(name) {
		return "", errors.Errorf("%s: the default release name %q, derived from the component name %q as Flux derives a HelmRelease's, %s; set releaseName", owner, name, componentName, helmReleaseNameRule)
	}
	return name, nil
}

// chartURL is the location handed to the renderer: a HelmRepository's base URL
// joined with the chart name, or an OCIRepository's URL unchanged.
func (s chartSource) chartURL() string {
	if s.Kind == "OCIRepository" {
		return s.URL // OCI URL already embeds the chart path
	}
	return strings.TrimRight(s.URL, "/") + "/" + s.Chart
}

// inlineChartSourceKind resolves an inline chart source's kind and checks it
// against the URL's scheme, shared by the helm rule and the helmtemplate
// terminal so both enforce one set. An empty kind is
// inferred from the scheme: oci:// is OCIRepository, anything else
// HelmRepository. A HelmRepository needs an http:// or https:// URL and a chart
// name; an OCIRepository needs an oci:// URL. A URL carrying a user or password
// is refused (refuseURLUserinfo), with the client-side render's remedy; the
// helm rule's flux delivery checks it first, with its own. Every error is
// prefixed with owner, the component type.
func inlineChartSourceKind(owner, url, kind, chart string) (string, error) {
	if err := refuseURLUserinfo(owner, url, templateUserinfoRemedy); err != nil {
		return "", err
	}
	if kind == "" {
		if strings.HasPrefix(url, "oci://") {
			kind = "OCIRepository"
		} else {
			kind = "HelmRepository"
		}
	}

	switch kind {
	case "HelmRepository":
		if strings.HasPrefix(url, "oci://") {
			return "", errors.Errorf("%s: source.kind HelmRepository is incompatible with oci:// URL", owner)
		}
		if !strings.HasPrefix(url, "https://") && !strings.HasPrefix(url, "http://") {
			return "", errors.Errorf("%s: HelmRepository source.url must start with https:// or http://", owner)
		}
		if chart == "" {
			return "", errors.Errorf("%s: source.kind HelmRepository requires chart to be specified", owner)
		}
	case "OCIRepository":
		if !strings.HasPrefix(url, "oci://") {
			return "", errors.Errorf("%s: source.kind OCIRepository requires an oci:// URL", owner)
		}
	default:
		return "", errors.Errorf("%s: source.kind %q is not valid for inline source; must be HelmRepository or OCIRepository", owner, kind)
	}
	return kind, nil
}

// templateUserinfoRemedy completes refuseURLUserinfo's message for a chart
// rendered client-side, whose render options carry no credentials.
const templateUserinfoRemedy = "; a client-side render takes no credentials"

// refuseURLUserinfo refuses an inline chart source URL that carries a user or
// password (https://user:token@host, oci://user@registry/chart): every path
// copies the URL verbatim, into a generated source or the rendered component,
// so the credential would land in plain text in the build output. remedy
// completes the message. No message names the URL, and an unparsable one is
// refused without url.Parse's error, whose text repeats the whole URL.
func refuseURLUserinfo(owner, rawURL, remedy string) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return errors.Errorf("%s: source.url is not a valid URL", owner)
	}
	if u.User != nil {
		return errors.Errorf("%s: source.url must not carry a user or password%s", owner, remedy)
	}
	return nil
}

// chartRender is a chart's client-side render, cached and split by Helm hook
// phase and weight. HelmTemplateConfig embeds it and declares its own
// AugmentLayout; chartRender must never gain one, since kure's layout walker
// type-asserts layout.LayoutAugmenter by presence and the method would be
// promoted onto every embedder, augmenter or not.
type chartRender struct {
	// hookGroups caches the rendered chart's manifests, split by Helm hook phase
	// and weight (via helm.SplitByHookWeight). Populated by render on first
	// call; nil until then. Not goroutine-safe — concurrent Generate/AugmentLayout
	// calls on the same config race on rendered/hookGroups (an identical cache in
	// a downstream consumer's analogous handler isn't goroutine-safe either).
	//
	// These objects are the render as ApplyPolicy checked it and are never
	// handed out: objects returns copies.
	hookGroups []helm.HookGroup
	// rendered reports whether render has already populated hookGroups, so that
	// ApplyPolicy, Generate and AugmentLayout (the transform's and kure's layout
	// walker's call order) render the chart over the network exactly once.
	rendered bool
	// emitted is the copy of hookGroups the last objects call handed out, group
	// by group; nil until then. partition builds the child layouts from it, so
	// they hold the very objects Generate returned, with whatever a trait
	// decorator did to them.
	emitted []helm.HookGroup
}

// render renders src client-side via renderFn on first call (a nil renderFn
// means helm.RenderChart), parses the result into Helm-hook-partitioned groups
// via parseChartManifests, and caches them in hookGroups. Subsequent calls are
// no-ops — rendered guards re-render — so a config used through both Generate
// (which flattens hookGroups into its returned union, objects) and
// AugmentLayout (partition, which repartitions the same groups into child
// layouts) — kure's layout walker's usual call order — renders the chart over
// the network exactly once. A render failure is reported as
// `<componentType> "<name>": rendering chart`. A failure to parse the rendered
// output (parseChartManifests) is returned as is, with no component type or
// name, under either caller.
//
// The render uses src's release identity (chartSource.renderOptions), which
// each caller decides. A namespaced object the chart rendered without
// metadata.namespace is then given src.Namespace (stampRenderedNamespaces), so
// the cached groups — what ApplyPolicy checks and what objects and partition
// hand out — already carry it. A scope override the rendered objects contradict
// is refused there, as `<componentType> "<name>": object <kind> "<name>": …`,
// and nothing is cached.
//
// With src.SecretValues set, the chart is rendered with them merged over
// src.Values, and neither render failure above is reported as it came: a
// template error can quote the value it failed on, and a parse error the
// rendered text around it. secretRenderFailure reports what can be said
// without them. The scope-override refusal is reported as it is either way: it
// names the object's kind and name and the two scopes, never a value.
func (r *chartRender) render(renderFn renderChartFunc, componentType, name string, src chartSource) error {
	if r.rendered {
		return nil
	}
	if renderFn == nil {
		renderFn = helm.RenderChart
	}
	groups, err := renderChartGroups(renderFn, componentType, name, src, mergeSecretValues(src.Values, src.SecretValues))
	if err != nil {
		if len(src.SecretValues) > 0 {
			return secretRenderFailure(renderFn, componentType, name, src)
		}
		return err
	}
	if err := stampRenderedNamespaces(src.Namespace, src.ScopeOverrides, groups); err != nil {
		return errors.Wrapf(err, "%s %q", componentType, name)
	}
	r.hookGroups = groups
	r.rendered = true
	return nil
}

// renderChartGroups renders src with values and parses the result into hook
// groups, with render's two errors.
func renderChartGroups(renderFn renderChartFunc, componentType, name string, src chartSource, values map[string]any) ([]helm.HookGroup, error) {
	raw, err := renderFn(src.chartURL(), src.Version, values, src.renderOptions()...)
	if err != nil {
		return nil, errors.Wrapf(err, "%s %q: rendering chart", componentType, name)
	}
	return parseChartManifests(raw)
}

// secretRenderFailure reports a render with secretValues that failed, without
// the failure's own text. It renders src once more with src.Values alone, a
// render no sensitive value is part of, so whatever that render reports can be
// shown: a chart that cannot be fetched, or a template that fails either way,
// is then still named. A chart that requires a value set only in secretValues
// fails this second render for that reason, which is why the message says the
// reported cause is the one without them. When the second render succeeds, the
// cause lies in secretValues and is withheld.
func secretRenderFailure(renderFn renderChartFunc, componentType, name string, src chartSource) error {
	const withheld = "the cause is withheld because it can repeat a sensitive value"
	_, err := renderChartGroups(renderFn, componentType, name, src, src.Values)
	if err == nil {
		return errors.Errorf("%s %q: rendering chart with %s failed, and without them it renders; %s", componentType, name, helmSecretValuesKey, withheld)
	}
	return errors.Wrapf(err, "%s %q: rendering chart with %s failed; %s. Without them it fails with", componentType, name, helmSecretValuesKey, withheld)
}

// stampRenderedNamespaces sets namespace on every emitted object that is
// namespaced and carries no metadata.namespace, which is where a Helm install
// into that namespace would create it. It resolves each object's scope as the
// manifests component does (resolveObjectScope): the scope overrides states
// for the object's apiVersion and kind, else kure's scope table, plus the
// scope a CustomResourceDefinition among the emitted objects declares for the
// kind it defines. A chart's crds/ directory is not rendered, so a CRD shipped
// there defines no scope here.
//
// Unlike the manifests component, it refuses nothing about what the chart
// wrote, since a chart is not authored by the application:
//
//   - a namespace the chart wrote is kept, on a cluster-scoped object too, as
//     Helm keeps it, and also when an override calls the kind cluster-scoped
//     (the manifests component refuses that object);
//   - an object whose scope is unknown — a kind kure does not register, custom
//     or built-in, with no CRD for it among the emitted objects and no
//     override — is left as rendered, namespace-less: stamping it would be a
//     guess, and a cluster-scoped object must not carry a namespace;
//   - a cluster-scoped object is left as rendered.
//
// The one thing it refuses is the document's own statement: an override that
// disagrees with the CustomResourceDefinition among the emitted objects for
// the same kind, which no cluster can honour. The error names the object's
// kind and name and both scopes, and nothing else of the object.
//
// An empty namespace (a config built directly, with none) stamps nothing; a
// contradicted override is refused all the same.
func stampRenderedNamespaces(namespace string, overrides map[schema.GroupVersionKind]manifest.ScopeResult, groups []helm.HookGroup) error {
	if namespace == "" && len(overrides) == 0 {
		return nil
	}
	crdScopes := map[schema.GroupKind]apiextv1.ResourceScope{}
	for _, g := range groups {
		for _, o := range g.Resources {
			if gk, scope, ok := manifest.CRDScope(o); ok {
				crdScopes[gk] = scope
			}
		}
	}
	for _, g := range groups {
		for _, o := range g.Resources {
			scope, declared, conflict := resolveObjectScope(o, overrides, crdScopes)
			if conflict {
				gvk := o.GetObjectKind().GroupVersionKind()
				return errors.Errorf("object %s %q: scopeOverrides says %s but the CustomResourceDefinition for %s the chart renders declares %s; that CRD defines the scope the cluster will serve, so drop the override", gvk.Kind, o.GetName(), scopeName(scope), gvk.GroupKind().String(), declared)
			}
			if namespace != "" && o.GetNamespace() == "" && scope == manifest.ScopeNamespaced {
				o.SetNamespace(namespace)
			}
		}
	}
	return nil
}

// objects flattens a deep copy of hookGroups in execution order, and records
// that copy in emitted. This is the union partition later repartitions into
// child layouts for a layout-walking consumer — Generate itself always returns
// the flat set, which is what keeps kurel build (which never walks a
// layout.ManifestLayout) and every validator unaffected, and is the premise
// GenerateCoversAugmentLayout's guard opt-out rests on.
//
// Each call copies afresh, as the manifests component copies what it decoded:
// the objects are typed, so a trait decorator that edits a workload in place
// (a mounted ConfigMap, topology spread constraints) edits the copy, and a
// second Generate starts again from the render instead of meeting the first
// call's edits.
func (r *chartRender) objects() []*client.Object {
	r.emitted = make([]helm.HookGroup, len(r.hookGroups))
	var objects []*client.Object
	for i, g := range r.hookGroups {
		r.emitted[i] = helm.HookGroup{Phase: g.Phase, Weight: g.Weight, Resources: make([]client.Object, len(g.Resources))}
		for j, obj := range g.Resources {
			o, _ := obj.DeepCopyObject().(client.Object)
			r.emitted[i].Resources[j] = o
			objects = append(objects, &o)
		}
	}
	return objects
}

// partition is AugmentLayout's work once the chart is rendered. With at most
// one hook group, ml.Resources already carries the flat union Generate
// returned and no children are needed. With multiple groups, that union is
// partitioned: ml.Resources is cleared and rebuilt solely from r.emitted (the
// copy of the render the last Generate flattened and returned; a fresh copy
// when Generate never ran), so this relies on ml.Resources containing exactly
// those objects when AugmentLayout runs — true today
// because every trait decorator in this repo (traits/decorator.go's
// decoratorBase-embedding types) only mutates the objects its inner Generate
// returns in place and never appends a new one; a trait's own additional
// resources (e.g. a ConfigMap or Secret) are emitted as a separate
// stack.Application and never merged into this Application's ml.Resources. A
// future decorator that broke this convention
// would have its addition silently dropped here. Each group becomes a child
// ManifestLayout written to a numbered sub-directory in execution order,
// chained via DependsOn so kure's FluxCD integrator (in FluxIntegratedPerLayout
// placement) waits for each hook group to reconcile healthy before the next.
//
// Each child's Namespace is the parent's path (ml.FullRepoPath()): kure's
// FullRepoPath() joins Namespace and Name verbatim, so the child's directory
// is "<parent>/<dirName>". A Namespace that already ended in the
// child's own name would nest it twice (go-kure/kure#771).
//
// Children inherit the parent's Mode/FluxPlacement/FileNaming/FilePer — but
// deliberately NOT ApplicationFileMode, left AppFileUnset on every child
// regardless of the parent's own value. kure's walker sets only three of the
// five layout-rule fields on the layout it hands the augmenter; a downstream
// consumer's identical augmenter copies all five verbatim, carrying the risk
// this deviation avoids (fixing that other copy is out of this repo's
// scope). kure reads a child's own literal ApplicationFileMode first, and an
// AppFileSingle child is no directory: it writes one "<dirName>.yaml" into
// its Namespace — the parent's own directory — which the parent's
// kustomization.yaml then lists directly, and under FluxIntegratedPerLayout
// kure's integrator gives such a child no Kustomization CR of its own, so
// its DependsOn — the hook-group ordering — would be silently lost. Left
// AppFileUnset, a child under FluxIntegratedPerLayout is pinned to a
// directory by that integrator and gets its own Kustomization CR (spec.path
// its FullRepoPath(), spec.dependsOn from DependsOn); under any other
// placement the writer applies its own Config default to the child, exactly
// as to a walked application layout.
//
// The partitioned layout itself, when its own ApplicationFileMode is
// AppFileUnset (always, for a layout kure's walker built), is pinned to
// AppFilePerResource: it must stay a directory whose kustomization.yaml lists
// the hook-group children. Left unset, kure's WriteManifest would give it the
// writer's Config.ApplicationFileMode under every placement except
// FluxIntegratedPerLayout, and under an AppFileSingle default a layout with
// children is refused ("is AppFileSingle and has child layouts"). With the
// pin, each child under such a default is written as one "<dirName>.yaml" in
// this layout's directory and listed there. Everywhere else the pin changes
// nothing: under FluxIntegratedPerLayout WriteManifest already resolves the
// layout as a directory and kure's integrator pins an unset mode to
// AppFilePerResource itself; WriteToDisk, WriteToTar and kure's Flux
// generator compare the literal mode with AppFileSingle only, so
// AppFilePerResource and AppFileUnset read the same there; and the walker's
// single-tier flattening never absorbs a layout that has children. A caller
// that sets the layout's mode explicitly keeps it.
//
// A child is named after application and ml (hookGroupChildName). Component
// names are unique only within one application, while the Kustomization CRs a
// consumer generates for the children of every application can share one
// namespace, so the application name is what keeps two differently named
// applications with a same-named component apart (go-kure/launcher#792). An
// empty application — a config built directly — leaves the names beginning
// with ml.Name.
func (r *chartRender) partition(application string, ml *layout.ManifestLayout) {
	if len(r.hookGroups) <= 1 {
		return
	}
	if r.emitted == nil {
		r.objects()
	}
	ml.Resources = nil
	if ml.ApplicationFileMode == layout.AppFileUnset {
		ml.ApplicationFileMode = layout.AppFilePerResource
	}
	parentPath := ml.FullRepoPath()
	var prevName string
	for i, g := range r.emitted {
		dirName := hookGroupChildName(application, ml.Name, i, g)
		child := &layout.ManifestLayout{
			Name:          dirName,
			Namespace:     parentPath,
			Resources:     append([]client.Object(nil), g.Resources...),
			Mode:          ml.Mode,
			FluxPlacement: ml.FluxPlacement,
			FileNaming:    ml.FileNaming,
			FilePer:       ml.FilePer,
			// ApplicationFileMode intentionally omitted (left AppFileUnset) — see the doc comment above.
		}
		if i > 0 {
			child.DependsOn = []string{prevName}
		}
		ml.Children = append(ml.Children, child)
		prevName = dirName
	}
}

// parseChartManifests decodes multi-doc YAML produced by renderChart and
// splits it into Helm hook-phase-and-weight groups via kure's
// helm.SplitByHookWeight.
//
// The decode is kure's parser with unstructured objects allowed
// (decodeChartManifests): an object of a kind kure's scheme registers is its
// Go type, which is what lets ApplyPolicy read a rendered workload's pod spec,
// and any other object is unstructured.
//
// kure's SplitByHookWeight documents (pkg/stack/helm/hooks.go:35-36) that a
// comma-separated helm.sh/hook annotation (e.g. "pre-install,pre-upgrade") is
// treated as one opaque phase string and sorted into the alphabetical
// "unknown" bucket, which its own phaseOrder (hooks.go:60-75) places *after*
// post-upgrade — inverting the ordering guarantee for exactly the kind of
// resource that annotation exists to order. Likewise a multi-value annotation
// whose tokens are all members of kure's excludedHookPhases (hooks.go:20-26,
// exact-string matched) is never excluded either, for the same reason. Both
// are corrected here, before objects reach SplitByHookWeight, via a grouping-
// only object copy (normalizeHookAnnotationForGrouping) — the original
// objects, with their original unmodified annotations, are what land in
// HookGroup.Resources and therefore in emitted output.
func parseChartManifests(raw []byte) ([]helm.HookGroup, error) {
	objs, err := decodeChartManifests(raw)
	if err != nil {
		return nil, err
	}

	groupingObjs := make([]client.Object, len(objs))
	origByGroupingObj := make(map[client.Object]client.Object, len(objs))
	for i, obj := range objs {
		normalized := normalizeHookAnnotationForGrouping(obj)
		groupingObjs[i] = normalized
		if normalized != obj {
			origByGroupingObj[normalized] = obj
		}
	}

	groups := helm.SplitByHookWeight(groupingObjs)
	for gi := range groups {
		for ri, r := range groups[gi].Resources {
			if orig, ok := origByGroupingObj[r]; ok {
				groups[gi].Resources[ri] = orig
			}
		}
	}
	return groups, nil
}

// hookPhaseOrder mirrors kure's own phaseOrder priority for the four ordered
// Helm lifecycle phases (kure pkg/stack/helm/hooks.go:60-75) that participate
// in FluxCD Kustomization ordering. "" (main/non-hook) is deliberately
// excluded — a comma-separated annotation is by definition non-empty.
var hookPhaseOrder = map[string]int{
	"pre-install":  0,
	"pre-upgrade":  1,
	"post-install": 2,
	"post-upgrade": 3,
}

// excludedHookPhases mirrors kure's own unexported excludedHookPhases set
// (kure pkg/stack/helm/hooks.go:20-26) — phases with no FluxCD GitOps
// lifecycle equivalent, which SplitByHookWeight drops from its output. Kept
// as a local copy since kure's map is unexported and this package must not
// edit the pinned dependency.
var excludedHookPhases = map[string]bool{
	"pre-delete":    true,
	"post-delete":   true,
	"pre-rollback":  true,
	"post-rollback": true,
	"test":          true,
}

// normalizeHookAnnotationForGrouping returns a client.Object suitable for
// handing to kure's helm.SplitByHookWeight for grouping-key determination.
// Single-value and empty helm.sh/hook annotations are already correct under
// kure's own logic and are returned unchanged (same pointer as obj — callers
// use pointer identity to detect whether a copy was made).
//
// For a comma-separated annotation, every excluded-phase token
// (excludedHookPhases) is first dropped from consideration — an excluded
// token must never influence the grouping decision for an object that is
// otherwise emitted, exactly as it never would if it were the object's only
// hook value. What remains after dropping excluded tokens is then
// classified:
//   - nothing remains (every token was excluded) -> rewritten to a single
//     excluded literal ("test") so kure's own exclusion logic (hooks.go:49)
//     drops the object, instead of it falling into the mis-sorted unknown
//     bucket.
//   - at least one surviving token is a recognized ordered phase
//     (hookPhaseOrder) -> rewritten to the earliest such token by kure's own
//     priority; any unrecognized tokens mixed in are dropped too (they
//     contribute no valid ordering).
//   - every surviving token is an unrecognized custom hook name, and at
//     least one excluded token was dropped to reach that state -> rewritten
//     to just the surviving tokens (comma-joined, original relative order)
//     so the dropped excluded token cannot affect the unknown-bucket
//     grouping key or hookGroupDir's slug.
//   - every original token was already an unrecognized custom hook name (no
//     excluded token was ever present) -> left unchanged verbatim. There is
//     no defined ordering priority among custom names, and kure's existing
//     unknown-bucket fallback is not wrong for this case — rewriting would
//     only reorder or reformat it for no behavioral reason.
//   - a degenerate annotation with no non-empty token at all (e.g. "," or
//     " , ") -> left unchanged verbatim, same as the previous case. "no
//     non-empty token was ever excluded" is a different condition from
//     "every non-empty token was excluded" — only the latter drops the
//     object; kure's own (pre-normalization) handling of such a literal
//     string is to treat it as one opaque unknown-phase string, not to
//     exclude or drop it, and this function must not change that.
func normalizeHookAnnotationForGrouping(obj client.Object) client.Object {
	ann := obj.GetAnnotations()
	hook := ann["helm.sh/hook"]
	if hook == "" || !strings.Contains(hook, ",") {
		return obj
	}

	sawToken := false
	anyExcluded := false
	var remaining []string
	bestPhase := ""
	bestOrder := -1
	for _, tok := range strings.Split(hook, ",") {
		tok = strings.TrimSpace(tok)
		if tok == "" {
			continue
		}
		sawToken = true
		if excludedHookPhases[tok] {
			anyExcluded = true
			continue
		}
		remaining = append(remaining, tok)
		if order, ok := hookPhaseOrder[tok]; ok && (bestOrder == -1 || order < bestOrder) {
			bestOrder = order
			bestPhase = tok
		}
	}

	switch {
	case sawToken && len(remaining) == 0:
		return cloneWithHookAnnotation(obj, ann, "test")
	case bestOrder != -1:
		return cloneWithHookAnnotation(obj, ann, bestPhase)
	case anyExcluded:
		return cloneWithHookAnnotation(obj, ann, strings.Join(remaining, ","))
	default:
		return obj
	}
}

// cloneWithHookAnnotation returns a copy of obj with its helm.sh/hook
// annotation rewritten to newHook, leaving obj itself — and its original
// annotations map — untouched. The copy exists only to steer kure's
// helm.SplitByHookWeight to the correct group; parseChartManifests swaps it
// back out for the original object before returning, so the rewritten
// annotation never reaches emitted output. The deep copy is safe on every
// object decodeChartManifests returns: a typed object copies itself, and an
// unstructured one was decoded from JSON, so its content is JSON-typed.
func cloneWithHookAnnotation(obj client.Object, ann map[string]string, newHook string) client.Object {
	cp, _ := obj.DeepCopyObject().(client.Object)
	newAnn := make(map[string]string, len(ann))
	maps.Copy(newAnn, ann)
	newAnn["helm.sh/hook"] = newHook
	cp.SetAnnotations(newAnn)
	return cp
}

// hookGroupDir returns a DNS-1123-safe directory-name segment for a
// HookGroup: Phase == "" (main / non-hook resources) maps to "main"; any
// other phase is slugified — lowercased, runs of characters outside
// [a-z0-9-] collapsed to a single "-", leading/trailing "-" trimmed,
// truncated to 40 characters and re-trimmed (the cut can expose a new
// trailing "-"), "unknown" if the result is empty.
//
// Deviates from a downstream consumer's analogous helper, which returns the
// phase verbatim: kure's
// SplitByHookWeight puts a comma-separated or otherwise malformed
// helm.sh/hook annotation into one opaque phase string (kure hooks.go) that
// becomes both a path segment and a literal Kustomization object name via
// kure's createKustomizationForLayout, which validates neither
// (pkg/stack/fluxcd/resource_generator.go). Unsanitized, a phase like
// "pre-install,post-install" breaks path safety and DNS-1123 validity, and
// Helm imposes no length limit on the annotation. Fixed here rather than
// inherited — see partition's doc comment for the write-time hazard this
// avoids.
func hookGroupDir(g helm.HookGroup) string {
	if g.Phase == "" {
		return "main"
	}
	var b strings.Builder
	prevDash := false
	for _, r := range strings.ToLower(g.Phase) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			prevDash = false
			continue
		}
		if !prevDash {
			b.WriteByte('-')
			prevDash = true
		}
	}
	slug := strings.Trim(b.String(), "-")
	const maxSlugLen = 40
	if len(slug) > maxSlugLen {
		slug = strings.TrimRight(slug[:maxSlugLen], "-")
	}
	if slug == "" {
		return "unknown"
	}
	return slug
}

// decodeChartManifests decodes multi-doc YAML from RenderChart into Kubernetes
// objects with kure's parser, unstructured objects allowed — the decode the
// manifests component gives a fetched document (decodeManifestDocuments). An
// object whose group, version and kind kure's scheme registers is its Go type
// (*appsv1.Deployment, *batchv1.Job); any other is *unstructured.Unstructured.
// A list is replaced by its items: those of a `v1` List each decoded as a
// document of its own (a nested list flattened in turn, to the depth kure's
// parser bounds), those of a typed list (DeploymentList) as the kind the list
// holds, and those of a list kind the scheme does not register one level deep,
// unstructured. An empty or comment-only document is skipped (kure filters
// NOTES.txt upstream).
//
// Every document that does not decode is an error, and the parser reports them
// together: invalid YAML; a document that is not a mapping; one without
// apiVersion or kind; a field of a registered kind whose value has the wrong
// type; and a list item that does not decode, named by its position.
//
// A field the vendored API type of a registered kind does not declare is not
// dropped, as the parser alone would drop it: a workload or a claim that sets
// one is an error naming the object and the field, and an object of any other
// registered kind comes back unstructured, as rendered, the field kept. An
// item of a `v1` List or of a typed list is held to that as a document of its
// own is.
//
// A list where a helm.sh/hook annotation is involved is refused before the
// parse (refuseHookInList): the parser reads only a list's items, so the hook
// Helm reads on the list would be lost.
func decodeChartManifests(raw []byte) ([]client.Object, error) {
	if err := refuseHookInList(raw); err != nil {
		return nil, errors.Wrap(err, "decoding rendered manifests")
	}
	objs, err := decodeManifestDocuments(raw)
	if err != nil {
		return nil, errors.Wrap(err, "decoding rendered manifests")
	}
	return objs, nil
}

// helmHookAnnotation is the annotation Helm reads a hook from.
const helmHookAnnotation = "helm.sh/hook"

// genericListGVK is the `v1` List, the one list whose items are documents of
// any kind.
var genericListGVK = schema.GroupVersionKind{Version: "v1", Kind: "List"}

// refuseHookInList returns an error for the first list document of raw where a
// helm.sh/hook annotation is involved: on the list's own metadata, or on one
// of its items.
//
// Helm reads a hook from the metadata of the rendered document, whatever its
// kind, and from nowhere else. kure's parser replaces a list by its items and
// reads only those. So a list that carries the annotation is a hook to Helm,
// and its items would be emitted as ordinary resources; an item that carries
// it inside a list that is no hook is an ordinary resource to Helm, and would
// be grouped as a hook here (or dropped, in an excluded phase). Neither can be
// rendered as Helm installs it.
//
// The documents are split as the parser splits them, and each is read as the
// JSON the parser decodes, value by value as written (jsonMembers): the check
// must not skip, or read differently, a document the parser accepts. One the
// parser will report (invalid YAML, not a mapping) is left to it, and ends the
// check: the build fails on it.
func refuseHookInList(raw []byte) error {
	if err := kubernetes.RegisterSchemes(); err != nil {
		return errors.Wrap(err, "registering kure's scheme")
	}
	decoder := yamlutil.NewYAMLOrJSONDecoder(bytes.NewReader(raw), 4096)
	var doc runtime.RawExtension
	for decoder.Decode(&doc) == nil {
		if err := hookInList(doc.Raw, 0); err != nil {
			return err
		}
		doc = runtime.RawExtension{}
	}
	return nil
}

// maxCheckedListNesting is the deepest a `v1` List may sit inside `v1` Lists
// for hookInList to follow its items: the depth kure's parser flattens a
// registered list to (its maxListNesting). A registered list nested deeper is
// a build error there, so nothing below it is emitted. The bound also keeps
// the check's cost linear in the document: each level reads what it holds
// again.
const maxCheckedListNesting = 8

// hookInList is refuseHookInList's check of one document, nesting `v1` Lists
// deep. The items of a `v1` List are documents of their own, so a list among
// them is checked in turn; the parser flattens no other list inside a list.
// The list itself and its items are checked at any depth: the parser's bound
// is on registered lists only, and a list of an unregistered kind below it is
// still flattened.
func hookInList(list json.RawMessage, nesting int) error {
	items, generic, ok := flattenedItems(list)
	if !ok {
		return nil
	}
	if hasHelmHook(list) {
		return errors.Errorf("list %s carries a %s annotation on its own metadata: Helm reads the hook there, and the items that replace the list do not carry it",
			docRef(list), helmHookAnnotation)
	}
	for i, item := range items {
		if hasHelmHook(item) {
			return errors.Errorf("item %d (%s) of list %s carries a %s annotation: Helm reads a hook only on the rendered document's own metadata, so it installs the item as an ordinary resource",
				i, docRef(item), docRef(list), helmHookAnnotation)
		}
		if !generic || nesting > maxCheckedListNesting {
			continue
		}
		if err := hookInList(item, nesting+1); err != nil {
			return errors.Errorf("item %d of list %s: %w", i, docRef(list), err)
		}
	}
	return nil
}

// flattenedItems reports whether kure's parser may replace doc by its items,
// and returns the items it would read. The parser does so for a list kind
// kure's scheme registers (the `v1` List, for which generic is true, or a typed
// list), and for a kind the scheme does not register when doc states items.
//
// The parser does not read doc's type in one way. Its list detection reads
// apiVersion and kind under exactly those keys; the decoder it falls back to
// reads them whatever their case (Kind, APIVERSION), and takes the last one
// stated. So a document may be a single object to the first and a list of an
// unregistered kind to the second. flattenedItems does not follow either: it
// holds doc for a list when any pairing of an apiVersion and a kind doc
// states, under any case of the key, is one the parser flattens. The result is
// every document the parser flattens, and beside those only documents that
// state their type more than once.
//
// An apiVersion left out is one more pairing for the list detection, whose
// exact read sees none where the key differs in case. To the fallback decoder
// it is the apiVersion only of a document that states none at all.
//
// items is read under that exact key, and in the last statement of it, as both
// of the parser's readers do: an earlier statement is in no object the parser
// returns. A last statement that is null states no items, to the parser too;
// one that is neither null nor an array is the parser's error.
func flattenedItems(doc json.RawMessage) (items []json.RawMessage, generic, ok bool) {
	stated := jsonExactMembers(doc, "items")
	if len(stated) > 0 {
		_ = json.Unmarshal(stated[len(stated)-1], &items)
	}
	apiVersions := jsonStrings(doc, "apiVersion")
	for _, kind := range jsonStrings(doc, "kind") {
		if kind == "" {
			continue
		}
		for i, apiVersion := range append(apiVersions[:len(apiVersions):len(apiVersions)], "") {
			gv, err := schema.ParseGroupVersion(apiVersion)
			if err != nil {
				continue
			}
			gvk := gv.WithKind(kind)
			obj, err := kubernetes.Scheme.New(gvk)
			if err != nil {
				leftOut := i == len(apiVersions)
				if len(stated) > 0 && (!leftOut || len(apiVersions) == 0) {
					ok = true
				}
				continue
			}
			if _, single := obj.(client.Object); single || !meta.IsListType(obj) {
				continue
			}
			ok = true
			generic = generic || gvk == genericListGVK
		}
	}
	if !ok {
		return nil, false, false
	}
	return items, generic, true
}

// hasHelmHook reports whether doc's own metadata carries the helm.sh/hook
// annotation, with any value. Every metadata and every annotations doc states
// is read, under any case of those two keys: a JSON document may state a key
// more than once, the typed decode merges the statements and the unstructured
// one keeps the last, and Helm reads both keys whatever their case. A hook in
// any of them is one Helm or the decoded object may carry. The annotation's
// own key is a map key, which every reader matches exactly.
func hasHelmHook(doc json.RawMessage) bool {
	for _, metadata := range jsonMembers(doc, "metadata") {
		for _, annotations := range jsonMembers(metadata, "annotations") {
			if len(jsonExactMembers(annotations, helmHookAnnotation)) > 0 {
				return true
			}
		}
	}
	return false
}

// docRef names doc in an error: its kind and its quoted name, as far as it
// states them.
func docRef(doc json.RawMessage) string {
	name := ""
	if metadata := jsonMembers(doc, "metadata"); len(metadata) > 0 {
		name = jsonString(metadata[len(metadata)-1], "name")
	}
	return strings.TrimSpace(fmt.Sprintf("%s %q", jsonString(doc, "kind"), name))
}

// jsonMembers returns every value the JSON object raw states under key, in the
// order it states them, and nil when raw is not an object. Each value is
// returned as written, undecoded, so one no Go type holds (a number beyond
// float64) does not fail the read, and a key stated twice yields both values.
// The key is matched whatever its case, as a decode into a Go struct matches
// it (Helm's read of a document's head, the Kubernetes decoder's read of
// apiVersion and kind): Metadata and KIND are members too.
func jsonMembers(raw json.RawMessage, key string) []json.RawMessage {
	return jsonMembersMatching(raw, func(name string) bool { return strings.EqualFold(name, key) })
}

// jsonExactMembers is jsonMembers for a key that is matched exactly: a map key
// (an annotation), or a field every reader matches by its exact name (items).
func jsonExactMembers(raw json.RawMessage, key string) []json.RawMessage {
	return jsonMembersMatching(raw, func(name string) bool { return name == key })
}

func jsonMembersMatching(raw json.RawMessage, match func(name string) bool) []json.RawMessage {
	dec := json.NewDecoder(bytes.NewReader(raw))
	if open, err := dec.Token(); err != nil || open != json.Delim('{') {
		return nil
	}
	var values []json.RawMessage
	for dec.More() {
		name, err := dec.Token()
		if err != nil {
			return values
		}
		var value json.RawMessage
		if dec.Decode(&value) != nil {
			return values
		}
		if key, isString := name.(string); isString && match(key) {
			values = append(values, value)
		}
	}
	return values
}

// jsonStrings returns every string the JSON object raw states under key,
// whatever the key's case, in the order it states them. A value that is not a
// string is left out.
func jsonStrings(raw json.RawMessage, key string) []string {
	var values []string
	for _, value := range jsonMembers(raw, key) {
		var s string
		if json.Unmarshal(value, &s) == nil {
			values = append(values, s)
		}
	}
	return values
}

// jsonString returns the last of jsonStrings, "" when there is none.
func jsonString(raw json.RawMessage, key string) string {
	values := jsonStrings(raw, key)
	if len(values) == 0 {
		return ""
	}
	return values[len(values)-1]
}

// hookGroupChildName computes partition's dirName for hook group i:
// "<application>-<ml.Name>-<%02d>-<hookGroupDir(g)>", or
// "<ml.Name>-<%02d>-<hookGroupDir(g)>" when application is empty (a config
// built directly, which no transform told its application). application and
// ml.Name are each a validated DNS-1123 subdomain up to 253 characters
// (pkg/oam/validate.go,
// k8s.io/apimachinery/pkg/util/validation.DNS1123SubdomainMaxLength), so the
// composed name can exceed 253 even with hookGroupDir's own 40-character slug
// cap — additional to that slug-only cap. Truncating the composed string from
// the right is wrong: near the limit, the fixed numeric+phase suffix would be
// cut away entirely and every group would yield the identical dirName — a
// deterministic collision. So the PREFIX (application and ml.Name, joined) is
// capped instead, by the one shortening rule (oam.ShortenNameWithSuffix): the
// digest of the full prefix takes the place of what is cut, so two different
// long prefixes are vanishingly unlikely to shorten to the same name (a
// probabilistic guarantee, not an absolute one).
//
// The join is a plain "-", which both names may contain: application "a-b"
// with component "c" and application "a" with component "b-c" compose the
// same prefix. Two applications with a same-named component and different
// names do not. Two applications with one name do, whatever their namespaces:
// the prefix, like the bundle name, carries no namespace.
func hookGroupChildName(application, mlName string, i int, g helm.HookGroup) string {
	prefix := mlName
	if application != "" {
		prefix = application + "-" + mlName
	}
	suffix := fmt.Sprintf("-%02d-%s", i, hookGroupDir(g)) // %02d is a minimum width, not a cap
	return oam.ShortenNameWithSuffix(prefix, suffix, oam.ShortenLimitSubdomain)
}
