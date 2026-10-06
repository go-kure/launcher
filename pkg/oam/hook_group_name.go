package oam

import (
	"fmt"

	"github.com/go-kure/launcher/pkg/errors"
)

// ErrHookGroupNameTooLong is what the refusal of an over-long hook-group name
// answers to under errors.Is: a name built from a prefix the author or the
// Naming hook set, which is never shortened, that no Flux Kustomization can
// carry (go-kure/launcher#787). errors.As with a *HookGroupNameError finds the
// same error and says which component, which name and which limit.
var ErrHookGroupNameTooLong = errors.New("oam: hook-group name too long")

// HookGroupNameError is the refusal of a hook-group name, "<prefix>-<NN>-<phase>",
// that is longer than a Flux Kustomization name may be. Only a prefix set by
// hookGroupNamePrefix or returned by the Naming hook is refused so: the default
// prefix is shortened. Its text is the one the refusal has always had, built
// from these fields.
//
// Two places return it. The transform does, behind a prefix of its own
// (`component "db": …`): it holds every config to the environment policy, the
// default one when it is given none, and a helmtemplate config renders its
// chart for that, so the names are known there (HookGroupNameChecker). The
// config's AugmentLayout does, when the layout is built, for what no transform
// checked: a config built directly, and a prefix set on a config after its
// transform. It is found with errors.As, or recognised with
// errors.Is(err, ErrHookGroupNameTooLong).
type HookGroupNameError struct {
	// ComponentType is the type of the component whose config built the name,
	// which leads the text: "helmtemplate", also for a helm component under
	// delivery: template, which is lowered to one.
	ComponentType string
	// Component is the component the hook groups belong to.
	Component string
	// Role is the role the prefix was resolved under, NameRoleHookGroup.
	Role NameRole
	// Name is the refused name: the longest of the component's hook-group
	// names, so a prefix that fits it fits them all.
	Name string
	// Length is the length of Name, in bytes, as the text counts it.
	Length int
	// Limit is the longest name a Flux Kustomization may have, 63.
	Limit int
	// Prefix is the prefix Name begins with, as the author or the hook gave it.
	Prefix string
}

// Error returns the refusal's text. The prefix length it asks for is what Limit
// leaves beside the part of Name that follows Prefix.
func (e *HookGroupNameError) Error() string {
	return fmt.Sprintf("%s: component %q: hook-group name %q (role %q) is %d characters, and a Flux Kustomization name has at most %d; its prefix %q was set by %s or returned by the Naming hook and is never shortened: use a prefix of at most %d characters, or none for the default, which is shortened",
		e.ComponentType, e.Component, e.Name, e.Role, e.Length, e.Limit, e.Prefix, HookGroupNamePrefixProperty, e.Limit-(e.Length-len(e.Prefix)))
}

// Is makes the error answer to ErrHookGroupNameTooLong under errors.Is.
func (e *HookGroupNameError) Is(target error) bool { return target == ErrHookGroupNameTooLong }
