package components

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"slices"

	barmanapi "github.com/cloudnative-pg/barman-cloud/pkg/api"
	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	machineryapi "github.com/cloudnative-pg/machinery/pkg/api"
	barmanv1 "github.com/cloudnative-pg/plugin-barman-cloud/api/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"

	"github.com/go-kure/launcher/pkg/errors"
	"github.com/go-kure/launcher/pkg/oam"
)

// LowerComponent validates comp as a postgresql component and emits the CNPG
// kind components that build the same objects (see PostgresqlRule).
func (r PostgresqlRule) LowerComponent(comp *oam.Component, lctx oam.LoweringContext) (oam.LoweringResult, error) {
	c, err := r.Parse(comp)
	if err != nil {
		return oam.LoweringResult{}, err
	}

	spec, err := c.clusterSpec()
	if err != nil {
		return oam.LoweringResult{}, err
	}
	clusterProps, err := specProperties(spec)
	if err != nil {
		return oam.LoweringResult{}, err
	}
	// ClusterSpec.Instances is always encoded. Left out when replicas was not
	// authored, so the Cluster's own default (1, as postgresql's) and the
	// policy default apply exactly as they applied to replicas.
	if !c.explicitReplicas {
		delete(clusterProps, "instances")
	}

	// The defaults trait goes first, so it is the innermost: it sets the
	// Cluster's policy-dependent values before any authored trait sees the
	// Cluster, as postgresql's Generate did.
	traits := append([]oam.Trait{{Type: postgresqlDefaultsTrait, Properties: map[string]any{}}}, comp.Traits...)
	out := []oam.Component{{
		Name:        comp.Name,
		Type:        "cnpg-cluster",
		Properties:  clusterProps,
		Traits:      traits,
		Annotations: maps.Clone(comp.Annotations),
	}}

	// The ObjectStore is named like the Cluster, so it is emitted under the
	// component's own name: a member of the Cluster's same-name sibling group.
	if c.ObjectStore != nil {
		props, err := specProperties(c.objectStoreSpec())
		if err != nil {
			return oam.LoweringResult{}, err
		}
		out = append(out, oam.Component{
			Name:        comp.Name,
			Type:        "cnpg-objectstore",
			Properties:  props,
			Annotations: maps.Clone(comp.Annotations),
		})
	}

	var dependents []string
	poolerName := ""
	if c.PoolerEnabled {
		name, err := postgresqlChildName(lctx, comp.Name, "pooler", "pooler")
		if err != nil {
			return oam.LoweringResult{}, err
		}
		props, err := specProperties(c.poolerSpec(comp.Name))
		if err != nil {
			return oam.LoweringResult{}, err
		}
		out = append(out, oam.Component{Name: name, Type: "cnpg-pooler", Properties: props, Annotations: maps.Clone(comp.Annotations)})
		dependents = append(dependents, name)
		poolerName = name
	}
	for i, db := range c.Databases {
		var name string
		if db.Name == "pooler" && poolerName != "" {
			// A Database named like the Pooler is a different object of the
			// same name, as postgresql emitted it: it joins the Pooler's
			// same-name sibling group, which generates at the Pooler's position.
			name = poolerName
		} else {
			if name, err = postgresqlChildName(lctx, comp.Name, db.Name, fmt.Sprintf("databases[%d] %q", i, db.Name)); err != nil {
				return oam.LoweringResult{}, err
			}
			dependents = append(dependents, name)
		}
		props, err := specProperties(c.databaseSpec(comp.Name, db))
		if err != nil {
			return oam.LoweringResult{}, err
		}
		out = append(out, oam.Component{Name: name, Type: "cnpg-database", Properties: props, Annotations: maps.Clone(comp.Annotations)})
	}

	policies := postgresqlMemberPolicies(lctx, comp.Name, dependents)
	split := slices.ContainsFunc(policies, func(p oam.ApplicationPolicy) bool { return p.Type == "dependency" })
	forwardMemberTraits(out, comp.Traits, split)
	return oam.LoweringResult{Components: out, Policies: policies}, nil
}

// postgresqlObjectTraits are the authored trait types that decorate every
// object the component generates, and postgresqlBundleTraits those that act on
// the component's Flux Kustomization. Each applied to every object or to the
// one bundle of a postgresql component; the rule keeps that by forwarding them
// to the members it emits beside the Cluster.
var (
	postgresqlObjectTraits = map[string]bool{"prune-protection": true, "force-replace": true}
	postgresqlBundleTraits = map[string]bool{"fluxcd-patches": true, "fluxcd-postbuild": true}
)

// forwardMemberTraits gives each component after the Cluster (out[0]) the
// authored traits that covered its objects when postgresql generated them
// itself: the object-decorating ones always, since each member's traits apply
// to its own objects only; the bundle ones only when split is set (the rule
// emitted a dependency policy, so the transformer lays out one bundle per
// component) and only to the first member of each name, since a later
// same-name sibling shares that member's bundle. Without split every
// component of the tier shares the Cluster's bundle, which already carries
// them.
//
// Each is a by-value copy of the authored element with the same properties
// map, which the engine recognises as forwarded rather than built by the rule
// (oam's isForwardedTrait): it keeps its authored classification and checks.
func forwardMemberTraits(out []oam.Component, authored []oam.Trait, split bool) {
	seen := map[string]bool{out[0].Name: true}
	for i := 1; i < len(out); i++ {
		ownBundle := split && !seen[out[i].Name]
		seen[out[i].Name] = true
		for _, t := range authored {
			if postgresqlObjectTraits[t.Type] || (ownBundle && postgresqlBundleTraits[t.Type]) {
				out[i].Traits = append(out[i].Traits, t)
			}
		}
	}
}

// postgresqlChildName allocates the name of a component the rule emits beside
// the Cluster, `<cluster>-<suffix>`, which is also the name of the object it
// generates. The allocator refuses a name that is not a DNS-1123 subdomain (the
// API server refuses it as an object name) and one another rule already
// generated. A component of the document already named so is refused here, by
// both names: the two components would otherwise share a name, which a
// document cannot hold. what names the postgresql property the name came from.
func postgresqlChildName(lctx oam.LoweringContext, cluster, suffix, what string) (string, error) {
	name, err := lctx.Namer.Name(cluster, suffix, lctx.Origin)
	if err != nil {
		return "", errors.Wrapf(err, "%s", what)
	}
	if lctx.Document != nil {
		for _, other := range lctx.Document.Spec.Components {
			if other.Name == name {
				return "", errors.Errorf("%s: generates component %q, which is already the name of component %q (type %q) in the document; rename one of them", what, name, other.Name, other.Type)
			}
		}
	}
	return name, nil
}

// postgresqlMemberPolicies returns the policies that keep the members (the
// components the rule emits beside the Cluster: the Pooler and the Databases,
// one per name) where postgresql's own objects were. A policy names a component;
// what the document's policies say about the postgresql component by its name
// covered all of its objects, and now reaches only the Cluster's.
//
//   - A placement of the component is repeated for every member, so the members
//     stay in the Cluster's tier (and, without a dependency policy, in its
//     bundle).
//   - Under a dependency policy, the members wait for the Cluster, and every
//     component the document makes wait for the postgresql component waits for
//     the members too (postgresqlDependencyRules).
//
// The names are implementation details no author refers to, so each takes the
// first of <base>, <base>-1, … that no policy of the document, and none this
// call already named, uses, rather than refusing the document. They are not
// claimed through lctx.Namer: a policy generates no object, and its name must
// be unique only among policies (the settled document's validation refuses a
// duplicate). Sharing the Namer's namespace with generated component names
// would refuse a valid document whose database, or another component's,
// generates the same name, depending on which rule ran first. Two calls cannot
// name the same policy either: each base is a component's own name followed by
// "-dependencies" or "-placement".
func postgresqlMemberPolicies(lctx oam.LoweringContext, cluster string, members []string) []oam.ApplicationPolicy {
	if len(members) == 0 || lctx.Document == nil {
		return nil
	}
	used := map[string]bool{}
	for _, p := range lctx.Document.Spec.Policies {
		used[p.Name] = true
	}
	free := func(base string) string {
		name := base
		for i := 1; used[name]; i++ {
			name = fmt.Sprintf("%s-%d", base, i)
		}
		used[name] = true
		return name
	}
	var out []oam.ApplicationPolicy
	if rules := postgresqlDependencyRules(lctx.Document, cluster, members); rules != nil {
		out = append(out, oam.ApplicationPolicy{Name: free(cluster + "-dependencies"), Type: "dependency", Properties: map[string]any{"rules": rules}})
	}
	for _, tier := range postgresqlPlacementTiers(lctx.Document, cluster) {
		for _, m := range members {
			out = append(out, oam.ApplicationPolicy{Name: free(m + "-placement"), Type: "placement", Properties: map[string]any{"component": m, "tier": tier}})
		}
	}
	return out
}

// postgresqlDependencyRules returns the dependency rules for the members, or
// nil.
//
// They are emitted only when the document already orders its components with a
// dependency policy that has a rule. That is when the transformer lays out one
// bundle per component and orders the bundles by those edges; the members are
// then bundles of their own, which gets them no automatic edge from or to the
// Cluster's. Without such a policy every component of a tier shares one bundle,
// so the objects apply together, as postgresql's did. Emitting edges
// unconditionally would break that: any dependency edge switches the whole
// document to the per-component layout (PolicyResult.HasDependencies), so a
// postgresql component with a pooler would change the layout of a document that
// never asked for ordering.
//
// Each member waits for the Cluster. Each component an authored rule makes wait
// for the postgresql component also waits for every member, as it waited for
// the one bundle of all of postgresql's objects.
func postgresqlDependencyRules(doc *oam.Application, cluster string, members []string) []any {
	if !documentOrdersComponents(doc) {
		return nil
	}
	rules := make([]any, 0, len(members))
	for _, m := range members {
		rules = append(rules, map[string]any{"component": m, "dependsOn": []any{cluster}})
	}
	seen := map[string]bool{}
	for _, p := range doc.Spec.Policies {
		if p.Type != "dependency" {
			continue
		}
		authored, _ := p.Properties["rules"].([]any)
		for _, r := range authored {
			rule, _ := r.(map[string]any)
			component, _ := rule["component"].(string)
			dependsOn, _ := rule["dependsOn"].([]any)
			if component == "" || seen[component] || !slices.Contains(dependsOn, any(cluster)) {
				continue
			}
			seen[component] = true
			onMembers := make([]any, 0, len(members))
			for _, m := range members {
				onMembers = append(onMembers, m)
			}
			rules = append(rules, map[string]any{"component": component, "dependsOn": onMembers})
		}
	}
	return rules
}

// postgresqlPlacementTiers returns the tiers the document's placement policies
// put the postgresql component in, each once, in policy order. A malformed
// placement is skipped: the placement handler refuses the authored policy
// itself.
func postgresqlPlacementTiers(doc *oam.Application, cluster string) []string {
	var tiers []string
	for _, p := range doc.Spec.Policies {
		if p.Type != "placement" {
			continue
		}
		component, _ := p.Properties["component"].(string)
		tier, _ := p.Properties["tier"].(string)
		if component == cluster && tier != "" && !slices.Contains(tiers, tier) {
			tiers = append(tiers, tier)
		}
	}
	return tiers
}

// documentOrdersComponents reports whether doc carries a dependency policy with
// at least one rule.
func documentOrdersComponents(doc *oam.Application) bool {
	if doc == nil {
		return false
	}
	for _, p := range doc.Spec.Policies {
		if p.Type != "dependency" {
			continue
		}
		if rules, ok := p.Properties["rules"].([]any); ok && len(rules) > 0 {
			return true
		}
	}
	return false
}

// specProperties encodes an upstream spec as the property map of the kind
// component that decodes it again: the JSON encoding the kind's strict decode
// reads (jsonProperties), so the component builds the spec it was given.
// Numbers are decoded exactly: an integer stays an int64, so one above 2^53
// (an int64 field such as wal.maxParallel) is not rounded through a float64
// into a value the kind's decode then refuses as overflowing.
func specProperties(spec any) (map[string]any, error) {
	data, err := json.Marshal(spec)
	if err != nil {
		return nil, errors.Wrap(err, "internal: encode the emitted spec")
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var props map[string]any
	if err := dec.Decode(&props); err != nil {
		return nil, errors.Wrap(err, "internal: decode the emitted spec")
	}
	if err := exactNumbers(props); err != nil {
		return nil, err
	}
	return props, nil
}

// exactNumbers replaces each json.Number in v, in place, by an int64 when it is
// an integer and a float64 otherwise, the types the property validators read.
func exactNumbers(v any) error {
	convert := func(n json.Number) (any, error) {
		if i, err := n.Int64(); err == nil {
			return i, nil
		}
		f, err := n.Float64()
		if err != nil {
			return nil, errors.Wrapf(err, "internal: decode the emitted number %s", n)
		}
		return f, nil
	}
	switch t := v.(type) {
	case map[string]any:
		for k, e := range t {
			if n, ok := e.(json.Number); ok {
				c, err := convert(n)
				if err != nil {
					return err
				}
				t[k] = c
				continue
			}
			if err := exactNumbers(e); err != nil {
				return err
			}
		}
	case []any:
		for i, e := range t {
			if n, ok := e.(json.Number); ok {
				c, err := convert(n)
				if err != nil {
					return err
				}
				t[i] = c
				continue
			}
			if err := exactNumbers(e); err != nil {
				return err
			}
		}
	}
	return nil
}

// clusterSpec is the Cluster spec postgresql wrote, without the values the
// environment policy decides: spec.enablePDB (set by the defaults trait) and,
// when storageSize was not authored, spec.storage.size (the policy default, or
// the defaults trait's 1Gi fallback). spec.instances is the authored replicas
// count, or 0 when replicas was not authored (LowerComponent then leaves it
// out).
//
// The checks postgresql made on the Cluster it generated are made here, with
// the same text, on the authored values; the Cluster's kind repeats them on the
// values after the policy.
func (c *PostgresqlConfig) clusterSpec() (cnpgv1.ClusterSpec, error) {
	// ClusterSpec.Instances has Minimum=1.
	if c.explicitReplicas && c.Replicas < 1 {
		return cnpgv1.ClusterSpec{}, errors.Errorf("replicas: must be >= 1, got %d", c.Replicas)
	}
	var storage cnpgv1.StorageConfiguration
	if c.explicitStorageSize {
		// An authored "" built a Cluster with no storage size, which
		// CloudNativePG's webhook refuses ("Size not configured").
		if c.StorageSize == "" {
			return cnpgv1.ClusterSpec{}, errors.New("storageSize: must not be empty; omit it to take the policy default or 1Gi")
		}
		// CloudNativePG's webhook parses only the size, so a zero or negative
		// one is admitted and its claims then fail the API server's positive
		// storage-request check (see BuildPVC).
		q, err := resource.ParseQuantity(c.StorageSize)
		if err != nil {
			return cnpgv1.ClusterSpec{}, errors.Errorf("storageSize: invalid quantity %q: %w", c.StorageSize, err)
		}
		if q.Sign() <= 0 {
			return cnpgv1.ClusterSpec{}, errors.Errorf("storageSize: quantity must be positive, got %q", c.StorageSize)
		}
		storage.Size = c.StorageSize
	}

	imageName := c.ImageName
	if imageName == "" {
		imageName = fmt.Sprintf("ghcr.io/cloudnative-pg/postgresql:%s", c.Version)
	}

	// primaryUpdateStrategy was injected by kure's retired config-struct layer
	// and is kept explicit so the emitted Cluster is unchanged.
	spec := cnpgv1.ClusterSpec{
		ImageName:             imageName,
		PrimaryUpdateStrategy: cnpgv1.PrimaryUpdateStrategyUnsupervised,
		StorageConfiguration:  storage,
	}
	if c.explicitReplicas {
		spec.Instances = int(c.Replicas)
	}

	// Left nil when both maps are empty: a non-nil empty block renders
	// `inheritedMetadata: {}`.
	if len(c.InheritedLabels) > 0 || len(c.InheritedAnnotations) > 0 {
		spec.InheritedMetadata = &cnpgv1.EmbeddedObjectMetadata{
			Labels:      maps.Clone(c.InheritedLabels),
			Annotations: maps.Clone(c.InheritedAnnotations),
		}
	}

	spec.Resources = corev1.ResourceRequirements{
		Requests: cnpgResourceList(c.Resources.Requests),
		Limits:   cnpgResourceList(c.Resources.Limits),
	}

	if c.BackupRetentionPolicy != "" || c.BackupDestinationPath != "" {
		bos := &barmanapi.BarmanObjectStoreConfiguration{
			DestinationPath: c.BackupDestinationPath,
			EndpointURL:     c.BackupEndpointURL,
		}
		if c.BackupSecretName != "" {
			bos.AWS = s3Credentials(c.BackupSecretName)
		}
		spec.Backup = &cnpgv1.BackupConfiguration{
			RetentionPolicy:   c.BackupRetentionPolicy,
			BarmanObjectStore: bos,
		}
	}

	if c.MonitoringEnabled {
		mon := &cnpgv1.MonitoringConfiguration{EnablePodMonitor: true} //nolint:staticcheck // SA1019: EnablePodMonitor is still the only upstream opt-in for operator-created PodMonitors
		for _, cq := range c.MonitoringCustomQueries {
			mon.CustomQueriesConfigMap = append(mon.CustomQueriesConfigMap, cnpgv1.ConfigMapKeySelector{
				LocalObjectReference: machineryapi.LocalObjectReference{Name: cq.Name},
				Key:                  cq.Key,
			})
		}
		spec.Monitoring = mon
	}

	// Parse refuses both sources together; recovery still wins here to match
	// the retired builder should that guard ever move.
	switch {
	case c.BootstrapRecoverySource != "":
		spec.Bootstrap = &cnpgv1.BootstrapConfiguration{
			Recovery: &cnpgv1.BootstrapRecovery{Source: c.BootstrapRecoverySource},
		}
	case c.BootstrapPgBasebackupSource != "":
		spec.Bootstrap = &cnpgv1.BootstrapConfiguration{
			PgBaseBackup: &cnpgv1.BootstrapPgBaseBackup{Source: c.BootstrapPgBasebackupSource},
		}
	}

	if len(c.ExternalClusters) > 0 {
		ecs := make([]cnpgv1.ExternalCluster, 0, len(c.ExternalClusters))
		for _, ec := range c.ExternalClusters {
			ext := cnpgv1.ExternalCluster{
				Name:                 ec.Name,
				ConnectionParameters: ec.ConnectionParameters,
			}
			if ec.BarmanObjectStore != nil {
				bos, err := toBarmanObjectStore(ec.BarmanObjectStore)
				if err != nil {
					return cnpgv1.ClusterSpec{}, errors.Wrapf(err, "external cluster %q", ec.Name)
				}
				ext.BarmanObjectStore = bos
			}
			ecs = append(ecs, ext)
		}
		spec.ExternalClusters = ecs
	}

	if len(c.PostgresqlParameters) > 0 {
		spec.PostgresConfiguration.Parameters = c.PostgresqlParameters
	}
	if c.SynchronousMethod != "" {
		sync := &cnpgv1.SynchronousReplicaConfiguration{
			Method: cnpgv1.SynchronousReplicaConfigurationMethod(c.SynchronousMethod),
			Number: int(c.SynchronousNumber),
		}
		if c.SynchronousDataDurability != "" {
			sync.DataDurability = cnpgv1.DataDurabilityLevel(c.SynchronousDataDurability)
		}
		spec.PostgresConfiguration.Synchronous = sync
	}

	// An objectStore component archives WAL through the barman-cloud plugin, pointed
	// at the ObjectStore the rule emits under the same name. The plugin reads the
	// store from barmanObjectName and the server name from serverName (defaulting
	// to the Cluster name); the ObjectStore CRD forbids a serverName of its own.
	if c.ObjectStore != nil {
		isWALArchiver := true
		params := map[string]string{"barmanObjectName": c.Name}
		if c.ObjectStore.ServerName != "" {
			params["serverName"] = c.ObjectStore.ServerName
		}
		spec.Plugins = []cnpgv1.PluginConfiguration{{
			Name:          barmanCloudPluginName,
			IsWALArchiver: &isWALArchiver,
			Parameters:    params,
		}}
	}

	if c.AffinityEnabled {
		// Always written, false included: CNPG reads a nil enablePodAntiAffinity
		// as enabled.
		enablePAA := c.AffinityEnablePodAntiAffinity
		spec.Affinity = cnpgv1.AffinityConfiguration{
			EnablePodAntiAffinity: &enablePAA,
			TopologyKey:           c.AffinityTopologyKey,
			PodAntiAffinityType:   c.AffinityPodAntiAffinityType,
			NodeSelector:          c.AffinityNodeSelector,
		}
	}

	for i, role := range c.ManagedRoles {
		// The parse-time refusal is repeated for a config built without
		// Parse: the Cluster would drop the 0 (go-kure/launcher#659).
		if role.ConnectionLimit != nil && *role.ConnectionLimit == 0 {
			return cnpgv1.ClusterSpec{}, postgresqlZeroConnectionLimit(fmt.Sprintf("managedRoles[%d]", i))
		}
		rc := cnpgv1.RoleConfiguration{
			Name:        role.Name,
			Comment:     role.Comment,
			Login:       role.Login,
			Superuser:   role.Superuser,
			CreateDB:    role.CreateDB,
			CreateRole:  role.CreateRole,
			Replication: role.Replication,
			Inherit:     role.Inherit,
			InRoles:     role.InRoles,
		}
		// A nil limit stays 0 (omitempty) so the operator applies its own default.
		if role.ConnectionLimit != nil {
			rc.ConnectionLimit = *role.ConnectionLimit
		}
		// Only "absent" is written; "present" and unset leave the field for the
		// operator to default.
		if role.Ensure == "absent" {
			rc.Ensure = cnpgv1.EnsureAbsent
		}
		if role.PasswordSecret != "" {
			rc.PasswordSecret = &cnpgv1.LocalObjectReference{Name: role.PasswordSecret}
		}
		if spec.Managed == nil {
			spec.Managed = &cnpgv1.ManagedConfiguration{}
		}
		spec.Managed.Roles = append(spec.Managed.Roles, rc)
	}

	return spec, nil
}

// poolerSpec is the Pooler spec postgresql wrote for cluster's pooler.
func (c *PostgresqlConfig) poolerSpec(cluster string) cnpgv1.PoolerSpec {
	// pgbouncer is required upstream (no omitempty), so it is always set, empty
	// when nothing is authored.
	pgBouncer := &cnpgv1.PgBouncerSpec{}
	if c.PoolerPoolMode != "" {
		pgBouncer.PoolMode = cnpgv1.PgBouncerPoolMode(c.PoolerPoolMode)
	}
	if len(c.PoolerParameters) > 0 {
		pgBouncer.Parameters = c.PoolerParameters
	}

	// Anything other than "ro" is written as rw, the value launcher has always
	// emitted for an unset type.
	poolerType := cnpgv1.PoolerTypeRW
	if c.PoolerType == "ro" {
		poolerType = cnpgv1.PoolerTypeRO
	}

	spec := cnpgv1.PoolerSpec{
		Cluster:   cnpgv1.LocalObjectReference{Name: cluster},
		Type:      poolerType,
		PgBouncer: pgBouncer,
	}
	// A non-positive count is omitted so the operator default applies.
	if c.PoolerInstances > 0 {
		instances := c.PoolerInstances
		spec.Instances = &instances
	}
	return spec
}

// objectStoreSpec is the ObjectStore spec postgresql wrote for objectStore.
func (c *PostgresqlConfig) objectStoreSpec() barmanv1.ObjectStoreSpec {
	spec := barmanv1.ObjectStoreSpec{
		Configuration: barmanapi.BarmanObjectStoreConfiguration{
			DestinationPath: c.ObjectStore.DestinationPath,
			EndpointURL:     c.ObjectStore.EndpointURL,
		},
		RetentionPolicy: c.ObjectStore.RetentionPolicy,
	}
	if c.ObjectStore.SecretName != "" {
		spec.Configuration.AWS = s3Credentials(c.ObjectStore.SecretName)
	}
	return spec
}

// databaseSpec is the Database spec postgresql wrote for db in cluster.
func (c *PostgresqlConfig) databaseSpec(cluster string, db DatabaseEntry) cnpgv1.DatabaseSpec {
	spec := cnpgv1.DatabaseSpec{
		ClusterRef: corev1.LocalObjectReference{Name: cluster},
		Name:       db.Name,
		Owner:      db.Owner,
	}
	// Only the non-default values are written; "present"/"retain" and unset leave
	// the field for the operator to default.
	if db.Ensure == "absent" {
		spec.Ensure = cnpgv1.EnsureAbsent
	}
	if db.ReclaimPolicy == "delete" {
		spec.ReclaimPolicy = cnpgv1.DatabaseReclaimDelete
	}
	// An extension's ensure is always written — present unless authored absent —
	// matching what launcher has always emitted.
	for _, ext := range db.Extensions {
		ensure := cnpgv1.EnsurePresent
		if ext.Ensure == "absent" {
			ensure = cnpgv1.EnsureAbsent
		}
		spec.Extensions = append(spec.Extensions, cnpgv1.ExtensionSpec{
			DatabaseObjectSpec: cnpgv1.DatabaseObjectSpec{Name: ext.Name, Ensure: ensure},
		})
	}
	return spec
}
