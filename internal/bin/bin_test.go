package bin

import (
	"archive/tar"
	"bytes"
	"crypto/sha3"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/lczyk/assert"
	"github.com/ulikunitz/xz"
)

func TestFromDirTarXZRoundTrip(t *testing.T) {
	dir := t.TempDir()
	pkgDir := filepath.Join(dir, "demo-hello")
	assert.NoError(t, os.MkdirAll(filepath.Join(pkgDir, "usr", "bin"), 0755))
	assert.NoError(t, os.WriteFile(filepath.Join(pkgDir, "usr", "bin", "demo-hello"), []byte("hi"), 0755))

	b, err := FromDir(pkgDir, Defaults{Arch: "amd64", Version: "1.0"})
	assert.NoError(t, err)
	assert.Equal(t, b.Name, "demo-hello")
	assert.Equal(t, b.Arch, "amd64")
	assert.EqualArrays(t, b.Files, []string{"/usr/bin/demo-hello"})

	sum := sha3.Sum384(b.TarXZ)
	assert.Equal(t, b.SHA3384, hex.EncodeToString(sum[:]))

	xr, err := xz.NewReader(bytes.NewReader(b.TarXZ))
	assert.NoError(t, err)
	tr := tar.NewReader(xr)
	found := false
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		assert.NoError(t, err)
		if h.Name == "./usr/bin/demo-hello" {
			found = true
		}
	}
	assert.That(t, found, "tarball missing ./usr/bin/demo-hello")
}

func TestStoreServesInfoAndDownload(t *testing.T) {
	b := &Bin{
		Name: "demo-hello", Version: "1.0", Arch: "amd64",
		Track: "1.0-ubuntu-24.04", Risk: "stable",
		TarXZ: []byte("xzbytes"), SHA3384: "deadbeef", Size: 7,
	}
	h := NewStore([]*Bin{b}).Handler()

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "https://api.staging.snapcraft.io/v2/bins/info/demo-hello?fields=x", nil))
	assert.Equal(t, rec.Code, 200)
	var resp infoResponse
	assert.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	assert.Len(t, resp.ChannelMap, 1)
	e := resp.ChannelMap[0]
	assert.Equal(t, e.Channel.Track, "1.0-ubuntu-24.04")
	assert.Equal(t, e.Channel.Risk, "stable")
	assert.Equal(t, e.Channel.Platform.Architecture, "amd64")
	assert.Equal(t, e.Revision.Download.SHA3384, "deadbeef")
	assert.ContainsString(t, e.Revision.Download.URL, "https://storage.snapcraftcontent.com/")

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "https://storage.snapcraftcontent.com/chisel-proxy/demo-hello.tar.xz", nil))
	assert.Equal(t, rec.Code, 200)
	assert.EqualArrays(t, rec.Body.Bytes(), b.TarXZ)

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "https://api.staging.snapcraft.io/v2/bins/info/nope", nil))
	assert.Equal(t, rec.Code, 404)
}
