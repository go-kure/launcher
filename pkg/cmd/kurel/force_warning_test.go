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
// force-applied PersistentVolumeClaim gets one warning, whichever source built the
// claim (a component's `volumes` entry or a `pvc` trait) and whichever way it is
// forced (the force-replace trait's annotation or a reconciliation policy's
// force: true), and the output is unchanged.

const forcedWarningTail = ": when an update changes an immutable field, Flux deletes and recreates it instead of failing the apply, which can lose its data"

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
	const (
		annotation = "kustomize.toolkit.fluxcd.io/force: enabled"
		bundle     = "its bundle's reconciliation policy sets force: true"
	)
	volumeClaim := func(reason string) string {
		return `PersistentVolumeClaim default/api-cache (component "api") is force-applied (` + reason + `)` + forcedWarningTail
	}
	traitClaim := func(reason string) string {
		return `PersistentVolumeClaim default/shared-data (sub-application "shared-data" of component "api") is force-applied (` + reason + `)` + forcedWarningTail
	}
	forceReplace := strings.Replace(forcedClaimsHeader, "      traits:\n", "      traits:\n        - type: force-replace\n", 1)
	const policy = `  policies:
    - name: flux
      type: reconciliation
      properties:
        force: true
`
	cases := []struct {
		name, app string
		want      []string
	}{
		{"no force", forcedClaimsHeader, nil},
		{"force-replace trait", forceReplace, []string{volumeClaim(annotation), traitClaim(annotation)}},
		{"reconciliation force", forcedClaimsHeader + policy, []string{volumeClaim(bundle), traitClaim(bundle)}},
		{"both", forceReplace + policy, []string{volumeClaim(annotation + "; " + bundle), traitClaim(annotation + "; " + bundle)}},
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

// TestWarnForcedVolumes_ListMembers pins that a claim inside a list a `manifests`
// component generates is warned about as Flux applies it: by its own annotation,
// or by the bundle's force. The manifest parser expands the outer list itself, so
// the claims sit in a nested one, which reaches the generated output as a list
// envelope that Kustomize and Flux expand at apply time.
func TestWarnForcedVolumes_ListMembers(t *testing.T) {
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
          apiVersion: example.com/v1
          kind: Widget
          items:
            - apiVersion: example.com/v1
              kind: ClaimList
              metadata:
                name: claims
                namespace: default
              items:
                - apiVersion: v1
                  kind: PersistentVolumeClaim
                  metadata:
                    name: annotated
                    namespace: default
                    annotations:
                      kustomize.toolkit.fluxcd.io/force: enabled
                - apiVersion: v1
                  kind: PersistentVolumeClaim
                  metadata:
                    name: plain
                    namespace: default
`
	const policy = "  policies:\n    - name: flux\n      type: reconciliation\n      properties:\n        force: true\n"
	claim := func(name, reason string) string {
		return `PersistentVolumeClaim default/` + name + ` (component "raw") is force-applied (` + reason + `)` + forcedWarningTail
	}
	if got, want := forcedVolumeWarnings(t, app), []string{claim("annotated", "kustomize.toolkit.fluxcd.io/force: enabled")}; !slices.Equal(got, want) {
		t.Errorf("annotated member: warnings =\n%q\nwant\n%q", got, want)
	}
	bundle := "its bundle's reconciliation policy sets force: true"
	want := []string{claim("annotated", "kustomize.toolkit.fluxcd.io/force: enabled; "+bundle), claim("plain", bundle)}
	if got := forcedVolumeWarnings(t, app+policy); !slices.Equal(got, want) {
		t.Errorf("forced bundle: warnings =\n%q\nwant\n%q", got, want)
	}
}

// TestBuild_ForcedClaimWarnsOnStderr pins that `kurel build` prints the warning to
// stderr and that its manifest output does not change.
func TestBuild_ForcedClaimWarnsOnStderr(t *testing.T) {
	dir := t.TempDir()
	plain := filepath.Join(dir, "plain.yaml")
	forced := filepath.Join(dir, "forced.yaml")
	profile := filepath.Join("testdata", "pvc-trait-force-replace", "cluster.yaml")
	if err := os.WriteFile(plain, []byte(forcedClaimsHeader), 0o644); err != nil {
		t.Fatal(err)
	}
	policy := "  policies:\n    - name: flux\n      type: reconciliation\n      properties:\n        force: true\n"
	if err := os.WriteFile(forced, []byte(forcedClaimsHeader+policy), 0o644); err != nil {
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
	plainOut, plainErr := build(plain)
	forcedOut, forcedErr := build(forced)
	if plainErr != "" {
		t.Errorf("unforced build warned: %q", plainErr)
	}
	want := "warning: PersistentVolumeClaim default/api-cache (component \"api\") is force-applied (its bundle's reconciliation policy sets force: true)" + forcedWarningTail + "\n" +
		"warning: PersistentVolumeClaim default/shared-data (sub-application \"shared-data\" of component \"api\") is force-applied (its bundle's reconciliation policy sets force: true)" + forcedWarningTail + "\n"
	if forcedErr != want {
		t.Errorf("stderr =\n%s\nwant\n%s", forcedErr, want)
	}
	if forcedOut != plainOut {
		t.Errorf("the warning changed the manifest output:\n--- forced\n%s\n--- plain\n%s", forcedOut, plainOut)
	}
}
