package middlewares

import (
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"net/http"
)

// APIKeyHeader is the request header carrying the API key.
const APIKeyHeader = "API-Key"

// APIKey holds the key accepted by AuthRequired.
type APIKey struct {
	hash [sha256.Size]byte
}

// NewAPIKey returns the key expected by AuthRequired.
func NewAPIKey(key string) (*APIKey, error) {
	if key == "" {
		return nil, errors.New("API key must not be empty")
	}
	return &APIKey{hash: sha256.Sum256([]byte(key))}, nil
}

func (k *APIKey) matches(key string) bool {
	// Hashing gives fixed-length inputs so the comparison time does not leak
	// the length of the expected key.
	hash := sha256.Sum256([]byte(key))
	return subtle.ConstantTimeCompare(hash[:], k.hash[:]) == 1
}

// AuthRequired requires an API-Key header matching the given API key. A nil
// key disables authentication.
func AuthRequired(apiKey *APIKey) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		if apiKey == nil {
			return next
		}
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !apiKey.matches(r.Header.Get(APIKeyHeader)) {
				http.Error(w, http.StatusText(http.StatusUnauthorized), http.StatusUnauthorized)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
