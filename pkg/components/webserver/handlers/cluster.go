package handlers

import (
	"github.com/go-fuego/fuego"
	"github.com/gofrs/uuid"
	"github.com/samber/lo"

	uuids "capv-proxy/pkg/helpers/uuids"
	"capv-proxy/pkg/strategies"
)

type ClusterHandler struct {
	clusterService strategies.ForManagingClusters
}

type ClusterCreateParams struct {
	CreatedByXOUserID    string `json:"created_by_xo_user_id,omitempty" description:"XO user ID of the cluster creator, persisted on the CAPI Cluster"`
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

type ClusterDescriptionParams struct {
	Description *string `json:"description" validate:"required,max=4000" description:"Cluster description, up to 4000 Unicode code points. An empty string clears it."`
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
	IsCordon                    *bool             `json:"is_cordon,omitempty"`
	IsDraining                  *bool             `json:"is_draining,omitempty" description:"Cordoned node with at least one terminating pod; cluster value means any node"`
	CreatedByXOUserID           string            `json:"created_by_xo_user_id,omitempty" description:"XO user ID supplied at cluster creation"`
	Paused                      *bool             `json:"is_reconciliation_paused,omitempty" description:"Cluster API reconciliation paused via spec.paused. VMs and workloads continue running"`
	IsAvailable                 *bool             `json:"is_available,omitempty"`
	ID                          *uuid.UUID        `json:"id,omitempty"`
	IsControlPlaneInitialized   *bool             `json:"is_control_plane_initialized,omitempty" description:"Control plane initialization milestone; stays true once reached. Omitted if unreported"`
	IsInfrastructureProvisioned *bool             `json:"is_infrastructure_provisioned,omitempty" description:"Infrastructure provisioning milestone; stays true once reached. Omitted if unreported"`
	Name                        string            `json:"name,omitempty"`
	Description                 string            `json:"description,omitempty"`
	Phase                       string            `json:"phase,omitempty"`
	CreatedAt                   string            `json:"created_at,omitempty"`
	ControlPlaneEndpoint        string            `json:"control_plane_endpoint,omitempty"`
	ControlPlaneStatus          *NodeStatus       `json:"control_plane_status,omitempty"`
	WorkerStatus                *NodeStatus       `json:"worker_status,omitempty"`
	Tags                        map[string]string `json:"tags,omitempty"`
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
	ID            *uuid.UUID `json:"id,omitempty"`
	IsCordon      *bool      `json:"is_cordon,omitempty"`
	IsDraining    *bool      `json:"is_draining,omitempty" description:"Cordoned node with at least one terminating pod; cluster value means any node"`
	Paused        *bool      `json:"is_reconciliation_paused,omitempty" description:"Machine CAPI reconciliation pause; independent of cluster pause. VMs and workloads keep running"`
	Name          string     `json:"name,omitempty"`
	NodeName      string     `json:"node_name,omitempty"`
	Role          string     `json:"role,omitempty"`
	Status        string     `json:"status,omitempty"`
	Endpoint      string     `json:"endpoint,omitempty"`
	MachineID     *uuid.UUID `json:"machine_id,omitempty"`
	ParentCluster *uuid.UUID `json:"$cluster,omitempty"`
	Version       string     `json:"version,omitempty"`
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
	id, err := uuids.Parse(c.PathParam("id"))
	if err != nil {
		return ClusterInfo{}, toHTTPError(err)
	}
	cluster, err := k.clusterService.GetClusterByUUID(c.Context(), id)
	if err != nil {
		return ClusterInfo{}, toHTTPError(err)
	}
	return toClusterInfo(cluster), nil
}

func (k ClusterHandler) GetNode(c fuego.ContextNoBody) (ClusterNodeInfo, error) {
	id, err := uuids.Parse(c.PathParam("id"))
	if err != nil {
		return ClusterNodeInfo{}, toHTTPError(err)
	}
	node, err := k.clusterService.GetNodeByUUID(c.Context(), id)
	if err != nil {
		return ClusterNodeInfo{}, toHTTPError(err)
	}
	return toNodeInfo(node), nil
}

func (k ClusterHandler) GetClusterNodesInfo(c fuego.ContextNoBody) ([]ClusterNodeInfo, error) {
	fields := c.QueryParam("fields")
	id, err := uuids.Parse(c.PathParam("id"))
	if err != nil {
		return nil, toHTTPError(err)
	}
	nodes, err := k.clusterService.GetClusterNodesInfo(c.Context(), id)
	if err != nil {
		return nil, toHTTPError(err)
	}

	return selectCollectionFields(toNodesInfo(nodes), fields)
}

func (k ClusterHandler) GetClusterConfig(c fuego.ContextNoBody) (ClusterConfigResponse, error) {
	id, err := uuids.Parse(c.PathParam("id"))
	if err != nil {
		return ClusterConfigResponse{}, toHTTPError(err)
	}
	config, err := k.clusterService.GetClusterConfig(c.Context(), id)
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
		CreatedByXOUserID:    value.CreatedByXOUserID,
		ControlPlaneReplicas: value.ControlPlaneReplicas,
		WorkerReplicas:       value.WorkerReplicas,
		ControlPlaneIP:       value.ControlPlaneIP,
		VMNamePrefix:         value.VMNamePrefix,
		Tags:                 value.Tags,
	}
}

func toClusterInfo(value strategies.ClusterInfo) ClusterInfo {
	return ClusterInfo{
		IsCordon:                    value.IsCordon,
		IsDraining:                  value.IsDraining,
		IsAvailable:                 &value.IsAvailable,
		IsControlPlaneInitialized:   value.IsControlPlaneInitialized,
		IsInfrastructureProvisioned: value.IsInfrastructureProvisioned,
		ID:                          &value.ID,
		Paused:                      &value.Paused,
		Name:                        value.Name,
		Description:                 value.Description,
		CreatedByXOUserID:           value.CreatedByXOUserID,
		Phase:                       value.Phase,
		CreatedAt:                   value.CreatedAt,
		ControlPlaneEndpoint:        value.ControlPlaneEndpoint,
		ControlPlaneStatus:          NewFromNodeStatus(value.ControlPlanesStatus),
		WorkerStatus:                NewFromNodeStatus(value.WorkersStatus),
		Tags:                        value.Tags,
	}
}

func toNodeInfo(node strategies.ClusterNodeInfo) ClusterNodeInfo {
	return ClusterNodeInfo{
		IsCordon:      node.IsCordon,
		IsDraining:    node.IsDraining,
		ID:            &node.ID,
		NodeName:      node.NodeName,
		Paused:        &node.Paused,
		ParentCluster: &node.ParentCluster,
		Name:          node.Name,
		Role:          node.Role,
		Status:        node.Status,
		Endpoint:      node.Endpoint,
		MachineID:     node.MachineID,
		Version:       node.Version,
	}
}

func toNodesInfo(value []strategies.ClusterNodeInfo) []ClusterNodeInfo {
	return lo.Map(value, func(node strategies.ClusterNodeInfo, _ int) ClusterNodeInfo {
		return toNodeInfo(node)
	})
}

func (k ClusterHandler) DeleteCluster(c fuego.ContextNoBody) (any, error) {
	id, err := uuids.Parse(c.PathParam("id"))
	if err != nil {
		return nil, toHTTPError(err)
	}
	if err := k.clusterService.DeleteCluster(c.Context(), id); err != nil {
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
	id, err := uuids.Parse(c.PathParam("id"))
	if err != nil {
		return ClusterTagsResponse{}, toHTTPError(err)
	}
	tags, err := k.clusterService.GetClusterTags(c.Context(), id)
	if err != nil {
		return ClusterTagsResponse{}, toHTTPError(err)
	}
	return ClusterTagsResponse{Result: tags}, nil
}

func (k ClusterHandler) GetClusterTag(c fuego.ContextNoBody) (string, error) {
	id, err := uuids.Parse(c.PathParam("id"))
	if err != nil {
		return "", toHTTPError(err)
	}
	value, err := k.clusterService.GetClusterTag(c.Context(), id, c.PathParam("tag"))
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

	id, err := uuids.Parse(c.PathParam("id"))
	if err != nil {
		return nil, toHTTPError(err)
	}
	if err := k.clusterService.SetClusterTag(c.Context(), id, c.PathParam("tag"), createParams.Value); err != nil {
		return nil, toHTTPError(err)
	}

	return nil, nil
}

func (k ClusterHandler) DeleteClusterTag(c fuego.ContextNoBody) (any, error) {
	id, err := uuids.Parse(c.PathParam("id"))
	if err != nil {
		return nil, toHTTPError(err)
	}
	if err := k.clusterService.DeleteClusterTag(c.Context(), id, c.PathParam("tag")); err != nil {
		return nil, toHTTPError(err)
	}

	return nil, nil
}

func (k ClusterHandler) GetAllNodes(c fuego.ContextNoBody) ([]ClusterNodeInfo, error) {
	nodes, err := k.clusterService.GetAllNodes(c.Context())
	if err != nil {
		return nil, toHTTPError(err)
	}

	return selectCollectionFields(toNodesInfo(nodes), c.QueryParam("fields"))
}

func (k ClusterHandler) DeleteClusterNode(c fuego.ContextNoBody) (any, error) {
	clusterID, nodeID, err := parseClusterNodeIDs(c.PathParam("id"), c.PathParam("nodeID"))
	if err != nil {
		return nil, toHTTPError(err)
	}
	if err := k.clusterService.DeleteClusterNode(c.Context(), clusterID, nodeID); err != nil {
		return nil, toHTTPError(err)
	}
	return nil, nil
}

func (k ClusterHandler) SetWorkerReplicas(c fuego.ContextWithBody[ClusterReplicasParams]) (any, error) {
	params, err := c.Body()
	if err != nil {
		return nil, toHTTPError(err)
	}
	id, err := uuids.Parse(c.PathParam("id"))
	if err != nil {
		return nil, toHTTPError(err)
	}
	if err := k.clusterService.SetWorkerReplicas(c.Context(), id, *params.Replicas); err != nil {
		return nil, toHTTPError(err)
	}
	return nil, nil
}

func (k ClusterHandler) SetControlPlaneReplicas(c fuego.ContextWithBody[ClusterReplicasParams]) (any, error) {
	params, err := c.Body()
	if err != nil {
		return nil, toHTTPError(err)
	}
	id, err := uuids.Parse(c.PathParam("id"))
	if err != nil {
		return nil, toHTTPError(err)
	}
	if err := k.clusterService.SetControlPlaneReplicas(c.Context(), id, *params.Replicas); err != nil {
		return nil, toHTTPError(err)
	}
	return nil, nil
}

func (k ClusterHandler) PauseCluster(c fuego.ContextNoBody) (any, error) {
	id, err := uuids.Parse(c.PathParam("id"))
	if err != nil {
		return nil, toHTTPError(err)
	}
	return nil, toHTTPErrorOrNil(k.clusterService.SetClusterPaused(c.Context(), id, true))
}

func (k ClusterHandler) ResumeCluster(c fuego.ContextNoBody) (any, error) {
	id, err := uuids.Parse(c.PathParam("id"))
	if err != nil {
		return nil, toHTTPError(err)
	}
	return nil, toHTTPErrorOrNil(k.clusterService.SetClusterPaused(c.Context(), id, false))
}

func (k ClusterHandler) PauseClusterNode(c fuego.ContextNoBody) (any, error) {
	clusterID, nodeID, err := parseClusterNodeIDs(c.PathParam("id"), c.PathParam("nodeID"))
	if err != nil {
		return nil, toHTTPError(err)
	}
	return nil, toHTTPErrorOrNil(k.clusterService.SetClusterNodePaused(c.Context(), clusterID, nodeID, true))
}

func (k ClusterHandler) ResumeClusterNode(c fuego.ContextNoBody) (any, error) {
	clusterID, nodeID, err := parseClusterNodeIDs(c.PathParam("id"), c.PathParam("nodeID"))
	if err != nil {
		return nil, toHTTPError(err)
	}
	return nil, toHTTPErrorOrNil(k.clusterService.SetClusterNodePaused(c.Context(), clusterID, nodeID, false))
}

func parseClusterNodeIDs(clusterIDValue, nodeIDValue string) (uuid.UUID, uuid.UUID, error) {
	clusterID, err := uuids.Parse(clusterIDValue)
	if err != nil {
		return uuid.Nil, uuid.Nil, err
	}
	nodeID, err := uuids.Parse(nodeIDValue)
	if err != nil {
		return uuid.Nil, uuid.Nil, err
	}
	return clusterID, nodeID, nil
}

func toHTTPErrorOrNil(err error) error {
	if err == nil {
		return nil
	}
	return toHTTPError(err)
}

func (k ClusterHandler) SetClusterDescription(c fuego.ContextWithBody[ClusterDescriptionParams]) (any, error) {
	id, err := uuids.Parse(c.PathParam("id"))
	if err != nil {
		return nil, toHTTPError(err)
	}
	params, err := c.Body()
	if err != nil {
		return nil, toHTTPError(err)
	}
	return nil, toHTTPErrorOrNil(k.clusterService.SetClusterDescription(c.Context(), id, *params.Description))
}
