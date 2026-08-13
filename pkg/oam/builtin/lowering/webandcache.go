package lowering

import (
	"github.com/go-kure/launcher/pkg/errors"
	"github.com/go-kure/launcher/pkg/oam"
)

// WebAndCacheRule is a toy component-position lowering rule proving D1's component
// position: it lowers a "web-and-cache" component into a "webservice" web
// component and a "webservice" cache component (D2: 1→2), plus one emitted
// "ordered" policy (proving a component-position rule may populate the Policies
// slice, per loweringPositionRules) recording that the web component depends on
// the cache component being up first. The "ordered" policy is itself non-terminal —
// OrderedRule (ordered.go) lowers it into terminal "dependency" policies on a later
// fixpoint round.
type WebAndCacheRule struct{}

func (WebAndCacheRule) ComponentType() string { return "web-and-cache" }

func (WebAndCacheRule) LowerComponent(comp *oam.Component, lctx oam.LoweringContext) (oam.LoweringResult, error) {
	image, _ := comp.Properties["image"].(string)
	if image == "" {
		return oam.LoweringResult{}, errors.Errorf("web-and-cache component %q: properties.image is required", comp.Name)
	}

	webName, err := lctx.Namer.Name(comp.Name, "web", lctx.Origin)
	if err != nil {
		return oam.LoweringResult{}, err
	}
	cacheName, err := lctx.Namer.Name(comp.Name, "cache", lctx.Origin)
	if err != nil {
		return oam.LoweringResult{}, err
	}

	web := oam.Component{Name: webName, Type: "webservice", Properties: map[string]any{"image": image}}
	cache := oam.Component{Name: cacheName, Type: "webservice", Properties: map[string]any{"image": "redis:7"}}

	orderedPolicy := oam.ApplicationPolicy{
		Name: comp.Name + "-order",
		Type: "ordered",
		Properties: map[string]any{
			"sequence": []any{cacheName, webName},
		},
	}

	return oam.LoweringResult{
		Components: []oam.Component{web, cache},
		Policies:   []oam.ApplicationPolicy{orderedPolicy},
	}, nil
}
