package strategies

import "github.com/gofrs/uuid"

// MaxClusterDescriptionLength limits descriptions in Unicode code points.
const MaxClusterDescriptionLength = 4000

// CreateClusterParams describes the minimal cluster creation intent.
type CreateClusterParams struct {
	Version              string
	Name                 string
	Description          string
	CreatedByXOUserID    string
	ControlPlaneReplicas int32
	WorkerReplicas       int32
	ControlPlaneIP       string
	VMNamePrefix         string
	Tags                 map[string]string
}

// ClusterCreationDefaults contains VM and bootstrap configuration read from the ConfigMap.
// Credentials remain in provider-managed Secrets.
type ClusterCreationDefaults struct {
	WorkerName                string
	PodCIDR                   string
	ServiceCIDR               string
	ServiceDomain             string
	ControlPlaneKubeadmConfig map[string]any
	ControlPlaneMachineSpec   map[string]any
	WorkerMachineSpec         map[string]any
	WorkerKubeadmConfig       map[string]any
	CNI                       string
	Cilium                    *CiliumConfig
	XOTags                    []string
	EndpointPort              int
	EndpointSubnet            int
	LoadBalancer              string
}

// CiliumConfig configures the provider-installed Cilium addon at cluster creation.
type CiliumConfig struct {
	KubeProxyReplacement bool   `json:"kubeProxyReplacement,omitempty"`
	K8sServiceHost       string `json:"k8sServiceHost,omitempty"`
	K8sServicePort       int32  `json:"k8sServicePort,omitempty"`
}

// CreateClusterResourceParams describes the resources required by the
// cluster resource Strategy.
type CreateClusterResourceParams struct {
	Version              string
	Defaults             ClusterCreationDefaults
	ControlPlaneIP       string
	VMNamePrefix         string
	Name                 string
	Description          string
	CreatedByXOUserID    string
	ControlPlaneReplicas *int32
	MachineDeployments   []MachineDeployment
	Tags                 map[string]string
}

type MachineDeployment struct {
	Name     string
	Replicas *int32
}

// ClusterInfo is the shared representation returned by cluster Strategies.
type ClusterInfo struct {
	IsDeleting                  bool
	IsScaling                   *bool
	IsCordon                    *bool
	IsDraining                  *bool
	IsUpgrading                 bool
	Paused                      bool
	IsAvailable                 bool
	ID                          uuid.UUID
	IsControlPlaneInitialized   *bool
	IsInfrastructureProvisioned *bool
	Name                        string
	Description                 string
	CreatedByXOUserID           string
	Phase                       string
	CreatedAt                   string
	Version                     string
	ControlPlaneEndpoint        string
	APIServerPort               int32
	PodCIDRBlocks               []string
	ServiceCIDRBlocks           []string
	ServiceDomain               string
	ControlPlanesStatus         NodeTypeStatus
	WorkersStatus               NodeTypeStatus
	Tags                        map[string]string
	Volumes                     []WorkloadVolume
}

type NodeTypeStatus struct {
	AvailableReplicas int
	DesiredReplicas   int
	ReadyReplicas     int
	Replicas          int
	UpToDateReplicas  int
	Versions          []VersionNodeStatus
}

type VersionNodeStatus struct {
	Replicas int
	Version  string
}

// UpgradeOperation is persisted with the lock and the resource identities needed
// to resume safely. Its JSON representation is independent of Kubernetes types.
type UpgradeOperation struct {
	ID           string          `json:"id"`
	Version      string          `json:"version"`
	Locked       bool            `json:"locked"`
	StartedAt    string          `json:"started_at"`
	CompletedAt  string          `json:"completed_at,omitempty"`
	Message      string          `json:"message,omitempty"`
	ControlPlane UpgradeTarget   `json:"control_plane"`
	Workers      []UpgradeTarget `json:"workers"`
}

type UpgradeTarget struct {
	Name          string `json:"name"`
	UID           string `json:"uid"`
	Version       string `json:"source_version"`
	BootstrapName string `json:"bootstrap_name,omitempty"`
	BootstrapUID  string `json:"bootstrap_uid,omitempty"`
	SourceHash    string `json:"source_hash"`
	TargetHash    string `json:"target_hash"`
}

// ResourceSnapshot identifies an observed resource and fences stale writes.
type ResourceSnapshot struct {
	Name            string
	UID             string
	ResourceVersion string
}

// UpgradeContext contains only the Cluster and its persisted operation. Reading
// it does not require the control plane, worker deployments or Machines to exist.
type UpgradeContext struct {
	Cluster         ResourceSnapshot
	Operation       *UpgradeOperation
	Paused          bool
	Deleting        bool
	TopologyManaged bool
}

type UpgradeResourceState struct {
	Resource ResourceSnapshot
	Version  string
	Ready    bool
	Paused   bool
	Deleting bool
}

// UpgradeWorkerState adds the rollout policy specific to worker deployments.
type UpgradeWorkerState struct {
	UpgradeResourceState
	RolloutStrategy string
}

type UpgradeMachineState struct {
	Version        string
	KubeletVersion string
	Ready          bool
	Paused         bool
	Deleting       bool
}

// WorkloadVolume describes a PersistentVolume of a workload cluster, whatever
// its provisioner.
type WorkloadVolume struct {
	IsDeleting    bool
	Name          string
	CapacityBytes int64
	Phase         string
	ReclaimPolicy string
	StorageClass  string
	AccessModes   []string
	VolumeMode    string
	CreatedAt     string
	Claim         *VolumeClaim
	Source        VolumeSource
	// AttachedNodes lists Nodes reported as attached by VolumeAttachments.
	// Only CSI volumes use VolumeAttachments.
	AttachedNodes []VolumeNode
	NodeAffinity  []VolumeNodeSelectorTerm
}

type VolumeClaim struct {
	Namespace string
	Name      string
}

// VolumeSource keeps the Kubernetes source field name in Type and details for
// the most common sources.
type VolumeSource struct {
	Type  string
	CSI   *CSIVolumeSource
	NFS   *NFSVolumeSource
	Local *LocalVolumeSource
}

type CSIVolumeSource struct {
	Driver string
	// VolumeHandle is not the VDI UUID for Xen Orchestra volumes: the driver
	// stores it on the VDI as the tag "k8s:volumeId:<VolumeHandle>".
	VolumeHandle string
	// XO is set only for volumes provisioned by the Xen Orchestra CSI driver.
	XO *XOVolume
}

type XOVolume struct {
	SRID        string
	PoolID      string
	StorageType string
}

type NFSVolumeSource struct {
	Server   string
	Path     string
	ReadOnly bool
}

type LocalVolumeSource struct {
	Path string
}

// VolumeNode identifies an attached Node; ID and VMID are resolved from the
// cluster's Machines and stay nil when unknown.
type VolumeNode struct {
	NodeName string
	ID       *uuid.UUID
	VMID     *uuid.UUID
}

// VolumeNodeSelectorTerm terms are ORed; their requirements are ANDed.
type VolumeNodeSelectorTerm struct {
	MatchExpressions []VolumeNodeSelectorRequirement
}

type VolumeNodeSelectorRequirement struct {
	Key      string
	Operator string
	Values   []string
}
