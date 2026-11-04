package webserver

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	eventmanager "github.com/vatesfr/vmk-engine/pkg/components/event_manager"
	"github.com/vatesfr/vmk-engine/pkg/strategies"
	"github.com/vatesfr/vmk-engine/pkg/strategies/mocks"
)

const (
	volumeName     = "pvc-xo"
	volumeID       = "volume-id"
	volumeNodeName = "worker"
	volumeCluster  = "with-volume"
	nameField      = "name"
)

var testVolume = strategies.WorkloadVolume{
	Name: volumeName, CapacityBytes: 1024, Phase: "Bound",
	Claim: &strategies.VolumeClaim{Namespace: "app", Name: "data"},
	Source: strategies.VolumeSource{Type: "csi", CSI: &strategies.CSIVolumeSource{
		Driver: "csi.xenorchestra.vates.tech", VolumeHandle: volumeID,
		XO: &strategies.XOVolume{SRID: "sr", PoolID: "pool"},
	}},
	AttachedNodes: []strategies.VolumeNode{{NodeName: volumeNodeName, ID: &clusterUUID}},
}

func serveJSON(t *testing.T, handler http.Handler, path string, result any) int {
	t.Helper()
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
	if response.Code == http.StatusOK {
		require.NoError(t, json.Unmarshal(response.Body.Bytes(), result))
	}
	return response.Code
}

func TestClusterVolumesJSON(t *testing.T) {
	ctrl := gomock.NewController(t)
	clusters := mocks.NewMockForManagingClusters(ctrl)
	clusters.EXPECT().GetClusterVolumes(gomock.Any(), clusterUUID).Return([]strategies.WorkloadVolume{testVolume}, nil).Times(2)
	handler := HttpServer{SwaggerUI: SwaggerUIDisabled}.Handler(clusters, nil, mocks.NewMockForManagingVersions(ctrl), eventmanager.NewEventManager())

	var volumes []map[string]any
	require.Equal(t, http.StatusOK, serveJSON(t, handler, "/kubernetes/clusters/"+clusterUUID.String()+"/volumes", &volumes))
	require.Equal(t, volumeName, volumes[0]["name"])
	require.Equal(t, map[string]any{"namespace": "app", nameField: "data"}, volumes[0]["claim"])
	source := volumes[0]["source"].(map[string]any)
	require.Equal(t, "csi", source["type"])
	require.Equal(t, map[string]any{"driver": "csi.xenorchestra.vates.tech", "volume_handle": volumeID, "xo": map[string]any{"sr_id": "sr", "pool_id": "pool"}}, source["csi"])
	require.Equal(t, []any{map[string]any{"node_name": volumeNodeName, "id": clusterUUID.String()}}, volumes[0]["attached_nodes"])

	volumes = nil
	require.Equal(t, http.StatusOK, serveJSON(t, handler, "/kubernetes/clusters/"+clusterUUID.String()+"/volumes?fields=name,source.csi.volume_handle", &volumes))
	require.Equal(t, []map[string]any{{nameField: volumeName, "source": map[string]any{"csi": map[string]any{"volume_handle": volumeID}}}}, volumes)
}

func TestClusterVolumesUnavailable(t *testing.T) {
	ctrl := gomock.NewController(t)
	clusters := mocks.NewMockForManagingClusters(ctrl)
	clusters.EXPECT().GetClusterVolumes(gomock.Any(), clusterUUID).Return(nil, strategies.ErrWorkloadUnavailable)
	handler := HttpServer{SwaggerUI: SwaggerUIDisabled}.Handler(clusters, nil, mocks.NewMockForManagingVersions(ctrl), eventmanager.NewEventManager())
	require.Equal(t, http.StatusServiceUnavailable, serveJSON(t, handler, "/kubernetes/clusters/"+clusterUUID.String()+"/volumes", nil))
}

func TestClusterJSONIncludesVolumesOnListAndDetail(t *testing.T) {
	ctrl := gomock.NewController(t)
	clusters := mocks.NewMockForManagingClusters(ctrl)
	clusters.EXPECT().GetClusters(gomock.Any()).Return([]strategies.ClusterInfo{
		{Name: volumeCluster, Volumes: []strategies.WorkloadVolume{testVolume}},
		{Name: "empty", Volumes: []strategies.WorkloadVolume{}},
		{Name: "offline"},
	}, nil)
	clusters.EXPECT().GetClusterByUUID(gomock.Any(), clusterUUID).Return(strategies.ClusterInfo{Name: volumeCluster, Volumes: []strategies.WorkloadVolume{testVolume}}, nil)
	handler := HttpServer{SwaggerUI: SwaggerUIDisabled}.Handler(clusters, nil, mocks.NewMockForManagingVersions(ctrl), eventmanager.NewEventManager())

	var list []map[string]any
	require.Equal(t, http.StatusOK, serveJSON(t, handler, "/kubernetes/clusters?fields=name,volumes.name", &list))
	require.Equal(t, []map[string]any{
		{nameField: volumeCluster, "volumes": []any{map[string]any{nameField: volumeName}}},
		{nameField: "empty", "volumes": []any{}},
		{nameField: "offline"},
	}, list)

	var detail map[string]any
	require.Equal(t, http.StatusOK, serveJSON(t, handler, "/kubernetes/clusters/"+clusterUUID.String(), &detail))
	require.Len(t, detail["volumes"], 1)
}

func TestClusterVolumesOpenAPI(t *testing.T) {
	ctrl := gomock.NewController(t)
	server := HttpServer{SwaggerUI: SwaggerUIDisabled}.newServer(mocks.NewMockForManagingClusters(ctrl), nil, mocks.NewMockForManagingVersions(ctrl), eventmanager.NewEventManager())
	path := server.OpenAPI.Description().Paths.Find("/kubernetes/clusters/{id}/volumes")
	require.NotNil(t, path)
	require.NotNil(t, path.Get)
	for _, code := range []string{"200", "404", "422", "503"} {
		require.NotNil(t, path.Get.Responses.Value(code), code)
	}
}
