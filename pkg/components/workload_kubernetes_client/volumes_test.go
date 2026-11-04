package workload_kubernetes_client

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/vatesfr/vmk-engine/pkg/strategies"

	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	ktesting "k8s.io/client-go/testing"
	"k8s.io/utils/ptr"
)

const (
	claimName    = "data"
	affinityNode = "worker-1"
)

func attachment(name, volume, node string, attached bool) *storagev1.VolumeAttachment {
	return &storagev1.VolumeAttachment{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec:       storagev1.VolumeAttachmentSpec{NodeName: node, Source: storagev1.VolumeAttachmentSource{PersistentVolumeName: ptr.To(volume)}},
		Status:     storagev1.VolumeAttachmentStatus{Attached: attached},
	}
}

func TestGetVolumesMapsXOAndOtherSources(t *testing.T) {
	xo := &corev1.PersistentVolume{
		ObjectMeta: metav1.ObjectMeta{Name: "pvc-xo"},
		Spec: corev1.PersistentVolumeSpec{
			Capacity:                      corev1.ResourceList{corev1.ResourceStorage: resource.MustParse("10Gi")},
			AccessModes:                   []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
			PersistentVolumeReclaimPolicy: corev1.PersistentVolumeReclaimDelete,
			StorageClassName:              "xo",
			VolumeMode:                    ptr.To(corev1.PersistentVolumeFilesystem),
			ClaimRef:                      &corev1.ObjectReference{Namespace: "app", Name: claimName},
			PersistentVolumeSource: corev1.PersistentVolumeSource{CSI: &corev1.CSIPersistentVolumeSource{
				Driver: xoCSIDriverName, VolumeHandle: "volume-id", FSType: "ext4",
				VolumeAttributes: map[string]string{"srId": "sr", "srName": "SR", "poolId": "pool", "poolName": "Pool", "storageType": "shared"},
			}},
		},
		Status: corev1.PersistentVolumeStatus{Phase: corev1.VolumeBound},
	}
	nfs := &corev1.PersistentVolume{
		ObjectMeta: metav1.ObjectMeta{Name: "pv-nfs"},
		Spec: corev1.PersistentVolumeSpec{PersistentVolumeSource: corev1.PersistentVolumeSource{
			NFS: &corev1.NFSVolumeSource{Server: "10.0.0.5", Path: "/exports"},
		}},
	}
	local := &corev1.PersistentVolume{
		ObjectMeta: metav1.ObjectMeta{Name: "pv-local"},
		Spec: corev1.PersistentVolumeSpec{
			PersistentVolumeSource: corev1.PersistentVolumeSource{Local: &corev1.LocalVolumeSource{Path: "/mnt/disk"}},
			NodeAffinity: &corev1.VolumeNodeAffinity{Required: &corev1.NodeSelector{NodeSelectorTerms: []corev1.NodeSelectorTerm{{
				MatchExpressions: []corev1.NodeSelectorRequirement{{Key: "kubernetes.io/hostname", Operator: corev1.NodeSelectorOpIn, Values: []string{affinityNode}}},
			}}}},
		},
	}
	other := &corev1.PersistentVolume{
		ObjectMeta: metav1.ObjectMeta{Name: "pv-rbd"},
		Spec:       corev1.PersistentVolumeSpec{PersistentVolumeSource: corev1.PersistentVolumeSource{RBD: &corev1.RBDPersistentVolumeSource{}}},
	}
	client := fake.NewClientset(xo, nfs, local, other,
		attachment("a1", "pvc-xo", "worker-2", true),
		attachment("a2", "pvc-xo", affinityNode, true),
		attachment("a3", "pv-nfs", affinityNode, false),
	)
	volumes, err := testWorkloadClient(client).GetVolumes(t.Context(), "cluster")
	require.NoError(t, err)
	require.Equal(t, []string{"pv-local", "pv-nfs", "pv-rbd", "pvc-xo"}, []string{volumes[0].Name, volumes[1].Name, volumes[2].Name, volumes[3].Name})

	require.Equal(t, "local", volumes[0].Source.Type)
	require.Equal(t, "/mnt/disk", volumes[0].Source.Local.Path)
	require.Equal(t, []strategies.VolumeNodeSelectorTerm{{MatchExpressions: []strategies.VolumeNodeSelectorRequirement{
		{Key: "kubernetes.io/hostname", Operator: "In", Values: []string{affinityNode}},
	}}}, volumes[0].NodeAffinity)

	require.Equal(t, "nfs", volumes[1].Source.Type)
	require.Equal(t, &strategies.NFSVolumeSource{Server: "10.0.0.5", Path: "/exports"}, volumes[1].Source.NFS)
	require.Empty(t, volumes[1].AttachedNodes, "detached VolumeAttachments are ignored")

	require.Equal(t, strategies.VolumeSource{Type: "rbd"}, volumes[2].Source)

	got := volumes[3]
	require.Equal(t, int64(10*1024*1024*1024), got.CapacityBytes)
	require.Equal(t, "Bound", got.Phase)
	require.Equal(t, "Delete", got.ReclaimPolicy)
	require.Equal(t, []string{"ReadWriteOnce"}, got.AccessModes)
	require.Equal(t, "Filesystem", got.VolumeMode)
	require.Equal(t, &strategies.VolumeClaim{Namespace: "app", Name: claimName}, got.Claim)
	require.Equal(t, "csi", got.Source.Type)
	require.Equal(t, &strategies.XOVolume{SRID: "sr", PoolID: "pool", StorageType: "shared"}, got.Source.CSI.XO)
	require.Equal(t, []strategies.VolumeNode{{NodeName: affinityNode}, {NodeName: "worker-2"}}, got.AttachedNodes)
}

func TestGetVolumesOmitsXOForOtherCSIDrivers(t *testing.T) {
	volume := &corev1.PersistentVolume{
		ObjectMeta: metav1.ObjectMeta{Name: "pv"},
		Spec: corev1.PersistentVolumeSpec{PersistentVolumeSource: corev1.PersistentVolumeSource{
			CSI: &corev1.CSIPersistentVolumeSource{Driver: "nfs.csi.k8s.io", VolumeHandle: "handle"},
		}},
	}
	volumes, err := testWorkloadClient(fake.NewClientset(volume)).GetVolumes(t.Context(), "cluster")
	require.NoError(t, err)
	require.Equal(t, "handle", volumes[0].Source.CSI.VolumeHandle)
	require.Nil(t, volumes[0].Source.CSI.XO)
}

func TestGetVolumesReturnsEmptySliceAndUnavailableErrors(t *testing.T) {
	volumes, err := testWorkloadClient(fake.NewClientset()).GetVolumes(t.Context(), "cluster")
	require.NoError(t, err)
	require.NotNil(t, volumes)
	require.Empty(t, volumes)

	for _, resource := range []string{"persistentvolumes", "volumeattachments"} {
		client := fake.NewClientset()
		client.PrependReactor("list", resource, func(ktesting.Action) (bool, runtime.Object, error) {
			return true, nil, errors.New("offline")
		})
		_, err := testWorkloadClient(client).GetVolumes(t.Context(), "cluster")
		require.ErrorIs(t, err, strategies.ErrWorkloadUnavailable, resource)
	}
}
