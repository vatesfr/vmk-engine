package management_kubernetes_client

import (
	"context"
	"errors"
	"maps"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/yaml"

	"capv-proxy/pkg/strategies"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
)

func validConfigMapData(overrides map[string]string) map[string]string {
	data := map[string]string{
		workerNameKey: testWorkerName,
		podCIDRKey:    testPodCIDR, serviceCIDRKey: testServiceCIDR, serviceDomainKey: testDomain,
		controlPlaneKubeadmConfigKey: testBootstrapYAML, controlPlaneMachineSpecKey: testMachineYAML,
		workerMachineSpecKey: testMachineYAML, workerKubeadmConfigKey: testBootstrapYAML,
		endpointPortKey: "6443", endpointSubnetKey: "16", loadBalancerKey: kubeVIP, cniKey: ciliumCNI,
	}
	maps.Copy(data, overrides)
	return data
}

func TestConfigMapClientGetClusterCreationDefaults(t *testing.T) {
	want := strategies.ClusterCreationDefaults{
		WorkerName: testWorkerName,
		PodCIDR:    testPodCIDR, ServiceCIDR: testServiceCIDR, ServiceDomain: testDomain,
		EndpointPort: 6443, EndpointSubnet: 16, LoadBalancer: kubeVIP, CNI: ciliumCNI,
		ControlPlaneMachineSpec: map[string]any{
			"templateID": "image", "poolID": "pool",
			"resourceSet":   map[string]any{"cpus": int64(4)},
			"networkConfig": map[string]any{"networks": []any{map[string]any{"networkID": "network"}}},
		},
		ControlPlaneKubeadmConfig: map[string]any{
			"joinConfiguration": map[string]any{"nodeRegistration": map[string]any{
				"kubeletExtraArgs": []any{map[string]any{nameKey: "cloud-provider", "value": "external"}},
			}},
		},
	}
	want.WorkerMachineSpec = runtime.DeepCopyJSON(want.ControlPlaneMachineSpec)
	want.WorkerKubeadmConfig = runtime.DeepCopyJSON(want.ControlPlaneKubeadmConfig)
	readErr := errors.New("read denied")
	type testConfigMapParams struct {
		data            map[string]string
		missing         bool
		getErr          error
		updatedData     map[string]string
		want            strategies.ClusterCreationDefaults
		wantErrContains string
		wantErr         error
	}
	withoutLoadBalancer := want
	withoutLoadBalancer.LoadBalancer = ""
	updated := want
	updated.WorkerName = "updated-workers"
	testCases := map[string]testConfigMapParams{
		"legacy version key is ignored": {data: validConfigMapData(map[string]string{"kubernetesVersion": "invalid"}), want: want},
		"valid configuration":           {data: validConfigMapData(nil), want: want},
		"empty load balancer":           {data: validConfigMapData(map[string]string{loadBalancerKey: ""}), want: withoutLoadBalancer},
		"missing ConfigMap":             {missing: true, wantErrContains: "get cluster defaults ConfigMap"},
		"empty ConfigMap":               {wantErrContains: workerNameKey},
		"partial ConfigMap":             {data: map[string]string{workerNameKey: "custom"}, wantErrContains: podCIDRKey},
		"Kubernetes read error":         {getErr: readErr, wantErr: readErr, wantErrContains: "get cluster defaults ConfigMap"},
		"reloads defaults on every call": {
			data: validConfigMapData(nil), want: want,
			updatedData: validConfigMapData(map[string]string{workerNameKey: updated.WorkerName}),
		},
	}
	for key := range validConfigMapData(nil) {
		data := validConfigMapData(nil)
		delete(data, key)
		testCases["missing "+key] = testConfigMapParams{data: data, wantErrContains: key}
	}
	for _, field := range []struct {
		key    string
		values map[string]string
	}{
		{endpointPortKey, map[string]string{"empty": "", "non-numeric": "not-a-port", "negative": "-1", "zero": "0", "out-of-range": "65536"}},
		{endpointSubnetKey, map[string]string{"empty": "", "non-numeric": "not-a-subnet", "negative": "-1", "zero": "0", "out-of-range": "33"}},
		{controlPlaneKubeadmConfigKey, map[string]string{"invalid": "bad/name"}},
	} {
		for name, value := range field.values {
			testCases[name+" "+field.key] = testConfigMapParams{
				data: validConfigMapData(map[string]string{field.key: value}), wantErrContains: field.key,
			}
		}
	}
	for name, tc := range testCases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			scheme := runtime.NewScheme()
			require.NoError(t, corev1.AddToScheme(scheme))
			cm := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: ClusterDefaultsConfigMapName, Namespace: testCreationNamespace}, Data: tc.data}
			builder := fake.NewClientBuilder().WithScheme(scheme)
			if !tc.missing {
				builder = builder.WithObjects(cm)
			}
			if tc.getErr != nil {
				builder = builder.WithInterceptorFuncs(interceptor.Funcs{Get: func(context.Context, client.WithWatch, client.ObjectKey, client.Object, ...client.GetOption) error {
					return tc.getErr
				}})
			}
			kube := builder.Build()
			config := NewConfigMapClient(kube, testCreationNamespace)
			got, err := config.GetClusterCreationDefaults(t.Context())
			if tc.wantErr != nil || tc.wantErrContains != "" {
				require.Error(t, err)
				if tc.wantErr != nil {
					require.ErrorIs(t, err, tc.wantErr)
				}
				if tc.wantErrContains != "" {
					require.ErrorContains(t, err, tc.wantErrContains)
				}
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.want, got)
			if tc.updatedData != nil {
				require.NoError(t, kube.Get(t.Context(), client.ObjectKeyFromObject(cm), cm))
				cm.Data = tc.updatedData
				require.NoError(t, kube.Update(t.Context(), cm))
				got, err = config.GetClusterCreationDefaults(t.Context())
				require.NoError(t, err)
				require.Equal(t, updated, got)
			}
		})
	}
}

func TestProviderConfiguration(t *testing.T) {
	valid := validConfigMapData(map[string]string{loadBalancerKey: ""})
	for _, machineSpec := range []string{
		"templateName: almalinux\npoolName: production\n",
		"templateName: almalinux\n",
	} {
		t.Run("provider configuration "+machineSpec, func(t *testing.T) {
			data := make(map[string]string, len(valid))
			for key, value := range valid {
				data[key] = value
			}
			data[controlPlaneMachineSpecKey] = machineSpec
			data[workerMachineSpecKey] = machineSpec
			defaults, err := parseClusterDefaults(data)
			require.NoError(t, err)
			p, _, scheme := newClusterCreationTestSetup(t)
			p.Defaults = defaults
			kube := fake.NewClientBuilder().WithScheme(scheme).Build()
			_, err = NewClusterClient(kube, clientAsCache{kube}, testCreationNamespace).Create(t.Context(), p)
			require.NoError(t, err)
			machine := resource(xoGroup, xoMachineTemplateKind, testControlPlane, testCreationNamespace, nil)
			require.NoError(t, kube.Get(t.Context(), client.ObjectKeyFromObject(machine), machine))
			stored, _, err := unstructured.NestedMap(machine.Object, specKey, templateKey, specKey)
			require.NoError(t, err)
			expected := runtime.DeepCopyJSON(defaults.ControlPlaneMachineSpec)
			expected[namePrefixKey] = p.VMNamePrefix + "-cp"
			require.Equal(t, expected, stored)
		})
	}
}

const testMachineYAML = `templateID: image
poolID: pool
resourceSet:
  cpus: 4
networkConfig:
  networks:
    - networkID: network
`

const testBootstrapYAML = `joinConfiguration:
  nodeRegistration:
    kubeletExtraArgs:
      - name: cloud-provider
        value: external
`

func TestInvalidResourceBlocks(t *testing.T) {
	for _, raw := range []string{"[broken", "[]", "null", "{}", "templateID: one\ntemplateID: two", "apiVersion: example/v1"} {
		t.Run(raw, func(t *testing.T) {
			data := map[string]string{
				workerNameKey: testWorkerName, podCIDRKey: testPodCIDR, serviceCIDRKey: testServiceCIDR, serviceDomainKey: testDomain,
				endpointPortKey: "6443", endpointSubnetKey: "16", loadBalancerKey: kubeVIP, cniKey: ciliumCNI,
				controlPlaneMachineSpecKey: raw, workerMachineSpecKey: testMachineYAML,
				controlPlaneKubeadmConfigKey: testBootstrapYAML, workerKubeadmConfigKey: testBootstrapYAML,
			}
			_, err := parseClusterDefaults(data)
			require.ErrorContains(t, err, controlPlaneMachineSpecKey)
		})
	}
}

func TestPackagedClusterConfiguration(t *testing.T) {
	for _, path := range []string{"../../../charts/go-capi-client/values.yaml", "../../../k8s/deployment.yaml"} {
		t.Run(path, func(t *testing.T) {
			raw, err := os.ReadFile(path)
			require.NoError(t, err)
			var data map[string]string
			for _, document := range strings.Split(string(raw), "\n---\n") {
				var config struct {
					Kind            string            `json:"kind"`
					Data            map[string]string `json:"data"`
					ClusterDefaults map[string]string `json:"clusterDefaults"`
				}
				require.NoError(t, yaml.Unmarshal([]byte(document), &config))
				if config.Kind == "ConfigMap" {
					data = config.Data
				}
				if config.ClusterDefaults != nil {
					data = config.ClusterDefaults
				}
			}
			require.NotEmpty(t, data)
			_, err = parseClusterDefaults(data)
			require.NoError(t, err)
			for key, value := range data {
				for _, placeholder := range []string{"<your-xo-template-uuid>", "<your-xo-pool-uuid>", "<your-xo-network-uuid>"} {
					value = strings.ReplaceAll(value, placeholder, "00000000-0000-4000-8000-000000000001")
				}
				data[key] = value
			}
			defaults, err := parseClusterDefaults(data)
			require.NoError(t, err)
			p, _, scheme := newClusterCreationTestSetup(t)
			p.Defaults = defaults
			p.Version = "v1.36.2"
			kube := fake.NewClientBuilder().WithScheme(scheme).Build()
			_, err = NewClusterClient(kube, clientAsCache{kube}, testCreationNamespace).Create(t.Context(), p)
			require.NoError(t, err)
			for _, target := range []struct {
				group, kind, name string
				path              []string
				count             int
			}{
				{cpGroup, controlPlaneKind, testControlPlane, []string{specKey, kubeadmConfigSpecKey, commandsKey}, 13},
				{bootstrapGroup, bootstrapTemplateKind, "demo-worker", []string{specKey, templateKey, specKey, commandsKey}, 14},
			} {
				object := resource(target.group, target.kind, target.name, testCreationNamespace, nil)
				require.NoError(t, kube.Get(t.Context(), client.ObjectKeyFromObject(object), object))
				commands, _, err := unstructured.NestedStringSlice(object.Object, target.path...)
				require.NoError(t, err)
				require.Len(t, commands, target.count)
				script := strings.Join(commands, "\n")
				require.Contains(t, script, `FULL="v1.36.2"`)
				require.Contains(t, script, "systemctl enable --now containerd")
				require.Contains(t, script, "systemctl enable --now kubelet")
				require.NotContains(t, script, "{{KUBERNETES_VERSION}}")
				require.NotContains(t, script, "{{CONTROL_PLANE_IP}}")
				if target.kind == bootstrapTemplateKind {
					require.Contains(t, script, "arping -I eth0 -c 3 "+p.ControlPlaneIP)
				}
			}
		})
	}
}
