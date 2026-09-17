package kubernetes_clients_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	"capv-proxy/pkg/components/kubernetes_clients"
	"capv-proxy/pkg/strategies"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	workerNameKey                 = "workerName"
	clusterClassNameKey           = "clusterClassName"
	controlPlaneEndpointPortKey   = "controlPlaneEndpointPort"
	controlPlaneEndpointSubnetKey = "controlPlaneEndpointSubnet"
)

var fallbackClusterCreationDefaults = strategies.ClusterCreationDefaults{
	ClusterClassName:           "vates-rhel-from-scratch",
	KubernetesVersion:          "v1.36.3",
	WorkerName:                 "worker-md-0",
	WorkerClass:                "worker-from-scratch",
	ControlPlaneEndpointPort:   6443,
	ControlPlaneEndpointSubnet: 16,
	ControlPlaneLoadBalancer:   "kube-vip",
}

type testConfigMapParams struct {
	data             map[string]string
	missing          bool
	getErr           error
	updatedData      map[string]string
	wantBeforeUpdate *strategies.ClusterCreationDefaults
	want             strategies.ClusterCreationDefaults
	wantErrContains  string
	wantErr          error
}

func TestConfigMapClientGetClusterCreationDefaults(t *testing.T) {
	firstDefaults := defaultsWithWorkerName("first")
	testCases := map[string]testConfigMapParams{
		"missing ConfigMap": {missing: true, wantErrContains: "get cluster defaults ConfigMap"},
		"empty ConfigMap":   {wantErrContains: clusterClassNameKey},
		"partial ConfigMap": {
			data:            map[string]string{workerNameKey: "custom"},
			wantErrContains: clusterClassNameKey,
		},
		"all overrides": {
			data: map[string]string{
				clusterClassNameKey:           "custom-class",
				"kubernetesVersion":           "v1.35.0",
				workerNameKey:                 "workers",
				"workerClass":                 "custom-worker",
				controlPlaneEndpointPortKey:   "8443",
				controlPlaneEndpointSubnetKey: "24",
				"controlPlaneLoadBalancer":    "custom-lb",
			},
			want: strategies.ClusterCreationDefaults{
				ClusterClassName:           "custom-class",
				KubernetesVersion:          "v1.35.0",
				WorkerName:                 "workers",
				WorkerClass:                "custom-worker",
				ControlPlaneEndpointPort:   8443,
				ControlPlaneEndpointSubnet: 24,
				ControlPlaneLoadBalancer:   "custom-lb",
			},
		},
		"empty control-plane endpoint port": {
			data: validConfigMapData(map[string]string{controlPlaneEndpointPortKey: ""}), wantErrContains: controlPlaneEndpointPortKey,
		},
		"non-numeric control-plane endpoint port": {
			data: validConfigMapData(map[string]string{controlPlaneEndpointPortKey: "not-a-port"}), wantErrContains: controlPlaneEndpointPortKey,
		},
		"zero control-plane endpoint port": {
			data: validConfigMapData(map[string]string{controlPlaneEndpointPortKey: "0"}), wantErrContains: controlPlaneEndpointPortKey,
		},
		"out-of-range control-plane endpoint port": {
			data: validConfigMapData(map[string]string{controlPlaneEndpointPortKey: "65536"}), wantErrContains: controlPlaneEndpointPortKey,
		},
		"empty control-plane endpoint subnet": {
			data: validConfigMapData(map[string]string{controlPlaneEndpointSubnetKey: ""}), wantErrContains: controlPlaneEndpointSubnetKey,
		},
		"non-numeric control-plane endpoint subnet": {
			data: validConfigMapData(map[string]string{controlPlaneEndpointSubnetKey: "not-a-subnet"}), wantErrContains: controlPlaneEndpointSubnetKey,
		},
		"negative control-plane endpoint subnet": {
			data: validConfigMapData(map[string]string{controlPlaneEndpointSubnetKey: "-1"}), wantErrContains: controlPlaneEndpointSubnetKey,
		},
		"out-of-range control-plane endpoint subnet": {
			data: validConfigMapData(map[string]string{controlPlaneEndpointSubnetKey: "33"}), wantErrContains: controlPlaneEndpointSubnetKey,
		},
		"Kubernetes read error": {getErr: errTest, wantErr: errTest},
		"reloads defaults on every call": {
			data:             validConfigMapData(map[string]string{workerNameKey: "first"}),
			updatedData:      validConfigMapData(map[string]string{workerNameKey: "second"}),
			wantBeforeUpdate: &firstDefaults,
			want:             defaultsWithWorkerName("second"),
		},
	}

	for name, tc := range testCases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			var objects []client.Object
			if !tc.missing {
				objects = append(objects, defaultsConfigMap(tc.data))
			}
			kubeClient := newFakeClient(t, objects...)
			if tc.getErr != nil {
				kubeClient = newErrorClient(t, interceptor.Funcs{Get: getErrorFunc(t, tc.getErr)})
			}
			configMapClient := kubernetes_clients.NewConfigMapClient(kubeClient, testNamespace)

			got, err := configMapClient.GetClusterCreationDefaults(t.Context())
			if tc.wantErr != nil || tc.wantErrContains != "" {
				require.Error(t, err)
				if tc.wantErr != nil {
					assert.ErrorIs(t, err, tc.wantErr)
				}
				if tc.wantErrContains != "" {
					assert.ErrorContains(t, err, tc.wantErrContains)
				}
				return
			}

			require.NoError(t, err)
			if tc.updatedData == nil {
				assert.Equal(t, tc.want, got)
				return
			}

			require.NotNil(t, tc.wantBeforeUpdate)
			assert.Equal(t, *tc.wantBeforeUpdate, got)
			configMap := defaultsConfigMap(nil)
			require.NoError(t, kubeClient.Get(t.Context(), client.ObjectKeyFromObject(configMap), configMap))
			configMap.Data = tc.updatedData
			require.NoError(t, kubeClient.Update(t.Context(), configMap))

			got, err = configMapClient.GetClusterCreationDefaults(t.Context())
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

func defaultsWithWorkerName(workerName string) strategies.ClusterCreationDefaults {
	defaults := fallbackClusterCreationDefaults
	defaults.WorkerName = workerName
	return defaults
}

func validConfigMapData(overrides map[string]string) map[string]string {
	data := map[string]string{
		clusterClassNameKey:           fallbackClusterCreationDefaults.ClusterClassName,
		"kubernetesVersion":           fallbackClusterCreationDefaults.KubernetesVersion,
		workerNameKey:                 fallbackClusterCreationDefaults.WorkerName,
		"workerClass":                 fallbackClusterCreationDefaults.WorkerClass,
		controlPlaneEndpointPortKey:   "6443",
		controlPlaneEndpointSubnetKey: "16",
		"controlPlaneLoadBalancer":    fallbackClusterCreationDefaults.ControlPlaneLoadBalancer,
	}
	for key, value := range overrides {
		data[key] = value
	}
	return data
}

func defaultsConfigMap(data map[string]string) *corev1.ConfigMap {
	return &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{
		Name: kubernetes_clients.ClusterDefaultsConfigMapName, Namespace: testNamespace,
	}, Data: data}
}
