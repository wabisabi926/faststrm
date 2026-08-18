package middleware

import (
	"net/http"
)

// CORS 跨域中间件
func CORS(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin == "" {
			origin = "*"
		}
		w.Header().Set("Access-Control-Allow-Origin", origin)
		w.Header().Set("Access-Control-Allow-Credentials", "true")
		w.Header().Set("Access-Control-Allow-Methods", "GET,POST,PUT,DELETE,OPTIONS,PATCH")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type,Authorization,X-Requested-With,X-Internal-Token")
		w.Header().Set("Access-Control-Expose-Headers", "Content-Length,Content-Disposition")

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next(w, r)
	}
}
