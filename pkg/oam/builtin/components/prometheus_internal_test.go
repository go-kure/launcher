package components

import (
	"maps"
	"reflect"
	"slices"
	"strings"
	"testing"

	monitoringv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	corev1 "k8s.io/api/core/v1"
)

// TestPrometheus_LogFileAtOperatorMount names as queryLogFile each path the
// operator mounts a volume at in the prometheus container, of a spec that makes
// it mount every one of them, and a file under each writable one. Every mount
// but the web configuration file is of a directory, which Prometheus cannot
// open as a file: config_out and the log-file volume are emptyDirs
// (pkg/prometheus/common.go:252-268 and :334-347 at prometheus-operator
// v0.94.1), the data volume an emptyDir or a claim's subPath directory holding
// the TSDB (common.go:277-283, server/statefulset.go:454), the rest Secret,
// ConfigMap or projected volumes mounted whole (common.go:269-332,
// server/statefulset.go:528-534, pkg/webconfig/tls_credentials.go:130-134 and
// :171-175); the web configuration is a key of its Secret mounted by subPath
// (pkg/webconfig/config.go:272-278). A mount the model gains fails here until
// it has a row.
func TestPrometheus_LogFileAtOperatorMount(t *testing.T) {
	const directory = "is the directory the Prometheus operator mounts a volume at in the prometheus container, so Prometheus cannot open it as a file"
	scrape := "scrape.log"
	spec := &monitoringv1.PrometheusSpec{}
	spec.Secrets = []string{"remote"}
	spec.ConfigMaps = []string{"targets"}
	spec.ScrapeFailureLogFile = &scrape
	spec.Web = &monitoringv1.PrometheusWebSpec{WebConfigFileFields: monitoringv1.WebConfigFileFields{TLSConfig: &monitoringv1.WebTLSConfig{
		KeySecret: corev1.SecretKeySelector{LocalObjectReference: corev1.LocalObjectReference{Name: "web-tls"}, Key: "tls.key"},
		Cert:      monitoringv1.SecretOrConfigMap{ConfigMap: &corev1.ConfigMapKeySelector{LocalObjectReference: corev1.LocalObjectReference{Name: "web-cert"}, Key: "tls.crt"}},
		ClientCA:  monitoringv1.SecretOrConfigMap{Secret: &corev1.SecretKeySelector{LocalObjectReference: corev1.LocalObjectReference{Name: "web-ca"}, Key: "ca.crt"}},
	}}}
	refused := map[string]string{
		"/prometheus":                                        directory,
		"/etc/prometheus/config_out":                         directory,
		"/var/log/prometheus":                                directory,
		"/etc/prometheus/certs":                              directory,
		"/etc/prometheus/secrets/remote":                     directory,
		"/etc/prometheus/configmaps/targets":                 directory,
		"/etc/prometheus/rules/prometheus-fast-rulefiles-0":  directory,
		"/etc/prometheus/rules/prometheus-fast-rulefiles-1":  directory,
		"/etc/prometheus/rules/prometheus-fast-rulefiles-2":  directory,
		"/etc/prometheus/web_config/secret/web-tls-key":      directory,
		"/etc/prometheus/web_config/configmap/web-cert-cert": directory,
		"/etc/prometheus/web_config/secret/web-ca-ca":        directory,
		"/etc/prometheus/web_config/web-config.yaml":         "is the read-only mount at /etc/prometheus/web_config/web-config.yaml in the prometheus container, so Prometheus cannot write the file",
	}
	var generated []string
	for _, m := range prometheusMounts("fast", spec) {
		if m.operator {
			generated = append(generated, m.path)
		}
	}
	var listed []string
	for p := range refused {
		listed = append(listed, p)
	}
	slices.Sort(generated)
	slices.Sort(listed)
	if !slices.Equal(generated, listed) {
		t.Fatalf("the operator's mounts are %v, the rows %v", generated, listed)
	}
	for p, want := range refused {
		spec.QueryLogFile = p
		err := validatePrometheusLogFiles("fast", spec)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("queryLogFile %s: got %v, want %q", p, err, want)
		}
	}
	for _, p := range []string{"/prometheus/query.log", "/etc/prometheus/config_out/query.log", "/var/log/prometheus/query.log"} {
		spec.QueryLogFile = p
		if err := validatePrometheusLogFiles("fast", spec); err != nil {
			t.Errorf("queryLogFile %s: %v", p, err)
		}
	}
}

// TestThanosDroppedMountFields: the fields thanosDroppedMountFields names are
// every field of corev1.VolumeMount but name and mountPath, which the operator
// copies into the sidecar's mount (pkg/prometheus/server/statefulset.go:642-647
// at prometheus-operator v0.94.1). A field a later Kubernetes release adds to
// the type fails here until it is listed. Each reads unset on an empty mount
// and set on one that writes every field.
func TestThanosDroppedMountFields(t *testing.T) {
	var fields []string
	for f := range reflect.TypeFor[corev1.VolumeMount]().Fields() {
		name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
		if name != "name" && name != "mountPath" {
			fields = append(fields, name)
		}
	}
	var listed []string
	for _, f := range thanosDroppedMountFields(corev1.VolumeMount{}) {
		listed = append(listed, f.name)
		if f.set {
			t.Errorf("%s reads set on an empty mount", f.name)
		}
	}
	slices.Sort(fields)
	slices.Sort(listed)
	if !slices.Equal(fields, listed) {
		t.Fatalf("listed %v, want every field of VolumeMount but name and mountPath: %v", listed, fields)
	}
	propagation, recursive := corev1.MountPropagationNone, corev1.RecursiveReadOnlyDisabled
	full := corev1.VolumeMount{
		Name: "data", MountPath: "/data",
		ReadOnly: true, RecursiveReadOnly: &recursive, SubPath: "a", MountPropagation: &propagation,
		SubPathExpr: "$(B)", BindMountOptions: []string{"noexec"},
	}
	for _, f := range thanosDroppedMountFields(full) {
		if !f.set {
			t.Errorf("%s reads unset on a mount that writes it", f.name)
		}
	}
}

// TestPrometheusVolumeKind: prometheusVolumeKind judges a directory exactly the
// volume types listed here with a type, each set alone, and leaves every
// other field of corev1.VolumeSource unjudged. A field a later Kubernetes
// release adds to the type fails here until it is listed. A hostPath is judged
// by its type: Directory and DirectoryOrCreate only.
func TestPrometheusVolumeKind(t *testing.T) {
	listed := map[string]string{
		"emptyDir": "emptyDir", "configMap": "configMap", "secret": "secret", "downwardAPI": "downwardAPI",
		"projected": "projected", "gitRepo": "gitRepo", "nfs": "nfs", "iscsi": "iscsi", "fc": "fc",
		// The type of what these put at the path is the claim's volume's,
		// the driver's or the runtime's, and a hostPath of no type is not
		// checked.
		"hostPath": "", "persistentVolumeClaim": "", "ephemeral": "", "csi": "", "flexVolume": "", "image": "",
		// No kubelet plugin of their own (cmd/kubelet/app/plugins.go:55-68
		// at Kubernetes v1.37.1).
		"gcePersistentDisk": "", "awsElasticBlockStore": "", "glusterfs": "", "rbd": "", "cinder": "", "cephfs": "",
		"flocker": "", "azureFile": "", "vsphereVolume": "", "quobyte": "", "azureDisk": "", "photonPersistentDisk": "",
		"portworxVolume": "", "scaleIO": "", "storageos": "",
	}
	var fields []string
	for f := range reflect.TypeFor[corev1.VolumeSource]().Fields() {
		name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
		fields = append(fields, name)
		var v corev1.VolumeSource
		field := reflect.ValueOf(&v).Elem().FieldByIndex(f.Index)
		field.Set(reflect.New(field.Type().Elem()))
		if got, want := prometheusVolumeKind(v), listed[name]; got != want {
			t.Errorf("%s: kind %q, want %q", name, got, want)
		}
	}
	slices.Sort(fields)
	names := slices.Sorted(maps.Keys(listed))
	if !slices.Equal(fields, names) {
		t.Fatalf("listed %v, want every field of VolumeSource: %v", names, fields)
	}
	for typ, want := range map[corev1.HostPathType]string{
		corev1.HostPathDirectory: "hostPath", corev1.HostPathDirectoryOrCreate: "hostPath",
		corev1.HostPathUnset: "", corev1.HostPathFile: "", corev1.HostPathFileOrCreate: "",
		corev1.HostPathSocket: "", corev1.HostPathCharDev: "", corev1.HostPathBlockDev: "",
	} {
		v := corev1.VolumeSource{HostPath: &corev1.HostPathVolumeSource{Path: "/srv", Type: &typ}}
		if got := prometheusVolumeKind(v); got != want {
			t.Errorf("hostPath of type %q: kind %q, want %q", typ, got, want)
		}
	}
}

// TestPrometheusMounts_PatchKeepsSubPath: a patch mount at the web
// configuration file's path that sets no subPath keeps the operator's
// (pkg/webconfig/config.go:272-278 at prometheus-operator v0.94.1), as the
// strategic merge keeps the fields a patch leaves out (pkg/k8s/merge.go:55), so
// an emptyDir patched there mounts one of its entries, not the volume as a
// directory, as does one that sets its own; patched without a subPath at the
// data mount, which carries none, it is a directory.
func TestPrometheusMounts_PatchKeepsSubPath(t *testing.T) {
	const file = "/etc/prometheus/web_config/web-config.yaml"
	mountAt := func(patch corev1.VolumeMount, storage ...*monitoringv1.StorageSpec) prometheusMount {
		spec := &monitoringv1.PrometheusSpec{}
		for _, s := range storage {
			spec.Storage = s
		}
		spec.Volumes = []corev1.Volume{{Name: "logs", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}}}
		spec.Containers = []corev1.Container{{Name: "prometheus", VolumeMounts: []corev1.VolumeMount{patch}}}
		mounts := prometheusMounts("fast", spec)
		i := slices.IndexFunc(mounts, func(m prometheusMount) bool { return m.path == patch.MountPath })
		if i < 0 {
			t.Fatalf("no mount at %s", patch.MountPath)
		}
		return mounts[i]
	}
	for name, patch := range map[string]corev1.VolumeMount{
		"inherited": {Name: "logs", MountPath: file},
		"its own":   {Name: "logs", MountPath: file, SubPath: "query.log"},
	} {
		if m := mountAt(patch); m.kind != "emptyDir" || m.dir || !m.writable || !m.operator {
			t.Errorf("%s: %+v, want the emptyDir mounted by subPath: not a directory, writable, at the operator's path", name, m)
		}
	}
	data := corev1.VolumeMount{Name: "logs", MountPath: "/prometheus"}
	if m := mountAt(data); !m.dir {
		t.Errorf("an emptyDir patched at the data mount without a subPath: %+v, want a directory", m)
	}
	// With storage set the data mount carries prometheus-db as its subPath
	// (SubPathForStorage, common.go:216-224 and :278-282), which the patch
	// keeps, unless disableMountSubPath drops it.
	storage := &monitoringv1.StorageSpec{EmptyDir: &corev1.EmptyDirVolumeSource{}}
	if m := mountAt(data, storage); m.dir || !m.writable || !m.operator {
		t.Errorf("an emptyDir patched at the data mount with storage set: %+v, want it mounted by the inherited subPath: not a directory, writable, at the operator's path", m)
	}
	if m := mountAt(data, &monitoringv1.StorageSpec{EmptyDir: &corev1.EmptyDirVolumeSource{}, DisableMountSubPath: true}); !m.dir { //nolint:staticcheck // SA1019: the operator still reads the deprecated field.
		t.Errorf("an emptyDir patched at the data mount with disableMountSubPath: %+v, want a directory", m)
	}
	for _, m := range prometheusMounts("fast", &monitoringv1.PrometheusSpec{CommonPrometheusFields: monitoringv1.CommonPrometheusFields{Storage: storage}}) {
		if m.path == "/prometheus" && !m.dir {
			t.Errorf("the operator's own data mount with storage set: %+v, want a directory", m)
		}
	}
}
