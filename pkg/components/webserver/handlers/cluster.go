package handlers

import (
	"github.com/go-fuego/fuego"

	"capv-proxy/pkg/strategies"
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
	// Kubernetes limits label names and values to 63 characters:
	// https://kubernetes.io/docs/concepts/overview/working-with-objects/labels/#syntax-and-character-set
	Tags map[string]string `json:"tags,omitempty"`
}

type ClusterCreateTagParams struct {
	// Kubernetes limits label values to 63 characters:
	// https://kubernetes.io/docs/concepts/overview/working-with-objects/labels/#syntax-and-character-set
	Value string `json:"value" validate:"max=63"`
}

type ClusterTagsResponse struct {
	Result map[string]string `json:"result"`
}

type ClusterInfo struct {
	Name                 string            `json:"name"`
	Phase                string            `json:"phase"`
	CreatedAt            string            `json:"createdAt"`
	ControlPlaneEndpoint string            `json:"controlPlaneEndpoint,omitempty"`
	ControlPlaneStatus   NodeStatus        `json:"controlPlaneStatus,omitempty"`
	WorkerStatus         NodeStatus        `json:"workerStatus,omitempty"`
	Tags                 map[string]string `json:"tags,omitempty"`
}

type NodeStatus struct {
	AvailableReplicas int `json:"availableReplicas"`
	DesiredReplicas   int `json:"desiredReplicas"`
	ReadyReplicas     int `json:"readyReplicas"`
	Replicas          int `json:"replicas"`
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

func (k ClusterHandler) GetClusters(c fuego.ContextNoBody) ([]ClusterInfo, error) {
	clusters, err := k.clusterService.GetClusters(c.Context())
	if err != nil {
		return nil, toHTTPError(err)
	}

	result := make([]ClusterInfo, 0, len(clusters))
	for _, value := range clusters {
		result = append(result, toClusterInfo(value))
	}
	return result, nil
}

func (k ClusterHandler) GetCluster(c fuego.ContextNoBody) (ClusterInfo, error) {
	cluster, err := k.clusterService.GetCluster(c.Context(), c.PathParam("id"))
	if err != nil {
		return ClusterInfo{}, toHTTPError(err)
	}
	return toClusterInfo(cluster), nil
}

func (k ClusterHandler) GetClusterNodesInfo(c fuego.ContextNoBody) ([]ClusterNodeInfo, error) {
	nodes, err := k.clusterService.GetClusterNodesInfo(c.Context(), c.PathParam("id"))
	if err != nil {
		return nil, toHTTPError(err)
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
	config, err := k.clusterService.GetClusterConfig(c.Context(), c.PathParam("id"))
	if err != nil {
		return "", toHTTPError(err)
	}
	return config, nil
}

func (k ClusterHandler) CreateCluster(c fuego.ContextWithBody[ClusterCreateParams]) (ClusterInfo, error) {
	createParams, err := c.Body()
	if err != nil {
		return ClusterInfo{}, toHTTPError(err)
	}

	created, err := k.clusterService.CreateCluster(c.Context(), toCreateClusterParams(createParams))
	if err != nil {
		return ClusterInfo{}, toHTTPError(err)
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
		Tags:                 value.Tags,
	}
}

func toClusterInfo(value strategies.ClusterInfo) ClusterInfo {
	return ClusterInfo{
		Name:                 value.Name,
		Phase:                value.Phase,
		CreatedAt:            value.CreatedAt,
		ControlPlaneEndpoint: value.ControlPlaneEndpoint,
		ControlPlaneStatus:   NodeStatus(value.ControlPlanesStatus),
		WorkerStatus:         NodeStatus(value.WorkersStatus),
		Tags:                 value.Tags,
	}
}

func (k ClusterHandler) DeleteCluster(c fuego.ContextNoBody) (string, error) {
	if err := k.clusterService.DeleteCluster(c.Context(), c.PathParam("id")); err != nil {
		return "", toHTTPError(err)
	}

	return "ok", nil
}

func (k ClusterHandler) GetClusterTags(c fuego.ContextNoBody) (ClusterTagsResponse, error) {
	tags, err := k.clusterService.GetClusterTags(c.Context(), c.PathParam("id"))
	if err != nil {
		return ClusterTagsResponse{}, toHTTPError(err)
	}
	return ClusterTagsResponse{Result: tags}, nil
}

func (k ClusterHandler) GetClusterTag(c fuego.ContextNoBody) (string, error) {
	value, err := k.clusterService.GetClusterTag(c.Context(), c.PathParam("id"), c.PathParam("tag"))
	if err != nil {
		return "", toHTTPError(err)
	}
	return value, nil
}

func (k ClusterHandler) CreateOrUpdateClusterTag(c fuego.ContextWithBody[ClusterCreateTagParams]) (string, error) {
	createParams, err := c.Body()
	if err != nil {
		return err.Error(), err
	}

	if err := k.clusterService.SetClusterTag(c.Context(), c.PathParam("id"), c.PathParam("tag"), createParams.Value); err != nil {
		return "", toHTTPError(err)
	}

	return "ok", nil
}

func (k ClusterHandler) DeleteClusterTag(c fuego.ContextNoBody) (string, error) {
	if err := k.clusterService.DeleteClusterTag(c.Context(), c.PathParam("id"), c.PathParam("tag")); err != nil {
		return "", toHTTPError(err)
	}

	return "ok", nil
}
