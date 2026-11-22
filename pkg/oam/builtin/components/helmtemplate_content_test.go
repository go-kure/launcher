package components_test

import (
	"context"
	"errors"
	"net/url"
	"sync"
	"testing"

	"github.com/go-kure/launcher/pkg/oam"
)

// chartCopies is a ContentSource over a map of chart name to copy. It records
// every key it is asked for and every copy it receives.
type chartCopies struct {
	mu        sync.Mutex
	copies    map[string]oam.Content
	lookupErr error
	looked    []oam.ContentKey
	storedKey []oam.ContentKey
	stored    []oam.Content
}

func (c *chartCopies) Lookup(_ context.Context, key oam.ContentKey) (oam.Content, bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.looked = append(c.looked, key)
	cp, ok := c.copies[key.Chart]
	return cp, ok, c.lookupErr
}

func (c *chartCopies) Store(_ context.Context, key oam.ContentKey, cp oam.Content) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.storedKey = append(c.storedKey, key)
	c.stored = append(c.stored, cp)
	return nil
}

// chartPolicy is a Policy that allows the given registries and carries a
// ContentSource.
type chartPolicy struct {
	*oam.NoopPolicy
	allowed []string
	src     oam.ContentSource
}

// newChartPolicy returns a chartPolicy over a NoopPolicy.
func newChartPolicy(allowed []string, src oam.ContentSource) chartPolicy {
	return chartPolicy{NoopPolicy: &oam.NoopPolicy{}, allowed: allowed, src: src}
}

func (p chartPolicy) AllowedRegistries() []string { return p.allowed }

func (p chartPolicy) ContentSource() oam.ContentSource { return p.src }

// chartHost is the host:port of a test server's URL.
func chartHost(t *testing.T, srvURL string) string {
	t.Helper()
	u, err := url.Parse(srvURL)
	if err != nil {
		t.Fatalf("parse %q: %v", srvURL, err)
	}
	return u.Host
}

// fetchedChart renders the chart once from its server through a source that
// holds nothing, and returns what the source was handed with the objects.
func fetchedChart(t *testing.T, srvURL string) (oam.ContentKey, oam.Content, []string) {
	t.Helper()
	src := &chartCopies{}
	objs, err := htTransform(srvURL, newChartPolicy([]string{chartHost(t, srvURL)}, src))
	if err != nil {
		t.Fatalf("first render: %v", err)
	}
	if len(src.stored) != 1 {
		t.Fatalf("the source was handed %d copies after a fetch, want 1", len(src.stored))
	}
	names := make([]string, 0, len(objs))
	for _, o := range objs {
		names = append(names, o.GetName())
	}
	return src.storedKey[0], src.stored[0], names
}

var contentChartFiles = map[string]string{
	"settings.yaml": "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: settings\ndata:\n  k: v\n",
}

// A fetch hands the source the chart with its key as kure built it and the
// host it went to.
func TestHelmTemplate_ContentSourceStoreGetsTheFetchedChart(t *testing.T) {
	srvURL, requests := startCountingHelmChartServer(t, "testchart", "0.1.0", contentChartFiles)
	key, cp, _ := fetchedChart(t, srvURL)

	if key.Kind != "chart" || key.URL != srvURL || key.Chart != "testchart" || key.Version != "0.1.0" || key.Digest != "" {
		t.Errorf("key = %+v, want the HTTP chart as kure built it (repository URL %q)", key, srvURL)
	}
	if len(cp.Data) == 0 || len(cp.Manifest) != 0 {
		t.Errorf("copy has %d archive bytes and %d manifest bytes, want an archive and no manifest", len(cp.Data), len(cp.Manifest))
	}
	if want := chartHost(t, srvURL); len(cp.Hosts) != 1 || cp.Hosts[0] != want {
		t.Errorf("hosts = %v, want [%s]", cp.Hosts, want)
	}
	if requests.Load() == 0 {
		t.Errorf("the first render made no request")
	}
}

// A source that holds the chart serves it with no request, and the objects
// equal the fetched ones.
func TestHelmTemplate_ContentSourceServesAStoredChartWithoutARequest(t *testing.T) {
	srvURL, requests := startCountingHelmChartServer(t, "testchart", "0.1.0", contentChartFiles)
	_, cp, fetched := fetchedChart(t, srvURL)
	before := requests.Load()

	src := &chartCopies{copies: map[string]oam.Content{"testchart": cp}}
	objs, err := htTransform(srvURL, newChartPolicy([]string{chartHost(t, srvURL)}, src))
	if err != nil {
		t.Fatalf("render from the source: %v", err)
	}
	if got := requests.Load() - before; got != 0 {
		t.Errorf("a stored chart cost %d requests, want 0", got)
	}
	if len(src.stored) != 0 {
		t.Errorf("a copy the source served was handed back to Store")
	}
	if len(objs) != len(fetched) {
		t.Fatalf("served render has %d objects, fetched had %d", len(objs), len(fetched))
	}
	for i, o := range objs {
		if o.GetName() != fetched[i] {
			t.Errorf("object %d = %q, want %q", i, o.GetName(), fetched[i])
		}
	}
}

// A hit is checked as a fetch is checked: on the host of source.url, once. The
// hosts a copy records are not compared with the allowed registries, so a copy
// recording a token service and a blob host off the list is served.
func TestHelmTemplate_ContentSourceChecksNoRecordedHost(t *testing.T) {
	srvURL, requests := startCountingHelmChartServer(t, "testchart", "0.1.0", contentChartFiles)
	_, cp, _ := fetchedChart(t, srvURL)
	cp.Hosts = []string{chartHost(t, srvURL), "token.example.test", "blobs.example.test:8443"}
	before := requests.Load()

	src := &chartCopies{copies: map[string]oam.Content{"testchart": cp}}
	if _, err := htTransform(srvURL, newChartPolicy([]string{chartHost(t, srvURL)}, src)); err != nil {
		t.Fatalf("render from a copy recording other hosts: %v", err)
	}
	if got := requests.Load() - before; got != 0 {
		t.Errorf("the copy was not served: %d requests", got)
	}
}

// A copy with no recorded host is a miss, as it is for a url: a fetch always
// records the first host, so such a copy was written by somebody else.
func TestHelmTemplate_ContentSourceCopyWithNoHostsIsAMiss(t *testing.T) {
	srvURL, requests := startCountingHelmChartServer(t, "testchart", "0.1.0", contentChartFiles)
	_, cp, _ := fetchedChart(t, srvURL)
	cp.Hosts = nil
	before := requests.Load()

	src := &chartCopies{copies: map[string]oam.Content{"testchart": cp}}
	if _, err := htTransform(srvURL, newChartPolicy([]string{chartHost(t, srvURL)}, src)); err != nil {
		t.Fatalf("render: %v", err)
	}
	if requests.Load()-before == 0 {
		t.Errorf("a copy with no hosts was served, want a fetch")
	}
	if len(src.stored) != 1 {
		t.Errorf("the fetch after the miss was not handed to Store")
	}
}

// A source.url on a refused host fails before the source is asked, as a fetch
// does.
func TestHelmTemplate_ContentSourceNotAskedForARefusedHost(t *testing.T) {
	srvURL, _ := startCountingHelmChartServer(t, "testchart", "0.1.0", contentChartFiles)
	_, cp, _ := fetchedChart(t, srvURL)

	src := &chartCopies{copies: map[string]oam.Content{"testchart": cp}}
	_, err := htTransform(srvURL, newChartPolicy([]string{"registry.example.test"}, src))
	if err == nil {
		t.Fatal("render with source.url on a refused host succeeded")
	}
	if len(src.looked) != 0 {
		t.Errorf("the source was asked %d times for a refused host, want 0", len(src.looked))
	}
}

// A Lookup that fails is a miss, even beside a copy: the chart is fetched.
func TestHelmTemplate_ContentSourceLookupErrorIsAMiss(t *testing.T) {
	srvURL, requests := startCountingHelmChartServer(t, "testchart", "0.1.0", contentChartFiles)
	_, cp, _ := fetchedChart(t, srvURL)
	before := requests.Load()

	src := &chartCopies{copies: map[string]oam.Content{"testchart": cp}, lookupErr: errors.New("source down")}
	if _, err := htTransform(srvURL, newChartPolicy([]string{chartHost(t, srvURL)}, src)); err != nil {
		t.Fatalf("render: %v", err)
	}
	if requests.Load()-before == 0 {
		t.Errorf("a failing Lookup served its copy, want a fetch")
	}
}
