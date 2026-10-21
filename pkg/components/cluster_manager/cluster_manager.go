package cluster_manager

import (
	"context"
	"fmt"
	"unicode/utf8"

	"github.com/gofrs/uuid"
	lop "github.com/samber/lo/parallel"

	"capv-proxy/pkg/strategies"

	"k8s.io/apimachinery/pkg/util/validation"
)

type ClusterManager struct {
	workloadNodes           strategies.ForManagingWorkloadNodes
	workloadStates          strategies.ForGettingWorkloadNodeStates
	versions                strategies.ForManagingVersions
	clusterResources        strategies.ForManagingClusterResources
	clusterConfigs          strategies.ForGettingClusterConfig
	clusterMachines         strategies.ForManagingClusterMachines
	clusterCreationDefaults strategies.ForGettingClusterCreationDefaults
}

var _ strategies.ForManagingClusters = ClusterManager{}

func NewClusterManager(
	clusterResources strategies.ForManagingClusterResources,
	clusterConfigs strategies.ForGettingClusterConfig,
	clusterMachines strategies.ForManagingClusterMachines,
	clusterCreationDefaults strategies.ForGettingClusterCreationDefaults,
	versions strategies.ForManagingVersions,
	workloadNodes strategies.ForManagingWorkloadNodes,
	workloadStates strategies.ForGettingWorkloadNodeStates,
) ClusterManager {
	return ClusterManager{
		versions:                versions,
		workloadNodes:           workloadNodes,
		workloadStates:          workloadStates,
		clusterResources:        clusterResources,
		clusterConfigs:          clusterConfigs,
		clusterMachines:         clusterMachines,
		clusterCreationDefaults: clusterCreationDefaults,
	}
}

func (c ClusterManager) CreateCluster(ctx context.Context, params strategies.CreateClusterParams) (strategies.ClusterInfo, error) {
	if params.Name == "" {
		return strategies.ClusterInfo{}, strategies.ErrClusterNameRequired
	}
	if errs := validation.IsDNS1123Label(params.Name); len(errs) > 0 {
		return strategies.ClusterInfo{}, fmt.Errorf("%w %q: %v", strategies.ErrInvalidClusterName, params.Name, errs)
	}

	if utf8.RuneCountInString(params.Description) > strategies.MaxClusterDescriptionLength {
		return strategies.ClusterInfo{}, strategies.ErrClusterDescriptionTooLong
	}

	if params.ControlPlaneIP == "" {
		return strategies.ClusterInfo{}, strategies.ErrControlPlaneIPRequired
	}

	if params.ControlPlaneReplicas <= 0 {
		return strategies.ClusterInfo{}, strategies.ErrControlPlaneReplicasRequired
	}

	if params.WorkerReplicas <= 0 {
		return strategies.ClusterInfo{}, strategies.ErrWorkerReplicasRequired
	}
	if params.Version == "" {
		return strategies.ClusterInfo{}, strategies.ErrClusterVersionRequired
	}
	if !c.versions.IsVersionAvailable(params.Version) {
		return strategies.ClusterInfo{}, fmt.Errorf("%w: %q", strategies.ErrClusterVersionUnavailable, params.Version)
	}
	defaults, err := c.clusterCreationDefaults.GetClusterCreationDefaults(ctx)
	if err != nil {
		return strategies.ClusterInfo{}, fmt.Errorf("%w: %w", strategies.ErrClusterCreationDefaultsRetrievalFailed, err)
	}

	clusterInfo, err := c.clusterResources.Create(ctx, strategies.CreateClusterResourceParams{
		Version:              "v" + params.Version,
		Name:                 params.Name,
		Description:          params.Description,
		CreatedByXOUserID:    params.CreatedByXOUserID,
		Defaults:             defaults,
		ControlPlaneIP:       params.ControlPlaneIP,
		VMNamePrefix:         params.VMNamePrefix,
		ControlPlaneReplicas: &params.ControlPlaneReplicas,
		MachineDeployments:   []strategies.MachineDeployment{{Name: defaults.WorkerName, Replicas: &params.WorkerReplicas}},
		Tags:                 params.Tags,
	})
	if err != nil {
		return strategies.ClusterInfo{}, fmt.Errorf("%w: %w", strategies.ErrClusterCreationFailed, err)
	}

	return clusterInfo, nil
}

func (c ClusterManager) GetClusters(ctx context.Context) ([]strategies.ClusterInfo, error) {
	clusters, err := c.clusterResources.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", strategies.ErrClusterListingFailed, err)
	}
	lop.ForEach(clusters, func(_ strategies.ClusterInfo, i int) {
		c.enrichCluster(ctx, &clusters[i])
	})
	return clusters, nil
}

func (c ClusterManager) GetClusterByUUID(ctx context.Context, id uuid.UUID) (strategies.ClusterInfo, error) {
	cluster, err := c.clusterResources.GetByUUID(ctx, id)
	if err != nil {
		return strategies.ClusterInfo{}, fmt.Errorf("%w: %w", strategies.ErrClusterRetrievalFailed, err)
	}
	c.enrichCluster(ctx, &cluster)
	return cluster, nil
}

func (c ClusterManager) GetClusterNodesInfo(ctx context.Context, id uuid.UUID) ([]strategies.ClusterNodeInfo, error) {
	// Ensure the cluster exists, so an unknown UUID is reported as not found
	// rather than as an empty node list.
	if _, err := c.clusterResources.GetByUUID(ctx, id); err != nil {
		return nil, fmt.Errorf("%w: %w", strategies.ErrClusterRetrievalFailed, err)
	}
	nodes, err := c.clusterMachines.GetMachinesByCluster(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", strategies.ErrClusterNodesRetrievalFailed, err)
	}
	c.enrichNodes(ctx, nodes)
	return nodes, nil
}

func (c ClusterManager) GetAllNodes(ctx context.Context) ([]strategies.ClusterNodeInfo, error) {
	nodes, err := c.clusterMachines.GetAllMachines(ctx)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", strategies.ErrClusterNodesRetrievalFailed, err)
	}
	c.enrichNodes(ctx, nodes)
	return nodes, nil
}

func (c ClusterManager) GetNodeByUUID(ctx context.Context, id uuid.UUID) (strategies.ClusterNodeInfo, error) {
	node, err := c.clusterMachines.GetMachineByUUID(ctx, id)
	if err != nil {
		return strategies.ClusterNodeInfo{}, err
	}
	nodes := []strategies.ClusterNodeInfo{node}
	c.enrichNodes(ctx, nodes)
	return nodes[0], nil
}

func (c ClusterManager) GetClusterConfig(ctx context.Context, id uuid.UUID) (string, error) {
	if err := validateClusterUUID(id); err != nil {
		return "", err
	}
	cluster, err := c.clusterResources.GetByUUID(ctx, id)
	if err != nil {
		return "", fmt.Errorf("%w: %w", strategies.ErrClusterNotFound, err)
	}
	config, err := c.clusterConfigs.Get(ctx, cluster.Name)
	if err != nil {
		return "", fmt.Errorf("%w: %w", strategies.ErrClusterConfigRetrievalFailed, err)
	}
	return config, nil
}

func (c ClusterManager) DeleteCluster(ctx context.Context, id uuid.UUID) error {
	if err := validateClusterUUID(id); err != nil {
		return err
	}
	if err := c.clusterResources.Delete(ctx, id); err != nil {
		return fmt.Errorf("%w: %w", strategies.ErrClusterDeletionFailed, err)
	}
	return nil
}

func (m ClusterManager) GetAllTags(ctx context.Context) (map[string]map[string]string, error) {
	tags, err := m.clusterResources.GetAllTags(ctx)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", strategies.ErrClusterTagsRetrievalFailed, err)
	}
	return tags, nil
}

func (m ClusterManager) GetClusterTags(ctx context.Context, clusterID uuid.UUID) (map[string]string, error) {
	tags, err := m.clusterResources.GetTags(ctx, clusterID)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", strategies.ErrClusterTagsRetrievalFailed, err)
	}
	return tags, nil
}

func (m ClusterManager) GetClusterTag(ctx context.Context, clusterID uuid.UUID, tag string) (string, error) {
	value, err := m.clusterResources.GetTag(ctx, clusterID, tag)
	if err != nil {
		return "", fmt.Errorf("%w: %w", strategies.ErrClusterTagsRetrievalFailed, err)
	}
	return value, nil
}

func (m ClusterManager) SetClusterTag(ctx context.Context, clusterID uuid.UUID, tag, value string) error {
	if err := m.clusterResources.SetTag(ctx, clusterID, tag, value); err != nil {
		return fmt.Errorf("%w: %w", strategies.ErrClusterTagUpdateFailed, err)
	}
	return nil
}

func (m ClusterManager) DeleteClusterTag(ctx context.Context, clusterID uuid.UUID, tag string) error {
	if err := m.clusterResources.DeleteTag(ctx, clusterID, tag); err != nil {
		return fmt.Errorf("%w: %w", strategies.ErrClusterTagDeletionFailed, err)
	}
	return nil
}

func validateClusterUUID(id uuid.UUID) error {
	if id == uuid.Nil {
		return strategies.ErrClusterIDRequired
	}
	return nil
}

func (c ClusterManager) DeleteClusterNode(ctx context.Context, clusterID uuid.UUID, nodeID uuid.UUID) error {
	if err := validateClusterUUID(clusterID); err != nil {
		return err
	}
	if nodeID == uuid.Nil {
		return strategies.ErrNodeIDRequired
	}
	// Ensure the cluster exists, so an unknown UUID is reported as not found
	cluster, err := c.clusterResources.GetByUUID(ctx, clusterID)
	if err != nil {
		return fmt.Errorf("%w: %w", strategies.ErrNodeDeletionFailed, err)
	}
	if cluster.IsUpgrading {
		return strategies.ErrClusterUpgradeConflict
	}
	if err := c.clusterMachines.DeleteMachine(ctx, clusterID, nodeID); err != nil {
		return fmt.Errorf("%w: %w", strategies.ErrNodeDeletionFailed, err)
	}
	return nil
}

func (c ClusterManager) SetClusterPaused(ctx context.Context, id uuid.UUID, paused bool) error {
	if err := validateClusterUUID(id); err != nil {
		return err
	}
	if err := c.clusterResources.SetPaused(ctx, id, paused); err != nil {
		return fmt.Errorf("%w: %w", strategies.ErrPauseUpdateFailed, err)
	}
	return nil
}

func (c ClusterManager) SetClusterNodePaused(ctx context.Context, clusterID, nodeID uuid.UUID, paused bool) error {
	if err := validateClusterUUID(clusterID); err != nil {
		return err
	}
	if nodeID == uuid.Nil {
		return strategies.ErrNodeIDRequired
	}
	cluster, err := c.clusterResources.GetByUUID(ctx, clusterID)
	if err != nil {
		return fmt.Errorf("%w: %w", strategies.ErrPauseUpdateFailed, err)
	}
	if cluster.IsUpgrading {
		return strategies.ErrClusterUpgradeConflict
	}
	if err := c.clusterMachines.SetMachinePaused(ctx, clusterID, nodeID, paused); err != nil {
		return fmt.Errorf("%w: %w", strategies.ErrPauseUpdateFailed, err)
	}
	return nil
}

func (c ClusterManager) SetClusterDescription(ctx context.Context, id uuid.UUID, description string) error {
	if err := validateClusterUUID(id); err != nil {
		return err
	}
	if utf8.RuneCountInString(description) > strategies.MaxClusterDescriptionLength {
		return strategies.ErrClusterDescriptionTooLong
	}
	if err := c.clusterResources.SetDescription(ctx, id, description); err != nil {
		return fmt.Errorf("%w: %w", strategies.ErrClusterDescriptionUpdateFailed, err)
	}
	return nil
}
