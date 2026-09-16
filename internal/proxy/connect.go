package proxy

import (
	"bufio"
	"bytes"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"
)

// handleConnect serves an HTTP CONNECT: for a host we intercept it terminates
// TLS and answers from the bin handler; otherwise it opens a plain byte tunnel
// to the real host.
func (p *Proxy) handleConnect(w http.ResponseWriter, r *http.Request) {
	hostport := r.Host
	hostname := hostport
	if h, _, err := net.SplitHostPort(hostport); err == nil {
		hostname = h
	}
	hj, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "proxy: connection hijacking unsupported", http.StatusInternalServerError)
		return
	}
	conn, _, err := hj.Hijack()
	if err != nil {
		return
	}
	if p.ca != nil && p.mitmHosts[hostname] {
		p.interceptTLS(conn, hostname)
		return
	}
	p.tunnel(conn, hostport)
}

func (p *Proxy) interceptTLS(conn net.Conn, hostname string) {
	defer conn.Close()
	if _, err := conn.Write([]byte("HTTP/1.1 200 Connection established\r\n\r\n")); err != nil {
		return
	}
	tlsConn := tls.Server(conn, p.ca.tlsConfig())
	if err := tlsConn.Handshake(); err != nil {
		p.logf("mitm handshake %s: %v", hostname, err)
		return
	}
	br := bufio.NewReader(tlsConn)
	for {
		req, err := http.ReadRequest(br)
		if err != nil {
			return
		}
		if req.Body != nil {
			_, _ = io.Copy(io.Discard, req.Body)
			req.Body.Close()
		}
		rw := &bufResponseWriter{header: http.Header{}}
		p.binHandler.ServeHTTP(rw, req)
		p.local.Add(1)
		p.logf("mitm     %s%s", hostname, req.URL.Path)
		if err := rw.writeTo(tlsConn); err != nil {
			return
		}
		if req.Close {
			return
		}
	}
}

func (p *Proxy) tunnel(client net.Conn, hostport string) {
	defer client.Close()
	server, err := net.DialTimeout("tcp", hostport, 20*time.Second)
	if err != nil {
		_, _ = client.Write([]byte("HTTP/1.1 502 Bad Gateway\r\n\r\n"))
		return
	}
	defer server.Close()
	if _, err := client.Write([]byte("HTTP/1.1 200 Connection established\r\n\r\n")); err != nil {
		return
	}
	p.upstream.Add(1)
	done := make(chan struct{}, 2)
	go func() { _, _ = io.Copy(server, client); done <- struct{}{} }()
	go func() { _, _ = io.Copy(client, server); done <- struct{}{} }()
	<-done
}

// bufResponseWriter buffers a handler's response and writes it as one HTTP/1.1
// message with an explicit Content-Length, so the intercepted connection stays
// usable for keep-alive.
type bufResponseWriter struct {
	header      http.Header
	status      int
	body        bytes.Buffer
	wroteHeader bool
}

func (w *bufResponseWriter) Header() http.Header { return w.header }

func (w *bufResponseWriter) WriteHeader(status int) {
	if !w.wroteHeader {
		w.status = status
		w.wroteHeader = true
	}
}

func (w *bufResponseWriter) Write(b []byte) (int, error) {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	return w.body.Write(b)
}

func (w *bufResponseWriter) writeTo(conn net.Conn) error {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	var out bytes.Buffer
	fmt.Fprintf(&out, "HTTP/1.1 %d %s\r\n", w.status, http.StatusText(w.status))
	w.header.Set("Content-Length", fmt.Sprintf("%d", w.body.Len()))
	if w.header.Get("Content-Type") == "" {
		w.header.Set("Content-Type", "application/octet-stream")
	}
	w.header.Set("Connection", "keep-alive")
	_ = w.header.Write(&out)
	out.WriteString("\r\n")
	out.Write(w.body.Bytes())
	_, err := conn.Write(out.Bytes())
	return err
}
