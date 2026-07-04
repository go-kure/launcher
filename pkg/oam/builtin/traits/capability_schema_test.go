package traits

import "testing"

// Capability-injected fields must NOT be marked user-required in a handler's
// PropertySchema: they are supplied by capability rendering (CapabilityRequired),
// not by the OAM author, and are validated in ValidateAndApplyDefaults. Marking
// them required makes a consumer's schema preflight reject every valid use of the
// trait (regression guard for the expose.controllerType / certificate.issuerRef
// bug shipped in v0.1.0-alpha.9).
func TestCapabilityInjectedFieldsNotUserRequired(t *testing.T) {
	exposeSchema := (&ExposeHandler{}).PropertySchema()
	if exposeSchema["controllerType"].Required {
		t.Error("expose: controllerType is capability-injected and must not be user-required")
	}

	certSchema := (&CertificateHandler{}).PropertySchema()
	issuerRef := certSchema["issuerRef"]
	if issuerRef.Required {
		t.Error("certificate: issuerRef is capability-injected and must not be user-required")
	}
	// If a user does supply issuerRef, its name stays required so `issuerRef: {}`
	// fails in preflight rather than later in parseProperties.
	if !issuerRef.Properties["name"].Required {
		t.Error("certificate: issuerRef.name must remain required when issuerRef is supplied")
	}
	// Genuinely user-facing fields stay required.
	for _, k := range []string{"secretName", "dnsNames"} {
		if !certSchema[k].Required {
			t.Errorf("certificate: %s is user-facing and must stay required", k)
		}
	}
}
