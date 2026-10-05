package handlers_test

import (
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
)

const (
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
				Name:                 clusterName,
				Phase:                clusterPhase,
				ControlPlaneEndpoint: controlPlaneIP,
				ControlPlaneStatus:   handlers.NewFromStrategyStatus(strategies.NodeTypeStatus{}),
				WorkerStatus:         handlers.NewFromStrategyStatus(strategies.NodeTypeStatus{}),
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
	expectedServiceResult := strategies.ClusterInfo{
		Name: clusterName, Phase: clusterPhase, ControlPlaneEndpoint: controlPlaneIP,
	}
	testCases := map[string]struct {
		serviceResult strategies.ClusterInfo
		serviceError  error
		expected      handlers.ClusterInfo
	}{
		testCaseSuccess: {
			serviceResult: expectedServiceResult,
			expected: handlers.ClusterInfo{
				Name:                 clusterName,
				Phase:                clusterPhase,
				ControlPlaneEndpoint: controlPlaneIP,
				ControlPlaneStatus:   handlers.NewFromStrategyStatus(strategies.NodeTypeStatus{}),
				WorkerStatus:         handlers.NewFromStrategyStatus(strategies.NodeTypeStatus{}),
			},
		},
		testCaseServiceError: {serviceError: errTest},
	}

	for name, testCase := range testCases {
		t.Run(name, func(t *testing.T) {
			service, handler := newClusterHandler(t)
			service.EXPECT().GetCluster(gomock.Any(), clusterName).Return(testCase.serviceResult, testCase.serviceError)

			result, err := handler.GetCluster(newContextWithClusterID())

			assert.Equal(t, testCase.expected, result)
			assertHandlerError(t, err, testCase.serviceError)
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
				Name: nodeName, Role: nodeRole, Status: nodeStatus, Endpoint: nodeEndpoint,
			}},
		},
		testCaseServiceError: {serviceError: errTest},
	}

	for name, testCase := range testCases {
		t.Run(name, func(t *testing.T) {
			service, handler := newClusterHandler(t)
			service.EXPECT().GetClusterNodesInfo(gomock.Any(), clusterName).Return(testCase.serviceResult, testCase.serviceError)

			ctx := newContextWithClusterID().SetQueryParam("fields", "*")
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
		expected      string
	}{
		testCaseSuccess:      {serviceResult: clusterConfig, expected: clusterConfig},
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
		Name: clusterName, ControlPlaneReplicas: controlPlaneReplicas, WorkerReplicas: workerReplicas,
		ControlPlaneIP: controlPlaneIP, VMNamePrefix: vmNamePrefix,
	}
	expectedParams := strategies.CreateClusterParams{
		Name: clusterName, ControlPlaneReplicas: controlPlaneReplicas, WorkerReplicas: workerReplicas,
		ControlPlaneIP: controlPlaneIP, VMNamePrefix: vmNamePrefix,
	}
	testCases := map[string]struct {
		serviceResult strategies.ClusterInfo
		serviceError  error
		expected      handlers.ClusterInfo
	}{
		testCaseSuccess: {
			serviceResult: strategies.ClusterInfo{Name: clusterName, Phase: clusterPhase},
			expected: handlers.ClusterInfo{
				Name:               clusterName,
				Phase:              clusterPhase,
				ControlPlaneStatus: handlers.NewFromStrategyStatus(strategies.NodeTypeStatus{}),
				WorkerStatus:       handlers.NewFromStrategyStatus(strategies.NodeTypeStatus{}),
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
		"cluster name required":           {strategies.ErrClusterNameRequired, http.StatusUnprocessableEntity},
		"control plane replicas required": {strategies.ErrControlPlaneReplicasRequired, http.StatusUnprocessableEntity},
		"worker replicas required":        {strategies.ErrWorkerReplicasRequired, http.StatusUnprocessableEntity},
		"control plane IP required":       {strategies.ErrControlPlaneIPRequired, http.StatusUnprocessableEntity},
		"cluster already exists":          {strategies.ErrClusterAlreadyExists, http.StatusConflict},
	}
	body := handlers.ClusterCreateParams{
		Name: clusterName, ControlPlaneReplicas: controlPlaneReplicas, WorkerReplicas: workerReplicas,
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
			service.EXPECT().GetCluster(gomock.Any(), clusterName).Return(strategies.ClusterInfo{}, strategies.ErrClusterNotFound)
			_, err := handler.GetCluster(newContextWithClusterID())
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
				{Name: nodeName, Role: nodeRole, Status: nodeStatus, Endpoint: nodeEndpoint},
				{Name: "worker-0", Role: "worker", Status: "NotReady"},
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
