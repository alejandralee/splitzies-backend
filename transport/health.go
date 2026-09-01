package transport

import "net/http"

// HealthHandler reports whether the service and its database connection are
// up. Used by the platform for deploy health checks and restarts.
func (t *Transport) HealthHandler(w http.ResponseWriter, r *http.Request) {
	if err := t.persistenceClient.Ping(r.Context()); err != nil {
		writeJSONError(w, http.StatusServiceUnavailable, "database_unavailable", "database is unreachable", "")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"status":"ok"}`))
}
