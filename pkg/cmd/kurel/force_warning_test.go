package kurel

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/go-kure/launcher/pkg/oam"
)

// These tests pin go-kure/launcher#720 through kurel's own transformer: every
// force-applied PersistentVolumeClaim gets one warning, whichever source built
// the claim (a component's `volumes` entry, a `pvc` trait, a
// persistentvolumeclaim component or a manifests component), and the output is
// unchanged. The force-replace trait sets the delivery intent on the claim's
// application (go-kure/launcher#782) and a manifests component can carry the
// Flux annotation itself; kurel sets no bundle's Force (go-kure/launcher#781),
// and that reason is covered in pkg/oam.

const forcedWarningTail = ": when an update changes an immutable field, Flux deletes and recreates it instead of failing the apply, which can lose its data"

const forceAnnotationReason = "kustomize.toolkit.fluxcd.io/force: enabled"

const forceIntentReason = "its application sets the force-replace delivery intent"

const forcedClaimsHeader = `apiVersion: launcher.gokure.dev/v1alpha1
kind: Application
metadata:
  name: shop
  namespace: default
spec:
  components:
    - name: api
      type: webservice
      properties:
        image: ghcr.io/example/api:v1.0.0
        port: 8080
        volumes:
          - name: cache
            type: pvc
            mountPath: /cache
            size: 1Gi
      traits:
        - type: pvc
          properties:
            name: shared-data
            size: 5Gi
`

// forceReplacedClaims is forcedClaimsHeader with the force-replace trait on its
// component.
var forceReplacedClaims = strings.Replace(forcedClaimsHeader, "      traits:\n", "      traits:\n        - type: force-replace\n", 1)

// forcedVolumeWarnings transforms appYAML with kurel's builtin transformer,
// generates it and returns the force warnings, as runBuild produces them.
func forcedVolumeWarnings(t *testing.T, appYAML string) []string {
	t.Helper()
	transformer := newBuiltinTransformer()
	var got []string
	transformer.SetWarningHandler(func(msg string) { got = append(got, msg) })
	app, err := oam.ParseWithExtraTypes([]byte(appYAML), nil, transformer.LowerableTypes())
	if err != nil {
		t.Fatalf("parsing: %v", err)
	}
	if err := transformer.ValidateAuthoredProperties(app); err != nil {
		t.Fatalf("validating: %v", err)
	}
	cluster, err := transformer.Transform(app, oam.TransformContext{Domain: kurelDomain})
	if err != nil {
		t.Fatalf("transforming: %v", err)
	}
	apps, err := oam.GenerateApplications(cluster)
	if err != nil {
		t.Fatalf("generating: %v", err)
	}
	if err := oam.CheckInDocumentCollisions(apps); err != nil {
		t.Fatalf("collision check: %v", err)
	}
	transformer.WarnForcedVolumes(apps)
	return got
}

func TestWarnForcedVolumes_BothClaimSources(t *testing.T) {
	// A role kind turns a described volume claim into a synthesized `pvc` trait
	// (go-kure/launcher#702), so it is attributed as a sub-application too.
	volumeClaim := `PersistentVolumeClaim default/api-cache (sub-application "api-cache" of component "api") is force-applied (` + forceIntentReason + `)` + forcedWarningTail
	traitClaim := `PersistentVolumeClaim default/shared-data (sub-application "shared-data" of component "api") is force-applied (` + forceIntentReason + `)` + forcedWarningTail
	cases := []struct {
		name, app string
		want      []string
	}{
		{"no force", forcedClaimsHeader, nil},
		{"force-replace trait", forceReplacedClaims, []string{volumeClaim, traitClaim}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := forcedVolumeWarnings(t, tc.app)
			if !slices.Equal(got, tc.want) {
				t.Errorf("warnings =\n%q\nwant\n%q", got, tc.want)
			}
		})
	}
}

// TestWarnForcedVolumes_ClaimComponent pins that the persistentvolumeclaim
// component's claim is warned about like any other generated claim.
func TestWarnForcedVolumes_ClaimComponent(t *testing.T) {
	const app = `apiVersion: launcher.gokure.dev/v1alpha1
kind: Application
metadata:
  name: shop
  namespace: default
spec:
  components:
    - name: media
      type: persistentvolumeclaim
      properties:
        size: 20Gi
      traits:
        - type: force-replace
`
	want := []string{`PersistentVolumeClaim default/media (component "media") is force-applied (` + forceIntentReason + `)` + forcedWarningTail}
	if got := forcedVolumeWarnings(t, app); !slices.Equal(got, want) {
		t.Errorf("warnings =\n%q\nwant\n%q", got, want)
	}
}

// TestWarnForcedVolumes_ManifestsClaims pins that a claim a `manifests` component
// emits is warned about by its own annotation. A claim inside a list envelope
// no longer builds through `manifests`: an object left untyped inside a list is
// one the environment policy cannot read, and it refuses it. The warning for
// the members of an envelope, which a caller's own handler can still emit, is
// covered in pkg/oam.
func TestWarnForcedVolumes_ManifestsClaims(t *testing.T) {
	const app = `apiVersion: launcher.gokure.dev/v1alpha1
kind: Application
metadata:
  name: shop
  namespace: default
spec:
  components:
    - name: raw
      type: manifests
      properties:
        inline: |
          apiVersion: v1
          kind: PersistentVolumeClaim
          metadata:
            name: annotated
            namespace: default
            annotations:
              kustomize.toolkit.fluxcd.io/force: enabled
          ---
          apiVersion: v1
          kind: PersistentVolumeClaim
          metadata:
            name: plain
            namespace: default
`
	want := []string{`PersistentVolumeClaim default/annotated (component "raw") is force-applied (` + forceAnnotationReason + `)` + forcedWarningTail}
	if got := forcedVolumeWarnings(t, app); !slices.Equal(got, want) {
		t.Errorf("warnings =\n%q\nwant\n%q", got, want)
	}
}

// TestBuild_ForcedClaimWarnsOnStderr pins that `kurel build` prints the warning to
// stderr, and only there: the manifest output carries no warning text.
func TestBuild_ForcedClaimWarnsOnStderr(t *testing.T) {
	dir := t.TempDir()
	plain := filepath.Join(dir, "plain.yaml")
	forced := filepath.Join(dir, "forced.yaml")
	profile := filepath.Join("testdata", "pvc-trait-force-replace", "cluster.yaml")
	if err := os.WriteFile(plain, []byte(forcedClaimsHeader), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(forced, []byte(forceReplacedClaims), 0o644); err != nil {
		t.Fatal(err)
	}
	build := func(appPath string) (string, string) {
		t.Helper()
		cmd := NewKurelCommand()
		var stdout, stderr bytes.Buffer
		cmd.SetOut(&stdout)
		cmd.SetErr(&stderr)
		cmd.SetArgs([]string{"build", appPath, "--profile", profile})
		if err := cmd.Execute(); err != nil {
			t.Fatalf("build %s: %v\nstderr: %s", appPath, err, stderr.String())
		}
		return stdout.String(), stderr.String()
	}
	if _, plainErr := build(plain); plainErr != "" {
		t.Errorf("unforced build warned: %q", plainErr)
	}
	forcedOut, forcedErr := build(forced)
	want := "warning: PersistentVolumeClaim default/api-cache (sub-application \"api-cache\" of component \"api\") is force-applied (" + forceIntentReason + ")" + forcedWarningTail + "\n" +
		"warning: PersistentVolumeClaim default/shared-data (sub-application \"shared-data\" of component \"api\") is force-applied (" + forceIntentReason + ")" + forcedWarningTail + "\n"
	if forcedErr != want {
		t.Errorf("stderr =\n%s\nwant\n%s", forcedErr, want)
	}
	if forcedOut == "" || strings.Contains(forcedOut, "is force-applied") {
		t.Errorf("the manifest output is empty or carries the warning:\n%s", forcedOut)
	}
}
