package oam

import (
	"fmt"

	"github.com/go-kure/kure/pkg/stack"
)

// subAppOrigin is who named a sub-application and the name it was created
// under (name). Its own ApplyPolicy, and any other step, may rename it:
// ownName is the name its trait or its own policy last gave it, policyFrom the
// name its own policy renamed it from (policyRenamed), and renamedSince is set
// when any other step renamed it after ownName was given (recordRenames).
type subAppOrigin struct {
	claim         resolvedNameClaim
	name          string
	ownName       string
	policyFrom    string
	policyRenamed bool
	renamedSince  bool
}

// newSubAppOrigin is the origin of a sub-application claim named name.
func newSubAppOrigin(claim resolvedNameClaim, name string) subAppOrigin {
	return subAppOrigin{claim: claim, name: name, ownName: name}
}

// recordSubAppOrigins records in origins who named each sub-application in
// added, the applications one trait of type traitType appended to the bundle.
// names are the sub-application names the trait resolved (takeSubAppNames). A
// name it never resolved is the trait's own (nameFromTrait). A name it resolved
// from one source only (the author's property, the hook, the default), as often
// as it added sub-applications of that name or more, is named from that
// source; with the default it was resolved for when every resolution had the
// same one, else with none (the hook gave one name for two defaults). Any other
// (two sources, or fewer resolutions than sub-applications) is resolved in more
// than one way, and which one named which sub-application is not known
// (nameFromTraitUnknown): a resolution is a name, not an application, so the
// order the trait resolved them in says nothing of the order it added them in.
func recordSubAppOrigins(origins map[*stack.Application]subAppOrigin, added []*stack.Application, names map[string][]subAppName, naming *traitNaming, traitType string) {
	count := make(map[string]int, len(added))
	for _, a := range added {
		count[a.Name]++
	}
	for _, a := range added {
		claim := resolvedNameClaim{owner: naming.ownerOf(traitType, NameRoleSubApplication, a.Name), source: nameFromTrait}
		if resolved := names[a.Name]; len(resolved) > 0 {
			claim.source = nameFromTraitUnknown
			if r, def, ok := oneSource(resolved); ok && count[a.Name] <= len(resolved) {
				claim = resolvedNameClaim{owner: naming.ownerOf(traitType, NameRoleSubApplication, def), source: r.source, property: r.property}
			}
		}
		origins[a] = newSubAppOrigin(claim, a.Name)
	}
}

// oneSource reports whether every resolution of one name came from one source
// and property, returning the first and the default they share, "" when they
// were resolved for two.
func oneSource(resolved []subAppName) (subAppName, string, bool) {
	first, def := resolved[0], resolved[0].def
	for _, r := range resolved[1:] {
		if r.source != first.source || r.property != first.property {
			return first, "", false
		}
		if r.def != def {
			def = ""
		}
	}
	return first, def, true
}

// recordedNames returns the current name of every sub-application origins
// records.
func recordedNames(origins map[*stack.Application]subAppOrigin) map[*stack.Application]string {
	names := make(map[*stack.Application]string, len(origins))
	for a := range origins {
		names[a] = a.Name
	}
	return names
}

// recordRenames records what one step changed among the sub-applications
// origins records, whose names were before as it started (recordedNames). The
// step is the ApplyPolicy of own, or a trait's Apply when own is nil. When its
// own policy renamed own, the name it gave is own's ownName, whatever renamed
// own before; any other rename, by another sub-application's policy or by a
// trait, whichever trait added the renamed one, marks it renamed since. Only a
// sub-application's own policy is its policy rename (nameFromPolicy); every
// other rename is a later one (nameRenamedLater), even one back to an earlier
// name.
func recordRenames(origins map[*stack.Application]subAppOrigin, before map[*stack.Application]string, own *stack.Application) {
	for a, name := range before {
		origin := origins[a]
		if a.Name == name {
			continue
		}
		if own != nil && a == own {
			origin.ownName, origin.policyFrom = a.Name, name
			origin.policyRenamed, origin.renamedSince = true, false
		} else {
			origin.renamedSince = true
		}
		origins[a] = origin
	}
}

// checkBundleApplicationNames fails when two applications of bundle share one
// name: a component's application and a sub-application, or two
// sub-applications. Their objects are generated under one application name, so
// the base library would merge them or refuse them late, by the directory they
// share, without saying who named them. The names are read once every trait of
// the bundle and the ApplyPolicy of every sub-application have run, since a
// policy may rename its own sub-application (go-kure/launcher#787), and again,
// for every bundle of the transform, once no step adds or renames an
// application any more (checkClusterApplicationNames).
//
// entries are components of the transform, whose applications are their own;
// origins says who named each sub-application (recordSubAppOrigins,
// resolveSynthesizedPolicyNames).
func checkBundleApplicationNames(bundle *stack.Bundle, entries []componentEntry, origins map[*stack.Application]subAppOrigin) error {
	component := make(map[*stack.Application]string, len(entries))
	for _, e := range entries {
		component[e.app] = e.component.Name
	}
	claimOf := func(a *stack.Application) resolvedNameClaim {
		if name, ok := component[a]; ok {
			return resolvedNameClaim{owner: nameOwner{component: name}}
		}
		origin, ok := origins[a]
		if !ok {
			// No entry, no trait's top-level additions and no synthesis account
			// for it (one in a bundle a trait added as a child): who named it is
			// not recorded.
			return resolvedNameClaim{owner: nameOwner{role: NameRoleSubApplication, def: a.Name}, source: nameOriginUnknown}
		}
		switch {
		case origin.renamedSince || a.Name != origin.ownName:
			// Renamed by a trait or another sub-application's policy, or after
			// the transform's traits: reported from the last name its trait or
			// its own policy gave it, or as renamed back to it ("") when that is
			// this one.
			from := origin.ownName
			if from == a.Name {
				from = ""
			}
			origin.claim.source, origin.claim.property = nameRenamedLater, from
		case origin.policyRenamed:
			origin.claim.source, origin.claim.property = nameFromPolicy, origin.policyFrom
		}
		return origin.claim
	}
	first := make(map[string]*stack.Application, len(bundle.Applications))
	for _, a := range bundle.Applications {
		prior, ok := first[a.Name]
		if !ok {
			first[a.Name] = a
			continue
		}
		key := nameClaimKey{class: nameClassSubApplication, objectIdentity: objectIdentity{name: a.Name}}
		return &TransformError{Message: fmt.Sprintf("bundle %q", bundle.Name), Cause: nameCollision(key, claimOf(prior), claimOf(a))}
	}
	return nil
}

// checkClusterApplicationNames holds every bundle of cluster to
// checkBundleApplicationNames, a bundle before its children: the
// sub-applications of the synthesized NetworkPolicies, and a bundle a trait
// added as a child, are there only now.
func checkClusterApplicationNames(cluster *stack.Cluster, entries []componentEntry, origins map[*stack.Application]subAppOrigin) error {
	if cluster == nil {
		return nil
	}
	var firstErr error
	walkBundles(cluster.Node, func(bundle *stack.Bundle) {
		if firstErr == nil {
			firstErr = checkBundleApplicationNames(bundle, entries, origins)
		}
	})
	return firstErr
}
