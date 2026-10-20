package webserver

import (
	"net"
	"net/http"
	"strconv"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/go-fuego/fuego"

	"capv-proxy/pkg/components/webserver/handlers"
	"capv-proxy/pkg/components/webserver/middlewares"
	"capv-proxy/pkg/strategies"
)

const (
	SwaggerUISwagger   = "swagger"
	SwaggerUIStoplight = "stoplight"
	SwaggerUIDisabled  = "disabled"
)

type HttpServer struct {
	Address   string
	Port      uint16
	SwaggerUI string
}

func IsSwaggerUI(value string) bool {
	return value == SwaggerUISwagger || value == SwaggerUIStoplight || value == SwaggerUIDisabled
}

func (httpServer HttpServer) Run(clusterService strategies.ForManagingClusters, versionService strategies.ForManagingVersions) error {
	return httpServer.newServer(clusterService, versionService).Run()
}

// Handler builds the complete HTTP application without opening a network
// listener. It is intended for httptest-based tests and embedding.
func (httpServer HttpServer) Handler(clusterService strategies.ForManagingClusters, versionService strategies.ForManagingVersions) http.Handler {
	return httpServer.newServer(clusterService, versionService).Mux
}

func (httpServer HttpServer) newServer(clusterService strategies.ForManagingClusters, versionService strategies.ForManagingVersions) *fuego.Server {
	openAPIConfig := fuego.OpenAPIConfig{
		DisableLocalSave:     true,
		DisableDefaultServer: true,
	}
	switch httpServer.SwaggerUI {
	case SwaggerUISwagger:
		openAPIConfig.UIHandler = swaggerUIHandler
	case SwaggerUIDisabled:
		openAPIConfig.DisableSwaggerUI = true
	}

	server := fuego.NewServer(
		fuego.WithAddr(net.JoinHostPort(httpServer.Address, strconv.Itoa(int(httpServer.Port)))),
		fuego.WithoutAutoGroupTags(),
		fuego.WithEngineOptions(
			fuego.WithOpenAPIConfig(openAPIConfig),
		),
	)
	server.OpenAPI.Description().Info.Title = "Cluster service"
	server.OpenAPI.Description().Info.Version = "0.1.0"
	server.OpenAPI.Description().Info.Description = "REST API for managing kubernetes clusters."

	fuego.Get(server, "/ping", handlers.HealthCheck,
		fuego.OptionTags("Health"),
	)

	apiRouter := fuego.Group(server, "/kubernetes",
		fuego.OptionTags("Clusters"),
	)
	fuego.Use(apiRouter, middlewares.AuthRequired())

	clusterRouter := fuego.Group(
		apiRouter,
		"/clusters",
	)
	clusterHandler := handlers.NewClusterHandler(clusterService)
	versionsHandler := handlers.NewVersionsHandler(versionService)

	fuego.Get(clusterRouter, "", clusterHandler.GetClusters,
		fuego.OptionQuery("fields", "Comma-separated JSON fields to return; use * for all fields"),
	)
	fuego.Get(clusterRouter, "/{id}", clusterHandler.GetCluster, notFoundResponse("Cluster not found"), validationResponse())
	fuego.Get(clusterRouter, "/{id}/nodes", clusterHandler.GetClusterNodesInfo,
		fuego.OptionQuery("fields", "Comma-separated JSON fields to return; use * for all fields"),
		validationResponse(),
	)
	fuego.Get(clusterRouter, "/{id}/config", clusterHandler.GetClusterConfig, notFoundResponse("Cluster not found"), validationResponse())
	fuego.Post(clusterRouter, "", clusterHandler.CreateCluster,
		fuego.OptionAddResponse(http.StatusConflict, "Cluster already exists", fuego.Response{Type: fuego.HTTPError{}}),
		decodingResponse(), validationResponse(),
	)

	fuego.Put(clusterRouter, "/{id}/description", clusterHandler.SetClusterDescription,
		fuego.OptionDescription("Replaces the cluster description. An empty string clears it. Maximum 4000 Unicode code points."),
		fuego.OptionDefaultStatusCode(http.StatusNoContent),
		noContentResponse(), notFoundResponse("Cluster not found"), decodingResponse(), validationResponse(),
		fuego.OptionAddResponse(http.StatusConflict, "Cluster changed during description update; retry", fuego.Response{Type: fuego.HTTPError{}}),
	)

	fuego.Put(clusterRouter, "/{id}/workers/replicas", clusterHandler.SetWorkerReplicas,
		fuego.OptionDescription("Sets spec.replicas on the cluster’s single MachineDeployment. A 204 confirms the desired count was saved; machine reconciliation is asynchronous."),
		fuego.OptionDefaultStatusCode(http.StatusNoContent),
		noContentResponse(), notFoundResponse("Cluster not found"), decodingResponse(), validationResponse(),
		fuego.OptionAddResponse(http.StatusConflict, "Cluster state, configuration or concurrent update prevents scaling", fuego.Response{Type: fuego.HTTPError{}}),
	)
	fuego.Put(clusterRouter, "/{id}/control-plane/replicas", clusterHandler.SetControlPlaneReplicas,
		fuego.OptionDescription("Sets desired KubeadmControlPlane replicas. Stacked etcd requires an odd count. A 204 confirms the write; machine reconciliation is asynchronous."),
		fuego.OptionDefaultStatusCode(http.StatusNoContent),
		noContentResponse(), notFoundResponse("Cluster not found"), decodingResponse(), validationResponse(),
		fuego.OptionAddResponse(http.StatusConflict, "Cluster state, configuration or concurrent update prevents scaling", fuego.Response{Type: fuego.HTTPError{}}),
	)
	fuego.Delete(clusterRouter, "/{id}", clusterHandler.DeleteCluster,
		fuego.OptionDefaultStatusCode(http.StatusNoContent),
		noContentResponse(), notFoundResponse("Cluster not found"), validationResponse(),
	)

	fuego.Delete(clusterRouter, "/{id}/nodes/{nodeID}", clusterHandler.DeleteClusterNode,
		fuego.OptionAddResponse(http.StatusConflict, "Node changed during deletion", fuego.Response{Type: fuego.HTTPError{}}),
		fuego.OptionDefaultStatusCode(http.StatusNoContent),
		noContentResponse(), notFoundResponse("Cluster or node not found"), validationResponse(),
	)

	fuego.Post(clusterRouter, "/{id}/reconciliation/pause", clusterHandler.PauseCluster,
		pauseRouteOptions("Pause cluster reconciliation")...,
	)
	fuego.Post(clusterRouter, "/{id}/reconciliation/resume", clusterHandler.ResumeCluster,
		pauseRouteOptions("Resume cluster reconciliation")...,
	)
	fuego.Post(clusterRouter, "/{id}/nodes/{nodeID}/reconciliation/pause", clusterHandler.PauseClusterNode,
		pauseRouteOptions("Pause Machine reconciliation by node UUID")...,
	)
	fuego.Post(clusterRouter, "/{id}/nodes/{nodeID}/reconciliation/resume", clusterHandler.ResumeClusterNode,
		pauseRouteOptions("Remove the Machine pause annotation; the parent cluster may remain paused")...,
	)

	fuego.Get(clusterRouter, "/{id}/tags", clusterHandler.GetClusterTags, notFoundResponse("Cluster not found"))
	fuego.Get(clusterRouter, "/{id}/tags/{tag}", clusterHandler.GetClusterTag, notFoundResponse("Cluster or tag not found"), validationResponse())
	fuego.Put(clusterRouter, "/{id}/tags/{tag}", clusterHandler.CreateOrUpdateClusterTag,
		fuego.OptionDefaultStatusCode(http.StatusNoContent),
		noContentResponse(), notFoundResponse("Cluster not found"), decodingResponse(), validationResponse(),
	)
	fuego.Delete(clusterRouter, "/{id}/tags/{tag}", clusterHandler.DeleteClusterTag,
		fuego.OptionDefaultStatusCode(http.StatusNoContent),
		noContentResponse(), notFoundResponse("Cluster or tag not found"), validationResponse(),
	)

	fuego.Get(apiRouter, "/nodes", clusterHandler.GetAllNodes)
	fuego.Get(apiRouter, "/nodes/{id}", clusterHandler.GetNode, notFoundResponse("Node not found"))
	fuego.Get(apiRouter, "/tags", clusterHandler.GetAllTags)
	fuego.Get(apiRouter, "/versions-available", versionsHandler.GetAvailableVersion)

	return server
}

func validationResponse() fuego.RouteOption {
	return fuego.OptionAddResponse(http.StatusUnprocessableEntity, "Invalid cluster parameters or request body", fuego.Response{Type: fuego.HTTPError{}})
}

func decodingResponse() fuego.RouteOption {
	return fuego.OptionAddResponse(http.StatusBadRequest, "Cannot decode request body (malformed input, incompatible type, or unknown field)", fuego.Response{Type: fuego.HTTPError{}})
}

func notFoundResponse(description string) fuego.RouteOption {
	return fuego.OptionAddResponse(http.StatusNotFound, description, fuego.Response{Type: fuego.HTTPError{}})
}

func noContentResponse() fuego.RouteOption {
	return func(r *fuego.BaseRoute) {
		// An empty content map prevents Fuego from generating a response body schema.
		r.Operation.AddResponse(http.StatusNoContent, openapi3.NewResponse().
			WithDescription(http.StatusText(http.StatusNoContent)).
			WithContent(openapi3.Content{}))
	}
}

func pauseRouteOptions(description string) []fuego.RouteOption {
	return []fuego.RouteOption{
		fuego.OptionDescription(description + ". Only affects reconciliation by Cluster API controllers; does not suspend VMs or running workloads."),
		fuego.OptionDefaultStatusCode(http.StatusNoContent),
		noContentResponse(), notFoundResponse("Cluster or node not found"), validationResponse(),
		fuego.OptionAddResponse(http.StatusConflict, "Resource changed during pause update; retry", fuego.Response{Type: fuego.HTTPError{}}),
	}
}
