package kurel

import (
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/go-kure/kure/pkg/stack/fluxcd"
	"github.com/go-kure/kure/pkg/stack/layout"

	"github.com/go-kure/launcher/pkg/oam"
)

// One application has one root-node shape whether or not its components are
// ordered (go-kure/launcher#783): the tree a layout-walking consumer writes for
// it has the same top, with the application bundle's directory in the same
// place. The tests below write one document to disk flat and with one
// dependency rule and compare the two trees down to that directory.

// rootShapeComponents are the two components of the documents below.
const rootShapeComponents = `    - name: db
      type: webservice
      properties:
        image: postgres:17
    - name: web
      type: webservice
      properties:
        image: nginx:1.27
`

// rootShapeDependency orders web after db.
const rootShapeDependency = `  policies:
    - name: order
      type: dependency
      properties:
        rules:
          - component: web
            dependsOn: [db]
`

// rootShapeRules are the default layout rules under placement and clusterName.
func rootShapeRules(placement layout.FluxPlacement, clusterName string) layout.LayoutRules {
	rules := layout.DefaultLayoutRules()
	rules.FluxPlacement = placement
	rules.ClusterName = clusterName
	return rules
}

// writtenTree transforms doc, walks the result under rules, integrates Flux into
// the walked tree and writes it to disk. Every bundle that holds applications
// has a Source the integration generates (hookGroupCluster). It returns the
// directory written to and the top directory of the tree inside it.
func writtenTree(t *testing.T, doc string, rules layout.LayoutRules) (dir, top string) {
	t.Helper()
	cluster, err := hookGroupCluster(t, doc, oam.TransformContext{})
	if err != nil {
		t.Fatalf("transforming: %v", err)
	}
	root, err := layout.WalkCluster(cluster, rules)
	if err != nil {
		t.Fatalf("WalkCluster: %v", err)
	}
	// The walk returns the root node's layout, with no layout of a cluster
	// directory above it, and the application bundle's directory inside it.
	if nodes := root.OriginNodes(); len(nodes) != 1 || nodes[0] != cluster.Node {
		t.Errorf("WalkCluster returned a layout of nodes %v, want the root node's", nodes)
	}
	if unit := root.OriginUnit(); unit == nil || len(unit.OriginBundles()) != 1 || unit.OriginBundles()[0] != cluster.Node.Bundle {
		t.Errorf("the returned layout's OriginUnit = %v, want the application bundle's directory", unit)
	}
	if err := fluxcd.NewWorkflowEngine().GetLayoutIntegrator().IntegrateWithLayout(root, cluster, rules); err != nil {
		t.Fatalf("IntegrateWithLayout: %v", err)
	}
	top, err = layout.TopDirectory(cluster.Node, rules)
	if err != nil {
		t.Fatalf("TopDirectory: %v", err)
	}
	dir = t.TempDir()
	if err := root.WriteToDisk(dir); err != nil {
		t.Fatalf("WriteToDisk: %v", err)
	}
	return dir, top
}

// directoryEntries lists the entries of dir by name, a directory with a
// trailing slash.
func directoryEntries(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("reading %s: %v", dir, err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() {
			name += "/"
		}
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

// nodeKustomizations lists the files under dir that hold a node's own Flux
// Kustomization, which the integration names "<node path>-node".
func nodeKustomizations(t *testing.T, dir string) []string {
	t.Helper()
	var found []string
	err := filepath.WalkDir(dir, func(path string, e fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !e.IsDir() && strings.HasSuffix(e.Name(), "-node.yaml") {
			rel, relErr := filepath.Rel(dir, path)
			if relErr != nil {
				return relErr
			}
			found = append(found, rel)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking %s: %v", dir, err)
	}
	return found
}

func TestRootNodeShape_OrderingKeepsTheTopOfTheTree(t *testing.T) {
	const application = "shop"
	flatDoc := hookApp(application, rootShapeComponents, "")
	orderedDoc := hookApp(application, rootShapeComponents, rootShapeDependency)

	placements := []layout.FluxPlacement{layout.FluxIntegratedPerLayout, layout.FluxIntegratedPerBundle}
	clusterDirs := []struct{ clusterName, top string }{
		{"", "cluster"},
		{".", "."},
		{"prod", "prod"},
	}
	for _, placement := range placements {
		for _, cd := range clusterDirs {
			t.Run(string(placement)+"/ClusterName="+cd.clusterName, func(t *testing.T) {
				rules := rootShapeRules(placement, cd.clusterName)
				flatDir, flatTop := writtenTree(t, flatDoc, rules)
				orderedDir, orderedTop := writtenTree(t, orderedDoc, rules)

				if flatTop != cd.top || orderedTop != cd.top {
					t.Fatalf("top directory: flat %q, ordered %q, want %q for both", flatTop, orderedTop, cd.top)
				}

				// The top directory holds the same entries: its kustomization.yaml,
				// the application's Flux Kustomization, the generated Source and the
				// application bundle's directory, nothing else.
				flatEntries := directoryEntries(t, filepath.Join(flatDir, flatTop))
				orderedEntries := directoryEntries(t, filepath.Join(orderedDir, orderedTop))
				if !slices.Equal(flatEntries, orderedEntries) {
					t.Errorf("top directory entries differ:\n flat:    %v\n ordered: %v", flatEntries, orderedEntries)
				}
				want := []string{
					"flux-system-kustomization-" + application + ".yaml",
					"flux-system-ocirepository-artifact.yaml",
					"kustomization.yaml",
					application + "/",
				}
				if !slices.Equal(orderedEntries, want) {
					t.Errorf("ordered top directory entries = %v, want %v", orderedEntries, want)
				}

				// The application bundle's directory is a kustomize directory in
				// both; what it holds is where the two differ (the objects, or the
				// groups).
				for name, dir := range map[string]string{"flat": filepath.Join(flatDir, flatTop), "ordered": filepath.Join(orderedDir, orderedTop)} {
					if _, err := os.Stat(filepath.Join(dir, application, "kustomization.yaml")); err != nil {
						t.Errorf("%s: the application bundle's directory has no kustomization.yaml: %v", name, err)
					}
				}
				orderedBundle := directoryEntries(t, filepath.Join(orderedDir, orderedTop, application))
				for _, group := range []string{application + "-00/", application + "-01/"} {
					if !slices.Contains(orderedBundle, group) {
						t.Errorf("ordered: the application bundle's directory holds %v, want the group %s in it", orderedBundle, group)
					}
				}

				// No node has a Flux Kustomization of its own between the top of
				// the tree and the application's.
				for name, dir := range map[string]string{"flat": flatDir, "ordered": orderedDir} {
					if found := nodeKustomizations(t, dir); len(found) != 0 {
						t.Errorf("%s: node Kustomizations %v, want none", name, found)
					}
				}
			})
		}
	}
}

// rootShapeHelmComponents are two helm components on one chart repository: the
// helm rule generates one source for both and orders the releases after it, so
// the application bundle holds the source as its own application and one group.
const rootShapeHelmComponents = `    - name: api
      type: helm
      properties:
        chart: api
        version: 1.0.0
        source:
          url: https://charts.example.com
    - name: web
      type: helm
      properties:
        chart: web
        version: 2.0.0
        source:
          url: https://charts.example.com
`

// TestRootNodeShape_GeneratedSourcesInTheBundleDirectory writes an ordered
// application whose bundle has applications of its own, the generated sources,
// beside its groups: the sources are written into the application bundle's
// directory and the group into a directory inside it, below the same top as
// every other application.
func TestRootNodeShape_GeneratedSourcesInTheBundleDirectory(t *testing.T) {
	const application = "shop"
	doc := hookApp(application, rootShapeHelmComponents, "")

	placements := []layout.FluxPlacement{layout.FluxIntegratedPerLayout, layout.FluxIntegratedPerBundle}
	clusterDirs := []struct{ clusterName, top string }{
		{"", "cluster"},
		{".", "."},
		{"prod", "prod"},
	}
	for _, placement := range placements {
		for _, cd := range clusterDirs {
			t.Run(string(placement)+"/ClusterName="+cd.clusterName, func(t *testing.T) {
				dir, top := writtenTree(t, doc, rootShapeRules(placement, cd.clusterName))
				if top != cd.top {
					t.Fatalf("top directory = %q, want %q", top, cd.top)
				}
				entries := directoryEntries(t, filepath.Join(dir, top))
				want := []string{
					"flux-system-kustomization-" + application + ".yaml",
					"flux-system-ocirepository-artifact.yaml",
					"kustomization.yaml",
					application + "/",
				}
				if !slices.Equal(entries, want) {
					t.Errorf("top directory entries = %v, want %v", entries, want)
				}

				bundle := directoryEntries(t, filepath.Join(dir, top, application))
				if !slices.ContainsFunc(bundle, func(name string) bool { return strings.Contains(name, "-helmrepository-") }) {
					t.Errorf("the application bundle's directory holds %v, want the generated HelmRepository in it", bundle)
				}
				if !slices.Contains(bundle, application+"-00/") {
					t.Errorf("the application bundle's directory holds %v, want the group %s-00/ in it", bundle, application)
				}
				if found := nodeKustomizations(t, dir); len(found) != 0 {
					t.Errorf("node Kustomizations %v, want none", found)
				}
			})
		}
	}
}

// TestRootNodeShape_OrderedBuildsWithAGeneratedSource holds a refusal that went
// away with the named root node. Under per-layout placement with a cluster
// directory whose last segment is not the node's name, a named root node had a
// Flux Kustomization of its own that applied its directory, and every Source
// the integration generates was hosted in that directory. Where every Source
// that Kustomization could take was such a one (as here, where every SourceRef
// has a URL), it would have delivered its own Source, so the integration
// refused the ordered application while it built the flat one. With the root
// node unnamed there is no such Kustomization and the ordered application
// builds.
func TestRootNodeShape_OrderedBuildsWithAGeneratedSource(t *testing.T) {
	orderedDoc := hookApp("shop", rootShapeComponents, rootShapeDependency)
	for _, clusterName := range []string{".", "prod"} {
		t.Run("ClusterName="+clusterName, func(t *testing.T) {
			rules := rootShapeRules(layout.FluxIntegratedPerLayout, clusterName)
			cluster, err := hookGroupCluster(t, orderedDoc, oam.TransformContext{})
			if err != nil {
				t.Fatalf("transforming: %v", err)
			}
			if source := cluster.Node.Bundle.Children[0].SourceRef; source == nil || source.URL == "" {
				t.Fatalf("group SourceRef = %+v, want one the integration generates a Source from", source)
			}
			root, err := layout.WalkCluster(cluster, rules)
			if err != nil {
				t.Fatalf("WalkCluster: %v", err)
			}
			if err := fluxcd.NewWorkflowEngine().GetLayoutIntegrator().IntegrateWithLayout(root, cluster, rules); err != nil {
				t.Fatalf("IntegrateWithLayout refused the ordered application: %v", err)
			}
		})
	}
}
