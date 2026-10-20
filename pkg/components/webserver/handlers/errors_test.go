package handlers

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/go-fuego/fuego"
	"github.com/go-playground/validator/v10"
	"github.com/gofrs/uuid"
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
		strategies.ErrClusterVersionRequired,
		strategies.ErrClusterVersionUnavailable,
		strategies.ErrClusterNameRequired,
		strategies.ErrInvalidClusterName,
		strategies.ErrControlPlaneReplicasRequired,
		strategies.ErrWorkerReplicasRequired,
		strategies.ErrControlPlaneIPRequired,
		strategies.ErrClusterTagNameRequired,
		strategies.ErrInvalidClusterTagName,
		strategies.ErrInvalidClusterTagValue,
		strategies.ErrInvalidReplicas,
		strategies.ErrInvalidControlPlaneReplicas,
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

func TestReplicaAdmissionErrorKeepsTechnicalCausePrivate(t *testing.T) {
	cause := errors.New("private webhook diagnostics")
	input := fmt.Errorf("%w: %w", strategies.ErrReplicasRejected, cause)
	var problem fuego.HTTPError
	require.ErrorAs(t, toHTTPError(input), &problem)
	require.Equal(t, http.StatusUnprocessableEntity, problem.StatusCode())
	require.Equal(t, strategies.ErrReplicasRejected.Error(), problem.Detail)
	require.ErrorIs(t, problem, cause)
}

func TestToHTTPErrorInvalidUUID(t *testing.T) {
	input, err := uuid.FromString("not-a-uuid")
	require.Error(t, err)
	assert.Equal(t, uuid.UUID{}, input)

	result := toHTTPError(err)
	var problem fuego.HTTPError
	require.ErrorAs(t, result, &problem)
	assert.Equal(t, http.StatusBadRequest, problem.StatusCode())
	assert.Equal(t, "Invalid UUID", problem.Title)
	assert.Equal(t, "path parameter 'id' must be a valid UUID", problem.Detail)
	assert.ErrorIs(t, result, err)
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

func TestClusterDescriptionHTTPValidation(t *testing.T) {
	for _, tc := range []struct {
		name        string
		description string
		isValid     bool
	}{
		{"empty", "", true},
		{"special characters", "Cluster \"Backend\"\nÉquipe 🚀", true},
		{"unicode boundary", strings.Repeat("🚀", strategies.MaxClusterDescriptionLength), true},
		{"too long", strings.Repeat("é", strategies.MaxClusterDescriptionLength+1), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := ClusterCreateParams{KubernetesVersion: "1.36.9", Name: "demo", Description: tc.description, ControlPlaneReplicas: 1, WorkerReplicas: 1, ControlPlaneIP: "192.0.2.10", VMNamePrefix: "demo"}
			_, err := fuego.TransformAndValidate(t.Context(), body)
			if !tc.isValid {
				require.Error(t, err)
				var status fuego.ErrorWithStatus
				require.ErrorAs(t, toHTTPError(err), &status)
				assert.Equal(t, http.StatusUnprocessableEntity, status.StatusCode())
			} else {
				require.NoError(t, err)
			}
		})
	}
}
