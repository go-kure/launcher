package oam

import "github.com/go-kure/launcher/pkg/errors"

// ModelEmit is what TransformContext.EmitHook is told about one component that a
// configuration-defined model emitted (go-kure/launcher#1261).
type ModelEmit struct {
	// Model is the registry key of the model: its name and canonical version.
	Model string
	// Part is the id of the part of the model that gave the component. A forEach part
	// gives one component for each element, all with the same Part.
	Part string
	// Type is the component type as the model wrote it, before the next lowering round
	// resolves it.
	Type string
	// Name is the name the build gave the component.
	Name string
	// Properties are the evaluated properties of the component. It is a copy: a change
	// to it does not reach the build.
	Properties map[string]any
}

// EmitModelComponent tells the EmitHook of the run about a component that the model
// rule of model emitted for part, and returns the hook's error wrapped with the model and
// the part. It does nothing where no hook is set. Only a model rule calls it: a Go rule
// is never reported to the hook.
func (l LoweringContext) EmitModelComponent(model, part string, comp Component) error {
	if l.Namer == nil || l.Namer.emit == nil {
		return nil
	}
	err := l.Namer.emit(ModelEmit{
		Model:      model,
		Part:       part,
		Type:       comp.Type,
		Name:       comp.Name,
		Properties: copyPropertyMap(comp.Properties),
	})
	if err != nil {
		return errors.Wrapf(err, "emit hook: %s: part %q", model, part)
	}
	return nil
}
