package webserver

import (
	"net"
	"strconv"

	"capv-proxy/internal/components/webserver/handlers"
	"capv-proxy/internal/components/webserver/middlewares"
	"capv-proxy/internal/strategies"

	"github.com/go-fuego/fuego"
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

	fuego.Get(clusterRouter, "/", clusterHandler.GetClusters)
	fuego.Get(clusterRouter, "/{id}", clusterHandler.GetCluster)
	fuego.Get(clusterRouter, "/{id}/nodes", clusterHandler.GetClusterNodesInfo)
	fuego.Get(clusterRouter, "/{id}/config", clusterHandler.GetClusterConfig)
	fuego.Post(clusterRouter, "/", clusterHandler.CreateCluster)
	fuego.Delete(clusterRouter, "/{id}", clusterHandler.DeleteCluster)

	return server.Run()
}
