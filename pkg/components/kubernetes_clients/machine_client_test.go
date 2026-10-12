package kubernetes_clients

import (
	"context"
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

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

const (
	resolvedNodeName  = "node-name"
	deleteMachineName = "machine"
	otherNamespace    = "other-namespace"
	controlPlaneRole  = "control-plane"
	readyStatus       = "Ready"
	notReadyStatus    = "NotReady"
	controlPlaneIP    = "10.0.0.1"
	workerIP          = "10.0.0.2"
	externalIP        = "1.1.1.1"
	hostname          = "host.example"
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
		machine.Status.NodeRef = capiv1.MachineNodeReference{Name: resolvedNodeName}
		machine.Status.Phase = "Provisioning"
		machineClient := NewMachineClient(newFakeClient(t, machine), testNamespace)

		nodes, err := machineClient.GetMachinesByCluster(t.Context(), clusterName)

		require.NoError(t, err)
		require.Len(t, nodes, 1)
		assert.Equal(t, "machine-name", nodes[0].Name)
		assert.Equal(t, resolvedNodeName, nodes[0].NodeName)
		assert.Equal(t, "Provisioning", nodes[0].Status)
	})

	t.Run("provider id is decoded into VM uuid", func(t *testing.T) {
		vmID := uuid.Must(uuid.NewV4())
		machine := machineWithStatus("machine-name", testNamespace, clusterName, false, metav1.ConditionTrue)
		machine.Spec.ProviderID = xok8scommon.GetProviderID(uuid.Nil, &xosdk.VM{ID: vmID})
		machineClient := NewMachineClient(newFakeClient(t, machine), testNamespace)

		nodes, err := machineClient.GetMachinesByCluster(t.Context(), clusterName)

		require.NoError(t, err)
		require.Len(t, nodes, 1)
		require.NotNil(t, nodes[0].MachineID)
		assert.Equal(t, vmID, *nodes[0].MachineID)
	})

	t.Run("non-xo provider id is ignored", func(t *testing.T) {
		machine := machineWithStatus("machine-name", testNamespace, clusterName, false, metav1.ConditionTrue)
		machine.Spec.ProviderID = "aws:///us-east-1a/i-1234567890abcdef0"
		machineClient := NewMachineClient(newFakeClient(t, machine), testNamespace)

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

type testAllMachinesParams struct {
	objects         []client.Object
	namespace       string
	listErr         error
	want            []strategies.ClusterNodeInfo
	wantErr         error
	wantErrContains string
}

func TestMachineClientGetAllMachines(t *testing.T) {
	first := machineWithStatus("z-machine", testNamespace, clusterName, true, metav1.ConditionTrue,
		capiv1.MachineAddress{Type: capiv1.MachineInternalIP, Address: controlPlaneIP})
	first.Status.NodeRef = capiv1.MachineNodeReference{Name: "a-node"}
	second := machineWithStatus("a-machine", testNamespace, "other-cluster", false, metav1.ConditionFalse,
		capiv1.MachineAddress{Type: capiv1.MachineExternalIP, Address: externalIP})
	second.Status.NodeRef = capiv1.MachineNodeReference{Name: "z-node"}
	unlabelled := machineWithStatus("unlabelled", testNamespace, "", false, "")
	unlabelled.Labels = nil
	unlabelled.Status.Phase = provisioningPhase

	testCases := map[string]testAllMachinesParams{
		"maps machines across clusters in the configured namespace sorted by node name": {
			objects: []client.Object{
				second, first, unlabelled,
				machineWithStatus("excluded", otherNamespace, clusterName, false, metav1.ConditionTrue),
			},
			namespace: testNamespace,
			want: []strategies.ClusterNodeInfo{
				{Name: "a-machine", NodeName: "z-node", Role: workerRole, Status: notReadyStatus, Endpoint: nodeEndpoint(externalIP)},
				{Name: "unlabelled", Role: workerRole, Status: provisioningPhase},
				{Name: "z-machine", NodeName: "a-node", Role: controlPlaneRole, Status: readyStatus, Endpoint: nodeEndpoint(controlPlaneIP)},
			},
		},
		"empty namespace returns an empty slice": {
			objects: []client.Object{
				machineWithStatus("excluded", otherNamespace, clusterName, false, ""),
			},
			namespace: testNamespace,
			want:      []strategies.ClusterNodeInfo{},
		},
		"namespace is required before calling Kubernetes": {
			wantErr: strategies.ErrClusterNamespaceRequired,
		},
		"Kubernetes error preserves cause and namespace": {
			namespace:       testNamespace,
			listErr:         errTest,
			wantErr:         errTest,
			wantErrContains: "list Machines in namespace " + testNamespace,
		},
	}

	for name, tc := range testCases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			var kubeClient client.Client
			if tc.namespace != "" {
				if tc.listErr != nil {
					kubeClient = newErrorClient(t, interceptor.Funcs{List: listErrorFunc(t, tc.listErr)})
				} else {
					kubeClient = newFakeClient(t, tc.objects...)
				}
			}
			machineClient := NewMachineClient(kubeClient, tc.namespace)

			nodes, err := machineClient.GetAllMachines(t.Context())

			if tc.wantErr != nil {
				assert.Nil(t, nodes)
				require.Error(t, err)
				assert.ErrorIs(t, err, tc.wantErr)
				if tc.wantErrContains != "" {
					assert.Contains(t, err.Error(), tc.wantErrContains)
				}
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tc.want, nodes)
		})
	}
}

func nodeEndpoint(ip string) string { return "https://" + ip + ":10250" }

func TestMachineClientDeleteMachine(t *testing.T) {
	for name, tc := range map[string]struct {
		namespace, cluster, machine, owner string
		wantErr                            error
	}{
		"deletes matching machine":        {testNamespace, clusterName, deleteMachineName, clusterName, nil},
		"other cluster is protected":      {testNamespace, clusterName, deleteMachineName, "other-cluster", strategies.ErrNodeNotFound},
		"unassigned machine is protected": {testNamespace, clusterName, deleteMachineName, "", strategies.ErrNodeNotFound},
		"missing machine":                 {testNamespace, clusterName, "missing", clusterName, strategies.ErrNodeNotFound},
		"other namespace is protected":    {otherNamespace, clusterName, deleteMachineName, clusterName, strategies.ErrNodeNotFound},
		"cluster required":                {testNamespace, "", deleteMachineName, clusterName, strategies.ErrClusterNameRequired},
		"machine required":                {testNamespace, clusterName, "", clusterName, strategies.ErrNodeNameRequired},
		"namespace required":              {"", clusterName, deleteMachineName, clusterName, strategies.ErrClusterNamespaceRequired},
	} {
		t.Run(name, func(t *testing.T) {
			// The label deliberately matches the requested cluster, even when the
			// spec assigns the machine elsewhere. Labels must not authorize deletion.
			machine := machineWithStatus(deleteMachineName, testNamespace, clusterName, false, "")
			machine.Spec.ClusterName = tc.owner
			kube := newFakeClient(t, machine)
			err := NewMachineClient(kube, tc.namespace).DeleteMachine(t.Context(), tc.cluster, tc.machine)
			if tc.wantErr != nil {
				require.ErrorIs(t, err, tc.wantErr)
				require.NoError(t, kube.Get(t.Context(), client.ObjectKeyFromObject(machine), &capiv1.Machine{}))
			} else {
				require.NoError(t, err)
				assert.True(t, apierrors.IsNotFound(kube.Get(t.Context(), client.ObjectKeyFromObject(machine), &capiv1.Machine{})))
			}
		})
	}
}

func TestMachineClientDeleteErrorsAndPreconditions(t *testing.T) {
	missing := apierrors.NewNotFound(schema.GroupResource{Group: capiv1.GroupVersion.Group, Resource: "machines"}, deleteMachineName)
	for name, tc := range map[string]struct {
		getErr, deleteErr, wantErr error
	}{
		"get failure":   {getErr: errTest, wantErr: errTest},
		"get not found": {getErr: missing, wantErr: strategies.ErrNodeNotFound},

		"delete failure":         {deleteErr: errTest, wantErr: errTest},
		"disappeared after read": {deleteErr: missing, wantErr: strategies.ErrNodeNotFound},
		"concurrent update":      {deleteErr: apierrors.NewConflict(schema.GroupResource{Resource: "machines"}, deleteMachineName, errTest), wantErr: strategies.ErrNodeDeletionConflict},
		"accepted deletion":      {},
	} {
		t.Run(name, func(t *testing.T) {
			deleted := false
			kube := newErrorClient(t, interceptor.Funcs{
				Get: func(_ context.Context, _ client.WithWatch, key client.ObjectKey, obj client.Object, _ ...client.GetOption) error {
					assert.Equal(t, client.ObjectKey{Namespace: testNamespace, Name: deleteMachineName}, key)
					if tc.getErr != nil {
						return tc.getErr
					}
					machine := obj.(*capiv1.Machine)
					machine.ObjectMeta = metav1.ObjectMeta{Name: key.Name, Namespace: key.Namespace, UID: "original-uid", ResourceVersion: "123"}
					machine.Spec.ClusterName = clusterName
					return nil
				},
				Delete: func(_ context.Context, _ client.WithWatch, _ client.Object, opts ...client.DeleteOption) error {
					deleted = true
					options := &client.DeleteOptions{}
					for _, opt := range opts {
						opt.ApplyToDelete(options)
					}
					require.NotNil(t, options.Preconditions)
					assert.EqualValues(t, "original-uid", *options.Preconditions.UID)
					assert.Equal(t, "123", *options.Preconditions.ResourceVersion)
					return tc.deleteErr
				},
			})
			err := NewMachineClient(kube, testNamespace).DeleteMachine(t.Context(), clusterName, deleteMachineName)
			if tc.wantErr == nil {
				require.NoError(t, err)
			} else {
				require.ErrorIs(t, err, tc.wantErr)
			}
			assert.Equal(t, tc.getErr == nil, deleted)
		})
	}
}

func TestMachineClientDeleteRetainsFinalizers(t *testing.T) {
	machine := machineWithStatus(deleteMachineName, testNamespace, clusterName, false, "")
	machine.Spec.ClusterName = clusterName
	machine.Finalizers = []string{"cluster.x-k8s.io/machine"}
	kube := newFakeClient(t, machine)
	require.NoError(t, NewMachineClient(kube, testNamespace).DeleteMachine(t.Context(), clusterName, machine.Name))
	remaining := &capiv1.Machine{}
	require.NoError(t, kube.Get(t.Context(), client.ObjectKeyFromObject(machine), remaining))
	assert.False(t, remaining.DeletionTimestamp.IsZero())
	assert.Equal(t, machine.Finalizers, remaining.Finalizers)
}

func TestDeleteMachineUsesResourceName(t *testing.T) {
	for name, tc := range map[string]struct {
		requested string
		duplicate bool
		wantErr   error
	}{
		"resource name deletes machine":             {requested: deleteMachineName},
		"node name is not a resource alias":         {requested: resolvedNodeName, wantErr: strategies.ErrNodeNotFound},
		"shared node name does not change identity": {requested: deleteMachineName, duplicate: true},
	} {
		t.Run(name, func(t *testing.T) {
			machine := machineWithStatus(deleteMachineName, testNamespace, clusterName, false, "")
			machine.Spec.ClusterName = clusterName
			machine.Status.NodeRef = capiv1.MachineNodeReference{Name: resolvedNodeName}
			objects := []client.Object{machine}
			other := machine.DeepCopy()
			other.Name = "other-machine"
			other.Spec.ClusterName = "other-cluster"
			objects = append(objects, other)
			if tc.duplicate {
				duplicate := machine.DeepCopy()
				duplicate.Name = "duplicate-machine"
				objects = append(objects, duplicate)
			}
			kube := newFakeClient(t, objects...)
			err := NewMachineClient(kube, testNamespace).DeleteMachine(t.Context(), clusterName, tc.requested)
			if tc.wantErr == nil {
				require.NoError(t, err)
				assert.True(t, apierrors.IsNotFound(kube.Get(t.Context(), client.ObjectKeyFromObject(machine), &capiv1.Machine{})))
			} else {
				require.ErrorIs(t, err, tc.wantErr)
				require.NoError(t, kube.Get(t.Context(), client.ObjectKeyFromObject(machine), &capiv1.Machine{}))
			}
			require.NoError(t, kube.Get(t.Context(), client.ObjectKeyFromObject(other), &capiv1.Machine{}))
		})
	}
}
