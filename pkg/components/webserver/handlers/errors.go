package handlers

import (
	"errors"
	"net/http"
	"strings"

	"github.com/go-fuego/fuego"
	"github.com/go-playground/validator/v10"

	"capv-proxy/pkg/strategies"
)

// toHTTPError translates application errors at the HTTP boundary. Application
// services remain independent from transport concerns and HTTP status codes.
func toHTTPError(err error) error {
	if err == nil {
		return nil
	}

	var validationErrors validator.ValidationErrors
	if errors.As(err, &validationErrors) {
		var problem fuego.HTTPError
		if !errors.As(err, &problem) {
			var problemPointer *fuego.HTTPError
			if errors.As(err, &problemPointer) && problemPointer != nil {
				problem = *problemPointer
			} else {
				problem.Title = "Validation Error"
			}
		}
		problem.Err = err
		problem.Status = http.StatusUnprocessableEntity
		return problem
	}

	if strings.Contains(err.Error(), "uuid:") || strings.Contains(err.Error(), "invalid UUID") || strings.Contains(err.Error(), "incorrect UUID") {
		return fuego.HTTPError{
			Err:    err,
			Status: http.StatusBadRequest,
			Title:  "Invalid UUID",
			Detail: "path parameter 'id' must be a valid UUID",
		}
	}

	var httpError fuego.ErrorWithStatus
	if errors.As(err, &httpError) {
		return err
	}

	switch {
	case errors.Is(err, strategies.ErrReplicasRejected):
		return fuego.HTTPError{Err: err, Status: http.StatusUnprocessableEntity, Title: "Invalid cluster parameters", Detail: strategies.ErrReplicasRejected.Error()}
	case errors.Is(err, strategies.ErrInvalidReplicas),
		errors.Is(err, strategies.ErrInvalidControlPlaneReplicas),
		errors.Is(err, strategies.ErrClusterDescriptionTooLong),
		errors.Is(err, strategies.ErrInvalidClusterName),
		errors.Is(err, strategies.ErrNodeNameRequired),
		errors.Is(err, strategies.ErrNodeIDRequired),
		errors.Is(err, strategies.ErrClusterVersionRequired),
		errors.Is(err, strategies.ErrClusterVersionUnavailable),
		errors.Is(err, strategies.ErrClusterNameRequired),
		errors.Is(err, strategies.ErrClusterIDRequired),
		errors.Is(err, strategies.ErrControlPlaneReplicasRequired),
		errors.Is(err, strategies.ErrWorkerReplicasRequired),
		errors.Is(err, strategies.ErrControlPlaneIPRequired),
		errors.Is(err, strategies.ErrClusterTagNameRequired),
		errors.Is(err, strategies.ErrInvalidClusterTagName),
		errors.Is(err, strategies.ErrInvalidClusterTagValue):
		return fuego.HTTPError{
			Err:    err,
			Status: http.StatusUnprocessableEntity,
			Title:  "Invalid cluster parameters",
			Detail: err.Error(),
		}
	case errors.Is(err, strategies.ErrClusterScalingConflict):
		return fuego.ConflictError{Err: err, Title: "Cluster scaling conflict", Detail: err.Error()}
	case errors.Is(err, strategies.ErrClusterDescriptionUpdateConflict):
		return fuego.ConflictError{Err: err, Title: "Description update conflict", Detail: strategies.ErrClusterDescriptionUpdateConflict.Error()}
	case errors.Is(err, strategies.ErrPauseUpdateConflict):
		return fuego.ConflictError{Err: err, Title: "Pause update conflict", Detail: strategies.ErrPauseUpdateConflict.Error()}
	case errors.Is(err, strategies.ErrNodeDeletionConflict):
		return fuego.ConflictError{Err: err, Title: "Node deletion conflict", Detail: strategies.ErrNodeDeletionConflict.Error()}
	case errors.Is(err, strategies.ErrNodeNotFound):
		return fuego.NotFoundError{Err: err, Title: "Node not found", Detail: strategies.ErrNodeNotFound.Error()}
	case errors.Is(err, strategies.ErrClusterTagNotFound):
		return fuego.NotFoundError{
			Err:    err,
			Title:  "Cluster tag not found",
			Detail: strategies.ErrClusterTagNotFound.Error(),
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
