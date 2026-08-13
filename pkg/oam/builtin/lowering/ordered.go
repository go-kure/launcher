package lowering

import (
	"strconv"

	"github.com/go-kure/launcher/pkg/errors"
	"github.com/go-kure/launcher/pkg/oam"
)

// OrderedRule is a toy policy-position lowering rule proving D1's fourth position:
// it lowers an "ordered" policy — properties.sequence, an ordered list of component
// names — into one terminal "dependency" policy (pkg/oam/builtin/policies) per
// consecutive pair, expressing that each later component depends on the one before
// it.
type OrderedRule struct{}

func (OrderedRule) PolicyType() string { return "ordered" }

func (OrderedRule) LowerPolicy(pol *oam.ApplicationPolicy, lctx oam.LoweringContext) (oam.LoweringResult, error) {
	raw, _ := pol.Properties["sequence"].([]any)
	if len(raw) < 2 {
		return oam.LoweringResult{}, errors.Errorf(
			"ordered policy %q: properties.sequence must be an array of at least 2 component names", pol.Name)
	}
	sequence := make([]string, len(raw))
	for i, v := range raw {
		s, ok := v.(string)
		if !ok {
			return oam.LoweringResult{}, errors.Errorf(
				"ordered policy %q: properties.sequence[%d] must be a string, got %T", pol.Name, i, v)
		}
		sequence[i] = s
	}

	policies := make([]oam.ApplicationPolicy, 0, len(sequence)-1)
	for i := 1; i < len(sequence); i++ {
		name, err := lctx.Namer.Name(pol.Name, strconv.Itoa(i-1), lctx.Origin)
		if err != nil {
			return oam.LoweringResult{}, err
		}
		policies = append(policies, oam.ApplicationPolicy{
			Name: name,
			Type: "dependency",
			Properties: map[string]any{
				"component": sequence[i],
				"dependsOn": []any{sequence[i-1]},
			},
		})
	}

	return oam.LoweringResult{Policies: policies}, nil
}
