package probe

import (
	"slices"
	"testing"

	"github.com/go-kure/launcher/pkg/oam"
)

// A place whose condition the component does not meet is none of a default's
// places, and a key left with no live place has none: it does not fall back to
// the authored path.
func TestPlacesOf_DropsPlacesNotLive(t *testing.T) {
	keys := map[string][]oam.LoweredKey{"version": {
		{Member: "member", Path: "image", When: []oam.LoweredWhen{{Key: "imageName", Values: []string{""}}}},
	}}
	for _, tc := range []struct {
		name  string
		props map[string]any
		want  []place
	}{
		{"condition met", map[string]any{}, []place{{"member", "", "image"}}},
		{"condition not met", map[string]any{"imageName": "other"}, []place{}},
	} {
		if got := placesOf(keys, tc.props, "version"); !slices.Equal(got, tc.want) {
			t.Errorf("%s: placesOf = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// Where the entries of a component name a member, a place is met by an entry of
// its own member only. Where none names one, the place is read without its
// member.
func TestReportedAt_MatchesTheMemberOfAGroup(t *testing.T) {
	for _, tc := range []struct {
		name   string
		member string
		want   bool
	}{
		{"an entry of another member", "other", false},
		{"an entry of the member", "member", true},
		{"an entry with no member", "", true},
	} {
		report := &oam.Provenance{Entries: []oam.PropertySource{
			{Component: "app", Member: tc.member, Path: "image", Source: oam.Source{Kind: oam.SourceRendered}},
		}}
		if got := reportedAt(report, "app", []place{{"member", "", "image"}}); got != tc.want {
			t.Errorf("%s: reportedAt = %v, want %v", tc.name, got, tc.want)
		}
	}
}
