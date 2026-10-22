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

func (httpServer HttpServer) Run(
	clusterService strategies.ForManagingClusters, clusterUpgrader strategies.ForUpgradingClusters,
	versionService strategies.ForManagingVersions, eventService strategies.ForWatchingKubernetesEvents,
) error {
	return httpServer.newServer(clusterService, clusterUpgrader, versionService, eventService).Run()
}

// Handler builds the complete HTTP application without opening a network
// listener. It is intended for httptest-based tests and embedding.
func (httpServer HttpServer) Handler(
	clusterService strategies.ForManagingClusters, clusterUpgrader strategies.ForUpgradingClusters,
	versionService strategies.ForManagingVersions, eventService strategies.ForWatchingKubernetesEvents,
) http.Handler {
	return httpServer.newServer(clusterService, clusterUpgrader, versionService, eventService).Mux
}

func (httpServer HttpServer) newServer(
	clusterService strategies.ForManagingClusters, clusterUpgrader strategies.ForUpgradingClusters,
	versionService strategies.ForManagingVersions, eventService strategies.ForWatchingKubernetesEvents,
) *fuego.Server {
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
	// Fuego's default is an absolute 30-second write deadline, which would
	// terminate SSE connections even when heartbeats are being sent.
	server.WriteTimeout = 0

	// Fuego's logging writer does not expose Unwrap/SetWriteDeadline. Keep
	// streaming on the original writer so HTTP deadlines and flush errors work.
	// Health checks also avoid the request/response logging middleware.
	unwrappedServer := fuego.NewServer(fuego.WithLoggingMiddleware(fuego.LoggingConfig{
		DisableRequest:  true,
		DisableResponse: true,
	}))
	unwrappedServer.Mux = server.Mux
	unwrappedServer.Engine = server.Engine
	fuego.Get(unwrappedServer, "/ping", handlers.HealthCheck,
		fuego.OptionTags("Health"),
	)

	apiRouter := fuego.Group(server, "/kubernetes",
		fuego.OptionTags("Clusters"),
	)
	fuego.Use(apiRouter, middlewares.AuthRequired())
	eventsRouter := fuego.Group(unwrappedServer, "/kubernetes", fuego.OptionTags("Clusters"))
	fuego.Use(eventsRouter, middlewares.AuthRequired())

	clusterRouter := fuego.Group(
		apiRouter,
		"/clusters",
	)
	clusterHandler := handlers.NewClusterHandler(clusterService, clusterUpgrader)
	versionsHandler := handlers.NewVersionsHandler(versionService)
	eventsHandler := handlers.NewKubernetesEventsHandler(eventService)

	fuego.Get(clusterRouter, "", clusterHandler.GetClusters,
		fuego.OptionQuery("fields", "Comma-separated JSON fields to return; use * for all fields"),
	)
	fuego.Get(clusterRouter, "/{id}", clusterHandler.GetCluster, notFoundResponse("Cluster not found"), validationResponse())
	fuego.Post(clusterRouter, "/{id}/upgrades", clusterHandler.UpgradeCluster,
		fuego.OptionDescription("Starts a durable rolling upgrade for this cluster. Locks mutations until all machines converge. "+
			"Repeating the active target returns the same operation."),
		fuego.OptionDefaultStatusCode(http.StatusAccepted),
		decodingResponse(), validationResponse(), notFoundResponse("Cluster not found"),
		fuego.OptionAddResponse(http.StatusConflict, "Cluster is locked or not ready for upgrade", fuego.Response{Type: fuego.HTTPError{}}),
	)
	fuego.GetStd(eventsRouter, "/events", eventsHandler.GetKubernetesEvents,
		fuego.OptionSummary("Watch Kubernetes events"),
		fuego.OptionDescription("Streams Kubernetes business transitions and a ping every five seconds. No event IDs or replay; reload REST state after reconnecting."),
		func(route *fuego.BaseRoute) {
			route.Operation.AddResponse(http.StatusOK, openapi3.NewResponse().
				WithDescription("Kubernetes SSE stream").
				WithContent(openapi3.Content{
					"text/event-stream": &openapi3.MediaType{Schema: &openapi3.SchemaRef{Value: openapi3.NewStringSchema()}},
				}))
		},
	)
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
		fuego.OptionAddResponse(http.StatusConflict, "Cluster is locked or changed during deletion", fuego.Response{Type: fuego.HTTPError{}}),
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

	fuego.Post(clusterRouter, "/{id}/nodes/{nodeID}/cordon", clusterHandler.CordonNode, nodeOperationOptions("Cordon the Kubernetes Node resolved from the CAPI Machine UUID")...)
	fuego.Post(clusterRouter, "/{id}/nodes/{nodeID}/uncordon", clusterHandler.UncordonNode, nodeOperationOptions("Uncordon the Kubernetes Node resolved from the CAPI Machine UUID")...)
	fuego.Post(clusterRouter, "/{id}/nodes/{nodeID}/drain", clusterHandler.DrainNode,
		append(nodeOperationOptions("Cordon and drain the Node, respecting PDBs. Waits until completion. "+
			"The Node remains cordoned after success or failure. Send {} for default options."), decodingResponse())...,
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

	fuego.Get(apiRouter, "/nodes", clusterHandler.GetAllNodes,
		fuego.OptionQuery("fields", "Comma-separated JSON fields to return; use * for all fields"),
	)
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

func nodeOperationOptions(description string) []fuego.RouteOption {
	return []fuego.RouteOption{
		fuego.OptionDescription(description),
		fuego.OptionDefaultStatusCode(http.StatusNoContent),
		noContentResponse(), notFoundResponse("Cluster, Machine or Kubernetes Node not found"), validationResponse(),
		fuego.OptionAddResponse(http.StatusConflict, "Node not registered, concurrent change or drain blocked; node may remain cordoned", fuego.Response{Type: fuego.HTTPError{}}),
		fuego.OptionAddResponse(http.StatusServiceUnavailable, "Workload cluster or kubeconfig unavailable", fuego.Response{Type: fuego.HTTPError{}}),
		fuego.OptionAddResponse(http.StatusGatewayTimeout, "Drain timed out; node remains cordoned", fuego.Response{Type: fuego.HTTPError{}}),
	}
}
