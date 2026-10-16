package envdb

import (
	"io"
	"net"
	"net/url"
	"sync"
	"testing"
	"time"
)

/* A deterministic way to make a slow operation slow.
 *
 * The manager lifecycle tests need a build or a DDL that is provably stuck,
 * not merely likely to take a while: a sleep would make them racy. This proxy
 * sits between the test and the cluster and holds new connections in the
 * startup handshake until the test resumes them. Existing connections keep
 * flowing, so the manager's admin connection stays usable while one build or
 * one DDL is parked.
 *
 * It is in-package because the tests it serves assert internal invariants (the
 * envs map and the in-flight marker) rather than the package's public surface.
 */

// stallProxy forwards TCP to a PostgreSQL address and can park the next
// connections.
type stallProxy struct {
	target string
	ln     net.Listener

	mu       sync.Mutex
	stalled  bool
	arrived  chan struct{}
	resumeCh chan struct{}
	conns    map[net.Conn]struct{}

	wg sync.WaitGroup
}

// startStallProxy listens on a free loopback port and forwards to target.
func startStallProxy(t *testing.T, target string) *stallProxy {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("start the stall proxy: %v", err)
	}
	p := &stallProxy{
		target: target,
		ln:     ln,
		conns:  map[net.Conn]struct{}{},
	}
	t.Cleanup(p.Close)
	go p.accept()
	return p
}

// Addr is the proxy's host:port, for a DSN.
func (p *stallProxy) Addr() string { return p.ln.Addr().String() }

// stall parks every connection that arrives from now on until resume.
func (p *stallProxy) stall() {
	p.mu.Lock()
	p.stalled = true
	p.arrived = make(chan struct{}, 1)
	p.resumeCh = make(chan struct{})
	p.mu.Unlock()
}

// resume lets the parked connections through.
func (p *stallProxy) resume() {
	p.mu.Lock()
	if p.stalled {
		p.stalled = false
		close(p.resumeCh)
	}
	p.mu.Unlock()
}

// waitArrival blocks until at least one connection is parked. It is how a test
// knows the operation under test is stuck rather than merely slow.
func (p *stallProxy) waitArrival(t *testing.T) {
	t.Helper()
	p.mu.Lock()
	arrived := p.arrived
	p.mu.Unlock()
	if arrived == nil {
		t.Fatal("waitArrival called before stall")
	}
	select {
	case <-arrived:
	case <-time.After(time.Minute):
		t.Fatal("no connection reached the stall proxy")
	}
}

// Close unblocks everything and waits for the forwarding goroutines to stop.
func (p *stallProxy) Close() {
	_ = p.ln.Close()
	p.resume()
	p.mu.Lock()
	conns := make([]net.Conn, 0, len(p.conns))
	for conn := range p.conns {
		conns = append(conns, conn)
	}
	p.mu.Unlock()
	for _, conn := range conns {
		_ = conn.Close()
	}
	p.wg.Wait()
}

func (p *stallProxy) accept() {
	for {
		conn, err := p.ln.Accept()
		if err != nil {
			return
		}
		p.mu.Lock()
		p.conns[conn] = struct{}{}
		stalled, arrived, resumeCh := p.stalled, p.arrived, p.resumeCh
		p.mu.Unlock()

		p.wg.Add(1)
		go func() {
			defer p.wg.Done()
			p.serve(conn, stalled, arrived, resumeCh)
		}()
	}
}

func (p *stallProxy) serve(conn net.Conn, stalled bool, arrived, resumeCh chan struct{}) {
	defer conn.Close()
	if stalled {
		select {
		case arrived <- struct{}{}:
		default:
		}
		select {
		case <-resumeCh:
		case <-time.After(2 * time.Minute):
			return
		}
	}
	upstream, err := net.Dial("tcp", p.target)
	if err != nil {
		return
	}
	defer upstream.Close()

	copied := make(chan struct{}, 2)
	go func() {
		_, _ = io.Copy(upstream, conn)
		copied <- struct{}{}
	}()
	go func() {
		_, _ = io.Copy(conn, upstream)
		copied <- struct{}{}
	}()
	<-copied
	_ = conn.Close()
	_ = upstream.Close()
	<-copied
}

// proxyDSN rewrites a DSN to point at the proxy.
func proxyDSN(t *testing.T, dsn string, p *stallProxy) string {
	t.Helper()
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatalf("parse DSN: %v", err)
	}
	u.Host = p.Addr()
	return u.String()
}

// dsnHost returns the host:port a DSN connects to.
func dsnHost(t *testing.T, dsn string) string {
	t.Helper()
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatalf("parse DSN: %v", err)
	}
	return u.Host
}
