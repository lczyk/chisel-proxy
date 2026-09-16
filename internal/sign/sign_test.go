package sign

//lint:file-ignore SA1019 the test verifies with chisel's openpgp package (x/crypto) on purpose, to prove cross-compatibility

import (
	"bytes"
	"io"
	"testing"

	"github.com/lczyk/assert"
	"golang.org/x/crypto/openpgp/armor"
	"golang.org/x/crypto/openpgp/clearsign"
	"golang.org/x/crypto/openpgp/packet"
)

// decodeKeys mirrors chisel's internal/pgputil.DecodeKeys exactly, so these
// tests assert against the same logic chisel uses to accept our key material.
func decodeKeys(t *testing.T, armored []byte) (pub []*packet.PublicKey, priv []*packet.PrivateKey) {
	t.Helper()
	block, err := armor.Decode(bytes.NewReader(armored))
	assert.NoError(t, err)
	r := packet.NewReader(block.Body)
	for {
		p, err := r.Next()
		if err == io.EOF {
			break
		}
		assert.NoError(t, err)
		switch k := p.(type) {
		case *packet.PublicKey:
			pub = append(pub, k)
		case *packet.PrivateKey:
			priv = append(priv, k)
		}
	}
	return pub, priv
}

func TestArmoredPublicKeyIsSingleSignCapablePacket(t *testing.T) {
	s, err := newWithBits("t", "t@example.invalid", 1024)
	assert.NoError(t, err)
	armored, err := s.ArmoredPublicKey()
	assert.NoError(t, err)

	pub, priv := decodeKeys(t, []byte(armored))
	assert.Len(t, priv, 0)
	// chisel's DecodePubKey rejects >1 public-key packet (subkeys count too).
	assert.Len(t, pub, 1)
	assert.Equal(t, pub[0].KeyIdString(), s.KeyID())
	assert.That(t, pub[0].PubKeyAlgo.CanSign(), "primary key must be sign-capable")
}

func TestClearSignVerifiesAgainstExportedKey(t *testing.T) {
	s, err := newWithBits("t", "t@example.invalid", 1024)
	assert.NoError(t, err)
	body := []byte("Label: Ubuntu\nSuite: demo\nComponents: demo\n")
	signed, err := s.ClearSign(body)
	assert.NoError(t, err)

	// Mirror pgputil.DecodeClearSigned.
	block, _ := clearsign.Decode(signed)
	assert.NotNil(t, block, "clearsign.Decode returned nil block")
	var sigs []*packet.Signature
	r := packet.NewReader(block.ArmoredSignature.Body)
	for {
		p, err := r.Next()
		if err == io.EOF {
			break
		}
		assert.NoError(t, err)
		if sig, ok := p.(*packet.Signature); ok {
			sigs = append(sigs, sig)
		}
	}
	assert.That(t, len(sigs) > 0, "clearsigned data contains no signatures")

	// Mirror pgputil.VerifyAnySignature / VerifySignature.
	pub, _ := decodeKeys(t, []byte(mustArmor(t, s)))
	verified := false
	for _, sig := range sigs {
		h := sig.Hash.New()
		_, err := io.Copy(h, bytes.NewReader(block.Bytes))
		assert.NoError(t, err)
		if err := pub[0].VerifySignature(h, sig); err == nil {
			verified = true
			break
		}
	}
	assert.That(t, verified, "signature did not verify against the exported public key")
}

func mustArmor(t *testing.T, s *Signer) string {
	t.Helper()
	a, err := s.ArmoredPublicKey()
	assert.NoError(t, err)
	return a
}
