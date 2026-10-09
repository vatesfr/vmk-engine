package kubernetes_clients

import (
	"testing"

	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	capiv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	"capv-proxy/pkg/strategies"

	xosdk "github.com/vatesfr/xenorchestra-go-sdk/pkg/payloads"
	xok8scommon "github.com/vatesfr/xenorchestra-k8s-common"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	controlPlaneRole = "control-plane"
	readyStatus      = "Ready"
	notReadyStatus   = "NotReady"
	controlPlaneIP   = "10.0.0.1"
	workerIP         = "10.0.0.2"
	externalIP       = "1.1.1.1"
	hostname         = "host.example"
)

func machineWithStatus(name, namespace, cluster string, controlPlane bool, conditionStatus metav1.ConditionStatus, addresses ...capiv1.MachineAddress) *capiv1.Machine {
	labels := map[string]string{capiv1.ClusterNameLabel: cluster}
	if controlPlane {
		labels[capiv1.MachineControlPlaneLabel] = ""
	}
	conditions := []metav1.Condition{}
	if conditionStatus != "" {
		conditions = append(conditions, metav1.Condition{Type: capiv1.MachineReadyCondition, Status: conditionStatus})
	}
	return &capiv1.Machine{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace, Labels: labels},
		Status: capiv1.MachineStatus{
			Addresses:  addresses,
			Conditions: conditions,
		},
	}
}

type testMachineParams struct {
	objects         []client.Object
	namespace       string
	clusterName     string
	want            []strategies.ClusterNodeInfo
	wantErrContains string
}

func TestMachineClientGetMachinesByCluster(t *testing.T) {
	testCases := map[string]testMachineParams{
		"maps machines of the cluster only, sorted by name": {
			objects: []client.Object{
				machineWithStatus("other-machine", testNamespace, "other-cluster", false, ""),
				machineWithStatus("worker-b", testNamespace, clusterName, false, metav1.ConditionTrue,
					capiv1.MachineAddress{Type: capiv1.MachineInternalIP, Address: workerIP}),
				machineWithStatus("control-plane-a", testNamespace, clusterName, true, metav1.ConditionTrue,
					capiv1.MachineAddress{Type: capiv1.MachineInternalIP, Address: controlPlaneIP}),
			},
			namespace:   testNamespace,
			clusterName: clusterName,
			want: []strategies.ClusterNodeInfo{
				{Name: "control-plane-a", Role: controlPlaneRole, Status: readyStatus, Endpoint: nodeEndpoint(controlPlaneIP)},
				{Name: "worker-b", Role: workerRole, Status: readyStatus, Endpoint: nodeEndpoint(workerIP)},
			},
		},
		"empty cluster returns no machine": {
			objects:     nil,
			namespace:   testNamespace,
			clusterName: clusterName,
			want:        []strategies.ClusterNodeInfo{},
		},
		caseEmptyName: {
			objects:         nil,
			namespace:       testNamespace,
			clusterName:     "",
			wantErrContains: errClusterName,
		},
		caseEmptyNamespace: {
			objects:         nil,
			namespace:       "",
			clusterName:     clusterName,
			wantErrContains: errClusterNamespace,
		},
		"not ready condition maps to NotReady": {
			objects: []client.Object{
				machineWithStatus(workerRole, testNamespace, clusterName, false, metav1.ConditionFalse,
					capiv1.MachineAddress{Type: capiv1.MachineInternalIP, Address: workerIP}),
			},
			namespace:   testNamespace,
			clusterName: clusterName,
			want: []strategies.ClusterNodeInfo{
				{Name: workerRole, Role: workerRole, Status: notReadyStatus, Endpoint: nodeEndpoint(workerIP)},
			},
		},
		"unknown ready condition maps to Unknown": {
			objects: []client.Object{
				machineWithStatus(workerRole, testNamespace, clusterName, false, metav1.ConditionUnknown),
			},
			namespace:   testNamespace,
			clusterName: clusterName,
			want: []strategies.ClusterNodeInfo{
				{Name: workerRole, Role: workerRole, Status: unknownStatus},
			},
		},
	}

	for name, tc := range testCases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			machineClient := NewMachineClient(newFakeClient(t, tc.objects...), tc.namespace)
			nodes, err := machineClient.GetMachinesByCluster(t.Context(), tc.clusterName)

			if tc.wantErrContains != "" {
				assert.Nil(t, nodes)
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.wantErrContains)
				if tc.clusterName == "" {
					assert.ErrorIs(t, err, strategies.ErrClusterNameRequired)
				} else {
					assert.ErrorIs(t, err, strategies.ErrClusterNamespaceRequired)
				}
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tc.want, nodes)
		})
	}

	t.Run("kubernetes error", func(t *testing.T) {
		kubeClient := newErrorClient(t, interceptor.Funcs{List: listErrorFunc(t, errTest)})
		machineClient := NewMachineClient(kubeClient, testNamespace)

		nodes, err := machineClient.GetMachinesByCluster(t.Context(), clusterName)

		assert.Nil(t, nodes)
		require.Error(t, err)
		assert.ErrorIs(t, err, errTest)
		assert.Contains(t, err.Error(), "list Machines for cluster "+testNamespace+"/"+clusterName)
	})

	t.Run("node name and phase", func(t *testing.T) {
		machine := machineWithStatus("machine-name", testNamespace, clusterName, false, "")
		machine.Status.NodeRef = capiv1.MachineNodeReference{Name: "node-name"}
		machine.Status.Phase = "Provisioning"
		machineClient := NewMachineClient(newFakeClient(t, machine), testNamespace)

		nodes, err := machineClient.GetMachinesByCluster(t.Context(), clusterName)

		require.NoError(t, err)
		require.Len(t, nodes, 1)
		assert.Equal(t, "node-name", nodes[0].Name)
		assert.Equal(t, "Provisioning", nodes[0].Status)
	})

	t.Run("provider id is decoded into VM uuid", func(t *testing.T) {
		vmID := uuid.Must(uuid.NewV4())
		machine := machineWithStatus("machine-name", testNamespace, clusterName, false, metav1.ConditionTrue)
		machine.Spec.ProviderID = xok8scommon.GetProviderID(uuid.Nil, &xosdk.VM{ID: vmID})
		machineClient := kubernetes_clients.NewMachineClient(newFakeClient(t, machine), testNamespace)

		nodes, err := machineClient.GetMachinesByCluster(t.Context(), clusterName)

		require.NoError(t, err)
		require.Len(t, nodes, 1)
		require.NotNil(t, nodes[0].MachineID)
		assert.Equal(t, vmID, *nodes[0].MachineID)
	})

	t.Run("non-xo provider id is ignored", func(t *testing.T) {
		machine := machineWithStatus("machine-name", testNamespace, clusterName, false, metav1.ConditionTrue)
		machine.Spec.ProviderID = "aws:///us-east-1a/i-1234567890abcdef0"
		machineClient := kubernetes_clients.NewMachineClient(newFakeClient(t, machine), testNamespace)

		nodes, err := machineClient.GetMachinesByCluster(t.Context(), clusterName)

		require.NoError(t, err)
		require.Len(t, nodes, 1)
		assert.Nil(t, nodes[0].MachineID)
	})
}

type testMachineEndpointParams struct {
	addresses []capiv1.MachineAddress
	want      string
}

func TestMachineClientEndpointPriority(t *testing.T) {
	testCases := map[string]testMachineEndpointParams{
		"internal ip wins": {
			addresses: []capiv1.MachineAddress{
				{Type: capiv1.MachineExternalIP, Address: externalIP},
				{Type: capiv1.MachineInternalIP, Address: workerIP},
			},
			want: nodeEndpoint(workerIP),
		},
		"external ip when no internal": {
			addresses: []capiv1.MachineAddress{{Type: capiv1.MachineExternalIP, Address: externalIP}},
			want:      nodeEndpoint(externalIP),
		},
		"hostname when no ip": {
			addresses: []capiv1.MachineAddress{{Type: capiv1.MachineHostName, Address: hostname}},
			want:      nodeEndpoint(hostname),
		},
		"no address returns empty endpoint": {
			addresses: nil,
			want:      "",
		},
		"empty preferred address is ignored": {
			addresses: []capiv1.MachineAddress{
				{Type: capiv1.MachineInternalIP, Address: ""},
				{Type: capiv1.MachineExternalIP, Address: externalIP},
			},
			want: nodeEndpoint(externalIP),
		},
		"ipv6 address is bracketed": {
			addresses: []capiv1.MachineAddress{{Type: capiv1.MachineInternalIP, Address: "2001:db8::1"}},
			want:      "https://[2001:db8::1]:10250",
		},
	}

	for name, tc := range testCases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			machine := machineWithStatus(workerRole, testNamespace, clusterName, false, "", tc.addresses...)
			machineClient := NewMachineClient(newFakeClient(t, machine), testNamespace)

			nodes, err := machineClient.GetMachinesByCluster(t.Context(), clusterName)
			require.NoError(t, err)
			require.Len(t, nodes, 1)
			assert.Equal(t, tc.want, nodes[0].Endpoint)
		})
	}
}

func nodeEndpoint(ip string) string { return "https://" + ip + ":10250" }
