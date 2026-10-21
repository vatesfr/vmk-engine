package uuids

import (
	"errors"
	"fmt"

	"github.com/gofrs/uuid"
)

// UUIDIndexKey is the field index key used when registering and querying the
// UUID field indexes on the informer cache.
const UUIDIndexKey = "uuid"

// ClusterUUIDIndexKey is the field index key used to look up Machines by the
// UUID of the cluster they belong to.
const ClusterUUIDIndexKey = "clusterUUID"

// RootNamespace is the UUID v5 root for all CAPI-proxy resource identifiers.
// It is derived deterministically from the application's DNS name so no
// constant needs to be stored or configured.
var RootNamespace = uuid.NewV5(uuid.NamespaceDNS, "capv-proxy.vates.tech")

// ErrInvalidFormat is returned when a UUID string cannot be parsed. The
// gofrs/uuid sentinel is unexported, so Parse wraps it in this exported one
// for callers to match with errors.Is.
var ErrInvalidFormat = errors.New("invalid UUID format")

// Parse parses a UUID string, returning an error matching ErrInvalidFormat.
func Parse(value string) (uuid.UUID, error) {
	id, err := uuid.FromString(value)
	if err != nil {
		return uuid.Nil, fmt.Errorf("%w: %s", ErrInvalidFormat, value)
	}
	return id, nil
}

// ClusterID returns the stable UUID v5 for a cluster identified by name.
func ClusterID(clusterName string) uuid.UUID {
	if clusterName == "" {
		return uuid.Nil
	}
	return uuid.NewV5(RootNamespace, clusterName)
}

// NodeID returns the stable UUID v5 for a node scoped under its cluster.
// nodeName must be the Kubernetes node name (NodeRef.Name) when the machine
// is bound to a node, otherwise the CAPI Machine name.
func NodeID(clusterName, nodeName string) uuid.UUID {
	if clusterName == "" || nodeName == "" {
		return uuid.Nil
	}
	return uuid.NewV5(ClusterID(clusterName), nodeName)
}
