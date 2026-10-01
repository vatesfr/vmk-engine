package handlers

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/go-fuego/fuego"
	"github.com/go-playground/validator/v10"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"capv-proxy/pkg/strategies"
)

func TestToHTTPErrorPreservesValidationDetails(t *testing.T) {
	_, original := fuego.TransformAndValidate(context.Background(), ClusterCreateParams{})
	var originalProblem fuego.HTTPError
	require.ErrorAs(t, original, &originalProblem)
	for name, input := range map[string]error{
		"value":   original,
		"pointer": &originalProblem,
		"wrapped": fmt.Errorf("request: %w", original),
	} {
		t.Run(name, func(t *testing.T) {
			result := toHTTPError(input)
			var problem fuego.HTTPError
			require.ErrorAs(t, result, &problem)
			assert.Equal(t, http.StatusUnprocessableEntity, problem.StatusCode())
			assert.Equal(t, originalProblem.Title, problem.Title)
			assert.Equal(t, originalProblem.Detail, problem.Detail)
			assert.Equal(t, originalProblem.Errors, problem.Errors)
			assert.Equal(t, input, errors.Unwrap(result))
			var fields validator.ValidationErrors
			assert.ErrorAs(t, result, &fields)
		})
	}
}

func TestToHTTPErrorApplicationValidation(t *testing.T) {
	for _, cause := range []error{
		strategies.ErrClusterNameRequired,
		strategies.ErrControlPlaneReplicasRequired,
		strategies.ErrWorkerReplicasRequired,
		strategies.ErrControlPlaneIPRequired,
		strategies.ErrClusterTagNameRequired,
		strategies.ErrInvalidClusterTagName,
		strategies.ErrInvalidClusterTagValue,
	} {
		t.Run(cause.Error(), func(t *testing.T) {
			input := fmt.Errorf("operation: %w", cause)
			result := toHTTPError(input)
			var status fuego.ErrorWithStatus
			require.ErrorAs(t, result, &status)
			assert.Equal(t, http.StatusUnprocessableEntity, status.StatusCode())
			assert.ErrorIs(t, result, cause)
		})
	}
}

func TestToHTTPErrorPreservesOtherHTTPStatuses(t *testing.T) {
	for _, input := range []error{
		fuego.BadRequestError{Err: errors.New("invalid JSON")},
		fuego.NotFoundError{},
		fuego.ConflictError{},
		fuego.HTTPError{Status: http.StatusUnauthorized},
	} {
		assert.Equal(t, input, toHTTPError(input))
	}
	// Missing server configuration remains an internal error.
	result := toHTTPError(strategies.ErrClusterNamespaceRequired)
	var status fuego.ErrorWithStatus
	require.ErrorAs(t, result, &status)
	assert.Equal(t, http.StatusInternalServerError, status.StatusCode())
}
