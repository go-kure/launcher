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
	// A role kind turns a described volume claim into a synthesized `pvc` trait
	// (go-kure/launcher#702), so it is attributed as a sub-application too.
	volumeClaim := func(reason string) string {
		return `PersistentVolumeClaim default/api-cache (sub-application "api-cache" of component "api") is force-applied (` + reason + `)` + forcedWarningTail
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

// TestWarnForcedVolumes_ClaimComponent pins that the persistentvolumeclaim
// component's claim is warned about like any other generated claim, by either
// force mechanism.
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
  policies:
    - name: flux
      type: reconciliation
      properties:
        force: true
`
	want := []string{`PersistentVolumeClaim default/media (component "media") is force-applied (kustomize.toolkit.fluxcd.io/force: enabled; its bundle's reconciliation policy sets force: true)` + forcedWarningTail}
	if got := forcedVolumeWarnings(t, app); !slices.Equal(got, want) {
		t.Errorf("warnings =\n%q\nwant\n%q", got, want)
	}
}

// TestWarnForcedVolumes_FluxCDPatches pins go-kure/launcher#728 through kurel's
// own transformer: the fluxcd-patches trait's patches are applied before the
// claim is read, so a patch that adds the force key warns and one that removes it
// does not.
func TestWarnForcedVolumes_FluxCDPatches(t *testing.T) {
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
`
	const (
		addForce = `        - type: fluxcd-patches
          properties:
            patches:
              - target:
                  kind: PersistentVolumeClaim
                patch: |
                  - op: add
                    path: /metadata/annotations
                    value:
                      kustomize.toolkit.fluxcd.io/force: enabled
`
		removeForce = `        - type: force-replace
        - type: fluxcd-patches
          properties:
            patches:
              - target:
                  kind: PersistentVolumeClaim
                  name: media
                patch: |
                  - op: remove
                    path: /metadata/annotations/kustomize.toolkit.fluxcd.io~1force
`
	)
	want := []string{`PersistentVolumeClaim default/media (component "media") is force-applied (kustomize.toolkit.fluxcd.io/force: enabled, set by its bundle's patches)` + forcedWarningTail}
	if got := forcedVolumeWarnings(t, app+addForce); !slices.Equal(got, want) {
		t.Errorf("patch adds the force key: warnings =\n%q\nwant\n%q", got, want)
	}
	if got := forcedVolumeWarnings(t, app+removeForce); len(got) != 0 {
		t.Errorf("patch removes the force key: warnings = %q, want none", got)
	}
}

// TestBuild_PatchedForceWarningLeavesOutputUntagged pins that reading a bundle's
// patched volumes changes nothing kurel emits: no kustomize-internal annotation
// of the patch build reaches the manifest output or any file a delivery build
// writes.
func TestBuild_PatchedForceWarningLeavesOutputUntagged(t *testing.T) {
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
        - type: fluxcd-patches
          properties:
            patches:
              - target:
                  kind: PersistentVolumeClaim
                patch: |
                  - op: add
                    path: /metadata/annotations
                    value:
                      kustomize.toolkit.fluxcd.io/force: enabled
`
	dir := t.TempDir()
	appPath := writeTempFile(t, dir, "app.yaml", app)
	profile := filepath.Join(deliveryTestdata, "cluster.yaml")
	build := func(args ...string) string {
		t.Helper()
		cmd := NewKurelCommand()
		var stdout, stderr bytes.Buffer
		cmd.SetOut(&stdout)
		cmd.SetErr(&stderr)
		cmd.SetArgs(append([]string{"build", appPath, "--profile", profile}, args...))
		if err := cmd.Execute(); err != nil {
			t.Fatalf("build %q: %v\nstderr: %s", args, err, stderr.String())
		}
		if !strings.Contains(stderr.String(), "set by its bundle's patches") {
			t.Fatalf("build %q did not read the patched claim: stderr = %q", args, stderr.String())
		}
		return stdout.String()
	}
	const tag = "internal.config.kubernetes.io"
	if out := build(); strings.Contains(out, tag) {
		t.Errorf("manifest output carries %s:\n%s", tag, out)
	}
	outDir := filepath.Join(dir, "out")
	build("-o", outDir, "--oci-repository", testOCIRepository)
	files := 0
	err := filepath.WalkDir(outDir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		files++
		if bytes.Contains(data, []byte(tag)) {
			t.Errorf("%s carries %s:\n%s", path, tag, data)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if files == 0 {
		t.Fatal("the delivery build wrote no file")
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
	want := "warning: PersistentVolumeClaim default/api-cache (sub-application \"api-cache\" of component \"api\") is force-applied (its bundle's reconciliation policy sets force: true)" + forcedWarningTail + "\n" +
		"warning: PersistentVolumeClaim default/shared-data (sub-application \"shared-data\" of component \"api\") is force-applied (its bundle's reconciliation policy sets force: true)" + forcedWarningTail + "\n"
	if forcedErr != want {
		t.Errorf("stderr =\n%s\nwant\n%s", forcedErr, want)
	}
	if forcedOut != plainOut {
		t.Errorf("the warning changed the manifest output:\n--- forced\n%s\n--- plain\n%s", forcedOut, plainOut)
	}
}
