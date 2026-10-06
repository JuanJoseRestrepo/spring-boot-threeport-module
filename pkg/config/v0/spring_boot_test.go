package v0

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	util "github.com/threeport/threeport/pkg/util/v0"
	yaml "sigs.k8s.io/yaml"

	api_v0 "spring-boot-threeport-module/pkg/api/v0"
)

// TestSpringBootDefinitionConfig_Validate covers the fields a user can get wrong in
// a config file. Most are otherwise rejected far later: Image by a database
// constraint, Environment, Replicas, ServerPort and HealthPath by the kube API
// once Threeport tries to apply the manifest, long after the definition was
// accepted. Database is the exception - nothing downstream rejects it, because
// the manifest deploys PostgreSQL for "postgres" and nothing for anything else,
// so a typo would deploy cleanly without the database that was asked for.
func TestSpringBootDefinitionConfig_Validate(t *testing.T) {
	tests := []struct {
		name    string
		values  SpringBootDefinitionValues
		wantErr string
	}{
		{
			name:   "a name and an image are enough",
			values: SpringBootDefinitionValues{Name: util.Ptr("myapp"), Image: util.Ptr("myorg/myapp:v1")},
		},
		{
			name:    "an image is required",
			values:  SpringBootDefinitionValues{Name: util.Ptr("myapp")},
			wantErr: "Image",
		},
		{
			name:    "a name is required",
			values:  SpringBootDefinitionValues{Image: util.Ptr("myorg/myapp:v1")},
			wantErr: "Name",
		},
		{
			name: "the name has to be usable as a label value",
			values: SpringBootDefinitionValues{
				Name:  util.Ptr("My App/v2"),
				Image: util.Ptr("myorg/myapp:v1"),
			},
			wantErr: "Name",
		},
		{
			name: "the environment has to be usable as a label value",
			values: SpringBootDefinitionValues{
				Name:        util.Ptr("myapp"),
				Image:       util.Ptr("myorg/myapp:v1"),
				Environment: util.Ptr("staging/eu-west"),
			},
			wantErr: "Environment",
		},
		{
			name: "an ordinary environment is accepted",
			values: SpringBootDefinitionValues{
				Name:        util.Ptr("myapp"),
				Image:       util.Ptr("myorg/myapp:v1"),
				Environment: util.Ptr("prod"),
			},
		},
		{
			name: "replicas cannot be negative",
			values: SpringBootDefinitionValues{
				Name:     util.Ptr("myapp"),
				Image:    util.Ptr("myorg/myapp:v1"),
				Replicas: util.Ptr(-1),
			},
			wantErr: "Replicas",
		},
		{
			name: "zero replicas is a legitimate way to scale down",
			values: SpringBootDefinitionValues{
				Name:     util.Ptr("myapp"),
				Image:    util.Ptr("myorg/myapp:v1"),
				Replicas: util.Ptr(0),
			},
		},
		{
			name: "a port outside the range is rejected",
			values: SpringBootDefinitionValues{
				Name:       util.Ptr("myapp"),
				Image:      util.Ptr("myorg/myapp:v1"),
				ServerPort: util.Ptr(70000),
			},
			wantErr: "ServerPort",
		},
		{
			name: "a non-default port is ordinary",
			values: SpringBootDefinitionValues{
				Name:       util.Ptr("myapp"),
				Image:      util.Ptr("myorg/myapp:v1"),
				ServerPort: util.Ptr(9000),
			},
		},
		{
			name: "a misspelt database is rejected rather than read as none",
			values: SpringBootDefinitionValues{
				Name:     util.Ptr("myapp"),
				Image:    util.Ptr("myorg/myapp:v1"),
				Database: util.Ptr("postgresql"),
			},
			wantErr: "Database",
		},
		{
			name: "both database values are accepted",
			values: SpringBootDefinitionValues{
				Name:     util.Ptr("myapp"),
				Image:    util.Ptr("myorg/myapp:v1"),
				Database: util.Ptr(api_v0.DatabasePostgres),
			},
		},
		{
			name: "an unparseable memory limit is rejected",
			values: SpringBootDefinitionValues{
				Name:        util.Ptr("myapp"),
				Image:       util.Ptr("myorg/myapp:v1"),
				MemoryLimit: util.Ptr("1 gig"),
			},
			wantErr: "MemoryLimit",
		},
		{
			name: "ordinary kubernetes quantities are accepted",
			values: SpringBootDefinitionValues{
				Name:          util.Ptr("myapp"),
				Image:         util.Ptr("myorg/myapp:v1"),
				CpuRequest:    util.Ptr("250m"),
				CpuLimit:      util.Ptr("1"),
				MemoryRequest: util.Ptr("512Mi"),
				MemoryLimit:   util.Ptr("2Gi"),
			},
		},
		{
			name: "a cpu request in the wrong unit is rejected",
			values: SpringBootDefinitionValues{
				Name:       util.Ptr("myapp"),
				Image:      util.Ptr("myorg/myapp:v1"),
				CpuRequest: util.Ptr("250mcpu"),
			},
			wantErr: "CpuRequest",
		},
		{
			name: "a memory request above its own limit is rejected",
			values: SpringBootDefinitionValues{
				Name:          util.Ptr("myapp"),
				Image:         util.Ptr("myorg/myapp:v1"),
				MemoryRequest: util.Ptr("2Gi"),
				MemoryLimit:   util.Ptr("1Gi"),
			},
			wantErr: "MemoryRequest 2Gi is above MemoryLimit 1Gi",
		},
		{
			name: "a request equal to its limit is fine",
			values: SpringBootDefinitionValues{
				Name:          util.Ptr("myapp"),
				Image:         util.Ptr("myorg/myapp:v1"),
				MemoryRequest: util.Ptr("1Gi"),
				MemoryLimit:   util.Ptr("1024Mi"),
			},
		},
		{
			name: "a cpu request above its own limit is rejected",
			values: SpringBootDefinitionValues{
				Name:       util.Ptr("myapp"),
				Image:      util.Ptr("myorg/myapp:v1"),
				CpuRequest: util.Ptr("2"),
				CpuLimit:   util.Ptr("500m"),
			},
			wantErr: "CpuRequest 2 is above CpuLimit 500m",
		},
		{
			name: "a health path without a leading slash is rejected",
			values: SpringBootDefinitionValues{
				Name:       util.Ptr("myapp"),
				Image:      util.Ptr("myorg/myapp:v1"),
				HealthPath: util.Ptr("actuator/health"),
			},
			wantErr: "HealthPath",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config := SpringBootDefinitionConfig{SpringBootDefinition: test.values}
			err := config.Validate()
			if test.wantErr == "" {
				assert.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.Contains(t, err.Error(), test.wantErr)
		})
	}
}

// TestSpringBootInstanceConfig_Validate covers the definition reference. Create
// dereferences SpringBootDefinition.Name to look the definition up, so a config
// without one panics rather than reporting a missing field.
func TestSpringBootInstanceConfig_Validate(t *testing.T) {
	tests := []struct {
		name    string
		values  SpringBootInstanceValues
		wantErr string
	}{
		{
			name: "a name and a definition are enough",
			values: SpringBootInstanceValues{
				Name:                 util.Ptr("myapp"),
				SpringBootDefinition: &SpringBootDefinitionValues{Name: util.Ptr("myapp")},
			},
		},
		{
			name:    "the definition is required",
			values:  SpringBootInstanceValues{Name: util.Ptr("myapp")},
			wantErr: "SpringBootDefinition.Name",
		},
		{
			name: "a definition without a name is no better than none",
			values: SpringBootInstanceValues{
				Name:                 util.Ptr("myapp"),
				SpringBootDefinition: &SpringBootDefinitionValues{},
			},
			wantErr: "SpringBootDefinition.Name",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config := SpringBootInstanceConfig{SpringBootInstance: test.values}
			err := config.Validate()
			if test.wantErr == "" {
				assert.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.Contains(t, err.Error(), test.wantErr)
		})
	}
}

// TestMapToSpringBootDefinedInstances covers the pairing a defined instance rests
// on, and that it carries the attributes from both halves rather than the name
// alone.
func TestMapToSpringBootDefinedInstances(t *testing.T) {
	definitions := []SpringBootDefinitionConfig{
		{SpringBootDefinition: SpringBootDefinitionValues{
			Name:        util.Ptr("myapp"),
			Image:       util.Ptr("myorg/myapp:v1"),
			Profile:     util.Ptr("postgres,prod"),
			ServerPort:  util.Ptr(9000),
			Environment: util.Ptr("prod"),
			Replicas:    util.Ptr(3),
			Database:    util.Ptr(api_v0.DatabasePostgres),
			CpuRequest:  util.Ptr("250m"),
			MemoryLimit: util.Ptr("2Gi"),
		}},
		{SpringBootDefinition: SpringBootDefinitionValues{Name: util.Ptr("other")}},
	}
	instances := []SpringBootInstanceConfig{
		{SpringBootInstance: SpringBootInstanceValues{
			Name:                 util.Ptr("myapp"),
			SubDomain:            util.Ptr("www"),
			SpringBootDefinition: &SpringBootDefinitionValues{Name: util.Ptr("myapp")},
			Age:                  util.Ptr("2d"),
		}},
	}

	configs := mapToSpringBootDefinedInstances(&definitions, &instances)
	require.Len(t, *configs, 1, "only the instance whose definition shares its name is a defined instance")

	values := (*configs)[0].SpringBoot
	assert.Equal(t, "myapp", *values.Name)
	assert.Equal(t, "myorg/myapp:v1", *values.Image, "the definition's attributes have to survive the mapping")
	assert.Equal(t, "prod", *values.Environment)
	assert.Equal(t, 3, *values.Replicas)
	assert.Equal(t, "postgres,prod", *values.Profile)
	assert.Equal(t, 9000, *values.ServerPort)
	assert.Equal(t, api_v0.DatabasePostgres, *values.Database)
	// the resource fields travel with the rest: a defined instance read back
	// has to describe what was deployed, or a get reports a workload with no
	// limits that has them
	require.NotNil(t, values.CpuRequest, "CpuRequest did not survive the mapping")
	assert.Equal(t, "250m", *values.CpuRequest)
	require.NotNil(t, values.MemoryLimit, "MemoryLimit did not survive the mapping")
	assert.Equal(t, "2Gi", *values.MemoryLimit)
	assert.Equal(t, "www", *values.SubDomain, "the instance's attributes have to survive it too")
	assert.Equal(t, "2d", *values.Age)
}

// TestMapToSpringBootDefinedInstances_SkipsIncompleteInstances covers the
// dereferences the pairing does. An instance missing a name or a definition
// reference used to panic here rather than be skipped.
func TestMapToSpringBootDefinedInstances_SkipsIncompleteInstances(t *testing.T) {
	definitions := []SpringBootDefinitionConfig{
		{SpringBootDefinition: SpringBootDefinitionValues{Name: util.Ptr("myapp")}},
		{SpringBootDefinition: SpringBootDefinitionValues{}},
	}
	instances := []SpringBootInstanceConfig{
		{SpringBootInstance: SpringBootInstanceValues{Name: nil}},
		{SpringBootInstance: SpringBootInstanceValues{Name: util.Ptr("myapp")}},
		{SpringBootInstance: SpringBootInstanceValues{
			Name:                 util.Ptr("myapp"),
			SpringBootDefinition: &SpringBootDefinitionValues{},
		}},
	}

	assert.NotPanics(t, func() {
		configs := mapToSpringBootDefinedInstances(&definitions, &instances)
		assert.Empty(t, *configs, "none of these instances is half of a defined instance")
	})
}

// TestSampleConfigsParse covers the files under samples/. The CLI unmarshals
// strictly, so a field the values structs do not carry is an error rather than
// something quietly ignored — which means a sample can go stale the moment a
// field is renamed, and the user is the one who finds out.
func TestSampleConfigsParse(t *testing.T) {
	tests := []struct {
		file   string
		into   interface{}
		assert func(t *testing.T, into interface{})
	}{
		{
			file: "spring-boot.yaml",
			into: &SpringBootConfig{},
			assert: func(t *testing.T, into interface{}) {
				values := into.(*SpringBootConfig).SpringBoot
				require.NotNil(t, values.Name)
				require.NotNil(t, values.Image, "a sample without an image would not pass Validate")
			},
		},
		{
			file: "spring-boot-definition.yaml",
			into: &SpringBootDefinitionConfig{},
			assert: func(t *testing.T, into interface{}) {
				values := into.(*SpringBootDefinitionConfig).SpringBootDefinition
				require.NotNil(t, values.Name)
				require.NotNil(t, values.Image)
				require.NotNil(t, values.Replicas)
				require.NotNil(t, values.Database)
			},
		},
		{
			file: "spring-boot-instance.yaml",
			into: &SpringBootInstanceConfig{},
			assert: func(t *testing.T, into interface{}) {
				values := into.(*SpringBootInstanceConfig).SpringBootInstance
				require.NotNil(t, values.Name)
				require.NotNil(t, values.SpringBootDefinition)
				require.Nil(
					t, values.KubernetesRuntimeInstance,
					"the sample documents omitting the runtime to get the default; setting one would contradict its own comment",
				)
			},
		},
	}

	for _, test := range tests {
		t.Run(test.file, func(t *testing.T) {
			content, err := os.ReadFile(filepath.Join("..", "..", "..", "samples", test.file))
			require.NoError(t, err)
			require.NoError(t, yaml.UnmarshalStrict(content, test.into), "the CLI would reject this file")
			test.assert(t, test.into)
		})
	}
}

// TestSpringBootConfig_CreateReportsAMissingName covers the error message wrapping
// Validate. A missing name is one of the things Validate reports, so reading it
// to describe the failure crashed tptctl with a stack trace instead of printing
// the missing field.
func TestSpringBootConfig_CreateReportsAMissingName(t *testing.T) {
	configs := []struct {
		name string
		call func() error
	}{
		{
			name: "spring boot definition",
			call: func() error {
				config := SpringBootDefinitionConfig{
					SpringBootDefinition: SpringBootDefinitionValues{Image: util.Ptr("myorg/myapp:v1")},
				}
				_, err := config.Create(nil, "")

				return err
			},
		},
		{
			name: "spring boot instance",
			call: func() error {
				config := SpringBootInstanceConfig{
					SpringBootInstance: SpringBootInstanceValues{
						SpringBootDefinition: &SpringBootDefinitionValues{Name: util.Ptr("myapp")},
					},
				}
				_, err := config.Create(nil, "")

				return err
			},
		},
	}

	for _, config := range configs {
		t.Run(config.name, func(t *testing.T) {
			var err error
			require.NotPanics(t, func() { err = config.call() })
			require.Error(t, err)
			assert.Contains(t, err.Error(), "missing required field in config: Name")
		})
	}
}

// TestSpringBootConfig_DeleteReportsAMissingName covers the same dereference on the
// delete path, which does not run Validate at all and read the name straight
// out of the config.
func TestSpringBootConfig_DeleteReportsAMissingName(t *testing.T) {
	configs := []struct {
		name string
		call func() error
	}{
		{
			name: "spring boot definition",
			call: func() error {
				config := SpringBootDefinitionConfig{}
				_, err := config.Delete(nil, "")

				return err
			},
		},
		{
			name: "spring boot instance",
			call: func() error {
				config := SpringBootInstanceConfig{}
				_, err := config.Delete(nil, "")

				return err
			},
		},
	}

	for _, config := range configs {
		t.Run(config.name, func(t *testing.T) {
			var err error
			require.NotPanics(t, func() { err = config.call() })
			require.Error(t, err)
			assert.Contains(t, err.Error(), "missing required field in config: Name")
		})
	}
}

// TestSpringBootName covers the helper the operation closures use to describe a
// config in an error. They wrap the failure Validate returns, so they have to
// render a name that is absent.
func TestSpringBootName(t *testing.T) {
	assert.Equal(t, "with name myapp", springBootName(util.Ptr("myapp")))
	assert.Equal(t, "with no name", springBootName(nil))
}

// TestSpringBootConfig_ReportsAMissingNameFromTheCombinedPath covers
// `tptctl springBoot create springBoot -c config.yaml`, which goes through SpringBootConfig
// rather than the definition and instance configs directly. Guarding only the
// inner operations moved the dereference up a level rather than removing it:
// the operation fails, and the wrapper that reports the failure read the name
// the failure is about.
func TestSpringBootConfig_ReportsAMissingNameFromTheCombinedPath(t *testing.T) {
	operations := []struct {
		name string
		call func(config SpringBootConfig) error
	}{
		{
			name: "create",
			call: func(config SpringBootConfig) error {
				_, err := config.Create(nil, "")

				return err
			},
		},
		{
			name: "delete",
			call: func(config SpringBootConfig) error {
				_, err := config.Delete(nil, "")

				return err
			},
		},
	}

	for _, operation := range operations {
		t.Run(operation.name, func(t *testing.T) {
			config := SpringBootConfig{SpringBoot: SpringBootValues{Image: util.Ptr("myorg/myapp:v1")}}

			var err error
			require.NotPanics(t, func() { err = operation.call(config) })
			require.Error(t, err)
			assert.Contains(t, err.Error(), "with no name")
		})
	}
}
