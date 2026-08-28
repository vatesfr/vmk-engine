package handlers

import (
	"capv-proxy/internal/webserver/strategies"

	"github.com/go-fuego/fuego"
)

type ClusterHandler struct {
	clusterService strategies.ForManagingCluster
}

func NewClusterHandler(clusterService strategies.ForManagingCluster) ClusterHandler {
	return ClusterHandler{
		clusterService: clusterService,
	}
}

func (k ClusterHandler) GetClusters(fuego.ContextNoBody) ([]strategies.ClusterInfo, error) {
	return k.clusterService.GetClusters()
}

func (k ClusterHandler) GetCluster(c fuego.ContextNoBody) (strategies.ClusterInfo, error) {
	return k.clusterService.GetCluster(c.PathParam("id"))
}

func (k ClusterHandler) GetClusterNodesInfo(c fuego.ContextNoBody) ([]strategies.ClusterNodeInfo, error) {
	return k.clusterService.GetClusterNodesInfo(c.PathParam("id"))
}

func (k ClusterHandler) GetClusterConfig(c fuego.ContextNoBody) (string, error) {
	return k.clusterService.GetClusterConfig(c.PathParam("id"))
}

func (k ClusterHandler) CreateCluster(c fuego.ContextWithBody[strategies.ClusterCreateParams]) (strategies.ClusterInfo, error) {
	createParams, err := c.Body()
	if err != nil {
		return strategies.ClusterInfo{}, err
	}

	return k.clusterService.CreateCluster(createParams)
}

func (k ClusterHandler) DeleteCluster(c fuego.ContextNoBody) (string, error) {
	if err := k.clusterService.DeleteCluster(c.PathParam("id")); err != nil {
		return "", err
	}

	return "ok", nil
}
