package handlers

import (
	"github.com/go-fuego/fuego"
	"github.com/gofrs/uuid"
	"github.com/samber/lo"

	uuids "github.com/vatesfr/vmk-engine/pkg/helpers/uuids"
	"github.com/vatesfr/vmk-engine/pkg/strategies"
)

type ClusterVolume struct {
	IsDeleting    bool               `json:"is_deleting,omitempty"`
	Name          string             `json:"name,omitempty" description:"PersistentVolume name"`
	CapacityBytes int64              `json:"capacity_bytes,omitempty"`
	Phase         string             `json:"phase,omitempty" description:"PersistentVolume phase: Pending, Available, Bound, Released or Failed"`
	ReclaimPolicy string             `json:"reclaim_policy,omitempty"`
	StorageClass  string             `json:"storage_class,omitempty"`
	AccessModes   []string           `json:"access_modes,omitempty"`
	VolumeMode    string             `json:"volume_mode,omitempty"`
	CreatedAt     string             `json:"created_at,omitempty"`
	Claim         *VolumeClaim       `json:"claim,omitempty" description:"Bound PersistentVolumeClaim"`
	Source        *VolumeSource      `json:"source,omitempty"`
	AttachedNodes []VolumeNode       `json:"attached_nodes,omitempty" description:"Nodes reported as attached by CSI VolumeAttachments"`
	NodeAffinity  []NodeSelectorTerm `json:"node_affinity,omitempty" description:"Required node affinity; terms are ORed, their requirements ANDed"`
}

type VolumeClaim struct {
	Namespace string `json:"namespace,omitempty"`
	Name      string `json:"name,omitempty"`
}

type VolumeSource struct {
	Type  string             `json:"type,omitempty" description:"Kubernetes PersistentVolume source field, for example csi, nfs, local or hostPath"`
	CSI   *CSIVolumeSource   `json:"csi,omitempty"`
	NFS   *NFSVolumeSource   `json:"nfs,omitempty"`
	Local *LocalVolumeSource `json:"local,omitempty"`
}

type CSIVolumeSource struct {
	Driver       string    `json:"driver,omitempty"`
	VolumeHandle string    `json:"volume_handle,omitempty" description:"For Xen Orchestra volumes, not the VDI UUID: the VDI carries the tag k8s:volumeId:<volume_handle>"`
	XO           *XOVolume `json:"xo,omitempty" description:"Set only for volumes of the Xen Orchestra CSI driver"`
}

type XOVolume struct {
	SRID        string `json:"sr_id,omitempty"`
	PoolID      string `json:"pool_id,omitempty"`
	StorageType string `json:"storage_type,omitempty" description:"shared or local"`
}

type NFSVolumeSource struct {
	Server   string `json:"server,omitempty"`
	Path     string `json:"path,omitempty"`
	ReadOnly bool   `json:"read_only,omitempty"`
}

type LocalVolumeSource struct {
	Path string `json:"path,omitempty"`
}

type VolumeNode struct {
	ID       *uuid.UUID `json:"id,omitempty" description:"Node UUID; omitted when no Machine references the Node"`
	NodeName string     `json:"node_name,omitempty"`
	VMID     *uuid.UUID `json:"vm_id,omitempty"`
}

type NodeSelectorTerm struct {
	MatchExpressions []NodeSelectorRequirement `json:"match_expressions,omitempty"`
}

type NodeSelectorRequirement struct {
	Key      string   `json:"key,omitempty"`
	Operator string   `json:"operator,omitempty"`
	Values   []string `json:"values,omitempty"`
}

func (k ClusterHandler) GetClusterVolumes(c fuego.ContextNoBody) ([]ClusterVolume, error) {
	fields := c.QueryParam("fields")
	id, err := uuids.Parse(c.PathParam("id"))
	if err != nil {
		return nil, toHTTPError(err)
	}
	volumes, err := k.clusterService.GetClusterVolumes(c.Context(), id)
	if err != nil {
		return nil, toHTTPError(err)
	}
	return selectCollectionFields(toClusterVolumes(volumes), fields)
}

func toClusterVolumes(volumes []strategies.WorkloadVolume) []ClusterVolume {
	return lo.Map(volumes, func(volume strategies.WorkloadVolume, _ int) ClusterVolume {
		return toClusterVolume(volume)
	})
}

func toClusterVolume(volume strategies.WorkloadVolume) ClusterVolume {
	result := ClusterVolume{
		IsDeleting:    volume.IsDeleting,
		Name:          volume.Name,
		CapacityBytes: volume.CapacityBytes,
		Phase:         volume.Phase,
		ReclaimPolicy: volume.ReclaimPolicy,
		StorageClass:  volume.StorageClass,
		AccessModes:   volume.AccessModes,
		VolumeMode:    volume.VolumeMode,
		CreatedAt:     volume.CreatedAt,
		Source:        toVolumeSource(volume.Source),
		AttachedNodes: lo.Map(volume.AttachedNodes, func(node strategies.VolumeNode, _ int) VolumeNode {
			return VolumeNode{ID: node.ID, NodeName: node.NodeName, VMID: node.VMID}
		}),
		NodeAffinity: lo.Map(volume.NodeAffinity, func(term strategies.VolumeNodeSelectorTerm, _ int) NodeSelectorTerm {
			return NodeSelectorTerm{MatchExpressions: toSelectorRequirements(term.MatchExpressions)}
		}),
	}
	if volume.Claim != nil {
		result.Claim = &VolumeClaim{Namespace: volume.Claim.Namespace, Name: volume.Claim.Name}
	}
	return result
}

func toSelectorRequirements(requirements []strategies.VolumeNodeSelectorRequirement) []NodeSelectorRequirement {
	return lo.Map(requirements, func(requirement strategies.VolumeNodeSelectorRequirement, _ int) NodeSelectorRequirement {
		return NodeSelectorRequirement{Key: requirement.Key, Operator: requirement.Operator, Values: requirement.Values}
	})
}

func toVolumeSource(source strategies.VolumeSource) *VolumeSource {
	result := &VolumeSource{Type: source.Type}
	if csi := source.CSI; csi != nil {
		result.CSI = &CSIVolumeSource{Driver: csi.Driver, VolumeHandle: csi.VolumeHandle}
		if xo := csi.XO; xo != nil {
			result.CSI.XO = &XOVolume{SRID: xo.SRID, PoolID: xo.PoolID, StorageType: xo.StorageType}
		}
	}
	if nfs := source.NFS; nfs != nil {
		result.NFS = &NFSVolumeSource{Server: nfs.Server, Path: nfs.Path, ReadOnly: nfs.ReadOnly}
	}
	if local := source.Local; local != nil {
		result.Local = &LocalVolumeSource{Path: local.Path}
	}
	return result
}
