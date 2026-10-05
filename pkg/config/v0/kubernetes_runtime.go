package v0

import (
	"errors"
	"fmt"
	"net/http"

	tpapi_v0 "github.com/threeport/threeport/pkg/api/v0"
	tpclient_v0 "github.com/threeport/threeport/pkg/client/v0"
	tpconfig_v0 "github.com/threeport/threeport/pkg/config/v0"
)

// getKubernetesRuntimeInstanceByNameOrDefault returns the Kubernetes runtime a
// config names, or the control plane's default when it names none.
//
// Threeport has this helper in pkg/config/v0 but does not export it, so it is
// repeated here rather than leaving every caller to resolve the default itself.
// The values type is Threeport's, so a config file names a runtime the same way
// it does for a workload.
func getKubernetesRuntimeInstanceByNameOrDefault(
	apiClient *http.Client,
	apiEndpoint string,
	values *tpconfig_v0.KubernetesRuntimeInstanceValues,
) (*tpapi_v0.KubernetesRuntimeInstance, error) {
	if values != nil && values.Name != nil {
		kubernetesRuntimeInstance, err := tpclient_v0.GetKubernetesRuntimeInstanceByName(
			apiClient,
			apiEndpoint,
			*values.Name,
		)
		if err != nil {
			return nil, fmt.Errorf(
				"failed to get kubernetes runtime instance with name %s: %w",
				*values.Name, err,
			)
		}

		return kubernetesRuntimeInstance, nil
	}

	kubernetesRuntimeInstance, err := tpclient_v0.GetDefaultKubernetesRuntimeInstance(
		apiClient,
		apiEndpoint,
	)
	if err != nil {
		return nil, fmt.Errorf(
			"no kubernetes runtime instance named in config and failed to get the default: %w",
			err,
		)
	}

	return kubernetesRuntimeInstance, nil
}

// getKubernetesRuntimeInstanceForReplace returns the runtime a replace should
// keep the instance on, and whether the config named a different one.
//
// Replace is a full replacement, so a config that omits the runtime would
// otherwise fall back to the default and silently move the workload to another
// cluster - on what looks to the user like an edit to an unrelated field. The
// instance's current runtime is kept instead, and naming a different one is
// reported rather than acted on.
//
// Threeport has getKubernetesRuntimeInstanceAndCheckId for this but does not
// export it, and its boolean reads the other way round: it is true when the
// runtime is unchanged. All five of its call sites treat that as "moved", so
// they reject the ordinary case and fall through to a nil dereference on the
// one they meant to catch. The boolean here is named for what it reports.
func getKubernetesRuntimeInstanceForReplace(
	apiClient *http.Client,
	apiEndpoint string,
	values *tpconfig_v0.KubernetesRuntimeInstanceValues,
	currentId *uint,
) (*tpapi_v0.KubernetesRuntimeInstance, bool, error) {
	if currentId == nil {
		return nil, false, errors.New("the existing spring boot instance has no kubernetes runtime instance")
	}

	current, err := tpclient_v0.GetKubernetesRuntimeInstanceByID(apiClient, apiEndpoint, *currentId)
	if err != nil {
		return nil, false, fmt.Errorf(
			"failed to get the kubernetes runtime instance with ID %d the spring boot instance is on: %w",
			*currentId, err,
		)
	}

	// no runtime named: the instance stays where it is
	if values == nil || values.Name == nil || *values.Name == "" {
		return current, false, nil
	}

	named, err := tpclient_v0.GetKubernetesRuntimeInstanceByName(apiClient, apiEndpoint, *values.Name)
	if err != nil {
		return nil, false, fmt.Errorf(
			"failed to get kubernetes runtime instance with name %s: %w",
			*values.Name, err,
		)
	}

	if named.ID == nil || *named.ID != *currentId {
		return named, true, nil
	}

	return current, false, nil
}
