package cli

import (
	"testing"

	"github.com/lczyk/assert"
)

func TestParseArgsCutSplit(t *testing.T) {
	f, pos, fwd, err := parseArgs(
		[]string{"--port", "1234", "--name", "x", "a.deb", "dir", "--", "slice_a", "--root", "/r"}, true)
	assert.NoError(t, err)
	assert.Equal(t, f.port, 1234)
	assert.Equal(t, f.name, "x")
	assert.EqualArrays(t, pos, []string{"a.deb", "dir"})
	assert.EqualArrays(t, fwd, []string{"slice_a", "--root", "/r"})
}

func TestParseArgsEqualsForm(t *testing.T) {
	f, _, _, err := parseArgs([]string{"--port=8", "--version=1.2"}, false)
	assert.NoError(t, err)
	assert.Equal(t, f.port, 8)
	assert.Equal(t, f.version, "1.2")
}

func TestParseArgsUnknownFlag(t *testing.T) {
	_, _, _, err := parseArgs([]string{"--nope"}, false)
	assert.Error(t, err, assert.AnyError)
}

func TestParseArgsBadPort(t *testing.T) {
	_, _, _, err := parseArgs([]string{"--port", "abc"}, false)
	assert.Error(t, err, assert.AnyError)
}

func TestParseArgsDashDashLast(t *testing.T) {
	_, pos, fwd, err := parseArgs([]string{"a.deb", "--"}, true)
	assert.NoError(t, err)
	assert.EqualArrays(t, pos, []string{"a.deb"})
	assert.Len(t, fwd, 0)
}
