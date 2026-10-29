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

// RootNamespace is the UUID v5 root for all VMK Engine resource identifiers.
// It is derived deterministically from the application's DNS name so no
// constant needs to be stored or configured.
var RootNamespace = uuid.NewV5(uuid.NamespaceDNS, "vmk-engine.vates.tech")

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
// machineName is the CAPI Machine resource name (metadata.name), never the
// registered Kubernetes Node name from status.nodeRef: the Machine name is
// assigned at creation and unique per namespace, so the ID never changes when
// the Node joins the cluster and two Machines can never share it.
func NodeID(clusterName, machineName string) uuid.UUID {
	if clusterName == "" || machineName == "" {
		return uuid.Nil
	}
	return uuid.NewV5(ClusterID(clusterName), machineName)
}
