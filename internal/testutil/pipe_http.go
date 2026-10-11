package testutil

import (
	"context"
	"net"
	"net/http"
	"sync"
	"testing"
)

// HTTPDialContext serves real HTTP (including WebSocket upgrades) over net.Pipe,
// allowing transport tests to run without permission to bind local TCP ports.
func HTTPDialContext(t testing.TB, handler http.Handler) func(context.Context, string, string) (net.Conn, error) {
	t.Helper()
	listener := &pipeListener{connections: make(chan net.Conn), closed: make(chan struct{})}
	server := &http.Server{Handler: handler}
	done := make(chan struct{})
	go func() {
		defer close(done)
		if err := server.Serve(listener); err != http.ErrServerClosed {
			t.Errorf("serve pipe HTTP: %v", err)
		}
	}()
	t.Cleanup(func() {
		_ = server.Close()
		<-done
	})
	return func(ctx context.Context, _, _ string) (net.Conn, error) {
		client, peer := net.Pipe()
		select {
		case listener.connections <- peer:
			return client, nil
		case <-ctx.Done():
			_ = client.Close()
			_ = peer.Close()
			return nil, ctx.Err()
		case <-listener.closed:
			_ = client.Close()
			_ = peer.Close()
			return nil, net.ErrClosed
		}
	}
}

type pipeListener struct {
	connections chan net.Conn
	closed      chan struct{}
	once        sync.Once
}

func (l *pipeListener) Accept() (net.Conn, error) {
	select {
	case connection := <-l.connections:
		return connection, nil
	case <-l.closed:
		return nil, net.ErrClosed
	}
}

func (l *pipeListener) Close() error {
	l.once.Do(func() { close(l.closed) })
	return nil
}

func (l *pipeListener) Addr() net.Addr { return pipeAddr{} }

type pipeAddr struct{}

func (pipeAddr) Network() string { return "pipe" }
func (pipeAddr) String() string  { return "moltnet.test:80" }
