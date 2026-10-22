package strategies

import (
	"context"

	"github.com/gofrs/uuid"
)

// ForObservingKubernetesResources supplies immutable, transport-independent
// resource observations. stop must release all registered observers.
type ForObservingKubernetesResources interface {
	ObserveKubernetesResources(ctx context.Context, handle func(KubernetesResourceChange)) (stop func(), err error)
}

const (
	ObservedCluster    = "Cluster"
	ObservedNode       = "Machine"
	ObservedKubeconfig = "Kubeconfig"
)

type KubernetesResourceChange struct {
	Previous *KubernetesResourceObservation
	Current  *KubernetesResourceObservation
	Initial  bool
}

type KubernetesResourceObservation struct {
	ID                  uuid.UUID
	Kind                string
	Namespace           string
	Name                string
	UID                 string
	ClusterName         string
	ClusterUID          string
	Cluster             *ClusterInfo
	Node                *ClusterNodeInfo
	Deleting            bool
	Scaling             *bool
	Upgrade             *UpgradeOperation
	KubeconfigAvailable bool
}
