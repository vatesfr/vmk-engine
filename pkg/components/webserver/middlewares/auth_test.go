package middlewares

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestNewAPIKey(t *testing.T) {
	if _, err := NewAPIKey(""); err == nil {
		t.Error("NewAPIKey(\"\") succeeded, want error")
	}
}

func TestAuthRequired(t *testing.T) {
	apiKey, err := NewAPIKey("s3cr3t")
	if err != nil {
		t.Fatal(err)
	}
	handler := AuthRequired(apiKey)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	for name, tc := range map[string]struct {
		header string
		value  string
		want   int
	}{
		"missing header":       {want: http.StatusUnauthorized},
		"wrong key":            {header: APIKeyHeader, value: "wrong", want: http.StatusUnauthorized},
		"empty key":            {header: APIKeyHeader, value: "", want: http.StatusUnauthorized},
		"key in Authorization": {header: "Authorization", value: "Bearer s3cr3t", want: http.StatusUnauthorized},
		"valid key":            {header: APIKeyHeader, value: "s3cr3t", want: http.StatusOK},
		"lowercase header":     {header: "api-key", value: "s3cr3t", want: http.StatusOK},
	} {
		t.Run(name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			if tc.header != "" {
				req.Header.Set(tc.header, tc.value)
			}
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if rec.Code != tc.want {
				t.Fatalf("status = %d, want %d", rec.Code, tc.want)
			}
		})
	}
}

func TestAuthRequiredWithoutAPIKey(t *testing.T) {
	handler := AuthRequired(nil)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
}
