package strategies

import (
	"errors"
)

// Errors exposed by ForManagingClusters. They belong to the application
// contract and can therefore be interpreted without depending on a concrete
// implementation such as cluster_manager.
var (
	ErrClusterNameRequired          = errors.New("cluster name is required")
	ErrControlPlaneReplicasRequired = errors.New("control plane replicas is required")
	ErrWorkerReplicasRequired       = errors.New("worker replicas is required")
	ErrControlPlaneIPRequired       = errors.New("control plane IP is required")
	ErrClusterCreationFailed        = errors.New("failed to create cluster")
)
