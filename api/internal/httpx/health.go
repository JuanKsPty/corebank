package httpx

import (
	"context"
	"net/http"
	"sync"
	"time"
)

// Checker is a dependency that can report whether it is reachable.
type Checker interface {
	Ping(ctx context.Context) error
}

// healthResponse is the body of /healthz.
type healthResponse struct {
	Status  string            `json:"status"`
	Version string            `json:"version"`
	Checks  map[string]string `json:"checks"`
}

// Health reports the liveness of every backing store.
//
// It actually pings them rather than returning a static 200. A health endpoint
// that cannot fail is worse than none: compose's `depends_on: service_healthy`
// and the operator both end up trusting a signal that means nothing. Failures
// are reported per dependency, and the response is 503 when any is down, so the
// answer to "why is the API not working" is in the body.
//
// Checks run concurrently under a short deadline, because a health probe that
// blocks on a hung database is itself a hung request.
func Health(version string, checks map[string]Checker) http.HandlerFunc {
	const probeTimeout = 3 * time.Second

	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), probeTimeout)
		defer cancel()

		var (
			mu      sync.Mutex
			results = make(map[string]string, len(checks))
			healthy = true
			wg      sync.WaitGroup
		)

		for name, check := range checks {
			wg.Add(1)
			go func(name string, check Checker) {
				defer wg.Done()
				status := "ok"
				if err := check.Ping(ctx); err != nil {
					status = "unreachable: " + err.Error()
				}

				mu.Lock()
				defer mu.Unlock()
				results[name] = status
				if status != "ok" {
					healthy = false
				}
			}(name, check)
		}
		wg.Wait()

		body := healthResponse{Status: "ok", Version: version, Checks: results}
		status := http.StatusOK
		if !healthy {
			body.Status = "degraded"
			status = http.StatusServiceUnavailable
		}
		JSON(w, r, status, body)
	}
}

// CheckerFunc adapts a function to Checker, for dependencies whose ping is not
// already a method with the right shape.
type CheckerFunc func(ctx context.Context) error

func (f CheckerFunc) Ping(ctx context.Context) error { return f(ctx) }
