package webserver

import (
	"capv-proxy/internal/webserver/handlers"
	"capv-proxy/internal/webserver/middlewares"
	"capv-proxy/internal/webserver/strategies"

	"github.com/go-fuego/fuego"
)

type HttpServer struct{}

func (HttpServer) Run(clusterService strategies.ForManagingCluster) error {
	server := fuego.NewServer(
		fuego.WithAddr("127.0.0.1:8080"),
		fuego.WithoutAutoGroupTags(),
		fuego.WithEngineOptions(
			fuego.WithOpenAPIConfig(fuego.OpenAPIConfig{
				// 		SpecURL:    "/swagger/openapi.json",
				// 		SwaggerURL: "/swagger",
				// 		UIHandler:  swaggerUIHandler,
				DisableLocalSave: true,
			}),
		),
	)
	server.OpenAPI.Description().Info.Title = "Cluster service"
	server.OpenAPI.Description().Info.Version = "0.1.0"
	server.OpenAPI.Description().Info.Description = "REST API for managing kubernetes clusters."

	fuego.Get(server, "/ping", handlers.PingHandler,
		fuego.OptionTags("Health"),
		fuego.OptionSummary("Vérifier l'état du service"),
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
