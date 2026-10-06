package lifecycle

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"sync"
	"testing"
	"time"
)

// httpRouter mirrors Goravel's gin route: Listen creates the http.Server, Shutdown
// is a no-op until it exists.
type httpRouter struct {
	mu     sync.Mutex
	server *http.Server
}

func (r *httpRouter) Listen(l net.Listener) error {
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "ok")
	})}
	r.mu.Lock()
	r.server = server
	r.mu.Unlock()
	if err := server.Serve(l); !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func (r *httpRouter) Shutdown(ctx ...context.Context) error {
	r.mu.Lock()
	server := r.server
	r.mu.Unlock()
	if server == nil {
		return nil
	}
	shutdownCtx := context.Background()
	if len(ctx) > 0 {
		shutdownCtx = ctx[0]
	}
	return server.Shutdown(shutdownCtx)
}

func listenLocal(t *testing.T) net.Listener {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	return listener
}

func TestNew_ListenerServer_RejectsMissingParts(t *testing.T) {
	if _, err := NewListenerServer(ListenerServerDeps{Listener: listenLocal(t)}); err == nil {
		t.Fatal("expected an error without a router")
	}
	if _, err := NewListenerServer(ListenerServerDeps{Router: &httpRouter{}}); err == nil {
		t.Fatal("expected an error without a listener")
	}
}

func TestNew_Listener_ServerKeepsItsDependencies(t *testing.T) {
	router := &httpRouter{}
	listener := listenLocal(t)
	server, err := NewListenerServer(ListenerServerDeps{Router: router, Listener: listener})
	if err != nil {
		t.Fatalf("NewListenerServer: %v", err)
	}
	if server.router != router {
		t.Fatal("listener server did not keep the router")
	}
	if server.listener != listener {
		t.Fatal("listener server did not keep the listener")
	}
}

func TestListener_Server_ServesUntilShutdown(t *testing.T) {
	listener := listenLocal(t)
	server, err := NewListenerServer(ListenerServerDeps{Router: &httpRouter{}, Listener: listener})
	if err != nil {
		t.Fatalf("NewListenerServer: %v", err)
	}
	served := make(chan error, 1)
	go func() { served <- server.Serve() }()

	response, err := http.Get("http://" + listener.Addr().String())
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	_ = response.Body.Close()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := server.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	select {
	case err := <-served:
		if err != nil {
			t.Fatalf("Serve after Shutdown: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Serve did not return after Shutdown")
	}
	if _, err := net.DialTimeout("tcp", listener.Addr().String(), 200*time.Millisecond); err == nil {
		t.Fatal("the port must stop accepting connections after Shutdown")
	}
}

func TestListener_Server_ShutdownBeforeServeClosesTheListener(t *testing.T) {
	server, err := NewListenerServer(ListenerServerDeps{Router: &httpRouter{}, Listener: listenLocal(t)})
	if err != nil {
		t.Fatalf("NewListenerServer: %v", err)
	}
	if err := server.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}

	served := make(chan error, 1)
	go func() { served <- server.Serve() }()
	select {
	case err := <-served:
		if err == nil {
			t.Fatal("Serve on a closed listener must fail")
		}
	case <-time.After(time.Second):
		t.Fatal("Serve kept accepting after an early Shutdown")
	}
}
