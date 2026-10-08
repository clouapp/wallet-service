package lifecycle

import (
	"context"
	"io"
	"net"
	"net/http"
	"testing"
	"time"
)

// httpRouter stands in for Goravel's route, which is an http.Handler.
type httpRouter struct{}

func (*httpRouter) ServeHTTP(w http.ResponseWriter, _ *http.Request) {
	_, _ = io.WriteString(w, "ok")
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
	if server.listener != listener || server.server.Handler == nil {
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
	listener := listenLocal(t)
	server, err := NewListenerServer(ListenerServerDeps{Router: &httpRouter{}, Listener: listener})
	if err != nil {
		t.Fatalf("NewListenerServer: %v", err)
	}
	if err := server.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}

	served := make(chan error, 1)
	go func() { served <- server.Serve() }()
	select {
	case <-served:
	case <-time.After(time.Second):
		t.Fatal("Serve kept running after an early Shutdown")
	}
	if _, err := net.DialTimeout("tcp", listener.Addr().String(), 200*time.Millisecond); err == nil {
		t.Fatal("the port must not accept connections after an early Shutdown")
	}
}

func TestListener_Server_WrapSitsInFrontOfTheRouter(t *testing.T) {
	listener := listenLocal(t)
	wrap := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("X-Wrapped", "yes")
			next.ServeHTTP(w, r)
		})
	}
	server, err := NewListenerServer(ListenerServerDeps{Router: &httpRouter{}, Listener: listener, Wrap: wrap})
	if err != nil {
		t.Fatalf("NewListenerServer: %v", err)
	}
	go func() { _ = server.Serve() }()
	t.Cleanup(func() { _ = server.Shutdown(context.Background()) })

	response, err := http.Get("http://" + listener.Addr().String())
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(response.Body)
	if response.Header.Get("X-Wrapped") != "yes" || string(body) != "ok" {
		t.Fatalf("wrapped = %q %v", body, response.Header)
	}
}
