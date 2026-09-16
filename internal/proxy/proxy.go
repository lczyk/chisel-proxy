// Package proxy is an HTTP forward-proxy that answers requests for its archive's
// suite from memory and streams everything else to the real upstream archive.
// chisel reaches it via the http_proxy environment variable; this is the only
// lever, since chisel hardcodes archive.ubuntu.com and has no per-archive URL.
package proxy

import (
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"sync/atomic"

	"github.com/lczyk/chisel-proxy/internal/apt"
)

// Proxy serves one archive and passes everything else through. When bin MITM is
// enabled it also terminates TLS for a set of hosts and answers them from a
// handler (the fake bin store).
type Proxy struct {
	archive  *apt.Archive
	direct   http.RoundTripper
	logger   *log.Logger
	local    atomic.Int64
	upstream atomic.Int64

	ca         *certAuthority
	binHandler http.Handler
	mitmHosts  map[string]bool
}

// EnableMITM turns on TLS interception for the given hosts, answering them from
// h. It returns the ephemeral CA certificate in PEM form, which the caller must
// hand to chisel via SSL_CERT_FILE so the forged certs are trusted.
func (p *Proxy) EnableMITM(hosts []string, h http.Handler) ([]byte, error) {
	ca, err := newCertAuthority()
	if err != nil {
		return nil, err
	}
	p.ca = ca
	p.binHandler = h
	p.mitmHosts = make(map[string]bool, len(hosts))
	for _, host := range hosts {
		p.mitmHosts[host] = true
	}
	return ca.CertPEM(), nil
}

// New builds a proxy for a. logger may be nil.
func New(a *apt.Archive, logger *log.Logger) *Proxy {
	return &Proxy{
		archive: a,
		// Proxy:nil so passthrough does not loop back through us.
		direct: &http.Transport{Proxy: nil},
		logger: logger,
	}
}

func (p *Proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodConnect {
		p.handleConnect(w, r)
		return
	}
	if p.archive.Match(r.URL.Path) {
		body, ctype, ok := p.archive.Serve(r.URL.Path)
		if !ok {
			p.logf("local 404 %s", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		p.local.Add(1)
		p.logf("local    %s", r.URL.Path)
		w.Header().Set("Content-Type", ctype)
		w.Header().Set("Content-Length", fmt.Sprintf("%d", len(body)))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(body)
		return
	}
	p.passthrough(w, r)
}

func (p *Proxy) passthrough(w http.ResponseWriter, r *http.Request) {
	req, err := http.NewRequest(r.Method, r.URL.String(), r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	req.Header = r.Header.Clone()
	req.Header.Del("Proxy-Connection")
	resp, err := p.direct.RoundTrip(req)
	if err != nil {
		p.logf("upstream error %s: %v", r.URL.String(), err)
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	p.upstream.Add(1)
	p.logf("upstream %s", r.URL.String())
	for k, vs := range resp.Header {
		for _, v := range vs {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}

// Start binds 127.0.0.1:port (0 = auto-pick) and serves in the background,
// returning the chosen host:port and a stop function.
func (p *Proxy) Start(port int) (addr string, stop func() error, err error) {
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return "", nil, err
	}
	srv := &http.Server{Handler: p}
	go func() { _ = srv.Serve(ln) }()
	return ln.Addr().String(), srv.Close, nil
}

// Counts returns how many requests were served locally versus passed upstream.
func (p *Proxy) Counts() (local, upstream int64) {
	return p.local.Load(), p.upstream.Load()
}

func (p *Proxy) logf(format string, args ...any) {
	if p.logger != nil {
		p.logger.Printf(format, args...)
	}
}
