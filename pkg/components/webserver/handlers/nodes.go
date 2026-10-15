package handlers

import (
	"time"

	"github.com/go-fuego/fuego"

	"capv-proxy/pkg/strategies"
)

type DrainNodeParams struct {
	TimeoutSeconds     int  `json:"timeout_seconds,omitempty" validate:"min=0,max=1800" description:"Drain deadline in seconds; 0 uses 300 seconds"`
	IgnoreDaemonSets   bool `json:"ignore_daemon_sets,omitempty" description:"Leave DaemonSet pods running"`
	DeleteEmptyDirData bool `json:"delete_empty_dir_data,omitempty" description:"Allow eviction of pods whose emptyDir data will be lost"`
	Force              bool `json:"force,omitempty" description:"Allow eviction of pods without a controller; still respects PDBs"`
}

func (k ClusterHandler) CordonNode(c fuego.ContextNoBody) (any, error) {
	clusterID, nodeID, err := parseClusterNodeIDs(c.PathParam("id"), c.PathParam("nodeID"))
	if err != nil {
		return nil, toHTTPError(err)
	}
	return nil, toHTTPErrorOrNil(k.clusterService.SetClusterNodeCordon(c.Context(), clusterID, nodeID, true))
}

func (k ClusterHandler) UncordonNode(c fuego.ContextNoBody) (any, error) {
	clusterID, nodeID, err := parseClusterNodeIDs(c.PathParam("id"), c.PathParam("nodeID"))
	if err != nil {
		return nil, toHTTPError(err)
	}
	return nil, toHTTPErrorOrNil(k.clusterService.SetClusterNodeCordon(c.Context(), clusterID, nodeID, false))
}

func (k ClusterHandler) DrainNode(c fuego.ContextWithBody[DrainNodeParams]) (any, error) {
	clusterID, nodeID, err := parseClusterNodeIDs(c.PathParam("id"), c.PathParam("nodeID"))
	if err != nil {
		return nil, toHTTPError(err)
	}
	params, err := c.Body()
	if err != nil {
		return nil, toHTTPError(err)
	}
	return nil, toHTTPErrorOrNil(k.clusterService.DrainClusterNode(c.Context(), clusterID, nodeID, strategies.DrainOptions{
		Timeout:            time.Duration(params.TimeoutSeconds) * time.Second,
		IgnoreDaemonSets:   params.IgnoreDaemonSets,
		DeleteEmptyDirData: params.DeleteEmptyDirData,
		Force:              params.Force,
	}))
}
