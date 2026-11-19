package kurel

// loweringOnlyTypes are the registered component types no document authors: a
// role rule lowers into one, the engine needs a handler for it, and the parser
// refuses it on purpose (go-kure/launcher#1629). The tests that build a
// document of every registered type leave them out.
var loweringOnlyTypes = map[string]bool{"role-deployment": true}
