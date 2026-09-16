package proxy

import (
	"crypto/x509"
	"testing"

	"github.com/lczyk/assert"
)

// TestCertAuthorityLeafVerifies proves a minted leaf chains to the CA the way
// chisel's TLS client will verify it: the CA PEM (handed over via SSL_CERT_FILE)
// as the only root, matched by SNI hostname.
func TestCertAuthorityLeafVerifies(t *testing.T) {
	ca, err := newCertAuthority()
	assert.NoError(t, err)
	leaf, err := ca.leafFor("api.staging.snapcraft.io")
	assert.NoError(t, err)

	pool := x509.NewCertPool()
	assert.That(t, pool.AppendCertsFromPEM(ca.CertPEM()), "CA PEM did not parse")
	leafCert, err := x509.ParseCertificate(leaf.Certificate[0])
	assert.NoError(t, err)
	_, err = leafCert.Verify(x509.VerifyOptions{
		DNSName: "api.staging.snapcraft.io",
		Roots:   pool,
	})
	assert.NoError(t, err)
}
