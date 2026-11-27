package webserver

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	eventmanager "github.com/vatesfr/vmk-engine/pkg/components/event_manager"
	versionmanager "github.com/vatesfr/vmk-engine/pkg/components/version_manager"
	"github.com/vatesfr/vmk-engine/pkg/components/webserver/middlewares"
)

const versionsPath = "/kubernetes/versions-available"

func TestAPIKeyProtectsKubernetesRoutes(t *testing.T) {
	apiKey, err := middlewares.NewAPIKey("s3cr3t")
	require.NoError(t, err)
	handler := HttpServer{SwaggerUI: SwaggerUIDisabled, APIKey: apiKey}.
		Handler(nil, nil, versionmanager.NewVersionManager(), eventmanager.NewEventManager())

	for name, tc := range map[string]struct {
		path string
		key  string
		want int
	}{
		"health check stays public": {path: "/ping", want: http.StatusOK},
		"API route without key":     {path: versionsPath, want: http.StatusUnauthorized},
		"API route with wrong key":  {path: versionsPath, key: "wrong", want: http.StatusUnauthorized},
		"API route with key":        {path: versionsPath, key: "s3cr3t", want: http.StatusOK},
		"event stream without key":  {path: "/kubernetes/events", want: http.StatusUnauthorized},
	} {
		t.Run(name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tc.path, nil)
			if tc.key != "" {
				req.Header.Set(middlewares.APIKeyHeader, tc.key)
			}
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			assert.Equal(t, tc.want, rec.Code)
		})
	}
}

func TestOpenAPIDeclaresAPIKeyAuth(t *testing.T) {
	document := (HttpServer{SwaggerUI: SwaggerUIDisabled}).
		newServer(nil, nil, versionmanager.NewVersionManager(), eventmanager.NewEventManager()).OpenAPI.Description()

	scheme := document.Components.SecuritySchemes[apiKeyAuthScheme]
	require.NotNil(t, scheme)
	assert.Equal(t, "apiKey", scheme.Value.Type)
	assert.Equal(t, "header", scheme.Value.In)
	assert.Equal(t, middlewares.APIKeyHeader, scheme.Value.Name)

	for path, method := range map[string]string{
		versionsPath:         http.MethodGet,
		"/kubernetes/events": http.MethodGet,
	} {
		operation := document.Paths.Find(path).GetOperation(method)
		require.NotNil(t, operation, path)
		require.NotNil(t, operation.Security, path)
		assert.Contains(t, *operation.Security, openapi3.SecurityRequirement{apiKeyAuthScheme: []string{}}, path)
		assert.NotNil(t, operation.Responses.Status(http.StatusUnauthorized), path)
	}
	ping := document.Paths.Find("/ping").GetOperation(http.MethodGet)
	require.NotNil(t, ping)
	assert.Nil(t, ping.Security)
}
