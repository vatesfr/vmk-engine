package kubernetes_clients_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	capiv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	"capv-proxy/pkg/components/kubernetes_clients"
	"capv-proxy/pkg/strategies"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
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
			createErr:       errTest,
			oracleErr:       errTest,
			wantErrContains: "create Cluster " + testNamespace + "/" + clusterName,
		},
	}

	for name, tc := range testCases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			kubeClient := newFakeClient(t)
			if tc.createErr != nil {
				kubeClient = newErrorClient(t, interceptor.Funcs{Create: createErrorFunc(t, tc.createErr)})
			}
			clusterClient := kubernetes_clients.NewClusterClient(kubeClient, tc.namespace)

			info, err := clusterClient.Create(t.Context(), tc.params)

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

		info, err := clusterClient.Create(t.Context(), strategies.CreateClusterResourceParams{Name: clusterName})

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

		clusters, err := clusterClient.List(t.Context())

		require.NoError(t, err)
		assert.ElementsMatch(t, []strategies.ClusterInfo{
			{Name: "provisioned", Phase: provisionedPhase, CreatedAt: time.Time{}.String()},
			{Name: "provisioning", Phase: provisioningPhase, CreatedAt: time.Time{}.String()},
		}, clusters)
	})

	t.Run("empty", func(t *testing.T) {
		clusterClient := kubernetes_clients.NewClusterClient(newFakeClient(t), testNamespace)

		clusters, err := clusterClient.List(t.Context())

		require.NoError(t, err)
		assert.Empty(t, clusters)
		assert.NotNil(t, clusters)
	})

	t.Run("error", func(t *testing.T) {
		clusterClient := kubernetes_clients.NewClusterClient(
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
		clusterClient := kubernetes_clients.NewClusterClient(newFakeClient(t, cluster), testNamespace)

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
				},
				Workers: &capiv1.WorkersStatus{
					AvailableReplicas: ptr.To(int32(5)), DesiredReplicas: ptr.To(int32(7)),
					ReadyReplicas: ptr.To(int32(6)), Replicas: ptr.To(int32(8)),
				},
			},
		}
		clusterClient := kubernetes_clients.NewClusterClient(newFakeClient(t, cluster), testNamespace)

		info, err := clusterClient.Get(t.Context(), clusterName)

		require.NoError(t, err)
		assert.Equal(t, strategies.ClusterInfo{
			Name: clusterName, Version: k8sVersion, CreatedAt: createdAt.Local().String(),
			ControlPlanesStatus: strategies.NodeTypeStatus{AvailableReplicas: 1, DesiredReplicas: 3, ReadyReplicas: 2, Replicas: 4},
			WorkersStatus:       strategies.NodeTypeStatus{AvailableReplicas: 5, DesiredReplicas: 7, ReadyReplicas: 6, Replicas: 8},
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
		clusterClient := kubernetes_clients.NewClusterClient(newFakeClient(t, cluster), testNamespace)

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
		clusterClient := kubernetes_clients.NewClusterClient(newFakeClient(t, cluster), testNamespace)

		info, err := clusterClient.Get(t.Context(), clusterName)

		require.NoError(t, err)
		assert.Empty(t, info.ControlPlaneEndpoint)
	})

	t.Run("error", func(t *testing.T) {
		clusterClient := kubernetes_clients.NewClusterClient(
			newErrorClient(t, interceptor.Funcs{Get: getErrorFunc(t, errTest)}), testNamespace,
		)

		info, err := clusterClient.Get(t.Context(), clusterName)

		assert.Empty(t, info)
		assert.ErrorIs(t, err, errTest)
	})

	t.Run("not found", func(t *testing.T) {
		clusterClient := kubernetes_clients.NewClusterClient(newFakeClient(t), testNamespace)

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
		clusterClient := kubernetes_clients.NewClusterClient(kubeClient, testNamespace)

		err := clusterClient.Delete(t.Context(), clusterName)

		require.NoError(t, err)
		err = kubeClient.Get(context.Background(), types.NamespacedName{Namespace: testNamespace, Name: clusterName}, &capiv1.Cluster{})
		assert.Error(t, err)
	})

	t.Run("empty name", func(t *testing.T) {
		clusterClient := kubernetes_clients.NewClusterClient(newFakeClient(t), testNamespace)

		err := clusterClient.Delete(t.Context(), "")

		assert.EqualError(t, err, errClusterName)
	})

	t.Run("empty namespace", func(t *testing.T) {
		clusterClient := kubernetes_clients.NewClusterClient(newFakeClient(t), "")

		err := clusterClient.Delete(t.Context(), clusterName)

		assert.EqualError(t, err, errClusterNamespace)
	})

	t.Run("error", func(t *testing.T) {
		clusterClient := kubernetes_clients.NewClusterClient(
			newErrorClient(t, interceptor.Funcs{Delete: deleteErrorFunc(t, errTest)}), testNamespace,
		)

		err := clusterClient.Delete(t.Context(), clusterName)

		require.Error(t, err)
		assert.ErrorIs(t, err, errTest)
		assert.Contains(t, err.Error(), "delete Cluster "+testNamespace+"/"+clusterName)
	})

	t.Run("not found", func(t *testing.T) {
		clusterClient := kubernetes_clients.NewClusterClient(newFakeClient(t), testNamespace)

		err := clusterClient.Delete(t.Context(), clusterName)

		require.Error(t, err)
		assert.ErrorIs(t, err, strategies.ErrClusterNotFound)
	})
}

const caseEmptyTagValue = "empty value"

const testTagLabelPrefix = "tags.vmk.vates.tech/"

const (
	ownerTag        = "owner"
	platformValue   = "platform"
	productionValue = "production"
)

func TestClusterClientCreatesClusterWithTags(t *testing.T) {
	testCases := map[string]struct {
		tags    map[string]string
		labels  map[string]string
		wantErr error
	}{
		"multiple tags": {
			tags:   map[string]string{"environment": productionValue, ownerTag: platformValue},
			labels: map[string]string{testTagLabelPrefix + "environment": productionValue, testTagLabelPrefix + ownerTag: platformValue},
		},
		"no tags":         {},
		caseEmptyTagValue: {tags: map[string]string{ownerTag: ""}, labels: map[string]string{testTagLabelPrefix + ownerTag: ""}},
		"invalid name":    {tags: map[string]string{strings.Repeat("a", 64): "value"}, wantErr: strategies.ErrInvalidClusterTagName},
	}
	for name, tc := range testCases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			kubeClient := newFakeClient(t)
			clusterClient := kubernetes_clients.NewClusterClient(kubeClient, testNamespace)
			info, err := clusterClient.Create(t.Context(), strategies.CreateClusterResourceParams{Name: clusterName, Tags: tc.tags})
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
			clusterClient := kubernetes_clients.NewClusterClient(newFakeClient(t, cluster), testNamespace)
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
			clusterClient := kubernetes_clients.NewClusterClient(kubeClient, testNamespace)
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
	testCases := map[string]func(context.Context, kubernetes_clients.ClusterClient) error{
		"list": func(ctx context.Context, c kubernetes_clients.ClusterClient) error {
			_, err := c.GetTags(ctx, clusterName)
			return err
		},
		"get": func(ctx context.Context, c kubernetes_clients.ClusterClient) error {
			_, err := c.GetTag(ctx, clusterName, ownerTag)
			return err
		},
		"set": func(ctx context.Context, c kubernetes_clients.ClusterClient) error {
			return c.SetTag(ctx, clusterName, ownerTag, platformValue)
		},
		"delete": func(ctx context.Context, c kubernetes_clients.ClusterClient) error {
			return c.DeleteTag(ctx, clusterName, ownerTag)
		},
	}
	for name, operation := range testCases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			clusterClient := kubernetes_clients.NewClusterClient(newFakeClient(t), testNamespace)
			assert.ErrorIs(t, operation(t.Context(), clusterClient), strategies.ErrClusterNotFound)
		})
	}
}
