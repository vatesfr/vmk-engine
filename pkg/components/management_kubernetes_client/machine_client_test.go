package management_kubernetes_client

import (
	"context"
	"testing"

	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	capiv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	"capv-proxy/pkg/helpers/uuids"
	"capv-proxy/pkg/strategies"

	xosdk "github.com/vatesfr/xenorchestra-go-sdk/pkg/payloads"
	xok8scommon "github.com/vatesfr/xenorchestra-k8s-common"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

const (
	resolvedNodeName     = "node-name"
	deleteMachineName    = "machine"
	duplicateMachineName = "duplicate-machine"
	otherNamespace       = "other-namespace"
	otherClusterName     = "other-cluster"
	controlPlaneRole     = "control-plane"
	readyStatus          = "Ready"
	notReadyStatus       = "NotReady"
	controlPlaneIP       = "10.0.0.1"
	workerIP             = "10.0.0.2"
	externalIP           = "1.1.1.1"
	hostname             = "host.example"
)

var deleteMachineUUID = uuids.NodeID(clusterName, deleteMachineName)

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
		Spec:       capiv1.MachineSpec{ClusterName: cluster},
		Status: capiv1.MachineStatus{
			Addresses:  addresses,
			Conditions: conditions,
		},
	}
}

type testMachineParams struct {
	objects         []client.Object
	namespace       string
	clusterID       uuid.UUID
	want            []strategies.ClusterNodeInfo
	wantErrContains string
}

func TestMachineClientGetMachinesByCluster(t *testing.T) {
	testCases := map[string]testMachineParams{
		"maps machines of the cluster only, sorted by name": {
			objects: []client.Object{
				machineWithStatus("other-machine", testNamespace, otherClusterName, false, ""),
				machineWithStatus("worker-b", testNamespace, clusterName, false, metav1.ConditionTrue,
					capiv1.MachineAddress{Type: capiv1.MachineInternalIP, Address: workerIP}),
				machineWithStatus("control-plane-a", testNamespace, clusterName, true, metav1.ConditionTrue,
					capiv1.MachineAddress{Type: capiv1.MachineInternalIP, Address: controlPlaneIP}),
			},
			namespace: testNamespace,
			clusterID: clusterUUID,
			want: []strategies.ClusterNodeInfo{
				{
					ID: uuids.NodeID(clusterName, "control-plane-a"), Name: "control-plane-a", Role: controlPlaneRole, Status: readyStatus,
					IPs: []string{controlPlaneIP}, ParentCluster: clusterUUID, Version: unknownVersion,
				},
				{ID: uuids.NodeID(clusterName, "worker-b"), Name: "worker-b", Role: workerRole, Status: readyStatus, IPs: []string{workerIP}, ParentCluster: clusterUUID, Version: unknownVersion},
			},
		},
		"empty cluster returns no machine": {
			objects:   nil,
			namespace: testNamespace,
			clusterID: clusterUUID,
			want:      []strategies.ClusterNodeInfo{},
		},
		caseEmptyName: {
			objects:         nil,
			namespace:       testNamespace,
			clusterID:       uuid.Nil,
			wantErrContains: "cluster ID is required",
		},
		caseEmptyNamespace: {
			objects:         nil,
			namespace:       "",
			clusterID:       clusterUUID,
			wantErrContains: errClusterNamespace,
		},
		"not ready condition maps to NotReady": {
			objects: []client.Object{
				machineWithStatus(workerRole, testNamespace, clusterName, false, metav1.ConditionFalse,
					capiv1.MachineAddress{Type: capiv1.MachineInternalIP, Address: workerIP}),
			},
			namespace: testNamespace,
			clusterID: clusterUUID,
			want: []strategies.ClusterNodeInfo{
				{ID: uuids.NodeID(clusterName, workerRole), Name: workerRole, Role: workerRole, Status: notReadyStatus, IPs: []string{workerIP}, ParentCluster: clusterUUID, Version: unknownVersion},
			},
		},
		"unknown ready condition maps to Unknown": {
			objects: []client.Object{
				machineWithStatus(workerRole, testNamespace, clusterName, false, metav1.ConditionUnknown),
			},
			namespace: testNamespace,
			clusterID: clusterUUID,
			want: []strategies.ClusterNodeInfo{
				{ID: uuids.NodeID(clusterName, workerRole), Name: workerRole, Role: workerRole, Status: unknownStatus, ParentCluster: clusterUUID, Version: unknownVersion},
			},
		},
	}

	for name, tc := range testCases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			machineClient := NewMachineClient(nil, newFakeCache(t, tc.objects...), tc.namespace)
			nodes, err := machineClient.GetMachinesByCluster(t.Context(), tc.clusterID)

			if tc.wantErrContains != "" {
				assert.Nil(t, nodes)
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.wantErrContains)
				if tc.clusterID == uuid.Nil {
					assert.ErrorIs(t, err, strategies.ErrClusterIDRequired)
				} else {
					assert.ErrorIs(t, err, strategies.ErrClusterNamespaceRequired)
				}
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tc.want, nodes)
		})
	}

	t.Run("spec cluster name controls indexes when label differs", func(t *testing.T) {
		machine := machineWithStatus("machine-name", testNamespace, clusterName, false, "")
		machine.Labels[capiv1.ClusterNameLabel] = otherClusterName
		fc := newFakeClient(t, machine)
		machineClient := NewMachineClient(fc, clientAsCache{fc}, testNamespace)

		nodes, err := machineClient.GetMachinesByCluster(t.Context(), clusterUUID)
		require.NoError(t, err)
		require.Len(t, nodes, 1)
		assert.Equal(t, uuids.NodeID(clusterName, machine.Name), nodes[0].ID)
		assert.Equal(t, clusterUUID, nodes[0].ParentCluster)

		node, err := machineClient.GetMachineByUUID(t.Context(), nodes[0].ID)
		require.NoError(t, err)
		assert.Equal(t, nodes[0].ID, node.ID)

		otherNodes, err := machineClient.GetMachinesByCluster(t.Context(), uuids.ClusterID(otherClusterName))
		require.NoError(t, err)
		assert.Empty(t, otherNodes)
	})

	t.Run("kubernetes error", func(t *testing.T) {
		kubeClient := newErrorClient(t, interceptor.Funcs{List: listErrorFunc(t, errTest)})
		machineClient := NewMachineClient(kubeClient, clientAsCache{kubeClient}, testNamespace)

		nodes, err := machineClient.GetMachinesByCluster(t.Context(), clusterUUID)

		assert.Nil(t, nodes)
		require.Error(t, err)
		assert.ErrorIs(t, err, errTest)
		assert.Contains(t, err.Error(), "list Machines for cluster "+testNamespace+"/"+clusterUUID.String())
	})

	t.Run("node name and phase", func(t *testing.T) {
		machine := machineWithStatus(resolvedNodeName, testNamespace, clusterName, false, "")
		machine.Status.Phase = "Provisioning"
		machineClient := NewMachineClient(nil, newFakeCache(t, machine), testNamespace)

		nodes, err := machineClient.GetMachinesByCluster(t.Context(), clusterUUID)

		require.NoError(t, err)
		require.Len(t, nodes, 1)
		assert.Equal(t, resolvedNodeName, nodes[0].Name)
		assert.Equal(t, "Provisioning", nodes[0].Status)
	})

	t.Run("provider id is decoded into VM uuid", func(t *testing.T) {
		vmID := uuid.Must(uuid.NewV4())
		machine := machineWithStatus("machine-name", testNamespace, clusterName, false, metav1.ConditionTrue)
		machine.Spec.ProviderID = xok8scommon.GetProviderID(uuid.Nil, &xosdk.VM{ID: vmID})
		machineClient := NewMachineClient(nil, newFakeCache(t, machine), testNamespace)

		nodes, err := machineClient.GetMachinesByCluster(t.Context(), clusterUUID)

		require.NoError(t, err)
		require.Len(t, nodes, 1)
		require.NotNil(t, nodes[0].MachineID)
		assert.Equal(t, vmID, *nodes[0].MachineID)
	})

	t.Run("non-xo provider id is ignored", func(t *testing.T) {
		machine := machineWithStatus("machine-name", testNamespace, clusterName, false, metav1.ConditionTrue)
		machine.Spec.ProviderID = "aws:///us-east-1a/i-1234567890abcdef0"
		machineClient := NewMachineClient(nil, newFakeCache(t, machine), testNamespace)

		nodes, err := machineClient.GetMachinesByCluster(t.Context(), clusterUUID)

		require.NoError(t, err)
		require.Len(t, nodes, 1)
		assert.Nil(t, nodes[0].MachineID)
	})
}

func TestMachineClientIPs(t *testing.T) {
	testCases := map[string]struct {
		addresses []capiv1.MachineAddress
		want      []string
	}{
		"all internal and external IPs": {
			addresses: []capiv1.MachineAddress{
				{Type: capiv1.MachineExternalIP, Address: externalIP},
				{Type: capiv1.MachineInternalIP, Address: workerIP},
				{Type: capiv1.MachineInternalIP, Address: "2001:db8::1"},
				{Type: capiv1.MachineExternalIP, Address: "2001:db8::2"},
			},
			want: []string{externalIP, workerIP, "2001:db8::1", "2001:db8::2"},
		},
		"hostname excluded": {
			addresses: []capiv1.MachineAddress{{Type: capiv1.MachineHostName, Address: hostname}},
		},
		"no addresses": {},
		"empty and invalid IPs excluded": {
			addresses: []capiv1.MachineAddress{
				{Type: capiv1.MachineInternalIP, Address: ""},
				{Type: capiv1.MachineInternalIP, Address: hostname},
				{Type: capiv1.MachineExternalIP, Address: externalIP},
			},
			want: []string{externalIP},
		},
		"duplicate IPs included once": {
			addresses: []capiv1.MachineAddress{
				{Type: capiv1.MachineInternalIP, Address: workerIP},
				{Type: capiv1.MachineExternalIP, Address: workerIP},
			},
			want: []string{workerIP},
		},
	}

	for name, tc := range testCases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			machine := machineWithStatus(workerRole, testNamespace, clusterName, false, "", tc.addresses...)
			machineClient := NewMachineClient(nil, newFakeCache(t, machine), testNamespace)

			nodes, err := machineClient.GetMachinesByCluster(t.Context(), clusterUUID)
			require.NoError(t, err)
			require.Len(t, nodes, 1)
			assert.Equal(t, tc.want, nodes[0].IPs)
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
	second := machineWithStatus("a-machine", testNamespace, otherClusterName, false, metav1.ConditionFalse,
		capiv1.MachineAddress{Type: capiv1.MachineExternalIP, Address: externalIP})
	second.Status.NodeRef = capiv1.MachineNodeReference{Name: "z-node"}
	second.Labels[capiv1.ClusterNameLabel] = "stale-cluster-label"
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
				{
					ID: uuids.NodeID(otherClusterName, "a-machine"), Name: "a-machine", NodeName: "z-node", Role: workerRole, Status: notReadyStatus,
					IPs: []string{externalIP}, ParentCluster: uuids.ClusterID(otherClusterName), Version: unknownVersion,
				},
				{ID: uuids.NodeID("", "unlabelled"), Name: "unlabelled", Role: workerRole, Status: provisioningPhase, Version: unknownVersion},
				{
					ID: uuids.NodeID(clusterName, "z-machine"), Name: "z-machine", NodeName: "a-node", Role: controlPlaneRole, Status: readyStatus,
					IPs: []string{controlPlaneIP}, ParentCluster: clusterUUID, Version: unknownVersion,
				},
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

			var informerCache cache.Cache
			if tc.namespace != "" {
				if tc.listErr != nil {
					informerCache = clientAsCache{newErrorClient(t, interceptor.Funcs{List: listErrorFunc(t, tc.listErr)})}
				} else {
					informerCache = newFakeCache(t, tc.objects...)
				}
			}
			machineClient := NewMachineClient(nil, informerCache, tc.namespace)

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

func TestMachineClientDeleteMachine(t *testing.T) {
	for name, tc := range map[string]struct {
		namespace string
		clusterID uuid.UUID
		machineID uuid.UUID
		owner     string
		wantErr   error
	}{
		"deletes matching machine":        {testNamespace, clusterUUID, deleteMachineUUID, clusterName, nil},
		"other cluster is protected":      {testNamespace, clusterUUID, deleteMachineUUID, otherClusterName, strategies.ErrNodeNotFound},
		"unassigned machine is protected": {testNamespace, clusterUUID, deleteMachineUUID, "", strategies.ErrNodeNotFound},
		"missing machine":                 {testNamespace, clusterUUID, uuids.NodeID(clusterName, "missing"), clusterName, strategies.ErrNodeNotFound},
		"other namespace is protected":    {otherNamespace, clusterUUID, deleteMachineUUID, clusterName, strategies.ErrNodeNotFound},
		"cluster ID required":             {testNamespace, uuid.Nil, deleteMachineUUID, clusterName, strategies.ErrClusterIDRequired},
		"machine ID required":             {testNamespace, clusterUUID, uuid.Nil, clusterName, strategies.ErrNodeIDRequired},
		"namespace required":              {"", clusterUUID, deleteMachineUUID, clusterName, strategies.ErrClusterNamespaceRequired},
	} {
		t.Run(name, func(t *testing.T) {
			// The label deliberately matches the requested cluster, even when the
			// spec assigns the machine elsewhere. Labels must not authorize deletion.
			machine := machineWithStatus(deleteMachineName, testNamespace, clusterName, false, "")
			machine.Spec.ClusterName = tc.owner
			kube := newFakeClient(t, machine, machineMutationCluster(testNamespace))
			err := NewMachineClient(kube, clientAsCache{kube}, tc.namespace).DeleteMachine(t.Context(), tc.clusterID, tc.machineID)
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
			base := newFakeClient(t, machineMutationCluster(testNamespace), machineWithStatus(deleteMachineName, testNamespace, clusterName, false, ""))
			kube := interceptor.NewClient(base.(client.WithWatch), interceptor.Funcs{
				Get: func(ctx context.Context, underlying client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
					if _, ok := obj.(*capiv1.Cluster); ok {
						return underlying.Get(ctx, key, obj, opts...)
					}
					assert.Equal(t, client.ObjectKey{Namespace: testNamespace, Name: deleteMachineName}, key)
					if tc.getErr != nil {
						return tc.getErr
					}
					labels := map[string]string{capiv1.ClusterNameLabel: clusterName}
					machine := obj.(*capiv1.Machine)
					machine.ObjectMeta = metav1.ObjectMeta{Name: key.Name, Namespace: key.Namespace, UID: "original-uid", ResourceVersion: "123", Labels: labels}
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
			err := NewMachineClient(kube, clientAsCache{kube}, testNamespace).DeleteMachine(t.Context(), clusterUUID, deleteMachineUUID)
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
	kube := newFakeClient(t, machine, machineMutationCluster(testNamespace))
	require.NoError(t, NewMachineClient(kube, clientAsCache{kube}, testNamespace).DeleteMachine(t.Context(), clusterUUID, uuids.NodeID(clusterName, machine.Name)))
	remaining := &capiv1.Machine{}
	require.NoError(t, kube.Get(t.Context(), client.ObjectKeyFromObject(machine), remaining))
	assert.False(t, remaining.DeletionTimestamp.IsZero())
	assert.Equal(t, machine.Finalizers, remaining.Finalizers)
}

func TestDeleteMachineUsesUUID(t *testing.T) {
	for name, tc := range map[string]struct {
		requested uuid.UUID
		duplicate bool
		wantErr   error
	}{
		"node ref UUID deletes machine":             {requested: deleteMachineUUID},
		"node name is not an alias":                 {requested: uuids.NodeID(clusterName, resolvedNodeName), wantErr: strategies.ErrNodeNotFound},
		"shared node name does not change identity": {requested: deleteMachineUUID, duplicate: true},
	} {
		t.Run(name, func(t *testing.T) {
			machine := machineWithStatus(deleteMachineName, testNamespace, clusterName, false, "")
			machine.Spec.ClusterName = clusterName
			machine.Status.NodeRef = capiv1.MachineNodeReference{Name: resolvedNodeName}
			objects := []client.Object{machine, machineMutationCluster(testNamespace)}
			other := machine.DeepCopy()
			other.Name = "other-machine"
			other.Spec.ClusterName = otherClusterName
			other.Labels[capiv1.ClusterNameLabel] = otherClusterName
			objects = append(objects, other)
			if tc.duplicate {
				duplicate := machine.DeepCopy()
				duplicate.Name = duplicateMachineName
				objects = append(objects, duplicate)
			}
			kube := newFakeClient(t, objects...)
			err := NewMachineClient(kube, clientAsCache{kube}, testNamespace).DeleteMachine(t.Context(), clusterUUID, tc.requested)
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

func TestPauseMachineWithoutAnnotations(t *testing.T) {
	machine := machineWithStatus(deleteMachineName, testNamespace, clusterName, false, "")
	kube := newFakeClient(t, machine, machineMutationCluster(testNamespace))
	require.NoError(t, NewMachineClient(kube, clientAsCache{kube}, testNamespace).SetMachinePaused(t.Context(), clusterUUID, deleteMachineUUID, true))
	require.NoError(t, kube.Get(t.Context(), client.ObjectKeyFromObject(machine), machine))
	require.Contains(t, machine.Annotations, capiv1.PausedAnnotation)
}

func TestPauseRejectsConcurrentMachineReassignment(t *testing.T) {
	machine := machineWithStatus(deleteMachineName, testNamespace, clusterName, false, "")
	kube := fake.NewClientBuilder().WithScheme(newFakeClient(t).Scheme()).WithObjects(machine, machineMutationCluster(testNamespace)).
		WithIndex(&capiv1.Cluster{}, uuids.UUIDIndexKey, clusterUUIDIndexer).
		WithIndex(&capiv1.Machine{}, uuids.UUIDIndexKey, machineUUIDIndexer).
		WithIndex(&capiv1.Machine{}, uuids.ClusterUUIDIndexKey, machineClusterUUIDIndexer).
		WithInterceptorFuncs(interceptor.Funcs{
			Patch: func(ctx context.Context, underlying client.WithWatch, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
				if _, ok := obj.(*capiv1.Cluster); ok {
					return underlying.Patch(ctx, obj, patch, opts...)
				}
				current := &capiv1.Machine{}
				require.NoError(t, underlying.Get(ctx, client.ObjectKeyFromObject(obj), current))
				current.Spec.ClusterName = otherClusterName
				require.NoError(t, underlying.Update(ctx, current))
				return underlying.Patch(ctx, obj, patch, opts...)
			},
		}).Build()
	err := NewMachineClient(kube, clientAsCache{kube}, testNamespace).SetMachinePaused(t.Context(), clusterUUID, deleteMachineUUID, true)
	require.ErrorIs(t, err, strategies.ErrPauseUpdateConflict)
	require.NoError(t, kube.Get(t.Context(), client.ObjectKeyFromObject(machine), machine))
	require.Equal(t, otherClusterName, machine.Spec.ClusterName)
	require.NotContains(t, machine.Annotations, capiv1.PausedAnnotation)
}

func TestMachineClientGetMachineByUUID(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		machine := machineWithStatus("machine-name", testNamespace, clusterName, false, metav1.ConditionTrue,
			capiv1.MachineAddress{Type: capiv1.MachineInternalIP, Address: workerIP})
		fc := newFakeClient(t, machine)
		machineClient := NewMachineClient(fc, clientAsCache{fc}, testNamespace)

		id := uuids.NodeID(clusterName, "machine-name")
		node, err := machineClient.GetMachineByUUID(t.Context(), id)

		require.NoError(t, err)
		assert.Equal(t, strategies.ClusterNodeInfo{
			ID: id, ParentCluster: clusterUUID, Name: "machine-name", Role: workerRole, Status: readyStatus, IPs: []string{workerIP},
			Version: unknownVersion,
		}, node)
	})

	t.Run("not found", func(t *testing.T) {
		fc := newFakeClient(t)
		machineClient := NewMachineClient(fc, clientAsCache{fc}, testNamespace)

		node, err := machineClient.GetMachineByUUID(t.Context(), uuids.NodeID(clusterName, "missing"))

		assert.Empty(t, node)
		require.Error(t, err)
		assert.ErrorIs(t, err, strategies.ErrNodeNotFound)
	})

	t.Run("namespace required", func(t *testing.T) {
		machineClient := NewMachineClient(nil, nil, "")

		node, err := machineClient.GetMachineByUUID(t.Context(), uuids.NodeID(clusterName, "any"))

		assert.Empty(t, node)
		require.Error(t, err)
		assert.ErrorIs(t, err, strategies.ErrClusterNamespaceRequired)
	})

	t.Run("kubernetes error", func(t *testing.T) {
		fc := newErrorClient(t, interceptor.Funcs{List: listErrorFunc(t, errTest)})
		machineClient := NewMachineClient(fc, clientAsCache{fc}, testNamespace)

		node, err := machineClient.GetMachineByUUID(t.Context(), uuids.NodeID(clusterName, "any"))

		assert.Empty(t, node)
		require.Error(t, err)
		assert.ErrorIs(t, err, errTest)
		assert.Contains(t, err.Error(), "list Machines by UUID")
	})
}

func TestGetNodeNameChecksMembershipAndRegistration(t *testing.T) {
	for _, tc := range []struct {
		name, cluster, nodeRef string
		wantErr                error
	}{
		{"registered", clusterName, resolvedNodeName, nil},
		{"other cluster", otherClusterName, resolvedNodeName, strategies.ErrNodeNotFound},
		{"not registered", clusterName, "", strategies.ErrNodeNotRegistered},
	} {
		t.Run(tc.name, func(t *testing.T) {
			machine := machineWithStatus(deleteMachineName, testNamespace, tc.cluster, false, "")
			if tc.nodeRef != "" {
				machine.Status.NodeRef = capiv1.MachineNodeReference{Name: tc.nodeRef}
			}
			fc := newFakeClient(t, machine)
			service := NewMachineClient(fc, clientAsCache{fc}, testNamespace)
			name, err := service.GetNodeName(t.Context(), clusterUUID, uuids.NodeID(tc.cluster, deleteMachineName))
			if tc.wantErr != nil {
				require.ErrorIs(t, err, tc.wantErr)
			} else {
				require.NoError(t, err)
				require.Equal(t, resolvedNodeName, name)
			}
		})
	}
}

func TestMachineClientNodeVersion(t *testing.T) {
	for _, tc := range []struct {
		name           string
		specVersion    string
		kubeletVersion string
		want           string
	}{
		{name: "spec version", specVersion: "v1.36.0", kubeletVersion: "v1.35.0", want: "v1.36.0"},
		{name: "kubelet fallback", kubeletVersion: "v1.35.4", want: "v1.35.4"},
		{name: "unknown", want: unknownVersion},
	} {
		t.Run(tc.name, func(t *testing.T) {
			machine := machineWithStatus("machine", testNamespace, clusterName, false, metav1.ConditionTrue)
			machine.Spec.Version = tc.specVersion
			if tc.kubeletVersion != "" {
				machine.Status.NodeInfo = &corev1.NodeSystemInfo{KubeletVersion: tc.kubeletVersion}
			}
			machineClient := NewMachineClient(nil, newFakeCache(t, machine), testNamespace)

			clusterNodes, err := machineClient.GetMachinesByCluster(t.Context(), clusterUUID)
			require.NoError(t, err)
			require.Len(t, clusterNodes, 1)
			assert.Equal(t, tc.want, clusterNodes[0].Version)

			allNodes, err := machineClient.GetAllMachines(t.Context())
			require.NoError(t, err)
			require.Len(t, allNodes, 1)
			assert.Equal(t, tc.want, allNodes[0].Version)
		})
	}
}

func machineMutationCluster(namespace string) *capiv1.Cluster {
	return &capiv1.Cluster{ObjectMeta: metav1.ObjectMeta{Name: clusterName, Namespace: namespace}}
}
