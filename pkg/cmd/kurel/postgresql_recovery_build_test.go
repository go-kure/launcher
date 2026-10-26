package kurel

import (
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/go-kure/launcher/pkg/oam"
)

// postgresqlRecoveryApp is a postgresql component recovered from an inline
// object store, so that it emits the recovery ObjectStore beside its Cluster,
// with a pooler, followed by traitsYAML (indented for the component's traits
// list), and a webservice, then extraComponentsYAML (more components, or
// empty) and policiesYAML (for spec.policies, or empty).
func postgresqlRecoveryApp(traitsYAML, extraComponentsYAML, policiesYAML string) string {
	return `apiVersion: launcher.gokure.dev/v1alpha1
kind: Application
metadata:
  name: shop
  namespace: shop
spec:
  components:
    - name: db
      type: postgresql
      properties:
        pooler:
          enabled: true
        recovery:
          source:
            serverName: origin
            objectStore:
              destinationPath: s3://bucket/origin/
              s3Credentials:
                inheritFromIAMRole: true
      traits:
` + traitsYAML + `    - name: api
      type: webservice
      properties:
        image: ghcr.io/example/api:v1.0.0
        port: 9090
` + extraComponentsYAML + policiesYAML
}

// postgresqlRecoveryPlugin is a webservice, plugin, for a document to make db
// wait for, or to place in a tier of its own.
const postgresqlRecoveryPlugin = `    - name: plugin
      type: webservice
      properties:
        image: ghcr.io/example/plugin:v1.0.0
        port: 9443
`

// postgresqlRecoveryUpstream is a second postgresql component, upstream, with
// a pooler, for a document to make db wait for.
const postgresqlRecoveryUpstream = `    - name: upstream
      type: postgresql
      properties:
        pooler:
          enabled: true
`

// TestBuild_PostgresqlRecoveryStoreOrdering: the recovering Cluster reads its
// source store before it can become ready, so the store never waits for the
// Cluster. Where the document orders its components, the Cluster waits for
// the store: the store's group comes first, and what waited for db waits for
// the Cluster and its members after it. Where the document does not, nothing
// is ordered and the store is in the one bundle with the Cluster.
func TestBuild_PostgresqlRecoveryStoreOrdering(t *testing.T) {
	const traits = "        - type: prune-protection\n"
	t.Run("an unordered document", func(t *testing.T) {
		bundles := postgresqlBundles(t, postgresqlRecoveryApp(traits, "", ""))
		if got := slices.Sorted(maps.Keys(bundles)); !slices.Equal(got, []string{"shop"}) {
			t.Fatalf("bundles = %v, want the one bundle shop", got)
		}
		var apps []string
		for _, a := range bundles["shop"].Applications {
			apps = append(apps, a.Name)
		}
		if want := []string{"db", "db-recovery-source", "db-pooler", "api"}; !slices.Equal(apps, want) {
			t.Errorf("applications = %v, want %v", apps, want)
		}
	})
	t.Run("an ordered document", func(t *testing.T) {
		cluster, _, err := transformWithBuiltins(t, postgresqlRecoveryApp(traits, "", postgresqlOrderPolicy))
		if err != nil {
			t.Fatalf("transforming: %v", err)
		}
		want := []string{"shop-00: db-recovery-source", "shop-01: db", "shop-02: db-pooler", "shop-03: api"}
		if got := groupNames(t, cluster); !slices.Equal(got, want) {
			t.Errorf("groups = %v, want %v", got, want)
		}
	})
	t.Run("an ordered document with a placement", func(t *testing.T) {
		cluster, _, err := transformWithBuiltins(t, postgresqlRecoveryApp(traits, "", postgresqlOrderPolicy+postgresqlPlacementPolicy))
		if err != nil {
			t.Fatalf("transforming: %v", err)
		}
		want := []string{"shop-00: db-recovery-source", "shop-01: db", "shop-02: db-pooler", "shop-03: api"}
		if got := groupNames(t, cluster); !slices.Equal(got, want) {
			t.Errorf("groups = %v, want %v", got, want)
		}
	})
	// What the author makes db wait for, all of db's objects wait for, the
	// store included, and the Cluster still waits for the store.
	t.Run("db waits for another component", func(t *testing.T) {
		const policies = postgresqlOrderPolicy + "          - component: db\n            dependsOn: [plugin]\n"
		cluster, _, err := transformWithBuiltins(t, postgresqlRecoveryApp(traits, postgresqlRecoveryPlugin, policies))
		if err != nil {
			t.Fatalf("transforming: %v", err)
		}
		want := []string{"shop-00: plugin", "shop-01: db-recovery-source", "shop-02: db", "shop-03: db-pooler", "shop-04: api"}
		if got := groupNames(t, cluster); !slices.Equal(got, want) {
			t.Errorf("groups = %v, want %v", got, want)
		}
	})
	// What db waits for, the store waits for, including the rules another
	// component's lowering generates: making db wait for upstream makes it
	// wait for upstream's Pooler too (go-kure/launcher#1041).
	t.Run("db waits for a postgresql component with a pooler", func(t *testing.T) {
		const policies = postgresqlOrderPolicy + "          - component: db\n            dependsOn: [upstream]\n"
		cluster, _, err := transformWithBuiltins(t, postgresqlRecoveryApp(traits, postgresqlRecoveryUpstream, policies))
		if err != nil {
			t.Fatalf("transforming: %v", err)
		}
		want := []string{"shop-00: upstream", "shop-01: upstream-pooler", "shop-02: db-recovery-source", "shop-03: db", "shop-04: db-pooler", "shop-05: api"}
		if got := groupNames(t, cluster); !slices.Equal(got, want) {
			t.Errorf("groups = %v, want %v", got, want)
		}
	})
	// No edge is lost: whatever db's Cluster follows, authored or generated, the
	// store follows too, and the Cluster still follows the store.
	t.Run("the store follows everything db follows", func(t *testing.T) {
		const policies = postgresqlOrderPolicy + "          - component: db\n            dependsOn: [plugin, upstream]\n"
		cluster, _, err := transformWithBuiltins(t, postgresqlRecoveryApp(traits, postgresqlRecoveryPlugin+postgresqlRecoveryUpstream, policies))
		if err != nil {
			t.Fatalf("transforming: %v", err)
		}
		level := map[string]int{}
		for i, group := range cluster.Node.Bundle.Children {
			for _, a := range group.Applications {
				level[a.Name] = i
			}
		}
		for _, before := range []string{"plugin", "upstream", "upstream-pooler"} {
			if level[before] >= level["db"] {
				t.Fatalf("db is in group %d, not after %s in group %d", level["db"], before, level[before])
			}
			if level[before] >= level["db-recovery-source"] {
				t.Errorf("db-recovery-source is in group %d, not after %s in group %d", level["db-recovery-source"], before, level[before])
			}
		}
		if level["db-recovery-source"] >= level["db"] {
			t.Errorf("db is in group %d, not after db-recovery-source in group %d", level["db"], level["db-recovery-source"])
		}
	})
	// A rule making db wait for its own store says what the order already
	// does: the store does not wait for itself.
	t.Run("db waits for its own store", func(t *testing.T) {
		const policies = postgresqlOrderPolicy + "          - component: db\n            dependsOn: [db-recovery-source]\n"
		cluster, _, err := transformWithBuiltins(t, postgresqlRecoveryApp(traits, "", policies))
		if err != nil {
			t.Fatalf("transforming: %v", err)
		}
		want := []string{"shop-00: db-recovery-source", "shop-01: db", "shop-02: db-pooler", "shop-03: api"}
		if got := groupNames(t, cluster); !slices.Equal(got, want) {
			t.Errorf("groups = %v, want %v", got, want)
		}
	})
	// A placement of db places the store too: with plugin in the tier before,
	// the store waits for it, which no dependency rule says.
	t.Run("a placement after another tier", func(t *testing.T) {
		const infra = "    - name: where-plugin\n      type: placement\n      properties:\n        component: plugin\n        tier: infra\n"
		cluster, _, err := transformWithBuiltins(t, postgresqlRecoveryApp(traits, postgresqlRecoveryPlugin, postgresqlOrderPolicy+postgresqlPlacementPolicy+infra))
		if err != nil {
			t.Fatalf("transforming: %v", err)
		}
		want := []string{"shop-infra: plugin", "shop-01: db-recovery-source", "shop-02: db", "shop-03: db-pooler", "shop-04: api"}
		if got := groupNames(t, cluster); !slices.Equal(got, want) {
			t.Errorf("groups = %v, want %v", got, want)
		}
	})
}

// TestBuild_PostgresqlRecoveryRefusedAtTheDocumentCheck: `recovery` is
// declared key by key, so kurel build refuses at the document check, and the
// inline store gives the reasons backup.objectStore gives, under its own path.
func TestBuild_PostgresqlRecoveryRefusedAtTheDocumentCheck(t *testing.T) {
	component := func(props string) string {
		return helmNamesApp("    - name: db\n      type: postgresql\n      properties:\n" + props)
	}
	const source = "        recovery:\n          source:\n            serverName: origin\n"
	for _, tc := range []struct {
		name, props, want string
	}{
		{
			"an unknown recovery key",
			source + "          backupName: x\n",
			`unsupported field "backupName"`,
		},
		{
			"the inline store's serverName",
			source + "            objectStore:\n              destinationPath: s3://bucket/origin/\n              serverName: origin\n              s3Credentials:\n                inheritFromIAMRole: true\n",
			"recovery.source.objectStore.serverName: not allowed (the ObjectStore CRD forbids it); use recovery.source.serverName",
		},
		{
			"an Azure inline store",
			source + "            objectStore:\n              destinationPath: https://acct.blob.core.windows.net/c\n              azureCredentials:\n                inheritFromAzureAD: true\n",
			"recovery.source.objectStore.azureCredentials: not supported (S3-compatible storage only; the cnpg-objectstore kind takes Azure credentials)",
		},
		{
			"an inline store without a path",
			source + "            objectStore:\n              s3Credentials:\n                inheritFromIAMRole: true\n",
			"recovery.source.objectStore.destinationPath: required (the object store path the cluster is recovered from)",
		},
		{
			"a target of the wrong type",
			source + "            objectStoreName: origin\n          target:\n            immediate: \"true\"\n",
			"recovery.target.immediate",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, out, err := buildDocs(t, component(tc.props))
			if err == nil {
				t.Fatalf("kurel build accepted the document; output: %s", out)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err = %v\nwant one containing %q", err, tc.want)
			}
		})
	}
}

// TestBuild_PostgresqlFormerRecoveryKeysAreRefused: the former
// bootstrap.recovery and the in-tree externalClusters[].barmanObjectStore are
// refused naming their replacement (go-kure/launcher#1001).
func TestBuild_PostgresqlFormerRecoveryKeysAreRefused(t *testing.T) {
	for name, tc := range map[string]struct {
		properties string
		want       string
	}{
		"bootstrap.recovery": {
			"        bootstrap:\n          recovery:\n            source: origin\n",
			"bootstrap.recovery: moved to recovery",
		},
		"externalClusters[].barmanObjectStore": {
			"        externalClusters:\n          - name: origin\n            barmanObjectStore:\n              destinationPath: s3://bucket/origin/\n",
			"externalClusters[0].barmanObjectStore: removed (in-tree Barman Cloud, gone in CloudNativePG 1.31); use recovery.source",
		},
	} {
		t.Run(name, func(t *testing.T) {
			app := `apiVersion: launcher.gokure.dev/v1alpha1
kind: Application
metadata:
  name: shop
  namespace: shop
spec:
  components:
    - name: db
      type: postgresql
      properties:
` + tc.properties
			_, out, err := buildDocs(t, app)
			if err == nil || !strings.Contains(err.Error()+out, tc.want) {
				t.Errorf("err = %v, output %q; want %q", err, out, tc.want)
			}
		})
	}
}

// TestBuild_PostgresqlRecoveryArchiveCheckUnderANamingHook: another
// component's ObjectStore is matched by its component name only where no
// Naming hook is set. Under a hook that renames it, objectStoreName "old"
// names no object of the document (a store outside it), so the archive check
// leaves it to the plugin; a name the author wrote is still matched.
func TestBuild_PostgresqlRecoveryArchiveCheckUnderANamingHook(t *testing.T) {
	doc := func(oldBackupExtra string) string {
		const store = "objectStore:\n            destinationPath: s3://bucket/pg/\n            s3Credentials:\n              inheritFromIAMRole: true\n"
		return `apiVersion: launcher.gokure.dev/v1alpha1
kind: Application
metadata:
  name: shop
  namespace: shop
spec:
  components:
    - name: old
      type: postgresql
      properties:
        backup:
          ` + store + oldBackupExtra + `    - name: db
      type: postgresql
      properties:
        backup:
          ` + store + `        recovery:
          source:
            serverName: db
            objectStoreName: old
`
	}
	hook := renameBy(map[string]string{string(oam.NameRolePostgresqlObjectStore) + " old": "renamed-store"})
	transform := func(t *testing.T, appYAML string, naming func(oam.NameRequest) (string, bool)) error {
		t.Helper()
		transformer := newBuiltinTransformer()
		app, err := oam.ParseWithExtraTypes([]byte(appYAML), nil, transformer.LowerableTypes())
		if err != nil {
			t.Fatalf("parsing: %v", err)
		}
		_, err = transformer.Transform(app, oam.TransformContext{Domain: kurelDomain, Naming: naming})
		return err
	}
	const refused = "recovery.source: the cluster would archive its WAL into the folder it is recovered from"
	t.Run("no hook", func(t *testing.T) {
		if err := transform(t, doc(""), nil); err == nil || !strings.Contains(err.Error(), refused) {
			t.Errorf("err = %v, want one containing %q", err, refused)
		}
	})
	t.Run("a hook renames the store", func(t *testing.T) {
		if err := transform(t, doc(""), hook); err != nil {
			t.Errorf("refused: %v", err)
		}
	})
	t.Run("a hook, and the author named the store", func(t *testing.T) {
		err := transform(t, doc("          objectStoreObjectName: old\n"), hook)
		if err == nil || !strings.Contains(err.Error(), refused) {
			t.Errorf("err = %v, want one containing %q", err, refused)
		}
	})
}
