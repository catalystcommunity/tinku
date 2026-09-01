// Package metrics owns tinku's Prometheus collectors and the ops
// listener that exposes them.
//
// The ops listener is a SEPARATE http.Server on its own port, not a route on
// the API. Three reasons, in order of how often they matter:
//
//   - /metrics and the probes must answer while the API is refusing traffic.
//     A readiness probe that goes down with the thing it reports on cannot
//     report anything.
//   - Metrics are internal. Keeping them off the API port means an ingress
//     that exposes the API cannot accidentally expose them too.
//   - Draining the API on shutdown must not drain the probes, or Kubernetes
//     sees a connection error instead of a clean "not ready".
//
// This mirrors corndogs' ops-port split (corndogs/server/server.go), which
// is the pattern the Go services in this organization already follow.
package metrics

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	log "github.com/sirupsen/logrus"
)

// Namespace prefixes every metric this service publishes.
const Namespace = "tinku"

var (
	// RPCRequests counts CSIL-RPC calls by service, op and outcome.
	// `outcome` is "ok" for a typed reply, "service_error" for a declared
	// error arm, or the transport status name for a transport failure —
	// so an application error and a transport error stay distinguishable
	// on the dashboard, exactly as they are on the wire.
	RPCRequests = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: Namespace,
		Name:      "rpc_requests_total",
		Help:      "CSIL-RPC calls handled, by service, op and outcome.",
	}, []string{"service", "op", "outcome"})

	// RPCDuration measures handler latency, excluding envelope decode.
	RPCDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: Namespace,
		Name:      "rpc_duration_seconds",
		Help:      "CSIL-RPC handler latency in seconds, by service and op.",
		Buckets:   prometheus.DefBuckets,
	}, []string{"service", "op"})

	// MigrationsPending is 1 while the schema is behind the binary. It is
	// the metric to alert on: it going to 1 and staying there means a
	// deploy is stuck before it ever serves traffic.
	MigrationsPending = promauto.NewGauge(prometheus.GaugeOpts{
		Namespace: Namespace,
		Name:      "migrations_pending",
		Help:      "1 while database migrations are outstanding, 0 once the schema is current.",
	})

	// SessionsReaped counts sessions removed by the expiry sweep.
	SessionsReaped = promauto.NewCounter(prometheus.CounterOpts{
		Namespace: Namespace,
		Name:      "sessions_reaped_total",
		Help:      "Expired sessions deleted by the background sweep.",
	})
)

// Readiness is the readiness gate `tinku serve` opens once migrations have
// been applied and the store answers. It is safe for concurrent use: the ops
// listener reads it from its own goroutine while boot writes it.
//
// Liveness and readiness are deliberately different questions. Liveness asks
// "is this process working" and stays true through a slow migration, so an
// orchestrator does not kill a pod that is mid-boot. Readiness asks "should
// this receive traffic" and is false until the schema is current AND the
// database answers.
type Readiness struct {
	ready  atomic.Bool
	reason atomic.Value // string: why not ready, for the probe body
	check  atomic.Value // func(context.Context) error: the live dependency check
}

// NewReadiness returns a gate that is closed, with a starting reason to
// report until something opens it.
func NewReadiness(reason string) *Readiness {
	r := &Readiness{}
	r.reason.Store(reason)
	return r
}

// NotReady closes the gate and records why.
func (r *Readiness) NotReady(reason string) {
	r.reason.Store(reason)
	r.ready.Store(false)
}

// Ready opens the gate. check, if non-nil, is called on every readiness
// probe afterwards, so readiness keeps tracking the database rather than
// latching true at boot and never looking again.
func (r *Readiness) Ready(check func(context.Context) error) {
	if check != nil {
		r.check.Store(check)
	}
	r.reason.Store("")
	r.ready.Store(true)
}

// Check returns nil when the service should receive traffic.
func (r *Readiness) Check(ctx context.Context) error {
	if !r.ready.Load() {
		reason, _ := r.reason.Load().(string)
		if reason == "" {
			reason = "not ready"
		}
		return errors.New(reason)
	}
	if check, ok := r.check.Load().(func(context.Context) error); ok && check != nil {
		if err := check(ctx); err != nil {
			return fmt.Errorf("dependency check failed: %w", err)
		}
	}
	return nil
}

// OpsServer serves /metrics, /healthz and /readyz.
type OpsServer struct {
	server    *http.Server
	readiness *Readiness
}

// NewOpsServer builds the ops listener on port.
func NewOpsServer(port int, readiness *Readiness) *OpsServer {
	ops := &OpsServer{readiness: readiness}

	mux := http.NewServeMux()
	// A dedicated registry would hide the Go runtime and process
	// collectors promauto's default registry gives us for free, and those
	// are most of what a Go service dashboard shows.
	mux.Handle("GET /metrics", promhttp.Handler())
	mux.HandleFunc("GET /healthz", ops.handleHealthz)
	mux.HandleFunc("GET /readyz", ops.handleReadyz)

	ops.server = &http.Server{
		Addr:              fmt.Sprintf(":%d", port),
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	return ops
}

// handleHealthz is liveness: the process is running and serving. It stays
// 200 through migrations on purpose — a pod restarted mid-migration would
// only have to start the migration over.
func (o *OpsServer) handleHealthz(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok\n"))
}

// handleReadyz is readiness: migrations are applied and the database
// answers. 503 with the reason in the body, so `kubectl describe` shows what
// is being waited on rather than a bare status code.
func (o *OpsServer) handleReadyz(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	if err := o.readiness.Check(ctx); err != nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = fmt.Fprintf(w, "not ready: %v\n", err)
		return
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ready\n"))
}

// ListenAndServe starts the ops listener and blocks until ctx is canceled,
// then shuts it down. It is started FIRST during boot, before migrations, so
// probes have something to talk to for the whole of a slow start.
func (o *OpsServer) ListenAndServe(ctx context.Context) error {
	errCh := make(chan error, 1)
	go func() {
		log.WithField("addr", o.server.Addr).Info("ops listener started (/metrics, /healthz, /readyz)")
		if err := o.server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
		close(errCh)
	}()

	select {
	case <-ctx.Done():
		// A short drain: nothing here holds a long-lived connection, and
		// probes should see a closed port promptly once we are going away.
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		return o.server.Shutdown(shutdownCtx)
	case err := <-errCh:
		return err
	}
}
