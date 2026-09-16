package proxy

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"github.com/lczyk/assert"
	"github.com/lczyk/chisel-proxy/internal/apt"
	"github.com/lczyk/chisel-proxy/internal/deb"
	"github.com/lczyk/chisel-proxy/internal/sign"
)

func testArchive(t *testing.T) *apt.Archive {
	t.Helper()
	dir := t.TempDir()
	pkgDir := filepath.Join(dir, "demo")
	assert.NoError(t, os.MkdirAll(filepath.Join(pkgDir, "usr", "bin"), 0755))
	assert.NoError(t, os.WriteFile(filepath.Join(pkgDir, "usr", "bin", "demo"), []byte("hi"), 0755))
	p, err := deb.FromDir(pkgDir, deb.Defaults{Arch: "amd64", Version: "1.0"})
	assert.NoError(t, err)
	signer, err := sign.New("t", "t@example.invalid")
	assert.NoError(t, err)
	a, err := apt.New("chisel-proxy", "amd64", []*deb.Package{p}, signer)
	assert.NoError(t, err)
	return a
}

func TestProxyLocalAndPassthrough(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "UPSTREAM:"+r.URL.Path)
	}))
	defer upstream.Close()

	px := New(testArchive(t), nil)
	addr, stop, err := px.Start(0)
	assert.NoError(t, err)
	defer func() { _ = stop() }()

	proxyURL, _ := url.Parse("http://" + addr)
	client := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)}}

	// Local suite request: served from memory, never touches the network.
	resp, err := client.Get("http://archive.ubuntu.com/ubuntu/dists/chisel-proxy/InRelease")
	assert.NoError(t, err)
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	assert.Equal(t, resp.StatusCode, 200)
	assert.ContainsString(t, string(body), "BEGIN PGP SIGNED MESSAGE")

	// Foreign suite: passed through to the (test) upstream verbatim.
	resp, err = client.Get(upstream.URL + "/ubuntu/dists/noble/InRelease")
	assert.NoError(t, err)
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	assert.Equal(t, resp.StatusCode, 200)
	assert.Equal(t, string(body), "UPSTREAM:/ubuntu/dists/noble/InRelease")

	local, up := px.Counts()
	assert.Equal(t, local, int64(1))
	assert.Equal(t, up, int64(1))
}
