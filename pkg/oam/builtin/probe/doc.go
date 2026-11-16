// Package probe holds the provenance report to what the library does, as observed
// in its probe documents (go-kure/launcher#1580). Each probe states, for every
// source switched off in turn, the fills the report shows and whether the document
// renders; three checks compare those statements with a real run.
//
// What a probe shows is what that document triggers. A fill no probe document
// triggers is not seen, so a clean run is an observation in these probes and
// never a claim about every fill.
package probe
