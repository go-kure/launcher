package oam_test

import (
	"strings"
	"testing"

	"github.com/go-kure/launcher/pkg/errors"
	"github.com/go-kure/launcher/pkg/oam"
)

// rawNamedKind is an out-of-package RawDocumentLoweringRule of the shape a consumer
// writes: it derives its one generated child name through lctx.Namer, with no nil
// fallback, exactly as the RawDocumentLoweringRule contract asks.
type rawNamedKind struct{}

type rawNamedDoc struct{ name string }

func (rawNamedKind) Kind() string { return "RawNamedKind" }

func (rawNamedKind) DecodeDocument(raw []byte) (any, error) {
	return &rawNamedDoc{name: string(raw)}, nil
}

func (rawNamedKind) LowerDocument(doc any, lctx oam.LoweringContext) (oam.LoweringResult, error) {
	d, ok := doc.(*rawNamedDoc)
	if !ok {
		return oam.LoweringResult{}, errors.Errorf("rawNamedKind: unexpected decoded type %T", doc)
	}
	name, err := lctx.Namer.Name(d.name, "svc", lctx.Origin)
	if err != nil {
		return oam.LoweringResult{}, err
	}
	return oam.LoweringResult{Documents: []oam.Application{{Metadata: oam.Metadata{Name: name}}}}, nil
}

var _ oam.RawDocumentLoweringRule = rawNamedKind{}

// TestNewNameAllocator_DrivesOutOfPackageRawRule is go-kure/launcher#380: a consumer
// driving its own RawDocumentLoweringRule outside this package (a unit test, a
// pre-pass, a fixture generator) must be able to build a LoweringContext whose Namer
// allocates names the way LowerRaws does — one shared reservation table, collisions
// across documents detected, scoped by namespace.
func TestNewNameAllocator_DrivesOutOfPackageRawRule(t *testing.T) {
	rule := rawNamedKind{}
	namer := oam.NewNameAllocator()
	lower := func(docName, namespace string) (oam.LoweringResult, error) {
		decoded, err := rule.DecodeDocument([]byte("app"))
		if err != nil {
			t.Fatalf("DecodeDocument: %v", err)
		}
		return rule.LowerDocument(decoded, oam.LoweringContext{
			Origin: oam.Origin{Document: docName, DocumentKind: rule.Kind(), Namespace: namespace},
			Namer:  namer,
		})
	}

	res, err := lower("first", "team-a")
	if err != nil {
		t.Fatalf("first document: unexpected error: %v", err)
	}
	if got := res.Documents[0].Metadata.Name; got != "app-svc" {
		t.Fatalf("first document: generated name = %q, want %q", got, "app-svc")
	}

	// A second document in the same namespace generating the same child name must
	// collide against the shared reservation table, naming both origins.
	_, err = lower("second", "team-a")
	if err == nil {
		t.Fatal("second document in the same namespace: expected a generated-name collision, got nil")
	}
	if !strings.Contains(err.Error(), `"app-svc" collides`) || !strings.Contains(err.Error(), "first") || !strings.Contains(err.Error(), "second") {
		t.Fatalf("collision error does not name the name and both origins: %v", err)
	}

	// The same child name in a different namespace is not a collision (reservations
	// are namespace-scoped, as in LowerRaws).
	if _, err := lower("third", "team-b"); err != nil {
		t.Fatalf("document in another namespace: unexpected error: %v", err)
	}
}

// TestNewNameAllocator_Independent pins that each call returns a fresh reservation
// table: a harness that builds one allocator per run must not inherit another run's
// claims.
func TestNewNameAllocator_Independent(t *testing.T) {
	origin := oam.Origin{Document: "doc", DocumentKind: "RawNamedKind"}
	if _, err := oam.NewNameAllocator().Name("app", "svc", origin); err != nil {
		t.Fatalf("first allocator: %v", err)
	}
	other := oam.Origin{Document: "other", DocumentKind: "RawNamedKind"}
	if _, err := oam.NewNameAllocator().Name("app", "svc", other); err != nil {
		t.Fatalf("second allocator saw the first allocator's claim: %v", err)
	}
}
