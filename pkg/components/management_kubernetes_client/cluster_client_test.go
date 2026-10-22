package management_kubernetes_client

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	capiv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	uuids "capv-proxy/pkg/helpers/uuids"
	"capv-proxy/pkg/strategies"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/utils/ptr"
)

const (
	scalingNoopCase          = "noop"
	scalingListCase          = "list failure"
	scalingGoneCase          = "target disappeared"
	scalingAdmissionCase     = "admission rejection"
	scalingGetCase           = "get failure"
	scalingOtherCluster      = "unrelated-cluster"
	scalingFinalizer         = "scaling-test"
	scalingConflictCase      = "concurrent change"
	scalingTarget            = "scaling-resource"
	otherK8sVersion          = "v1.35.0"
	k8sVersion               = "v1.32.0"
	clusterName              = "test-cluster"
	testNamespace            = "capv"
	provisionedPhase         = "Provisioned"
	provisioningPhase        = "Provisioning"
	caseEmptyName            = "empty name"
	caseEmptyNamespace       = "empty namespace"
	errClusterID             = "cluster ID is required"
	errClusterNamespace      = "cluster namespace is required"
	errNotFound              = "not found"
	caseEmptyTagValue        = "empty value"
	testTagLabelPrefix       = "tags.vmk.vates.tech/"
	ownerTag                 = "owner"
	platformValue            = "platform"
	productionValue          = "production"
	testCluster              = "demo"
	testCreationNamespace    = "default"
	testVersion              = "v1.36.1"
	testPodCIDR              = "172.31.0.0/16"
	testControlPlane         = "demo-cp"
	testOriginalPrefix       = "original"
	testServiceCIDR          = "192.168.0.0/16"
	testDomain               = "cluster.local"
	testWorkerName           = "md-0"
	kubeVIP                  = "kube-vip"
	ciliumCNI                = "cilium"
	pauseMachineResource     = "machines"
	preservedAnnotationValue = "preserved"
)

var (
	errTest         = errors.New("test error")
	clusterUUID     = uuids.ClusterID(clusterName)
	testClusterUUID = uuids.ClusterID(testCluster)
)

func TestClusterClientList(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		objects := []client.Object{
			&capiv1.Cluster{ObjectMeta: metav1.ObjectMeta{Name: "other", Namespace: otherNamespace}},
			&capiv1.Cluster{ObjectMeta: metav1.ObjectMeta{Name: "provisioned", Namespace: testNamespace}, Status: capiv1.ClusterStatus{Phase: provisionedPhase}},
			&capiv1.Cluster{ObjectMeta: metav1.ObjectMeta{Name: "provisioning", Namespace: testNamespace}, Status: capiv1.ClusterStatus{Phase: provisioningPhase}},
		}
		fc := newFakeClient(t, objects...)
		clusterClient := NewClusterClient(fc, clientAsCache{fc}, testNamespace)

		clusters, err := clusterClient.List(t.Context())

		require.NoError(t, err)
		assert.ElementsMatch(t, []strategies.ClusterInfo{
			{APIServerPort: 6443, ID: uuids.ClusterID("provisioned"), Name: "provisioned", Phase: provisionedPhase, CreatedAt: time.Time{}.String()},
			{APIServerPort: 6443, ID: uuids.ClusterID("provisioning"), Name: "provisioning", Phase: provisioningPhase, CreatedAt: time.Time{}.String()},
		}, clusters)
	})

	t.Run("empty", func(t *testing.T) {
		fc := newFakeClient(t)
		clusterClient := NewClusterClient(fc, clientAsCache{fc}, testNamespace)

		clusters, err := clusterClient.List(t.Context())

		require.NoError(t, err)
		assert.Empty(t, clusters)
		assert.NotNil(t, clusters)
	})

	t.Run("error", func(t *testing.T) {
		fc := newErrorClient(t, interceptor.Funcs{List: listErrorFunc(t, errTest)})
		clusterClient := NewClusterClient(fc, clientAsCache{fc}, testNamespace)

		clusters, err := clusterClient.List(t.Context())

		assert.Nil(t, clusters)
		assert.ErrorIs(t, err, errTest)
	})
}

func TestClusterAvailability(t *testing.T) {
	for _, testCase := range []struct {
		name       string
		conditions []metav1.Condition
		available  bool
	}{
		{name: "missing condition"},
		{name: "available", conditions: []metav1.Condition{{Type: capiv1.ClusterAvailableCondition, Status: metav1.ConditionTrue}}, available: true},
		{name: "unavailable", conditions: []metav1.Condition{{Type: capiv1.ClusterAvailableCondition, Status: metav1.ConditionFalse}}},
		{name: "unknown", conditions: []metav1.Condition{{Type: capiv1.ClusterAvailableCondition, Status: metav1.ConditionUnknown}}},
		{name: "unrelated condition", conditions: []metav1.Condition{{Type: capiv1.ClusterControlPlaneAvailableCondition, Status: metav1.ConditionTrue}}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			cluster := &capiv1.Cluster{
				ObjectMeta: metav1.ObjectMeta{Name: clusterName, Namespace: testNamespace},
				Status:     capiv1.ClusterStatus{Phase: provisionedPhase, Conditions: testCase.conditions},
			}
			clusterClient := NewClusterClient(newFakeClient(t, cluster), newFakeCache(t, cluster), testNamespace)
			info, err := clusterClient.Get(t.Context(), clusterName)
			require.NoError(t, err)
			assert.Equal(t, testCase.available, info.IsAvailable)
			clusters, err := clusterClient.List(t.Context())
			require.NoError(t, err)
			require.Len(t, clusters, 1)
			assert.Equal(t, testCase.available, clusters[0].IsAvailable)
		})
	}
}

func TestClusterInitialization(t *testing.T) {
	for _, testCase := range []struct {
		name                      string
		controlPlaneInitialized   *bool
		infrastructureProvisioned *bool
	}{
		{name: "not reported"},
		{name: "not initialized", controlPlaneInitialized: ptr.To(false), infrastructureProvisioned: ptr.To(false)},
		{name: "infrastructure provisioned", controlPlaneInitialized: ptr.To(false), infrastructureProvisioned: ptr.To(true)},
		{name: "initialized", controlPlaneInitialized: ptr.To(true), infrastructureProvisioned: ptr.To(true)},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			cluster := &capiv1.Cluster{
				ObjectMeta: metav1.ObjectMeta{Name: clusterName, Namespace: testNamespace},
				Status: capiv1.ClusterStatus{Initialization: capiv1.ClusterInitializationStatus{
					ControlPlaneInitialized:   testCase.controlPlaneInitialized,
					InfrastructureProvisioned: testCase.infrastructureProvisioned,
				}},
			}
			kube := newFakeClient(t, cluster)
			clusterClient := NewClusterClient(kube, clientAsCache{kube}, testNamespace)
			info, err := clusterClient.Get(t.Context(), clusterName)
			require.NoError(t, err)
			assert.Equal(t, testCase.controlPlaneInitialized, info.IsControlPlaneInitialized)
			assert.Equal(t, testCase.infrastructureProvisioned, info.IsInfrastructureProvisioned)
			clusters, err := clusterClient.List(t.Context())
			require.NoError(t, err)
			require.Len(t, clusters, 1)
			assert.Equal(t, info, clusters[0])
		})
	}
}

func TestClusterNetworking(t *testing.T) {
	for _, configured := range []bool{false, true} {
		t.Run(map[bool]string{false: "unspecified", true: "dual stack with custom bind port"}[configured], func(t *testing.T) {
			cluster := &capiv1.Cluster{ObjectMeta: metav1.ObjectMeta{Name: clusterName, Namespace: testNamespace}}
			if configured {
				cluster.Spec.ClusterNetwork = capiv1.ClusterNetwork{
					APIServerPort: 7443,
					Pods:          capiv1.NetworkRanges{CIDRBlocks: []string{testPodCIDR, "fd00:1::/64"}},
					Services:      capiv1.NetworkRanges{CIDRBlocks: []string{testServiceCIDR, "fd00:2::/108"}},
					ServiceDomain: testDomain,
				}
				cluster.Spec.ControlPlaneEndpoint = capiv1.APIEndpoint{Host: controlPlaneIP, Port: 6443}
			}
			kube := newFakeClient(t, cluster)
			resource := NewClusterClient(kube, clientAsCache{kube}, testNamespace)
			listed, err := resource.List(t.Context())
			require.NoError(t, err)
			require.Len(t, listed, 1)
			detail, err := resource.GetByUUID(t.Context(), clusterUUID)
			require.NoError(t, err)
			require.Equal(t, listed[0], detail)
			if configured {
				require.Equal(t, int32(7443), detail.APIServerPort)
				require.Equal(t, []string{testPodCIDR, "fd00:1::/64"}, detail.PodCIDRBlocks)
				require.Equal(t, []string{testServiceCIDR, "fd00:2::/108"}, detail.ServiceCIDRBlocks)
				require.Equal(t, testDomain, detail.ServiceDomain)
				require.Equal(t, "https://"+controlPlaneIP+":6443", detail.ControlPlaneEndpoint)
			} else {
				require.Equal(t, int32(6443), detail.APIServerPort)
				require.Empty(t, detail.PodCIDRBlocks)
				require.Empty(t, detail.ServiceCIDRBlocks)
				require.Empty(t, detail.ServiceDomain)
			}
		})
	}
}

func TestClusterClientGet(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		cluster := &capiv1.Cluster{
			ObjectMeta: metav1.ObjectMeta{Name: clusterName, Namespace: testNamespace},
			Spec:       capiv1.ClusterSpec{ControlPlaneEndpoint: capiv1.APIEndpoint{Host: controlPlaneIP, Port: 6443}},
			Status:     capiv1.ClusterStatus{Phase: provisionedPhase},
		}
		fc := newFakeClient(t, cluster)
		clusterClient := NewClusterClient(fc, clientAsCache{fc}, testNamespace)

		info, err := clusterClient.Get(t.Context(), clusterName)

		require.NoError(t, err)
		assert.Equal(t, strategies.ClusterInfo{
			APIServerPort: 6443,
			ID:            uuids.ClusterID(clusterName), CreatedAt: time.Time{}.String(), Name: clusterName, Phase: provisionedPhase, ControlPlaneEndpoint: "https://" + controlPlaneIP + ":6443",
		}, info)
	})

	t.Run("replica status and metadata", func(t *testing.T) {
		createdAt := metav1.NewTime(time.Date(2026, time.September, 21, 10, 0, 0, 0, time.UTC))
		cluster := &capiv1.Cluster{
			ObjectMeta: metav1.ObjectMeta{Name: clusterName, Namespace: testNamespace, CreationTimestamp: createdAt},
			Spec:       capiv1.ClusterSpec{Topology: capiv1.Topology{Version: k8sVersion}},
			Status: capiv1.ClusterStatus{
				ControlPlane: &capiv1.ClusterControlPlaneStatus{
					AvailableReplicas: ptr.To(int32(1)), DesiredReplicas: ptr.To(int32(3)),
					ReadyReplicas: ptr.To(int32(2)), Replicas: ptr.To(int32(4)),
					UpToDateReplicas: ptr.To(int32(1)),
					Versions:         []capiv1.StatusVersion{{Version: k8sVersion, Replicas: 1}, {Version: otherK8sVersion, Replicas: 3}},
				},
				Workers: &capiv1.WorkersStatus{
					AvailableReplicas: ptr.To(int32(5)), DesiredReplicas: ptr.To(int32(7)),
					ReadyReplicas: ptr.To(int32(6)), Replicas: ptr.To(int32(8)),
					UpToDateReplicas: ptr.To(int32(5)),
					Versions:         []capiv1.StatusVersion{{Version: otherK8sVersion, Replicas: 3}, {Version: k8sVersion, Replicas: 5}},
				},
			},
		}
		fc := newFakeClient(t, cluster)
		clusterClient := NewClusterClient(fc, clientAsCache{fc}, testNamespace)

		info, err := clusterClient.Get(t.Context(), clusterName)

		require.NoError(t, err)
		assert.Equal(t, strategies.ClusterInfo{
			APIServerPort: 6443,
			ID:            uuids.ClusterID(clusterName), Name: clusterName, Version: k8sVersion, CreatedAt: createdAt.Local().String(),
			ControlPlanesStatus: strategies.NodeTypeStatus{
				AvailableReplicas: 1, DesiredReplicas: 3, ReadyReplicas: 2, Replicas: 4, UpToDateReplicas: 1,
				Versions: []strategies.VersionNodeStatus{{Version: k8sVersion, Replicas: 1}, {Version: otherK8sVersion, Replicas: 3}},
			},
			WorkersStatus: strategies.NodeTypeStatus{
				AvailableReplicas: 5, DesiredReplicas: 7, ReadyReplicas: 6, Replicas: 8, UpToDateReplicas: 5,
				Versions: []strategies.VersionNodeStatus{{Version: otherK8sVersion, Replicas: 3}, {Version: k8sVersion, Replicas: 5}},
			},
		}, info)

		clusters, err := clusterClient.List(t.Context())
		require.NoError(t, err)
		assert.Equal(t, []strategies.ClusterInfo{info}, clusters)
	})

	t.Run("versions with zero replicas", func(t *testing.T) {
		cluster := &capiv1.Cluster{
			ObjectMeta: metav1.ObjectMeta{Name: clusterName, Namespace: testNamespace},
			Status: capiv1.ClusterStatus{
				ControlPlane: &capiv1.ClusterControlPlaneStatus{
					UpToDateReplicas: ptr.To(int32(0)),
					Versions:         []capiv1.StatusVersion{{Version: k8sVersion}},
				},
				Workers: &capiv1.WorkersStatus{
					Versions: []capiv1.StatusVersion{{Version: otherK8sVersion}},
				},
			},
		}
		clusterClient := NewClusterClient(newFakeClient(t, cluster), newFakeCache(t, cluster), testNamespace)

		info, err := clusterClient.Get(t.Context(), clusterName)

		require.NoError(t, err)
		assert.Equal(t, strategies.ClusterInfo{
			APIServerPort: 6443,
			ID:            uuids.ClusterID(clusterName), Name: clusterName, Version: k8sVersion, CreatedAt: time.Time{}.String(),
			ControlPlanesStatus: strategies.NodeTypeStatus{
				Versions: []strategies.VersionNodeStatus{{Version: k8sVersion}},
			},
			WorkersStatus: strategies.NodeTypeStatus{
				Versions: []strategies.VersionNodeStatus{{Version: otherK8sVersion}},
			},
		}, info)
	})

	t.Run("partially populated replica status", func(t *testing.T) {
		cluster := &capiv1.Cluster{
			ObjectMeta: metav1.ObjectMeta{Name: clusterName, Namespace: testNamespace},
			Status: capiv1.ClusterStatus{
				ControlPlane: &capiv1.ClusterControlPlaneStatus{DesiredReplicas: ptr.To(int32(3))},
				Workers:      &capiv1.WorkersStatus{Replicas: ptr.To(int32(2))},
			},
		}
		fc := newFakeClient(t, cluster)
		clusterClient := NewClusterClient(fc, clientAsCache{fc}, testNamespace)

		info, err := clusterClient.Get(t.Context(), clusterName)

		require.NoError(t, err)
		assert.Equal(t, strategies.ClusterInfo{
			APIServerPort: 6443,
			ID:            uuids.ClusterID(clusterName), Name: clusterName, CreatedAt: time.Time{}.String(),
			ControlPlanesStatus: strategies.NodeTypeStatus{DesiredReplicas: 3},
			WorkersStatus:       strategies.NodeTypeStatus{Replicas: 2},
		}, info)
	})

	t.Run("invalid endpoint", func(t *testing.T) {
		cluster := &capiv1.Cluster{ObjectMeta: metav1.ObjectMeta{Name: clusterName, Namespace: testNamespace}}
		fc := newFakeClient(t, cluster)
		clusterClient := NewClusterClient(fc, clientAsCache{fc}, testNamespace)

		info, err := clusterClient.Get(t.Context(), clusterName)

		require.NoError(t, err)
		assert.Empty(t, info.ControlPlaneEndpoint)
	})

	t.Run("error", func(t *testing.T) {
		fc := newErrorClient(t, interceptor.Funcs{Get: getErrorFunc(t, errTest)})
		clusterClient := NewClusterClient(fc, clientAsCache{fc}, testNamespace)

		info, err := clusterClient.Get(t.Context(), clusterName)

		assert.Empty(t, info)
		assert.ErrorIs(t, err, errTest)
	})

	t.Run("not found", func(t *testing.T) {
		fc := newFakeClient(t)
		clusterClient := NewClusterClient(fc, clientAsCache{fc}, testNamespace)

		info, err := clusterClient.Get(t.Context(), clusterName)

		assert.Empty(t, info)
		require.Error(t, err)
		assert.ErrorIs(t, err, strategies.ErrClusterNotFound)
	})
}

func newFakeClient(t *testing.T, objects ...client.Object) client.Client {
	t.Helper()
	return fake.NewClientBuilder().
		WithScheme(newTestScheme(t)).
		WithObjects(objects...).
		WithIndex(&capiv1.Cluster{}, uuids.UUIDIndexKey, clusterUUIDIndexer).
		WithIndex(&capiv1.Machine{}, uuids.UUIDIndexKey, machineUUIDIndexer).
		WithIndex(&capiv1.Machine{}, uuids.ClusterUUIDIndexKey, machineClusterUUIDIndexer).
		Build()
}

func TestClusterClientGetByUUID(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		cluster := &capiv1.Cluster{
			ObjectMeta: metav1.ObjectMeta{Name: clusterName, Namespace: testNamespace},
			Status:     capiv1.ClusterStatus{Phase: provisionedPhase},
		}
		fc := newFakeClient(t, cluster)
		clusterClient := NewClusterClient(fc, clientAsCache{fc}, testNamespace)

		id := uuids.ClusterID(clusterName)
		info, err := clusterClient.GetByUUID(t.Context(), id)

		require.NoError(t, err)
		assert.Equal(t, strategies.ClusterInfo{
			APIServerPort: 6443,
			ID:            id, Name: clusterName, Phase: provisionedPhase, CreatedAt: time.Time{}.String(),
		}, info)
	})

	t.Run("not found", func(t *testing.T) {
		fc := newFakeClient(t)
		clusterClient := NewClusterClient(fc, clientAsCache{fc}, testNamespace)

		info, err := clusterClient.GetByUUID(t.Context(), uuids.ClusterID("missing"))

		assert.Empty(t, info)
		require.Error(t, err)
		assert.ErrorIs(t, err, strategies.ErrClusterNotFound)
	})

	t.Run("duplicate cluster ID is ambiguous", func(t *testing.T) {
		id := uuids.ClusterID(clusterName)
		fc := fake.NewClientBuilder().
			WithScheme(newTestScheme(t)).
			WithObjects(
				&capiv1.Cluster{ObjectMeta: metav1.ObjectMeta{Name: "first", Namespace: testNamespace}},
				&capiv1.Cluster{ObjectMeta: metav1.ObjectMeta{Name: "second", Namespace: testNamespace}},
			).
			WithIndex(&capiv1.Cluster{}, uuids.UUIDIndexKey, func(client.Object) []string { return []string{id.String()} }).
			Build()
		clusterClient := NewClusterClient(fc, clientAsCache{fc}, testNamespace)

		info, err := clusterClient.GetByUUID(t.Context(), id)

		assert.Empty(t, info)
		require.ErrorIs(t, err, strategies.ErrClusterAmbiguous)
	})

	t.Run("kubernetes error", func(t *testing.T) {
		fc := newErrorClient(t, interceptor.Funcs{List: listErrorFunc(t, errTest)})
		clusterClient := NewClusterClient(fc, clientAsCache{fc}, testNamespace)

		info, err := clusterClient.GetByUUID(t.Context(), uuids.ClusterID(clusterName))

		assert.Empty(t, info)
		require.Error(t, err)
		assert.ErrorIs(t, err, errTest)
		assert.ErrorIs(t, err, strategies.ErrClusterRetrievalFailed)
	})
}

func TestClusterClientDelete(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		cluster := &capiv1.Cluster{ObjectMeta: metav1.ObjectMeta{Name: clusterName, Namespace: testNamespace}}
		kubeClient := newFakeClient(t, cluster)
		clusterClient := NewClusterClient(kubeClient, clientAsCache{kubeClient}, testNamespace)

		err := clusterClient.Delete(t.Context(), clusterUUID)

		require.NoError(t, err)
		err = kubeClient.Get(context.Background(), types.NamespacedName{Namespace: testNamespace, Name: clusterName}, &capiv1.Cluster{})
		assert.Error(t, err)
	})

	t.Run("empty name", func(t *testing.T) {
		fc := newFakeClient(t)
		clusterClient := NewClusterClient(fc, clientAsCache{fc}, testNamespace)

		err := clusterClient.Delete(t.Context(), uuid.Nil)

		assert.EqualError(t, err, errClusterID)
	})

	t.Run("empty namespace", func(t *testing.T) {
		fc := newFakeClient(t)
		clusterClient := NewClusterClient(fc, clientAsCache{fc}, "")

		err := clusterClient.Delete(t.Context(), clusterUUID)

		assert.EqualError(t, err, errClusterNamespace)
	})

	t.Run("error", func(t *testing.T) {
		cluster := &capiv1.Cluster{ObjectMeta: metav1.ObjectMeta{Name: clusterName, Namespace: testNamespace}}
		fc := newErrorClient(t, interceptor.Funcs{Delete: deleteErrorFunc(t, errTest)}, cluster)
		clusterClient := NewClusterClient(fc, clientAsCache{fc}, testNamespace)

		err := clusterClient.Delete(t.Context(), clusterUUID)

		require.Error(t, err)
		assert.ErrorIs(t, err, errTest)
		assert.Contains(t, err.Error(), "delete Cluster "+testNamespace+"/"+clusterName)
	})

	t.Run("not found", func(t *testing.T) {
		fc := newFakeClient(t)
		clusterClient := NewClusterClient(fc, clientAsCache{fc}, testNamespace)

		err := clusterClient.Delete(t.Context(), clusterUUID)

		require.Error(t, err)
		assert.ErrorIs(t, err, strategies.ErrClusterNotFound)
	})
}

func TestClusterClientCreatesClusterWithTags(t *testing.T) {
	testCases := map[string]struct {
		tags    map[string]string
		labels  map[string]string
		wantErr error
	}{
		"multiple tags": {
			tags: map[string]string{"environment": productionValue, ownerTag: platformValue},
			labels: map[string]string{
				capiv1.ClusterNameLabel:            clusterName,
				testTagLabelPrefix + "environment": productionValue, testTagLabelPrefix + ownerTag: platformValue,
			},
		},
		"no tags": {labels: map[string]string{capiv1.ClusterNameLabel: clusterName}},
		caseEmptyTagValue: {
			tags:   map[string]string{ownerTag: ""},
			labels: map[string]string{capiv1.ClusterNameLabel: clusterName, testTagLabelPrefix + ownerTag: ""},
		},
		"invalid name": {tags: map[string]string{strings.Repeat("a", 64): platformValue}, wantErr: strategies.ErrInvalidClusterTagName},
	}
	for name, tc := range testCases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			params, objects, scheme := newClusterCreationTestSetup(t)
			params.Name = clusterName
			params.Tags = tc.tags
			kubeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).Build()
			clusterClient := NewClusterClient(kubeClient, clientAsCache{kubeClient}, testNamespace)
			info, err := clusterClient.Create(t.Context(), params)
			if tc.wantErr != nil {
				require.ErrorIs(t, err, tc.wantErr)
				assert.Empty(t, info)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.tags, info.Tags)
			stored := &capiv1.Cluster{}
			require.NoError(t, kubeClient.Get(t.Context(), types.NamespacedName{Namespace: testNamespace, Name: clusterName}, stored))
			assert.Equal(t, tc.labels, stored.Labels)
		})
	}
}

func TestClusterClientGetAllTags(t *testing.T) {
	testCases := map[string]struct {
		objects []client.Object
		tags    map[string]map[string]string
		wantErr error
	}{
		"tags by cluster": {
			objects: []client.Object{
				&capiv1.Cluster{ObjectMeta: metav1.ObjectMeta{Name: clusterName, Namespace: testNamespace, Labels: map[string]string{
					capiv1.ClusterNameLabel:       clusterName,
					testTagLabelPrefix + ownerTag: platformValue,
					testTagLabelPrefix + "env":    productionValue,
				}}},
				&capiv1.Cluster{ObjectMeta: metav1.ObjectMeta{Name: "empty-value", Namespace: testNamespace, Labels: map[string]string{testTagLabelPrefix + ownerTag: ""}}},
				&capiv1.Cluster{ObjectMeta: metav1.ObjectMeta{Name: "no-tags", Namespace: testNamespace}},
				&capiv1.Cluster{ObjectMeta: metav1.ObjectMeta{Name: clusterName, Namespace: otherNamespace, Labels: map[string]string{testTagLabelPrefix + ownerTag: "different-owner"}}},
			},
			tags: map[string]map[string]string{
				clusterName:   {ownerTag: platformValue, "env": productionValue},
				"empty-value": {ownerTag: ""},
				"no-tags":     {},
			},
		},
		"no clusters": {tags: map[string]map[string]string{}},
		"list error":  {wantErr: errTest},
	}
	for name, tc := range testCases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			kubeClient := newFakeClient(t, tc.objects...)
			if tc.wantErr != nil {
				kubeClient = newErrorClient(t, interceptor.Funcs{List: listErrorFunc(t, tc.wantErr)})
			}
			clusterClient := NewClusterClient(kubeClient, clientAsCache{kubeClient}, testNamespace)

			tags, err := clusterClient.GetAllTags(t.Context())

			if tc.wantErr != nil {
				require.ErrorIs(t, err, tc.wantErr)
			} else {
				require.NoError(t, err)
			}
			assert.Equal(t, tc.tags, tags)
		})
	}
}

func TestClusterClientReadsClusterTags(t *testing.T) {
	testCases := map[string]struct {
		labels map[string]string
		tags   map[string]string
	}{
		"filters unrelated labels": {
			labels: map[string]string{capiv1.ClusterNameLabel: clusterName, testTagLabelPrefix + ownerTag: platformValue},
			tags:   map[string]string{ownerTag: platformValue},
		},
		caseEmptyTagValue: {labels: map[string]string{testTagLabelPrefix + ownerTag: ""}, tags: map[string]string{ownerTag: ""}},
		"no labels":       {},
	}
	for name, tc := range testCases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			cluster := &capiv1.Cluster{ObjectMeta: metav1.ObjectMeta{Name: clusterName, Namespace: testNamespace, Labels: tc.labels}}
			fc := newFakeClient(t, cluster)
			clusterClient := NewClusterClient(fc, clientAsCache{fc}, testNamespace)
			tags, err := clusterClient.GetTags(t.Context(), clusterUUID)
			require.NoError(t, err)
			if tc.tags == nil {
				assert.Equal(t, map[string]string{}, tags)
			} else {
				assert.Equal(t, tc.tags, tags)
			}
			info, err := clusterClient.Get(t.Context(), clusterName)
			require.NoError(t, err)
			assert.Equal(t, tc.tags, info.Tags)
			clusters, err := clusterClient.List(t.Context())
			require.NoError(t, err)
			require.Len(t, clusters, 1)
			assert.Equal(t, tc.tags, clusters[0].Tags)
			for tag, expected := range tc.tags {
				value, err := clusterClient.GetTag(t.Context(), clusterUUID, tag)
				require.NoError(t, err)
				assert.Equal(t, expected, value)
			}
		})
	}
}

func TestClusterClientUpdatesClusterTags(t *testing.T) {
	testCases := map[string]struct {
		initial   map[string]string
		tag       string
		value     string
		deleteTag bool
		want      map[string]string
		wantErr   error
	}{
		"create":          {tag: ownerTag, value: platformValue, want: map[string]string{ownerTag: platformValue}},
		"update":          {initial: map[string]string{ownerTag: platformValue}, tag: ownerTag, value: "compute", want: map[string]string{ownerTag: "compute"}},
		caseEmptyTagValue: {tag: ownerTag, want: map[string]string{ownerTag: ""}},
		"delete":          {initial: map[string]string{ownerTag: platformValue}, tag: ownerTag, deleteTag: true, want: map[string]string{}},
		"missing tag":     {tag: ownerTag, deleteTag: true, wantErr: strategies.ErrClusterTagNotFound},
		"invalid value":   {tag: ownerTag, value: strings.Repeat("a", 64), wantErr: strategies.ErrInvalidClusterTagValue},
		"invalid name":    {tag: "invalid/name", value: platformValue, wantErr: strategies.ErrInvalidClusterTagName},
	}
	for name, tc := range testCases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			labels := map[string]string{capiv1.ClusterNameLabel: clusterName}
			for tag, value := range tc.initial {
				labels[testTagLabelPrefix+tag] = value
			}
			cluster := &capiv1.Cluster{ObjectMeta: metav1.ObjectMeta{Name: clusterName, Namespace: testNamespace, Labels: labels}}
			kubeClient := newFakeClient(t, cluster)
			clusterClient := NewClusterClient(kubeClient, clientAsCache{kubeClient}, testNamespace)
			var err error
			if tc.deleteTag {
				err = clusterClient.DeleteTag(t.Context(), clusterUUID, tc.tag)
			} else {
				err = clusterClient.SetTag(t.Context(), clusterUUID, tc.tag, tc.value)
			}
			if tc.wantErr != nil {
				require.ErrorIs(t, err, tc.wantErr)
			} else {
				require.NoError(t, err)
				labels = map[string]string{capiv1.ClusterNameLabel: clusterName}
				for tag, value := range tc.want {
					labels[testTagLabelPrefix+tag] = value
				}
			}
			stored := &capiv1.Cluster{}
			require.NoError(t, kubeClient.Get(t.Context(), types.NamespacedName{Namespace: testNamespace, Name: clusterName}, stored))
			assert.Equal(t, labels, stored.Labels)
			if tc.deleteTag && tc.wantErr == nil {
				_, err := clusterClient.GetTag(t.Context(), clusterUUID, tc.tag)
				assert.ErrorIs(t, err, strategies.ErrClusterTagNotFound)
			}
		})
	}
}

func TestClusterClientTagMissingCluster(t *testing.T) {
	testCases := map[string]func(context.Context, ClusterClient) error{
		"list": func(ctx context.Context, c ClusterClient) error {
			_, err := c.GetTags(ctx, clusterUUID)
			return err
		},
		"get": func(ctx context.Context, c ClusterClient) error {
			_, err := c.GetTag(ctx, clusterUUID, ownerTag)
			return err
		},
		"set": func(ctx context.Context, c ClusterClient) error {
			return c.SetTag(ctx, clusterUUID, ownerTag, platformValue)
		},
		"delete": func(ctx context.Context, c ClusterClient) error {
			return c.DeleteTag(ctx, clusterUUID, ownerTag)
		},
	}
	for name, operation := range testCases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			fc := newFakeClient(t)
			clusterClient := NewClusterClient(fc, clientAsCache{fc}, testNamespace)
			assert.ErrorIs(t, operation(t.Context(), clusterClient), strategies.ErrClusterNotFound)
		})
	}
}

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

func newErrorClient(t *testing.T, funcs interceptor.Funcs, objects ...client.Object) client.Client {
	t.Helper()

	return fake.NewClientBuilder().
		WithScheme(newTestScheme(t)).
		WithObjects(objects...).
		WithIndex(&capiv1.Cluster{}, uuids.UUIDIndexKey, clusterUUIDIndexer).
		WithIndex(&capiv1.Machine{}, uuids.UUIDIndexKey, machineUUIDIndexer).
		WithIndex(&capiv1.Machine{}, uuids.ClusterUUIDIndexKey, machineClusterUUIDIndexer).
		WithInterceptorFuncs(funcs).
		Build()
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

func newClusterCreationTestSetup(t *testing.T) (strategies.CreateClusterResourceParams, []client.Object, *runtime.Scheme) {
	t.Helper()
	d := strategies.ClusterCreationDefaults{
		PodCIDR:                   testPodCIDR,
		ServiceCIDR:               testServiceCIDR,
		ServiceDomain:             testDomain,
		EndpointPort:              6443,
		EndpointSubnet:            16,
		LoadBalancer:              kubeVIP,
		CNI:                       ciliumCNI,
		ControlPlaneKubeadmConfig: map[string]any{commandsKey: []any{"echo preserved"}},
		WorkerKubeadmConfig:       map[string]any{commandsKey: []any{"echo worker"}},
		ControlPlaneMachineSpec:   getValidXOMachineSpec(),
		WorkerMachineSpec:         getValidXOMachineSpec(),
	}
	p := strategies.CreateClusterResourceParams{
		Version:              testVersion,
		Name:                 testCluster,
		Defaults:             d,
		ControlPlaneIP:       "192.0.2.10",
		VMNamePrefix:         "custom",
		ControlPlaneReplicas: ptr.To(int32(3)),
		MachineDeployments: []strategies.MachineDeployment{{
			Name:     testWorkerName,
			Replicas: ptr.To(int32(2)),
		}},
		Tags: map[string]string{"env": "test"},
	}
	objects := []client.Object{}
	scheme := runtime.NewScheme()
	require.NoError(t, capiv1.AddToScheme(scheme))
	return p, objects, scheme
}

func TestClusterResourceCreation(t *testing.T) {
	p, objects, scheme := newClusterCreationTestSetup(t)
	p.CreatedByXOUserID = "xo-user-123"
	p.Description = "Cluster de l’équipe \"Backend\"\nTest 🚀"
	createdAt := metav1.NewTime(time.Date(2026, time.October, 15, 10, 0, 0, 0, time.UTC))
	creating := true
	kube := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).WithInterceptorFuncs(interceptor.Funcs{
		Create: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
			if _, ok := obj.(*capiv1.Cluster); ok {
				obj.SetCreationTimestamp(createdAt)
			}
			return c.Create(ctx, obj, opts...)
		},
		Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
			if creating {
				require.Fail(t, "creation must not read Kubernetes templates")
				return errors.New("unexpected Get")
			}
			return c.Get(ctx, key, obj, opts...)
		},
	}).Build()
	info, err := NewClusterClient(kube, clientAsCache{kube}, testCreationNamespace).Create(t.Context(), p)
	creating = false
	require.NoError(t, err)
	require.Equal(t, testCluster, info.Name)
	require.Equal(t, p.Tags, info.Tags)
	require.Equal(t, p.Description, info.Description)
	require.Equal(t, p.CreatedByXOUserID, info.CreatedByXOUserID)
	require.Equal(t, "Creating", info.Phase)
	responseCreatedAt, err := time.Parse("2006-01-02 15:04:05 -0700 MST", info.CreatedAt)
	require.NoError(t, err)
	require.True(t, createdAt.Time.Equal(responseCreatedAt))
	require.Equal(t, p.Version, info.Version)
	require.Equal(t, "https://192.0.2.10:6443", info.ControlPlaneEndpoint)
	require.Equal(t, strategies.NodeTypeStatus{DesiredReplicas: 3}, info.ControlPlanesStatus)
	require.Equal(t, strategies.NodeTypeStatus{DesiredReplicas: 2}, info.WorkersStatus)
	require.False(t, info.IsAvailable)
	require.False(t, info.Paused)
	cluster := &capiv1.Cluster{}
	require.NoError(t, kube.Get(t.Context(), client.ObjectKey{Name: testCluster, Namespace: testCreationNamespace}, cluster))
	require.Equal(t, p.Description, cluster.Annotations["vmk.vates.tech/description"])
	require.Equal(t, p.CreatedByXOUserID, cluster.Annotations["vmk.vates.tech/created-by-xo-user-id"])
	// A fresh client reads persisted metadata for both detail and list.
	freshClient := NewClusterClient(kube, clientAsCache{kube}, testCreationNamespace)
	detail, err := freshClient.Get(t.Context(), testCluster)
	require.NoError(t, err)
	require.Equal(t, p.Description, detail.Description)
	require.Equal(t, p.CreatedByXOUserID, detail.CreatedByXOUserID)
	listed, err := freshClient.List(t.Context())
	require.NoError(t, err)
	require.Len(t, listed, 1)
	require.Equal(t, p.Description, listed[0].Description)
	require.Equal(t, p.CreatedByXOUserID, listed[0].CreatedByXOUserID)
	require.Nil(t, cluster.Spec.Paused)
	require.Empty(t, cluster.Spec.Topology.ClassRef.Name)
	require.Equal(t, []string{testPodCIDR}, cluster.Spec.ClusterNetwork.Pods.CIDRBlocks)
	require.Equal(t, testControlPlane, cluster.Spec.ControlPlaneRef.Name)
	require.Equal(t, testCluster, cluster.Labels[capiv1.ClusterNameLabel])
	require.Equal(t, "test", cluster.Labels[clusterTagLabelPrefix+"env"])
	get := func(group, kind, name string) *unstructured.Unstructured {
		t.Helper()
		obj := resource(group, kind, name, testCreationNamespace, nil)
		require.NoError(t, kube.Get(t.Context(), client.ObjectKeyFromObject(obj), obj))
		return obj
	}
	xo := get(xoGroup, xoClusterKind, testCluster)
	owner := metav1.GetControllerOf(xo)
	require.NotNil(t, owner)
	require.Equal(t, cluster.Name, owner.Name)
	require.Equal(t, cluster.UID, owner.UID)
	addons, _, err := unstructured.NestedMap(xo.Object, specKey, "addons")
	require.NoError(t, err)
	require.Equal(t, map[string]any{cniKey: ciliumCNI, "ccm": true, "csi": true}, addons)
	host, _, _ := unstructured.NestedString(xo.Object, specKey, endpointKey, "host")
	require.Equal(t, p.ControlPlaneIP, host)
	cp := get(cpGroup, controlPlaneKind, testControlPlane)
	require.Nil(t, metav1.GetControllerOf(cp))
	version, found, err := unstructured.NestedString(cp.Object, specKey, "version")
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, p.Version, version)
	replicas, _, _ := unstructured.NestedInt64(cp.Object, specKey, "replicas")
	require.Equal(t, int64(3), replicas)
	sans, _, _ := unstructured.NestedStringSlice(cp.Object, specKey, kubeadmConfigSpecKey, clusterConfigurationKey, apiServerKey, certSANsKey)
	require.Contains(t, sans, p.ControlPlaneIP)
	commands, _, _ := unstructured.NestedStringSlice(cp.Object, specKey, kubeadmConfigSpecKey, commandsKey)
	require.Equal(t, []string{"echo preserved"}, commands)
	vm := get(xoGroup, xoMachineTemplateKind, testControlPlane)
	prefix, _, _ := unstructured.NestedString(vm.Object, specKey, templateKey, specKey, namePrefixKey)
	require.Equal(t, "custom-cp", prefix)
	require.Equal(t, testCluster, vm.GetOwnerReferences()[0].Name)
	require.Equal(t, testOriginalPrefix, p.Defaults.ControlPlaneMachineSpec[namePrefixKey])
	md := &capiv1.MachineDeployment{}
	require.NoError(t, kube.Get(t.Context(), client.ObjectKey{Name: "demo-md-0", Namespace: testCreationNamespace}, md))
	require.Equal(t, int32(2), *md.Spec.Replicas)
	require.Equal(t, testVersion, md.Spec.Template.Spec.Version)
	get(bootstrapGroup, bootstrapTemplateKind, "demo-worker")
	_, err = NewClusterClient(kube, clientAsCache{kube}, testCreationNamespace).Create(t.Context(), p)
	require.ErrorIs(t, err, strategies.ErrClusterAlreadyExists)
}

func TestClusterResourcePreflightAndPartialFailure(t *testing.T) {
	for _, missing := range []bool{true, false} {
		t.Run(map[bool]string{true: "missing VM configuration", false: "create fails"}[missing], func(t *testing.T) {
			p, objects, scheme := newClusterCreationTestSetup(t)
			if missing {
				p.Defaults.ControlPlaneMachineSpec = nil
			}
			injected := errors.New("write denied")
			kube := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).WithInterceptorFuncs(interceptor.Funcs{Create: func(ctx context.Context,
				c client.WithWatch,
				obj client.Object,
				opts ...client.CreateOption,
			) error {
				if obj.GetObjectKind().GroupVersionKind().Kind == xoClusterKind {
					return injected
				}
				return c.Create(ctx, obj, opts...)
			}}).Build()
			_, err := NewClusterClient(kube, clientAsCache{kube}, testCreationNamespace).Create(t.Context(), p)
			require.Error(t, err)
			clusters := &capiv1.ClusterList{}
			require.NoError(t, kube.List(t.Context(), clusters))
			if missing {
				require.Empty(t, clusters.Items)
			} else {
				require.ErrorIs(t, err, injected)
				require.Len(t, clusters.Items, 1)
				require.True(t, *clusters.Items[0].Spec.Paused)
			}
		})
	}
}

func TestClusterResourceVersionFromStatus(t *testing.T) {
	cluster := &capiv1.Cluster{
		ObjectMeta: metav1.ObjectMeta{Name: testCluster},
		Status:     capiv1.ClusterStatus{ControlPlane: &capiv1.ClusterControlPlaneStatus{Versions: []capiv1.StatusVersion{{Version: testVersion}}}},
	}
	require.Equal(t, testVersion, toInfo(cluster).Version)
}

func TestDeletePausedCluster(t *testing.T) {
	_, _, scheme := newClusterCreationTestSetup(t)
	cluster := &capiv1.Cluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:       clusterName,
			Namespace:  testCreationNamespace,
			Finalizers: []string{"cluster.cluster.x-k8s.io"},
		},
		Spec: capiv1.ClusterSpec{Paused: ptr.To(true)},
	}
	kube := fake.NewClientBuilder().
		WithScheme(scheme).WithObjects(cluster).
		WithIndex(&capiv1.Cluster{}, uuids.UUIDIndexKey, clusterUUIDIndexer).
		Build()
	require.NoError(t, NewClusterClient(kube, clientAsCache{kube}, testCreationNamespace).Delete(t.Context(), clusterUUID))
	require.NoError(t, kube.Get(t.Context(), client.ObjectKeyFromObject(cluster), cluster))
	require.False(t, cluster.DeletionTimestamp.IsZero())
	require.Nil(t, cluster.Spec.Paused)
}

func TestClusterCreationValidation(t *testing.T) {
	for _, tc := range []struct {
		name   string
		modify func(*strategies.CreateClusterResourceParams, *string)
		want   error
	}{
		{"missing name", func(p *strategies.CreateClusterResourceParams, _ *string) { p.Name = "" }, strategies.ErrClusterNameRequired},
		{"uppercase name", func(p *strategies.CreateClusterResourceParams, _ *string) { p.Name = "NHT-Kub-Prod" }, strategies.ErrInvalidClusterName},
		{"generated name too long", func(p *strategies.CreateClusterResourceParams, _ *string) { p.Name = strings.Repeat("a", 57) }, strategies.ErrInvalidClusterName},
		{"worker deployment name too long", func(p *strategies.CreateClusterResourceParams, _ *string) {
			p.Name = strings.Repeat("a", 56)
			p.MachineDeployments[0].Name = "long-worker"
		}, strategies.ErrInvalidClusterName},
		{"missing namespace", func(_ *strategies.CreateClusterResourceParams, ns *string) { *ns = "" }, strategies.ErrClusterNamespaceRequired},
		{"invalid tag", func(p *strategies.CreateClusterResourceParams, _ *string) {
			p.Tags = map[string]string{"invalid/key": platformValue}
		}, strategies.ErrInvalidClusterTagName},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, objects, scheme := newClusterCreationTestSetup(t)
			ns := testCreationNamespace
			tc.modify(&p, &ns)
			kube := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).Build()
			_, err := NewClusterClient(kube, clientAsCache{kube}, ns).Create(t.Context(), p)
			require.ErrorIs(t, err, tc.want)
			clusters := &capiv1.ClusterList{}
			require.NoError(t, kube.List(t.Context(), clusters))
			require.Empty(t, clusters.Items)
		})
	}
}

func TestClusterCreateAPIError(t *testing.T) {
	p, objects, scheme := newClusterCreationTestSetup(t)
	injected := errors.New("API unavailable")
	kube := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).WithInterceptorFuncs(interceptor.Funcs{
		Create: func(context.Context, client.WithWatch, client.Object, ...client.CreateOption) error { return injected },
	}).Build()
	_, err := NewClusterClient(kube, clientAsCache{kube}, testCreationNamespace).Create(t.Context(), p)
	require.ErrorIs(t, err, injected)
}

func getValidXOMachineSpec() map[string]any {
	return map[string]any{
		"templateID": "image", "poolID": "pool", namePrefixKey: testOriginalPrefix,
		"networkConfig": map[string]any{"networks": []any{map[string]any{"networkID": "network"}}},
	}
}

func TestRenderBootstrapCommands(t *testing.T) {
	config := map[string]any{commandsKey: []any{`FULL="{{KUBERNETES_VERSION}}"; echo "$FULL $(hostname) ${IP}"; arping {{CONTROL_PLANE_IP}}`}}
	require.NoError(t, renderBootstrapCommands(config, "v1.36.2", "10.0.0.8"))
	require.Equal(t, []any{`FULL="v1.36.2"; echo "$FULL $(hostname) ${IP}"; arping 10.0.0.8`}, config[commandsKey])
	require.NoError(t, renderBootstrapCommands(map[string]any{}, "v1.36.2", "10.0.0.8"))
	require.Error(t, renderBootstrapCommands(map[string]any{commandsKey: []any{int64(1)}}, "v1.36.2", "10.0.0.8"))
}

func scalingObjects() (*capiv1.Cluster, *capiv1.MachineDeployment, *unstructured.Unstructured) {
	cluster := &capiv1.Cluster{ObjectMeta: metav1.ObjectMeta{Name: "demo", Namespace: testNamespace, UID: "cluster-uid"}}
	cluster.Spec.ControlPlaneRef = capiv1.ContractVersionedObjectReference{APIGroup: cpGroup, Kind: controlPlaneKind, Name: "custom-cp"}
	owner := metav1.OwnerReference{APIVersion: capiv1.GroupVersion.String(), Kind: scalingClusterKind, Name: cluster.Name, UID: cluster.UID}
	md := &capiv1.MachineDeployment{ObjectMeta: metav1.ObjectMeta{
		Name: "custom-workers", Namespace: testNamespace,
		Labels: map[string]string{capiv1.ClusterNameLabel: cluster.Name}, OwnerReferences: []metav1.OwnerReference{owner},
	}}
	md.Spec.ClusterName = cluster.Name
	md.Spec.Replicas = ptr.To(int32(1))
	cp := resource(cpGroup, controlPlaneKind, "custom-cp", testNamespace, map[string]any{"replicas": int64(1), "version": "v1.36.1", "kubeadmConfigSpec": map[string]any{}})
	cp.SetOwnerReferences([]metav1.OwnerReference{owner})
	return cluster, md, cp
}

func TestSetClusterReplicas(t *testing.T) {
	for _, controlPlane := range []bool{false, true} {
		name := "workers"
		if controlPlane {
			name = "control-plane"
		}
		t.Run(name, func(t *testing.T) {
			cluster, md, cp := scalingObjects()
			otherCluster, otherMD, _ := scalingObjects()
			otherCluster.Name = scalingOtherCluster
			otherMD.Name = "other-workers"
			otherMD.Spec.ClusterName = scalingOtherCluster
			otherMD.Labels[capiv1.ClusterNameLabel] = scalingOtherCluster
			k8s := newFakeClient(t, cluster, md, cp, otherCluster, otherMD)
			service := NewClusterClient(k8s, clientAsCache{k8s}, testNamespace)
			for _, replicas := range []int32{3, 1, 1} {
				var err error
				if controlPlane {
					err = service.SetControlPlaneReplicas(t.Context(), testClusterUUID, replicas)
				} else {
					err = service.SetWorkerReplicas(t.Context(), testClusterUUID, replicas)
				}
				require.NoError(t, err)
				require.NoError(t, k8s.Get(t.Context(), client.ObjectKeyFromObject(md), md))
				require.NoError(t, k8s.Get(t.Context(), client.ObjectKeyFromObject(cp), cp))
				actual, _, err := unstructured.NestedInt64(cp.Object, "spec", "replicas")
				require.NoError(t, err)
				if controlPlane {
					require.Equal(t, int64(replicas), actual)
					require.Equal(t, int32(1), *md.Spec.Replicas)
				} else {
					require.Equal(t, replicas, *md.Spec.Replicas)
					require.Equal(t, int64(1), actual)
				}
				require.Equal(t, "v1.36.1", cp.Object["spec"].(map[string]any)["version"])
				require.NoError(t, k8s.Get(t.Context(), client.ObjectKeyFromObject(otherMD), otherMD))
				require.Equal(t, int32(1), *otherMD.Spec.Replicas)
			}
		})
	}
}

func TestClusterScalingGuards(t *testing.T) {
	cases := []struct {
		name         string
		controlPlane bool
		replicas     int32
		mutate       func(*capiv1.Cluster, *capiv1.MachineDeployment, *unstructured.Unstructured) []client.Object
		want         error
	}{
		{name: "invalid replicas", replicas: 0, want: strategies.ErrInvalidReplicas},
		{name: "negative replicas", controlPlane: true, replicas: -1, want: strategies.ErrInvalidReplicas},
		{name: "cluster paused", mutate: func(c *capiv1.Cluster, _ *capiv1.MachineDeployment, _ *unstructured.Unstructured) []client.Object {
			c.Spec.Paused = ptr.To(true)
			return nil
		}},
		{name: "cluster annotation pause", controlPlane: true, mutate: func(c *capiv1.Cluster, _ *capiv1.MachineDeployment, _ *unstructured.Unstructured) []client.Object {
			c.Annotations = map[string]string{capiv1.PausedAnnotation: ""}
			return nil
		}},
		{name: "cluster deleting", mutate: func(c *capiv1.Cluster, _ *capiv1.MachineDeployment, _ *unstructured.Unstructured) []client.Object {
			c.DeletionTimestamp = ptr.To(metav1.Now())
			c.Finalizers = []string{scalingFinalizer}
			return nil
		}},
		{name: "topology", mutate: func(c *capiv1.Cluster, _ *capiv1.MachineDeployment, _ *unstructured.Unstructured) []client.Object {
			c.Spec.Topology.ClassRef.Name = "class"
			return nil
		}},
		{name: "autoscaler", mutate: func(_ *capiv1.Cluster, md *capiv1.MachineDeployment, _ *unstructured.Unstructured) []client.Object {
			md.Annotations = map[string]string{capiv1.AutoscalerMinSizeAnnotation: "1"}
			return nil
		}},
		{name: "worker paused", mutate: func(_ *capiv1.Cluster, md *capiv1.MachineDeployment, _ *unstructured.Unstructured) []client.Object {
			md.Annotations = map[string]string{capiv1.PausedAnnotation: ""}
			return nil
		}},
		{name: "worker spec paused", mutate: func(_ *capiv1.Cluster, md *capiv1.MachineDeployment, _ *unstructured.Unstructured) []client.Object {
			md.Spec.Paused = ptr.To(true)
			return nil
		}},
		{name: "control plane deleting", controlPlane: true, mutate: func(_ *capiv1.Cluster, _ *capiv1.MachineDeployment, cp *unstructured.Unstructured) []client.Object {
			cp.SetDeletionTimestamp(ptr.To(metav1.Now()))
			cp.SetFinalizers([]string{scalingFinalizer})
			return nil
		}},
		{name: "worker deleting", mutate: func(_ *capiv1.Cluster, md *capiv1.MachineDeployment, _ *unstructured.Unstructured) []client.Object {
			md.DeletionTimestamp = ptr.To(metav1.Now())
			md.Finalizers = []string{scalingFinalizer}
			return nil
		}},
		{name: "worker owner mismatch", mutate: func(_ *capiv1.Cluster, md *capiv1.MachineDeployment, _ *unstructured.Unstructured) []client.Object {
			md.OwnerReferences[0].UID = "old-cluster"
			return nil
		}},
		{name: "worker cluster mismatch", mutate: func(_ *capiv1.Cluster, md *capiv1.MachineDeployment, _ *unstructured.Unstructured) []client.Object {
			md.Spec.ClusterName = scalingOtherCluster
			return nil
		}},
		{name: "multiple workers", mutate: func(_ *capiv1.Cluster, md *capiv1.MachineDeployment, _ *unstructured.Unstructured) []client.Object {
			other := md.DeepCopy()
			other.Name = "second"
			return []client.Object{other}
		}},
		{name: "no workers", mutate: func(_ *capiv1.Cluster, md *capiv1.MachineDeployment, _ *unstructured.Unstructured) []client.Object {
			md.Labels = nil
			return nil
		}},
		{name: "unsupported provider", controlPlane: true, mutate: func(c *capiv1.Cluster, _ *capiv1.MachineDeployment, _ *unstructured.Unstructured) []client.Object {
			c.Spec.ControlPlaneRef.Kind = "Other"
			return nil
		}},
		{name: "missing reference", controlPlane: true, mutate: func(c *capiv1.Cluster, _ *capiv1.MachineDeployment, _ *unstructured.Unstructured) []client.Object {
			c.Spec.ControlPlaneRef.Name = ""
			return nil
		}},
		{name: "missing control plane", controlPlane: true, mutate: func(c *capiv1.Cluster, _ *capiv1.MachineDeployment, _ *unstructured.Unstructured) []client.Object {
			c.Spec.ControlPlaneRef.Name = "missing"
			return nil
		}},
		{name: "control plane paused", controlPlane: true, mutate: func(_ *capiv1.Cluster, _ *capiv1.MachineDeployment, cp *unstructured.Unstructured) []client.Object {
			cp.SetAnnotations(map[string]string{capiv1.PausedAnnotation: ""})
			return nil
		}},
		{name: "control plane owner mismatch", controlPlane: true, mutate: func(_ *capiv1.Cluster, _ *capiv1.MachineDeployment, cp *unstructured.Unstructured) []client.Object {
			owners := cp.GetOwnerReferences()
			owners[0].UID = "old"
			cp.SetOwnerReferences(owners)
			return nil
		}},
		{name: "stacked etcd even", controlPlane: true, replicas: 2, want: strategies.ErrInvalidControlPlaneReplicas},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cluster, md, cp := scalingObjects()
			objects := []client.Object{cluster, md, cp}
			if tc.mutate != nil {
				objects = append(objects, tc.mutate(cluster, md, cp)...)
			}
			service := NewClusterClient(newFakeClient(t, objects...), newFakeCache(t, objects...), testNamespace)
			replicas := tc.replicas
			if replicas == 0 && tc.name != "invalid replicas" {
				replicas = 3
			}
			var err error
			if tc.controlPlane {
				err = service.SetControlPlaneReplicas(t.Context(), testClusterUUID, replicas)
			} else {
				err = service.SetWorkerReplicas(t.Context(), testClusterUUID, replicas)
			}
			want := tc.want
			if want == nil {
				want = strategies.ErrClusterScalingConflict
			}
			require.ErrorIs(t, err, want)
		})
	}
}

func TestScalingExternalEtcd(t *testing.T) {
	cluster, md, cp := scalingObjects()
	require.NoError(t, unstructured.SetNestedMap(cp.Object, map[string]any{"endpoints": []any{"https://etcd:2379"}}, "spec", "kubeadmConfigSpec", "clusterConfiguration", "etcd", "external"))
	service := NewClusterClient(newFakeClient(t, cluster, md, cp), newFakeCache(t, cluster, md, cp), testNamespace)
	require.NoError(t, service.SetControlPlaneReplicas(t.Context(), testClusterUUID, 2))
}

func TestScalingPreservesResourcesAndNamespace(t *testing.T) {
	cluster, md, cp := scalingObjects()
	md.Annotations = map[string]string{"scaling.example/annotation": "preserved"}
	md.Spec.Template.Spec.Version = "v1.36.1"
	cp.SetAnnotations(map[string]string{"scaling.example/annotation": "preserved"})
	require.NoError(t, unstructured.SetNestedMap(cp.Object, map[string]any{"spec": map[string]any{"infrastructureRef": reference(xoGroup, xoMachineTemplateKind, "demo-cp")}}, "spec", "machineTemplate"))
	otherMD := md.DeepCopy()
	otherMD.Namespace = otherNamespace
	otherCP := cp.DeepCopy()
	otherCP.SetNamespace(otherNamespace)
	k8s := newFakeClient(t, cluster, md, cp, otherMD, otherCP)
	require.NoError(t, k8s.Get(t.Context(), client.ObjectKeyFromObject(md), md))
	require.NoError(t, k8s.Get(t.Context(), client.ObjectKeyFromObject(cp), cp))
	expectedMD, expectedCP := md.DeepCopy(), cp.DeepCopy()
	service := NewClusterClient(k8s, clientAsCache{k8s}, testNamespace)
	require.NoError(t, service.SetWorkerReplicas(t.Context(), testClusterUUID, 3))
	require.NoError(t, service.SetControlPlaneReplicas(t.Context(), testClusterUUID, 3))
	require.NoError(t, k8s.Get(t.Context(), client.ObjectKeyFromObject(md), md))
	require.NoError(t, k8s.Get(t.Context(), client.ObjectKeyFromObject(cp), cp))
	expectedMD.Spec.Replicas = ptr.To(int32(3))
	expectedMD.ResourceVersion = md.ResourceVersion
	require.NoError(t, unstructured.SetNestedField(expectedCP.Object, int64(3), "spec", "replicas"))
	expectedCP.SetResourceVersion(cp.GetResourceVersion())
	require.Equal(t, expectedMD, md)
	require.Equal(t, expectedCP, cp)
	require.NoError(t, k8s.Get(t.Context(), client.ObjectKeyFromObject(otherMD), otherMD))
	require.NoError(t, k8s.Get(t.Context(), client.ObjectKeyFromObject(otherCP), otherCP))
	require.Equal(t, int32(1), *otherMD.Spec.Replicas)
	count, _, err := unstructured.NestedInt64(otherCP.Object, "spec", "replicas")
	require.NoError(t, err)
	require.Equal(t, int64(1), count)
}

func TestScalingPatchAndErrors(t *testing.T) {
	for _, controlPlane := range []bool{false, true} {
		for _, kind := range []string{scalingNoopCase, scalingConflictCase, scalingGoneCase, scalingAdmissionCase, "timeout", scalingGetCase, scalingListCase} {
			if controlPlane && kind == scalingListCase {
				continue
			}
			t.Run(kind+map[bool]string{true: "/cp", false: "/workers"}[controlPlane], func(t *testing.T) {
				cluster, md, cp := scalingObjects()
				patches := 0
				injected := errors.New("technical failure")
				interceptors := interceptor.Funcs{Patch: func(ctx context.Context, c client.WithWatch, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
					if _, ok := obj.(*capiv1.Cluster); ok {
						return c.Patch(ctx, obj, patch, opts...)
					}
					patches++
					data, err := patch.Data(obj)
					require.NoError(t, err)
					require.Contains(t, string(data), "resourceVersion")
					require.NotContains(t, string(data), "kubeadmConfigSpec")
					switch kind {
					case scalingConflictCase:
						return apierrors.NewConflict(schema.GroupResource{Resource: scalingTarget}, obj.GetName(), injected)
					case scalingGoneCase:
						return apierrors.NewNotFound(schema.GroupResource{Resource: scalingTarget}, obj.GetName())
					case scalingAdmissionCase:
						return apierrors.NewInvalid(schema.GroupKind{Kind: scalingTarget}, obj.GetName(), nil)
					default:
						return injected
					}
				}}
				if kind == scalingGetCase {
					interceptors.Get = func(context.Context, client.WithWatch, client.ObjectKey, client.Object, ...client.GetOption) error {
						return injected
					}
				}
				if kind == scalingListCase {
					interceptors.List = func(context.Context, client.WithWatch, client.ObjectList, ...client.ListOption) error {
						return injected
					}
				}
				k8s := interceptor.NewClient(newFakeClient(t, cluster, md, cp).(client.WithWatch), interceptors)
				service := NewClusterClient(k8s, clientAsCache{k8s}, testNamespace)
				replicas := int32(3)
				if kind == scalingNoopCase {
					replicas = 1
				}
				var err error
				if controlPlane {
					err = service.SetControlPlaneReplicas(t.Context(), testClusterUUID, replicas)
				} else {
					err = service.SetWorkerReplicas(t.Context(), testClusterUUID, replicas)
				}
				switch kind {
				case scalingNoopCase:
					require.NoError(t, err)
					require.Zero(t, patches)
				case scalingConflictCase, scalingGoneCase:
					require.ErrorIs(t, err, strategies.ErrClusterScalingConflict)
				case scalingAdmissionCase:
					require.ErrorIs(t, err, strategies.ErrReplicasRejected)
					require.True(t, apierrors.IsInvalid(err))
				default:
					require.ErrorIs(t, err, injected)
				}
			})
		}
	}
}

func TestScalingMissingClusterAndNamespace(t *testing.T) {
	service := NewClusterClient(newFakeClient(t), newFakeCache(t), testNamespace)
	require.ErrorIs(t, service.SetWorkerReplicas(t.Context(), uuids.ClusterID("missing"), 3), strategies.ErrClusterNotFound)
	require.ErrorIs(t, service.SetControlPlaneReplicas(t.Context(), uuid.Nil, 3), strategies.ErrClusterIDRequired)
	service.namespace = ""
	require.ErrorIs(t, service.SetWorkerReplicas(t.Context(), testClusterUUID, 3), strategies.ErrClusterNamespaceRequired)
}

func TestPauseRoundTrip(t *testing.T) {
	cluster := &capiv1.Cluster{ObjectMeta: metav1.ObjectMeta{Name: clusterName, Namespace: testNamespace, Annotations: map[string]string{"keep": preservedAnnotationValue}}}
	machine := machineWithStatus(deleteMachineName, testNamespace, clusterName, false, "")
	machine.Annotations = map[string]string{"keep": preservedAnnotationValue, capiv1.PausedAnnotation: "old"}
	kube := newFakeClient(t, cluster, machine)
	clusters := NewClusterClient(kube, clientAsCache{kube}, testNamespace)
	machines := NewMachineClient(kube, clientAsCache{kube}, testNamespace)
	for _, paused := range []bool{true, true, false, false} {
		require.NoError(t, clusters.SetPaused(t.Context(), clusterUUID, paused))
		require.NoError(t, machines.SetMachinePaused(t.Context(), clusterUUID, uuids.NodeID(clusterName, machine.Name), paused))
		require.NoError(t, kube.Get(t.Context(), client.ObjectKeyFromObject(cluster), cluster))
		require.NoError(t, kube.Get(t.Context(), client.ObjectKeyFromObject(machine), machine))
		require.Equal(t, paused, toInfo(cluster).Paused)
		require.Equal(t, paused, toClusterNodeInfo(machine).Paused)
		require.Equal(t, preservedAnnotationValue, cluster.Annotations["keep"])
		require.Equal(t, preservedAnnotationValue, machine.Annotations["keep"])
		value, present := machine.Annotations[capiv1.PausedAnnotation]
		require.Equal(t, paused, present)
		require.Empty(t, value)
		if !paused {
			require.Nil(t, cluster.Spec.Paused)
		}
	}
}

func TestResumeClusterPreservesMachinePause(t *testing.T) {
	cluster := &capiv1.Cluster{ObjectMeta: metav1.ObjectMeta{Name: clusterName, Namespace: testNamespace}, Spec: capiv1.ClusterSpec{Paused: ptr.To(true)}}
	machine := machineWithStatus(deleteMachineName, testNamespace, clusterName, false, "")
	machine.Annotations = map[string]string{capiv1.PausedAnnotation: ""}
	kube := newFakeClient(t, cluster, machine)
	require.NoError(t, NewClusterClient(kube, clientAsCache{kube}, testNamespace).SetPaused(t.Context(), clusterUUID, false))
	require.NoError(t, kube.Get(t.Context(), client.ObjectKeyFromObject(machine), machine))
	require.True(t, toClusterNodeInfo(machine).Paused)
}

func TestPauseValidation(t *testing.T) {
	machine := machineWithStatus(deleteMachineName, testNamespace, otherClusterName, false, "")
	machine.Labels[capiv1.ClusterNameLabel] = clusterName
	kube := newFakeClient(t, machine)
	clusters := NewClusterClient(kube, clientAsCache{kube}, testNamespace)
	machines := NewMachineClient(kube, clientAsCache{kube}, testNamespace)
	for _, paused := range []bool{true, false} {
		require.ErrorIs(t, clusters.SetPaused(t.Context(), uuid.Nil, paused), strategies.ErrClusterIDRequired)
		require.ErrorIs(t, NewClusterClient(kube, clientAsCache{kube}, "").SetPaused(t.Context(), clusterUUID, paused), strategies.ErrClusterNamespaceRequired)
		require.ErrorIs(t, clusters.SetPaused(t.Context(), clusterUUID, paused), strategies.ErrClusterNotFound)
		require.ErrorIs(t, machines.SetMachinePaused(t.Context(), uuid.Nil, deleteMachineUUID, paused), strategies.ErrClusterIDRequired)
		require.ErrorIs(t, machines.SetMachinePaused(t.Context(), clusterUUID, uuid.Nil, paused), strategies.ErrNodeIDRequired)
		require.ErrorIs(t, NewMachineClient(kube, clientAsCache{kube}, "").SetMachinePaused(t.Context(), clusterUUID, deleteMachineUUID, paused), strategies.ErrClusterNamespaceRequired)
		require.ErrorIs(t, machines.SetMachinePaused(t.Context(), clusterUUID, uuid.FromStringOrNil("00000000-0000-4000-8000-000000000001"), paused), strategies.ErrNodeNotFound)
		require.ErrorIs(t, machines.SetMachinePaused(t.Context(), clusterUUID, deleteMachineUUID, paused), strategies.ErrNodeNotFound)
	}
	require.NoError(t, kube.Get(t.Context(), client.ObjectKeyFromObject(machine), machine))
	require.Empty(t, machine.Annotations)
}

func TestPausePatchErrors(t *testing.T) {
	for _, tc := range []struct {
		name      string
		err, want error
	}{
		{"conflict", apierrors.NewConflict(schema.GroupResource{Group: capiv1.GroupVersion.Group, Resource: pauseMachineResource}, deleteMachineName, errTest), strategies.ErrPauseUpdateConflict},
		{"not found", apierrors.NewNotFound(schema.GroupResource{Group: capiv1.GroupVersion.Group, Resource: pauseMachineResource}, deleteMachineName), nil},
		{"failure", errTest, errTest},
	} {
		for _, targetMachine := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/machine=%t", tc.name, targetMachine), func(t *testing.T) {
				cluster := &capiv1.Cluster{ObjectMeta: metav1.ObjectMeta{Name: clusterName, Namespace: testNamespace}}
				machine := machineWithStatus(deleteMachineName, testNamespace, clusterName, false, "")
				kube := fake.NewClientBuilder().WithScheme(newTestScheme(t)).WithObjects(cluster, machine).
					WithIndex(&capiv1.Cluster{}, uuids.UUIDIndexKey, clusterUUIDIndexer).
					WithIndex(&capiv1.Machine{}, uuids.UUIDIndexKey, machineUUIDIndexer).
					WithIndex(&capiv1.Machine{}, uuids.ClusterUUIDIndexKey, machineClusterUUIDIndexer).WithInterceptorFuncs(interceptor.Funcs{
					Patch: func(ctx context.Context, underlying client.WithWatch, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
						data, err := patch.Data(obj)
						require.NoError(t, err)
						if strings.Contains(string(data), mutationAnnotation) {
							return underlying.Patch(ctx, obj, patch, opts...)
						}
						require.Contains(t, string(data), `"resourceVersion"`)
						return tc.err
					},
				}).Build()
				want := tc.want
				var err error
				if targetMachine {
					if want == nil {
						want = strategies.ErrNodeNotFound
					}
					err = NewMachineClient(kube, clientAsCache{kube}, testNamespace).SetMachinePaused(t.Context(), clusterUUID, deleteMachineUUID, true)
				} else {
					if want == nil {
						want = strategies.ErrClusterNotFound
					}
					err = NewClusterClient(kube, clientAsCache{kube}, testNamespace).SetPaused(t.Context(), clusterUUID, true)
				}
				require.ErrorIs(t, err, want)
			})
		}
	}
}

func TestSetDescriptionPersistence(t *testing.T) {
	for _, annotations := range []map[string]string{nil, {"description-test-metadata": preservedAnnotationValue, clusterDescriptionAnnotation: "initial description"}} {
		t.Run("annotations", func(t *testing.T) {
			original := &capiv1.Cluster{ObjectMeta: metav1.ObjectMeta{
				Name: clusterName, Namespace: testNamespace, Annotations: annotations,
				Labels: map[string]string{"description-test-metadata": preservedAnnotationValue},
			}, Spec: capiv1.ClusterSpec{Paused: ptr.To(true)}}
			kube := newFakeClient(t, original)
			resource := NewClusterClient(kube, clientAsCache{kube}, testNamespace)
			for _, description := range []string{"Équipe \"Backend\" 🚀\nTest", "updated", "updated", "", ""} {
				require.NoError(t, resource.SetDescription(t.Context(), clusterUUID, description))
				stored := &capiv1.Cluster{}
				require.NoError(t, kube.Get(t.Context(), types.NamespacedName{Name: clusterName, Namespace: testNamespace}, stored))
				require.Equal(t, description, stored.Annotations[clusterDescriptionAnnotation])
				require.Equal(t, original.Annotations["description-test-metadata"], stored.Annotations["description-test-metadata"])
				require.Equal(t, original.Labels, stored.Labels)
				require.Equal(t, original.Spec, stored.Spec)
				if description == "" {
					require.NotContains(t, stored.Annotations, clusterDescriptionAnnotation)
				}
				fresh := NewClusterClient(kube, clientAsCache{kube}, testNamespace)
				info, err := fresh.Get(t.Context(), clusterName)
				require.NoError(t, err)
				require.Equal(t, description, info.Description)
				listed, err := fresh.List(t.Context())
				require.NoError(t, err)
				require.Len(t, listed, 1)
				require.Equal(t, description, listed[0].Description)
			}
		})
	}
}

func TestSetDescriptionErrors(t *testing.T) {
	kube := newFakeClient(t)
	resource := NewClusterClient(kube, clientAsCache{kube}, testNamespace)
	require.ErrorIs(t, resource.SetDescription(t.Context(), clusterUUID, "new"), strategies.ErrClusterNotFound)
	for _, tc := range []struct{ patchErr, wantErr error }{
		{apierrors.NewConflict(schema.GroupResource{Resource: "clusters"}, clusterName, errTest), strategies.ErrClusterDescriptionUpdateConflict},
		{apierrors.NewNotFound(schema.GroupResource{Resource: "clusters"}, clusterName), strategies.ErrClusterNotFound},
		{errTest, errTest},
	} {
		t.Run(tc.wantErr.Error(), func(t *testing.T) {
			kube := fake.NewClientBuilder().WithScheme(newTestScheme(t)).
				WithObjects(&capiv1.Cluster{ObjectMeta: metav1.ObjectMeta{Name: clusterName, Namespace: testNamespace}}).
				WithIndex(&capiv1.Cluster{}, uuids.UUIDIndexKey, clusterUUIDIndexer).
				WithInterceptorFuncs(interceptor.Funcs{
					Patch: func(context.Context, client.WithWatch, client.Object, client.Patch, ...client.PatchOption) error {
						return tc.patchErr
					},
				}).Build()
			require.ErrorIs(t, NewClusterClient(kube, clientAsCache{kube}, testNamespace).SetDescription(t.Context(), clusterUUID, "new"), tc.wantErr)
		})
	}
}

func TestClusterCreatorAnnotationOptional(t *testing.T) {
	for _, creatorID := range []string{"xo-user-123", ""} {
		t.Run(creatorID, func(t *testing.T) {
			params, objects, scheme := newClusterCreationTestSetup(t)
			params.CreatedByXOUserID = creatorID
			kube := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).Build()
			resource := NewClusterClient(kube, clientAsCache{kube}, testCreationNamespace)
			created, err := resource.Create(t.Context(), params)
			require.NoError(t, err)
			require.Equal(t, creatorID, created.CreatedByXOUserID)
			stored := &capiv1.Cluster{}
			require.NoError(t, kube.Get(t.Context(), client.ObjectKey{Name: params.Name, Namespace: testCreationNamespace}, stored))
			if creatorID == "" {
				require.NotContains(t, stored.Annotations, clusterCreatedByXOUserIDAnnotation)
			} else {
				require.Equal(t, creatorID, stored.Annotations[clusterCreatedByXOUserIDAnnotation])
			}
			detail, err := resource.Get(t.Context(), params.Name)
			require.NoError(t, err)
			require.Equal(t, creatorID, detail.CreatedByXOUserID)
		})
	}
}

func TestScalingRequiresCurrentKnownConditions(t *testing.T) {
	cluster := &capiv1.Cluster{ObjectMeta: metav1.ObjectMeta{Generation: 2}}
	assert.Nil(t, clusterScaling(cluster))
	cluster.Status.Conditions = []metav1.Condition{
		{Type: capiv1.ClusterScalingUpCondition, Status: metav1.ConditionTrue, ObservedGeneration: 1},
		{Type: capiv1.ClusterScalingDownCondition, Status: metav1.ConditionFalse, ObservedGeneration: 2},
	}
	assert.Nil(t, clusterScaling(cluster), "stale conditions must not start an operation")
	cluster.Status.Conditions[0].ObservedGeneration = 2
	require.NotNil(t, clusterScaling(cluster))
	assert.True(t, *clusterScaling(cluster))
	cluster.Status.Conditions[0].Status = metav1.ConditionUnknown
	assert.Nil(t, clusterScaling(cluster))
	cluster.Status.Conditions[0].Status = metav1.ConditionFalse
	require.NotNil(t, clusterScaling(cluster))
	assert.False(t, *clusterScaling(cluster))
}
