package middleware

import "net/http"

// CORS is a small hand-written middleware rather than a third-party CORS
// package (docs/PLAN.md 4 does not list one, and the actual need here is
// narrow): it exists so Swagger UI, served from its own origin on port
// 8081 (docker-compose.yml), can actually call this API on port 8080 when
// a reviewer clicks "Try it out" in a browser — without it, the browser
// blocks the cross-origin request before it ever reaches this handler.
// Allowing any origin is safe for this API specifically: there are no
// cookies or sessions to leak, since client identity is a self-generated
// X-Client-Id header and partner identity is an X-Api-Key header, neither
// of which a browser attaches automatically the way it would a cookie.
func CORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PATCH, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, X-Client-Id, X-Api-Key, Idempotency-Key, If-None-Match")
		w.Header().Set("Access-Control-Expose-Headers", "ETag")

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}
