package middlewares

import "net/http"

func AuthRequired() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		// ... check token, session, etc.
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r)
		})
	}
}
