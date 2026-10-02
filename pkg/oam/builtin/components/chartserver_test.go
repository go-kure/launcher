package components_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"fmt"
	"maps"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-kure/kure/pkg/stack"
)

// identityChart renders one ConfigMap named and namespaced from the release
// identity, and one with no metadata.namespace at all.
var identityChart = map[string]string{
	"identity.yaml": "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: {{ .Release.Name }}-cm\n  namespace: {{ .Release.Namespace }}\ndata:\n  k: v\n",
	"bare.yaml":     "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: bare\ndata:\n  k: v\n",
}

// renderedIdentity generates cfg and returns the identity ConfigMap's name and
// namespace, failing if the bare ConfigMap gained a namespace: nothing stamps
// metadata.namespace after the render.
func renderedIdentity(t *testing.T, cfg stack.ApplicationConfig) (name, namespace string) {
	t.Helper()
	objs, err := cfg.Generate(stack.NewApplication("app", "ignored", cfg))
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	found := false
	for _, o := range objs {
		if (*o).GetName() == "bare" {
			if ns := (*o).GetNamespace(); ns != "" {
				t.Errorf("bare ConfigMap namespace = %q, want none (no stamping)", ns)
			}
			continue
		}
		name, namespace, found = (*o).GetName(), (*o).GetNamespace(), true
	}
	if !found {
		t.Fatalf("identity ConfigMap not rendered; got %d objects", len(objs))
	}
	return name, namespace
}

// buildMinimalChartTar packages a minimal Helm chart (Chart.yaml plus the
// given extra files) as a gzipped tar. Duplicated locally from
// pkg/cmd/kurel/build_test.go's identically-named helper: different package,
// no shared test-helper package to import it from.
func buildMinimalChartTar(t *testing.T, name, version string, extraFiles map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	files := map[string]string{
		name + "/Chart.yaml": fmt.Sprintf("apiVersion: v2\nname: %s\nversion: %s\n", name, version),
	}
	maps.Copy(files, extraFiles)
	for path, content := range files {
		hdr := &tar.Header{Name: path, Mode: 0o600, Size: int64(len(content))}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatalf("tar header: %v", err)
		}
		if _, err := tw.Write([]byte(content)); err != nil {
			t.Fatalf("tar write: %v", err)
		}
	}
	tw.Close()
	gz.Close()
	return buf.Bytes()
}

// startMinimalHelmChartServer serves a minimal chart (name/version, with the
// given template files under templates/) over HTTP — the same fetch path a
// kurel build of a helmtemplate component takes — and returns the server's
// base URL. Closed via t.Cleanup.
func startMinimalHelmChartServer(t *testing.T, name, version string, templateFiles map[string]string) string {
	t.Helper()
	chartFiles := make(map[string]string, len(templateFiles))
	for path, content := range templateFiles {
		chartFiles[name+"/templates/"+path] = content
	}
	chartBuf := buildMinimalChartTar(t, name, version, chartFiles)
	tgzName := name + "-" + version + ".tgz"

	// Derive the chart URL from the request itself (r.Host), not a captured
	// server-URL variable — the handler runs on a goroutine the server starts
	// before NewServer returns, so a variable assigned by the caller after
	// NewServer returns would be read/written across goroutines with no
	// synchronization between them.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/index.yaml":
			fmt.Fprintf(w, "apiVersion: v1\nentries:\n  %s:\n  - name: %s\n    version: %s\n    urls:\n      - http://%s/%s\ngenerated: \"2024-01-01T00:00:00Z\"\n",
				name, name, version, r.Host, tgzName)
		case "/" + tgzName:
			_, _ = w.Write(chartBuf)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}
