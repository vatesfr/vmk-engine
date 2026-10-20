package cluster_manager

import (
	"context"
	"fmt"
	"unicode/utf8"

	"github.com/gofrs/uuid"

	"capv-proxy/pkg/strategies"

	"k8s.io/apimachinery/pkg/util/validation"
)

type ClusterManager struct {
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
) ClusterManager {
	return ClusterManager{
		versions:                versions,
		clusterResources:        clusterResources,
		clusterConfigs:          clusterConfigs,
		clusterMachines:         clusterMachines,
		clusterCreationDefaults: clusterCreationDefaults,
	}
}

func (c ClusterManager) CreateCluster(ctx context.Context, params strategies.CreateClusterParams) (strategies.ClusterInfo, error) {
	if err := validateClusterName(params.Name); err != nil {
		return strategies.ClusterInfo{}, err
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
	return clusters, nil
}

func (c ClusterManager) GetCluster(ctx context.Context, name string) (strategies.ClusterInfo, error) {
	if err := validateClusterName(name); err != nil {
		return strategies.ClusterInfo{}, err
	}
	cluster, err := c.clusterResources.Get(ctx, name)
	if err != nil {
		return strategies.ClusterInfo{}, fmt.Errorf("%w: %w", strategies.ErrClusterRetrievalFailed, err)
	}
	return cluster, nil
}

func (c ClusterManager) GetClusterByUUID(ctx context.Context, id uuid.UUID) (strategies.ClusterInfo, error) {
	cluster, err := c.clusterResources.GetByUUID(ctx, id)
	if err != nil {
		return strategies.ClusterInfo{}, fmt.Errorf("%w: %w", strategies.ErrClusterRetrievalFailed, err)
	}
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
	return nodes, nil
}

func (c ClusterManager) GetAllNodes(ctx context.Context) ([]strategies.ClusterNodeInfo, error) {
	nodes, err := c.clusterMachines.GetAllMachines(ctx)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", strategies.ErrClusterNodesRetrievalFailed, err)
	}
	return nodes, nil
}

func (c ClusterManager) GetNodeByUUID(ctx context.Context, id uuid.UUID) (strategies.ClusterNodeInfo, error) {
	node, err := c.clusterMachines.GetMachineByUUID(ctx, id)
	if err != nil {
		return strategies.ClusterNodeInfo{}, err
	}
	return node, nil
}

func (c ClusterManager) GetClusterConfig(ctx context.Context, name string) (string, error) {
	if err := validateClusterName(name); err != nil {
		return "", err
	}
	config, err := c.clusterConfigs.Get(ctx, name)
	if err != nil {
		return "", fmt.Errorf("%w: %w", strategies.ErrClusterConfigRetrievalFailed, err)
	}
	return config, nil
}

func (c ClusterManager) DeleteCluster(ctx context.Context, name string) error {
	if err := validateClusterName(name); err != nil {
		return err
	}
	if err := c.clusterResources.Delete(ctx, name); err != nil {
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

func (m ClusterManager) GetClusterTags(ctx context.Context, clusterName string) (map[string]string, error) {
	tags, err := m.clusterResources.GetTags(ctx, clusterName)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", strategies.ErrClusterTagsRetrievalFailed, err)
	}
	return tags, nil
}

func (m ClusterManager) GetClusterTag(ctx context.Context, clusterName, tag string) (string, error) {
	value, err := m.clusterResources.GetTag(ctx, clusterName, tag)
	if err != nil {
		return "", fmt.Errorf("%w: %w", strategies.ErrClusterTagsRetrievalFailed, err)
	}
	return value, nil
}

func (m ClusterManager) SetClusterTag(ctx context.Context, clusterName, tag, value string) error {
	if err := m.clusterResources.SetTag(ctx, clusterName, tag, value); err != nil {
		return fmt.Errorf("%w: %w", strategies.ErrClusterTagUpdateFailed, err)
	}
	return nil
}

func (m ClusterManager) DeleteClusterTag(ctx context.Context, clusterName, tag string) error {
	if err := m.clusterResources.DeleteTag(ctx, clusterName, tag); err != nil {
		return fmt.Errorf("%w: %w", strategies.ErrClusterTagDeletionFailed, err)
	}
	return nil
}

func validateClusterName(name string) error {
	if name == "" {
		return strategies.ErrClusterNameRequired
	}
	return nil
}

func (c ClusterManager) DeleteClusterNode(ctx context.Context, clusterName, nodeName string) error {
	if err := validateClusterName(clusterName); err != nil {
		return err
	}
	if nodeName == "" {
		return strategies.ErrNodeNameRequired
	}
	if _, err := c.clusterResources.Get(ctx, clusterName); err != nil {
		return fmt.Errorf("%w: %w", strategies.ErrNodeDeletionFailed, err)
	}
	if err := c.clusterMachines.DeleteMachine(ctx, clusterName, nodeName); err != nil {
		return fmt.Errorf("%w: %w", strategies.ErrNodeDeletionFailed, err)
	}
	return nil
}

func (c ClusterManager) SetClusterPaused(ctx context.Context, name string, paused bool) error {
	if err := validateClusterName(name); err != nil {
		return err
	}
	if err := c.clusterResources.SetPaused(ctx, name, paused); err != nil {
		return fmt.Errorf("%w: %w", strategies.ErrPauseUpdateFailed, err)
	}
	return nil
}

func (c ClusterManager) SetClusterNodePaused(ctx context.Context, clusterName, machineName string, paused bool) error {
	if err := validateClusterName(clusterName); err != nil {
		return err
	}
	if machineName == "" {
		return strategies.ErrNodeNameRequired
	}
	if _, err := c.clusterResources.Get(ctx, clusterName); err != nil {
		return fmt.Errorf("%w: %w", strategies.ErrPauseUpdateFailed, err)
	}
	if err := c.clusterMachines.SetMachinePaused(ctx, clusterName, machineName, paused); err != nil {
		return fmt.Errorf("%w: %w", strategies.ErrPauseUpdateFailed, err)
	}
	return nil
}

func (c ClusterManager) SetClusterDescription(ctx context.Context, name, description string) error {
	if err := validateClusterName(name); err != nil {
		return err
	}
	if utf8.RuneCountInString(description) > strategies.MaxClusterDescriptionLength {
		return strategies.ErrClusterDescriptionTooLong
	}
	if err := c.clusterResources.SetDescription(ctx, name, description); err != nil {
		return fmt.Errorf("%w: %w", strategies.ErrClusterDescriptionUpdateFailed, err)
	}
	return nil
}
