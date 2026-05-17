package kurel

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
	"sigs.k8s.io/controller-runtime/pkg/client"

	kio "github.com/go-kure/kure/pkg/io"
	"github.com/go-kure/kure/pkg/stack"

	"github.com/go-kure/launcher/pkg/errors"
	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/components"
	"github.com/go-kure/launcher/pkg/oam/builtin/traits"
)

type buildOptions struct {
	profilePath string
	outputDir   string
	namespace   string
	clusterID   string
}

func newBuildCommand() *cobra.Command {
	opts := &buildOptions{}

	cmd := &cobra.Command{
		Use:   "build <app.yaml>",
		Short: "Build Kubernetes manifests from an OAM Application",
		Long: `Build generates static Kubernetes manifests from an OAM Application YAML file
and a platform ClusterProfile. Output is written to stdout (default) or a directory.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runBuild(cmd, args[0], opts)
		},
	}

	cmd.Flags().StringVar(&opts.profilePath, "profile", "", "path to ClusterProfile YAML (required)")
	cmd.Flags().StringVarP(&opts.outputDir, "output", "o", "", "output directory (default: stdout)")
	cmd.Flags().StringVarP(&opts.namespace, "namespace", "n", "", "namespace override")
	cmd.Flags().StringVar(&opts.clusterID, "cluster-id", "local", "cluster identifier")

	_ = cmd.MarkFlagRequired("profile")

	return cmd
}

func runBuild(cmd *cobra.Command, appPath string, opts *buildOptions) error {
	appData, err := os.ReadFile(appPath)
	if err != nil {
		return errors.Wrapf(err, "reading application file %q", appPath)
	}

	app, err := oam.Parse(appData)
	if err != nil {
		return errors.Wrapf(err, "parsing application file %q", appPath)
	}

	profileData, err := os.ReadFile(opts.profilePath)
	if err != nil {
		return errors.Wrapf(err, "reading profile file %q", opts.profilePath)
	}

	var profile oam.ClusterProfile
	if err := yaml.Unmarshal(profileData, &profile); err != nil {
		return errors.Wrapf(err, "parsing profile file %q", opts.profilePath)
	}

	transformer := newBuiltinTransformer()

	ctx := oam.TransformContext{
		ClusterID:    opts.clusterID,
		Namespace:    opts.namespace,
		Capabilities: profile.Spec.Capabilities,
	}

	cluster, err := transformer.Transform(app, ctx)
	if err != nil {
		return errors.Wrap(err, "transforming application")
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

// newBuiltinTransformer creates a Transformer pre-loaded with the supported
// built-in handlers for this vertical slice (webservice, expose, ingress).
func newBuiltinTransformer() *oam.Transformer {
	return oam.NewTransformer(
		map[string]oam.ComponentHandler{
			"webservice": &components.WebserviceHandler{},
		},
		map[string]oam.TraitHandler{
			"expose":  &traits.ExposeHandler{},
			"ingress": &traits.IngressHandler{}, //nolint:staticcheck
		},
	)
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
	if err := os.WriteFile(outPath, data, 0644); err != nil {
		return errors.Wrapf(err, "writing output file %q", outPath)
	}
	return nil
}
