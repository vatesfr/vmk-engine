package cluster_manager_test

import (
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"capv-proxy/pkg/components/cluster_manager"
	"capv-proxy/pkg/strategies"
	"capv-proxy/pkg/strategies/mocks"
)

func TestSetReplicas(t *testing.T) {
	for _, controlPlane := range []bool{false, true} {
		for _, tc := range []struct {
			name              string
			cluster           string
			replicas          int32
			resourceErr, want error
		}{
			{name: "sets target", cluster: clusterName, replicas: 3},
			{name: "missing name", replicas: 3, want: strategies.ErrClusterNameRequired},
			{name: "zero", cluster: clusterName, want: strategies.ErrInvalidReplicas},
			{name: "negative", cluster: clusterName, replicas: -1, want: strategies.ErrInvalidReplicas},
			{name: "conflict", cluster: clusterName, replicas: 3, resourceErr: strategies.ErrClusterScalingConflict, want: strategies.ErrClusterScalingConflict},
			{name: "not found", cluster: clusterName, replicas: 3, resourceErr: strategies.ErrClusterNotFound, want: strategies.ErrClusterNotFound},
			{name: "technical error", cluster: clusterName, replicas: 3, resourceErr: errTest, want: errTest},
		} {
			target := "workers"
			if controlPlane {
				target = "control-plane"
			}
			t.Run(target+"/"+tc.name, func(t *testing.T) {
				resources := mocks.NewMockForManagingClusterResources(gomock.NewController(t))
				if tc.cluster != "" && tc.replicas > 0 {
					if controlPlane {
						resources.EXPECT().SetControlPlaneReplicas(t.Context(), tc.cluster, tc.replicas).Return(tc.resourceErr)
					} else {
						resources.EXPECT().SetWorkerReplicas(t.Context(), tc.cluster, tc.replicas).Return(tc.resourceErr)
					}
				}
				manager := cluster_manager.NewClusterManager(resources, nil, nil, nil, nil)
				var err error
				if controlPlane {
					err = manager.SetControlPlaneReplicas(t.Context(), tc.cluster, tc.replicas)
				} else {
					err = manager.SetWorkerReplicas(t.Context(), tc.cluster, tc.replicas)
				}
				require.ErrorIs(t, err, tc.want)
				if tc.resourceErr != nil {
					require.ErrorIs(t, err, strategies.ErrClusterScalingFailed)
				}
			})
		}
	}
}
