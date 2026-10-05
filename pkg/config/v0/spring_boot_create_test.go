package v0

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	util "github.com/threeport/threeport/pkg/util/v0"

	api_v0 "spring-boot-threeport-module/pkg/api/v0"
)

// TestSpringBootConfig_Create_CarriesEveryField covers the gap between the
// values a config file can set and the object the API is sent.
//
// GetOperations copies the defined instance's values into a definition config
// field by field. A field left out of that copy is not a compile error and not
// a validation failure: the create succeeds, reports success, and deploys an
// application configured with the API's defaults instead of what the user
// asked for. Only the request body shows it.
func TestSpringBootConfig_Create_CarriesEveryField(t *testing.T) {
	var sentDefinition map[string]interface{}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		path := r.URL.Path

		switch {
		case r.Method == http.MethodPost && strings.Contains(path, "spring-boot-definitions"):
			body, err := io.ReadAll(r.Body)
			require.NoError(t, err)
			require.NoError(t, json.Unmarshal(body, &sentDefinition))
			w.WriteHeader(http.StatusCreated)
			fmt.Fprint(w, `{"Data":[{"ID":3,"Name":"petclinic","CreatedAt":"2026-09-01T00:00:00Z"}]}`)

		case r.Method == http.MethodPost && strings.Contains(path, "spring-boot-instances"):
			w.WriteHeader(http.StatusCreated)
			fmt.Fprint(w, `{"Data":[{"ID":1,"Name":"petclinic","CreatedAt":"2026-09-01T00:00:00Z"}]}`)

		case strings.Contains(path, "spring-boot-definitions"):
			fmt.Fprint(w, `{"Data":[{"ID":3,"Name":"petclinic"}]}`)

		default:
			fmt.Fprint(w, `{"Data":[{"ID":5,"Name":"default-cluster","DefaultRuntime":true}]}`)
		}
	}))
	defer server.Close()

	config := SpringBootConfig{
		SpringBoot: SpringBootValues{
			Name:        util.Ptr("petclinic"),
			Image:       util.Ptr("myorg/petclinic:v1"),
			Profile:     util.Ptr("postgres,prod"),
			ServerPort:  util.Ptr(9000),
			JavaOpts:    util.Ptr("-Xmx512m"),
			Environment: util.Ptr("prod"),
			Replicas:    util.Ptr(2),
			Database:    util.Ptr(api_v0.DatabasePostgres),
			HealthPath:  util.Ptr("/healthz"),
		},
	}

	_, err := config.Create(server.Client(), apiAddr(server))
	require.NoError(t, err)
	require.NotNil(t, sentDefinition, "the definition was never posted")

	for field, want := range map[string]interface{}{
		"Name":        "petclinic",
		"Image":       "myorg/petclinic:v1",
		"Profile":     "postgres,prod",
		"ServerPort":  float64(9000),
		"JavaOpts":    "-Xmx512m",
		"Environment": "prod",
		"Replicas":    float64(2),
		"Database":    api_v0.DatabasePostgres,
		"HealthPath":  "/healthz",
	} {
		assert.Equal(
			t, want, sentDefinition[field],
			field+" was set in the config and did not reach the API",
		)
	}
}
