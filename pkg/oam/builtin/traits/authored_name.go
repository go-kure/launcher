package traits

import (
	"strings"

	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/validation"

	"github.com/go-kure/launcher/pkg/errors"
	"github.com/go-kure/launcher/pkg/oam"
)

// checkAuthoredObjectName refuses an authored name that cannot be the name of
// the object it names. A name launcher generates by default is shortened to fit
// (oam.ShortenName); an authored one is used as written or refused, never
// shortened, since only its author can say what it should be instead
// (go-kure/launcher#787). property is the trait property the name was written
// in, object what it names ("the ConfigMap").
//
// Every object these traits name can carry a DNS-1123 subdomain: for most
// built-in kinds among them it is their own validation, for a custom resource
// the API server's default for metadata.name. The cluster's rule for the RBAC
// kinds is looser; the rbac trait's `name` is held to the subdomain all the
// same (see the README for why).
func checkAuthoredObjectName(property, object, name string) error {
	if name == "" {
		return errors.Errorf("%s is empty: write the name of %s, or leave the property out for the default", property, object)
	}
	if errs := validation.IsDNS1123Subdomain(name); len(errs) > 0 {
		return errors.Errorf("%s %q cannot name %s: not a valid DNS-1123 subdomain: %s",
			property, name, object, strings.Join(errs, "; "))
	}
	return nil
}

// resolveObjectName resolves the name of an object a trait generates
// (oam.Trait.ResolveName): the authored one, else the consumer hook's, else
// def, launcher's default already shortened to fit. authored is the value of
// property as the handler parsed it, "" when the author left the property out;
// an empty authored string is refused before it gets here
// (checkAuthoredObjectName), so "" never stands for one.
func resolveObjectName(trait *oam.Trait, role oam.NameRole, kind schema.GroupKind, namespace, property, authored, def string) (string, error) {
	return resolveNameSpec(trait, oam.NameSpec{Role: role, Kind: kind, Namespace: namespace, Default: def}, property, authored)
}

// resolveClusterObjectName is resolveObjectName for a cluster-scoped object,
// which has no namespace (oam.NameSpec.ClusterScoped).
func resolveClusterObjectName(trait *oam.Trait, role oam.NameRole, kind schema.GroupKind, property, authored, def string) (string, error) {
	return resolveNameSpec(trait, oam.NameSpec{Role: role, Kind: kind, ClusterScoped: true, Default: def}, property, authored)
}

func resolveNameSpec(trait *oam.Trait, spec oam.NameSpec, property, authored string) (string, error) {
	if authored != "" {
		spec.Property, spec.Authored = property, authored
	}
	return trait.ResolveName(spec)
}

// resolveSubApplicationName resolves the name of the sub-application a trait
// adds to the bundle: the consumer hook's, else def. No author property names
// a sub-application.
func resolveSubApplicationName(trait *oam.Trait, def string) (string, error) {
	return trait.ResolveName(oam.NameSpec{Role: oam.NameRoleSubApplication, Default: def})
}

// checkAuthoredNamePart refuses an authored value that is one part of a
// generated object name when the name built from it has a character an object
// name cannot hold. Only the syntax is checked: the generated name is shortened
// when it is too long, and shortening would hide an invalid character in the
// part a digest replaces. full is the name as built, before it is shortened.
func checkAuthoredNamePart(property, value, object, full string) error {
	if errs := oam.SubdomainSyntaxErrors(full); len(errs) > 0 {
		return errors.Errorf("%s %q cannot be part of the name of %s (%q): not a valid DNS-1123 subdomain: %s",
			property, value, object, full, strings.Join(errs, "; "))
	}
	return nil
}
