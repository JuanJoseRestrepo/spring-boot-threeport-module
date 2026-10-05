package springboot

import (
	"context"
	"encoding/json"
	"fmt"

	tpclient "github.com/threeport/threeport/pkg/client/v0"
	controller "github.com/threeport/threeport/pkg/controller/v0"
	kube "github.com/threeport/threeport/pkg/kube/v0"
	kubeerrors "k8s.io/apimachinery/pkg/api/errors"
	kubemetav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
)

// secretResource names the Kubernetes API resource this file operates on.
var secretResource = schema.GroupVersionResource{Version: "v1", Resource: "secrets"}

// workloadNamespace returns the namespace Threeport placed a workload instance
// in, or an empty string when it has not placed it yet.
//
// The namespace is not knowable in advance: Threeport builds it from the
// workload instance name plus a random suffix while reconciling, so a caller
// that needs to put something in it has to wait for the resources to exist and
// read the namespace back off them.
func workloadNamespace(
	r *controller.Reconciler,
	workloadInstanceId uint,
) (string, error) {
	resourceInstances, err := tpclient.GetKubernetesWorkloadResourceInstancesByID(
		r.APIClient,
		r.APIServer,
		workloadInstanceId,
	)
	if err != nil {
		return "", fmt.Errorf(
			"failed to get kubernetes workload resource instances for workload instance %d: %w",
			workloadInstanceId, err,
		)
	}

	for _, resourceInstance := range *resourceInstances {
		if resourceInstance.JSONDefinition == nil {
			continue
		}
		var resource map[string]interface{}
		if err := json.Unmarshal([]byte(*resourceInstance.JSONDefinition), &resource); err != nil {
			return "", fmt.Errorf("failed to unmarshal workload resource instance: %w", err)
		}
		metadata, ok := resource["metadata"].(map[string]interface{})
		if !ok {
			continue
		}
		if namespace, ok := metadata["namespace"].(string); ok && namespace != "" {
			return namespace, nil
		}
	}

	return "", nil
}

// runtimeKubeClient returns a client for the Kubernetes runtime a workload was
// deployed to.
func runtimeKubeClient(
	r *controller.Reconciler,
	runtimeInstanceId uint,
) (dynamic.Interface, error) {
	runtimeInstance, err := tpclient.GetKubernetesRuntimeInstanceByID(
		r.APIClient,
		r.APIServer,
		runtimeInstanceId,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to get kubernetes runtime instance %d: %w", runtimeInstanceId, err)
	}

	// true says the caller is a control plane component: when the runtime also
	// hosts the control plane, the endpoint recorded on the runtime instance is
	// the one reachable from outside the cluster, and this controller runs
	// inside it, so it has to go through the in-cluster kube API service
	// instead.
	kubeClient, _, err := kube.GetClient(
		runtimeInstance,
		true,
		r.APIClient,
		r.APIServer,
		r.EncryptionKey,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to build kube client for the runtime: %w", err)
	}

	return kubeClient, nil
}

// ensureSecret creates a Secret in the given namespace if one of that name is
// not already there, and reports whether it created one.
//
// Existing secrets are left untouched rather than overwritten. Reconciliation
// runs again on every requeue, and this secret holds the credential the running
// database was initialised with: rewriting it would hand the application a
// password PostgreSQL no longer accepts, since postgres only reads
// POSTGRES_PASSWORD when it initialises an empty data directory.
func ensureSecret(
	kubeClient dynamic.Interface,
	namespace string,
	name string,
	labels map[string]string,
	data map[string]string,
) (bool, error) {
	_, err := kubeClient.Resource(secretResource).
		Namespace(namespace).
		Get(context.TODO(), name, kubemetav1.GetOptions{})
	if err == nil {
		return false, nil
	}
	if !kubeerrors.IsNotFound(err) {
		return false, fmt.Errorf("failed to check for existing secret %s/%s: %w", namespace, name, err)
	}

	stringData := make(map[string]interface{}, len(data))
	for key, value := range data {
		stringData[key] = value
	}
	secretLabels := make(map[string]interface{}, len(labels))
	for key, value := range labels {
		secretLabels[key] = value
	}

	secret := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "v1",
			"kind":       "Secret",
			"metadata": map[string]interface{}{
				"name":      name,
				"namespace": namespace,
				"labels":    secretLabels,
			},
			"type":       "Opaque",
			"stringData": stringData,
		},
	}

	if _, err := kubeClient.Resource(secretResource).
		Namespace(namespace).
		Create(context.TODO(), secret, kubemetav1.CreateOptions{}); err != nil {
		// another reconcile pass can win the race between the check above and
		// this create, which is the same outcome as finding it already there
		if kubeerrors.IsAlreadyExists(err) {
			return false, nil
		}

		return false, fmt.Errorf("failed to create secret %s/%s: %w", namespace, name, err)
	}

	return true, nil
}

// databaseSecretData returns the credential the PostgreSQL deployment is
// initialised with and the application connects through.
//
// Both read the same secret, so the password cannot drift between them. It is
// generated rather than taken from the user: it never leaves the cluster, and
// asking for one would put a credential in the API and in the user's config
// file.
//
// The connection URL is not here. It names a Service and a database, neither of
// which is secret, and keeping it out means the credential can be rotated
// without also rewriting a connection string that embeds it.
func databaseSecretData() (map[string]string, error) {
	password, err := generatePassword(32)
	if err != nil {
		return nil, fmt.Errorf("failed to generate a database password: %w", err)
	}

	return map[string]string{
		"POSTGRES_DB":       dbName,
		"POSTGRES_USER":     dbUser,
		"POSTGRES_PASSWORD": password,
	}, nil
}
