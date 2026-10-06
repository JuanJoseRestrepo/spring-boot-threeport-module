package springboot

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"sigs.k8s.io/yaml"
	api_v0 "spring-boot-threeport-module/pkg/api/v0"
)

// docsIn parses every document in a multi-document YAML string. Splitting on
// the separator is enough here because the manifest is built from structured
// objects, so no document body can contain one.
func docsIn(t *testing.T, doc string) []map[string]interface{} {
	t.Helper()

	var docs []map[string]interface{}
	for _, chunk := range strings.Split(doc, "\n---\n") {
		if strings.TrimSpace(chunk) == "" {
			continue
		}
		var parsed map[string]interface{}
		require.NoError(t, yaml.Unmarshal([]byte(chunk), &parsed), "every document must be valid YAML")
		docs = append(docs, parsed)
	}

	return docs
}

// kindsIn returns the kind of every document, in order.
func kindsIn(t *testing.T, doc string) []string {
	t.Helper()

	var kinds []string
	for _, parsed := range docsIn(t, doc) {
		kind, _ := parsed["kind"].(string)
		kinds = append(kinds, kind)
	}

	return kinds
}

func countOf(kinds []string, kind string) int {
	var n int
	for _, k := range kinds {
		if k == kind {
			n++
		}
	}

	return n
}

// objectNamed returns the document with the given kind and metadata name.
func objectNamed(t *testing.T, doc, kind, name string) map[string]interface{} {
	t.Helper()

	for _, parsed := range docsIn(t, doc) {
		if parsed["kind"] != kind {
			continue
		}
		metadata, _ := parsed["metadata"].(map[string]interface{})
		if metadata["name"] == name {
			return parsed
		}
	}
	require.Failf(t, "object not found", "no %s named %s in the manifest", kind, name)

	return nil
}

// appContainer digs the application container out of a Deployment.
func appContainer(t *testing.T, doc, name string) map[string]interface{} {
	t.Helper()

	deployment := objectNamed(t, doc, "Deployment", name)
	containers, err := nestedSlice(deployment, "spec", "template", "spec", "containers")
	require.NoError(t, err)
	require.Len(t, containers, 1, "the application pod runs one container")
	container, ok := containers[0].(map[string]interface{})
	require.True(t, ok)

	return container
}

func nestedSlice(obj map[string]interface{}, path ...string) ([]interface{}, error) {
	current := interface{}(obj)
	for _, key := range path {
		asMap, ok := current.(map[string]interface{})
		if !ok {
			return nil, assert.AnError
		}
		current = asMap[key]
	}
	slice, ok := current.([]interface{})
	if !ok {
		return nil, assert.AnError
	}

	return slice, nil
}

// envValue returns the literal value of an environment variable on a container,
// and whether it is set at all.
func envValue(container map[string]interface{}, name string) (string, bool) {
	env, ok := container["env"].([]interface{})
	if !ok {
		return "", false
	}
	for _, entry := range env {
		asMap, ok := entry.(map[string]interface{})
		if !ok || asMap["name"] != name {
			continue
		}
		// a literal value only: an entry sourced from a secret has no value
		// key, and reporting it as set would hide a credential written into
		// the manifest behind a passing test
		value, hasValue := asMap["value"].(string)

		return value, hasValue
	}

	return "", false
}

// envSecretKey returns the Secret name and key an environment variable is
// sourced from, and whether it is sourced from a Secret at all.
func envSecretKey(container map[string]interface{}, name string) (string, string, bool) {
	env, ok := container["env"].([]interface{})
	if !ok {
		return "", "", false
	}
	for _, entry := range env {
		asMap, ok := entry.(map[string]interface{})
		if !ok || asMap["name"] != name {
			continue
		}
		valueFrom, ok := asMap["valueFrom"].(map[string]interface{})
		if !ok {
			return "", "", false
		}
		ref, ok := valueFrom["secretKeyRef"].(map[string]interface{})
		if !ok {
			return "", "", false
		}
		secretName, _ := ref["name"].(string)
		key, _ := ref["key"].(string)

		return secretName, key, true
	}

	return "", "", false
}

func defaultInput() springBootManifestInput {
	return springBootManifestInput{
		definitionName: "myapp",
		image:          "myorg/myapp:v1",
		serverPort:     api_v0.DefaultServerPort,
		replicas:       1,
		environment:    "dev",
		database:       api_v0.DatabaseNone,
		healthPath:     api_v0.DefaultHealthPath,
		dbStorageGb:    20,
	}
}

// TestSpringBootYaml_NoDatabase covers the default: a Spring Boot application
// can run on an embedded database, so asking for none must leave out the
// PostgreSQL objects entirely rather than deploy an unused database.
// TestSpringBootYaml_ResourcesOmittedWhenUnset covers the resources block being
// absent rather than empty. A LimitRange in the namespace defaults against an
// absent field and has nothing to default against an empty one.
func TestSpringBootYaml_ResourcesOmittedWhenUnset(t *testing.T) {
	doc, err := springBootYaml(defaultInput())
	require.NoError(t, err)

	assert.NotContains(t, appContainer(t, doc, "myapp"), "resources")
}

// TestSpringBootYaml_Resources covers each of the four quantities reaching the
// container, and requests and limits staying independent: a limit is what the
// JVM sizes its heap from, so inferring one from the other would be guessing at
// a number the user chose deliberately.
func TestSpringBootYaml_Resources(t *testing.T) {
	in := defaultInput()
	in.cpuRequest = "250m"
	in.memoryLimit = "1Gi"

	doc, err := springBootYaml(in)
	require.NoError(t, err)

	resources, ok := appContainer(t, doc, "myapp")["resources"].(map[string]interface{})
	require.True(t, ok, "the resources block must be set")

	requests, _ := resources["requests"].(map[string]interface{})
	limits, _ := resources["limits"].(map[string]interface{})
	assert.Equal(t, "250m", requests["cpu"])
	assert.Equal(t, "1Gi", limits["memory"])
	assert.NotContains(t, requests, "memory", "a memory request was not asked for")
	assert.NotContains(t, limits, "cpu", "a cpu limit was not asked for")

	in.cpuLimit = "1"
	in.memoryRequest = "512Mi"
	doc, err = springBootYaml(in)
	require.NoError(t, err)
	resources = appContainer(t, doc, "myapp")["resources"].(map[string]interface{})
	assert.Equal(t, "512Mi", resources["requests"].(map[string]interface{})["memory"])
	assert.Equal(t, "1", resources["limits"].(map[string]interface{})["cpu"])
}

// TestSpringBootYaml_ResourcesOnlyOnTheApplication covers the database keeping
// its own sizing: postgres is not a JVM and has nothing to do with the heap the
// application was sized for.
func TestSpringBootYaml_ResourcesOnlyOnTheApplication(t *testing.T) {
	in := defaultInput()
	in.database = api_v0.DatabasePostgres
	in.memoryLimit = "1Gi"

	doc, err := springBootYaml(in)
	require.NoError(t, err)

	postgres := containerIn(t, doc, "myapp-postgres", "postgres")
	assert.NotContains(t, postgres, "resources", "the application's limit must not be applied to the database")
}

func TestSpringBootYaml_NoDatabase(t *testing.T) {
	doc, err := springBootYaml(defaultInput())
	require.NoError(t, err)

	kinds := kindsIn(t, doc)

	assert.Equal(t, 1, countOf(kinds, "Deployment"), "only the application")
	assert.Equal(t, 1, countOf(kinds, "Service"), "only the application")
	assert.NotContains(t, kinds, "PersistentVolumeClaim", "no database means no storage")
	assert.NotContains(
		t, kinds, "Secret",
		"the credential is created per instance by the reconciler, not rendered into the shared manifest",
	)
}

// TestSpringBootYaml_WithPostgres covers the database being asked for.
func TestSpringBootYaml_WithPostgres(t *testing.T) {
	in := defaultInput()
	in.database = api_v0.DatabasePostgres

	doc, err := springBootYaml(in)
	require.NoError(t, err)

	kinds := kindsIn(t, doc)

	assert.Equal(t, 2, countOf(kinds, "Deployment"), "one for postgres and one for the app")
	assert.Equal(t, 2, countOf(kinds, "Service"), "one for postgres and one for the app")
	assert.Contains(t, kinds, "PersistentVolumeClaim")
	assert.NotContains(t, kinds, "Secret", "still created per instance by the reconciler")
}

// TestSpringBootYaml_DatasourceOnlyWithDatabase covers the datasource wiring.
// Setting SPRING_DATASOURCE_URL with no database to point at would override
// whatever the image configured for itself and break an application that was
// running fine on its embedded one.
func TestSpringBootYaml_DatasourceOnlyWithDatabase(t *testing.T) {
	withoutDb, err := springBootYaml(defaultInput())
	require.NoError(t, err)

	_, set := envValue(appContainer(t, withoutDb, "myapp"), "SPRING_DATASOURCE_URL")
	assert.False(t, set, "no datasource is configured when no database is deployed")

	in := defaultInput()
	in.database = api_v0.DatabasePostgres
	withDb, err := springBootYaml(in)
	require.NoError(t, err)

	url, set := envValue(appContainer(t, withDb, "myapp"), "SPRING_DATASOURCE_URL")
	require.True(t, set, "the application has to be told where the database is")
	assert.Equal(t, "jdbc:postgresql://myapp-postgres:5432/springboot", url)
}

// TestSpringBootYaml_CredentialsComeFromTheSecret covers that neither the
// username nor the password is rendered as a literal: both are read from the
// Secret the instance reconciler writes, so the shared manifest carries no
// credential.
func TestSpringBootYaml_CredentialsComeFromTheSecret(t *testing.T) {
	in := defaultInput()
	in.database = api_v0.DatabasePostgres

	doc, err := springBootYaml(in)
	require.NoError(t, err)

	container := appContainer(t, doc, "myapp")
	for name, key := range map[string]string{
		"SPRING_DATASOURCE_USERNAME": "POSTGRES_USER",
		"SPRING_DATASOURCE_PASSWORD": "POSTGRES_PASSWORD",
	} {
		literal, isLiteral := envValue(container, name)
		assert.Empty(t, literal, name+" must not be written into the manifest as a value")
		assert.False(t, isLiteral, name+" must not be a literal value")

		secretName, secretKey, fromSecret := envSecretKey(container, name)
		require.True(t, fromSecret, name+" must be sourced from the instance secret")
		assert.Equal(t, DbSecretName("myapp"), secretName)
		assert.Equal(t, key, secretKey)
	}
}

// TestSpringBootYaml_WaitsForDatabase covers the init container. Spring Boot
// fails the whole application context when the datasource is unreachable at
// startup, so starting alongside the database is a crash loop rather than a
// retry.
func TestSpringBootYaml_WaitsForDatabase(t *testing.T) {
	withoutDb, err := springBootYaml(defaultInput())
	require.NoError(t, err)

	deployment := objectNamed(t, withoutDb, "Deployment", "myapp")
	podSpec := deployment["spec"].(map[string]interface{})["template"].(map[string]interface{})["spec"].(map[string]interface{})
	assert.NotContains(t, podSpec, "initContainers", "nothing to wait for without a database")

	in := defaultInput()
	in.database = api_v0.DatabasePostgres
	withDb, err := springBootYaml(in)
	require.NoError(t, err)

	deployment = objectNamed(t, withDb, "Deployment", "myapp")
	initContainers, err := nestedSlice(deployment, "spec", "template", "spec", "initContainers")
	require.NoError(t, err, "the application must wait for the database")
	require.Len(t, initContainers, 1)
	assert.Contains(t, initContainers[0].(map[string]interface{})["command"].([]interface{})[2], "pg_isready")
}

// TestSpringBootYaml_ProbesUseTheHealthPath covers all three probes. A JVM
// application accepts TCP connections before its context has refreshed, so a
// tcpSocket check would report it ready while it is still wiring beans.
func TestSpringBootYaml_ProbesUseTheHealthPath(t *testing.T) {
	in := defaultInput()
	in.healthPath = "/healthz"
	in.serverPort = 9000

	doc, err := springBootYaml(in)
	require.NoError(t, err)

	container := appContainer(t, doc, "myapp")
	for _, probe := range []string{"startupProbe", "readinessProbe", "livenessProbe"} {
		asMap, ok := container[probe].(map[string]interface{})
		require.True(t, ok, probe+" must be set")
		httpGet, ok := asMap["httpGet"].(map[string]interface{})
		require.True(t, ok, probe+" must be an HTTP check, not a TCP one")
		assert.Equal(t, "/healthz", httpGet["path"], probe+" must use the configured path")
		assert.EqualValues(t, 9000, httpGet["port"], probe+" must use the configured port")
	}
}

// TestSpringBootYaml_StartupProbeCoversSlowBoot covers why the startup probe
// exists: without it the liveness probe would restart a JVM application
// mid-boot and it would never finish starting.
func TestSpringBootYaml_StartupProbeCoversSlowBoot(t *testing.T) {
	doc, err := springBootYaml(defaultInput())
	require.NoError(t, err)

	container := appContainer(t, doc, "myapp")
	startup := container["startupProbe"].(map[string]interface{})

	period, _ := startup["periodSeconds"].(float64)
	failures, _ := startup["failureThreshold"].(float64)
	assert.GreaterOrEqual(
		t, period*failures, float64(120),
		"a cold JVM with a database connection can take minutes; the startup budget has to cover it",
	)
}

// TestSpringBootYaml_ServiceTargetsTheServerPort covers that the Service and
// the container agree. The port is a field precisely because an image built
// with a different server.port would otherwise get a Service pointing nowhere.
func TestSpringBootYaml_ServiceTargetsTheServerPort(t *testing.T) {
	in := defaultInput()
	in.serverPort = 9000

	doc, err := springBootYaml(in)
	require.NoError(t, err)

	service := objectNamed(t, doc, "Service", "myapp")
	ports, err := nestedSlice(service, "spec", "ports")
	require.NoError(t, err)
	port := ports[0].(map[string]interface{})

	assert.EqualValues(t, 80, port["port"], "callers reach the app on a normal HTTP port")
	assert.EqualValues(t, 9000, port["targetPort"], "which has to map onto whatever the image serves on")
}

// TestSpringBootYaml_OptionalEnvOmitted covers the optional fields. Spring
// falls back to its own defaults when the variables are absent, so an empty
// value must not be set rather than set to "": SPRING_PROFILES_ACTIVE="" is not
// the same as leaving the application on its default profile.
func TestSpringBootYaml_OptionalEnvOmitted(t *testing.T) {
	doc, err := springBootYaml(defaultInput())
	require.NoError(t, err)

	container := appContainer(t, doc, "myapp")
	for _, name := range []string{"SPRING_PROFILES_ACTIVE", "JAVA_TOOL_OPTIONS"} {
		_, set := envValue(container, name)
		assert.False(t, set, name+" must be absent when not configured")
	}

	in := defaultInput()
	in.profile = "postgres,prod"
	in.javaOpts = "-Xmx512m"
	doc, err = springBootYaml(in)
	require.NoError(t, err)

	container = appContainer(t, doc, "myapp")
	profile, set := envValue(container, "SPRING_PROFILES_ACTIVE")
	assert.True(t, set)
	assert.Equal(t, "postgres,prod", profile)
	javaOpts, set := envValue(container, "JAVA_TOOL_OPTIONS")
	assert.True(t, set)
	assert.Equal(t, "-Xmx512m", javaOpts)
}

// TestSpringBootYaml_NoNamespaces covers that nothing declares a namespace.
// Threeport assigns one per workload instance and rewrites whatever the
// manifest says, so declaring one would imply a control the module lacks.
func TestSpringBootYaml_NoNamespaces(t *testing.T) {
	in := defaultInput()
	in.database = api_v0.DatabasePostgres

	doc, err := springBootYaml(in)
	require.NoError(t, err)

	for _, parsed := range docsIn(t, doc) {
		metadata, _ := parsed["metadata"].(map[string]interface{})
		assert.NotContains(t, metadata, "namespace", "object %v declares a namespace", metadata["name"])
	}
}

func TestReplicasByEnv(t *testing.T) {
	assert.Equal(t, 3, replicasByEnv("prod"))
	assert.Equal(t, 1, replicasByEnv("dev"))
	assert.Equal(t, 1, replicasByEnv("anything else"))
}

func TestDbStorageByEnv(t *testing.T) {
	assert.Equal(t, 100, dbStorageByEnv("prod"))
	assert.Equal(t, 20, dbStorageByEnv("dev"))
}

// TestGeneratePassword covers that the password is random and URL-safe: it ends
// up in a connection string, so a character needing escaping would break it.
func TestGeneratePassword(t *testing.T) {
	first, err := generatePassword(32)
	require.NoError(t, err)
	second, err := generatePassword(32)
	require.NoError(t, err)

	assert.NotEqual(t, first, second, "two passwords must not come out the same")
	assert.NotEmpty(t, first)
	assert.Equal(t, first, strings.TrimFunc(first, func(r rune) bool {
		return !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_')
	}), "the password must be URL-safe")
}
