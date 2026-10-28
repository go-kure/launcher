package oam

import (
	"fmt"
	"slices"
	"strings"

	helmv2 "github.com/fluxcd/helm-controller/api/v2"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// fluxSourceGroup is the API group of Flux's source kinds, helmReleaseKind the
// kind (in helmGroup) that makes a claim, and helmChartKind and
// ociRepositoryKind the kinds of the source helm-controller generates for a
// HelmRelease's chart template of that kind.
const (
	fluxSourceGroup   = "source.toolkit.fluxcd.io"
	helmReleaseKind   = "HelmRelease"
	helmChartKind     = "HelmChart"
	ociRepositoryKind = "OCIRepository"
)

// controllerClaim is an object a controller generates in the cluster for a
// generated object: no application emits it, yet it is applied under its name
// (go-kure/launcher#1072, go-kure/launcher#1074). helm-controller
// server-side-applies the HelmChart or OCIRepository of a HelmRelease's chart
// template with force ownership, and deletes whatever holds that name when the
// HelmRelease is deleted, when the kind, name or namespace of the chart source
// it generates changes, or when it switches to chartRef, without asking who
// owns it (helm-controller v1.7.1 internal/reconcile/helmchart_template.go:78-99,
// 142-152,183-210). So an object of that identity that an application
// generates is taken over, then deleted: the collision checks hold a claim as
// a generated object.
type controllerClaim struct {
	id objectIdentity
	// by is the object the controller generates it for.
	by objectIdentity
}

func (c controllerClaim) String() string {
	return fmt.Sprintf("helm-controller for %s (chart.kind %s)", c.by, c.id.kind)
}

// controllerClaims returns the claims obj makes, read as keyedObjects keys it:
// for a HelmRelease with a chart template, the source helm-controller generates
// from it (GeneratedChartSource).
func controllerClaims(obj client.Object) ([]controllerClaim, error) {
	src, ok, err := GeneratedChartSource(obj)
	if err != nil || !ok {
		return nil, err
	}
	gvk := obj.GetObjectKind().GroupVersionKind()
	return []controllerClaim{{
		id: objectIdentity{group: fluxSourceGroup, kind: src.Kind, namespace: src.Namespace, name: src.Name},
		by: objectIdentity{group: gvk.Group, kind: gvk.Kind, namespace: obj.GetNamespace(), name: obj.GetName()},
	}}, nil
}

// ChartSource is the source object, in source.toolkit.fluxcd.io, that
// helm-controller generates from a HelmRelease's chart template (spec.chart).
type ChartSource struct {
	// Kind is the template's kind: HelmChart, its default, or OCIRepository.
	Kind string
	// Namespace is chart.spec.sourceRef.namespace for a HelmChart when that is
	// set, else the HelmRelease's.
	Namespace string
	// Name is `<namespace>-<name>` after the HelmRelease's own namespace and
	// name, whatever namespace the source is in.
	Name string
}

// GeneratedChartSource returns the source helm-controller generates for obj, a
// HelmRelease with a chart template (HelmRelease.GetHelmChartTemplateReference,
// helm-controller api v1.7.1 v2/helmrelease_types.go:1633-1649). ok is false
// for an object that is no HelmRelease, and for a chartRef, a chart that is no
// object, or a kind the API does not take: none generates a source, and the
// HelmRelease's own admission refuses the last two.
func GeneratedChartSource(obj client.Object) (src ChartSource, ok bool, err error) {
	gvk := obj.GetObjectKind().GroupVersionKind()
	if gvk.Group != helmGroup || gvk.Kind != helmReleaseKind {
		return ChartSource{}, false, nil
	}
	chart, ok, err := chartTemplateContent(obj)
	if err != nil || !ok {
		return ChartSource{}, false, err
	}
	ns := obj.GetNamespace()
	src = ChartSource{Namespace: ns, Name: ns + "-" + obj.GetName()}
	src.Kind, _ = chart["kind"].(string)
	switch src.Kind {
	case "", helmChartKind:
		src.Kind = helmChartKind
		chartSpec, _ := chart["spec"].(map[string]any)
		sourceRef, _ := chartSpec["sourceRef"].(map[string]any)
		if sourceNS, _ := sourceRef["namespace"].(string); sourceNS != "" {
			src.Namespace = sourceNS
		}
	case ociRepositoryKind:
	default:
		return ChartSource{}, false, nil
	}
	return src, true, nil
}

// chartTemplateContent returns the spec.chart of obj, a HelmRelease, as the
// cluster would be sent it, and whether it is an object. A typed HelmRelease
// has only its chart template converted: the converter decodes values into
// float64, so a whole conversion would refuse a Helm value one cannot hold
// (1e400), which the HelmRelease carries exactly and this does not read.
func chartTemplateContent(obj client.Object) (map[string]any, bool, error) {
	if hr, ok := obj.(*helmv2.HelmRelease); ok {
		if hr.Spec.Chart == nil {
			return nil, false, nil
		}
		chart, err := runtime.DefaultUnstructuredConverter.ToUnstructured(hr.Spec.Chart)
		return chart, err == nil, err
	}
	content, err := objectContent(obj)
	if err != nil {
		return nil, false, err
	}
	spec, _ := content["spec"].(map[string]any)
	chart, ok := spec["chart"].(map[string]any)
	return chart, ok, nil
}

// claimAt is a claim and the index of the application or document that
// generates the object making it.
type claimAt struct {
	controllerClaim
	at int
}

// claimedIDs returns the identities claims name. A collision on one is
// reported by claimCollisions, with its claims and every generator, so a check
// leaves it out of its own report and reports each object once.
func claimedIDs(claims []claimAt) map[objectIdentity]bool {
	ids := make(map[objectIdentity]bool, len(claims))
	for _, c := range claims {
		ids[c.id] = true
	}
	return ids
}

// claimCollisions reports each object a claim names that something else also
// produces: an object generated by any application or document, the claimant's
// own included, or another claim, as generators keys them. Claims are kept
// apart from generators because the checks key an object once per application
// or document: filed with them, a claim would be lost beside its claimant's own
// object of that identity. owner names the application or document at an
// index. Each object is reported once, in the order it was first claimed, with
// its claims first, then its generators in input order.
func claimCollisions(claims []claimAt, generators map[objectIdentity][]int, owner func(int) string) []string {
	byID := map[objectIdentity][]claimAt{}
	var order []objectIdentity
	for _, c := range claims {
		prev := byID[c.id]
		if len(prev) == 0 {
			order = append(order, c.id)
		}
		// One claimant generated twice by one producer claims once.
		if slices.Contains(prev, c) {
			continue
		}
		byID[c.id] = append(prev, c)
	}
	var collisions []string
	for _, id := range order {
		claimed, generated := byID[id], generators[id]
		if len(claimed)+len(generated) < 2 {
			continue
		}
		var names []string
		for _, c := range claimed {
			names = append(names, fmt.Sprintf("%s of %s", c.controllerClaim, owner(c.at)))
		}
		for _, i := range generated {
			names = append(names, owner(i))
		}
		collisions = append(collisions, fmt.Sprintf("%s is generated by %s", id, joinProducers(names)))
	}
	return collisions
}

// joinProducers joins names as the collision messages do: "both A and B" for
// two, "A, B and C" for more.
func joinProducers(names []string) string {
	last := len(names) - 1
	switch last {
	case 0:
		return names[0]
	case 1:
		return "both " + names[0] + " and " + names[1]
	}
	return strings.Join(names[:last], ", ") + " and " + names[last]
}
