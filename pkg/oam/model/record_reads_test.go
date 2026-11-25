package model_test

import (
	"strings"
	"testing"
)

// These tests pin the record form of a read (go-kure/launcher#1643): a part's output is a
// record, and a document reads a field of it. The part's own object is `parts.<id>.name`,
// and every other output is `parts.<id>.<key>.name`.

const recordReadHeader = `
    - id: creds
      name: creds
      type: ` + secretType + `
      properties: {}
    - id: app
      name: app
      type: ` + webType + `
      properties:
        image: ghcr.io/acme/app:v1
    - id: db
      name: db
      type: ` + pgType + `
      properties: {}
    - id: cfg
      name: cfg
      type: configmap@v1
      properties:
        data:
          x: "`

func recordReader(expr string) string { return recordReadHeader + expr + "\"\n" }

// TestRecordRead_ReadsTheNameOfARecord: the read of a part's own object, of one of its other
// outputs, with an indexed key, and inside text, each gives the name the build gives.
func TestRecordRead_ReadsTheNameOfARecord(t *testing.T) {
	for name, tc := range map[string]struct{ expr, want string }{
		"own object":          {`${ parts.creds.name }`, "creds"},
		"other output":        {`${ parts.app.service.name }`, "app"},
		"literal index":       {`${ parts.app['service'].name }`, "app"},
		"derived output":      {`${ parts.db.app_secret.name }`, "db-app"},
		"read-write Service":  {`${ parts.db.rw_service.name }`, "db-rw"},
		"the Service in text": {`host-${ parts.db.rw_service.name }`, "host-db-rw"},
		"inside text":         {`secret-${ parts.creds.name }`, "secret-creds"},
		"in an expression":    {`${ parts.creds.name + '-x' }`, "creds-x"},
		"the name of a name":  {`${ parts.app.deployment.name }`, "app"},
	} {
		t.Run(name, func(t *testing.T) {
			comps := expandOK(t, compileParts(t, recordReader(tc.expr)), nil)
			if got := prop(t, comps[len(comps)-1].Properties, "data", "x"); got != tc.want {
				t.Fatalf("data.x = %v, want %s", got, tc.want)
			}
		})
	}
}

// TestRecordRead_RefusesAReadThatIsNotARecordName: each failing read names the site, the part
// and what to read instead, and the old string form of a read is one of them.
func TestRecordRead_RefusesAReadThatIsNotARecordName(t *testing.T) {
	const site = "spec.parts[3].properties.data.x"
	for name, tc := range map[string]struct {
		expr  string
		wants []string
	}{
		"an undeclared key": {`${ parts.app.nope.name }`,
			[]string{site, "parts.app", "nope", "deployment", "service", "serviceaccount"}},
		"a read that stops at a record": {`${ parts.app.service }`,
			[]string{site, "parts.app.service", "record", ".name"}},
		"a read that stops at the part": {`${ parts.app }`,
			[]string{site, "parts.app", "outputs"}},
		"a field the record does not hold": {`${ parts.app.service.kind }`,
			[]string{site, "parts.app.service", "kind", "name"}},
		"the old string form of an output": {`${ parts.db.app_secret + '' }`,
			[]string{site, "parts.db.app_secret", "record", ".name"}},
		"the old key of a one-object part": {`${ parts.creds.object }`,
			[]string{site, "parts.creds", "object", "name"}},
		"a name of a part with no object": {`${ parts.db.name }`,
			[]string{site, "parts.db", "name", "app_secret", "cluster"}},
		"a read below the name": {`${ parts.creds.name.more }`,
			[]string{site, "parts.creds.name", "more"}},
		"an indexed undeclared key": {`${ parts.app['nope'].name }`,
			[]string{site, "parts.app", "nope"}},
	} {
		t.Run(name, func(t *testing.T) {
			c := compileParts(t, recordReader(tc.expr))
			comps, err := c.Expand(expandTransformer(t), expandScope, nil, nil)
			if err == nil {
				t.Fatalf("Expand accepted %s: %v", tc.expr, comps)
			}
			for _, want := range tc.wants {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q does not name %q", err, want)
				}
			}
			if comps != nil {
				t.Errorf("a failed Expand gave components: %v", comps)
			}
		})
	}
}
