package springboot

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	v0 "spring-boot-threeport-module/pkg/api/v0"
)

// TestDatabaseSecretData covers what PostgreSQL is initialised with. The
// official image reads exactly these three variables, and only on an empty data
// directory, so a wrong name here produces a database nobody can log in to.
func TestDatabaseSecretData(t *testing.T) {
	data, err := databaseSecretData()
	require.NoError(t, err)

	assert.Equal(t, dbName, data["POSTGRES_DB"])
	assert.Equal(t, dbUser, data["POSTGRES_USER"])
	assert.NotEmpty(t, data["POSTGRES_PASSWORD"])

	assert.Len(t, data, 3, "the secret holds the credential and nothing else")
	for key, value := range data {
		assert.NotContains(
			t, value, "jdbc:",
			key+" must not embed a connection string: the URL is not secret, and a "+
				"copy of it here would have to be rewritten to rotate the password",
		)
	}
}

// TestDatabaseSecretData_PasswordIsPerCall covers that two instances backed by
// one definition do not end up sharing a password.
func TestDatabaseSecretData_PasswordIsPerCall(t *testing.T) {
	first, err := databaseSecretData()
	require.NoError(t, err)
	second, err := databaseSecretData()
	require.NoError(t, err)

	assert.NotEqual(
		t, first["POSTGRES_PASSWORD"], second["POSTGRES_PASSWORD"],
		"a leak from one instance must not reach every other instance of the definition",
	)
}

// TestSecretKeysMatchTheManifest covers the two halves agreeing. The manifest
// reads the credential by key and the reconciler writes it by key, from
// different files: a rename in one leaves pods in CreateContainerConfigError
// with a secret sitting right there.
func TestSecretKeysMatchTheManifest(t *testing.T) {
	data, err := databaseSecretData()
	require.NoError(t, err)

	in := defaultInput()
	in.database = v0.DatabasePostgres
	doc, err := springBootYaml(in)
	require.NoError(t, err)

	app := appContainer(t, doc, "myapp")
	for _, name := range []string{"SPRING_DATASOURCE_USERNAME", "SPRING_DATASOURCE_PASSWORD"} {
		secretName, key, fromSecret := envSecretKey(app, name)
		require.True(t, fromSecret, name+" must come from the secret")
		assert.Equal(t, DbSecretName("myapp"), secretName)
		assert.Contains(t, data, key, "the manifest reads a key the reconciler never writes")
	}

	postgres := containerIn(t, doc, "myapp-postgres", "postgres")
	for _, name := range []string{"POSTGRES_DB", "POSTGRES_USER", "POSTGRES_PASSWORD"} {
		_, key, fromSecret := envSecretKey(postgres, name)
		require.True(t, fromSecret, name+" must come from the secret")
		assert.Contains(t, data, key, "the manifest reads a key the reconciler never writes")
	}
}

// containerIn returns the named container of a Deployment.
func containerIn(t *testing.T, doc, deploymentName, containerName string) map[string]interface{} {
	t.Helper()

	deployment := objectNamed(t, doc, "Deployment", deploymentName)
	containers, err := nestedSlice(deployment, "spec", "template", "spec", "containers")
	require.NoError(t, err)
	for _, entry := range containers {
		asMap, ok := entry.(map[string]interface{})
		if ok && asMap["name"] == containerName {
			return asMap
		}
	}
	require.Failf(t, "container not found", "no container %s in deployment %s", containerName, deploymentName)

	return nil
}

// TestDefinitionDatabase covers the fallback. The column defaults to none in
// the database, but a definition the reconciler reads back before that default
// is applied - or one built in a test - has a nil field, and treating nil as
// "deploy postgres" would create a database the manifest never referenced.
func TestDefinitionDatabase(t *testing.T) {
	assert.Equal(t, v0.DatabaseNone, definitionDatabase(&v0.SpringBootDefinition{}))

	postgres := v0.DatabasePostgres
	assert.Equal(
		t, v0.DatabasePostgres,
		definitionDatabase(&v0.SpringBootDefinition{Database: &postgres}),
	)
}

// TestDbSecretName covers the name both halves have to agree on.
func TestDbSecretName(t *testing.T) {
	assert.Equal(t, "myapp-db", DbSecretName("myapp"))
	assert.True(t, strings.HasPrefix(DbSecretName("myapp"), "myapp"))
}
