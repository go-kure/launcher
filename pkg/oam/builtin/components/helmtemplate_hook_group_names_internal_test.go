package components

import (
	"strings"
	"testing"

	"github.com/go-kure/launcher/pkg/oam"
)

// TestHookGroupNaming_ChildNames: with no prefix the directory keeps the whole
// default name and the Kustomization name is shortened to 63 characters; a
// prefix names both as written. A prefix that makes a name too long is refused
// for the longest child name, whichever group that is, so the prefix length the
// message asks for fits every child.
func TestHookGroupNaming_ChildNames(t *testing.T) {
	suffixes := []string{"-00-pre-install", "-01-main", "-02-post-install", "-03-test"}

	t.Run("the default", func(t *testing.T) {
		long := strings.Repeat("c", 60)
		got, err := hookGroupNaming{component: long, application: "shop"}.childNames(long, suffixes)
		if err != nil {
			t.Fatalf("childNames: %v", err)
		}
		for i, suffix := range suffixes {
			if want := "shop-" + long + suffix; got[i].dir != want {
				t.Errorf("child %d: directory %q, want %q", i, got[i].dir, want)
			}
			want := oam.ShortenNameWithSuffix("shop-"+long, suffix, 63)
			if got[i].kustomization != want || len(want) > 63 || !strings.HasSuffix(want, suffix) {
				t.Errorf("child %d: Kustomization name %q, want %q: at most 63 characters and ending in %q", i, got[i].kustomization, want, suffix)
			}
		}
		short, err := hookGroupNaming{component: "db", application: "shop"}.childNames("db", suffixes)
		if err != nil {
			t.Fatalf("childNames: %v", err)
		}
		if want := (hookGroupChildNames{dir: "shop-db-01-main", kustomization: "shop-db-01-main"}); short[1] != want {
			t.Errorf("a name that fits: %+v, want %+v", short[1], want)
		}
		direct, err := hookGroupNaming{component: "db"}.childNames("db", suffixes)
		if err != nil {
			t.Fatalf("childNames: %v", err)
		}
		if want := (hookGroupChildNames{dir: "db-01-main", kustomization: "db-01-main"}); direct[1] != want {
			t.Errorf("no application: %+v, want %+v", direct[1], want)
		}
	})

	t.Run("a prefix", func(t *testing.T) {
		// 47 characters: the longest child name is exactly 63.
		fits := strings.Repeat("p", 47)
		got, err := hookGroupNaming{component: "db", application: "shop", prefix: fits}.childNames("db", suffixes)
		if err != nil {
			t.Fatalf("childNames: %v", err)
		}
		for i, suffix := range suffixes {
			if want := (hookGroupChildNames{dir: fits + suffix, kustomization: fits + suffix}); got[i] != want {
				t.Errorf("child %d: %+v, want %+v", i, got[i], want)
			}
		}

		// 49 characters: the first child's name is 64 already, and the third's,
		// the longest, is 65.
		over := strings.Repeat("p", 49)
		_, err = hookGroupNaming{component: "db", application: "shop", prefix: over}.childNames("db", suffixes)
		want := `helmtemplate: component "db": hook-group name "` + over + `-02-post-install" (role "hook-group") is 65 characters, and a Flux Kustomization name has at most 63; ` +
			`its prefix "` + over + `" was set by hookGroupNamePrefix or returned by the Naming hook and is never shortened: use a prefix of at most 47 characters, or none for the default, which is shortened`
		if err == nil || err.Error() != want {
			t.Errorf("err = %v\nwant %s", err, want)
		}

		_, err = hookGroupNaming{component: "db", prefix: "Bad_Prefix"}.childNames("db", suffixes)
		if want := `helmtemplate: component "db": hook-group name "Bad_Prefix-00-pre-install" (role "hook-group") is not a valid DNS-1123 subdomain: `; err == nil || !strings.HasPrefix(err.Error(), want) {
			t.Errorf("err = %v\nwant one beginning %q", err, want)
		}
	})
}
