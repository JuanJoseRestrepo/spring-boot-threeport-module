package v0

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	util "github.com/threeport/threeport/pkg/util/v0"
)

// apiAddr returns the address in the form the generated client expects: it adds
// the scheme itself, so passing the server URL whole yields http://http//...
func apiAddr(server *httptest.Server) string {
	return strings.TrimPrefix(server.URL, "http://")
}

// withShortDeletionWait shrinks the wait so a test can reach the timeout.
func withShortDeletionWait(t *testing.T) {
	t.Helper()

	attempts, seconds := deletionWaitAttempts, deletionWaitSeconds
	deletionWaitAttempts, deletionWaitSeconds = 3, 0
	t.Cleanup(func() { deletionWaitAttempts, deletionWaitSeconds = attempts, seconds })
}

// instanceAPI serves the two calls Delete makes: the lookup that finds the
// instance, the delete itself, and then the polling lookups. getStatus decides
// what each polling lookup answers.
func instanceAPI(t *testing.T, getStatus func(call int) (int, string)) *httptest.Server {
	t.Helper()

	found := `{"Data":[{"ID":1,"Name":"myapp"}]}`
	var gets int32

	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		if r.Method == http.MethodDelete {
			w.WriteHeader(http.StatusOK)
			fmt.Fprint(w, `{"Data":[{"ID":1,"Name":"myapp"}]}`)
			return
		}

		// the first GET is Delete finding the instance to remove; the ones
		// after it are the wait checking whether it is gone
		call := int(atomic.AddInt32(&gets, 1))
		if call == 1 {
			w.WriteHeader(http.StatusOK)
			fmt.Fprint(w, found)
			return
		}

		status, body := getStatus(call - 1)
		w.WriteHeader(status)
		fmt.Fprint(w, body)
	}))
}

// TestSpringBootInstanceConfig_Delete_WaitsForNotFound covers the ordinary path:
// the instance lingers for a couple of polls and then reports not found.
func TestSpringBootInstanceConfig_Delete_WaitsForNotFound(t *testing.T) {
	withShortDeletionWait(t)

	server := instanceAPI(t, func(call int) (int, string) {
		if call == 1 {
			return http.StatusOK, `{"Data":[{"ID":1,"Name":"myapp"}]}`
		}
		return http.StatusNotFound, `{"Error":{"Message":"object not found"}}`
	})
	defer server.Close()

	config := SpringBootInstanceConfig{SpringBootInstance: SpringBootInstanceValues{Name: util.Ptr("myapp")}}
	_, err := config.Delete(server.Client(), apiAddr(server))
	assert.NoError(t, err)
}

// TestSpringBootInstanceConfig_Delete_DoesNotTreatApiErrorsAsDeleted is the point of
// the wait. A failing API says nothing about whether the row is gone, and
// reporting success would let a caller go on to delete the definition the
// instance still refers to.
func TestSpringBootInstanceConfig_Delete_DoesNotTreatApiErrorsAsDeleted(t *testing.T) {
	withShortDeletionWait(t)

	server := instanceAPI(t, func(int) (int, string) {
		return http.StatusInternalServerError, `{"Error":{"Message":"boom"}}`
	})
	defer server.Close()

	config := SpringBootInstanceConfig{SpringBootInstance: SpringBootInstanceValues{Name: util.Ptr("myapp")}}
	_, err := config.Delete(server.Client(), apiAddr(server))
	require.Error(t, err, "an api that cannot answer must not be read as a successful deletion")
	assert.Contains(t, err.Error(), "gave up waiting")
}

// TestSpringBootInstanceConfig_Delete_ReportsATimeout covers an instance that never
// goes away. Discarding the retry error let Delete return success here.
func TestSpringBootInstanceConfig_Delete_ReportsATimeout(t *testing.T) {
	withShortDeletionWait(t)

	server := instanceAPI(t, func(int) (int, string) {
		return http.StatusOK, `{"Data":[{"ID":1,"Name":"myapp"}]}`
	})
	defer server.Close()

	config := SpringBootInstanceConfig{SpringBootInstance: SpringBootInstanceValues{Name: util.Ptr("myapp")}}
	_, err := config.Delete(server.Client(), apiAddr(server))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "gave up waiting")
}
