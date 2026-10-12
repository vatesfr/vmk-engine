package kubernetes_clients

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	capiv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	"capv-proxy/pkg/strategies"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/utils/ptr"
)

const (
	otherK8sVersion     = "v1.35.0"
	k8sVersion          = "v1.32.0"
	clusterName         = "test-cluster"
	testNamespace       = "capv"
	provisionedPhase    = "Provisioned"
	provisioningPhase   = "Provisioning"
	caseEmptyName       = "empty name"
	caseEmptyNamespace  = "empty namespace"
	errClusterName      = "cluster name is required"
	errClusterNamespace = "cluster namespace is required"
	errNotFound         = "not found"
	caseEmptyTagValue   = "empty value"
	testTagLabelPrefix  = "tags.vmk.vates.tech/"
	ownerTag            = "owner"
	platformValue       = "platform"
	productionValue     = "production"
)

var errTest = errors.New("test error")

func TestClusterClientList(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		objects := []client.Object{
			&capiv1.Cluster{ObjectMeta: metav1.ObjectMeta{Name: "other", Namespace: otherNamespace}},
			&capiv1.Cluster{ObjectMeta: metav1.ObjectMeta{Name: "provisioned", Namespace: testNamespace}, Status: capiv1.ClusterStatus{Phase: provisionedPhase}},
			&capiv1.Cluster{ObjectMeta: metav1.ObjectMeta{Name: "provisioning", Namespace: testNamespace}, Status: capiv1.ClusterStatus{Phase: provisioningPhase}},
		}
		clusterClient := NewClusterClient(newFakeClient(t, objects...), testNamespace)

		clusters, err := clusterClient.List(t.Context())

		require.NoError(t, err)
		assert.ElementsMatch(t, []strategies.ClusterInfo{
			{Name: "provisioned", Phase: provisionedPhase, CreatedAt: time.Time{}.String()},
			{Name: "provisioning", Phase: provisioningPhase, CreatedAt: time.Time{}.String()},
		}, clusters)
	})

	t.Run("empty", func(t *testing.T) {
		clusterClient := NewClusterClient(newFakeClient(t), testNamespace)

		clusters, err := clusterClient.List(t.Context())

		require.NoError(t, err)
		assert.Empty(t, clusters)
		assert.NotNil(t, clusters)
	})

	t.Run("error", func(t *testing.T) {
		clusterClient := NewClusterClient(
			newErrorClient(t, interceptor.Funcs{List: listErrorFunc(t, errTest)}), testNamespace,
		)

		clusters, err := clusterClient.List(t.Context())

		assert.Nil(t, clusters)
		assert.ErrorIs(t, err, errTest)
	})
}

func TestClusterClientGet(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		cluster := &capiv1.Cluster{
			ObjectMeta: metav1.ObjectMeta{Name: clusterName, Namespace: testNamespace},
			Spec:       capiv1.ClusterSpec{ControlPlaneEndpoint: capiv1.APIEndpoint{Host: controlPlaneIP, Port: 6443}},
			Status:     capiv1.ClusterStatus{Phase: provisionedPhase},
		}
		clusterClient := NewClusterClient(newFakeClient(t, cluster), testNamespace)

		info, err := clusterClient.Get(t.Context(), clusterName)

		require.NoError(t, err)
		assert.Equal(t, strategies.ClusterInfo{
			CreatedAt: time.Time{}.String(), Name: clusterName, Phase: provisionedPhase, ControlPlaneEndpoint: "https://" + controlPlaneIP + ":6443",
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
		clusterClient := NewClusterClient(newFakeClient(t, cluster), testNamespace)

		info, err := clusterClient.Get(t.Context(), clusterName)

		require.NoError(t, err)
		assert.Equal(t, strategies.ClusterInfo{
			Name: clusterName, Version: k8sVersion, CreatedAt: createdAt.Local().String(),
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
		clusterClient := NewClusterClient(newFakeClient(t, cluster), testNamespace)

		info, err := clusterClient.Get(t.Context(), clusterName)

		require.NoError(t, err)
		assert.Equal(t, strategies.ClusterInfo{
			Name: clusterName, Version: k8sVersion, CreatedAt: time.Time{}.String(),
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
		clusterClient := NewClusterClient(newFakeClient(t, cluster), testNamespace)

		info, err := clusterClient.Get(t.Context(), clusterName)

		require.NoError(t, err)
		assert.Equal(t, strategies.ClusterInfo{
			Name: clusterName, CreatedAt: time.Time{}.String(),
			ControlPlanesStatus: strategies.NodeTypeStatus{DesiredReplicas: 3},
			WorkersStatus:       strategies.NodeTypeStatus{Replicas: 2},
		}, info)
	})

	t.Run("invalid endpoint", func(t *testing.T) {
		cluster := &capiv1.Cluster{ObjectMeta: metav1.ObjectMeta{Name: clusterName, Namespace: testNamespace}}
		clusterClient := NewClusterClient(newFakeClient(t, cluster), testNamespace)

		info, err := clusterClient.Get(t.Context(), clusterName)

		require.NoError(t, err)
		assert.Empty(t, info.ControlPlaneEndpoint)
	})

	t.Run("error", func(t *testing.T) {
		clusterClient := NewClusterClient(
			newErrorClient(t, interceptor.Funcs{Get: getErrorFunc(t, errTest)}), testNamespace,
		)

		info, err := clusterClient.Get(t.Context(), clusterName)

		assert.Empty(t, info)
		assert.ErrorIs(t, err, errTest)
	})

	t.Run("not found", func(t *testing.T) {
		clusterClient := NewClusterClient(newFakeClient(t), testNamespace)

		info, err := clusterClient.Get(t.Context(), clusterName)

		assert.Empty(t, info)
		require.Error(t, err)
		assert.ErrorIs(t, err, strategies.ErrClusterNotFound)
	})
}

func TestClusterClientDelete(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		cluster := &capiv1.Cluster{ObjectMeta: metav1.ObjectMeta{Name: clusterName, Namespace: testNamespace}}
		kubeClient := newFakeClient(t, cluster)
		clusterClient := NewClusterClient(kubeClient, testNamespace)

		err := clusterClient.Delete(t.Context(), clusterName)

		require.NoError(t, err)
		err = kubeClient.Get(context.Background(), types.NamespacedName{Namespace: testNamespace, Name: clusterName}, &capiv1.Cluster{})
		assert.Error(t, err)
	})

	t.Run("empty name", func(t *testing.T) {
		clusterClient := NewClusterClient(newFakeClient(t), testNamespace)

		err := clusterClient.Delete(t.Context(), "")

		assert.EqualError(t, err, errClusterName)
	})

	t.Run("empty namespace", func(t *testing.T) {
		clusterClient := NewClusterClient(newFakeClient(t), "")

		err := clusterClient.Delete(t.Context(), clusterName)

		assert.EqualError(t, err, errClusterNamespace)
	})

	t.Run("error", func(t *testing.T) {
		clusterClient := NewClusterClient(
			newErrorClient(t, interceptor.Funcs{Delete: deleteErrorFunc(t, errTest)}), testNamespace,
		)

		err := clusterClient.Delete(t.Context(), clusterName)

		require.Error(t, err)
		assert.ErrorIs(t, err, errTest)
		assert.Contains(t, err.Error(), "delete Cluster "+testNamespace+"/"+clusterName)
	})

	t.Run("not found", func(t *testing.T) {
		clusterClient := NewClusterClient(newFakeClient(t), testNamespace)

		err := clusterClient.Delete(t.Context(), clusterName)

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
			clusterClient := NewClusterClient(kubeClient, testNamespace)
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
			clusterClient := NewClusterClient(kubeClient, testNamespace)

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
			clusterClient := NewClusterClient(newFakeClient(t, cluster), testNamespace)
			tags, err := clusterClient.GetTags(t.Context(), clusterName)
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
				value, err := clusterClient.GetTag(t.Context(), clusterName, tag)
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
			clusterClient := NewClusterClient(kubeClient, testNamespace)
			var err error
			if tc.deleteTag {
				err = clusterClient.DeleteTag(t.Context(), clusterName, tc.tag)
			} else {
				err = clusterClient.SetTag(t.Context(), clusterName, tc.tag, tc.value)
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
				_, err := clusterClient.GetTag(t.Context(), clusterName, tc.tag)
				assert.ErrorIs(t, err, strategies.ErrClusterTagNotFound)
			}
		})
	}
}

func TestClusterClientTagMissingCluster(t *testing.T) {
	testCases := map[string]func(context.Context, ClusterClient) error{
		"list": func(ctx context.Context, c ClusterClient) error {
			_, err := c.GetTags(ctx, clusterName)
			return err
		},
		"get": func(ctx context.Context, c ClusterClient) error {
			_, err := c.GetTag(ctx, clusterName, ownerTag)
			return err
		},
		"set": func(ctx context.Context, c ClusterClient) error {
			return c.SetTag(ctx, clusterName, ownerTag, platformValue)
		},
		"delete": func(ctx context.Context, c ClusterClient) error {
			return c.DeleteTag(ctx, clusterName, ownerTag)
		},
	}
	for name, operation := range testCases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			clusterClient := NewClusterClient(newFakeClient(t), testNamespace)
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
		KubernetesVersion:         fixtureVersion,
		PodCIDR:                   fixturePodCIDR,
		ServiceCIDR:               fixtureServiceCIDR,
		ServiceDomain:             fixtureDomain,
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
		Name:                 fixtureCluster,
		Defaults:             d,
		ControlPlaneIP:       "192.0.2.10",
		VMNamePrefix:         "custom",
		ControlPlaneReplicas: ptr.To(int32(3)),
		MachineDeployments: []strategies.MachineDeployment{{
			Name:     fixtureWorkerName,
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
	p.Description = "Cluster de l’équipe \"Backend\"\nTest 🚀"
	creating := true
	kube := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).WithInterceptorFuncs(interceptor.Funcs{
		Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
			if creating {
				require.Fail(t, "creation must not read Kubernetes templates")
				return errors.New("unexpected Get")
			}
			return c.Get(ctx, key, obj, opts...)
		},
	}).Build()
	info, err := NewClusterClient(kube, fixtureNamespace).Create(t.Context(), p)
	creating = false
	require.NoError(t, err)
	require.Equal(t, fixtureCluster, info.Name)
	require.Equal(t, p.Tags, info.Tags)
	require.Equal(t, p.Description, info.Description)
	cluster := &capiv1.Cluster{}
	require.NoError(t, kube.Get(t.Context(), client.ObjectKey{Name: fixtureCluster, Namespace: fixtureNamespace}, cluster))
	require.Equal(t, p.Description, cluster.Annotations["vmk.vates.tech/description"])
	// A fresh client reads the persisted description for both detail and list.
	freshClient := NewClusterClient(kube, fixtureNamespace)
	detail, err := freshClient.Get(t.Context(), fixtureCluster)
	require.NoError(t, err)
	require.Equal(t, p.Description, detail.Description)
	listed, err := freshClient.List(t.Context())
	require.NoError(t, err)
	require.Len(t, listed, 1)
	require.Equal(t, p.Description, listed[0].Description)
	require.Nil(t, cluster.Spec.Paused)
	require.Empty(t, cluster.Spec.Topology.ClassRef.Name)
	require.Equal(t, []string{fixturePodCIDR}, cluster.Spec.ClusterNetwork.Pods.CIDRBlocks)
	require.Equal(t, fixtureControlPlane, cluster.Spec.ControlPlaneRef.Name)
	require.Equal(t, fixtureCluster, cluster.Labels[capiv1.ClusterNameLabel])
	require.Equal(t, "test", cluster.Labels[clusterTagLabelPrefix+"env"])
	get := func(group, kind, name string) *unstructured.Unstructured {
		t.Helper()
		obj := resource(group, kind, name, fixtureNamespace, nil)
		require.NoError(t, kube.Get(t.Context(), client.ObjectKeyFromObject(obj), obj))
		return obj
	}
	xo := get(xoGroup, xoClusterKind, fixtureCluster)
	owner := metav1.GetControllerOf(xo)
	require.NotNil(t, owner)
	require.Equal(t, cluster.Name, owner.Name)
	require.Equal(t, cluster.UID, owner.UID)
	addons, _, err := unstructured.NestedMap(xo.Object, specKey, "addons")
	require.NoError(t, err)
	require.Equal(t, map[string]any{cniKey: ciliumCNI, "ccm": true, "csi": true}, addons)
	host, _, _ := unstructured.NestedString(xo.Object, specKey, endpointKey, "host")
	require.Equal(t, p.ControlPlaneIP, host)
	cp := get(cpGroup, controlPlaneKind, fixtureControlPlane)
	require.Nil(t, metav1.GetControllerOf(cp))
	replicas, _, _ := unstructured.NestedInt64(cp.Object, specKey, "replicas")
	require.Equal(t, int64(3), replicas)
	sans, _, _ := unstructured.NestedStringSlice(cp.Object, specKey, kubeadmConfigSpecKey, clusterConfigurationKey, apiServerKey, certSANsKey)
	require.Contains(t, sans, p.ControlPlaneIP)
	commands, _, _ := unstructured.NestedStringSlice(cp.Object, specKey, kubeadmConfigSpecKey, commandsKey)
	require.Equal(t, []string{"echo preserved"}, commands)
	vm := get(xoGroup, xoMachineTemplateKind, fixtureControlPlane)
	prefix, _, _ := unstructured.NestedString(vm.Object, specKey, templateKey, specKey, namePrefixKey)
	require.Equal(t, "custom-cp", prefix)
	require.Equal(t, fixtureCluster, vm.GetOwnerReferences()[0].Name)
	require.Equal(t, fixtureOriginalPrefix, p.Defaults.ControlPlaneMachineSpec[namePrefixKey])
	md := &capiv1.MachineDeployment{}
	require.NoError(t, kube.Get(t.Context(), client.ObjectKey{Name: "demo-md-0", Namespace: fixtureNamespace}, md))
	require.Equal(t, int32(2), *md.Spec.Replicas)
	require.Equal(t, fixtureVersion, md.Spec.Template.Spec.Version)
	get(bootstrapGroup, bootstrapTemplateKind, "demo-worker")
	_, err = NewClusterClient(kube, fixtureNamespace).Create(t.Context(), p)
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
			_, err := NewClusterClient(kube, fixtureNamespace).Create(t.Context(), p)
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
		ObjectMeta: metav1.ObjectMeta{Name: fixtureCluster},
		Status:     capiv1.ClusterStatus{ControlPlane: &capiv1.ClusterControlPlaneStatus{Versions: []capiv1.StatusVersion{{Version: fixtureVersion}}}},
	}
	require.Equal(t, fixtureVersion, toInfo(cluster).Version)
}

func TestDeletePausedCluster(t *testing.T) {
	_, _, scheme := newClusterCreationTestSetup(t)
	cluster := &capiv1.Cluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:       fixtureCluster,
			Namespace:  fixtureNamespace,
			Finalizers: []string{"cluster.cluster.x-k8s.io"},
		},
		Spec: capiv1.ClusterSpec{Paused: ptr.To(true)},
	}
	kube := fake.NewClientBuilder().WithScheme(scheme).WithObjects(cluster).Build()
	require.NoError(t, NewClusterClient(kube, fixtureNamespace).Delete(t.Context(), fixtureCluster))
	require.NoError(t, kube.Get(t.Context(), client.ObjectKeyFromObject(cluster), cluster))
	require.False(t, cluster.DeletionTimestamp.IsZero())
	require.Nil(t, cluster.Spec.Paused)
}

const (
	fixtureCluster        = "demo"
	fixtureNamespace      = "default"
	fixtureVersion        = "v1.36.1"
	fixturePodCIDR        = "172.31.0.0/16"
	fixtureControlPlane   = "demo-cp"
	fixtureOriginalPrefix = "original"
)

const (
	fixtureServiceCIDR = "192.168.0.0/16"
	fixtureDomain      = "cluster.local"
	fixtureWorkerName  = "md-0"
)

func TestClusterCreationValidation(t *testing.T) {
	for _, tc := range []struct {
		name   string
		modify func(*strategies.CreateClusterResourceParams, *string)
		want   error
	}{
		{"missing name", func(p *strategies.CreateClusterResourceParams, _ *string) { p.Name = "" }, strategies.ErrClusterNameRequired},
		{"missing namespace", func(_ *strategies.CreateClusterResourceParams, ns *string) { *ns = "" }, strategies.ErrClusterNamespaceRequired},
		{"invalid tag", func(p *strategies.CreateClusterResourceParams, _ *string) {
			p.Tags = map[string]string{"invalid/key": platformValue}
		}, strategies.ErrInvalidClusterTagName},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, objects, scheme := newClusterCreationTestSetup(t)
			ns := fixtureNamespace
			tc.modify(&p, &ns)
			kube := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).Build()
			_, err := NewClusterClient(kube, ns).Create(t.Context(), p)
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
	_, err := NewClusterClient(kube, fixtureNamespace).Create(t.Context(), p)
	require.ErrorIs(t, err, injected)
}

func getValidXOMachineSpec() map[string]any {
	return map[string]any{
		"templateID": "image", "poolID": "pool", namePrefixKey: fixtureOriginalPrefix,
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

const (
	kubeVIP   = "kube-vip"
	ciliumCNI = "cilium"
)
