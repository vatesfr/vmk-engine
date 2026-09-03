package webserver_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"capv-proxy/pkg/components/webserver"
	"capv-proxy/pkg/strategies"
	"capv-proxy/pkg/strategies/mocks"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
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
	clustersPath                   = "/api/kubernetes/clusters/"
	getClusterPath                 = "/api/kubernetes/clusters/cluster-1"
	clusterNodesPath               = "/api/kubernetes/clusters/cluster-1/nodes"
	clusterConfigPath              = "/api/kubernetes/clusters/cluster-1/config"
	contentTypeHeader              = "Content-Type"
	acceptHeader                   = "Accept"
	applicationJSON                = "application/json"
	unknownPath                    = "/unknown"
	createClusterRequestBody       = `{
		"name":"cluster-1",
		"controlPlaneReplicas":1,
		"workerReplicas":1,
		"controlPlaneIP":"10.0.0.10",
		"vmNamePrefix":"test"
	}`
	malformedRequestBody   = `{"name":`
	missingNameRequestBody = `{
		"controlPlaneReplicas":1,
		"workerReplicas":1,
		"controlPlaneIP":"10.0.0.10",
		"vmNamePrefix":"test"
	}`
	invalidControlPlaneIPRequestBody = `{
		"name":"cluster-1",
		"controlPlaneReplicas":1,
		"workerReplicas":1,
		"controlPlaneIP":"invalid",
		"vmNamePrefix":"test"
	}`
	invalidControlPlaneReplicasRequestBody = `{
		"name":"cluster-1",
		"controlPlaneReplicas":0,
		"workerReplicas":1,
		"controlPlaneIP":"10.0.0.10",
		"vmNamePrefix":"test"
	}`
	invalidWorkerReplicasRequestBody = `{
		"name":"cluster-1",
		"controlPlaneReplicas":1,
		"workerReplicas":0,
		"controlPlaneIP":"10.0.0.10",
		"vmNamePrefix":"test"
	}`
	missingVMNamePrefixRequestBody = `{
		"name":"cluster-1",
		"controlPlaneReplicas":1,
		"workerReplicas":1,
		"controlPlaneIP":"10.0.0.10"
	}`
)

var testError = errors.New("test error")

type httpTestCase struct {
	method         string
	path           string
	body           string
	setupMock      func(*mocks.MockForManagingClusters)
	expectedStatus int
	expectedJSON   string
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
			setupMock: func(service *mocks.MockForManagingClusters) {
				service.EXPECT().GetClusters().Return([]strategies.ClusterInfo{
					{Name: clusterName, Phase: clusterPhase, ControlPlaneEndpoint: controlPlaneEndpoint},
					{Name: secondClusterName, Phase: secondClusterPhase},
				}, nil)
			},
			expectedStatus: http.StatusOK,
			expectedJSON: `[
				{"name":"cluster-1","phase":"Provisioned","controlPlaneEndpoint":"10.0.0.10"},
				{"name":"cluster-2","phase":"Provisioning"}
			]`,
		},
		"get cluster": {
			method: http.MethodGet,
			path:   getClusterPath,
			setupMock: func(service *mocks.MockForManagingClusters) {
				service.EXPECT().GetCluster(clusterName).Return(strategies.ClusterInfo{
					Name: clusterName, Phase: clusterPhase, ControlPlaneEndpoint: controlPlaneEndpoint,
				}, nil)
			},
			expectedStatus: http.StatusOK,
			expectedJSON:   `{"name":"cluster-1","phase":"Provisioned","controlPlaneEndpoint":"10.0.0.10"}`,
		},
		"get cluster nodes": {
			method: http.MethodGet,
			path:   clusterNodesPath,
			setupMock: func(service *mocks.MockForManagingClusters) {
				service.EXPECT().GetClusterNodesInfo(clusterName).Return([]strategies.ClusterNodeInfo{{
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
			setupMock: func(service *mocks.MockForManagingClusters) {
				service.EXPECT().GetClusterConfig(clusterName).Return(clusterConfig, nil)
			},
			expectedStatus: http.StatusOK,
			expectedJSON:   `"apiVersion: v1\nkind: Config"`,
		},
		"create cluster": {
			method: http.MethodPost,
			path:   clustersPath,
			body:   createClusterRequestBody,
			setupMock: func(service *mocks.MockForManagingClusters) {
				service.EXPECT().CreateCluster(validCreateClusterParams()).Return(strategies.ClusterInfo{
					Name: clusterName, Phase: clusterPhase, ControlPlaneEndpoint: controlPlaneEndpoint,
				}, nil)
			},
			expectedStatus: http.StatusOK,
			expectedJSON:   `{"name":"cluster-1","phase":"Provisioned","controlPlaneEndpoint":"10.0.0.10"}`,
		},
		"create cluster service error": {
			method: http.MethodPost,
			path:   clustersPath,
			body:   createClusterRequestBody,
			setupMock: func(service *mocks.MockForManagingClusters) {
				service.EXPECT().CreateCluster(validCreateClusterParams()).Return(strategies.ClusterInfo{}, testError)
			},
			expectedStatus: http.StatusInternalServerError,
		},
		"delete cluster": {
			method: http.MethodDelete,
			path:   getClusterPath,
			setupMock: func(service *mocks.MockForManagingClusters) {
				service.EXPECT().DeleteCluster(clusterName).Return(nil)
			},
			expectedStatus: http.StatusOK,
			expectedJSON:   `"ok"`,
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

func executeHTTPTestCase(t *testing.T, testCase httpTestCase) {
	t.Helper()

	ctrl := gomock.NewController(t)
	clusterService := mocks.NewMockForManagingClusters(ctrl)
	if testCase.setupMock != nil {
		testCase.setupMock(clusterService)
	}

	handler := newTestHandler(clusterService)
	request := httptest.NewRequest(testCase.method, testCase.path, strings.NewReader(testCase.body))
	request.Header.Set(acceptHeader, applicationJSON)
	if testCase.body != "" {
		request.Header.Set(contentTypeHeader, applicationJSON)
	}
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	require.Equal(t, testCase.expectedStatus, response.Code, response.Body.String())
	if testCase.expectedJSON != "" {
		assert.JSONEq(t, testCase.expectedJSON, response.Body.String())
	} else if testCase.expectedStatus >= http.StatusBadRequest &&
		response.Header().Get(contentTypeHeader) == applicationJSON {
		assertJSONErrorStatus(t, response, testCase.expectedStatus)
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

func validCreateClusterParams() strategies.CreateClusterParams {
	return strategies.CreateClusterParams{
		Name:                 clusterName,
		ControlPlaneReplicas: controlPlaneReplicas,
		WorkerReplicas:       workerReplicas,
		ControlPlaneIP:       controlPlaneIP,
		VMNamePrefix:         vmNamePrefix,
	}
}

func newTestHandler(clusterService strategies.ForManagingClusters) http.Handler {
	return webserver.HttpServer{
		SwaggerUI: webserver.SwaggerUIDisabled,
	}.Handler(clusterService)
}
