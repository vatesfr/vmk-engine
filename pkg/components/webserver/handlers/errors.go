package handlers

import (
	"errors"

	"capv-proxy/pkg/strategies"

	"github.com/go-fuego/fuego"
)

// toHTTPError translates application errors at the HTTP boundary. Application
// services remain independent from transport concerns and HTTP status codes.
func toHTTPError(err error) error {
	var httpError fuego.ErrorWithStatus
	if errors.As(err, &httpError) {
		return err
	}

	switch {
	case errors.Is(err, strategies.ErrClusterNameRequired),
		errors.Is(err, strategies.ErrControlPlaneReplicasRequired),
		errors.Is(err, strategies.ErrWorkerReplicasRequired),
		errors.Is(err, strategies.ErrControlPlaneIPRequired):
		return fuego.BadRequestError{
			Err:    err,
			Title:  "Invalid cluster parameters",
			Detail: err.Error(),
		}
	case errors.Is(err, strategies.ErrClusterNotFound):
		return fuego.NotFoundError{
			Err:    err,
			Title:  "Cluster not found",
			Detail: strategies.ErrClusterNotFound.Error(),
		}
	case errors.Is(err, strategies.ErrClusterAlreadyExists):
		return fuego.ConflictError{
			Err:    err,
			Title:  "Cluster already exists",
			Detail: strategies.ErrClusterAlreadyExists.Error(),
		}
	default:
		// Keep the technical cause for logs but do not expose it to clients.
		return fuego.InternalServerError{
			Err:   err,
			Title: "Internal Server Error",
		}
	}
}
