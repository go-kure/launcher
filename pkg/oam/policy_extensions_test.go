package oam

import "testing"

// consumerDeliverySettings is a consumer's own policy result: a type launcher has
// never seen.
type consumerDeliverySettings struct {
	Interval string
	Prune    bool
}

// consumerDeliveryPolicyHandler is a policy handler a consumer registers for its
// own policy type. It records its result under its own key in
// PolicyResult.Extensions.
type consumerDeliveryPolicyHandler struct {
	key    string
	stored *consumerDeliverySettings
}

func (h *consumerDeliveryPolicyHandler) CanHandle(policyType string) bool {
	return policyType == "consumer-delivery"
}

func (h *consumerDeliveryPolicyHandler) Apply(policy *ApplicationPolicy, _ []string, result *PolicyResult) error {
	interval, _ := policy.Properties["interval"].(string)
	prune, _ := policy.Properties["prune"].(bool)
	h.stored = &consumerDeliverySettings{Interval: interval, Prune: prune}
	result.Extensions[h.key] = h.stored
	return nil
}

// TestTransformWithPolicy_ConsumerPolicyResult: a consumer-registered policy
// handler returns data of its own type through PolicyResult.Extensions, and
// TransformWithPolicy hands it back untouched (go-kure/launcher#781).
func TestTransformWithPolicy_ConsumerPolicyResult(t *testing.T) {
	const key = "consumer.example/delivery"
	handler := &consumerDeliveryPolicyHandler{key: key}
	tr := NewTransformer(
		map[string]ComponentHandler{"webservice": &pipelineComponentHandler{typ: "webservice"}},
		nil,
	)
	tr.RegisterPolicy("consumer-delivery", handler)

	app := makeApp("myapp", makeComponent("web", "webservice"))
	app.Spec.Policies = []ApplicationPolicy{{
		Name:       "delivery",
		Type:       "consumer-delivery",
		Properties: map[string]any{"interval": "5m", "prune": true},
	}}

	_, result, err := tr.TransformWithPolicy(app, TransformContext{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Extensions) != 1 {
		t.Fatalf("Extensions = %v, want exactly the handler's entry", result.Extensions)
	}
	got, ok := result.Extensions[key].(*consumerDeliverySettings)
	if !ok {
		t.Fatalf("Extensions[%q] is %T, want the handler's own type", key, result.Extensions[key])
	}
	if got != handler.stored {
		t.Error("Extensions holds a different value than the one the handler stored")
	}
	if want := (consumerDeliverySettings{Interval: "5m", Prune: true}); *got != want {
		t.Errorf("Extensions[%q] = %+v, want %+v", key, *got, want)
	}
}

// TestTransformWithPolicy_NoExtensionsWithoutAHandlerWritingOne: launcher itself
// writes nothing to Extensions.
func TestTransformWithPolicy_NoExtensionsWithoutAHandlerWritingOne(t *testing.T) {
	tr := NewTransformer(
		map[string]ComponentHandler{
			"webservice": &pipelineComponentHandler{typ: "webservice"},
			"postgresql": &pipelineComponentHandler{typ: "postgresql"},
		},
		nil,
	)
	tr.RegisterPolicy("dependency", &depWritingPolicyHandler{from: "web", to: "db"})

	app := makeApp("myapp", makeComponent("web", "webservice"), makeComponent("db", "postgresql"))
	app.Spec.Policies = []ApplicationPolicy{{Name: "order", Type: "dependency"}}

	_, result, err := tr.TransformWithPolicy(app, TransformContext{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Extensions) != 0 {
		t.Errorf("Extensions = %v, want empty", result.Extensions)
	}
}
