package components

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/go-kure/kure/pkg/stack/helm"
	"github.com/go-kure/kure/pkg/stack/layout"
	"gopkg.in/yaml.v3"
	chartutil "helm.sh/helm/v4/pkg/chart/v2/util"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-kure/launcher/pkg/errors"
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
// values tree handed to the render as-is, the namespace the render uses as
// .Release.Namespace (empty leaves kure's default, "default"), and the release
// name it uses as .Release.Name, which templateReleaseName has already resolved
// and checked (empty leaves kure's default, "release").
type chartSource struct {
	URL         string
	Kind        string // "HelmRepository" or "OCIRepository"
	Chart       string
	Version     string
	Values      map[string]any
	Namespace   string
	ReleaseName string
}

// renderOptions turns the release name and the namespace into kure render
// options, each when set. The namespace only sets .Release.Namespace: kure
// stamps no metadata.namespace, so a chart that omits it still renders
// namespace-less objects.
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
// releaseName when set, else the release name Flux gives a HelmRelease named
// componentName with no spec.releaseName and no spec.targetNamespace — the
// name itself, shortened as Flux shortens it (fluxShortenReleaseName) — so a
// chart renders under the same name under either delivery of the helm rule.
// An authored name is never shortened; it, and the default, must be a valid
// Helm release name (validHelmReleaseName). A default that is not (Flux's
// shortening can leave a label starting with '-') is refused with the remedy to
// set releaseName. Every error is prefixed with owner, the component type.
func templateReleaseName(owner, releaseName, componentName string) (string, error) {
	if releaseName != "" {
		if !validHelmReleaseName(releaseName) {
			return "", errors.Errorf("%s: releaseName %q %s", owner, releaseName, helmReleaseNameRule)
		}
		return releaseName, nil
	}
	if componentName == "" {
		return "", errors.Errorf("%s: no release name: releaseName is unset and the component has no name to derive it from; set ReleaseName or Name", owner)
	}
	name := fluxShortenReleaseName(componentName)
	if !validHelmReleaseName(name) {
		return "", errors.Errorf("%s: the default release name %q, derived from the component name %q as Flux derives a HelmRelease's, %s; set releaseName", owner, name, componentName, helmReleaseNameRule)
	}
	return name, nil
}

// fluxReleaseNameMaxLen and fluxReleaseNameHashLen are the constants of Flux
// helm-controller's release-name shortening (fluxShortenReleaseName).
const (
	fluxReleaseNameMaxLen  = 53
	fluxReleaseNameHashLen = 12
)

// fluxShortenReleaseName mirrors Flux helm-controller's release.ShortenName,
// which helm-controller applies to HelmRelease.GetReleaseName() before an
// install or upgrade, and which is internal to helm-controller, so it cannot be
// imported: a name of at most 53 characters is kept; a longer one is cut to its
// first 40 characters, followed by '-' and the first 12 hex digits of the
// SHA-256 of the whole name — 53 characters in all.
func fluxShortenReleaseName(name string) string {
	if len(name) <= fluxReleaseNameMaxLen {
		return name
	}
	sum := sha256.Sum256([]byte(name))
	return name[:fluxReleaseNameMaxLen-(fluxReleaseNameHashLen+1)] + "-" + hex.EncodeToString(sum[:])[:fluxReleaseNameHashLen]
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
	hookGroups []helm.HookGroup
	// rendered reports whether render has already populated hookGroups, so that
	// Generate followed by AugmentLayout (kure's layout walker's usual call
	// order) renders the chart over the network exactly once.
	rendered bool
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
// each caller decides. Nothing stamps metadata.namespace afterwards, so a chart
// that leaves it unset renders namespace-less objects whatever the namespace.
func (r *chartRender) render(renderFn renderChartFunc, componentType, name string, src chartSource) error {
	if r.rendered {
		return nil
	}
	if renderFn == nil {
		renderFn = helm.RenderChart
	}
	raw, err := renderFn(src.chartURL(), src.Version, src.Values, src.renderOptions()...)
	if err != nil {
		return errors.Wrapf(err, "%s %q: rendering chart", componentType, name)
	}
	groups, err := parseChartManifests(raw)
	if err != nil {
		return err
	}
	r.hookGroups = groups
	r.rendered = true
	return nil
}

// objects flattens hookGroups in execution order. This is the union partition
// later repartitions into child layouts for a layout-walking consumer —
// Generate itself always returns the flat set, which is what keeps kurel build
// (which never walks a layout.ManifestLayout) and every validator unaffected,
// and is the premise GenerateCoversAugmentLayout's guard opt-out rests on.
func (r *chartRender) objects() []*client.Object {
	var objects []*client.Object
	for _, g := range r.hookGroups {
		for _, obj := range g.Resources {
			o := obj
			objects = append(objects, &o)
		}
	}
	return objects
}

// partition is AugmentLayout's work once the chart is rendered. With at most
// one hook group, ml.Resources already carries the flat union Generate
// returned and no children are needed. With multiple groups, that union is
// partitioned: ml.Resources is cleared and rebuilt solely from r.hookGroups
// (the same cached render Generate flattened), so this relies on ml.Resources
// containing exactly that render's objects when AugmentLayout runs — true today
// because every trait decorator in this repo (traits/decorator.go's
// decoratorBase-embedding types) only mutates the objects its inner Generate
// returns in place and never appends a new one; a trait's own additional
// resources (e.g. a ConfigMap or Secret) are emitted as a separate
// stack.Application and never merged into this Application's ml.Resources (see
// traits/pruneprotection.go's "narrow scope" doc comment for the same
// convention stated explicitly). A future decorator that broke this convention
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
// Residual gap, documented not fixed: two DIFFERENT Applications with a
// same-named component still collide (component names are unique only
// within one Application, but every emitted Kustomization CR shares one
// controller namespace) — a downstream consumer's identical augmenter
// admits the same gap; inherited here, newly exposed by this repo's own
// template-delivery support. Out of scope; see the components README.
func (r *chartRender) partition(ml *layout.ManifestLayout) {
	if len(r.hookGroups) <= 1 {
		return
	}
	ml.Resources = nil
	if ml.ApplicationFileMode == layout.AppFileUnset {
		ml.ApplicationFileMode = layout.AppFilePerResource
	}
	parentPath := ml.FullRepoPath()
	var prevName string
	for i, g := range r.hookGroups {
		dirName := hookGroupChildName(ml.Name, i, g)
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
	objs, err := decodeKubeManifests(raw)
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

// hookDropsObject reports whether hook grouping drops, unwritten, an object
// whose helm.sh/hook annotation is hook: kure's SplitByHookWeight drops one
// whose whole annotation is an excludedHookPhases member (an exact match), and
// normalizeHookAnnotationForGrouping has it drop one whose comma-separated
// annotation has at least one non-empty token and nothing but excluded ones.
func hookDropsObject(hook string) bool {
	if !strings.Contains(hook, ",") {
		return excludedHookPhases[hook]
	}
	sawToken := false
	for _, tok := range strings.Split(hook, ",") {
		tok = strings.TrimSpace(tok)
		if tok == "" {
			continue
		}
		if !excludedHookPhases[tok] {
			return false
		}
		sawToken = true
	}
	return sawToken
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
// annotation never reaches emitted output. The deep copy relies on obj's
// content being JSON-typed, which decodeKubeManifests guarantees.
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

// decodeKubeManifests decodes multi-doc YAML from RenderChart into Kubernetes objects.
// Real YAML parse errors are returned immediately.
// A document that is not a mapping (a scalar, a sequence, or nil, as a
// comment-only document decodes) and an empty mapping are skipped defensively
// (kure filters NOTES.txt upstream).
// Mapping documents without apiVersion/kind are an error (broken chart manifest).
// Each object's content is converted in place to JSON types (toJSONTypes), so
// the objects are safe to deep-copy. A document with a key or value that
// cannot be emitted is an error — including a key that is not a string at
// its own top level, for which yaml.v3 decodes the whole document to
// map[any]any — unless hook grouping would drop it unwritten
// (hookDropsObject): then it is skipped, since nothing it holds is emitted.
func decodeKubeManifests(raw []byte) ([]client.Object, error) {
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	var objects []client.Object
	for {
		var rawDoc any
		if err := dec.Decode(&rawDoc); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return nil, errors.Wrapf(err, "decoding rendered manifest")
		}
		var doc map[string]any
		var convErr error
		switch d := rawDoc.(type) {
		case map[string]any:
			if len(d) == 0 {
				continue // defensive: skip an empty document
			}
			doc = d
		case map[any]any:
			// Never empty: yaml.v3 decodes an empty mapping to map[string]any.
			// The document cannot be emitted, but its string-keyed entries are
			// read as any document's are, for the checks below and the error.
			doc = stringKeyedEntries(d)
			_, convErr = toJSONTypes(d, "")
		default:
			continue // defensive: skip a scalar, a sequence or a nil document
		}
		if doc["apiVersion"] == nil || doc["kind"] == nil {
			return nil, errors.Errorf("rendered document is missing apiVersion or kind: %v", rawDoc)
		}
		u := &unstructured.Unstructured{Object: doc}
		// Read before converting: a failed conversion stops at the first value
		// it cannot convert and leaves the document partly converted, in map
		// iteration order.
		hook := hookAnnotation(doc)
		if convErr == nil {
			_, convErr = toJSONTypes(doc, "")
		}
		if convErr != nil {
			if hookDropsObject(hook) {
				continue
			}
			return nil, errors.Wrapf(convErr, "rendered %s %q", u.GetKind(), u.GetName())
		}
		objects = append(objects, u)
	}
	return objects, nil
}

// hookAnnotation returns the helm.sh/hook annotation of doc, a decoded
// document, or "" when it has none. Unlike unstructured's GetAnnotations, it
// reads metadata and metadata.annotations as either map[string]any or the
// map[any]any yaml.v3 decodes a mapping with a non-string key to, so such a
// key cannot hide the hook that decides whether the document is dropped.
func hookAnnotation(doc map[string]any) string {
	hook, _ := stringKeyedValue(stringKeyedValue(doc["metadata"], "annotations"), "helm.sh/hook").(string)
	return hook
}

// stringKeyedValue returns the value under key in m, a map[string]any or a
// map[any]any, or nil when m is neither or has no such key.
func stringKeyedValue(m any, key string) any {
	switch t := m.(type) {
	case map[string]any:
		return t[key]
	case map[any]any:
		return t[key]
	default:
		return nil
	}
}

// stringKeyedEntries returns the entries of m, a document yaml.v3 decoded to
// map[any]any, whose key is a string: the object's apiVersion, kind and
// metadata, read through unstructured's accessors exactly as for a document
// with string keys only. The values are shared with m, not copied.
func stringKeyedEntries(m map[any]any) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		if s, ok := k.(string); ok {
			out[s] = v
		}
	}
	return out
}

// toJSONTypes converts v, a value yaml.v3 decoded into any, to the types an
// unstructured.Unstructured holds: those runtime.DeepCopyJSONValue accepts,
// which panics on anything else ("cannot deep copy int"). Unstructured's
// DeepCopy copies through it, so every copy of an object does, including the
// grouping copy cloneWithHookAnnotation makes. Maps and slices are converted
// in place. Each conversion encodes to the same JSON as the value it replaces,
// and kure writes an object from its JSON encoding, so the written manifest is
// unchanged:
//   - int, yaml.v3's type for an integer that fits in an int64, becomes int64;
//   - uint64, its type for an integer above math.MaxInt64 up to
//     math.MaxUint64, becomes a json.Number of the same digits (int64 cannot
//     hold it);
//   - time.Time, its type for an unquoted timestamp, becomes the RFC 3339
//     string encoding/json writes for it (time.Time.MarshalJSON's);
//   - float64 (also its type for an integer beyond math.MaxUint64, and for
//     .inf and .nan, which encoding/json refuses to write, as before),
//     string, bool and nil are kept.
//
// A mapping with a key that is not a string (map[any]any) is an error naming
// its path — "top level" for the document itself, path "" — since
// encoding/json cannot write one either. So is a time.Time that
// MarshalJSON refuses because RFC 3339 cannot express it (a year outside
// [0,9999], or a UTC offset of 24 hours or more, which the time.Parse behind
// yaml.v3's timestamps accepts), and any other type.
func toJSONTypes(v any, path string) (any, error) {
	switch t := v.(type) {
	case map[string]any:
		for k, e := range t {
			c, err := toJSONTypes(e, path+"."+k)
			if err != nil {
				return nil, err
			}
			t[k] = c
		}
		return t, nil
	case []any:
		for i, e := range t {
			c, err := toJSONTypes(e, fmt.Sprintf("%s[%d]", path, i))
			if err != nil {
				return nil, err
			}
			t[i] = c
		}
		return t, nil
	case int:
		return int64(t), nil
	case uint64:
		return json.Number(strconv.FormatUint(t, 10)), nil
	case time.Time:
		// The string MarshalJSON writes, quotes removed: RFC 3339 text holds no
		// character JSON escapes. Its refusal is what kure's writer met before.
		b, err := t.MarshalJSON()
		if err != nil {
			return nil, errors.Wrapf(err, "%s: a timestamp outside RFC 3339 cannot be emitted", path)
		}
		return string(b[1 : len(b)-1]), nil
	case nil, bool, string, int64, float64:
		return t, nil
	case map[any]any:
		if path == "" {
			path = "top level"
		}
		return nil, errors.Errorf("%s: a mapping key that is not a string cannot be emitted", path)
	default:
		return nil, errors.Errorf("%s: a value of type %T cannot be emitted", path, v)
	}
}

// hookGroupChildName computes partition's dirName for hook group
// i: "<ml.Name>-<%02d>-<hookGroupDir(g)>". ml.Name is a validated DNS-1123
// subdomain up to 253 characters (pkg/oam/validate.go,
// k8s.io/apimachinery/pkg/util/validation.DNS1123SubdomainMaxLength), so the
// composed name can exceed 253 even with hookGroupDir's own 40-character slug
// cap — additional to that slug-only cap. Truncating the composed string from
// the right is wrong: near a 253-char ml.Name, the fixed numeric+phase suffix
// would be cut away entirely and every group would yield the identical
// dirName — a deterministic collision. So the PREFIX (ml.Name) is capped
// instead, following the same truncation approach as helmrelease.go's
// boundedResourceName: reserve room for a short sha256 hash of the
// full ml.Name so two different long ml.Names are vanishingly unlikely to
// truncate to the same prefix (the same probabilistic guarantee as that
// approach, not an absolute one).
func hookGroupChildName(mlName string, i int, g helm.HookGroup) string {
	suffix := fmt.Sprintf("-%02d-%s", i, hookGroupDir(g)) // %02d is a minimum width, not a cap
	const maxLen = 253
	if len(mlName)+len(suffix) <= maxLen {
		return mlName + suffix
	}
	maxPrefix := maxLen - len(suffix)
	const hashLen = 8
	sum := sha256.Sum256([]byte(mlName))
	hash := hex.EncodeToString(sum[:])[:hashLen]
	prefixLen := max(maxPrefix-hashLen-1, 0) // -1 for the "-" joining prefix and hash
	prefix := strings.TrimRight(mlName[:prefixLen], "-.")
	return prefix + "-" + hash + suffix
}
