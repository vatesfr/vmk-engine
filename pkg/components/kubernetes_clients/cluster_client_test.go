package kubernetes_clients_test

import (
	"context"
	"testing"

	"capv-proxy/pkg/components/kubernetes_clients"
	"capv-proxy/pkg/strategies"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	capiv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
)

const (
	clusterClassName  = "capv"
	creatingPhase     = "Creating"
	k8sVersion        = "v1.32.0"
	mdName            = "md-a"
	mdClass           = "md-class-a"
	failureDomain     = "fd-1"
	variableRegion    = "region"
	variableSize      = "size"
	variableRegionVal = "eu-west-1"
	variableSizeVal   = "2"
)

type testClusterCreateParams struct {
	params          strategies.CreateClusterResourceParams
	namespace       string
	createErr       error
	oracleErr       error
	wantErrContains string
}

func TestClusterClientCreate(t *testing.T) {
	replicas := int32(3)
	testCases := map[string]testClusterCreateParams{
		"success": {
			params: strategies.CreateClusterResourceParams{
				Name:                 clusterName,
				ClusterClassName:     clusterClassName,
				KubernetesVersion:    k8sVersion,
				ControlPlaneReplicas: &replicas,
				MachineDeployments: []strategies.MachineDeployment{
					{Name: mdName, Class: mdClass, Replicas: &replicas, FailureDomain: failureDomain},
				},
				Variables: map[string]any{variableSize: int64(2), variableRegion: variableRegionVal},
			},
			namespace: testNamespace,
		},
		caseEmptyName: {
			params:          strategies.CreateClusterResourceParams{ClusterClassName: clusterClassName},
			namespace:       testNamespace,
			wantErrContains: errClusterName,
		},
		caseEmptyNamespace: {
			params:          strategies.CreateClusterResourceParams{Name: clusterName},
			wantErrContains: errClusterNamespace,
		},
		"invalid variable encoding error": {
			params: strategies.CreateClusterResourceParams{
				Name:      clusterName,
				Variables: map[string]any{"invalid": make(chan int)},
			},
			namespace:       testNamespace,
			wantErrContains: `encode cluster variable "invalid"`,
		},
		"kubernetes error": {
			params:          strategies.CreateClusterResourceParams{Name: clusterName},
			namespace:       testNamespace,
			createErr:       testError,
			oracleErr:       testError,
			wantErrContains: "create Cluster " + testNamespace + "/" + clusterName,
		},
	}

	for name, tc := range testCases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			kubeClient := newFakeClient(t)
			if tc.createErr != nil {
				kubeClient = newErrorClient(t, interceptor.Funcs{Create: createErrorFunc(tc.createErr)})
			}
			clusterClient := kubernetes_clients.NewClusterClient(kubeClient, tc.namespace)

			info, err := clusterClient.Create(tc.params)

			if tc.wantErrContains != "" {
				assert.Empty(t, info)
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.wantErrContains)
				if tc.oracleErr != nil {
					assert.ErrorIs(t, err, tc.oracleErr)
				}
				return
			}

			require.NoError(t, err)
			assert.Equal(t, strategies.ClusterInfo{Name: clusterName, Phase: creatingPhase}, info)
			assertCreatedCluster(t, kubeClient, tc.namespace, replicas)
		})
	}

	t.Run("cluster already exists", func(t *testing.T) {
		existing := &capiv1.Cluster{ObjectMeta: metav1.ObjectMeta{Name: clusterName, Namespace: testNamespace}}
		clusterClient := kubernetes_clients.NewClusterClient(newFakeClient(t, existing), testNamespace)

		info, err := clusterClient.Create(strategies.CreateClusterResourceParams{Name: clusterName})

		assert.Empty(t, info)
		require.Error(t, err)
		assert.ErrorIs(t, err, strategies.ErrClusterAlreadyExists)
	})
}

func assertCreatedCluster(t *testing.T, kubeClient client.Client, namespace string, replicas int32) {
	t.Helper()

	stored := &capiv1.Cluster{}
	require.NoError(t, kubeClient.Get(context.Background(), types.NamespacedName{Namespace: namespace, Name: clusterName}, stored))
	assert.Equal(t, clusterClassName, stored.Spec.Topology.ClassRef.Name)
	assert.Equal(t, k8sVersion, stored.Spec.Topology.Version)
	require.NotNil(t, stored.Spec.Topology.ControlPlane.Replicas)
	assert.Equal(t, replicas, *stored.Spec.Topology.ControlPlane.Replicas)
	assert.Equal(t, []capiv1.MachineDeploymentTopology{
		{Name: mdName, Class: mdClass, Replicas: &replicas, FailureDomain: failureDomain},
	}, stored.Spec.Topology.Workers.MachineDeployments)
	require.Len(t, stored.Spec.Topology.Variables, 2)
	assert.Equal(t, variableRegion, stored.Spec.Topology.Variables[0].Name)
	assert.Equal(t, variableSize, stored.Spec.Topology.Variables[1].Name)
	assert.JSONEq(t, `"`+variableRegionVal+`"`, string(stored.Spec.Topology.Variables[0].Value.Raw))
	assert.JSONEq(t, variableSizeVal, string(stored.Spec.Topology.Variables[1].Value.Raw))
}

func TestClusterClientList(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		objects := []client.Object{
			&capiv1.Cluster{ObjectMeta: metav1.ObjectMeta{Name: "other", Namespace: "other-namespace"}},
			&capiv1.Cluster{ObjectMeta: metav1.ObjectMeta{Name: "provisioned", Namespace: testNamespace}, Status: capiv1.ClusterStatus{Phase: provisionedPhase}},
			&capiv1.Cluster{ObjectMeta: metav1.ObjectMeta{Name: "provisioning", Namespace: testNamespace}, Status: capiv1.ClusterStatus{Phase: provisioningPhase}},
		}
		clusterClient := kubernetes_clients.NewClusterClient(newFakeClient(t, objects...), testNamespace)

		clusters, err := clusterClient.List()

		require.NoError(t, err)
		assert.ElementsMatch(t, []strategies.ClusterInfo{
			{Name: "provisioned", Phase: provisionedPhase},
			{Name: "provisioning", Phase: provisioningPhase},
		}, clusters)
	})

	t.Run("empty", func(t *testing.T) {
		clusterClient := kubernetes_clients.NewClusterClient(newFakeClient(t), testNamespace)

		clusters, err := clusterClient.List()

		require.NoError(t, err)
		assert.Empty(t, clusters)
		assert.NotNil(t, clusters)
	})

	t.Run("error", func(t *testing.T) {
		clusterClient := kubernetes_clients.NewClusterClient(
			newErrorClient(t, interceptor.Funcs{List: listErrorFunc(testError)}), testNamespace,
		)

		clusters, err := clusterClient.List()

		assert.Nil(t, clusters)
		assert.ErrorIs(t, err, testError)
	})

}

func TestClusterClientGet(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		cluster := &capiv1.Cluster{
			ObjectMeta: metav1.ObjectMeta{Name: clusterName, Namespace: testNamespace},
			Spec:       capiv1.ClusterSpec{ControlPlaneEndpoint: capiv1.APIEndpoint{Host: controlPlaneIP, Port: 6443}},
			Status:     capiv1.ClusterStatus{Phase: provisionedPhase},
		}
		clusterClient := kubernetes_clients.NewClusterClient(newFakeClient(t, cluster), testNamespace)

		info, err := clusterClient.Get(clusterName)

		require.NoError(t, err)
		assert.Equal(t, strategies.ClusterInfo{
			Name: clusterName, Phase: provisionedPhase, ControlPlaneEndpoint: "https://" + controlPlaneIP + ":6443",
		}, info)
	})

	t.Run("invalid endpoint", func(t *testing.T) {
		cluster := &capiv1.Cluster{ObjectMeta: metav1.ObjectMeta{Name: clusterName, Namespace: testNamespace}}
		clusterClient := kubernetes_clients.NewClusterClient(newFakeClient(t, cluster), testNamespace)

		info, err := clusterClient.Get(clusterName)

		require.NoError(t, err)
		assert.Empty(t, info.ControlPlaneEndpoint)
	})

	t.Run("error", func(t *testing.T) {
		clusterClient := kubernetes_clients.NewClusterClient(
			newErrorClient(t, interceptor.Funcs{Get: getErrorFunc(testError)}), testNamespace,
		)

		info, err := clusterClient.Get(clusterName)

		assert.Empty(t, info)
		assert.ErrorIs(t, err, testError)
	})

	t.Run("not found", func(t *testing.T) {
		clusterClient := kubernetes_clients.NewClusterClient(newFakeClient(t), testNamespace)

		info, err := clusterClient.Get(clusterName)

		assert.Empty(t, info)
		require.Error(t, err)
		assert.ErrorIs(t, err, strategies.ErrClusterNotFound)
	})
}

func TestClusterClientDelete(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		cluster := &capiv1.Cluster{ObjectMeta: metav1.ObjectMeta{Name: clusterName, Namespace: testNamespace}}
		kubeClient := newFakeClient(t, cluster)
		clusterClient := kubernetes_clients.NewClusterClient(kubeClient, testNamespace)

		err := clusterClient.Delete(clusterName)

		require.NoError(t, err)
		err = kubeClient.Get(context.Background(), types.NamespacedName{Namespace: testNamespace, Name: clusterName}, &capiv1.Cluster{})
		assert.Error(t, err)
	})

	t.Run("empty name", func(t *testing.T) {
		clusterClient := kubernetes_clients.NewClusterClient(newFakeClient(t), testNamespace)

		err := clusterClient.Delete("")

		assert.EqualError(t, err, errClusterName)
	})

	t.Run("empty namespace", func(t *testing.T) {
		clusterClient := kubernetes_clients.NewClusterClient(newFakeClient(t), "")

		err := clusterClient.Delete(clusterName)

		assert.EqualError(t, err, errClusterNamespace)
	})

	t.Run("error", func(t *testing.T) {
		clusterClient := kubernetes_clients.NewClusterClient(
			newErrorClient(t, interceptor.Funcs{Delete: deleteErrorFunc(testError)}), testNamespace,
		)

		err := clusterClient.Delete(clusterName)

		require.Error(t, err)
		assert.ErrorIs(t, err, testError)
		assert.Contains(t, err.Error(), "delete Cluster "+testNamespace+"/"+clusterName)
	})

	t.Run("not found", func(t *testing.T) {
		clusterClient := kubernetes_clients.NewClusterClient(newFakeClient(t), testNamespace)

		err := clusterClient.Delete(clusterName)

		require.Error(t, err)
		assert.ErrorIs(t, err, strategies.ErrClusterNotFound)
	})
}
