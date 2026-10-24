// Package fakedns is a DNS server for tests: it answers A and AAAA queries from a
// table, over UDP on loopback, so a *net.Resolver pointed at it resolves names the
// test chooses, and changes them while the test runs (DNS rebinding), without
// asking the network.
package fakedns

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"strings"
	"sync"
	"testing"

	"golang.org/x/net/dns/dnsmessage"
)

// Server answers the names of its table; any other name is NXDOMAIN. With Silent
// set it answers nothing, like a resolver that is down.
type Server struct {
	conn net.PacketConn

	mu      sync.Mutex
	answers map[string][]netip.Addr
	silent  bool
	queries []string
}

// Start serves answers (name → addresses) until the test ends.
func Start(t testing.TB, answers map[string][]string) *Server {
	t.Helper()
	s, err := Serve(answers)
	if err != nil {
		t.Fatalf("fakedns: %v", err)
	}
	t.Cleanup(s.Close)
	return s
}

// Serve serves answers until Close, for a server a whole test binary shares.
func Serve(answers map[string][]string) (*Server, error) {
	conn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("listen: %w", err)
	}
	s := &Server{conn: conn, answers: map[string][]netip.Addr{}}
	for name, addrs := range answers {
		s.Set(name, addrs...)
	}
	go s.serve()
	return s, nil
}

// Close stops the server.
func (s *Server) Close() {
	_ = s.conn.Close()
}

// Resolver resolves through this server only (the hosts file still answers the
// names it lists, such as localhost).
func (s *Server) Resolver() *net.Resolver {
	address := s.conn.LocalAddr().String()
	return &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var dialer net.Dialer
			return dialer.DialContext(ctx, "udp", address)
		},
	}
}

// UseAsSystemResolver makes net.DefaultResolver, which a lookup without a resolver
// of its own goes through (a guard built with a nil resolver), resolve through this
// server only until the test ends. It changes a value the whole process shares: a
// test that calls it must not run in parallel.
func (s *Server) UseAsSystemResolver(t testing.TB) {
	t.Helper()
	previousGo, previousDial := net.DefaultResolver.PreferGo, net.DefaultResolver.Dial
	net.DefaultResolver.PreferGo, net.DefaultResolver.Dial = true, s.Resolver().Dial
	t.Cleanup(func() {
		net.DefaultResolver.PreferGo, net.DefaultResolver.Dial = previousGo, previousDial
	})
}

// Set makes name answer addrs from now on; no address makes it NXDOMAIN.
func (s *Server) Set(name string, addrs ...string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := canonical(name)
	if len(addrs) == 0 {
		delete(s.answers, key)
		return
	}
	parsed := make([]netip.Addr, 0, len(addrs))
	for _, addr := range addrs {
		parsed = append(parsed, netip.MustParseAddr(addr))
	}
	s.answers[key] = parsed
}

// Silence makes the server stop answering.
func (s *Server) Silence() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.silent = true
}

// Queries are the names asked so far, without the trailing dot, one per question.
func (s *Server) Queries() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.queries...)
}

func (s *Server) serve() {
	buf := make([]byte, 1500)
	for {
		n, from, err := s.conn.ReadFrom(buf)
		if err != nil {
			return
		}
		if reply := s.reply(buf[:n]); reply != nil {
			_, _ = s.conn.WriteTo(reply, from)
		}
	}
}

func (s *Server) reply(packet []byte) []byte {
	var parser dnsmessage.Parser
	header, err := parser.Start(packet)
	if err != nil {
		return nil
	}
	question, err := parser.Question()
	if err != nil {
		return nil
	}
	name := canonical(question.Name.String())

	s.mu.Lock()
	silent := s.silent
	addrs, known := s.answers[name]
	s.queries = append(s.queries, name)
	s.mu.Unlock()
	if silent {
		return nil
	}

	answer := dnsmessage.Header{ID: header.ID, Response: true, RecursionDesired: header.RecursionDesired, RecursionAvailable: true}
	if !known {
		answer.RCode = dnsmessage.RCodeNameError
	}
	builder := dnsmessage.NewBuilder(nil, answer)
	builder.EnableCompression()
	if builder.StartQuestions() != nil || builder.Question(question) != nil || builder.StartAnswers() != nil {
		return nil
	}
	head := dnsmessage.ResourceHeader{Name: question.Name, Class: dnsmessage.ClassINET, TTL: 0}
	for _, addr := range addrs {
		switch {
		case question.Type == dnsmessage.TypeA && addr.Is4():
			if builder.AResource(head, dnsmessage.AResource{A: addr.As4()}) != nil {
				return nil
			}
		case question.Type == dnsmessage.TypeAAAA && addr.Is6():
			if builder.AAAAResource(head, dnsmessage.AAAAResource{AAAA: addr.As16()}) != nil {
				return nil
			}
		}
	}
	reply, err := builder.Finish()
	if err != nil {
		return nil
	}
	return reply
}

func canonical(name string) string {
	return strings.TrimSuffix(strings.ToLower(name), ".")
}
