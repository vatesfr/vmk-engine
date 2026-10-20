package uuids

import "github.com/gofrs/uuid"

// UUIDIndexKey is the field index key used when registering and querying the
// UUID field indexes on the informer cache.
const UUIDIndexKey = "uuid"

// RootNamespace is the UUID v5 root for all CAPI-proxy resource identifiers.
// It is derived deterministically from the application's DNS name so no
// constant needs to be stored or configured.
var RootNamespace = uuid.NewV5(uuid.NamespaceDNS, "capv-proxy.vates.tech")

// ClusterID returns the stable UUID v5 for a cluster identified by name.
func ClusterID(clusterName string) uuid.UUID {
	return uuid.NewV5(RootNamespace, clusterName)
}

// NodeID returns the stable UUID v5 for a node scoped under its cluster.
// nodeName must be the Kubernetes node name (NodeRef.Name) when the machine
// is bound to a node, otherwise the CAPI Machine name.
func NodeID(clusterName, nodeName string) uuid.UUID {
	return uuid.NewV5(ClusterID(clusterName), nodeName)
}
