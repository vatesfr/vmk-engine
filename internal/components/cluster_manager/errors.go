package cluster_manager

import (
	"errors"
	"fmt"
)

var (
	ErrClusterNameRequired          = errors.New("cluster name is required")
	ErrControlPlaneReplicasRequired = errors.New("control plane replicas is required")
	ErrWorkerReplicasRequired       = errors.New("worker replicas is required")
	ErrControlPlaneIPRequired       = errors.New("control plane IP is required")
	ErrClusterCreationFailed        = errors.New("failed to create cluster")
)

func NewErrClusterCreationFailed(err error) error {
	return fmt.Errorf("%w: %w", ErrClusterCreationFailed, err)
}
