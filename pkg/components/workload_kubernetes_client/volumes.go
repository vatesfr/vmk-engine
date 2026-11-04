package workload_kubernetes_client

import (
	"context"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"time"

	"github.com/samber/lo"

	"github.com/vatesfr/vmk-engine/pkg/strategies"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Names published by the Xen Orchestra CSI driver in PersistentVolumes.
const (
	xoCSIDriverName          = "csi.xenorchestra.vates.tech"
	xoVolumeAttributeSRID    = "srId"
	xoVolumeAttributePoolID  = "poolId"
	xoVolumeAttributeStorage = "storageType"
)

// GetVolumes lists PersistentVolumes and VolumeAttachments once each.
func (c *WorkloadClient) GetVolumes(ctx context.Context, clusterName string) ([]strategies.WorkloadVolume, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	client, err := c.clientFor(ctx, clusterName)
	if err != nil {
		return nil, err
	}
	volumes, err := client.CoreV1().PersistentVolumes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("%w: %w", strategies.ErrWorkloadUnavailable, err)
	}
	attachments, err := client.StorageV1().VolumeAttachments().List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("%w: %w", strategies.ErrWorkloadUnavailable, err)
	}
	attachedNodes := make(map[string][]string)
	for _, attachment := range attachments.Items {
		volumeName := attachment.Spec.Source.PersistentVolumeName
		if volumeName != nil && attachment.Status.Attached {
			attachedNodes[*volumeName] = append(attachedNodes[*volumeName], attachment.Spec.NodeName)
		}
	}
	result := make([]strategies.WorkloadVolume, 0, len(volumes.Items))
	for _, volume := range volumes.Items {
		info := toWorkloadVolume(volume)
		nodeNames := attachedNodes[volume.Name]
		slices.Sort(nodeNames)
		for _, nodeName := range slices.Compact(nodeNames) {
			info.AttachedNodes = append(info.AttachedNodes, strategies.VolumeNode{NodeName: nodeName})
		}
		result = append(result, info)
	}
	slices.SortFunc(result, func(a, b strategies.WorkloadVolume) int { return strings.Compare(a.Name, b.Name) })
	return result, nil
}

func toWorkloadVolume(volume corev1.PersistentVolume) strategies.WorkloadVolume {
	info := strategies.WorkloadVolume{
		IsDeleting:    volume.DeletionTimestamp != nil,
		Name:          volume.Name,
		Phase:         string(volume.Status.Phase),
		ReclaimPolicy: string(volume.Spec.PersistentVolumeReclaimPolicy),
		StorageClass:  volume.Spec.StorageClassName,
		AccessModes: lo.Map(volume.Spec.AccessModes, func(mode corev1.PersistentVolumeAccessMode, _ int) string {
			return string(mode)
		}),
		CreatedAt: volume.CreationTimestamp.String(),
		Source:    toVolumeSource(volume.Spec.PersistentVolumeSource),
	}
	if capacity, ok := volume.Spec.Capacity[corev1.ResourceStorage]; ok {
		info.CapacityBytes = capacity.Value()
	}
	if volume.Spec.VolumeMode != nil {
		info.VolumeMode = string(*volume.Spec.VolumeMode)
	}
	if claim := volume.Spec.ClaimRef; claim != nil {
		info.Claim = &strategies.VolumeClaim{Namespace: claim.Namespace, Name: claim.Name}
	}
	if affinity := volume.Spec.NodeAffinity; affinity != nil && affinity.Required != nil {
		info.NodeAffinity = lo.Map(affinity.Required.NodeSelectorTerms, func(term corev1.NodeSelectorTerm, _ int) strategies.VolumeNodeSelectorTerm {
			return strategies.VolumeNodeSelectorTerm{MatchExpressions: toSelectorRequirements(term.MatchExpressions)}
		})
	}
	return info
}

func toSelectorRequirements(requirements []corev1.NodeSelectorRequirement) []strategies.VolumeNodeSelectorRequirement {
	return lo.Map(requirements, func(requirement corev1.NodeSelectorRequirement, _ int) strategies.VolumeNodeSelectorRequirement {
		return strategies.VolumeNodeSelectorRequirement{Key: requirement.Key, Operator: string(requirement.Operator), Values: requirement.Values}
	})
}

func toVolumeSource(source corev1.PersistentVolumeSource) strategies.VolumeSource {
	result := strategies.VolumeSource{Type: volumeSourceType(source)}
	switch {
	case source.CSI != nil:
		csi := &strategies.CSIVolumeSource{Driver: source.CSI.Driver, VolumeHandle: source.CSI.VolumeHandle}
		if source.CSI.Driver == xoCSIDriverName {
			attributes := source.CSI.VolumeAttributes
			csi.XO = &strategies.XOVolume{
				SRID:        attributes[xoVolumeAttributeSRID],
				PoolID:      attributes[xoVolumeAttributePoolID],
				StorageType: attributes[xoVolumeAttributeStorage],
			}
		}
		result.CSI = csi
	case source.NFS != nil:
		result.NFS = &strategies.NFSVolumeSource{Server: source.NFS.Server, Path: source.NFS.Path, ReadOnly: source.NFS.ReadOnly}
	case source.Local != nil:
		result.Local = &strategies.LocalVolumeSource{Path: source.Local.Path}
	}
	return result
}

// volumeSourceType returns the JSON name of the populated source field, so
// sources without dedicated details remain identifiable.
func volumeSourceType(source corev1.PersistentVolumeSource) string {
	value := reflect.ValueOf(source)
	for i := range value.NumField() {
		if field := value.Field(i); field.Kind() == reflect.Pointer && !field.IsNil() {
			name, _, _ := strings.Cut(value.Type().Field(i).Tag.Get("json"), ",")
			return name
		}
	}
	return ""
}
