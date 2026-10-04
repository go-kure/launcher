package traits

import (
	"strings"

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
// Every object these traits name takes a DNS-1123 subdomain: the built-in kinds
// among them by their own validation, a custom resource by the API server's
// default for metadata.name.
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
