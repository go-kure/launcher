package components

import (
	"fmt"
	"maps"
	"strings"

	"github.com/go-kure/launcher/pkg/errors"
	"github.com/go-kure/launcher/pkg/oam"
)

// roleObjectTraits are the authored trait types a role rule forwards to every
// member, because they cover every object a component generates and each
// member generates its own (see WebserviceRule).
var roleObjectTraits = map[string]bool{"prune-protection": true, "force-replace": true}

// The properties a role component names its members' objects with
// (go-kure/launcher#787). Each names one object the component generates, in
// place of the component name, and that object alone. They end in ObjectName
// because `serviceAccountName` already names an existing account on these
// types, and `serviceName` a Service to refer to on others.
const (
	deploymentObjectNameProperty     = "deploymentObjectName"
	serviceObjectNameProperty        = "serviceObjectName"
	serviceAccountObjectNameProperty = "serviceAccountObjectName"
	// claimObjectNameProperty is a `pvc` volume's key: the name of the claim
	// the volume generates (roleClaims). `claimName` already references an
	// existing claim on the same volume.
	claimObjectNameProperty = "claimObjectName"
)

// schemaRoleObjectNames declares the properties a role component names its
// members' objects with: the Deployment's and the ServiceAccount's, and, for a
// type that generates one (service), the Service's.
func schemaRoleObjectNames(service bool) map[string]oam.PropertySchema {
	m := map[string]oam.PropertySchema{
		deploymentObjectNameProperty:     {Type: oam.PropertyTypeString, Description: "Name of the Deployment the component generates, in place of the component name, used as written. It names the object alone: the pod labels, the selectors and every name derived from the component keep the component name."},
		serviceAccountObjectNameProperty: {Type: oam.PropertyTypeString, Description: "Name of the ServiceAccount the component generates for its pods, in place of the component name, used as written. The pods run as it, and an rbac trait binds it. Not with serviceAccountName, which names an existing account: the component then generates none."},
	}
	if service {
		m[serviceObjectNameProperty] = oam.PropertySchema{Type: oam.PropertyTypeString, Description: "Name of the Service the component generates, in place of the component name, used as written; a DNS-1035 label. The routing traits on the component and the synthesized NetworkPolicy follow it. The Service's name is its DNS name in the cluster. Launcher builds no such address, so every address written with the component name (an env value, a URL in another component) is the author's to change."}
	}
	return m
}

// dropRoleObjectNames removes the object-name properties from the properties a
// role rule hands its deployment member: the rule reads them itself.
func dropRoleObjectNames(depProps map[string]any) {
	delete(depProps, deploymentObjectNameProperty)
	delete(depProps, serviceObjectNameProperty)
	delete(depProps, serviceAccountObjectNameProperty)
}

// nameRoleMember resolves the name of the object of member, one a role rule is
// about to emit for comp, and sets it on member
// (oam.LoweringContext.ResolveMemberName): the author's property, else the
// Naming hook's answer for role, else the component name. handler is the
// member's own, which declares the object's kind and scope; object is what the
// refusal calls it. The member keeps the component's name, so the group, the
// labels and the selectors stay where they were.
func nameRoleMember(comp *oam.Component, lctx oam.LoweringContext, member *oam.Component, handler oam.ComponentObjectProvider, role oam.NameRole, property, object string) error {
	spec := memberNameSpec(handler, role)
	name, named, err := parseRawStringField(comp.Properties, property, property)
	if err != nil {
		return err
	}
	if named {
		spec.Property, spec.Authored = property, name
	}
	// The name is the component's own, whatever context the rule was handed.
	lctx.Component = comp
	if err := lctx.ResolveMemberName(member, spec); err != nil {
		return errors.Wrapf(err, "naming the %s", object)
	}
	return nil
}

// roleServiceAccount moves a role component's per-component ServiceAccount out
// of its deployment member into a `serviceaccount` sibling member
// (go-kure/launcher#702). When serviceAccountName is authored, the pod runs as
// that existing account and nothing is emitted, as before; a
// serviceAccountObjectName beside it would name an account the component does
// not generate, and is refused. Otherwise the member is the account deployment
// generated itself — the component's name, its labels,
// automountServiceAccountToken false — under the name resolved for its object
// (nameRoleMember, role oam.NameRoleWorkloadServiceAccount), and the deployment
// member is handed that object name, so it emits no account of its own and its
// pods run as the one generated. traits are the authored traits forwarded to
// the member: the object decorators, which acted on the account when deployment
// generated it.
func roleServiceAccount(comp *oam.Component, depProps map[string]any, traits []oam.Trait, lctx oam.LoweringContext) (*oam.Component, error) {
	if _, authored := authoredValue(depProps, "serviceAccountName"); authored {
		if _, named := authoredValue(comp.Properties, serviceAccountObjectNameProperty); named {
			return nil, errors.Errorf("%s and serviceAccountName are both set: serviceAccountName names an existing account, so the component generates none for %s to name; remove one of them",
				serviceAccountObjectNameProperty, serviceAccountObjectNameProperty)
		}
		return nil, nil
	}
	var saTraits []oam.Trait
	for _, t := range traits {
		if roleObjectTraits[t.Type] {
			saTraits = append(saTraits, t)
		}
	}
	sa := &oam.Component{
		Name:        comp.Name,
		Type:        "serviceaccount",
		Properties:  map[string]any{"automountServiceAccountToken": false},
		Traits:      saTraits,
		Annotations: maps.Clone(comp.Annotations),
	}
	if err := nameRoleMember(comp, lctx, sa, &ServiceAccountHandler{}, oam.NameRoleWorkloadServiceAccount, serviceAccountObjectNameProperty, "ServiceAccount"); err != nil {
		return nil, err
	}
	// The reference follows the object's name, not the member's.
	depProps["serviceAccountName"] = sa.ObjectName()
	return sa, nil
}

// roleClaims moves the claims a role component's `pvc` volumes generate out of
// its deployment member into synthesized `pvc` traits (go-kure/launcher#702),
// one per volume that does not reference an existing claim, in volume order.
// Each trait carries the claim the deployment kind built before it stopped
// generating claims — size, storageClassName (an authored "" included),
// accessModes and volumeMode — under the name resolved for it
// (go-kure/launcher#787, role oam.NameRoleWorkloadVolumeClaim): the volume's
// claimObjectName, else the Naming hook's answer, else the component-qualified
// name (escapeForPVCQualification). Its volume is rewritten to reference that
// claim by claimName, keeping accessModes so the non-RWX constraints still see
// it. depProps must be the rule's own copy: its `volumes` list is replaced,
// never edited in place. parseVolumes has already accepted every volume, so
// only the shape this rewrite reads is assumed.
//
// A synthesized trait is sealed, so the engine merges no capability rendering
// into it. A volume that leaves storageClass unauthored (absent or null)
// therefore takes the ClusterProfile `pvc` capability's storageClassName here,
// as an authored pvc trait and the persistentvolumeclaim kind take it
// (go-kure/launcher#746). An authored value, "" included, wins. The binding is
// read through lctx, so its key is recorded as consumed, and only when a
// volume generates a claim.
func roleClaims(comp *oam.Component, depProps map[string]any, lctx oam.LoweringContext) ([]oam.Trait, error) {
	vols, ok := depProps["volumes"].([]any)
	if !ok {
		return nil, nil
	}
	var traits []oam.Trait
	var platformClass any
	platformRead := false
	rewritten := make([]any, len(vols))
	// The claims are the component's own, whatever context the rule was handed.
	lctx.Component = comp
	if lctx.Namer == nil {
		// A webservice or worker rule driven directly, outside the engine, keeps
		// working as before, as nameRoleMember does: no Naming hook is asked, the
		// name is the author's or the default, and no transform follows to claim
		// it. An allocator of this call's own only holds its volumes' claims
		// apart.
		lctx.Namer = oam.NewNameAllocator()
	}
	for i, v := range vols {
		rewritten[i] = v
		m, ok := nullElem(v).(map[string]any)
		if !ok || m["type"] != "pvc" {
			continue
		}
		if _, ref := authoredValue(m, "claimName"); ref {
			continue
		}
		volName, _ := m["name"].(string)
		spec := oam.NameSpec{Role: oam.NameRoleWorkloadVolumeClaim, Kind: coreKind("PersistentVolumeClaim")}
		property := fmt.Sprintf("volumes[%d].%s", i, claimObjectNameProperty)
		authored, named, err := parseRawStringField(m, claimObjectNameProperty, property)
		if err != nil {
			return nil, err
		}
		if named {
			spec.Property, spec.Authored = property, authored
		}
		// Kubernetes PersistentVolumeClaim names must be DNS-1123 subdomains.
		// The characters of the default are checked on the name as built, before
		// shortening can replace an invalid one by the digest; only the length
		// may be over. An authored name stands in for a default that cannot be
		// built (LoweringContext.ResolveName).
		claimBase, claimSuffix := escapeForPVCQualification(comp.Name), escapeForPVCQualification(volName)
		if errs := oam.SubdomainSyntaxErrors(claimBase + "-" + claimSuffix); len(errs) > 0 && !named {
			return nil, errors.Errorf("PVC name %q is not a valid DNS-1123 subdomain: %s", claimBase+"-"+claimSuffix, strings.Join(errs, "; "))
		}
		// A default over 253 characters is shortened by the one rule: the
		// escaped component is cut, the escaped volume kept whole.
		claim, err := lctx.ResolveName(claimBase, claimSuffix, spec)
		if err != nil {
			return nil, errors.Wrapf(err, "volume %q: naming the PersistentVolumeClaim", volName)
		}
		props := map[string]any{"name": claim, "size": m["size"]}
		if !platformRead {
			platformRead = true
			sc, err := capabilityStorageClass(lctx)
			if err != nil {
				return nil, err
			}
			platformClass = sc
		}
		if sc, present := authoredValue(m, "storageClass"); present {
			props["storageClassName"] = sc
		} else if platformClass != nil {
			props["storageClassName"] = platformClass
		}
		for _, key := range []string{"accessModes", "volumeMode"} {
			if val, present := authoredValue(m, key); present {
				props[key] = val
			}
		}
		traits = append(traits, oam.Trait{Type: "pvc", Properties: props})

		vol := maps.Clone(m)
		delete(vol, "size")
		delete(vol, "storageClass")
		delete(vol, claimObjectNameProperty)
		vol["claimName"] = claim
		rewritten[i] = vol
	}
	if len(traits) > 0 {
		depProps["volumes"] = rewritten
	}
	return traits, nil
}

// capabilityStorageClass returns the storageClassName the ClusterProfile `pvc`
// capability renders, or nil when the profile binds no `pvc` key or the
// rendering leaves it absent or null. A value that is not a string is refused:
// the claim's storageClassName is one, and anything else would share a
// profile value with the output.
func capabilityStorageClass(lctx oam.LoweringContext) (any, error) {
	binding, ok := lctx.Capability("pvc")
	if !ok {
		return nil, nil
	}
	v, present := authoredValue(binding.Rendering, "storageClassName")
	if !present {
		return nil, nil
	}
	if _, isString := v.(string); !isString {
		return nil, errors.Errorf("capability \"pvc\" storageClassName: expected string, got %T", v)
	}
	return v, nil
}
