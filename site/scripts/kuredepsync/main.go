// Command kuredepsync is the analysis half of the check-kure-dep-sync guard.
//
// It compares launcher's DIRECT dependencies against the DIRECT dependencies of
// the kure release launcher imports, and reports when launcher would lead kure on
// a shared direct dependency. Go's Minimum Version Selection makes the floor
// uncontrollable (launcher can never fall below the kure it imports), so only the
// "launcher ahead" direction is enforced.
//
// The wrapper script (check-kure-dep-sync.sh) handles all git/go plumbing and
// hands this program four go.mod files (head + base, launcher + kure); this
// program only parses and applies the rule, so it stays pure and unit-testable.
//
// Modes:
//
//	--report                              whole-tree lead report, never fails
//	--launcher-base X --kure-base Y        diff-scoped enforcement (exit 1 on violation)
//
// The rule (only enforced when base files are supplied):
//
//	violation iff headAhead && ( !baseAhead
//	                             || launcher raised D
//	                             || kure regressed for D )
//
// Each comparison runs only when both operands are present; an absent version
// means "not a direct shared dep on that side".
package main

import (
	"fmt"
	"io"
	"os"
	"sort"

	"golang.org/x/mod/modfile"
	"golang.org/x/mod/semver"
)

const kureModule = "github.com/go-kure/kure"

func main() {
	code, err := run(os.Args[1:], os.Stdout)
	if err != nil {
		fmt.Fprintln(os.Stderr, "kuredepsync:", err)
		os.Exit(2)
	}
	os.Exit(code)
}

type opts struct {
	launcherHead string
	kureHead     string
	launcherBase string
	kureBase     string
	report       bool
}

// run parses args, applies the guard, and returns a process exit code
// (0 = ok/report, 1 = violations found).
func run(args []string, out io.Writer) (int, error) {
	var o opts
	for i := 0; i < len(args); i++ {
		next := func() string {
			i++
			if i >= len(args) {
				return ""
			}
			return args[i]
		}
		switch args[i] {
		case "--launcher-head":
			o.launcherHead = next()
		case "--kure-head":
			o.kureHead = next()
		case "--launcher-base":
			o.launcherBase = next()
		case "--kure-base":
			o.kureBase = next()
		case "--report":
			o.report = true
		default:
			return 2, fmt.Errorf("unknown argument: %s", args[i])
		}
	}
	if o.launcherHead == "" || o.kureHead == "" {
		return 2, fmt.Errorf("--launcher-head and --kure-head are required")
	}

	launcherHead, err := directRequires(o.launcherHead)
	if err != nil {
		return 2, err
	}
	kureHead, err := directRequires(o.kureHead)
	if err != nil {
		return 2, err
	}

	// Report mode (explicit, or no base supplied): list current direct lead, never fail.
	if o.report || o.launcherBase == "" {
		reportLead(out, launcherHead, kureHead)
		return 0, nil
	}

	launcherBase, err := directRequires(o.launcherBase)
	if err != nil {
		return 2, err
	}
	kureBase, err := directRequires(o.kureBase)
	if err != nil {
		return 2, err
	}

	violations := evaluate(launcherHead, kureHead, launcherBase, kureBase)
	if len(violations) == 0 {
		fmt.Fprintln(out, "OK: launcher does not newly lead imported kure on any shared direct dependency.")
		return 0, nil
	}
	for _, v := range violations {
		fmt.Fprintln(out, v.message())
	}
	return 1, nil
}

type violation struct {
	module       string
	reason       string // launcher-raised | kure-regressed | newly-shared/ahead
	launcherHead string
	kureHead     string
}

func (v violation) message() string {
	return fmt.Sprintf(
		"%s [%s]: launcher pins %s but imported kure pins %s — launcher would lead kure. "+
			"Land the matching kure release and bump the %s require first, then take this dep. "+
			"(guard: check-kure-dep-sync.sh)",
		v.module, v.reason, v.launcherHead, v.kureHead, kureModule)
}

// evaluate applies the guard rule and returns the violating modules, sorted by name.
//
// A dep D is a "direct shared dep on a side" iff it is a direct require of both
// launcher and that side's imported kure. Comparisons run only when both operands
// are present.
func evaluate(launcherHead, kureHead, launcherBase, kureBase map[string]string) []violation {
	// Candidate set: union of the per-side shared-direct sets. kure itself is never
	// a candidate (directRequires already drops it; guarded here for defense too).
	candidates := map[string]struct{}{}
	for m := range launcherHead {
		if _, ok := kureHead[m]; ok && m != kureModule {
			candidates[m] = struct{}{}
		}
	}
	for m := range launcherBase {
		if _, ok := kureBase[m]; ok && m != kureModule {
			candidates[m] = struct{}{}
		}
	}

	var out []violation
	for m := range candidates {
		lh, lhOK := launcherHead[m]
		kh, khOK := kureHead[m]
		lb, lbOK := launcherBase[m]
		kb, kbOK := kureBase[m]

		headAhead := lhOK && khOK && semver.Compare(lh, kh) > 0
		if !headAhead {
			continue
		}
		baseAhead := lbOK && kbOK && semver.Compare(lb, kb) > 0

		var reason string
		switch {
		case lhOK && lbOK && semver.Compare(lh, lb) > 0:
			reason = "launcher-raised"
		case khOK && kbOK && semver.Compare(kh, kb) < 0:
			reason = "kure-regressed"
		case !baseAhead:
			reason = "newly-shared/ahead"
		default:
			// headAhead && baseAhead && launcher unchanged && kure not regressed:
			// pre-existing, grandfathered drift — not a violation.
			continue
		}
		out = append(out, violation{module: m, reason: reason, launcherHead: lh, kureHead: kh})
	}

	sort.Slice(out, func(i, j int) bool { return out[i].module < out[j].module })
	return out
}

// reportLead prints current direct-shared lead without failing.
func reportLead(out io.Writer, launcherHead, kureHead map[string]string) {
	var ahead []string
	for m, lv := range launcherHead {
		kv, ok := kureHead[m]
		if !ok {
			continue
		}
		if semver.Compare(lv, kv) > 0 {
			ahead = append(ahead, fmt.Sprintf("  %s: launcher %s > kure %s", m, lv, kv))
		}
	}
	sort.Strings(ahead)
	if len(ahead) == 0 {
		fmt.Fprintln(out, "Shared direct deps (enforced): launcher does not lead imported kure. OK.")
		return
	}
	fmt.Fprintln(out, "Shared direct deps where launcher leads imported kure (enforced scope):")
	for _, line := range ahead {
		fmt.Fprintln(out, line)
	}
}

// directRequires parses a go.mod and returns its DIRECT (non-indirect) requires as
// module→version, excluding the kure module itself.
func directRequires(path string) (map[string]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	f, err := modfile.Parse(path, data, nil)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	m := map[string]string{}
	for _, r := range f.Require {
		if r.Indirect || r.Mod.Path == kureModule {
			continue
		}
		m[r.Mod.Path] = r.Mod.Version
	}
	return m, nil
}
