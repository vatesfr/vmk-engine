package strategies

import (
	"errors"
)

// Errors exposed by ForManagingClusters. They belong to the application
// contract and can therefore be interpreted without depending on a concrete
// implementation such as cluster_manager.
var (
	ErrClusterDescriptionTooLong              = errors.New("cluster description must not exceed 4000 characters")
	ErrClusterNameRequired                    = errors.New("cluster name is required")
	ErrControlPlaneReplicasRequired           = errors.New("control plane replicas is required")
	ErrWorkerReplicasRequired                 = errors.New("worker replicas is required")
	ErrControlPlaneIPRequired                 = errors.New("control plane IP is required")
	ErrClusterNamespaceRequired               = errors.New("cluster namespace is required")
	ErrClusterNotFound                        = errors.New("cluster not found")
	ErrClusterAlreadyExists                   = errors.New("cluster already exists")
	ErrClusterCreationFailed                  = errors.New("failed to create cluster")
	ErrClusterCreationDefaultsRetrievalFailed = errors.New("failed to get cluster creation defaults")
	ErrClusterListingFailed                   = errors.New("failed to list clusters")
	ErrClusterRetrievalFailed                 = errors.New("failed to get cluster")
	ErrClusterNodesRetrievalFailed            = errors.New("failed to get cluster nodes")
	ErrClusterConfigRetrievalFailed           = errors.New("failed to get cluster config")
	ErrClusterDeletionFailed                  = errors.New("failed to delete cluster")
	ErrClusterTagNameRequired                 = errors.New("cluster tag name is required")
	ErrInvalidClusterTagName                  = errors.New("invalid cluster tag name")
	ErrInvalidClusterTagValue                 = errors.New("invalid cluster tag value")
	ErrClusterTagNotFound                     = errors.New("cluster tag not found")
	ErrClusterTagsRetrievalFailed             = errors.New("failed to get cluster tags")
	ErrClusterTagUpdateFailed                 = errors.New("failed to update cluster tag")
	ErrClusterTagDeletionFailed               = errors.New("failed to delete cluster tag")
)
