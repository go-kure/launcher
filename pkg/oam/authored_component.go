package oam

// RegisterAuthoredComponent is RegisterComponent for a component type a document
// may name and launcher does not ship (go-kure/launcher#102 spike). Plain
// RegisterComponent stays what it was: a handler that only a lowering rule's
// output may name, such as role-deployment, must not become authorable by being
// registered.
func (t *Transformer) RegisterAuthoredComponent(typeName string, h ComponentHandler) {
	t.RegisterComponent(typeName, h)
}
