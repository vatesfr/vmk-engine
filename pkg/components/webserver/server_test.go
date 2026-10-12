package webserver_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"capv-proxy/pkg/components/webserver"
	"capv-proxy/pkg/strategies"
	"capv-proxy/pkg/strategies/mocks"
)

const (
	k8sVersion                     = "v1.36.0"
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
	getClusterPath                 = "/kubernetes/clusters/cluster-1"
	allNodesPath                   = "/kubernetes/nodes"
	allTagsPath                    = "/kubernetes/tags"
	clusterNodesPath               = "/kubernetes/clusters/cluster-1/nodes"
	clusterConfigPath              = "/kubernetes/clusters/cluster-1/config"
	clusterTagsPath                = "/kubernetes/clusters/cluster-1/tags"
	clusterTagPath                 = "/kubernetes/clusters/cluster-1/tags/environment"
	contentTypeHeader              = "Content-Type"
	acceptHeader                   = "Accept"
	applicationJSON                = "application/json"
	unknownPath                    = "/unknown"
	createClusterRequestBody       = `{
		"name":"cluster-1",
		"control_plane_replicas":1,
		"worker_replicas":1,
		"control_plane_IP":"10.0.0.10",
		"vm_name_prefix":"test"
	}`
	malformedRequestBody   = `{"name":`
	missingNameRequestBody = `{
		"control_plane_replicas":1,
		"worker_replicas":1,
		"control_plane_IP":"10.0.0.10",
		"vm_name_prefix":"test"
	}`
	invalidControlPlaneIPRequestBody = `{
		"name":"cluster-1",
		"control_plane_replicas":1,
		"worker_replicas":1,
		"control_plane_IP":"invalid",
		"vm_name_prefix":"test"
	}`
	invalidControlPlaneReplicasRequestBody = `{
		"name":"cluster-1",
		"control_plane_replicas":0,
		"worker_replicas":1,
		"control_plane_IP":"10.0.0.10",
		"vm_name_prefix":"test"
	}`
	invalidWorkerReplicasRequestBody = `{
		"name":"cluster-1",
		"control_plane_replicas":1,
		"worker_replicas":0,
		"control_plane_IP":"10.0.0.10",
		"vm_name_prefix":"test"
	}`
	missingVMNamePrefixRequestBody = `{
		"name":"cluster-1",
		"control_plane_replicas":1,
		"worker_replicas":1,
		"control_plane_IP":"10.0.0.10"
	}`
)

var errTest = errors.New("test error")

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
					{Name: clusterName, Phase: clusterPhase, ControlPlaneEndpoint: controlPlaneEndpoint},
					{Name: secondClusterName, Phase: secondClusterPhase},
				}, nil)
			},
			expectedStatus: http.StatusOK,
			expectedJSON: `[
				{"name":"cluster-1","phase":"Provisioned","control_plane_endpoint":"10.0.0.10",
                "control_plane_status":{"available_replicas":0,"desired_replicas":0,"ready_replicas":0,"replicas":0,"up_to_date_replicas":0},
                "worker_status":{"available_replicas":0,"desired_replicas":0,"ready_replicas":0,"replicas":0,"up_to_date_replicas":0}},
				{"name":"cluster-2","phase":"Provisioning",
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
					{Name: clusterName, Phase: clusterPhase, ControlPlaneEndpoint: controlPlaneEndpoint},
					{Name: secondClusterName, Phase: secondClusterPhase},
				}, nil)
			},
			expectedStatus: http.StatusOK,
			expectedJSON: `[
				{"name":"cluster-1","phase":"Provisioned","control_plane_endpoint":"10.0.0.10",
                "control_plane_status":{"available_replicas":0,"desired_replicas":0,"ready_replicas":0,"replicas":0,"up_to_date_replicas":0},
                "worker_status":{"available_replicas":0,"desired_replicas":0,"ready_replicas":0,"replicas":0,"up_to_date_replicas":0}},
				{"name":"cluster-2","phase":"Provisioning",
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
		"get cluster": {
			method: http.MethodGet,
			path:   getClusterPath,
			setupMock: func(ctx context.Context, service *mocks.MockForManagingClusters) {
				service.EXPECT().GetCluster(ctx, clusterName).Return(strategies.ClusterInfo{
					Name: clusterName, Phase: clusterPhase, ControlPlaneEndpoint: controlPlaneEndpoint,
				}, nil)
			},
			expectedStatus: http.StatusOK,
			expectedJSON: `{"name":"cluster-1","phase":"Provisioned","control_plane_endpoint":"10.0.0.10",
                "control_plane_status":{"available_replicas":0,"desired_replicas":0,"ready_replicas":0,"replicas":0,"up_to_date_replicas":0},
                "worker_status":{"available_replicas":0,"desired_replicas":0,"ready_replicas":0,"replicas":0,"up_to_date_replicas":0}}`,
		},
		"get missing cluster": {
			method: http.MethodGet,
			path:   getClusterPath,
			setupMock: func(ctx context.Context, service *mocks.MockForManagingClusters) {
				service.EXPECT().GetCluster(ctx, clusterName).Return(strategies.ClusterInfo{}, strategies.ErrClusterNotFound)
			},
			expectedStatus: http.StatusNotFound,
			expectedTitle:  "Cluster not found",
			expectedDetail: strategies.ErrClusterNotFound.Error(),
		},
		"get cluster with version status": {
			method: http.MethodGet,
			path:   getClusterPath,
			setupMock: func(ctx context.Context, service *mocks.MockForManagingClusters) {
				service.EXPECT().GetCluster(ctx, clusterName).Return(strategies.ClusterInfo{
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
			expectedJSON: `{"name":"cluster-1",
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
				service.EXPECT().GetClusterNodesInfo(ctx, clusterName).Return([]strategies.ClusterNodeInfo{{
					Name:     controlPlaneNodeName,
					Role:     controlPlaneNodeRole,
					Status:   controlPlaneNodeStatus,
					Endpoint: controlPlaneNodeEndpoint,
				}}, nil)
			},
			expectedStatus: http.StatusOK,
			expectedJSON: `[{"name":"control-plane-0","role":"control-plane",` +
				`"status":"Ready","endpoint":"10.0.0.11"}]`,
		},
		"get cluster nodes with all fields": {
			method: http.MethodGet,
			path:   clusterNodesPath + "?fields=*",
			setupMock: func(ctx context.Context, service *mocks.MockForManagingClusters) {
				service.EXPECT().GetClusterNodesInfo(ctx, clusterName).Return([]strategies.ClusterNodeInfo{{
					Name: controlPlaneNodeName, Role: controlPlaneNodeRole,
					Status: controlPlaneNodeStatus, Endpoint: controlPlaneNodeEndpoint,
				}}, nil)
			},
			expectedStatus: http.StatusOK,
			expectedJSON:   `[{"name":"control-plane-0","role":"control-plane","status":"Ready","endpoint":"10.0.0.11"}]`,
		},
		"get cluster nodes with selected fields": {
			method: http.MethodGet,
			path:   clusterNodesPath + "?fields=name,status",
			setupMock: func(ctx context.Context, service *mocks.MockForManagingClusters) {
				service.EXPECT().GetClusterNodesInfo(ctx, clusterName).Return([]strategies.ClusterNodeInfo{{
					Name: controlPlaneNodeName, Role: controlPlaneNodeRole,
					Status: controlPlaneNodeStatus, Endpoint: controlPlaneNodeEndpoint,
				}}, nil)
			},
			expectedStatus: http.StatusOK,
			expectedJSON:   `[{"name":"control-plane-0","status":"Ready"}]`,
		},
		"get cluster config": {
			method: http.MethodGet,
			path:   clusterConfigPath,
			setupMock: func(ctx context.Context, service *mocks.MockForManagingClusters) {
				service.EXPECT().GetClusterConfig(ctx, clusterName).Return(clusterConfig, nil)
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
			expectedJSON: `{"name":"cluster-1","phase":"Provisioned","control_plane_endpoint":"10.0.0.10",
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
			expectedJSON: `{"name":"cluster-1","tags":{"environment":"production"},
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
			path:   getClusterPath,
			setupMock: func(ctx context.Context, service *mocks.MockForManagingClusters) {
				service.EXPECT().DeleteCluster(ctx, clusterName).Return(nil)
			},
			expectedStatus: http.StatusNoContent,
		},
		"delete missing cluster": {
			method: http.MethodDelete,
			path:   getClusterPath,
			setupMock: func(ctx context.Context, service *mocks.MockForManagingClusters) {
				service.EXPECT().DeleteCluster(ctx, clusterName).Return(strategies.ErrClusterNotFound)
			},
			expectedStatus: http.StatusNotFound,
			expectedTitle:  "Cluster not found",
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
				service.EXPECT().GetClusterTags(ctx, clusterName).Return(map[string]string{environmentTag: productionValue}, nil)
			},
			expectedStatus: http.StatusOK,
			expectedJSON:   `{"result":{"environment":"production"}}`,
		},
		"get cluster tag": {
			method: http.MethodGet,
			path:   clusterTagPath,
			setupMock: func(ctx context.Context, service *mocks.MockForManagingClusters) {
				service.EXPECT().GetClusterTag(ctx, clusterName, environmentTag).Return(productionValue, nil)
			},
			expectedStatus: http.StatusOK,
			expectedJSON:   `"production"`,
		},
		"set cluster tag": {
			method: http.MethodPut,
			path:   clusterTagPath,
			body:   `{"value":"production"}`,
			setupMock: func(ctx context.Context, service *mocks.MockForManagingClusters) {
				service.EXPECT().SetClusterTag(ctx, clusterName, environmentTag, productionValue).Return(nil)
			},
			expectedStatus: http.StatusNoContent,
		},
		"delete cluster tag": {
			method: http.MethodDelete,
			path:   clusterTagPath,
			setupMock: func(ctx context.Context, service *mocks.MockForManagingClusters) {
				service.EXPECT().DeleteClusterTag(ctx, clusterName, environmentTag).Return(nil)
			},
			expectedStatus: http.StatusNoContent,
		},
		"missing cluster tag": {
			method: http.MethodGet,
			path:   clusterTagPath,
			setupMock: func(ctx context.Context, service *mocks.MockForManagingClusters) {
				service.EXPECT().GetClusterTag(ctx, clusterName, environmentTag).Return("", strategies.ErrClusterTagNotFound)
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
				service.EXPECT().SetClusterTag(ctx, clusterName, environmentTag, "invalid value").Return(fmt.Errorf("update: %w", strategies.ErrInvalidClusterTagValue))
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
				service.EXPECT().GetClusterTag(ctx, clusterName, environmentTag).Return("", strategies.ErrInvalidClusterTagName)
			},
			expectedStatus: http.StatusUnprocessableEntity,
		},
		"invalid tag on deletion": {
			method: http.MethodDelete,
			path:   clusterTagPath,
			setupMock: func(ctx context.Context, service *mocks.MockForManagingClusters) {
				service.EXPECT().DeleteClusterTag(ctx, clusterName, environmentTag).Return(strategies.ErrInvalidClusterTagName)
			},
			expectedStatus: http.StatusUnprocessableEntity,
		},
		"invalid cluster tag": {
			method: http.MethodPut,
			path:   clusterTagPath,
			body:   `{"value":"production"}`,
			setupMock: func(ctx context.Context, service *mocks.MockForManagingClusters) {
				service.EXPECT().SetClusterTag(ctx, clusterName, environmentTag, productionValue).Return(strategies.ErrInvalidClusterTagName)
			},
			expectedStatus: http.StatusUnprocessableEntity,
		},
		"unknown route": {
			method:         http.MethodGet,
			path:           unknownPath,
			expectedStatus: http.StatusNotFound,
		},
		"method not allowed": {
			method:         http.MethodPatch,
			path:           getClusterPath,
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
					testCase.expectedFields = 5
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
	return webserver.HttpServer{
		SwaggerUI: webserver.SwaggerUIDisabled,
	}.Handler(clusterService)
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
					{Name: controlPlaneNodeName, Role: controlPlaneNodeRole, Status: controlPlaneNodeStatus, Endpoint: controlPlaneNodeEndpoint},
					{Name: "worker-0", Role: "worker", Status: "NotReady"},
				}, nil)
			},
			expectedStatus: http.StatusOK,
			expectedJSON: `[
				{"name":"control-plane-0","role":"control-plane","status":"Ready","endpoint":"10.0.0.11"},
				{"name":"worker-0","role":"worker","status":"NotReady"}
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
