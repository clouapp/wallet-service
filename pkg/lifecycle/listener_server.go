package lifecycle

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
)

// ListenerServer serves a handler on a listener bound before Run, so a busy port fails
// before any worker starts. It owns the http.Server, which is what lets Wrap sit at
// the net/http level, in front of the Goravel router.
type ListenerServer struct {
	server   *http.Server
	listener net.Listener
}

// ListenerServerDeps is everything the listener server needs. Router and Listener are
// required; a nil one is rejected.
type ListenerServerDeps struct {
	// Router is the Goravel route (facades.Route()), which is an http.Handler.
	Router   http.Handler
	Listener net.Listener
	// Wrap decorates Router, for example with a request deadline. Optional.
	Wrap func(http.Handler) http.Handler
	// MaxHeaderBytes caps the request header size; zero keeps the net/http default.
	MaxHeaderBytes int
}

// NewListenerServer wires the listener server from ListenerServerDeps.
func NewListenerServer(deps ListenerServerDeps) (*ListenerServer, error) {
	if deps.Router == nil {
		return nil, errors.New("lifecycle: router is required")
	}
	if deps.Listener == nil {
		return nil, errors.New("lifecycle: listener is required")
	}
	// AllowQuerySemicolons is what Goravel's own Listen puts in front of the router.
	handler := http.AllowQuerySemicolons(deps.Router)
	if deps.Wrap != nil {
		handler = deps.Wrap(handler)
	}
	server := &http.Server{
		Addr:           deps.Listener.Addr().String(),
		Handler:        handler,
		MaxHeaderBytes: deps.MaxHeaderBytes,
	}
	return &ListenerServer{server: server, listener: deps.Listener}, nil
}

// Serve blocks until Shutdown; a server closed on purpose is not an error.
func (s *ListenerServer) Serve() error {
	if err := s.server.Serve(s.listener); !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// Shutdown also closes the listener, so a Shutdown that arrives before Serve leaves
// nothing accepting.
func (s *ListenerServer) Shutdown(ctx context.Context) error {
	shutdownErr := s.server.Shutdown(ctx)
	if closeErr := s.listener.Close(); closeErr != nil && !errors.Is(closeErr, net.ErrClosed) {
		return errors.Join(shutdownErr, fmt.Errorf("close listener: %w", closeErr))
	}
	return shutdownErr
}
