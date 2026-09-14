package kubernetes_clients_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	capiv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
)

const (
	clusterName       = "test-cluster"
	testNamespace     = "capv"
	provisionedPhase  = "Provisioned"
	provisioningPhase = "Provisioning"
	workerRole        = "worker"
	readyStatus       = "Ready"
	notReadyStatus    = "NotReady"
	controlPlaneIP    = "10.0.0.1"
	workerIP          = "10.0.0.2"
	externalIP        = "1.1.1.1"
	hostname          = "host.example"

	caseEmptyName       = "empty name"
	caseEmptyNamespace  = "empty namespace"
	errClusterName      = "cluster name is required"
	errClusterNamespace = "cluster namespace is required"
	errNotFound         = "not found"
)

var errTest = errors.New("test error")

func nodeEndpoint(ip string) string { return "https://" + ip + ":10250" }

func newTestScheme(t *testing.T) *runtime.Scheme {
	t.Helper()

	scheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		t.Fatalf("register Kubernetes types: %v", err)
	}
	if err := capiv1.AddToScheme(scheme); err != nil {
		t.Fatalf("register Cluster API types: %v", err)
	}
	return scheme
}

func newFakeClient(t *testing.T, objects ...client.Object) client.Client {
	t.Helper()

	return fake.NewClientBuilder().
		WithScheme(newTestScheme(t)).
		WithObjects(objects...).
		Build()
}

func newErrorClient(t *testing.T, funcs interceptor.Funcs) client.Client {
	t.Helper()

	return fake.NewClientBuilder().
		WithScheme(newTestScheme(t)).
		WithInterceptorFuncs(funcs).
		Build()
}

func createErrorFunc(t *testing.T, err error) func(context.Context, client.WithWatch, client.Object, ...client.CreateOption) error {
	t.Helper()
	return func(ctx context.Context, _ client.WithWatch, _ client.Object, _ ...client.CreateOption) error {
		require.Same(t, t.Context(), ctx)
		return err
	}
}

func getErrorFunc(t *testing.T, err error) func(context.Context, client.WithWatch, client.ObjectKey, client.Object, ...client.GetOption) error {
	t.Helper()
	return func(ctx context.Context, _ client.WithWatch, _ client.ObjectKey, _ client.Object, _ ...client.GetOption) error {
		require.Same(t, t.Context(), ctx)
		return err
	}
}

func listErrorFunc(t *testing.T, err error) func(context.Context, client.WithWatch, client.ObjectList, ...client.ListOption) error {
	t.Helper()
	return func(ctx context.Context, _ client.WithWatch, _ client.ObjectList, _ ...client.ListOption) error {
		require.Same(t, t.Context(), ctx)
		return err
	}
}

func deleteErrorFunc(t *testing.T, err error) func(context.Context, client.WithWatch, client.Object, ...client.DeleteOption) error {
	t.Helper()
	return func(ctx context.Context, _ client.WithWatch, _ client.Object, _ ...client.DeleteOption) error {
		require.Same(t, t.Context(), ctx)
		return err
	}
}
