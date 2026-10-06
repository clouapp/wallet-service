package httpclient

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// silenceableProxy forwards TCP connections until silence is called; from then on the
// connections open at that moment swallow every byte in both directions without being
// closed, like a route that died under an established connection. New connections
// still work.
type silenceableProxy struct {
	listener net.Listener
	target   string

	mu    sync.Mutex
	links []*atomic.Bool
}

func newSilenceableProxy(t *testing.T, target string) *silenceableProxy {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	proxy := &silenceableProxy{listener: listener, target: target}
	t.Cleanup(func() { _ = listener.Close() })
	go proxy.serve(t)
	return proxy
}

func (p *silenceableProxy) addr() string { return p.listener.Addr().String() }

func (p *silenceableProxy) serve(t *testing.T) {
	for {
		client, err := p.listener.Accept()
		if err != nil {
			return
		}
		upstream, err := net.Dial("tcp", p.target)
		if err != nil {
			t.Errorf("dial upstream: %v", err)
			_ = client.Close()
			return
		}
		silenced := &atomic.Bool{}
		p.mu.Lock()
		p.links = append(p.links, silenced)
		p.mu.Unlock()
		go pipe(client, upstream, silenced)
		go pipe(upstream, client, silenced)
	}
}

func (p *silenceableProxy) silence() {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, link := range p.links {
		link.Store(true)
	}
}

func pipe(src, dst net.Conn, silenced *atomic.Bool) {
	buf := make([]byte, 32<<10)
	for {
		n, err := src.Read(buf)
		if n > 0 && !silenced.Load() {
			if _, werr := dst.Write(buf[:n]); werr != nil {
				return
			}
		}
		if err != nil {
			return
		}
	}
}

func http2TestServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "ok")
	}))
	srv.EnableHTTP2 = true
	srv.StartTLS()
	t.Cleanup(srv.Close)
	return srv
}

func clientThroughProxy(srv *httptest.Server, transport *http.Transport, timeout time.Duration) *http.Client {
	transport.TLSClientConfig = srv.Client().Transport.(*http.Transport).TLSClientConfig.Clone()
	return &http.Client{Timeout: timeout, Transport: transport}
}

func get(client *http.Client, url string) (*http.Response, error) {
	resp, err := client.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if _, err := io.ReadAll(resp.Body); err != nil {
		return nil, err
	}
	return resp, nil
}

// firstSuccessAfterSilence keeps requesting until one succeeds or the deadline passes,
// and reports whether one did.
func firstSuccessAfterSilence(client *http.Client, url string, deadline time.Duration) bool {
	stop := time.Now().Add(deadline)
	for time.Now().Before(stop) {
		if _, err := get(client, url); err == nil {
			return true
		}
	}
	return false
}

func TestNew_ReplacesASilentlyDeadHTTP2Connection(t *testing.T) {
	srv := http2TestServer(t)
	proxy := newSilenceableProxy(t, srv.Listener.Addr().String())
	url := "https://" + proxy.addr() + "/"
	client := clientThroughProxy(srv, newTransport(100*time.Millisecond, 100*time.Millisecond), time.Second)

	resp, err := get(client, url)
	if err != nil {
		t.Fatalf("request before the connection dies: %v", err)
	}
	if resp.ProtoMajor != 2 {
		t.Fatalf("expected HTTP/2, got %s", resp.Proto)
	}

	proxy.silence()
	if !firstSuccessAfterSilence(client, url, 4*time.Second) {
		t.Fatal("requests kept failing on the dead connection: the PING health check did not replace it")
	}
}

func TestDefaultTransportWithoutHealthCheck_KeepsUsingTheDeadConnection(t *testing.T) {
	if testing.Short() {
		t.Skip("documents the failure mode the health check fixes; takes a few seconds")
	}
	srv := http2TestServer(t)
	proxy := newSilenceableProxy(t, srv.Listener.Addr().String())
	url := "https://" + proxy.addr() + "/"
	client := clientThroughProxy(srv, baseTransport(), 500*time.Millisecond)

	if _, err := get(client, url); err != nil {
		t.Fatalf("request before the connection dies: %v", err)
	}

	proxy.silence()
	_, err := get(client, url)
	var netErr net.Error
	if err == nil || !errors.As(err, &netErr) || !netErr.Timeout() {
		t.Fatalf("expected the request on the dead connection to time out, got %v", err)
	}
	if firstSuccessAfterSilence(client, url, 2*time.Second) {
		t.Fatal("expected the unchecked transport to keep reusing the dead connection")
	}
}

func TestNew_SharesTheHealthCheckedTransport(t *testing.T) {
	first, second := New(5*time.Second), New(30*time.Second)
	if first.Transport != sharedTransport || second.Transport != sharedTransport {
		t.Fatal("clients must share the health-checked transport and its connection pool")
	}
	if first.Timeout != 5*time.Second || second.Timeout != 30*time.Second {
		t.Fatalf("unexpected timeouts %s / %s", first.Timeout, second.Timeout)
	}
	config := sharedTransport.HTTP2
	if config == nil || config.SendPingTimeout != pingAfterIdleRead || config.PingTimeout != pingTimeout {
		t.Fatalf("unexpected HTTP/2 health check config %+v", config)
	}
}

func TestClientDo_ReturnsStatusHeaderAndLimitedBody(t *testing.T) {
	const maxBytes = 4
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, pass, ok := r.BasicAuth()
		if !ok || user != "rpc" || pass != "secret" {
			t.Errorf("basic auth = %q %q ok=%v", user, pass, ok)
		}
		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("content-type %q", r.Header.Get("Content-Type"))
		}
		if r.Method != http.MethodPost {
			t.Errorf("method %s", r.Method)
		}
		w.Header().Set("Retry-After", "3")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = io.WriteString(w, "slow-down-please")
	}))
	t.Cleanup(srv.Close)

	resp, err := Wrap(srv.Client()).Do(context.Background(), Request{
		Method:   MethodPost,
		URL:      srv.URL,
		Header:   map[string]string{"Content-Type": "application/json"},
		Body:     []byte(`{"ping":true}`),
		HasBody:  true,
		Username: "rpc",
		Password: "secret",
		MaxBytes: maxBytes,
	})
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	if resp.StatusCode != StatusTooManyRequests {
		t.Fatalf("status %d", resp.StatusCode)
	}
	if resp.Header.Get("retry-after") != "3" {
		t.Fatalf("retry-after %q", resp.Header.Get("retry-after"))
	}
	if string(resp.Body) != "slow" {
		t.Fatalf("body %q", resp.Body)
	}
}

func TestClientDo_ExpiredContextDoesNotDial(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("request was sent after the deadline")
	}))
	t.Cleanup(srv.Close)

	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	_, err := NewClient(time.Second).Do(ctx, Request{Method: MethodGet, URL: srv.URL})
	if !IsBuild(err) || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("got %v", err)
	}
}

func TestClientDo_RejectsAMissingURLBeforeDialing(t *testing.T) {
	_, err := NewClient(time.Second).Do(context.Background(), Request{Method: MethodGet})
	if !IsBuild(err) {
		t.Fatalf("expected a build error, got %v", err)
	}
}

func TestNew_RejectsANonPositiveTimeout(t *testing.T) {
	for _, timeout := range []time.Duration{0, -time.Second} {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("expected a panic for timeout %s", timeout)
				}
			}()
			New(timeout)
		}()
	}
}
