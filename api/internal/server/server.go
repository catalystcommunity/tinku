package server

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/rs/cors"
	log "github.com/sirupsen/logrus"

	"github.com/catalystcommunity/tinku/api/internal/config"
	"github.com/catalystcommunity/tinku/api/internal/csilservices"
	"github.com/catalystcommunity/tinku/api/internal/store"
	"github.com/catalystcommunity/tinku/api/internal/transport"
)

// RPCMountPath is the canonical CSIL-RPC HTTP mount. The whole POST body is
// one request envelope and the whole response body is one response envelope;
// the path carries no routing meaning, because (service, op) live inside the
// envelope.
const RPCMountPath = "/csil/v1/rpc"

// maxRPCBodyBytes caps one envelope's HTTP body at the same limit the
// transport package enforces on a stream carrier, so an HTTP caller cannot
// force an allocation a framed carrier would have refused.
const maxRPCBodyBytes = transport.MaxFrameDefault

// Server is tinku-api's public HTTP surface. The ops surface (/metrics,
// /healthz, /readyz) is deliberately NOT here — it is a separate listener on
// a separate port, see api/internal/metrics.
type Server struct {
	cfg    config.Config
	store  store.Store
	pki    csilservices.PKIClient
	routes map[string]map[string]typedHandler

	httpServer *http.Server
}

// New constructs a Server. svcs supplies one implementation per generated
// CSIL service; pki is the linkkeys relying-party client the auth callback
// needs, and may be nil when linkkeys is unconfigured.
func New(cfg config.Config, st store.Store, pki csilservices.PKIClient, svcs Services) *Server {
	return &Server{cfg: cfg, store: st, pki: pki, routes: buildRoutes(svcs)}
}

// Handler builds the full middleware chain around the mux. Order, outermost
// first: CORS (so even a rejected or preflight request gets its headers),
// request logging, the session-cookie writer, then the session reader, then
// the mux.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST "+RPCMountPath, s.handleRPC)
	// The identity provider can only redirect a browser to a plain GET URL,
	// so this one step of the login flow cannot be a CSIL op.
	mux.HandleFunc("GET /auth/callback", s.handleAuthCallback)

	var handler http.Handler = mux
	handler = sessionMiddleware(s.store)(handler)
	handler = sessionCookieMiddleware(s.cfg)(handler)
	handler = requestLoggingMiddleware(handler)
	handler = corsMiddleware(s.cfg.CORSOrigins)(handler)
	return handler
}

// handleRPC is the HTTP carrier for CSIL-RPC: decode the body as one request
// envelope, dispatch it, encode the response, and answer HTTP 200.
//
// A non-zero transport status still rides inside a successfully encoded
// envelope — HTTP status codes are reserved for carrier failures the
// envelope cannot express, which here means a wrong mount (404, from the
// mux) or an over-size body (413).
func (s *Server) handleRPC(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, maxRPCBodyBytes+1))
	if err != nil {
		http.Error(w, "reading request body: "+err.Error(), http.StatusBadRequest)
		return
	}
	if len(body) > maxRPCBodyBytes {
		http.Error(w, "request body exceeds max envelope size", http.StatusRequestEntityTooLarge)
		return
	}

	var resp transport.RpcResponse
	req, decodeErr := transport.DecodeRpcRequest(body)
	if decodeErr != nil {
		resp = transport.NewRpcResponseTransportError(transport.StatusMalformedEnvelope, decodeErr.Error())
	} else {
		outcome := dispatch(r.Context(), s.routes, &req)
		if outcome.IsReply {
			resp = transport.NewRpcResponseOk(outcome.Variant, outcome.Payload).WithID(req.ID)
		} else {
			resp = transport.NewRpcResponseTransportError(outcome.Status, outcome.Message).WithID(req.ID)
		}
	}

	out, err := resp.Encode()
	if err != nil {
		// Encoding a well-formed response should not fail; if it does,
		// there is no typed answer left to give.
		log.WithError(err).Error("encoding CSIL-RPC response envelope")
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/cbor")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(out)
}

// corsMiddleware wraps handler with rs/cors. A single "*" entry — the
// default — allows any origin, which suits a local run and nothing else.
//
// The wildcard is expressed as AllowOriginFunc rather than as
// AllowedOrigins:["*"], and that is not a style choice. Every call here
// carries the session cookie, and a browser REFUSES a credentialed response
// whose Access-Control-Allow-Origin is the literal "*". rs/cors sends "*"
// for AllowedOrigins:["*"] and echoes the request's origin for anything
// else, so the wildcard has to be spelled the second way or the permissive
// setting becomes the one that fails. See TestCredentialedCorsEchoesTheOrigin.
func corsMiddleware(origins []string) func(http.Handler) http.Handler {
	options := cors.Options{
		AllowedMethods:   []string{http.MethodGet, http.MethodPost, http.MethodOptions},
		AllowedHeaders:   []string{"Content-Type", "Authorization"},
		AllowCredentials: true,
	}
	// A "*" ANYWHERE in the list is the wildcard, not only a list that is
	// exactly ["*"]. `TINKU_CORS_ORIGINS=*,https://tinku.example` means the
	// same thing as `*`, and reading it any other way would hand the
	// wildcard to rs/cors as a literal origin and break credentials on the
	// most permissive setting there is.
	wildcard := false
	for _, origin := range origins {
		if origin == "*" {
			wildcard = true
			break
		}
	}
	if wildcard {
		options.AllowOriginFunc = func(string) bool { return true }
	} else {
		options.AllowedOrigins = origins
	}
	return cors.New(options).Handler
}

// statusRecorder captures the status a handler wrote, which
// http.ResponseWriter offers no way to read back.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(status int) {
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}

// requestLoggingMiddleware logs one line per request: method, path, status
// and duration.
func requestLoggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		log.WithFields(log.Fields{
			"method":   r.Method,
			"path":     r.URL.Path,
			"status":   rec.status,
			"duration": time.Since(start).String(),
		}).Info("request")
	})
}

// ListenAndServe starts the API listener and blocks until ctx is canceled,
// then drains in-flight requests before returning. A listener error returns
// immediately rather than waiting for ctx.
func (s *Server) ListenAndServe(ctx context.Context) error {
	s.httpServer = &http.Server{
		Addr:              fmt.Sprintf(":%d", s.cfg.APIPort),
		Handler:           s.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		if err := s.httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
		close(errCh)
	}()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return s.httpServer.Shutdown(shutdownCtx)
	case err := <-errCh:
		return err
	}
}
