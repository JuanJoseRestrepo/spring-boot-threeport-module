package springboot

import (
	"testing"

	"github.com/stretchr/testify/assert"
	util "github.com/threeport/threeport/pkg/util/v0"
	"k8s.io/apimachinery/pkg/api/resource"

	v0 "spring-boot-threeport-module/pkg/api/v0"
)

// TestResourcesFor covers the defaulting rule. CPU and memory are defaulted as
// units: a definition that states either memory field keeps memory to itself,
// because filling in only the other side can produce a request above a limit,
// which the kube API rejects outright.
func TestResourcesFor(t *testing.T) {
	tests := []struct {
		name                                             string
		definition                                       v0.SpringBootDefinition
		environment                                      string
		cpuRequest, cpuLimit, memoryRequest, memoryLimit string
	}{
		{
			name:          "nothing stated takes the development sizing",
			environment:   "dev",
			cpuRequest:    "250m",
			memoryRequest: "512Mi",
			memoryLimit:   "1Gi",
		},
		{
			name:          "prod is sized larger",
			environment:   "prod",
			cpuRequest:    "500m",
			memoryRequest: "1Gi",
			memoryLimit:   "2Gi",
		},
		{
			name:          "a stated memory limit leaves memory alone",
			definition:    v0.SpringBootDefinition{MemoryLimit: util.Ptr("4Gi")},
			environment:   "dev",
			cpuRequest:    "250m",
			memoryLimit:   "4Gi",
			memoryRequest: "",
		},
		{
			name:          "a stated memory request leaves memory alone",
			definition:    v0.SpringBootDefinition{MemoryRequest: util.Ptr("4Gi")},
			environment:   "dev",
			cpuRequest:    "250m",
			memoryRequest: "4Gi",
			memoryLimit:   "",
		},
		{
			name:          "a stated cpu limit leaves cpu alone",
			definition:    v0.SpringBootDefinition{CpuLimit: util.Ptr("2")},
			environment:   "dev",
			cpuLimit:      "2",
			cpuRequest:    "",
			memoryRequest: "512Mi",
			memoryLimit:   "1Gi",
		},
		{
			name: "everything stated is left untouched",
			definition: v0.SpringBootDefinition{
				CpuRequest:    util.Ptr("100m"),
				CpuLimit:      util.Ptr("200m"),
				MemoryRequest: util.Ptr("128Mi"),
				MemoryLimit:   util.Ptr("256Mi"),
			},
			environment:   "prod",
			cpuRequest:    "100m",
			cpuLimit:      "200m",
			memoryRequest: "128Mi",
			memoryLimit:   "256Mi",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			definition := test.definition
			cpuRequest, cpuLimit, memoryRequest, memoryLimit := resourcesFor(&definition, test.environment)

			assert.Equal(t, test.cpuRequest, cpuRequest, "cpu request")
			assert.Equal(t, test.cpuLimit, cpuLimit, "cpu limit")
			assert.Equal(t, test.memoryRequest, memoryRequest, "memory request")
			assert.Equal(t, test.memoryLimit, memoryLimit, "memory limit")
		})
	}
}

// TestResourcesByEnv_DefaultsNeverExceedTheirLimit covers the defaults being
// internally consistent: a request above its own limit is rejected by the kube
// API, so a bad pair here would make every default deployment fail.
func TestResourcesByEnv_DefaultsNeverExceedTheirLimit(t *testing.T) {
	for _, env := range []string{"dev", "prod", "staging"} {
		defaults := resourcesByEnv(env)
		assert.NotEmpty(t, defaults.memoryLimit, env+" must default a memory limit")
		assert.NotEmpty(t, defaults.memoryRequest, env+" must default a memory request")
		assert.NotEmpty(t, defaults.cpuRequest, env+" must default a cpu request")

		request := mustQuantity(t, defaults.memoryRequest)
		limit := mustQuantity(t, defaults.memoryLimit)
		assert.LessOrEqual(t, request, limit, env+" defaults a memory request above its own limit")
	}
}

// TestResourcesByEnv_NoCpuLimit covers the deliberate absence. A JVM is at its
// most CPU-hungry while the application context starts, so a limit low enough
// to matter later throttles it exactly then and turns a slow start into a
// failed one.
func TestResourcesByEnv_NoCpuLimit(t *testing.T) {
	for _, env := range []string{"dev", "prod"} {
		definition := v0.SpringBootDefinition{}
		_, cpuLimit, _, _ := resourcesFor(&definition, env)
		assert.Empty(t, cpuLimit, env+" must not default a cpu limit")
	}
}

// mustQuantity parses a Kubernetes quantity into bytes for comparison.
func mustQuantity(t *testing.T, value string) int64 {
	t.Helper()

	quantity, err := resource.ParseQuantity(value)
	if err != nil {
		t.Fatalf("default %q is not a valid quantity: %v", value, err)
	}

	return quantity.Value()
}
