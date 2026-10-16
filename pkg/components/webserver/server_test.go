package webserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"capv-proxy/pkg/components/cluster_manager"
	versionmanager "capv-proxy/pkg/components/version_manager"
	uuids "capv-proxy/pkg/helpers/uuids"
	"capv-proxy/pkg/strategies"
	"capv-proxy/pkg/strategies/mocks"
)

const (
	scalingWorkerTarget            = "workers"
	k8sVersion                     = "v1.36.0"
	clusterNotFoundTitle           = "Cluster not found"
	productionValue                = "production"
	environmentTag                 = "environment"
	clusterName                    = "cluster-1"
	secondClusterName              = "cluster-2"
	clusterPhase                   = "Provisioned"
	secondClusterPhase             = "Provisioning"
	controlPlaneIP                 = "10.0.0.10"
	controlPlaneEndpoint           = controlPlaneIP
	vmNamePrefix                   = "test"
	controlPlaneReplicas     int32 = 1
	workerReplicas           int32 = 1
	controlPlaneNodeName           = "control-plane-0"
	controlPlaneNodeRole           = "control-plane"
	controlPlaneNodeStatus         = "Ready"
	controlPlaneNodeEndpoint       = "10.0.0.11"
	clusterConfig                  = "apiVersion: v1\nkind: Config"
	pingPath                       = "/ping"
	clustersPath                   = "/kubernetes/clusters"
	getClusterPathInvalid          = "/kubernetes/clusters/cluster-1"
	allNodesPath                   = "/kubernetes/nodes"
	allTagsPath                    = "/kubernetes/tags"
	contentTypeHeader              = "Content-Type"
	acceptHeader                   = "Accept"
	applicationJSON                = "application/json"
	unknownPath                    = "/unknown"
	createClusterRequestBody       = `{
		"name":"cluster-1",
		"kubernetes_version":"1.36.9",
		"control_plane_replicas":1,
		"worker_replicas":1,
		"control_plane_IP":"10.0.0.10",
		"vm_name_prefix":"test"
	}`
	malformedRequestBody   = `{"name":`
	missingNameRequestBody = `{
		"kubernetes_version":"1.36.9",
		"control_plane_replicas":1,
		"worker_replicas":1,
		"control_plane_IP":"10.0.0.10",
		"vm_name_prefix":"test"
	}`
	invalidControlPlaneIPRequestBody = `{
		"name":"cluster-1",
		"kubernetes_version":"1.36.9",
		"control_plane_replicas":1,
		"worker_replicas":1,
		"control_plane_IP":"invalid",
		"vm_name_prefix":"test"
	}`
	invalidControlPlaneReplicasRequestBody = `{
		"name":"cluster-1",
		"kubernetes_version":"1.36.9",
		"control_plane_replicas":0,
		"worker_replicas":1,
		"control_plane_IP":"10.0.0.10",
		"vm_name_prefix":"test"
	}`
	invalidWorkerReplicasRequestBody = `{
		"name":"cluster-1",
		"kubernetes_version":"1.36.9",
		"control_plane_replicas":1,
		"worker_replicas":0,
		"control_plane_IP":"10.0.0.10",
		"vm_name_prefix":"test"
	}`
	missingVMNamePrefixRequestBody = `{
		"name":"cluster-1",
		"kubernetes_version":"1.36.9",
		"control_plane_replicas":1,
		"worker_replicas":1,
		"control_plane_IP":"10.0.0.10"
	}`
)

var (
	errTest              = errors.New("test error")
	clusterUUID          = uuids.ClusterID(clusterName)
	node1UUID            = uuids.NodeID(clusterName, "node-1")
	getClusterByUUIDPath = "/kubernetes/clusters/" + clusterUUID.String()
	clusterNodesPath     = getClusterByUUIDPath + "/nodes"
	getNodeByUUIDPath    = "/kubernetes/nodes/" + uuids.NodeID(clusterName, controlPlaneNodeName).String()

	clusterConfigPath = getClusterByUUIDPath + "/config"
	clusterTagsPath   = getClusterByUUIDPath + "/tags"
	clusterTagPath    = getClusterByUUIDPath + "/tags/environment"
)

type httpTestCase struct {
	method         string
	path           string
	body           string
	setupMock      func(context.Context, *mocks.MockForManagingClusters)
	expectedStatus int
	expectedJSON   string
	expectedTitle  string
	expectedDetail string
	expectedFields int
}

func TestHTTPRoutes(t *testing.T) {
	testCases := map[string]httpTestCase{
		"ping": {
			method:         http.MethodGet,
			path:           pingPath,
			expectedStatus: http.StatusOK,
			expectedJSON:   `{"message":"pong"}`,
		},
		"list clusters": {
			method: http.MethodGet,
			path:   clustersPath,
			setupMock: func(ctx context.Context, service *mocks.MockForManagingClusters) {
				service.EXPECT().GetClusters(ctx).Return([]strategies.ClusterInfo{
					{Name: clusterName, Phase: clusterPhase, IsAvailable: true, ControlPlaneEndpoint: controlPlaneEndpoint},
					{Name: secondClusterName, Phase: secondClusterPhase},
				}, nil)
			},
			expectedStatus: http.StatusOK,
			expectedJSON: `[
				{"id":"00000000-0000-0000-0000-000000000000","is_reconciliation_paused":false,"is_available":true,"name":"cluster-1","phase":"Provisioned","control_plane_endpoint":"10.0.0.10",
                "control_plane_status":{"available_replicas":0,"desired_replicas":0,"ready_replicas":0,"replicas":0,"up_to_date_replicas":0},
                "worker_status":{"available_replicas":0,"desired_replicas":0,"ready_replicas":0,"replicas":0,"up_to_date_replicas":0}},
				{"id":"00000000-0000-0000-0000-000000000000","is_reconciliation_paused":false,"is_available":false,"name":"cluster-2","phase":"Provisioning",
                "control_plane_status":{"available_replicas":0,"desired_replicas":0,"ready_replicas":0,"replicas":0,"up_to_date_replicas":0},
                "worker_status":{"available_replicas":0,"desired_replicas":0,"ready_replicas":0,"replicas":0,"up_to_date_replicas":0}}
			]`,
		},
		"list empty clusters": {
			method: http.MethodGet,
			path:   clustersPath,
			setupMock: func(ctx context.Context, service *mocks.MockForManagingClusters) {
				service.EXPECT().GetClusters(ctx).Return(nil, nil)
			},
			expectedStatus: http.StatusOK,
			expectedJSON:   `[]`,
		},
		"list clusters with all fields": {
			method: http.MethodGet,
			path:   clustersPath + "?fields=*",
			setupMock: func(ctx context.Context, service *mocks.MockForManagingClusters) {
				service.EXPECT().GetClusters(ctx).Return([]strategies.ClusterInfo{
					{Name: clusterName, Phase: clusterPhase, IsAvailable: true, ControlPlaneEndpoint: controlPlaneEndpoint},
					{Name: secondClusterName, Phase: secondClusterPhase},
				}, nil)
			},
			expectedStatus: http.StatusOK,
			expectedJSON: `[
				{"id":"00000000-0000-0000-0000-000000000000","is_reconciliation_paused":false,"is_available":true,"name":"cluster-1","phase":"Provisioned","control_plane_endpoint":"10.0.0.10",
                "control_plane_status":{"available_replicas":0,"desired_replicas":0,"ready_replicas":0,"replicas":0,"up_to_date_replicas":0},
                "worker_status":{"available_replicas":0,"desired_replicas":0,"ready_replicas":0,"replicas":0,"up_to_date_replicas":0}},
				{"id":"00000000-0000-0000-0000-000000000000","is_reconciliation_paused":false,"is_available":false,"name":"cluster-2","phase":"Provisioning",
                "control_plane_status":{"available_replicas":0,"desired_replicas":0,"ready_replicas":0,"replicas":0,"up_to_date_replicas":0},
                "worker_status":{"available_replicas":0,"desired_replicas":0,"ready_replicas":0,"replicas":0,"up_to_date_replicas":0}}
			]`,
		},
		"list clusters with selected nested fields": {
			method: http.MethodGet,
			path:   clustersPath + "?fields=name,control_plane_status.ready_replicas",
			setupMock: func(ctx context.Context, service *mocks.MockForManagingClusters) {
				service.EXPECT().GetClusters(ctx).Return([]strategies.ClusterInfo{
					{Name: clusterName, Phase: clusterPhase},
					{Name: secondClusterName, Phase: secondClusterPhase},
				}, nil)
			},
			expectedStatus: http.StatusOK,
			expectedJSON: `[{
				"name":"cluster-1","control_plane_status":{"ready_replicas":0}},
				{"name":"cluster-2","control_plane_status":{"ready_replicas":0}}
			]`,
		},
		"list clusters with selected availability": {
			method: http.MethodGet,
			path:   clustersPath + "?fields=name,is_available",
			setupMock: func(ctx context.Context, service *mocks.MockForManagingClusters) {
				service.EXPECT().GetClusters(ctx).Return([]strategies.ClusterInfo{
					{Name: clusterName, IsAvailable: true},
					{Name: secondClusterName, IsAvailable: false},
				}, nil)
			},
			expectedStatus: http.StatusOK,
			expectedJSON:   `[{"name":"cluster-1","is_available":true},{"name":"cluster-2","is_available":false}]`,
		},
		"get available cluster": {
			method: http.MethodGet,
			path:   getClusterByUUIDPath,
			setupMock: func(ctx context.Context, service *mocks.MockForManagingClusters) {
				service.EXPECT().GetClusterByUUID(ctx, clusterUUID).Return(strategies.ClusterInfo{Name: clusterName, IsAvailable: true}, nil)
			},
			expectedStatus: http.StatusOK,
			expectedJSON: `{"id":"00000000-0000-0000-0000-000000000000","is_reconciliation_paused":false,"name":"cluster-1","is_available":true,
				"control_plane_status":{"available_replicas":0,"desired_replicas":0,"ready_replicas":0,"replicas":0,"up_to_date_replicas":0},
				"worker_status":{"available_replicas":0,"desired_replicas":0,"ready_replicas":0,"replicas":0,"up_to_date_replicas":0}}`,
		},
		"get cluster": {
			method: http.MethodGet,
			path:   getClusterByUUIDPath,
			setupMock: func(ctx context.Context, service *mocks.MockForManagingClusters) {
				service.EXPECT().GetClusterByUUID(ctx, clusterUUID).Return(strategies.ClusterInfo{
					ID: clusterUUID, Name: clusterName, Phase: clusterPhase, ControlPlaneEndpoint: controlPlaneEndpoint,
				}, nil)
			},
			expectedStatus: http.StatusOK,
			expectedJSON: `{"id":"` + clusterUUID.String() + `","is_reconciliation_paused":false,"is_available":false,"name":"cluster-1","phase":"Provisioned","control_plane_endpoint":"10.0.0.10",
                "control_plane_status":{"available_replicas":0,"desired_replicas":0,"ready_replicas":0,"replicas":0,"up_to_date_replicas":0},
                "worker_status":{"available_replicas":0,"desired_replicas":0,"ready_replicas":0,"replicas":0,"up_to_date_replicas":0}}`,
		},
		"get cluster with invalid UUID": {
			method:         http.MethodGet,
			path:           getClusterPathInvalid,
			expectedStatus: http.StatusBadRequest,
			expectedTitle:  "Invalid UUID",
			expectedDetail: "path parameter 'id' must be a valid UUID",
		},
		"get missing cluster by UUID": {
			method: http.MethodGet,
			path:   getClusterByUUIDPath,
			setupMock: func(ctx context.Context, service *mocks.MockForManagingClusters) {
				service.EXPECT().GetClusterByUUID(ctx, clusterUUID).Return(strategies.ClusterInfo{}, strategies.ErrClusterNotFound)
			},
			expectedStatus: http.StatusNotFound,
			expectedTitle:  clusterNotFoundTitle,
			expectedDetail: strategies.ErrClusterNotFound.Error(),
		},
		"get cluster with version status": {
			method: http.MethodGet,
			path:   getClusterByUUIDPath,
			setupMock: func(ctx context.Context, service *mocks.MockForManagingClusters) {
				service.EXPECT().GetClusterByUUID(ctx, clusterUUID).Return(strategies.ClusterInfo{
					Name: clusterName,
					ControlPlanesStatus: strategies.NodeTypeStatus{
						UpToDateReplicas: 1,
						Versions:         []strategies.VersionNodeStatus{{Version: k8sVersion, Replicas: 1}, {Version: "v1.35.0", Replicas: 2}},
					},
					WorkersStatus: strategies.NodeTypeStatus{
						UpToDateReplicas: 4,
						Versions:         []strategies.VersionNodeStatus{{Version: k8sVersion, Replicas: 4}},
					},
				}, nil)
			},
			expectedStatus: http.StatusOK,
			expectedJSON: `{"id":"00000000-0000-0000-0000-000000000000","is_reconciliation_paused":false,"is_available":false,"name":"cluster-1",
				"control_plane_status":{"available_replicas":0,"desired_replicas":0,"ready_replicas":0,"replicas":0,"up_to_date_replicas":1,
					"versions":[{"version":"v1.36.0","replicas":1},{"version":"v1.35.0","replicas":2}]},
				"worker_status":{"available_replicas":0,"desired_replicas":0,"ready_replicas":0,"replicas":0,"up_to_date_replicas":4,
					"versions":[{"version":"v1.36.0","replicas":4}]}}`,
		},
		"list clusters with selected version fields": {
			method: http.MethodGet,
			path:   clustersPath + "?fields=name,control_plane_status.up_to_date_replicas,control_plane_status.versions.version,worker_status.versions",
			setupMock: func(ctx context.Context, service *mocks.MockForManagingClusters) {
				service.EXPECT().GetClusters(ctx).Return([]strategies.ClusterInfo{{
					Name: clusterName,
					ControlPlanesStatus: strategies.NodeTypeStatus{
						UpToDateReplicas: 1,
						Versions:         []strategies.VersionNodeStatus{{Version: k8sVersion, Replicas: 1}, {Version: "v1.35.0", Replicas: 2}},
					},
					WorkersStatus: strategies.NodeTypeStatus{
						UpToDateReplicas: 4,
						Versions:         []strategies.VersionNodeStatus{{Version: k8sVersion, Replicas: 4}},
					},
				}}, nil)
			},
			expectedStatus: http.StatusOK,
			expectedJSON: `[{"name":"cluster-1",
				"control_plane_status":{"up_to_date_replicas":1,"versions":[{"version":"v1.36.0"},{"version":"v1.35.0"}]},
				"worker_status":{"versions":[{"version":"v1.36.0","replicas":4}]}}]`,
		},
		"get cluster nodes": {
			method: http.MethodGet,
			path:   clusterNodesPath,
			setupMock: func(ctx context.Context, service *mocks.MockForManagingClusters) {
				service.EXPECT().GetClusterNodesInfo(ctx, clusterUUID).Return([]strategies.ClusterNodeInfo{{
					Name:          controlPlaneNodeName,
					ParentCluster: clusterUUID,
					Role:          controlPlaneNodeRole,
					Status:        controlPlaneNodeStatus,
					Endpoint:      controlPlaneNodeEndpoint,
					Version:       k8sVersion,
				}}, nil)
			},
			expectedStatus: http.StatusOK,
			expectedJSON: `[{"id":"00000000-0000-0000-0000-000000000000","is_reconciliation_paused":false,"name":"control-plane-0","role":"control-plane",` +
				`"status":"Ready","endpoint":"10.0.0.11","$cluster":"` + clusterUUID.String() + `","version":"v1.36.0"}]`,
		},
		"get cluster nodes with all fields": {
			method: http.MethodGet,
			path:   clusterNodesPath + "?fields=*",
			setupMock: func(ctx context.Context, service *mocks.MockForManagingClusters) {
				service.EXPECT().GetClusterNodesInfo(ctx, clusterUUID).Return([]strategies.ClusterNodeInfo{{
					Name: controlPlaneNodeName, Role: controlPlaneNodeRole,
					Status: controlPlaneNodeStatus, Endpoint: controlPlaneNodeEndpoint, Version: k8sVersion,
					ParentCluster: clusterUUID,
				}}, nil)
			},
			expectedStatus: http.StatusOK,
			expectedJSON: `[{"id":"00000000-0000-0000-0000-000000000000","is_reconciliation_paused":false,"name":"control-plane-0","role":"control-plane",` +
				`"status":"Ready","endpoint":"10.0.0.11","$cluster":"` + clusterUUID.String() + `","version":"v1.36.0"}]`,
		},
		"get cluster nodes with selected fields": {
			method: http.MethodGet,
			path:   clusterNodesPath + "?fields=name,status",
			setupMock: func(ctx context.Context, service *mocks.MockForManagingClusters) {
				service.EXPECT().GetClusterNodesInfo(ctx, clusterUUID).Return([]strategies.ClusterNodeInfo{{
					Name: controlPlaneNodeName, Role: controlPlaneNodeRole,
					Status: controlPlaneNodeStatus, Endpoint: controlPlaneNodeEndpoint, Version: k8sVersion,
				}}, nil)
			},
			expectedStatus: http.StatusOK,
			expectedJSON:   `[{"name":"control-plane-0","status":"Ready"}]`,
		},
		"get cluster nodes with selected version": {
			method: http.MethodGet,
			path:   clusterNodesPath + "?fields=name,version",
			setupMock: func(ctx context.Context, service *mocks.MockForManagingClusters) {
				service.EXPECT().GetClusterNodesInfo(ctx, clusterUUID).Return([]strategies.ClusterNodeInfo{{
					Name: controlPlaneNodeName, Role: controlPlaneNodeRole, Version: k8sVersion,
				}}, nil)
			},
			expectedStatus: http.StatusOK,
			expectedJSON:   `[{"name":"control-plane-0","version":"v1.36.0"}]`,
		},
		"get cluster config": {
			method: http.MethodGet,
			path:   clusterConfigPath,
			setupMock: func(ctx context.Context, service *mocks.MockForManagingClusters) {
				service.EXPECT().GetClusterConfig(ctx, clusterUUID).Return(clusterConfig, nil)
			},
			expectedStatus: http.StatusOK,
			expectedJSON:   `{"kubeconfig":"apiVersion: v1\nkind: Config"}`,
		},
		"create cluster": {
			method: http.MethodPost,
			path:   clustersPath,
			body:   createClusterRequestBody,
			setupMock: func(ctx context.Context, service *mocks.MockForManagingClusters) {
				service.EXPECT().CreateCluster(ctx, validCreateClusterParams()).Return(strategies.ClusterInfo{
					Name: clusterName, Phase: clusterPhase, ControlPlaneEndpoint: controlPlaneEndpoint,
				}, nil)
			},
			expectedStatus: http.StatusOK,
			expectedJSON: `{"id":"00000000-0000-0000-0000-000000000000","is_reconciliation_paused":false,"is_available":false,"name":"cluster-1","phase":"Provisioned","control_plane_endpoint":"10.0.0.10",
                "control_plane_status":{"available_replicas":0,"desired_replicas":0,"ready_replicas":0,"replicas":0,"up_to_date_replicas":0},
                "worker_status":{"available_replicas":0,"desired_replicas":0,"ready_replicas":0,"replicas":0,"up_to_date_replicas":0}}`,
		},
		"create cluster with tags": {
			method: http.MethodPost,
			path:   clustersPath,
			body:   strings.Replace(createClusterRequestBody, `"vm_name_prefix":"test"`, `"vm_name_prefix":"test","tags":{"environment":"production"}`, 1),
			setupMock: func(ctx context.Context, service *mocks.MockForManagingClusters) {
				params := validCreateClusterParams()
				params.Tags = map[string]string{environmentTag: productionValue}
				service.EXPECT().CreateCluster(ctx, params).Return(strategies.ClusterInfo{Name: clusterName, Tags: params.Tags}, nil)
			},
			expectedStatus: http.StatusOK,
			expectedJSON: `{"id":"00000000-0000-0000-0000-000000000000","is_reconciliation_paused":false,"is_available":false,"name":"cluster-1","tags":{"environment":"production"},
                "control_plane_status":{"available_replicas":0,"desired_replicas":0,"ready_replicas":0,"replicas":0,"up_to_date_replicas":0},
                "worker_status":{"available_replicas":0,"desired_replicas":0,"ready_replicas":0,"replicas":0,"up_to_date_replicas":0}}`,
		},
		"create cluster service error": {
			method: http.MethodPost,
			path:   clustersPath,
			body:   createClusterRequestBody,
			setupMock: func(ctx context.Context, service *mocks.MockForManagingClusters) {
				service.EXPECT().CreateCluster(ctx, validCreateClusterParams()).Return(strategies.ClusterInfo{}, errTest)
			},
			expectedStatus: http.StatusInternalServerError,
		},
		"create existing cluster": {
			method: http.MethodPost,
			path:   clustersPath,
			body:   createClusterRequestBody,
			setupMock: func(ctx context.Context, service *mocks.MockForManagingClusters) {
				service.EXPECT().CreateCluster(ctx, validCreateClusterParams()).Return(
					strategies.ClusterInfo{}, strategies.ErrClusterAlreadyExists,
				)
			},
			expectedStatus: http.StatusConflict,
		},
		"delete cluster": {
			method: http.MethodDelete,
			path:   getClusterByUUIDPath,
			setupMock: func(ctx context.Context, service *mocks.MockForManagingClusters) {
				service.EXPECT().DeleteCluster(ctx, clusterUUID).Return(nil)
			},
			expectedStatus: http.StatusNoContent,
		},
		"delete missing cluster": {
			method: http.MethodDelete,
			path:   getClusterByUUIDPath,
			setupMock: func(ctx context.Context, service *mocks.MockForManagingClusters) {
				service.EXPECT().DeleteCluster(ctx, clusterUUID).Return(strategies.ErrClusterNotFound)
			},
			expectedStatus: http.StatusNotFound,
			expectedTitle:  clusterNotFoundTitle,
			expectedDetail: strategies.ErrClusterNotFound.Error(),
		},
		"get all tags": {
			method: http.MethodGet,
			path:   allTagsPath,
			setupMock: func(ctx context.Context, service *mocks.MockForManagingClusters) {
				service.EXPECT().GetAllTags(ctx).Return(map[string]map[string]string{
					clusterName:       {environmentTag: productionValue, "owner": "platform"},
					secondClusterName: {},
				}, nil)
			},
			expectedStatus: http.StatusOK,
			expectedJSON:   `{"result":{"cluster-1":{"environment":"production","owner":"platform"},"cluster-2":{}}}`,
		},
		"get all tags without clusters": {
			method: http.MethodGet,
			path:   allTagsPath,
			setupMock: func(ctx context.Context, service *mocks.MockForManagingClusters) {
				service.EXPECT().GetAllTags(ctx).Return(map[string]map[string]string{}, nil)
			},
			expectedStatus: http.StatusOK,
			expectedJSON:   `{"result":{}}`,
		},
		"get all tags service error": {
			method: http.MethodGet,
			path:   allTagsPath,
			setupMock: func(ctx context.Context, service *mocks.MockForManagingClusters) {
				service.EXPECT().GetAllTags(ctx).Return(nil, fmt.Errorf("%w: %w", strategies.ErrClusterTagsRetrievalFailed, errTest))
			},
			expectedStatus: http.StatusInternalServerError,
		},
		"get cluster tags": {
			method: http.MethodGet,
			path:   clusterTagsPath,
			setupMock: func(ctx context.Context, service *mocks.MockForManagingClusters) {
				service.EXPECT().GetClusterTags(ctx, clusterUUID).Return(map[string]string{environmentTag: productionValue}, nil)
			},
			expectedStatus: http.StatusOK,
			expectedJSON:   `{"result":{"environment":"production"}}`,
		},
		"get cluster tag": {
			method: http.MethodGet,
			path:   clusterTagPath,
			setupMock: func(ctx context.Context, service *mocks.MockForManagingClusters) {
				service.EXPECT().GetClusterTag(ctx, clusterUUID, environmentTag).Return(productionValue, nil)
			},
			expectedStatus: http.StatusOK,
			expectedJSON:   `"production"`,
		},
		"set cluster tag": {
			method: http.MethodPut,
			path:   clusterTagPath,
			body:   `{"value":"production"}`,
			setupMock: func(ctx context.Context, service *mocks.MockForManagingClusters) {
				service.EXPECT().SetClusterTag(ctx, clusterUUID, environmentTag, productionValue).Return(nil)
			},
			expectedStatus: http.StatusNoContent,
		},
		"delete cluster tag": {
			method: http.MethodDelete,
			path:   clusterTagPath,
			setupMock: func(ctx context.Context, service *mocks.MockForManagingClusters) {
				service.EXPECT().DeleteClusterTag(ctx, clusterUUID, environmentTag).Return(nil)
			},
			expectedStatus: http.StatusNoContent,
		},
		"missing cluster tag": {
			method: http.MethodGet,
			path:   clusterTagPath,
			setupMock: func(ctx context.Context, service *mocks.MockForManagingClusters) {
				service.EXPECT().GetClusterTag(ctx, clusterUUID, environmentTag).Return("", strategies.ErrClusterTagNotFound)
			},
			expectedStatus: http.StatusNotFound,
			expectedTitle:  "Cluster tag not found",
			expectedDetail: strategies.ErrClusterTagNotFound.Error(),
		},
		"invalid tag value wrapped by service": {
			method: http.MethodPut,
			path:   clusterTagPath,
			body:   `{"value":"invalid value"}`,
			setupMock: func(ctx context.Context, service *mocks.MockForManagingClusters) {
				service.EXPECT().SetClusterTag(ctx, clusterUUID, environmentTag, "invalid value").Return(fmt.Errorf("update: %w", strategies.ErrInvalidClusterTagValue))
			},
			expectedStatus: http.StatusUnprocessableEntity,
			expectedTitle:  "Invalid cluster parameters",
			expectedDetail: "update: invalid cluster tag value",
		},
		"invalid tag on cluster creation": {
			method: http.MethodPost,
			path:   clustersPath,
			body:   strings.Replace(createClusterRequestBody, `"vmNamePrefix":"test"`, `"vmNamePrefix":"test","tags":{"invalid tag":"production"}`, 1),
			setupMock: func(ctx context.Context, service *mocks.MockForManagingClusters) {
				service.EXPECT().CreateCluster(ctx, gomock.Any()).Return(strategies.ClusterInfo{}, fmt.Errorf("create: %w", strategies.ErrInvalidClusterTagName))
			},
			expectedStatus: http.StatusUnprocessableEntity,
		},
		"invalid tag on retrieval": {
			method: http.MethodGet,
			path:   clusterTagPath,
			setupMock: func(ctx context.Context, service *mocks.MockForManagingClusters) {
				service.EXPECT().GetClusterTag(ctx, clusterUUID, environmentTag).Return("", strategies.ErrInvalidClusterTagName)
			},
			expectedStatus: http.StatusUnprocessableEntity,
		},
		"invalid tag on deletion": {
			method: http.MethodDelete,
			path:   clusterTagPath,
			setupMock: func(ctx context.Context, service *mocks.MockForManagingClusters) {
				service.EXPECT().DeleteClusterTag(ctx, clusterUUID, environmentTag).Return(strategies.ErrInvalidClusterTagName)
			},
			expectedStatus: http.StatusUnprocessableEntity,
		},
		"invalid cluster tag": {
			method: http.MethodPut,
			path:   clusterTagPath,
			body:   `{"value":"production"}`,
			setupMock: func(ctx context.Context, service *mocks.MockForManagingClusters) {
				service.EXPECT().SetClusterTag(ctx, clusterUUID, environmentTag, productionValue).Return(strategies.ErrInvalidClusterTagName)
			},
			expectedStatus: http.StatusUnprocessableEntity,
		},
		"get node by UUID": {
			method: http.MethodGet,
			path:   getNodeByUUIDPath,
			setupMock: func(ctx context.Context, service *mocks.MockForManagingClusters) {
				nodeID := uuids.NodeID(clusterName, controlPlaneNodeName)
				service.EXPECT().GetNodeByUUID(ctx, nodeID).Return(strategies.ClusterNodeInfo{
					ID: nodeID, Name: controlPlaneNodeName, Role: controlPlaneNodeRole,
					Status: controlPlaneNodeStatus, Endpoint: controlPlaneNodeEndpoint,
					ParentCluster: clusterUUID,
				}, nil)
			},
			expectedStatus: http.StatusOK,
			expectedJSON: `{"id":"` + uuids.NodeID(clusterName, controlPlaneNodeName).String() + `","is_reconciliation_paused":false,"name":"control-plane-0",` +
				`"role":"control-plane","status":"Ready","endpoint":"10.0.0.11", "$cluster":"` + clusterUUID.String() + `"}`,
		},
		"get missing node by UUID": {
			method: http.MethodGet,
			path:   getNodeByUUIDPath,
			setupMock: func(ctx context.Context, service *mocks.MockForManagingClusters) {
				service.EXPECT().GetNodeByUUID(ctx, uuids.NodeID(clusterName, controlPlaneNodeName)).Return(strategies.ClusterNodeInfo{}, strategies.ErrNodeNotFound)
			},
			expectedStatus: http.StatusNotFound,
			expectedTitle:  "Node not found",
			expectedDetail: strategies.ErrNodeNotFound.Error(),
		},
		"get node with invalid UUID": {
			method:         http.MethodGet,
			path:           "/kubernetes/nodes/not-a-uuid",
			expectedStatus: http.StatusBadRequest,
			expectedTitle:  "Invalid UUID",
			expectedDetail: "path parameter 'id' must be a valid UUID",
		},
		"unknown route": {
			method:         http.MethodGet,
			path:           unknownPath,
			expectedStatus: http.StatusNotFound,
		},
		"method not allowed": {
			method:         http.MethodPatch,
			path:           getClusterByUUIDPath,
			expectedStatus: http.StatusMethodNotAllowed,
		},
	}

	for name, testCase := range testCases {
		t.Run(name, func(t *testing.T) {
			executeHTTPTestCase(t, testCase)
		})
	}
}

func TestCreateClusterHTTPValidation(t *testing.T) {
	testCases := map[string]httpTestCase{
		"malformed JSON":                      {body: malformedRequestBody, expectedStatus: http.StatusBadRequest},
		"wrong type":                          {body: `{"control_plane_replicas":"one"}`, expectedStatus: http.StatusBadRequest},
		"unknown field":                       {body: `{"unknown":true}`, expectedStatus: http.StatusBadRequest},
		"empty body":                          {body: ""},
		"negative control plane replicas":     {body: strings.Replace(createClusterRequestBody, `"control_plane_replicas":1`, `"control_plane_replicas":-1`, 1)},
		"negative worker replicas":            {body: strings.Replace(createClusterRequestBody, `"worker_replicas":1`, `"worker_replicas":-1`, 1)},
		"missing version":                     {body: strings.Replace(createClusterRequestBody, `"kubernetes_version":"1.36.9",`, "", 1)},
		"empty version":                       {body: strings.Replace(createClusterRequestBody, `"kubernetes_version":"1.36.9"`, `"kubernetes_version":""`, 1)},
		"missing name":                        {body: missingNameRequestBody},
		"invalid control plane IP":            {body: invalidControlPlaneIPRequestBody},
		"invalid control plane replicas":      {body: invalidControlPlaneReplicasRequestBody},
		"invalid worker replicas":             {body: invalidWorkerReplicasRequestBody},
		"missing virtual machine name prefix": {body: missingVMNamePrefixRequestBody},
	}

	for name, testCase := range testCases {
		t.Run(name, func(t *testing.T) {
			testCase.method = http.MethodPost
			testCase.path = clustersPath
			if testCase.expectedStatus == 0 {
				testCase.expectedStatus = http.StatusUnprocessableEntity
				testCase.expectedTitle = "Validation Error"
				testCase.expectedFields = 1
				if name == "empty body" {
					testCase.expectedFields = 6
				}
			}
			executeHTTPTestCase(t, testCase)
		})
	}
}

func TestClusterTagValueHTTPValidation(t *testing.T) {
	executeHTTPTestCase(t, httpTestCase{
		method:         http.MethodPut,
		path:           clusterTagPath,
		body:           `{"value":"` + strings.Repeat("a", 64) + `"}`,
		expectedTitle:  "Validation Error",
		expectedFields: 1,
		expectedStatus: http.StatusUnprocessableEntity,
	})
}

func executeHTTPTestCase(t *testing.T, testCase httpTestCase) {
	t.Helper()

	ctrl := gomock.NewController(t)
	clusterService := mocks.NewMockForManagingClusters(ctrl)
	if testCase.setupMock != nil {
		testCase.setupMock(t.Context(), clusterService)
	}

	handler := newTestHandler(clusterService)
	request := httptest.NewRequestWithContext(t.Context(), testCase.method, testCase.path, strings.NewReader(testCase.body))
	request.Header.Set(acceptHeader, "application/json, application/xml")
	if testCase.body != "" {
		request.Header.Set(contentTypeHeader, applicationJSON)
	}
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	require.Equal(t, testCase.expectedStatus, response.Code, response.Body.String())
	assert.Empty(t, response.Header().Get("Location"), "API routes must respond without redirecting")
	if testCase.expectedStatus == http.StatusNoContent {
		assert.Empty(t, response.Body.String())
	}
	if testCase.expectedJSON != "" {
		assert.JSONEq(t, testCase.expectedJSON, response.Body.String())
	} else if testCase.expectedStatus >= http.StatusBadRequest &&
		response.Header().Get(contentTypeHeader) == applicationJSON {
		assertJSONErrorStatus(t, response, testCase.expectedStatus)
	}
	if testCase.expectedTitle != "" || testCase.expectedDetail != "" {
		if testCase.expectedFields == 0 {
			assertJSONError(t, response, testCase.expectedTitle, testCase.expectedDetail)
		} else {
			var problem struct {
				Title  string `json:"title"`
				Detail string `json:"detail"`
				Errors []struct {
					Name   string `json:"name"`
					Reason string `json:"reason"`
				} `json:"errors"`
			}
			require.NoError(t, json.Unmarshal(response.Body.Bytes(), &problem))
			assert.Equal(t, testCase.expectedTitle, problem.Title)
			assert.NotEmpty(t, problem.Detail)
			require.Len(t, problem.Errors, testCase.expectedFields)
			for _, field := range problem.Errors {
				assert.NotEmpty(t, field.Name)
				assert.NotEmpty(t, field.Reason)
			}
		}
	}
}

func assertJSONErrorStatus(t *testing.T, response *httptest.ResponseRecorder, expectedStatus int) {
	t.Helper()

	var body struct {
		Status int `json:"status"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &body), response.Body.String())
	assert.Equal(t, expectedStatus, body.Status, response.Body.String())
}

func assertJSONError(t *testing.T, response *httptest.ResponseRecorder, expectedTitle, expectedDetail string) {
	t.Helper()

	var body struct {
		Title  string `json:"title"`
		Detail string `json:"detail"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &body), response.Body.String())
	assert.Equal(t, expectedTitle, body.Title)
	assert.Equal(t, expectedDetail, body.Detail)
}

func validCreateClusterParams() strategies.CreateClusterParams {
	return strategies.CreateClusterParams{
		Version:              "1.36.9",
		Name:                 clusterName,
		ControlPlaneReplicas: controlPlaneReplicas,
		WorkerReplicas:       workerReplicas,
		ControlPlaneIP:       controlPlaneIP,
		VMNamePrefix:         vmNamePrefix,
	}
}

func newTestHandler(
	clusterService strategies.ForManagingClusters,
) http.Handler {
	return HttpServer{
		SwaggerUI: SwaggerUIDisabled,
	}.Handler(clusterService, versionmanager.NewVersionManager())
}

func TestClusterTagBodyHTTPDecoding(t *testing.T) {
	for name, body := range map[string]string{
		"malformed JSON": `{"value":`,
		"wrong type":     `{"value":123}`,
		"unknown field":  `{"unknown":true}`,
	} {
		t.Run(name, func(t *testing.T) {
			executeHTTPTestCase(t, httpTestCase{method: http.MethodPut, path: clusterTagPath, body: body, expectedStatus: http.StatusBadRequest})
		})
	}
}

func TestGetAllNodesHTTP(t *testing.T) {
	testCases := map[string]httpTestCase{
		"list all nodes": {
			setupMock: func(ctx context.Context, service *mocks.MockForManagingClusters) {
				service.EXPECT().GetAllNodes(ctx).Return([]strategies.ClusterNodeInfo{
					{Name: controlPlaneNodeName, Role: controlPlaneNodeRole, Status: controlPlaneNodeStatus, Endpoint: controlPlaneNodeEndpoint, ParentCluster: clusterUUID, Version: k8sVersion},
					{Name: "worker-0", Role: "worker", Status: "NotReady", ParentCluster: uuid.FromStringOrNil("10000000-0000-0000-0000-000000000001")},
				}, nil)
			},
			expectedStatus: http.StatusOK,
			expectedJSON: `[
				{"id":"00000000-0000-0000-0000-000000000000","is_reconciliation_paused":false,"name":"control-plane-0","role":"control-plane","status":"Ready","endpoint":"10.0.0.11",` +
				`"$cluster":"` + clusterUUID.String() + `","version":"v1.36.0"},
				{"id":"00000000-0000-0000-0000-000000000000","is_reconciliation_paused":false,"name":"worker-0","role":"worker","status":"NotReady","$cluster":"10000000-0000-0000-0000-000000000001"}
			]`,
		},
		"empty list": {
			setupMock: func(ctx context.Context, service *mocks.MockForManagingClusters) {
				service.EXPECT().GetAllNodes(ctx).Return([]strategies.ClusterNodeInfo{}, nil)
			},
			expectedStatus: http.StatusOK,
			expectedJSON:   "[]",
		},
		"nil list": {
			setupMock: func(ctx context.Context, service *mocks.MockForManagingClusters) {
				service.EXPECT().GetAllNodes(ctx).Return(nil, nil)
			},
			expectedStatus: http.StatusOK,
			expectedJSON:   "[]",
		},
		"retrieval error": {
			setupMock: func(ctx context.Context, service *mocks.MockForManagingClusters) {
				service.EXPECT().GetAllNodes(ctx).Return(nil,
					fmt.Errorf("%w: %w", strategies.ErrClusterNodesRetrievalFailed, errTest))
			},
			expectedStatus: http.StatusInternalServerError,
			expectedTitle:  "Internal Server Error",
		},
		"method not allowed": {method: http.MethodPost, expectedStatus: http.StatusMethodNotAllowed},
	}
	for name, tc := range testCases {
		t.Run(name, func(t *testing.T) {
			tc.path = allNodesPath
			if tc.method == "" {
				tc.method = http.MethodGet
			}
			executeHTTPTestCase(t, tc)
		})
	}
}

func TestDeleteClusterNodeHTTP(t *testing.T) {
	for name, tc := range map[string]struct {
		err    error
		status int
		title  string
	}{
		"success":            {status: http.StatusNoContent},
		"missing cluster":    {err: strategies.ErrClusterNotFound, status: http.StatusNotFound, title: clusterNotFoundTitle},
		"missing node":       {err: strategies.ErrNodeNotFound, status: http.StatusNotFound, title: "Node not found"},
		"concurrent update":  {err: strategies.ErrNodeDeletionConflict, status: http.StatusConflict, title: "Node deletion conflict"},
		"invalid node":       {err: strategies.ErrNodeNameRequired, status: http.StatusUnprocessableEntity, title: "Invalid cluster parameters"},
		"Kubernetes failure": {err: errTest, status: http.StatusInternalServerError, title: "Internal Server Error"},
	} {
		t.Run(name, func(t *testing.T) {
			executeHTTPTestCase(t, httpTestCase{
				method: http.MethodDelete, path: getClusterByUUIDPath + "/nodes/" + node1UUID.String(),
				setupMock: func(ctx context.Context, service *mocks.MockForManagingClusters) {
					var err error
					if tc.err != nil {
						err = fmt.Errorf("%w: %w", strategies.ErrNodeDeletionFailed, tc.err)
					}
					service.EXPECT().DeleteClusterNode(ctx, clusterUUID, node1UUID).Return(err)
				},
				expectedStatus: tc.status, expectedTitle: tc.title,
				expectedDetail: func() string {
					if tc.err == strategies.ErrNodeNotFound || tc.err == strategies.ErrClusterNotFound || tc.err == strategies.ErrNodeDeletionConflict {
						return tc.err.Error()
					}
					if tc.err == strategies.ErrNodeNameRequired {
						return "failed to delete node: node name is required"
					}
					return ""
				}(),
			})
		})
	}
}

func TestNodeHTTPAbstraction(t *testing.T) {
	nodeID := uuid.Must(uuid.FromString("01234567-89ab-cdef-0123-456789abcdef"))
	for _, path := range []string{clusterNodesPath, allNodesPath} {
		t.Run(path, func(t *testing.T) {
			executeHTTPTestCase(t, httpTestCase{
				method: http.MethodGet, path: path,
				setupMock: func(ctx context.Context, service *mocks.MockForManagingClusters) {
					nodes := []strategies.ClusterNodeInfo{{Name: "node-1", NodeName: "worker-01", MachineID: &nodeID}}
					if path == clusterNodesPath {
						service.EXPECT().GetClusterNodesInfo(ctx, clusterUUID).Return(nodes, nil)
					} else {
						service.EXPECT().GetAllNodes(ctx).Return(nodes, nil)
					}
				},
				expectedStatus: http.StatusOK,
				expectedJSON: `[{"id":"00000000-0000-0000-0000-000000000000","is_reconciliation_paused":false,"name":"node-1",` +
					`"node_name":"worker-01","machine_id":"01234567-89ab-cdef-0123-456789abcdef", "$cluster":"00000000-0000-0000-0000-000000000000"}]`,
			})
		})
	}
}

func TestNodeDeletionOpenAPI(t *testing.T) {
	service := mocks.NewMockForManagingClusters(gomock.NewController(t))
	document := (HttpServer{SwaggerUI: SwaggerUIDisabled}).newServer(service, versionmanager.NewVersionManager()).OpenAPI.Description()
	route := document.Paths.Find("/kubernetes/clusters/{id}/nodes/{nodeID}")
	require.NotNil(t, route)
	require.NotNil(t, route.Delete)
	for _, status := range []int{http.StatusNoContent, http.StatusNotFound, http.StatusConflict, http.StatusUnprocessableEntity} {
		require.NotNil(t, route.Delete.Responses.Status(status))
	}
	assert.Empty(t, route.Delete.Responses.Status(http.StatusNoContent).Value.Content)
	assert.Nil(t, route.Delete.RequestBody)
	for _, parameter := range []string{"id", "nodeID"} {
		ref := route.Delete.Parameters.GetByInAndName("path", parameter)
		require.NotNil(t, ref)
		assert.True(t, ref.Required)
	}
}

func TestCreateClusterHTTPRejectsInvalidParameters(t *testing.T) {
	const versionField = "kubernetes_version"
	testCases := []struct {
		field  string
		value  string
		detail string
	}{
		{versionField, "1.36.99", strategies.ErrClusterVersionUnavailable.Error()},
		{versionField, "1.36", strategies.ErrClusterVersionUnavailable.Error()},
		{versionField, "v1.36.9", strategies.ErrClusterVersionUnavailable.Error()},
		{"name", "NHT-Kub-Prod", "lowercase RFC 1123 label"},
	}
	for _, tc := range testCases {
		t.Run(tc.field+"/"+tc.value, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			versions := versionmanager.NewVersionManager()
			manager := cluster_manager.NewClusterManager(
				mocks.NewMockForManagingClusterResources(ctrl), nil, nil,
				mocks.NewMockForGettingClusterCreationDefaults(ctrl), versions,
				nil, nil,
			)
			handler := HttpServer{SwaggerUI: SwaggerUIDisabled}.Handler(manager, versions)
			var payload map[string]any
			require.NoError(t, json.Unmarshal([]byte(createClusterRequestBody), &payload))
			payload[tc.field] = tc.value
			body, err := json.Marshal(payload)
			require.NoError(t, err)
			request := httptest.NewRequest(http.MethodPost, clustersPath, strings.NewReader(string(body)))
			request.Header.Set(contentTypeHeader, applicationJSON)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			require.Equal(t, http.StatusUnprocessableEntity, response.Code, response.Body.String())
			var problem struct {
				Detail string `json:"detail"`
			}
			require.NoError(t, json.Unmarshal(response.Body.Bytes(), &problem))
			assert.Contains(t, problem.Detail, tc.value)
			assert.Contains(t, problem.Detail, tc.detail)
		})
	}
}

func TestScalingHTTP(t *testing.T) {
	for _, target := range []string{scalingWorkerTarget, controlPlaneNodeRole} {
		for _, tc := range []struct {
			name   string
			err    error
			status int
		}{
			{"success", nil, http.StatusNoContent},
			{"not found", fmt.Errorf("wrapped: %w", strategies.ErrClusterNotFound), http.StatusNotFound},
			{"conflict", strategies.ErrClusterScalingConflict, http.StatusConflict},
			{"invalid", strategies.ErrInvalidReplicas, http.StatusUnprocessableEntity},
			{"etcd parity", strategies.ErrInvalidControlPlaneReplicas, http.StatusUnprocessableEntity},
			{"admission rejected", fmt.Errorf("%w: %w", strategies.ErrReplicasRejected, errTest), http.StatusUnprocessableEntity},
			{"technical error", errTest, http.StatusInternalServerError},
		} {
			t.Run(target+"/"+tc.name, func(t *testing.T) {
				executeHTTPTestCase(t, httpTestCase{
					method: http.MethodPut, path: clustersPath + "/" + clusterUUID.String() + "/" + target + "/replicas", body: `{"replicas":3}`, expectedStatus: tc.status,
					setupMock: func(ctx context.Context, service *mocks.MockForManagingClusters) {
						if target == "workers" {
							service.EXPECT().SetWorkerReplicas(ctx, clusterUUID, int32(3)).Return(tc.err)
						} else {
							service.EXPECT().SetControlPlaneReplicas(ctx, clusterUUID, int32(3)).Return(tc.err)
						}
					},
				})
			})
		}
	}
}

func TestPauseHTTP(t *testing.T) {
	for _, node := range []bool{false, true} {
		for _, paused := range []bool{false, true} {
			for _, tc := range []struct {
				err    error
				status int
			}{
				{nil, http.StatusNoContent},
				{strategies.ErrClusterNotFound, http.StatusNotFound},
				{strategies.ErrNodeNotFound, http.StatusNotFound},
				{strategies.ErrPauseUpdateConflict, http.StatusConflict},
				{strategies.ErrNodeNameRequired, http.StatusUnprocessableEntity},
				{errTest, http.StatusInternalServerError},
			} {
				path, method := getClusterByUUIDPath+"/reconciliation/resume", http.MethodPost
				if node {
					path = getClusterByUUIDPath + "/nodes/" + node1UUID.String() + "/reconciliation/resume"
				}
				if paused {
					path = strings.TrimSuffix(path, "/reconciliation/resume") + "/reconciliation/pause"
					method = http.MethodPost
				}
				t.Run(method+path+strconv.Itoa(tc.status), func(t *testing.T) {
					executeHTTPTestCase(t, httpTestCase{
						method: method, path: path, expectedStatus: tc.status,
						setupMock: func(ctx context.Context, service *mocks.MockForManagingClusters) {
							if node {
								service.EXPECT().SetClusterNodePaused(ctx, clusterUUID, node1UUID, paused).Return(tc.err)
							} else {
								service.EXPECT().SetClusterPaused(ctx, clusterUUID, paused).Return(tc.err)
							}
						},
					})
				})
			}
		}
	}
}

func TestScalingHTTPValidation(t *testing.T) {
	for _, target := range []string{scalingWorkerTarget, controlPlaneNodeRole} {
		for _, tc := range []struct {
			body   string
			status int
		}{
			{`{}`, 422},
			{`{"replicas":null}`, 422},
			{`{"replicas":0}`, 422},
			{`{"replicas":-1}`, 422},
			{``, 422},
			{`{`, 400},
			{`{"replicas":"3"}`, 400},
			{`{"replicas":1.5}`, 400},
			{`{"replicas":2147483648}`, 400},
			{`{"replicas":3,"unknown":true}`, 400},
		} {
			t.Run(target+"/"+tc.body, func(t *testing.T) {
				executeHTTPTestCase(t, httpTestCase{method: http.MethodPut, path: clustersPath + "/demo/" + target + "/replicas", body: tc.body, expectedStatus: tc.status})
			})
		}
	}
}

func TestScalingOpenAPI(t *testing.T) {
	service := mocks.NewMockForManagingClusters(gomock.NewController(t))
	document := (HttpServer{SwaggerUI: SwaggerUIDisabled}).newServer(service, versionmanager.NewVersionManager()).OpenAPI.Description()
	for _, target := range []string{scalingWorkerTarget, controlPlaneNodeRole} {
		route := document.Paths.Find("/kubernetes/clusters/{id}/" + target + "/replicas")
		require.NotNil(t, route)
		require.NotNil(t, route.Put)
		require.Nil(t, route.Post)
		require.NotNil(t, route.Put.RequestBody)
		for _, status := range []int{204, 400, 404, 409, 422} {
			require.NotNil(t, route.Put.Responses.Status(status))
		}
		assert.Empty(t, route.Put.Responses.Status(204).Value.Content)
		parameter := route.Put.Parameters.GetByInAndName("path", "id")
		require.NotNil(t, parameter)
		assert.True(t, parameter.Required)
		requestBody := route.Put.RequestBody.Value
		require.NotNil(t, requestBody)
		media := requestBody.Content.Get("application/json")
		require.NotNil(t, media, "content: %v", requestBody.Content)
		schema := media.Schema
		body := schema.Value
		if body == nil {
			body = document.Components.Schemas[strings.TrimPrefix(schema.Ref, "#/components/schemas/")].Value
		}
		require.NotNil(t, body)
		assert.Contains(t, body.Required, "replicas")
	}
}

func TestPauseOpenAPI(t *testing.T) {
	service := mocks.NewMockForManagingClusters(gomock.NewController(t))
	document := (HttpServer{SwaggerUI: SwaggerUIDisabled}).newServer(service, versionmanager.NewVersionManager()).OpenAPI.Description()
	for _, base := range []string{"/kubernetes/clusters/{id}", "/kubernetes/clusters/{id}/nodes/{nodeName}"} {
		for _, endpoint := range []struct {
			method string
			suffix string
		}{
			{http.MethodPost, "/reconciliation/pause"},
			{http.MethodPost, "/reconciliation/resume"},
		} {
			route := document.Paths.Find(base + endpoint.suffix)
			require.NotNil(t, route)
			operation := route.GetOperation(endpoint.method)
			require.NotNil(t, operation)
			require.Nil(t, operation.RequestBody)
			require.Contains(t, operation.Description, "Cluster API controllers")
			require.Contains(t, operation.Description, "does not suspend VMs or running workloads")
			require.Nil(t, route.Delete)
			require.Nil(t, route.Put)
			for _, status := range []int{204, 404, 409, 422} {
				require.NotNil(t, operation.Responses.Status(status))
			}
			require.Empty(t, operation.Responses.Status(http.StatusNoContent).Value.Content)
		}
	}
}

func TestReconciliationPausedFieldSelection(t *testing.T) {
	for _, paused := range []bool{false, true} {
		value := strconv.FormatBool(paused)
		executeHTTPTestCase(t, httpTestCase{
			method: http.MethodGet, path: clustersPath + "?fields=is_reconciliation_paused", expectedStatus: http.StatusOK,
			expectedJSON: `[{"is_reconciliation_paused":` + value + `}]`,
			setupMock: func(ctx context.Context, service *mocks.MockForManagingClusters) {
				service.EXPECT().GetClusters(ctx).Return([]strategies.ClusterInfo{{Name: clusterName, Paused: paused}}, nil)
			},
		})
		executeHTTPTestCase(t, httpTestCase{
			method: http.MethodGet, path: clusterNodesPath + "?fields=is_reconciliation_paused", expectedStatus: http.StatusOK,
			expectedJSON: `[{"is_reconciliation_paused":` + value + `}]`,
			setupMock: func(ctx context.Context, service *mocks.MockForManagingClusters) {
				service.EXPECT().GetClusterNodesInfo(ctx, clusterUUID).Return([]strategies.ClusterNodeInfo{{Name: "node-1", Paused: paused}}, nil)
			},
		})
	}
}

func TestUpdateClusterDescriptionHTTP(t *testing.T) {
	const emptyDescriptionBody = `{"description":""}`
	for _, tc := range []struct {
		name, body, description string
		status                  int
		call                    bool
		err                     error
	}{
		{name: "edit", body: `{"description":"Équipe 🚀\nBackend"}`, description: "Équipe 🚀\nBackend", status: 204, call: true},
		{name: "clear", body: emptyDescriptionBody, status: 204, call: true},
		{
			name: "boundary", body: `{"description":"` + strings.Repeat("🚀", strategies.MaxClusterDescriptionLength) + `"}`,
			description: strings.Repeat("🚀", strategies.MaxClusterDescriptionLength), status: 204, call: true,
		},
		{name: "too long", body: `{"description":"` + strings.Repeat("é", strategies.MaxClusterDescriptionLength+1) + `"}`, status: 422},
		{name: "missing", body: `{}`, status: 422},
		{name: "null", body: `{"description":null}`, status: 422},
		{name: "numeric description", body: `{"description":123}`, status: 400},
		{name: "malformed", body: `{"description":`, status: 400},
		{name: "unexpected property", body: `{"description":"", "other":true}`, status: 400},
		{name: "not found", body: emptyDescriptionBody, status: 404, call: true, err: strategies.ErrClusterNotFound},
		{name: "conflict", body: emptyDescriptionBody, status: 409, call: true, err: strategies.ErrClusterDescriptionUpdateConflict},
		{name: "failure", body: emptyDescriptionBody, status: 500, call: true, err: errTest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			executeHTTPTestCase(t, httpTestCase{
				method: http.MethodPut, path: getClusterByUUIDPath + "/description", body: tc.body, expectedStatus: tc.status,
				setupMock: func(ctx context.Context, service *mocks.MockForManagingClusters) {
					if tc.call {
						service.EXPECT().SetClusterDescription(ctx, clusterUUID, tc.description).Return(tc.err)
					}
				},
			})
		})
	}
}

func TestGetAllNodesHTTPFields(t *testing.T) {
	for name, tc := range map[string]struct {
		query        string
		expectedJSON string
	}{
		"selected fields":           {query: "?fields=name,version", expectedJSON: `[{"name":"control-plane-0","version":"v1.36.0"}]`},
		"paused false is preserved": {query: "?fields=is_reconciliation_paused", expectedJSON: `[{"is_reconciliation_paused":false}]`},
		"unknown field is ignored":  {query: "?fields=name,unknown", expectedJSON: `[{"name":"control-plane-0"}]`},
		"only unknown fields":       {query: "?fields=unknown", expectedJSON: `[{}]`},
		"wildcard": {query: "?fields=*", expectedJSON: `[{"id":"00000000-0000-0000-0000-000000000000","$cluster":"00000000-0000-0000-0000-000000000000",` +
			`"is_reconciliation_paused":false,"name":"control-plane-0","version":"v1.36.0"}]`},
		"empty fields": {query: "?fields=", expectedJSON: `[{"id":"00000000-0000-0000-0000-000000000000","$cluster":"00000000-0000-0000-0000-000000000000",` +
			`"is_reconciliation_paused":false,"name":"control-plane-0","version":"v1.36.0"}]`},
	} {
		t.Run(name, func(t *testing.T) {
			executeHTTPTestCase(t, httpTestCase{
				method: http.MethodGet, path: allNodesPath + tc.query,
				setupMock: func(ctx context.Context, service *mocks.MockForManagingClusters) {
					service.EXPECT().GetAllNodes(ctx).Return([]strategies.ClusterNodeInfo{{Name: controlPlaneNodeName, Version: k8sVersion}}, nil)
				},
				expectedStatus: http.StatusOK, expectedJSON: tc.expectedJSON,
			})
		})
	}
}
