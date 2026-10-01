package webserver_test

import (
	"context"
	"encoding/json"
	"errors"
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
	clustersPath                   = "/api/kubernetes/clusters"
	getClusterPath                 = "/api/kubernetes/clusters/cluster-1"
	clusterNodesPath               = "/api/kubernetes/clusters/cluster-1/nodes"
	clusterConfigPath              = "/api/kubernetes/clusters/cluster-1/config"
	clusterTagsPath                = "/api/kubernetes/clusters/cluster-1/tags"
	clusterTagPath                 = "/api/kubernetes/clusters/cluster-1/tags/environment"
	contentTypeHeader              = "Content-Type"
	acceptHeader                   = "Accept"
	applicationJSON                = "application/json"
	okJSON                         = `"ok"`
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
				{"name":"cluster-1","phase":"Provisioned","control_plane_endpoint":"10.0.0.10","created_at":"",
                "control_plane_status":{"available_replicas":0,"desired_replicas":0,"ready_replicas":0,"replicas":0},
                "worker_status":{"available_replicas":0,"desired_replicas":0,"ready_replicas":0,"replicas":0}},
				{"name":"cluster-2","phase":"Provisioning","created_at":"",
                "control_plane_status":{"available_replicas":0,"desired_replicas":0,"ready_replicas":0,"replicas":0},
                "worker_status":{"available_replicas":0,"desired_replicas":0,"ready_replicas":0,"replicas":0}}
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
			expectedJSON: `{"name":"cluster-1","phase":"Provisioned","control_plane_endpoint":"10.0.0.10","created_at":"",
                "control_plane_status":{"available_replicas":0,"desired_replicas":0,"ready_replicas":0,"replicas":0},
                "worker_status":{"available_replicas":0,"desired_replicas":0,"ready_replicas":0,"replicas":0}}`,
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
		"get cluster config": {
			method: http.MethodGet,
			path:   clusterConfigPath,
			setupMock: func(ctx context.Context, service *mocks.MockForManagingClusters) {
				service.EXPECT().GetClusterConfig(ctx, clusterName).Return(clusterConfig, nil)
			},
			expectedStatus: http.StatusOK,
			expectedJSON:   `"apiVersion: v1\nkind: Config"`,
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
			expectedJSON: `{"name":"cluster-1","phase":"Provisioned","control_plane_endpoint":"10.0.0.10","created_at":"",
                "control_plane_status":{"available_replicas":0,"desired_replicas":0,"ready_replicas":0,"replicas":0},
                "worker_status":{"available_replicas":0,"desired_replicas":0,"ready_replicas":0,"replicas":0}}`,
		},
		"create cluster with tags": {
			method: http.MethodPost,
			path:   clustersPath,
			body:   strings.Replace(createClusterRequestBody, `"vm_name_prefix":"test"`, `"vm_name_prefix":"test","tags":{"environment":"production"}`, 1),
			setupMock: func(ctx context.Context, service *mocks.MockForManagingClusters) {
				params := validCreateClusterParams()
				params.Tags = map[string]string{"environment": "production"}
				service.EXPECT().CreateCluster(ctx, params).Return(strategies.ClusterInfo{Name: clusterName, Tags: params.Tags}, nil)
			},
			expectedStatus: http.StatusOK,
			expectedJSON: `{"name":"cluster-1","phase":"","tags":{"environment":"production"},"created_at":"",
                "control_plane_status":{"available_replicas":0,"desired_replicas":0,"ready_replicas":0,"replicas":0},
                "worker_status":{"available_replicas":0,"desired_replicas":0,"ready_replicas":0,"replicas":0}}`,
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
			expectedStatus: http.StatusOK,
			expectedJSON:   okJSON,
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
		"get cluster tags": {
			method: http.MethodGet,
			path:   clusterTagsPath,
			setupMock: func(ctx context.Context, service *mocks.MockForManagingClusters) {
				service.EXPECT().GetClusterTags(ctx, clusterName).Return(map[string]string{"environment": "production"}, nil)
			},
			expectedStatus: http.StatusOK,
			expectedJSON:   `{"result":{"environment":"production"}}`,
		},
		"get cluster tag": {
			method: http.MethodGet,
			path:   clusterTagPath,
			setupMock: func(ctx context.Context, service *mocks.MockForManagingClusters) {
				service.EXPECT().GetClusterTag(ctx, clusterName, "environment").Return("production", nil)
			},
			expectedStatus: http.StatusOK,
			expectedJSON:   `"production"`,
		},
		"set cluster tag": {
			method: http.MethodPut,
			path:   clusterTagPath,
			body:   `{"value":"production"}`,
			setupMock: func(ctx context.Context, service *mocks.MockForManagingClusters) {
				service.EXPECT().SetClusterTag(ctx, clusterName, "environment", "production").Return(nil)
			},
			expectedStatus: http.StatusOK,
			expectedJSON:   okJSON,
		},
		"delete cluster tag": {
			method: http.MethodDelete,
			path:   clusterTagPath,
			setupMock: func(ctx context.Context, service *mocks.MockForManagingClusters) {
				service.EXPECT().DeleteClusterTag(ctx, clusterName, "environment").Return(nil)
			},
			expectedStatus: http.StatusOK,
			expectedJSON:   okJSON,
		},
		"missing cluster tag": {
			method: http.MethodGet,
			path:   clusterTagPath,
			setupMock: func(ctx context.Context, service *mocks.MockForManagingClusters) {
				service.EXPECT().GetClusterTag(ctx, clusterName, "environment").Return("", strategies.ErrClusterTagNotFound)
			},
			expectedStatus: http.StatusNotFound,
			expectedTitle:  "Cluster tag not found",
			expectedDetail: strategies.ErrClusterTagNotFound.Error(),
		},
		"invalid cluster tag": {
			method: http.MethodPut,
			path:   clusterTagPath,
			body:   `{"value":"production"}`,
			setupMock: func(ctx context.Context, service *mocks.MockForManagingClusters) {
				service.EXPECT().SetClusterTag(ctx, clusterName, "environment", "production").Return(strategies.ErrInvalidClusterTagName)
			},
			expectedStatus: http.StatusBadRequest,
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
		"malformed JSON":                      {body: malformedRequestBody},
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
			testCase.expectedStatus = http.StatusBadRequest
			executeHTTPTestCase(t, testCase)
		})
	}
}

func TestClusterTagValueHTTPValidation(t *testing.T) {
	executeHTTPTestCase(t, httpTestCase{
		method:         http.MethodPut,
		path:           clusterTagPath,
		body:           `{"value":"` + strings.Repeat("a", 64) + `"}`,
		expectedStatus: http.StatusBadRequest,
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
	if testCase.expectedJSON != "" {
		assert.JSONEq(t, testCase.expectedJSON, response.Body.String())
	} else if testCase.expectedStatus >= http.StatusBadRequest &&
		response.Header().Get(contentTypeHeader) == applicationJSON {
		assertJSONErrorStatus(t, response, testCase.expectedStatus)
	}
	if testCase.expectedTitle != "" || testCase.expectedDetail != "" {
		assertJSONError(t, response, testCase.expectedTitle, testCase.expectedDetail)
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
