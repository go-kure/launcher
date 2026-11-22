package components

import (
	"context"

	"github.com/go-kure/kure/pkg/stack/helm"

	"github.com/go-kure/launcher/pkg/oam"
)

// chartContent hands a policy's oam.ContentSource to kure's chart render as a
// helm.ContentSource (go-kure/launcher#1669). The two interfaces carry the same
// fields, so it only converts; it holds the render to no policy of its own.
//
// A stored chart is checked as a fetched one is. A chart fetch is checked once,
// before any request, on the host of source.url (ApplyPolicy), and the token
// service, a blob redirect or an archive host the fetch then touches are never
// compared with the allowed registries. So the hosts of a stored copy are not
// compared with them either. The one rule on them is the url half's: a copy
// that records no host is a miss, since a fetch always records the first and
// such a copy was written by somebody else.
type chartContent struct {
	src oam.ContentSource
}

// Lookup asks the source for key and answers a miss for a copy with no host.
func (c chartContent) Lookup(ctx context.Context, key helm.ContentKey) (helm.Content, bool, error) {
	got, ok, err := c.src.Lookup(ctx, oam.ContentKey(key))
	if err == nil && ok && len(got.Hosts) > 0 {
		return helm.Content(got), true, nil
	}
	return helm.Content{}, false, nil
}

// Store hands the source a chart kure fetched.
func (c chartContent) Store(ctx context.Context, key helm.ContentKey, got helm.Content) error {
	return c.src.Store(ctx, oam.ContentKey(key), oam.Content(got))
}
