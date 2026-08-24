package kurel

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
	"sigs.k8s.io/controller-runtime/pkg/client"

	kio "github.com/go-kure/kure/pkg/io"
	"github.com/go-kure/kure/pkg/stack"
	"github.com/go-kure/kure/pkg/stack/layout"

	"github.com/go-kure/launcher/pkg/errors"
	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/components"
	"github.com/go-kure/launcher/pkg/oam/builtin/traits"
)

// kurelDomain is the label/annotation domain the kurel CLI stamps into every transform.
// The library default is oam.DefaultDomain ("gokure.dev"); kurel uses its own subdomain so
// kurel-rendered manifests are self-describing. Embedders override via TransformContext.Domain.
const kurelDomain = "launcher.gokure.dev"

type buildOptions struct {
	profilePath        string
	outputDir          string
	namespace          string
	clusterID          string
	valuesPath         string
	setValues          []string // "key=value" strings from --set
	capabilityDefPaths []string
	strictCapabilities bool
}

func newBuildCommand() *cobra.Command {
	opts := &buildOptions{}

	cmd := &cobra.Command{
		Use:   "build <app.yaml|package-dir>",
		Short: "Build Kubernetes manifests from an OAM Application",
		Long: `Build generates static Kubernetes manifests from an OAM Application YAML file
or package directory and a platform ClusterProfile. The positional argument accepts
either a path to an app.yaml file or a directory containing app.yaml (and optionally
kurel.yaml for parameterized packages). Output is written to stdout (default) or a directory.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runBuild(cmd, args[0], opts)
		},
	}

	cmd.Flags().StringVar(&opts.profilePath, "profile", "", "path to ClusterProfile YAML (required)")
	cmd.Flags().StringVarP(&opts.outputDir, "output", "o", "", "output directory (default: stdout)")
	cmd.Flags().StringVarP(&opts.namespace, "namespace", "n", "", "namespace override")
	cmd.Flags().StringVar(&opts.clusterID, "cluster-id", "local", "cluster identifier")
	cmd.Flags().StringVar(&opts.valuesPath, "values", "", "path to values YAML file")
	cmd.Flags().StringArrayVar(&opts.setValues, "set", nil, "set a parameter value (key=value, repeatable)")
	cmd.Flags().StringArrayVar(&opts.capabilityDefPaths, "capability-def", nil, "CapabilityDefinition file (repeatable)")
	cmd.Flags().BoolVar(&opts.strictCapabilities, "strict-capabilities", false, "error instead of warn on unvalidated custom capabilities")

	_ = cmd.MarkFlagRequired("profile")

	return cmd
}

func runBuild(cmd *cobra.Command, arg string, opts *buildOptions) error {
	// Resolve positional arg: file path or directory containing app.yaml.
	var appPath, appDir string
	info, err := os.Stat(arg)
	if err != nil {
		return errors.Wrapf(err, "accessing %q", arg)
	}
	if info.IsDir() {
		appDir = arg
		appPath = filepath.Join(arg, "app.yaml")
	} else {
		appPath = arg
		appDir = filepath.Dir(arg)
	}

	appData, err := os.ReadFile(appPath)
	if err != nil {
		return errors.Wrapf(err, "reading application file %q", appPath)
	}

	// Parameter resolution: look for kurel.yaml next to app.yaml.
	kurelPath := filepath.Join(appDir, "kurel.yaml")
	_, kurelExists := os.Stat(kurelPath)
	hasValues := opts.valuesPath != "" || len(opts.setValues) > 0

	if kurelExists != nil && hasValues {
		return errors.Errorf("--values and --set require a kurel.yaml package descriptor in %q", appDir)
	}

	if kurelExists == nil {
		// Package mode: resolve parameters before parsing.
		kurelData, err := os.ReadFile(kurelPath)
		if err != nil {
			return errors.Wrapf(err, "reading package file %q", kurelPath)
		}
		pkg, err := oam.ParsePackage(kurelData)
		if err != nil {
			return errors.Wrapf(err, "parsing package file %q", kurelPath)
		}

		supplied, err := loadSuppliedValues(opts)
		if err != nil {
			return err
		}

		appData, err = oam.ResolveParameters(appData, pkg.Spec.Parameters, supplied)
		if err != nil {
			return errors.Wrap(err, "resolving parameters")
		}
	}

	// Load capability definitions before parsing the app so that custom trait
	// types from --capability-def pass the trait type validation in oam.Parse.
	capDefs, err := oam.LoadCapabilityDefinitions(opts.capabilityDefPaths, filepath.Join(appDir, "definitions"))
	if err != nil {
		return errors.Wrap(err, "loading capability definitions")
	}

	customTraitTypes := make([]string, 0, len(capDefs))
	for name := range capDefs {
		customTraitTypes = append(customTraitTypes, name)
	}

	transformer := newBuiltinTransformer()

	app, err := oam.ParseWithExtraTypes(appData, customTraitTypes, transformer.LowerableTypes())
	if err != nil {
		return errors.Wrapf(err, "parsing application file %q", appPath)
	}

	profileData, err := os.ReadFile(opts.profilePath)
	if err != nil {
		return errors.Wrapf(err, "reading profile file %q", opts.profilePath)
	}

	profile, err := oam.ParseClusterProfile(profileData)
	if err != nil {
		return errors.Wrapf(err, "parsing profile file %q", opts.profilePath)
	}

	transformer.SetCapabilityDefs(capDefs)
	transformer.SetStrictCapabilities(opts.strictCapabilities)
	transformer.SetWarningHandler(func(msg string) {
		_, _ = fmt.Fprintln(cmd.ErrOrStderr(), "warning:", msg)
	})

	evaluatedProfile, err := transformer.EvaluateProfile(profile)
	if err != nil {
		return errors.Wrapf(err, "evaluating profile %q", opts.profilePath)
	}

	ctx := oam.TransformContext{
		ClusterID:    opts.clusterID,
		Namespace:    opts.namespace,
		Capabilities: evaluatedProfile.Spec.Capabilities,
		Domain:       kurelDomain,
	}

	cluster, err := transformer.Transform(app, ctx)
	if err != nil {
		return errors.Wrap(err, "transforming application")
	}

	if err := rejectLayoutAugmenters(cluster.Node); err != nil {
		return err
	}

	objects, err := collectFromNode(cluster.Node)
	if err != nil {
		return errors.Wrap(err, "generating manifests")
	}

	if len(objects) == 0 {
		_, _ = fmt.Fprintln(cmd.ErrOrStderr(), "warning: no resources generated")
		return nil
	}

	yamlBytes, err := kio.EncodeObjectsToYAML(objects)
	if err != nil {
		return errors.Wrap(err, "encoding YAML output")
	}

	if opts.outputDir == "" {
		_, err = cmd.OutOrStdout().Write(yamlBytes)
		return errors.Wrap(err, "writing to stdout")
	}

	return writeOutputDir(opts.outputDir, app.Metadata.Name, yamlBytes)
}

// loadSuppliedValues merges --values file and --set flags into a single map.
// --set entries override --values entries for the same key.
func loadSuppliedValues(opts *buildOptions) (map[string]any, error) {
	supplied := make(map[string]any)

	if opts.valuesPath != "" {
		data, err := os.ReadFile(opts.valuesPath)
		if err != nil {
			return nil, errors.Wrapf(err, "reading values file %q", opts.valuesPath)
		}
		var raw any
		if err := yaml.Unmarshal(data, &raw); err != nil {
			return nil, errors.Wrapf(err, "parsing values file %q", opts.valuesPath)
		}
		if raw == nil {
			// Empty file — treat as empty map, not an error.
		} else {
			m, ok := raw.(map[string]any)
			if !ok {
				return nil, errors.Errorf("values file %q must be a YAML mapping (key: value pairs), got %T", opts.valuesPath, raw)
			}
			supplied = m
		}
	}

	// --set entries override --values.
	for _, kv := range opts.setValues {
		k, v, ok := strings.Cut(kv, "=")
		if !ok {
			return nil, errors.Errorf("--set %q: expected key=value format", kv)
		}
		supplied[k] = v // string; coercion happens in resolver based on schema type
	}

	return supplied, nil
}

// builtinComponentHandlers returns the built-in component handlers keyed by type.
// It is the single source of truth for component registration, shared by
// newBuiltinTransformer and the handler-schema parity test.
func builtinComponentHandlers() map[string]oam.ComponentHandler {
	return map[string]oam.ComponentHandler{
		"webservice":  &components.WebserviceHandler{},
		"worker":      &components.WorkerHandler{},
		"cronjob":     &components.CronjobHandler{},
		"daemonset":   &components.DaemonsetHandler{},
		"statefulset": &components.StatefulsetHandler{},
		"postgresql":  &components.PostgresqlHandler{},
		"helmchart":   &components.HelmchartHandler{},
		"passthrough": &components.PassthroughHandler{},
		"crd":         &components.CRDHandler{},
		"manifests":   &components.ManifestsHandler{},
		"oci":         &components.OCIHandler{},
	}
}

// builtinTraitHandlers returns the built-in trait handlers keyed by type. It is
// the single source of truth for trait registration, shared by
// newBuiltinTransformer and the handler-schema parity test.
func builtinTraitHandlers() map[string]oam.TraitHandler {
	return map[string]oam.TraitHandler{
		"ingress":              &traits.IngressHandler{},
		"httproute":            &traits.HTTPRouteHandler{},
		"certificate":          &traits.CertificateHandler{},
		"scaler":               &traits.ScalerHandler{},
		"pvc":                  &traits.PVCHandler{},
		"external-secret":      &traits.ExternalSecretHandler{},
		"configmap":            &traits.ConfigMapHandler{},
		"networkpolicy":        &traits.NetworkPolicyHandler{},
		"cilium-networkpolicy": &traits.CiliumNetworkPolicyHandler{},
		"volsync":              &traits.VolSyncHandler{},
		"rbac":                 &traits.RBACHandler{},
		"fluxcd-patches":       &traits.FluxCDPatchesHandler{},
		"fluxcd-postbuild":     &traits.PostBuildHandler{},
		"prune-protection":     &traits.PruneProtectionHandler{},
		"security-context":     &traits.SecurityContextHandler{},
	}
}

// builtinTraitLoweringRules returns the built-in trait-position lowering rules
// (oam.TraitLoweringRule, D5) keyed by the trait type they claim. It is the single
// source of truth for trait-lowering registration, shared by newBuiltinTransformer
// and the handler-schema parity/description tests — mirroring
// builtinComponentHandlers/builtinTraitHandlers above so a rule added here is
// covered by those tests exactly like a dispatchable handler is.
func builtinTraitLoweringRules() map[string]oam.TraitLoweringRule {
	return map[string]oam.TraitLoweringRule{
		"expose": traits.ExposeRule{},
	}
}

// newBuiltinTransformer creates a Transformer pre-loaded with all supported
// built-in component and trait handlers.
func newBuiltinTransformer() *oam.Transformer {
	t := oam.NewTransformer(builtinComponentHandlers(), nil)
	for name, h := range builtinTraitHandlers() {
		t.RegisterBuiltinTrait(name, h)
	}
	// "expose" is a trait-position lowering rule (D5), not a dispatchable handler:
	// it lowers into a terminal "ingress" or "httproute" trait for IngressHandler/
	// HTTPRouteHandler (registered above) to dispatch on the next fixpoint round.
	// It must not also appear in builtinTraitHandlers — RegisterTraitLowering
	// panics on that collision, which is the guard doing its job.
	// RegisterBuiltinTraitLowering (not RegisterTraitLowering) so EvaluateProfile
	// exempts "expose" from CapabilityDefinition schema application exactly like a
	// built-in TraitHandler is exempt.
	for _, r := range builtinTraitLoweringRules() {
		t.RegisterBuiltinTraitLowering(r)
	}
	return t
}

// rejectLayoutAugmenters walks the transform result and fails loudly if any
// component's config implements layout.LayoutAugmenter without also
// implementing oam.LayoutAugmentationCoverage returning true (e.g. a
// helmchart component with valuesMode: configMap, which needs a generated
// values ConfigMap emitted alongside it). collectFromNode/collectFromBundle
// below never construct or walk a layout.ManifestLayout, so an augmenter's
// resources are otherwise silently dropped — leaving, for the configMap
// case, a HelmRelease whose valuesFrom reference points at a ConfigMap that
// was never generated. Fail-closed default: a LayoutAugmenter that does not
// also implement LayoutAugmentationCoverage, or implements it but returns
// false, is rejected — only an explicit true (asserting that Generate's own
// output already covers everything AugmentLayout would add, e.g. a helmchart
// component with delivery: template) is let through.
func rejectLayoutAugmenters(node *stack.Node) error {
	if node == nil {
		return nil
	}
	if node.Bundle != nil {
		if err := rejectLayoutAugmentersInBundle(node.Bundle); err != nil {
			return err
		}
	}
	for _, child := range node.Children {
		if err := rejectLayoutAugmenters(child); err != nil {
			return err
		}
	}
	return nil
}

// rejectLayoutAugmentersInBundle mirrors collectFromBundle's umbrella/leaf
// traversal so every Application this build would actually generate is checked.
func rejectLayoutAugmentersInBundle(bundle *stack.Bundle) error {
	if bundle == nil {
		return nil
	}
	if bundle.IsUmbrella() {
		for _, child := range bundle.Children {
			if err := rejectLayoutAugmentersInBundle(child); err != nil {
				return err
			}
		}
		return nil
	}
	for _, app := range bundle.Applications {
		if _, ok := app.Config.(layout.LayoutAugmenter); !ok {
			continue
		}
		if cov, ok := app.Config.(oam.LayoutAugmentationCoverage); ok && cov.GenerateCoversAugmentLayout() {
			continue
		}
		return errors.Errorf(
			"kurel build: component %q needs layout-level resources (e.g. a helmchart valuesMode: configMap values ConfigMap) that this build path cannot generate — kurel build does not walk a layout.ManifestLayout, so those resources would be silently missing from the output; switch the component to a mode that does not need one",
			app.Name)
	}
	return nil
}

// collectFromNode walks the node tree and collects all generated client.Objects
// from leaf bundles.
func collectFromNode(node *stack.Node) ([]*client.Object, error) {
	if node == nil {
		return nil, nil
	}
	var all []*client.Object
	if node.Bundle != nil {
		objs, err := collectFromBundle(node.Bundle)
		if err != nil {
			return nil, err
		}
		all = append(all, objs...)
	}
	for _, child := range node.Children {
		objs, err := collectFromNode(child)
		if err != nil {
			return nil, err
		}
		all = append(all, objs...)
	}
	return all, nil
}

// collectFromBundle collects generated objects from a bundle.
// Umbrella bundles are recursed; leaf bundles are generated directly.
func collectFromBundle(bundle *stack.Bundle) ([]*client.Object, error) {
	if bundle == nil {
		return nil, nil
	}
	if bundle.IsUmbrella() {
		var all []*client.Object
		for _, child := range bundle.Children {
			objs, err := collectFromBundle(child)
			if err != nil {
				return nil, err
			}
			all = append(all, objs...)
		}
		return all, nil
	}
	return bundle.Generate()
}

func writeOutputDir(dir, appName string, data []byte) error {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return errors.Wrapf(err, "creating output directory %q", dir)
	}
	outPath := filepath.Join(dir, appName+".yaml")
	//nolint:gosec // G304: gosec's taint analysis flags appName as attacker-controlled,
	// but oam.validateWithExtraTypes rejects any Metadata.Name that fails
	// validation.IsDNS1123Subdomain before parsing succeeds, so it can never contain
	// '/' or '..'. dir is intentionally unrestricted: it is the --output flag the
	// invoking operator passed directly on their own command line (same trust
	// boundary as the process itself, like `go build -o`), not attacker-supplied
	// input crossing a privilege boundary, so the CLI Safety standard's
	// path-escape rule does not apply to it.
	if err := os.WriteFile(outPath, data, 0644); err != nil {
		return errors.Wrapf(err, "writing output file %q", outPath)
	}
	return nil
}
