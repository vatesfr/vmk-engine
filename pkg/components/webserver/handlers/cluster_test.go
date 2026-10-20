package handlers_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/go-fuego/fuego"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"capv-proxy/pkg/components/webserver/handlers"
	"capv-proxy/pkg/strategies"
	"capv-proxy/pkg/strategies/mocks"
	"capv-proxy/pkg/uuids"

	"k8s.io/utils/ptr"
)

const (
	clusterVersion             = "1.36.9"
	testCaseSuccess            = "success"
	testCaseServiceError       = "service error"
	clusterName                = "cluster-1"
	clusterPhase               = "Provisioned"
	controlPlaneIP             = "10.0.0.10"
	vmNamePrefix               = "test"
	controlPlaneReplicas int32 = 1
	workerReplicas       int32 = 1
	nodeName                   = "control-plane-0"
	nodeRole                   = "control-plane"
	nodeStatus                 = "Ready"
	nodeEndpoint               = "10.0.0.11"
	clusterConfig              = "apiVersion: v1\nkind: Config"
)

var errTest = errors.New("test error")

func TestNewFromNodeStatus(t *testing.T) {
	testCases := map[string]struct {
		status strategies.NodeTypeStatus
		json   string
	}{
		"empty status": {
			json: `{"available_replicas":0,"desired_replicas":0,"ready_replicas":0,"replicas":0,"up_to_date_replicas":0}`,
		},
		"empty versions": {
			status: strategies.NodeTypeStatus{Versions: []strategies.VersionNodeStatus{}},
			json:   `{"available_replicas":0,"desired_replicas":0,"ready_replicas":0,"replicas":0,"up_to_date_replicas":0}`,
		},
		"multiple versions": {
			status: strategies.NodeTypeStatus{
				AvailableReplicas: 1, DesiredReplicas: 2, ReadyReplicas: 3, Replicas: 4, UpToDateReplicas: 5,
				Versions: []strategies.VersionNodeStatus{{Version: "v1.36.0", Replicas: 2}, {Version: "v1.35.0", Replicas: 3}},
			},
			json: `{"available_replicas":1,"desired_replicas":2,"ready_replicas":3,"replicas":4,"up_to_date_replicas":5,
				"versions":[{"version":"v1.36.0","replicas":2},{"version":"v1.35.0","replicas":3}]}`,
		},
		"zero version replicas": {
			status: strategies.NodeTypeStatus{Versions: []strategies.VersionNodeStatus{{Version: "v1.36.0"}}},
			json: `{"available_replicas":0,"desired_replicas":0,"ready_replicas":0,"replicas":0,"up_to_date_replicas":0,
				"versions":[{"version":"v1.36.0","replicas":0}]}`,
		},
	}
	for name, testCase := range testCases {
		t.Run(name, func(t *testing.T) {
			encoded, err := json.Marshal(handlers.NewFromNodeStatus(testCase.status))
			require.NoError(t, err)
			assert.JSONEq(t, testCase.json, string(encoded))
		})
	}
}

func TestGetClusters(t *testing.T) {
	testCases := map[string]struct {
		serviceResult []strategies.ClusterInfo
		serviceError  error
		expected      []handlers.ClusterInfo
	}{
		testCaseSuccess: {
			serviceResult: []strategies.ClusterInfo{{
				Name: clusterName, Phase: clusterPhase, ControlPlaneEndpoint: controlPlaneIP,
			}},
			expected: []handlers.ClusterInfo{{
				Paused:               ptr.To(false),
				IsAvailable:          ptr.To(false),
				Name:                 clusterName,
				Phase:                clusterPhase,
				ControlPlaneEndpoint: controlPlaneIP,
				ControlPlaneStatus:   handlers.NewFromNodeStatus(strategies.NodeTypeStatus{}),
				WorkerStatus:         handlers.NewFromNodeStatus(strategies.NodeTypeStatus{}),
			}},
		},
		testCaseServiceError: {serviceError: errTest},
	}

	for name, testCase := range testCases {
		t.Run(name, func(t *testing.T) {
			service, handler := newClusterHandler(t)
			service.EXPECT().GetClusters(gomock.Any()).Return(testCase.serviceResult, testCase.serviceError)

			ctx := fuego.NewMockContextNoBody().SetQueryParam("fields", "*")
			result, err := handler.GetClusters(ctx)

			assert.Equal(t, testCase.expected, result)
			assertHandlerError(t, err, testCase.serviceError)
		})
	}
}

func TestGetCluster(t *testing.T) {
	clusterUUID := uuids.ClusterID(clusterName)
	expectedServiceResult := strategies.ClusterInfo{
		ID: clusterUUID, Name: clusterName, Phase: clusterPhase, ControlPlaneEndpoint: controlPlaneIP,
	}
	testCases := map[string]struct {
		serviceResult strategies.ClusterInfo
		serviceError  error
		expected      handlers.ClusterInfo
	}{
		testCaseSuccess: {
			serviceResult: expectedServiceResult,
			expected: handlers.ClusterInfo{
				Paused:               ptr.To(false),
				IsAvailable:          ptr.To(false),
				ID:                   clusterUUID,
				Name:                 clusterName,
				Phase:                clusterPhase,
				ControlPlaneEndpoint: controlPlaneIP,
				ControlPlaneStatus:   handlers.NewFromNodeStatus(strategies.NodeTypeStatus{}),
				WorkerStatus:         handlers.NewFromNodeStatus(strategies.NodeTypeStatus{}),
			},
		},
		testCaseServiceError: {serviceError: errTest},
	}

	for name, testCase := range testCases {
		t.Run(name, func(t *testing.T) {
			service, handler := newClusterHandler(t)
			service.EXPECT().GetClusterByUUID(gomock.Any(), clusterUUID).Return(testCase.serviceResult, testCase.serviceError)

			result, err := handler.GetCluster(newContextWithClusterUUID())

			assert.Equal(t, testCase.expected, result)
			assertHandlerError(t, err, testCase.serviceError)
		})
	}
}

func TestClusterInitializationResponse(t *testing.T) {
	for _, testCase := range []struct {
		name                        string
		isControlPlaneInitialized   *bool
		isInfrastructureProvisioned *bool
		expectedJSON                string
	}{
		{name: "not reported", expectedJSON: `{}`},
		{
			name: "not initialized", isControlPlaneInitialized: ptr.To(false), isInfrastructureProvisioned: ptr.To(false),
			expectedJSON: `{"is_control_plane_initialized":false,"is_infrastructure_provisioned":false}`,
		},
		{
			name: "infrastructure provisioned", isControlPlaneInitialized: ptr.To(false), isInfrastructureProvisioned: ptr.To(true),
			expectedJSON: `{"is_control_plane_initialized":false,"is_infrastructure_provisioned":true}`,
		},
		{
			name: "initialized", isControlPlaneInitialized: ptr.To(true), isInfrastructureProvisioned: ptr.To(true),
			expectedJSON: `{"is_control_plane_initialized":true,"is_infrastructure_provisioned":true}`,
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			service, handler := newClusterHandler(t)
			info := strategies.ClusterInfo{
				IsControlPlaneInitialized:   testCase.isControlPlaneInitialized,
				IsInfrastructureProvisioned: testCase.isInfrastructureProvisioned,
			}
			service.EXPECT().GetCluster(gomock.Any(), clusterID).Return(info, nil)
			result, err := handler.GetCluster(newContextWithClusterID())
			require.NoError(t, err)
			assert.Equal(t, testCase.isControlPlaneInitialized, result.IsControlPlaneInitialized)
			assert.Equal(t, testCase.isInfrastructureProvisioned, result.IsInfrastructureProvisioned)
			encoded, err := json.Marshal(result)
			require.NoError(t, err)
			var fields map[string]json.RawMessage
			require.NoError(t, json.Unmarshal(encoded, &fields))
			for field := range fields {
				if field != "is_control_plane_initialized" && field != "is_infrastructure_provisioned" {
					delete(fields, field)
				}
			}
			encoded, err = json.Marshal(fields)
			require.NoError(t, err)
			assert.JSONEq(t, testCase.expectedJSON, string(encoded))

			service.EXPECT().GetClusters(gomock.Any()).Return([]strategies.ClusterInfo{info}, nil)
			ctx := fuego.NewMockContextNoBody().SetQueryParam("fields", "is_control_plane_initialized,is_infrastructure_provisioned")
			clusters, err := handler.GetClusters(ctx)
			require.NoError(t, err)
			encoded, err = json.Marshal(clusters)
			require.NoError(t, err)
			assert.JSONEq(t, "["+testCase.expectedJSON+"]", string(encoded))
		})
	}
}

func TestGetClusterNodesInfo(t *testing.T) {
	testCases := map[string]struct {
		serviceResult []strategies.ClusterNodeInfo
		serviceError  error
		expected      []handlers.ClusterNodeInfo
	}{
		testCaseSuccess: {
			serviceResult: []strategies.ClusterNodeInfo{{
				Name: nodeName, Role: nodeRole, Status: nodeStatus, Endpoint: nodeEndpoint,
			}},
			expected: []handlers.ClusterNodeInfo{{
				Paused: ptr.To(false),
				Name:   nodeName, Role: nodeRole, Status: nodeStatus, Endpoint: nodeEndpoint,
			}},
		},
		testCaseServiceError: {serviceError: errTest},
	}

	for name, testCase := range testCases {
		t.Run(name, func(t *testing.T) {
			service, handler := newClusterHandler(t)
			service.EXPECT().GetClusterNodesInfo(gomock.Any(), uuids.ClusterID(clusterName)).Return(testCase.serviceResult, testCase.serviceError)

			ctx := newContextWithClusterUUID().SetQueryParam("fields", "*")
			result, err := handler.GetClusterNodesInfo(ctx)

			assert.Equal(t, testCase.expected, result)
			assertHandlerError(t, err, testCase.serviceError)
		})
	}
}

func TestGetClusterConfig(t *testing.T) {
	testCases := map[string]struct {
		serviceResult string
		serviceError  error
		expected      handlers.ClusterConfigResponse
	}{
		testCaseSuccess:      {serviceResult: clusterConfig, expected: handlers.ClusterConfigResponse{Kubeconfig: clusterConfig}},
		testCaseServiceError: {serviceError: errTest},
	}

	for name, testCase := range testCases {
		t.Run(name, func(t *testing.T) {
			service, handler := newClusterHandler(t)
			service.EXPECT().GetClusterConfig(gomock.Any(), clusterName).Return(testCase.serviceResult, testCase.serviceError)

			result, err := handler.GetClusterConfig(newContextWithClusterID())

			assert.Equal(t, testCase.expected, result)
			assertHandlerError(t, err, testCase.serviceError)
		})
	}
}

func TestCreateCluster(t *testing.T) {
	validBody := handlers.ClusterCreateParams{
		KubernetesVersion: clusterVersion,
		Name:              clusterName, ControlPlaneReplicas: controlPlaneReplicas, WorkerReplicas: workerReplicas,
		ControlPlaneIP: controlPlaneIP, VMNamePrefix: vmNamePrefix,
		Description: "Cluster \"Backend\" 🚀\nTest",
	}
	expectedParams := strategies.CreateClusterParams{
		Version: clusterVersion,
		Name:    clusterName, ControlPlaneReplicas: controlPlaneReplicas, WorkerReplicas: workerReplicas,
		ControlPlaneIP: controlPlaneIP, VMNamePrefix: vmNamePrefix,
		Description: "Cluster \"Backend\" 🚀\nTest",
	}
	testCases := map[string]struct {
		serviceResult strategies.ClusterInfo
		serviceError  error
		expected      handlers.ClusterInfo
	}{
		testCaseSuccess: {
			serviceResult: strategies.ClusterInfo{Name: clusterName, Phase: clusterPhase, Description: validBody.Description},
			expected: handlers.ClusterInfo{
				Paused:             ptr.To(false),
				IsAvailable:        ptr.To(false),
				Name:               clusterName,
				Description:        validBody.Description,
				Phase:              clusterPhase,
				ControlPlaneStatus: handlers.NewFromNodeStatus(strategies.NodeTypeStatus{}),
				WorkerStatus:       handlers.NewFromNodeStatus(strategies.NodeTypeStatus{}),
			},
		},
		testCaseServiceError: {serviceError: errTest},
	}

	for name, testCase := range testCases {
		t.Run(name, func(t *testing.T) {
			service, handler := newClusterHandler(t)
			service.EXPECT().CreateCluster(gomock.Any(), expectedParams).Return(testCase.serviceResult, testCase.serviceError)
			ctx := fuego.NewMockContext[handlers.ClusterCreateParams, any](validBody, nil)

			result, err := handler.CreateCluster(ctx)

			assert.Equal(t, testCase.expected, result)
			assertHandlerError(t, err, testCase.serviceError)
		})
	}
}

func TestCreateClusterApplicationErrors(t *testing.T) {
	testCases := map[string]struct {
		err    error
		status int
	}{
		"description too long":            {strategies.ErrClusterDescriptionTooLong, http.StatusUnprocessableEntity},
		"cluster version required":        {strategies.ErrClusterVersionRequired, http.StatusUnprocessableEntity},
		"cluster version unavailable":     {strategies.ErrClusterVersionUnavailable, http.StatusUnprocessableEntity},
		"cluster name required":           {strategies.ErrClusterNameRequired, http.StatusUnprocessableEntity},
		"control plane replicas required": {strategies.ErrControlPlaneReplicasRequired, http.StatusUnprocessableEntity},
		"worker replicas required":        {strategies.ErrWorkerReplicasRequired, http.StatusUnprocessableEntity},
		"control plane IP required":       {strategies.ErrControlPlaneIPRequired, http.StatusUnprocessableEntity},
		"cluster already exists":          {strategies.ErrClusterAlreadyExists, http.StatusConflict},
	}
	body := handlers.ClusterCreateParams{
		KubernetesVersion: clusterVersion,
		Name:              clusterName, ControlPlaneReplicas: controlPlaneReplicas, WorkerReplicas: workerReplicas,
		ControlPlaneIP: controlPlaneIP, VMNamePrefix: vmNamePrefix,
	}

	for name, testCase := range testCases {
		t.Run(name, func(t *testing.T) {
			service, handler := newClusterHandler(t)
			service.EXPECT().CreateCluster(gomock.Any(), gomock.Any()).Return(strategies.ClusterInfo{}, testCase.err)

			ctx := fuego.NewMockContext[handlers.ClusterCreateParams, any](body, nil)
			_, err := handler.CreateCluster(ctx)

			require.Error(t, err)
			assert.ErrorIs(t, err, testCase.err)
			assert.Equal(t, testCase.status, errorStatusCode(t, err))
		})
	}
}

func TestClusterNotFoundApplicationErrors(t *testing.T) {
	testCases := map[string]func(*mocks.MockForManagingClusters, handlers.ClusterHandler) error{
		"get cluster": func(service *mocks.MockForManagingClusters, handler handlers.ClusterHandler) error {
			service.EXPECT().GetClusterByUUID(gomock.Any(), uuids.ClusterID(clusterName)).Return(strategies.ClusterInfo{}, strategies.ErrClusterNotFound)
			_, err := handler.GetCluster(newContextWithClusterUUID())
			return err
		},
		"get config": func(service *mocks.MockForManagingClusters, handler handlers.ClusterHandler) error {
			service.EXPECT().GetClusterConfig(gomock.Any(), clusterName).Return("", strategies.ErrClusterNotFound)
			_, err := handler.GetClusterConfig(newContextWithClusterID())
			return err
		},
		"delete cluster": func(service *mocks.MockForManagingClusters, handler handlers.ClusterHandler) error {
			service.EXPECT().DeleteCluster(gomock.Any(), clusterName).Return(strategies.ErrClusterNotFound)
			_, err := handler.DeleteCluster(newContextWithClusterID())
			return err
		},
	}

	for name, run := range testCases {
		t.Run(name, func(t *testing.T) {
			service, handler := newClusterHandler(t)

			err := run(service, handler)

			require.Error(t, err)
			assert.ErrorIs(t, err, strategies.ErrClusterNotFound)
			assert.Equal(t, http.StatusNotFound, errorStatusCode(t, err))
		})
	}
}

func TestDeleteCluster(t *testing.T) {
	testCases := map[string]struct {
		serviceError error
	}{
		testCaseSuccess:      {},
		testCaseServiceError: {serviceError: errTest},
	}

	for name, testCase := range testCases {
		t.Run(name, func(t *testing.T) {
			service, handler := newClusterHandler(t)
			service.EXPECT().DeleteCluster(gomock.Any(), clusterName).Return(testCase.serviceError)

			result, err := handler.DeleteCluster(newContextWithClusterID())

			assert.Nil(t, result)
			assertHandlerError(t, err, testCase.serviceError)
		})
	}
}

func newClusterHandler(t *testing.T) (*mocks.MockForManagingClusters, handlers.ClusterHandler) {
	t.Helper()
	service := mocks.NewMockForManagingClusters(gomock.NewController(t))
	return service, handlers.NewClusterHandler(service)
}

func newContextWithClusterID() *fuego.MockContext[any, any] {
	ctx := fuego.NewMockContextNoBody()
	ctx.PathParams["id"] = clusterName
	return ctx
}

func newContextWithClusterUUID() *fuego.MockContext[any, any] {
	ctx := fuego.NewMockContextNoBody()
	ctx.PathParams["id"] = uuids.ClusterID(clusterName).String()
	return ctx
}

func newContextWithNodeUUID() *fuego.MockContext[any, any] {
	ctx := fuego.NewMockContextNoBody()
	ctx.PathParams["id"] = uuids.NodeID(clusterName, nodeName).String()
	return ctx
}

func assertHandlerError(t *testing.T, actual, serviceError error) {
	t.Helper()
	if serviceError == nil {
		require.NoError(t, actual)
		return
	}
	require.Error(t, actual)
	assert.ErrorIs(t, actual, serviceError)
	assert.Equal(t, http.StatusInternalServerError, errorStatusCode(t, actual))
}

func errorStatusCode(t *testing.T, err error) int {
	t.Helper()
	var statusError fuego.ErrorWithStatus
	require.ErrorAs(t, err, &statusError)
	return statusError.StatusCode()
}

func TestGetNode(t *testing.T) {
	nodeUUID := uuids.NodeID(clusterName, nodeName)
	testCases := map[string]struct {
		serviceResult strategies.ClusterNodeInfo
		serviceError  error
		expected      handlers.ClusterNodeInfo
	}{
		testCaseSuccess: {
			serviceResult: strategies.ClusterNodeInfo{
				ID: nodeUUID, Name: nodeName, Role: nodeRole, Status: nodeStatus, Endpoint: nodeEndpoint,
			},
			expected: handlers.ClusterNodeInfo{
				Paused: ptr.To(false),
				ID:     nodeUUID, Name: nodeName, Role: nodeRole, Status: nodeStatus, Endpoint: nodeEndpoint,
			},
		},
		testCaseServiceError: {serviceError: errTest},
		"node not found":     {serviceError: strategies.ErrNodeNotFound},
	}

	for name, testCase := range testCases {
		t.Run(name, func(t *testing.T) {
			service, handler := newClusterHandler(t)
			service.EXPECT().GetNodeByUUID(gomock.Any(), nodeUUID).Return(testCase.serviceResult, testCase.serviceError)

			result, err := handler.GetNode(newContextWithNodeUUID())

			assert.Equal(t, testCase.expected, result)
			if testCase.serviceError == nil {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
				assert.ErrorIs(t, err, testCase.serviceError)
			}
		})
	}
}

func TestGetAllNodes(t *testing.T) {
	testCases := map[string]struct {
		serviceResult []strategies.ClusterNodeInfo
		serviceError  error
		expected      []handlers.ClusterNodeInfo
	}{
		"maps all node fields and preserves order": {
			serviceResult: []strategies.ClusterNodeInfo{
				{Name: nodeName, Role: nodeRole, Status: nodeStatus, Endpoint: nodeEndpoint},
				{Name: "worker-0", Role: "worker", Status: "NotReady"},
			},
			expected: []handlers.ClusterNodeInfo{
				{Paused: ptr.To(false), Name: nodeName, Role: nodeRole, Status: nodeStatus, Endpoint: nodeEndpoint},
				{Paused: ptr.To(false), Name: "worker-0", Role: "worker", Status: "NotReady"},
			},
		},
		"empty result":                      {serviceResult: []strategies.ClusterNodeInfo{}, expected: []handlers.ClusterNodeInfo{}},
		"nil result becomes an empty slice": {expected: []handlers.ClusterNodeInfo{}},
		"service error discards partial result": {
			serviceResult: []strategies.ClusterNodeInfo{{Name: nodeName}},
			serviceError:  errTest,
		},
		"retrieval error": {serviceError: strategies.ErrClusterNodesRetrievalFailed},
	}
	for name, tc := range testCases {
		t.Run(name, func(t *testing.T) {
			service, handler := newClusterHandler(t)
			ctx := fuego.NewMockContextNoBody()
			service.EXPECT().GetAllNodes(ctx.Context()).Return(tc.serviceResult, tc.serviceError)

			nodes, err := handler.GetAllNodes(ctx)

			assert.Equal(t, tc.expected, nodes)
			assertHandlerError(t, err, tc.serviceError)
		})
	}
}
