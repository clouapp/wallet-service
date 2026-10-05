package lifecycle

import (
	"context"
	"errors"
	"fmt"
	"net"
)

// ListenRouter serves on a listener and shuts its server down; Goravel's route.Route
// satisfies it.
type ListenRouter interface {
	Listen(l net.Listener) error
	Shutdown(ctx ...context.Context) error
}

// ListenerServer serves a router on a listener bound before Run, so a busy port fails
// before any worker starts.
type ListenerServer struct {
	router   ListenRouter
	listener net.Listener
}

// ListenerServerDeps is everything the listener server needs. Both fields are
// required; a nil field is rejected.
type ListenerServerDeps struct {
	Router   ListenRouter
	Listener net.Listener
}

// NewListenerServer wires the listener server from ListenerServerDeps.
func NewListenerServer(deps ListenerServerDeps) (*ListenerServer, error) {
	if deps.Router == nil {
		return nil, errors.New("lifecycle: router is required")
	}
	if deps.Listener == nil {
		return nil, errors.New("lifecycle: listener is required")
	}
	return &ListenerServer{router: deps.Router, listener: deps.Listener}, nil
}

func (s *ListenerServer) Serve() error {
	return s.router.Listen(s.listener)
}

// Shutdown also closes the listener: a router that has not created its server yet
// returns from Shutdown without closing anything, and Serve must not keep accepting.
func (s *ListenerServer) Shutdown(ctx context.Context) error {
	shutdownErr := s.router.Shutdown(ctx)
	if closeErr := s.listener.Close(); closeErr != nil && !errors.Is(closeErr, net.ErrClosed) {
		return errors.Join(shutdownErr, fmt.Errorf("close listener: %w", closeErr))
	}
	return shutdownErr
}
