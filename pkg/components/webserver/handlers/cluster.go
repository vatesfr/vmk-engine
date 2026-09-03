package handlers

import (
	"capv-proxy/pkg/strategies"

	"github.com/go-fuego/fuego"
)

type ClusterHandler struct {
	clusterService strategies.ForManagingClusters
}

type ClusterCreateParams struct {
	Name                 string `json:"name" validate:"required"`
	ControlPlaneReplicas int32  `json:"controlPlaneReplicas" validate:"required,min=1"`
	WorkerReplicas       int32  `json:"workerReplicas" validate:"required,min=1"`
	ControlPlaneIP       string `json:"controlPlaneIP" validate:"required,ip"`
	VMNamePrefix         string `json:"vmNamePrefix" validate:"required"`
}

type ClusterInfo struct {
	Name                 string `json:"name"`
	Phase                string `json:"phase"`
	ControlPlaneEndpoint string `json:"controlPlaneEndpoint,omitempty"`
}

type ClusterNodeInfo struct {
	Name     string `json:"name"`
	Role     string `json:"role"`
	Status   string `json:"status"`
	Endpoint string `json:"endpoint,omitempty"`
}

func NewClusterHandler(clusterService strategies.ForManagingClusters) ClusterHandler {
	return ClusterHandler{
		clusterService: clusterService,
	}
}

func (k ClusterHandler) GetClusters(fuego.ContextNoBody) ([]ClusterInfo, error) {
	clusters, err := k.clusterService.GetClusters()
	if err != nil {
		return nil, err
	}

	result := make([]ClusterInfo, 0, len(clusters))
	for _, value := range clusters {
		result = append(result, toClusterInfo(value))
	}
	return result, nil
}

func (k ClusterHandler) GetCluster(c fuego.ContextNoBody) (ClusterInfo, error) {
	cluster, err := k.clusterService.GetCluster(c.PathParam("id"))
	if err != nil {
		return ClusterInfo{}, err
	}
	return toClusterInfo(cluster), nil
}

func (k ClusterHandler) GetClusterNodesInfo(c fuego.ContextNoBody) ([]ClusterNodeInfo, error) {
	nodes, err := k.clusterService.GetClusterNodesInfo(c.PathParam("id"))
	if err != nil {
		return nil, err
	}

	result := make([]ClusterNodeInfo, 0, len(nodes))
	for _, node := range nodes {
		result = append(result, ClusterNodeInfo{
			Name: node.Name, Role: node.Role, Status: node.Status, Endpoint: node.Endpoint,
		})
	}
	return result, nil
}

func (k ClusterHandler) GetClusterConfig(c fuego.ContextNoBody) (string, error) {
	return k.clusterService.GetClusterConfig(c.PathParam("id"))
}

func (k ClusterHandler) CreateCluster(c fuego.ContextWithBody[ClusterCreateParams]) (ClusterInfo, error) {
	createParams, err := c.Body()
	if err != nil {
		return ClusterInfo{}, err
	}

	created, err := k.clusterService.CreateCluster(toCreateClusterParams(createParams))
	if err != nil {
		return ClusterInfo{}, err
	}
	return toClusterInfo(created), nil
}

func toCreateClusterParams(value ClusterCreateParams) strategies.CreateClusterParams {
	return strategies.CreateClusterParams{
		Name:                 value.Name,
		ControlPlaneReplicas: value.ControlPlaneReplicas,
		WorkerReplicas:       value.WorkerReplicas,
		ControlPlaneIP:       value.ControlPlaneIP,
		VMNamePrefix:         value.VMNamePrefix,
	}
}

func toClusterInfo(value strategies.ClusterInfo) ClusterInfo {
	return ClusterInfo{
		Name:                 value.Name,
		Phase:                value.Phase,
		ControlPlaneEndpoint: value.ControlPlaneEndpoint,
	}
}

func (k ClusterHandler) DeleteCluster(c fuego.ContextNoBody) (string, error) {
	if err := k.clusterService.DeleteCluster(c.PathParam("id")); err != nil {
		return "", err
	}

	return "ok", nil
}
