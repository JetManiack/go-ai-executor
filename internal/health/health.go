// Package health provides Kubernetes liveness/readiness endpoints.
package health

import (
	"context"
	"encoding/json"
	"net/http"
	"time"
)

// Livez reports unconditional 200 once the process is serving HTTP. It
// deliberately checks nothing else: a dependency outage must not restart the
// pod, since restarting can't fix it and a crash loop only delays recovery.
func Livez(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
}

// ReadyChecker backs the /readyz endpoint. MigrationsReady and OIDCReady are
// booleans captured once at startup rather than live-checked: both
// storage.Open (AutoMigrate) and OIDC discovery are synchronous and
// fatal-on-error before the server starts serving, so by construction they're
// already true for the whole lifetime of a serving process.
type ReadyChecker struct {
	Ping            func(ctx context.Context) error
	MigrationsReady bool
	OIDCReady       bool
}

type readyzResponse struct {
	Status  string   `json:"status"`
	Failing []string `json:"failing,omitempty"`
}

// Readyz reports 200 when the database is reachable and the boot-time
// migrations/OIDC-discovery checks succeeded, else 503 naming what failed.
func (rc ReadyChecker) Readyz(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()

	var failing []string
	if !rc.MigrationsReady {
		failing = append(failing, "migrations")
	}
	if !rc.OIDCReady {
		failing = append(failing, "oidc_discovery")
	}
	if rc.Ping == nil {
		failing = append(failing, "database")
	} else if err := rc.Ping(ctx); err != nil {
		failing = append(failing, "database")
	}

	w.Header().Set("Content-Type", "application/json")
	if len(failing) > 0 {
		w.WriteHeader(http.StatusServiceUnavailable)
		_ = json.NewEncoder(w).Encode(readyzResponse{Status: "not_ready", Failing: failing})
		return
	}
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(readyzResponse{Status: "ready"})
}
