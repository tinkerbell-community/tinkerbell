package conversionwebhook

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"time"
)

// Server serves the conversion webhook over TLS.
type Server struct {
	// Addr is the address to listen on, e.g. ":9443".
	Addr    string
	Handler http.Handler
	// GetCertificate provides the serving certificate; see Certs.
	GetCertificate func(*tls.ClientHelloInfo) (*tls.Certificate, error)
}

// Listen binds the listener, so callers know the webhook can be reached
// before they configure the API server to use it.
func (s *Server) Listen(ctx context.Context) (net.Listener, error) {
	var lc net.ListenConfig
	l, err := lc.Listen(ctx, "tcp", s.Addr)
	if err != nil {
		return nil, fmt.Errorf("conversion webhook: %w", err)
	}
	return l, nil
}

// Serve serves on l until ctx is done.
func (s *Server) Serve(ctx context.Context, l net.Listener) error {
	mux := http.NewServeMux()
	mux.Handle(Path, s.Handler)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	srv := &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
		TLSConfig: &tls.Config{
			MinVersion:     tls.VersionTLS12,
			GetCertificate: s.GetCertificate,
		},
	}
	errCh := make(chan error, 1)
	go func() { errCh <- srv.ServeTLS(l, "", "") }()
	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
		return nil
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return fmt.Errorf("conversion webhook: %w", err)
	}
}
