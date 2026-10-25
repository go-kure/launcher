package kurel

import (
	"strings"
	"testing"
)

// TestBuild_PostgresqlBackupOldKeysRefused: a document written against the
// `backup` and `objectStore` of before go-kure/launcher#1001 is refused by
// kurel build, at the document check, in words that name where each key went.
func TestBuild_PostgresqlBackupOldKeysRefused(t *testing.T) {
	component := func(props string) string {
		return helmNamesApp("    - name: db\n      type: postgresql\n      properties:\n" + props)
	}
	for _, tc := range []struct {
		name, props, want string
	}{
		{
			"the top-level objectStore",
			"        objectStore:\n          destinationPath: s3://bucket/db/\n          secretName: creds\n",
			`unsupported field "objectStore" (allowed: affinity, backup, `,
		},
		{
			"the top-level objectStore's hint",
			"        objectStore:\n          destinationPath: s3://bucket/db/\n",
			"; objectStore: moved to backup.objectStore (its retentionPolicy and serverName to backup.retentionPolicy and backup.serverName; its secretName to backup.objectStore.s3Credentials)",
		},
		{
			"the top-level objectStoreObjectName",
			"        objectStoreObjectName: store\n",
			"; objectStoreObjectName: moved to backup.objectStoreObjectName",
		},
		{
			"backup.destinationPath",
			"        backup:\n          destinationPath: s3://bucket/db/\n          retentionPolicy: 30d\n",
			"backup.destinationPath: removed; use backup.objectStore.destinationPath",
		},
		{
			"backup.endpointURL",
			"        backup:\n          objectStore:\n            destinationPath: s3://bucket/db/\n            s3Credentials:\n              inheritFromIAMRole: true\n          endpointURL: https://s3.example.com\n",
			"backup.endpointURL: removed; use backup.objectStore.endpointURL",
		},
		{
			"backup.secretName",
			"        backup:\n          objectStore:\n            destinationPath: s3://bucket/db/\n            s3Credentials:\n              inheritFromIAMRole: true\n          secretName: creds\n",
			"backup.secretName: removed; use backup.objectStore.s3Credentials (accessKeyId and secretAccessKey each reference a key of the Secret)",
		},
		{
			"a backup without an object store",
			"        backup:\n          retentionPolicy: 30d\n",
			"backup.objectStore: required (the object store backups and WAL are written to)",
		},
		{
			"backup.objectStore.serverName",
			"        backup:\n          objectStore:\n            destinationPath: s3://bucket/db/\n            serverName: db\n            s3Credentials:\n              inheritFromIAMRole: true\n",
			"backup.objectStore.serverName: not allowed (the ObjectStore CRD forbids it); use backup.serverName",
		},
		{
			// No s3Credentials: the document check must not ask for them
			// before it gives the reason Azure is refused.
			"an Azure object store",
			"        backup:\n          objectStore:\n            destinationPath: https://acct.blob.core.windows.net/c\n            azureCredentials:\n              inheritFromAzureAD: true\n",
			"backup.objectStore.azureCredentials: not supported (backup writes to S3-compatible storage only; the cnpg-objectstore kind takes Azure credentials)",
		},
		{
			"a Google object store",
			"        backup:\n          objectStore:\n            destinationPath: gs://bucket/db/\n            googleCredentials:\n              gkeEnvironment: true\n",
			"backup.objectStore.googleCredentials: not supported (backup writes to S3-compatible storage only; the cnpg-objectstore kind takes Google credentials)",
		},
		{
			"an object store without a path",
			"        backup:\n          objectStore:\n            s3Credentials:\n              inheritFromIAMRole: true\n",
			"backup.objectStore.destinationPath: required (the object store path backups and WAL are written to)",
		},
		{
			"an object store without credentials",
			"        backup:\n          objectStore:\n            destinationPath: s3://bucket/db/\n",
			"backup.objectStore.s3Credentials: required (accessKeyId and secretAccessKey, or inheritFromIAMRole: true)",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, out, err := buildDocs(t, component(tc.props))
			if err == nil {
				t.Fatalf("kurel build accepted the document; output: %s", out)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err = %v\nwant one containing %q", err, tc.want)
			}
		})
	}
}
