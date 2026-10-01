package components

import "testing"

// TestPostgresqlConfig_ClusterSpecRefusesZeroConnectionLimit pins the rule's
// repeat of the go-kure/launcher#659 parse refusal for a config built without
// Parse: the Cluster spec would drop the 0 and the operator would apply its
// unlimited default, so clusterSpec refuses it with the parse's text.
func TestPostgresqlConfig_ClusterSpecRefusesZeroConnectionLimit(t *testing.T) {
	zero := int64(0)
	c := &PostgresqlConfig{Version: "16", Replicas: 1, ManagedRoles: []ManagedRoleConfig{
		{Name: "reader", Login: true},
		{Name: "app_user", Login: true, ConnectionLimit: &zero},
	}}
	_, err := c.clusterSpec()
	const want = "managedRoles[1].connectionLimit: 0 cannot be carried by the CloudNativePG API types (the field is omitted when zero, so the operator would apply its default -1, no limit); set login: false to keep the role from connecting"
	if err == nil || err.Error() != want {
		t.Errorf("err = %v, want %q", err, want)
	}

	limit := int64(-1)
	c.ManagedRoles[1].ConnectionLimit = &limit
	spec, err := c.clusterSpec()
	if err != nil {
		t.Fatalf("connectionLimit -1: %v", err)
	}
	if got := spec.Managed.Roles[1].ConnectionLimit; got != -1 {
		t.Errorf("connectionLimit -1: spec carries %d", got)
	}
}
