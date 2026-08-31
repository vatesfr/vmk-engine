package handlers

import "github.com/go-fuego/fuego"

type PingResponse struct {
	Message string `json:"message"`
}

func HealthCheck(fuego.ContextNoBody) (PingResponse, error) {
	return PingResponse{Message: "pong"}, nil
}
