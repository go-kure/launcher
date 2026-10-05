package components

import (
	"encoding/json"
	"maps"
	"strings"

	"github.com/fluxcd/pkg/apis/meta"

	"github.com/go-kure/launcher/pkg/errors"
	"github.com/go-kure/launcher/pkg/oam"
)

// digestPrefix marks an OCI reference as a digest rather than a tag.
const digestPrefix = "sha256:"

// ociType is the oci component type. It prefixes every error the rule raises
// itself.
const ociType = "oci"

// OCIRule lowers an "oci" component (D1 component position,
// oam.ComponentLoweringRule) to the two kind-named Flux terminals it stands
// for (go-kure/launcher#784): an `ocirepository`, the source of the artifact,
// and a `fluxcd-kustomization` that reconciles it.
//
// A component whose source no other oci component of the document shares
// lowers to a same-name sibling group: the ocirepository member and the
// fluxcd-kustomization member, both named after the component and deployed as
// one unit, OCIRepository first. That is the pair of objects the former
// OCIHandler generated.
//
// When two or more oci components of a document have the same source, the
// source belongs to the application rather than to whichever component comes
// first: it is emitted once, named <document>-source-<digest> (or what the
// Naming hook returns for it, role oam.NameRoleHelmSource, asked once for the
// document) and claimed as the helm rule's generated source is
// (generatedSourceName), and each component lowers to its
// fluxcd-kustomization alone, referencing it
// and ordered after it (Component.OrderAfter). That order makes it a generated
// source, which pkg/oam keeps out of the ordered groups and applies with the
// application bundle itself, so every consumer follows it wherever it is placed.
// The source identity is the url, the version and the effective interval
// (ociSourceIdentity): components share only when all three are equal, so a
// component with its own interval keeps its own source. The identity differs
// from the helm rule's, so an oci and a helm component on one artifact never
// share a source; the helm one copies the chart layer instead of extracting
// it. Sharing is decided among the oci components the document holds in the
// round the rule runs in.
//
// source.name names the source (go-kure/launcher#787), and makes it the shared
// form whatever the number of consumers: emitted once under that name, with
// neither annotations nor traits, and each component that writes the name for
// the same identity lowering to its fluxcd-kustomization alone. The name is the
// component's own choice: a component that writes none does not share the
// source of one that does, and is not counted among its consumers (a name
// equal to the shared unnamed source's own names that source: one name for one
// identity is one source). The source a
// component keeps to itself is named after the component, and neither
// source.name's role nor the hook reaches it.
//
// LowerComponent first runs the oci parse (parseOCI, the former
// OCIHandler.ToApplicationConfig sequence), so every input the handler refused
// is still refused, with the same cause text. Everything past the parse is the
// terminals' own: the allowed-registries policy is the ocirepository's
// (OCIRepositoryConfig.ApplyPolicy), and both kinds treat a zero interval as
// unset.
//
// Annotations go to both members of a group, so a tier override places the
// group as one; the fluxcd-kustomization alone carries them in the shared
// case. `prune-protection` and `force-replace` cover every object a
// component generates, so both members carry them; every other authored trait
// goes to the fluxcd-kustomization. A shared source carries neither
// annotations nor traits.
type OCIRule struct{}

// ComponentType claims the "oci" component type at the component lowering
// position. build.go registers this rule via RegisterComponentLowering; no
// dispatchable handler exists for "oci".
func (OCIRule) ComponentType() string { return ociType }

// PropertySchema declares the oci component's user-facing properties.
func (OCIRule) PropertySchema() map[string]oam.PropertySchema {
	return map[string]oam.PropertySchema{
		"source": {
			Type:        oam.PropertyTypeObject,
			Required:    true,
			Description: "OCIRepository source of the artifact to reconcile.",
			Properties: map[string]oam.PropertySchema{
				"url":  {Type: oam.PropertyTypeString, Required: true, Description: "OCI artifact URL (must use the oci:// scheme)."},
				"name": {Type: oam.PropertyTypeString, Description: "Name of the generated OCIRepository, in place of the component name (or of <document>-source-<digest> for a source several components share), used as written. The source is then generated on its own, apart from the Kustomization: it carries none of the component's annotations and traits. It must differ from every component name of the document; components that write the same name share the source only when their url, version and interval are the same."},
			},
		},
		"version":         {Type: oam.PropertyTypeString, Required: true, Description: "Artifact version to reconcile: a tag or sha256:<digest>."},
		"path":            {Type: oam.PropertyTypeString, Default: "./", Description: "Path within the artifact that the Kustomization reconciles."},
		"prune":           {Type: oam.PropertyTypeBoolean, Default: true, Description: "Whether the Kustomization prunes resources removed from the source."},
		"interval":        {Type: oam.PropertyTypeString, Description: "Reconciliation interval as a Flux duration: unsigned, units ms, s, m, h, e.g. 10m or 1h30m; 0s or at least 1ms (default 60m)."},
		"targetNamespace": {Type: oam.PropertyTypeString, Description: "Set the Kustomization's spec.targetNamespace, which sets or overrides the namespace of every namespaced object in the artifact, Flux custom resources included. No default: unset, each object keeps the namespace the artifact's own kustomize build gives it. Author it when namespaced objects are still without a namespace after that build (the artifact sets none, in the objects or in a kustomization.yaml namespace), since they otherwise fail at apply with \"namespace not specified\". Unlike helmrelease under a Flux namespace, this does not default to the application namespace, because a default would override every object's own namespace."},
		"wait":            {Type: oam.PropertyTypeBoolean, Description: "Set the Kustomization's spec.wait: Flux waits for every resource it applies to become ready before reporting the Kustomization ready. Unset or false emits nothing. Cannot be combined with a non-empty healthChecks, which kustomize-controller ignores when wait is true."},
		"healthChecks": {
			Type:        oam.PropertyTypeArray,
			Description: "Objects listed, in authored order, in the Kustomization's spec.healthChecks: Flux reports the Kustomization ready only once these are ready. The component delivers an opaque artifact, so the list is authored, never derived. An empty list emits nothing. Cannot be combined with wait: true.",
			Items: &oam.PropertySchema{
				Type:        oam.PropertyTypeObject,
				Description: "One object Flux checks for readiness.",
				Properties: map[string]oam.PropertySchema{
					"apiVersion": {Type: oam.PropertyTypeString, Required: true, Description: "API version of the object (e.g. apps/v1, or v1 for a core kind)."},
					"kind":       {Type: oam.PropertyTypeString, Required: true, Description: "Kind of the object (e.g. Deployment)."},
					"name":       {Type: oam.PropertyTypeString, Required: true, Description: "Name of the object."},
					"namespace":  {Type: oam.PropertyTypeString, Description: "Namespace of the object; omit for a cluster-scoped kind."},
				},
			},
		},
	}
}

// ociHealthCheckKeys is the accepted key set of one `healthChecks` entry, the
// fields of Flux's NamespacedObjectKindReference. Any other key is refused.
var ociHealthCheckKeys = []string{"apiVersion", "kind", "name", "namespace"}

// ociProperties is an oci component's properties as parseOCI read them.
type ociProperties struct {
	url     string // oci:// artifact URL
	version string // tag, or sha256:<digest>
	// sourceName is the authored source.name, "" for none: absent, null or an
	// empty string, as the helm component reads its own.
	sourceName string

	path            string
	prune           bool
	interval        string // as authored; "" when unset
	targetNamespace string

	// wait sets the Kustomization's spec.wait; false emits nothing.
	wait bool
	// healthChecks become the Kustomization's spec.healthChecks, in order.
	// Never non-empty while wait is true: kustomize-controller ignores
	// healthChecks when wait is true, so parseOCI refuses the pair.
	healthChecks []meta.NamespacedObjectKindReference
}

// parseOCI reads an oci component's properties.
//
//	source:
//	  url: oci://registry.example.com/org/artifact   # required, oci:// scheme
//	  name: artifact-source                           # optional; names the OCIRepository
//	version: 1.2.3                                    # required; tag, or sha256:<digest>
//	path: ./                                          # optional, default "./"
//	prune: true                                       # optional, default true
//	interval: 60m                                     # optional, default 60m
//	targetNamespace: my-workload                      # optional
//	wait: false                                       # optional, no default; true sets spec.wait
//	healthChecks:                                     # optional, not with wait: true; sets spec.healthChecks, in order
//	  - apiVersion: apps/v1                           # required
//	    kind: Deployment                              # required
//	    name: my-workload                             # required
//	    namespace: my-workload                        # optional; omit for a cluster-scoped kind
//
// wait and healthChecks are opt-in (go-kure/launcher#432): a document in which
// neither requests anything — wait absent or false, and healthChecks absent,
// null or empty — builds the same Kustomization it always did. wait: true
// emits spec.wait even beside an empty healthChecks, and a non-empty
// healthChecks emits its checks even beside wait: false. wait: true together
// with a non-empty healthChecks is refused, because kustomize-controller
// ignores healthChecks when wait is true.
func parseOCI(props map[string]any) (*ociProperties, error) {
	out := &ociProperties{path: "./", prune: true}

	src, ok := props["source"].(map[string]any)
	if !ok {
		return nil, errors.New("oci: source is required")
	}
	// A wrongly typed url or version is named as a type error, not reported
	// missing (go-kure/launcher#453).
	srcURL, present, err := parseStringField(src, "url", "oci: source.url")
	if err != nil {
		return nil, err
	}
	if !present {
		return nil, errors.New("oci: source.url is required")
	}
	out.url = srcURL
	if !strings.HasPrefix(out.url, "oci://") {
		// Not quoted: the url can carry a credential (userinfo, a query).
		return nil, errors.New("oci: source.url must use the oci:// scheme")
	}
	if out.sourceName, _, err = parseStringField(src, "name", "oci: source.name"); err != nil {
		return nil, err
	}

	version, present, err := parseStringField(props, "version", "oci: version")
	if err != nil {
		return nil, err
	}
	if !present {
		return nil, errors.New("oci: version is required (a tag, or sha256:<digest>)")
	}
	out.version = version

	if p, present, err := parseStringField(props, "path", "path"); err != nil {
		return nil, err
	} else if present {
		out.path = p
	}
	if pr, err := parseBoolField(props, "prune", "prune"); err != nil {
		return nil, err
	} else if pr != nil {
		out.prune = *pr
	}
	interval, _, err := parseStringField(props, "interval", "interval")
	if err != nil {
		return nil, err
	}
	out.interval = interval
	if out.interval != "" {
		if err := validateFluxInterval(ociType, out.interval); err != nil {
			return nil, err
		}
	}
	targetNamespace, _, err := parseStringField(props, "targetNamespace", "targetNamespace")
	if err != nil {
		return nil, err
	}
	out.targetNamespace = targetNamespace

	if w, err := parseBoolField(props, "wait", "wait"); err != nil {
		return nil, err
	} else if w != nil {
		out.wait = *w
	}
	healthChecks, err := parseOCIHealthChecks(props)
	if err != nil {
		return nil, err
	}
	out.healthChecks = healthChecks
	if out.wait && len(out.healthChecks) > 0 {
		return nil, errors.New("oci: wait: true and healthChecks are mutually exclusive: kustomize-controller ignores healthChecks when wait is true, so the listed checks would never run; drop wait to check only the listed objects, or drop healthChecks to wait for everything applied")
	}

	return out, nil
}

// parseOCIHealthChecks reads the optional `healthChecks` list. Absent, null or
// empty yields nil. Each entry must be an object with only the keys in
// ociHealthCheckKeys; apiVersion (e.g. apps/v1, or v1 for a core kind), kind
// and name are required non-empty strings, and namespace is an optional
// string, left out for a cluster-scoped kind.
func parseOCIHealthChecks(props map[string]any) ([]meta.NamespacedObjectKindReference, error) {
	entries, _, err := parseObjectList(props, "healthChecks")
	if err != nil {
		return nil, err
	}
	if len(entries) == 0 {
		return nil, nil
	}
	refs := make([]meta.NamespacedObjectKindReference, 0, len(entries))
	for i, m := range entries {
		label := indexedLabel("healthChecks", i)
		if err := rejectUnknownKeys(m, ociHealthCheckKeys, label); err != nil {
			return nil, err
		}
		apiVersion, err := requiredStringField(m, "apiVersion", label)
		if err != nil {
			return nil, err
		}
		kind, err := requiredStringField(m, "kind", label)
		if err != nil {
			return nil, err
		}
		name, err := requiredStringField(m, "name", label)
		if err != nil {
			return nil, err
		}
		namespace, _, err := parseStringField(m, "namespace", label+".namespace")
		if err != nil {
			return nil, err
		}
		refs = append(refs, meta.NamespacedObjectKindReference{
			APIVersion: apiVersion,
			Kind:       kind,
			Name:       name,
			Namespace:  namespace,
		})
	}
	return refs, nil
}

// LowerComponent validates comp as an oci component and emits its
// ocirepository and fluxcd-kustomization components (see OCIRule).
func (OCIRule) LowerComponent(comp *oam.Component, lctx oam.LoweringContext) (oam.LoweringResult, error) {
	props, err := parseOCI(comp.Properties)
	if err != nil {
		return oam.LoweringResult{}, err
	}
	identity, err := props.sourceIdentity()
	if err != nil {
		return oam.LoweringResult{}, err
	}

	kustomization := oam.Component{
		Name:        comp.Name,
		Type:        fluxcdKustomizationType,
		Traits:      comp.Traits,
		Annotations: maps.Clone(comp.Annotations),
	}
	source := oam.Component{Type: "ocirepository", Properties: props.sourceProperties()}

	if props.sourceName == "" && ociSourceConsumers(lctx.Document, identity) < 2 {
		// The component's own source: a same-name sibling group, the source
		// first, as the former handler generated the two objects.
		source.Name = comp.Name
		source.Annotations = maps.Clone(comp.Annotations)
		for _, t := range comp.Traits {
			if roleObjectTraits[t.Type] {
				source.Traits = append(source.Traits, t)
			}
		}
		kustomization.Properties = props.kustomizationProperties(source.Name)
		return oam.LoweringResult{Components: []oam.Component{source, kustomization}}, nil
	}

	// The source on its own: the one the author named, or the one the document's
	// unnamed consumers of this identity share.
	name, adopted, err := generatedSourceName(lctx, ociType, comp, "OCIRepository", identity, props.sourceName)
	if err != nil {
		return oam.LoweringResult{}, err
	}
	var result oam.LoweringResult
	if !adopted {
		source.Name = name
		result.Components = append(result.Components, source)
	}
	kustomization.Properties = props.kustomizationProperties(name)
	// Such a source is the application's (see OCIRule): ordered after it,
	// whether this component generated or adopted it, the Kustomization makes
	// it a generated source, held by the application bundle ahead of every group.
	kustomization.OrderAfter(name)
	result.Components = append(result.Components, kustomization)
	return result, nil
}

// sourceIdentity is the content identity of the component's source: every
// input that shapes the OCIRepository, namely the url, the version and the
// effective interval, as JSON so that no separator inside a url can make two
// identities collide. The interval is the parsed duration, so 60m, 1h and
// unset (the default, and zero, which the terminals read as unset) are one
// identity. The "oci-artifact:" prefix keeps it apart from the helm rule's
// identity for an OCI chart (helmSourceIdentity).
func (p *ociProperties) sourceIdentity() (string, error) {
	interval := parseDuration(effectiveInterval(p.interval))
	defaultFluxSourceInterval(&interval)
	b, err := json.Marshal(struct {
		URL      string `json:"url"`
		Version  string `json:"version"`
		Interval string `json:"interval"`
	}{p.url, p.version, interval.Duration.String()})
	if err != nil {
		return "", errors.Wrapf(err, "%s: encoding the source identity", ociType)
	}
	return "oci-artifact:" + string(b), nil
}

// ociSourceConsumers counts the oci components of doc that name no source of
// their own (source.name) and whose source has the
// given identity, the component being lowered included. A component that
// names its source keeps it apart, so it is no consumer of the shared one. A
// component that does
// not parse counts for nothing: its own lowering refuses it. A nil doc (a rule
// driven directly, outside the engine) has only the component at hand.
func ociSourceConsumers(doc *oam.Application, identity string) int {
	if doc == nil {
		return 1
	}
	n := 0
	for i := range doc.Spec.Components {
		c := &doc.Spec.Components[i]
		if c.Type != ociType {
			continue
		}
		props, err := parseOCI(c.Properties)
		if err != nil || props.sourceName != "" {
			continue
		}
		if other, err := props.sourceIdentity(); err == nil && other == identity {
			n++
		}
	}
	return n
}

// sourceProperties are the ocirepository component's properties: the url, the
// version as ref.tag, or ref.digest for a sha256: value, and the authored
// interval; unset, the terminal applies its own 60m default.
func (p *ociProperties) sourceProperties() map[string]any {
	ref := map[string]any{"tag": p.version}
	if strings.HasPrefix(p.version, digestPrefix) {
		ref = map[string]any{"digest": p.version}
	}
	out := map[string]any{"url": p.url, "ref": ref}
	if p.interval != "" {
		out["interval"] = p.interval
	}
	return out
}

// kustomizationProperties are the fluxcd-kustomization component's properties,
// its sourceRef naming the OCIRepository source.
func (p *ociProperties) kustomizationProperties(source string) map[string]any {
	out := map[string]any{
		"path":      p.path,
		"prune":     p.prune,
		"sourceRef": map[string]any{"kind": "OCIRepository", "name": source},
	}
	if p.interval != "" {
		out["interval"] = p.interval
	}
	// No default, unlike helmrelease: a Kustomization targetNamespace
	// overrides every object's namespace (go-kure/launcher#622).
	if p.targetNamespace != "" {
		out["targetNamespace"] = p.targetNamespace
	}
	if p.wait {
		out["wait"] = true
	}
	if len(p.healthChecks) > 0 {
		checks := make([]any, 0, len(p.healthChecks))
		for _, ref := range p.healthChecks {
			check := map[string]any{"apiVersion": ref.APIVersion, "kind": ref.Kind, "name": ref.Name}
			if ref.Namespace != "" {
				check["namespace"] = ref.Namespace
			}
			checks = append(checks, check)
		}
		out["healthChecks"] = checks
	}
	return out
}
