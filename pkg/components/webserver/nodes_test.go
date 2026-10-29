package webserver

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	eventmanager "github.com/vatesfr/vmk-engine/pkg/components/event_manager"
	"github.com/vatesfr/vmk-engine/pkg/strategies"
	"github.com/vatesfr/vmk-engine/pkg/strategies/mocks"

	"k8s.io/utils/ptr"
)

func TestNodeStateJSONPreservesFalseAndOmitsUnknown(t *testing.T) {
	ctrl := gomock.NewController(t)
	clusters := mocks.NewMockForManagingClusters(ctrl)
	versions := mocks.NewMockForManagingVersions(ctrl)
	clusters.EXPECT().GetClusterNodesInfo(gomock.Any(), clusterUUID).AnyTimes().Return([]strategies.ClusterNodeInfo{
		{Name: "known", IsCordon: ptr.To(false), IsDraining: ptr.To(false)}, {Name: "unknown"},
	}, nil)
	handler := HttpServer{SwaggerUI: SwaggerUIDisabled}.Handler(clusters, nil, versions, eventmanager.NewEventManager())
	for _, query := range []string{"", "?fields=name,is_cordon,is_draining"} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/kubernetes/clusters/"+clusterUUID.String()+"/nodes"+query, nil))
		require.Equal(t, http.StatusOK, response.Code)
		var result []map[string]any
		require.NoError(t, json.Unmarshal(response.Body.Bytes(), &result))
		require.Equal(t, false, result[0]["is_cordon"])
		require.Equal(t, false, result[0]["is_draining"])
		require.NotContains(t, result[1], "is_cordon")
		require.NotContains(t, result[1], "is_draining")
	}
}

func TestNodeRoutesOpenAPI(t *testing.T) {
	ctrl := gomock.NewController(t)
	server := HttpServer{SwaggerUI: SwaggerUIDisabled}.newServer(mocks.NewMockForManagingClusters(ctrl), nil, mocks.NewMockForManagingVersions(ctrl), eventmanager.NewEventManager())
	actions := []string{"cordon", "uncordon", "drain"}
	for _, action := range actions {
		path := server.OpenAPI.Description().Paths.Find("/kubernetes/clusters/{id}/nodes/{nodeID}/" + action)
		require.NotNil(t, path)
		operation := path.Post
		require.NotNil(t, operation)
		for _, code := range []string{"204", "404", "409", "422", "503", "504"} {
			require.NotNil(t, operation.Responses.Value(code))
		}
	}
}

func TestClusterStateJSONOnListAndDetail(t *testing.T) {
	ctrl := gomock.NewController(t)
	clusters := mocks.NewMockForManagingClusters(ctrl)
	versions := mocks.NewMockForManagingVersions(ctrl)
	info := strategies.ClusterInfo{Name: "cluster", IsCordon: ptr.To(false), IsDraining: ptr.To(true)}
	clusters.EXPECT().GetClusters(gomock.Any()).Return([]strategies.ClusterInfo{info}, nil)
	clusters.EXPECT().GetClusterByUUID(gomock.Any(), clusterUUID).Return(info, nil)
	handler := HttpServer{SwaggerUI: SwaggerUIDisabled}.Handler(clusters, nil, versions, eventmanager.NewEventManager())
	for _, path := range []string{"/kubernetes/clusters?fields=name,is_cordon,is_draining", "/kubernetes/clusters/" + clusterUUID.String()} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		require.Equal(t, http.StatusOK, response.Code)
		var result map[string]any
		if strings.Contains(path, "?") {
			var list []map[string]any
			require.NoError(t, json.Unmarshal(response.Body.Bytes(), &list))
			require.Len(t, list, 1)
			result = list[0]
		} else {
			require.NoError(t, json.Unmarshal(response.Body.Bytes(), &result))
		}
		require.Equal(t, false, result["is_cordon"])
		require.Equal(t, true, result["is_draining"])
	}
}
