package oam

import (
	"cmp"
	"regexp"
	"sort"
	"strings"

	"github.com/go-kure/launcher/pkg/errors"
)

// Type identity (go-kure/launcher#1258): a component, trait or policy type, or
// a configuration-defined model, is identified by `name@version`, lowercase.
// The grammar and the resolution rule are in README.md, "Type identity".

// Refusals of the type-identity functions. Each is wrapped with the offending
// text, so a caller matches with errors.Is and prints the error as it is.
var (
	// ErrTypeIdentityInvalid: the text is not `name@version` of the grammar.
	ErrTypeIdentityInvalid = errors.New("invalid type identity")
	// ErrTypeRegistration: an identity conflicts with one already registered.
	ErrTypeRegistration = errors.New("type identity registration refused")
	// ErrTypeReferenceUnknown: no registered type is named by the reference.
	ErrTypeReferenceUnknown = errors.New("unknown type reference")
	// ErrTypeReferenceAmbiguous: the reference names more than one type or version.
	ErrTypeReferenceAmbiguous = errors.New("ambiguous type reference")
)

const maxTypeNameLen = 253

var (
	// The first label starts with a letter; a later label (the domain or the
	// Kubernetes group) is a DNS label and may start with a digit.
	firstLabelRe = regexp.MustCompile(`^[a-z]([a-z0-9-]*[a-z0-9])?$`)
	laterLabelRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`)
	// v<major>, v<major>.<minor> or v<major>.<minor>.<patch>: a short version is exact.
	semverRe = regexp.MustCompile(`^v(0|[1-9][0-9]*)(?:\.(0|[1-9][0-9]*))?(?:\.(0|[1-9][0-9]*))?$`)
	// A Kubernetes API version beyond vN: v1beta1, v2alpha3.
	apiVersionRe = regexp.MustCompile(`^v[1-9][0-9]*(alpha|beta)[1-9][0-9]*$`)
)

// TypeID is the identity of one type at one version. Name is the qualified
// name: `<name>.<domain>` for a launcher or consumer type, `kind.group` for a
// Kubernetes kind (the core group has no group part, so a core kind is a bare
// `service`). Version is the version as the type declares it.
type TypeID struct {
	Name    string
	Version string
}

// String returns the `name@version` form.
func (t TypeID) String() string { return t.Name + "@" + t.Version }

// ShortName returns the name a type claims as a short name: the leading label
// of Name, which is Name itself when it has no dot.
func (t TypeID) ShortName() string {
	if i := strings.IndexByte(t.Name, '.'); i >= 0 {
		return t.Name[:i]
	}
	return t.Name
}

// ValidateTypeIdentity checks s against the grammar and returns its parts. It
// refuses a missing or empty name or version, an uppercase letter, a name whose
// first label does not start with a letter, an empty label and a version that
// is neither v<semver> nor a Kubernetes API version. It resolves nothing: a
// reference to a registered type goes through ResolveTypeReference.
func ValidateTypeIdentity(s string) (TypeID, error) {
	name, version, ok := strings.Cut(s, "@")
	if !ok || version == "" {
		return TypeID{}, errors.Errorf("%w %q: want name@version", ErrTypeIdentityInvalid, s)
	}
	if err := validateTypeName(name); err != nil {
		return TypeID{}, errors.Errorf("%w %q: %w", ErrTypeIdentityInvalid, s, err)
	}
	if key, err := normalizeTypeVersion(version); err != nil {
		return TypeID{}, errors.Errorf("%w %q: %w", ErrTypeIdentityInvalid, s, err)
	} else if err := checkVersionForName(name, key); err != nil {
		return TypeID{}, errors.Errorf("%w %q: %w", ErrTypeIdentityInvalid, s, err)
	}
	return TypeID{Name: name, Version: version}, nil
}

func validateTypeName(name string) error {
	if name == "" {
		return errors.New("empty name")
	}
	if len(name) > maxTypeNameLen {
		return errors.Errorf("name longer than %d characters", maxTypeNameLen)
	}
	for i, label := range strings.Split(name, ".") {
		re := laterLabelRe
		if i == 0 {
			re = firstLabelRe
		}
		if !re.MatchString(label) {
			return errors.Errorf("label %q is not lowercase letters, digits and hyphens (the first label starts with a letter)", label)
		}
	}
	return nil
}

// checkVersionForName refuses an API version (v1beta1) on a launcher-domain
// name: only a Kubernetes kind carries one, every other type carries v<semver>.
func checkVersionForName(name, key string) error {
	if strings.HasSuffix(name, ".launcher.gokure.dev") && apiVersionRe.MatchString(key) {
		return errors.Errorf("version %q is a Kubernetes API version, but a launcher type takes v<semver>", key)
	}
	return nil
}

// normalizeTypeVersion returns the key two spellings of one version share:
// v2, v2.0 and v2.0.0 are one version, an API version such as v1beta1 is itself.
func normalizeTypeVersion(v string) (string, error) {
	if m := semverRe.FindStringSubmatch(v); m != nil {
		parts := [3]string{m[1], "0", "0"}
		for i := 2; i <= 3; i++ {
			if m[i] != "" {
				parts[i-1] = m[i]
			}
		}
		return "v" + strings.Join(parts[:], "."), nil
	}
	if apiVersionRe.MatchString(v) {
		return v, nil
	}
	return "", errors.Errorf("version %q is neither v<major>[.<minor>[.<patch>]] nor a Kubernetes API version", v)
}

// TypeRegistry holds the identities of one position (components, traits or
// policies), keyed by name and version. It carries identity only: the handler
// stays in the Transformer map under its plain type name until the dispatch
// moves to resolved identities.
type TypeRegistry struct {
	versions map[string]map[string]TypeID // qualified name -> normalized version -> id
	shorts   map[string]map[string]bool   // short name -> qualified names claiming it
}

// NewTypeRegistry returns an empty registry.
func NewTypeRegistry() *TypeRegistry {
	return &TypeRegistry{versions: map[string]map[string]TypeID{}, shorts: map[string]map[string]bool{}}
}

// Register adds id. It refuses an invalid identity, a version already
// registered under the name (v2 and v2.0.0 are one version), and a short name
// that would equal the qualified name of another type, in either order.
func (r *TypeRegistry) Register(id TypeID) error {
	if _, err := ValidateTypeIdentity(id.String()); err != nil {
		return errors.Errorf("%w: %w", ErrTypeRegistration, err)
	}
	key, _ := normalizeTypeVersion(id.Version)
	if prev, dup := r.versions[id.Name][key]; dup {
		return errors.Errorf("%w: %s duplicates %s", ErrTypeRegistration, id, prev)
	}
	short := id.ShortName()
	// A dotted name's short name must not be another type's qualified name; and a
	// bare name (a core kind) must not already be the short name of another type.
	if short != id.Name {
		if _, taken := r.versions[short]; taken {
			return errors.Errorf("%w: short name %q of %s is the qualified name of another type", ErrTypeRegistration, short, id)
		}
	} else {
		for other := range r.shorts[short] {
			if other != id.Name {
				return errors.Errorf("%w: the name %s is the short name of %s", ErrTypeRegistration, id, other)
			}
		}
	}
	if r.versions[id.Name] == nil {
		r.versions[id.Name] = map[string]TypeID{}
	}
	r.versions[id.Name][key] = id
	if r.shorts[short] == nil {
		r.shorts[short] = map[string]bool{}
	}
	r.shorts[short][id.Name] = true
	return nil
}

// IDs returns every registered identity, ordered by name and then by version:
// major, then alpha before beta before GA, then the number after the stage (see
// versionLess).
func (r *TypeRegistry) IDs() []TypeID {
	var out []TypeID
	for _, byVersion := range r.versions {
		for _, id := range byVersion {
			out = append(out, id)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return versionLess(out[i].Version, out[j].Version)
	})
	return out
}

// Stages of a version, in order: a Kubernetes alpha, a beta, and a v<semver>.
const (
	stageAlpha = iota
	stageBeta
	stageGA
)

// versionKey is the place of a version in the order of a type's versions
// (go-kure/launcher#1288). The numbers are digit strings, which the grammar
// writes without a leading zero, so they compare by length and then by text:
// a number longer than an int still has its place. An invalid version has no
// key; it is ordered after every valid one, by its text.
type versionKey struct {
	invalid bool
	raw     string // the text, set only for an invalid version
	major   string
	stage   int
	a, b    string // alpha and beta: the number after the stage, and "0"; GA: minor and patch
}

func parseVersionKey(v string) versionKey {
	norm, err := normalizeTypeVersion(v)
	if err != nil {
		return versionKey{invalid: true, raw: v}
	}
	rest := strings.TrimPrefix(norm, "v")
	if parts := strings.Split(rest, "."); len(parts) == 3 {
		return versionKey{major: parts[0], stage: stageGA, a: parts[1], b: parts[2]}
	}
	for stage, word := range [...]string{stageAlpha: "alpha", stageBeta: "beta"} {
		if major, n, ok := strings.Cut(rest, word); ok {
			return versionKey{major: major, stage: stage, a: n, b: "0"}
		}
	}
	return versionKey{invalid: true, raw: v} // normalizeTypeVersion accepts nothing else
}

func compareNumbers(a, b string) int {
	return cmp.Or(cmp.Compare(len(a), len(b)), strings.Compare(a, b))
}

// versionLess orders versions by major, then stage (alpha, beta, GA), then the
// number after the stage (alpha, beta) or the minor and patch (GA). It is a
// strict weak ordering: the comparison is lexicographic over a fixed list of
// totally ordered parts, and two spellings of one version (v2, v2.0, v2.0.0)
// are the same key, so neither is less.
func versionLess(a, b string) bool {
	ka, kb := parseVersionKey(a), parseVersionKey(b)
	if ka.invalid || kb.invalid {
		if ka.invalid && kb.invalid {
			return ka.raw < kb.raw
		}
		return kb.invalid
	}
	return cmp.Or(
		compareNumbers(ka.major, kb.major),
		cmp.Compare(ka.stage, kb.stage),
		compareNumbers(ka.a, kb.a),
		compareNumbers(ka.b, kb.b),
	) < 0
}

// Resolve resolves ref, `name` or `name@version`, to one registered identity.
// The name is a qualified name or a short name; a short name resolves only
// when exactly one type claims it. An omitted version resolves only when
// exactly one version is registered. The result is the registered identity, so
// it carries the exact version resolved to.
func (r *TypeRegistry) Resolve(ref string) (TypeID, error) {
	name, version, hasVersion := strings.Cut(ref, "@")
	if err := validateTypeName(name); err != nil {
		return TypeID{}, errors.Errorf("%w %q: %w", ErrTypeIdentityInvalid, ref, err)
	}
	if hasVersion && version == "" {
		return TypeID{}, errors.Errorf("%w %q: empty version", ErrTypeIdentityInvalid, ref)
	}
	qualified, err := r.resolveName(name, ref)
	if err != nil {
		return TypeID{}, err
	}
	byVersion := r.versions[qualified]
	if !hasVersion {
		if len(byVersion) != 1 {
			return TypeID{}, errors.Errorf("%w %q: %d versions registered (%s), name one", ErrTypeReferenceAmbiguous, ref, len(byVersion), r.versionList(qualified))
		}
		for _, id := range byVersion {
			return id, nil
		}
	}
	key, err := normalizeTypeVersion(version)
	if err == nil {
		err = checkVersionForName(qualified, key)
	}
	if err != nil {
		return TypeID{}, errors.Errorf("%w %q: %w", ErrTypeIdentityInvalid, ref, err)
	}
	if id, ok := byVersion[key]; ok {
		return id, nil
	}
	return TypeID{}, errors.Errorf("%w %q: %s has no version %s (registered: %s)", ErrTypeReferenceUnknown, ref, qualified, version, r.versionList(qualified))
}

func (r *TypeRegistry) resolveName(name, ref string) (string, error) {
	if _, ok := r.versions[name]; ok {
		// A qualified name, or a bare core kind. Registration keeps a short name from
		// equalling another type's qualified name, so this is never also an alias.
		return name, nil
	}
	claimants := r.shorts[name]
	switch len(claimants) {
	case 0:
		return "", errors.Errorf("%w %q: no registered type is named %q", ErrTypeReferenceUnknown, ref, name)
	case 1:
		for q := range claimants {
			return q, nil
		}
	}
	names := make([]string, 0, len(claimants))
	for q := range claimants {
		names = append(names, q)
	}
	sort.Strings(names)
	return "", errors.Errorf("%w %q: the short name %q is claimed by %s, use a qualified name", ErrTypeReferenceAmbiguous, ref, name, strings.Join(names, ", "))
}

func (r *TypeRegistry) versionList(name string) string {
	var vs []TypeID
	for _, id := range r.versions[name] {
		vs = append(vs, id)
	}
	sort.Slice(vs, func(i, j int) bool { return versionLess(vs[i].Version, vs[j].Version) })
	out := make([]string, len(vs))
	for i, id := range vs {
		out[i] = id.Version
	}
	return strings.Join(out, ", ")
}

// ResolveTypeReference resolves ref against registered, a flat list of
// identities (a registry's IDs, or a caller's own set), by the rule in
// README.md "Type identity". It refuses the list itself when two entries
// conflict, since a resolution against a conflicted set proves nothing.
func ResolveTypeReference(ref string, registered []TypeID) (TypeID, error) {
	r := NewTypeRegistry()
	for _, id := range registered {
		if err := r.Register(id); err != nil {
			return TypeID{}, err
		}
	}
	return r.Resolve(ref)
}
