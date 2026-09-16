package cli

import (
	"os"
	"runtime"
	"testing"

	"github.com/lczyk/assert"
)

func TestParseGoVersion(t *testing.T) {
	cases := []struct {
		in           string
		major, minor int
		ok           bool
	}{
		{"go1.27.1", 1, 27, true},
		{"go1.25.14", 1, 25, true},
		{"go1.27rc1", 1, 27, true},
		{"devel go1.28-abcdef", 1, 28, true},
		{"go1", 0, 0, false},
		{"garbage", 0, 0, false},
	}
	for _, c := range cases {
		major, minor, ok := parseGoVersion(c.in)
		assert.Equal(t, ok, c.ok, c.in)
		if ok {
			assert.Equal(t, major, c.major, c.in)
			assert.Equal(t, minor, c.minor, c.in)
		}
	}
}

func TestMergeGODEBUG(t *testing.T) {
	assert.Equal(t, mergeGODEBUG("", "x=1"), "x=1")
	assert.Equal(t, mergeGODEBUG("a=0", "x=1"), "a=0,x=1")
}

// The test binary itself is built with the current toolchain (1.27+), so on
// darwin it must pass the gate; this exercises the buildinfo read path.
func TestCheckBinPlatformAcceptsModernBinary(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("bin path unsupported here")
	}
	assert.NoError(t, checkBinPlatform(os.Args[0]))
}
