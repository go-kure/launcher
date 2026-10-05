package oam

import (
	"fmt"

	"github.com/go-kure/kure/pkg/stack"
	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// forTrait returns what the engine attaches to trait before applying it for
// component, on the sibling group member of type member when the component is a
// group. The trait's slot is its authored index when an authored trait stands
// behind it, else position, its place among the traits of the component it is
// applied on. Each call is one trait applied: two calls never give one owner,
// whatever their slots. A nil resolver attaches nothing: the trait then
// resolves as one built outside a transform does.
func (r *nameResolver) forTrait(component, member string, trait Trait, position int) *traitNaming {
	if r == nil {
		return nil
	}
	r.applied++
	naming := &traitNaming{resolver: r, component: component, member: member, slot: position, apply: r.applied}
	if trait.authoredIndex != nil {
		naming.slot, naming.authored = *trait.authoredIndex, true
	}
	if r.outputs == nil {
		r.outputs = make(map[traitPlace]int)
	}
	place := traitPlace{component: component, member: member, trait: trait.Type, slot: naming.slot, authored: naming.authored}
	r.outputs[place]++
	naming.nth = r.outputs[place]
	return naming
}

// resolveBundleName resolves the name of the application's bundle
// (NameRoleBundle) or of one group's (NameRoleGroup). An empty default is
// returned as it is, for the bundle's own constructor to refuse.
func (r *nameResolver) resolveBundleName(role NameRole, def string) (string, error) {
	if def == "" {
		return def, nil
	}
	name, err := r.resolve(nameOwner{role: role, def: def}, NameSpec{Role: role, Default: def})
	if err != nil {
		return "", &TransformError{Message: fmt.Sprintf("%s name", role), Cause: err}
	}
	return name, nil
}

// resolveHookGroupNamePrefix resolves the prefix of the hook-group layout names
// of component (NameRoleHookGroup) and hands config a prefix that is not the
// default: the author's, or the hook's answer. The default,
// "<application>-<component>", is claimed like any other and left for the config
// to build: it is the only prefix the config may shorten. Two components that
// resolve one prefix are refused with both named.
//
// Only the prefix is claimed. The child names are built from it after the
// render, and two different prefixes can still give one name: a default that
// is shortened to another component's prefix, or a prefix that ends as the
// phase of another component's group begins. The transform accepts those; the
// base library refuses a Flux Kustomization name used twice where it
// integrates the walked tree, which is where every name is known.
func (r *nameResolver) resolveHookGroupNamePrefix(component string, config HookGroupNamePrefixSetter) error {
	def := component
	if r != nil && r.application != "" {
		def = r.application + "-" + component
	}
	spec := NameSpec{Role: NameRoleHookGroup, Default: def}
	if authored, ok := config.AuthoredHookGroupNamePrefix(); ok {
		spec.Property, spec.Authored = HookGroupNamePrefixProperty, authored
	}
	prefix, source, err := r.resolveFrom(nameOwner{component: component, role: NameRoleHookGroup, def: def}, spec)
	if err != nil {
		return err
	}
	if source != nameFromDefault {
		config.SetHookGroupNamePrefix(prefix)
	}
	return nil
}

// synthesizedPolicy is a config of the NetworkPolicy synthesis: one generated
// NetworkPolicy, named by the transform after the synthesis has run.
type synthesizedPolicy interface {
	// synthesizedFor returns the component the policy was synthesized for, ""
	// for an external backend's.
	synthesizedFor() string
	policyName() string
	setPolicyName(name string)
}

func (c *componentAllowPolicyConfig) synthesizedFor() string { return c.ComponentName }

// policyName returns the configured resource name, defaulting to the
// component's inbound name so a directly-built config keeps its name.
func (c *componentAllowPolicyConfig) policyName() string {
	if c.PolicyName == "" {
		return ingressTrafficPolicyName(c.ComponentName)
	}
	return c.PolicyName
}
func (c *componentAllowPolicyConfig) setPolicyName(name string) { c.PolicyName = name }

func (c *backendIngressAllowPolicyConfig) synthesizedFor() string    { return c.ComponentName }
func (c *backendIngressAllowPolicyConfig) policyName() string        { return c.PolicyName }
func (c *backendIngressAllowPolicyConfig) setPolicyName(name string) { c.PolicyName = name }

func (c *componentEgressPolicyConfig) synthesizedFor() string { return c.ComponentName }

// policyName returns the configured resource name, defaulting to the
// component's outbound name so a directly-built config keeps its name.
func (c *componentEgressPolicyConfig) policyName() string {
	if c.PolicyName == "" {
		return egressTrafficPolicyName(c.ComponentName)
	}
	return c.PolicyName
}
func (c *componentEgressPolicyConfig) setPolicyName(name string) { c.PolicyName = name }

func (c *componentEndpointIngressPolicyConfig) synthesizedFor() string    { return c.ComponentName }
func (c *componentEndpointIngressPolicyConfig) setPolicyName(name string) { c.PolicyName = name }

// resolveSynthesizedPolicyNames resolves, for every NetworkPolicy the synthesis
// added to cluster, the policy's name (NameRoleNetpolSynth) and the name of the
// sub-application holding it (NameRoleSubApplication). Both defaults are the
// name the synthesis gave the policy; the two are resolved apart, so a hook
// that renames the sub-application leaves the policy's name alone.
//
// It runs after the three synthesis passes and before anything wraps a config,
// so each synthesized application still holds its own config type.
func (r *nameResolver) resolveSynthesizedPolicyNames(cluster *stack.Cluster) error {
	if r == nil || cluster == nil {
		return nil
	}
	kind := schema.GroupKind{Group: networkingv1.GroupName, Kind: "NetworkPolicy"}
	var firstErr error
	walkLeafBundles(cluster.Node, func(bundle *stack.Bundle) {
		for _, app := range bundle.Applications {
			if firstErr != nil {
				return
			}
			policy, ok := app.Config.(synthesizedPolicy)
			if !ok {
				continue
			}
			component, def := policy.synthesizedFor(), policy.policyName()
			// An external backend's policy is owned by its Service: two Services
			// whose shortened default names meet are two owners, and are refused.
			service := ""
			if external, ok := policy.(*backendIngressAllowPolicyConfig); ok {
				service = external.Service
			}
			name, err := r.resolve(
				nameOwner{component: component, service: service, role: NameRoleNetpolSynth, def: def},
				NameSpec{Role: NameRoleNetpolSynth, Kind: kind, Namespace: app.Namespace, Default: def})
			if err == nil {
				policy.setPolicyName(name)
				app.Name, err = r.resolve(
					nameOwner{component: component, role: NameRoleSubApplication, def: app.Name},
					NameSpec{Role: NameRoleSubApplication, Default: app.Name})
			}
			if err != nil {
				firstErr = &TransformError{Message: fmt.Sprintf("synthesized NetworkPolicy %q", def), Cause: err}
			}
		}
	})
	return firstErr
}
