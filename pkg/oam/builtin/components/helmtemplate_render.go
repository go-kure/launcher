package components

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"maps"
	"strings"

	"github.com/go-kure/kure/pkg/stack/helm"
	"github.com/go-kure/kure/pkg/stack/layout"
	"gopkg.in/yaml.v3"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-kure/launcher/pkg/errors"
)

// This file is the one implementation of client-side Helm chart rendering and
// of the Helm hook-group layout partition. Two configs run it: HelmTemplateConfig
// (the kind-named helmtemplate terminal) and HelmchartConfig under delivery:
// template (the helmchart composite). Each embeds chartRender, which renders once
// and caches the hook groups, and hands it its own renderer, its own chart
// source and the component type its errors are prefixed with. Nothing below
// depends on which of the two it serves.

// renderChartFunc is the chart renderer: kure's helm.RenderChart, or a stub
// injected by a test. Variadic opts matches kure's RenderChart signature (kure
// v0.2.0-beta.10+, helm.RenderOption) so that helm.RenderChart itself
// satisfies it without a wrapper; chartRender.render does not pass any opts
// yet (see its doc comment).
type renderChartFunc = func(chartURL, version string, values map[string]any, opts ...helm.RenderOption) ([]byte, error)

// chartSource is what one client-side render fetches: an inline chart source
// whose Kind inlineChartSourceKind has already resolved and checked against
// URL's scheme, the chart name within a HelmRepository, the chart version, and
// the values tree handed to the render as-is.
type chartSource struct {
	URL     string
	Kind    string // "HelmRepository" or "OCIRepository"
	Chart   string
	Version string
	Values  map[string]any
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
// against the URL's scheme — the helmchart composite's Form A rules, shared
// with the helmtemplate terminal so both enforce one set. An empty kind is
// inferred from the scheme: oci:// is OCIRepository, anything else
// HelmRepository. A HelmRepository needs an http:// or https:// URL and a chart
// name; an OCIRepository needs an oci:// URL. Every error is prefixed with
// owner, the component type.
func inlineChartSourceKind(owner, url, kind, chart string) (string, error) {
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

// chartRender is a chart's client-side render, cached and split by Helm hook
// phase and weight. HelmTemplateConfig and HelmchartConfig embed it; it must
// never gain an AugmentLayout method of its own, since kure's layout walker
// type-asserts layout.LayoutAugmenter by presence and the method would be
// promoted onto every *HelmchartConfig — see wrapIfHelmchartAugmenter.
type chartRender struct {
	// hookGroups caches the rendered chart's manifests, split by Helm hook phase
	// and weight (via helm.SplitByHookWeight). Populated by render on first
	// call; nil until then (and always nil for the composite's delivery: native,
	// which never renders). Not goroutine-safe — concurrent Generate/AugmentLayout
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
// `<componentType> "<name>": rendering chart`.
//
// Known limitation: this call passes no release-identity opts, so kure renders with
// its defaults, .Release.Name = "release" and .Release.Namespace = "default" (kure
// pkg/stack/helm/render.go). Neither caller lets a document set either: the helmchart
// composite rejects releaseName/targetNamespace outright for delivery: template, and
// the helmtemplate terminal does not declare them, so its strict decode refuses them —
// the rejection, not a silent drop, is today's behavior for a chart that needs
// .Release.Name/.Release.Namespace. kure now exposes helm.WithReleaseName/
// helm.WithNamespace (kure v0.2.0-beta.10+); wiring them through and relaxing those
// rejections is a follow-up, not attempted here.
func (r *chartRender) render(renderFn renderChartFunc, componentType, name string, src chartSource) error {
	if r.rendered {
		return nil
	}
	if renderFn == nil {
		renderFn = helm.RenderChart
	}
	raw, err := renderFn(src.chartURL(), src.Version, src.Values)
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
// annotation never reaches emitted output.
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
// Non-map and empty documents are skipped defensively (kure filters NOTES.txt upstream).
// Mapping documents without apiVersion/kind are an error (broken chart manifest).
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
		doc, ok := rawDoc.(map[string]any)
		if !ok || len(doc) == 0 {
			continue // defensive: skip non-map or empty documents
		}
		if doc["apiVersion"] == nil || doc["kind"] == nil {
			return nil, errors.Errorf("rendered document is missing apiVersion or kind: %v", doc)
		}
		objects = append(objects, &unstructured.Unstructured{Object: doc})
	}
	return objects, nil
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
// instead, following the same truncation approach as the helmchart
// composite's valuesConfigMapName: reserve room for a short sha256 hash of the
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
