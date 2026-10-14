package handlers

import (
	"github.com/go-fuego/fuego"
	"github.com/gofrs/uuid"
	"github.com/samber/lo"

	"capv-proxy/pkg/strategies"
)

type ClusterHandler struct {
	clusterService strategies.ForManagingClusters
}

type ClusterCreateParams struct {
	KubernetesVersion    string `json:"kubernetes_version" validate:"required" description:"Available Kubernetes release in major.minor.patch format, without a v prefix" example:"1.36.9"`
	Name                 string `json:"name" validate:"required" description:"Lowercase RFC 1123 label; generated resource names including suffixes must fit within 63 characters"`
	Description          string `json:"description,omitempty" validate:"max=4000"`
	ControlPlaneReplicas int32  `json:"control_plane_replicas" validate:"required,min=1"`
	WorkerReplicas       int32  `json:"worker_replicas" validate:"required,min=1"`
	ControlPlaneIP       string `json:"control_plane_ip" validate:"required,ip"`
	VMNamePrefix         string `json:"vm_name_prefix" validate:"required"`
	// Kubernetes limits label names and values to 63 characters:
	// https://kubernetes.io/docs/concepts/overview/working-with-objects/labels/#syntax-and-character-set
	Tags map[string]string `json:"tags,omitempty"`
}

type ClusterReplicasParams struct {
	Replicas *int32 `json:"replicas" validate:"required,min=1" description:"Desired machine count; at least 1. Stacked-etcd control planes require an odd count." example:"3"`
}

type ClusterCreateTagParams struct {
	// Kubernetes limits label values to 63 characters:
	// https://kubernetes.io/docs/concepts/overview/working-with-objects/labels/#syntax-and-character-set
	Value string `json:"value" validate:"max=63"`
}

type ClusterTagsResponse struct {
	Result map[string]string `json:"result"`
}

type AllClusterTagsResponse struct {
	Result map[string]map[string]string `json:"result"`
}

type ClusterConfigResponse struct {
	Kubeconfig string `json:"kubeconfig"`
}

type ClusterInfo struct {
	Paused               *bool             `json:"is_reconciliation_paused,omitempty" description:"Whether Cluster API reconciliation is paused via spec.paused. VMs and workloads continue running"`
	IsAvailable          *bool             `json:"is_available,omitempty"`
	Name                 string            `json:"name,omitempty"`
	Description          string            `json:"description,omitempty"`
	Phase                string            `json:"phase,omitempty"`
	CreatedAt            string            `json:"created_at,omitempty"`
	ControlPlaneEndpoint string            `json:"control_plane_endpoint,omitempty"`
	ControlPlaneStatus   *NodeStatus       `json:"control_plane_status,omitempty"`
	WorkerStatus         *NodeStatus       `json:"worker_status,omitempty"`
	Tags                 map[string]string `json:"tags,omitempty"`
}

type NodeStatus struct {
	AvailableReplicas *int                `json:"available_replicas,omitempty"`
	DesiredReplicas   *int                `json:"desired_replicas,omitempty"`
	ReadyReplicas     *int                `json:"ready_replicas,omitempty"`
	Replicas          *int                `json:"replicas,omitempty"`
	UpToDateReplicas  *int                `json:"up_to_date_replicas,omitempty"`
	Versions          []VersionNodeStatus `json:"versions,omitempty"`
}

type VersionNodeStatus struct {
	Replicas *int   `json:"replicas,omitempty"`
	Version  string `json:"version,omitempty"`
}

func NewFromNodeStatus(status strategies.NodeTypeStatus) *NodeStatus {
	n := NodeStatus{}
	n.AvailableReplicas = &status.AvailableReplicas
	n.DesiredReplicas = &status.DesiredReplicas
	n.ReadyReplicas = &status.ReadyReplicas
	n.Replicas = &status.Replicas
	n.UpToDateReplicas = &status.UpToDateReplicas
	for _, version := range status.Versions {
		n.Versions = append(n.Versions, VersionNodeStatus{
			Replicas: &version.Replicas,
			Version:  version.Version,
		})
	}
	return &n
}

type ClusterNodeInfo struct {
	Paused        *bool      `json:"is_reconciliation_paused,omitempty" description:"Machine CAPI reconciliation pause; independent of cluster pause. VMs and workloads keep running"`
	Name          string     `json:"name,omitempty"`
	NodeName      string     `json:"nodeName,omitempty"`
	Role          string     `json:"role,omitempty"`
	Status        string     `json:"status,omitempty"`
	Endpoint      string     `json:"endpoint,omitempty"`
	NodeID        *uuid.UUID `json:"node_id,omitempty"`
	ParentCluster string     `json:"$cluster,omitempty"`
}

func NewClusterHandler(clusterService strategies.ForManagingClusters) ClusterHandler {
	return ClusterHandler{
		clusterService: clusterService,
	}
}

func (k ClusterHandler) GetClusters(c fuego.ContextNoBody) ([]ClusterInfo, error) {
	fields := c.QueryParam("fields")

	clusters, err := k.clusterService.GetClusters(c.Context())
	if err != nil {
		return nil, toHTTPError(err)
	}

	result := make([]ClusterInfo, 0, len(clusters))
	for _, value := range clusters {
		result = append(result, toClusterInfo(value))
	}
	return selectCollectionFields(result, fields)
}

func (k ClusterHandler) GetCluster(c fuego.ContextNoBody) (ClusterInfo, error) {
	cluster, err := k.clusterService.GetCluster(c.Context(), c.PathParam("id"))
	if err != nil {
		return ClusterInfo{}, toHTTPError(err)
	}
	return toClusterInfo(cluster), nil
}

func (k ClusterHandler) GetClusterNodesInfo(c fuego.ContextNoBody) ([]ClusterNodeInfo, error) {
	fields := c.QueryParam("fields")

	nodes, err := k.clusterService.GetClusterNodesInfo(c.Context(), c.PathParam("id"))
	if err != nil {
		return nil, toHTTPError(err)
	}

	return selectCollectionFields(toNodesInfo(nodes), fields)
}

func (k ClusterHandler) GetClusterConfig(c fuego.ContextNoBody) (ClusterConfigResponse, error) {
	config, err := k.clusterService.GetClusterConfig(c.Context(), c.PathParam("id"))
	if err != nil {
		return ClusterConfigResponse{}, toHTTPError(err)
	}
	return ClusterConfigResponse{Kubeconfig: config}, nil
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
		Version:              value.KubernetesVersion,
		Name:                 value.Name,
		Description:          value.Description,
		ControlPlaneReplicas: value.ControlPlaneReplicas,
		WorkerReplicas:       value.WorkerReplicas,
		ControlPlaneIP:       value.ControlPlaneIP,
		VMNamePrefix:         value.VMNamePrefix,
		Tags:                 value.Tags,
	}
}

func toClusterInfo(value strategies.ClusterInfo) ClusterInfo {
	return ClusterInfo{
		IsAvailable:          &value.IsAvailable,
		Paused:               &value.Paused,
		Name:                 value.Name,
		Description:          value.Description,
		Phase:                value.Phase,
		CreatedAt:            value.CreatedAt,
		ControlPlaneEndpoint: value.ControlPlaneEndpoint,
		ControlPlaneStatus:   NewFromNodeStatus(value.ControlPlanesStatus),
		WorkerStatus:         NewFromNodeStatus(value.WorkersStatus),
		Tags:                 value.Tags,
	}
}

func toNodesInfo(value []strategies.ClusterNodeInfo) []ClusterNodeInfo {
	return lo.Map(value, func(node strategies.ClusterNodeInfo, _ int) ClusterNodeInfo {
		return ClusterNodeInfo{
			Name:          node.Name,
			NodeName:      node.NodeName,
			Role:          node.Role,
			Status:        node.Status,
			Endpoint:      node.Endpoint,
			NodeID:        node.MachineID,
			ParentCluster: node.ParentCluster,
			Paused:        &node.Paused,
		}
	})
}

func (k ClusterHandler) DeleteCluster(c fuego.ContextNoBody) (any, error) {
	if err := k.clusterService.DeleteCluster(c.Context(), c.PathParam("id")); err != nil {
		return nil, toHTTPError(err)
	}

	return nil, nil
}

func (k ClusterHandler) GetAllTags(c fuego.ContextNoBody) (AllClusterTagsResponse, error) {
	tags, err := k.clusterService.GetAllTags(c.Context())
	if err != nil {
		return AllClusterTagsResponse{}, toHTTPError(err)
	}
	return AllClusterTagsResponse{Result: tags}, nil
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

func (k ClusterHandler) CreateOrUpdateClusterTag(c fuego.ContextWithBody[ClusterCreateTagParams]) (any, error) {
	createParams, err := c.Body()
	if err != nil {
		return nil, toHTTPError(err)
	}

	if err := k.clusterService.SetClusterTag(c.Context(), c.PathParam("id"), c.PathParam("tag"), createParams.Value); err != nil {
		return nil, toHTTPError(err)
	}

	return nil, nil
}

func (k ClusterHandler) DeleteClusterTag(c fuego.ContextNoBody) (any, error) {
	if err := k.clusterService.DeleteClusterTag(c.Context(), c.PathParam("id"), c.PathParam("tag")); err != nil {
		return nil, toHTTPError(err)
	}

	return nil, nil
}

func (k ClusterHandler) GetAllNodes(c fuego.ContextNoBody) ([]ClusterNodeInfo, error) {
	nodes, err := k.clusterService.GetAllNodes(c.Context())
	if err != nil {
		return nil, toHTTPError(err)
	}

	return toNodesInfo(nodes), nil
}

func (k ClusterHandler) DeleteClusterNode(c fuego.ContextNoBody) (any, error) {
	if err := k.clusterService.DeleteClusterNode(c.Context(), c.PathParam("id"), c.PathParam("nodeName")); err != nil {
		return nil, toHTTPError(err)
	}
	return nil, nil
}

func (k ClusterHandler) SetWorkerReplicas(c fuego.ContextWithBody[ClusterReplicasParams]) (any, error) {
	params, err := c.Body()
	if err != nil {
		return nil, toHTTPError(err)
	}
	if err := k.clusterService.SetWorkerReplicas(c.Context(), c.PathParam("id"), *params.Replicas); err != nil {
		return nil, toHTTPError(err)
	}
	return nil, nil
}

func (k ClusterHandler) SetControlPlaneReplicas(c fuego.ContextWithBody[ClusterReplicasParams]) (any, error) {
	params, err := c.Body()
	if err != nil {
		return nil, toHTTPError(err)
	}
	if err := k.clusterService.SetControlPlaneReplicas(c.Context(), c.PathParam("id"), *params.Replicas); err != nil {
		return nil, toHTTPError(err)
	}
	return nil, nil
}

func (k ClusterHandler) PauseCluster(c fuego.ContextNoBody) (any, error) {
	return nil, toHTTPErrorOrNil(k.clusterService.SetClusterPaused(c.Context(), c.PathParam("id"), true))
}

func (k ClusterHandler) ResumeCluster(c fuego.ContextNoBody) (any, error) {
	return nil, toHTTPErrorOrNil(k.clusterService.SetClusterPaused(c.Context(), c.PathParam("id"), false))
}

func (k ClusterHandler) PauseClusterNode(c fuego.ContextNoBody) (any, error) {
	return nil, toHTTPErrorOrNil(k.clusterService.SetClusterNodePaused(c.Context(), c.PathParam("id"), c.PathParam("nodeName"), true))
}

func (k ClusterHandler) ResumeClusterNode(c fuego.ContextNoBody) (any, error) {
	return nil, toHTTPErrorOrNil(k.clusterService.SetClusterNodePaused(c.Context(), c.PathParam("id"), c.PathParam("nodeName"), false))
}

func toHTTPErrorOrNil(err error) error {
	if err == nil {
		return nil
	}
	return toHTTPError(err)
}
