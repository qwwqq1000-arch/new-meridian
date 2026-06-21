package main

import (
	"net"
	"net/http"
	"sync"

	tls "github.com/refraction-networking/utls"
	"golang.org/x/net/http2"
)

type utlsRoundTripper struct {
	mu    sync.Mutex
	conns map[string]*http2.ClientConn
}

func NewUTLSTransport() http.RoundTripper {
	return &utlsRoundTripper{conns: map[string]*http2.ClientConn{}}
}

func (t *utlsRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	host := req.URL.Host
	addr := host
	if req.URL.Port() == "" {
		addr = host + ":443"
	}
	t.mu.Lock()
	if c, ok := t.conns[host]; ok && c.CanTakeNewRequest() {
		t.mu.Unlock()
		return c.RoundTrip(req)
	}
	t.mu.Unlock()

	conn, err := net.Dial("tcp", addr)
	if err != nil {
		return nil, err
	}
	uconn := tls.UClient(conn, &tls.Config{ServerName: req.URL.Hostname(), NextProtos: []string{"h2"}}, tls.HelloChrome_Auto)
	if err := uconn.Handshake(); err != nil {
		conn.Close()
		return nil, err
	}
	tr := &http2.Transport{}
	h2, err := tr.NewClientConn(uconn)
	if err != nil {
		uconn.Close()
		return nil, err
	}
	t.mu.Lock()
	if c2, ok := t.conns[host]; ok && c2.CanTakeNewRequest() {
		t.mu.Unlock()
		return c2.RoundTrip(req)
	}
	t.conns[host] = h2
	t.mu.Unlock()
	return h2.RoundTrip(req)
}
