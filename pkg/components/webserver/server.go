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

func (httpServer HttpServer) Run(clusterService strategies.ForManagingClusters) error {
	return httpServer.newServer(clusterService).Run()
}

// Handler builds the complete HTTP application without opening a network
// listener. It is intended for httptest-based tests and embedding.
func (httpServer HttpServer) Handler(clusterService strategies.ForManagingClusters) http.Handler {
	return httpServer.newServer(clusterService).Mux
}

func (httpServer HttpServer) newServer(clusterService strategies.ForManagingClusters) *fuego.Server {
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

	apiRouter := fuego.Group(server, "/api/kubernetes",
		fuego.OptionTags("Clusters"),
	)
	fuego.Use(apiRouter, middlewares.AuthRequired())

	clusterRouter := fuego.Group(
		apiRouter,
		"/clusters",
	)
	clusterHandler := handlers.NewClusterHandler(clusterService)

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
	fuego.Delete(clusterRouter, "/{id}", clusterHandler.DeleteCluster,
		fuego.OptionDefaultStatusCode(http.StatusNoContent),
		noContentResponse(), notFoundResponse("Cluster not found"), validationResponse(),
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
