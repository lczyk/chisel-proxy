// Package sign creates an ephemeral OpenPGP key and produces the two artefacts
// chisel needs: a clearsigned InRelease and a single-packet armored public key.
// The key is generated per run and never persisted or reused.
//
// We sign with the maintained github.com/ProtonMail/go-crypto fork. chisel
// verifies with the frozen golang.org/x/crypto/openpgp; the OpenPGP wire format
// is identical, so its output verifies there. To stay within what the frozen
// verifier can parse, the key is forced to RSA and version 4.
package sign

import (
	"bytes"
	"crypto"
	"fmt"

	"github.com/ProtonMail/go-crypto/openpgp"
	"github.com/ProtonMail/go-crypto/openpgp/armor"
	"github.com/ProtonMail/go-crypto/openpgp/clearsign"
	"github.com/ProtonMail/go-crypto/openpgp/packet"
)

// Signer holds one ephemeral signing key.
type Signer struct {
	entity *openpgp.Entity
	cfg    *packet.Config
}

// New generates a fresh signing key. name and email are cosmetic; chisel checks
// neither.
func New(name, email string) (*Signer, error) {
	return newWithBits(name, email, 3072)
}

func newWithBits(name, email string, bits int) (*Signer, error) {
	cfg := &packet.Config{
		Algorithm:   packet.PubKeyAlgoRSA, // RSA v4 keeps the frozen x/crypto verifier able to parse it
		RSABits:     bits,
		DefaultHash: crypto.SHA256,
		V6Keys:      false,
	}
	e, err := openpgp.NewEntity(name, "chisel-proxy ephemeral archive key", email, cfg)
	if err != nil {
		return nil, fmt.Errorf("cannot generate signing key: %w", err)
	}
	// Drop the encryption subkey: it serialises as a second public-key packet,
	// and chisel rejects armor holding more than one. Leaves the sign-capable
	// primary alone.
	e.Subkeys = nil
	return &Signer{entity: e, cfg: cfg}, nil
}

// KeyID returns the 16-hex-uppercase long key id, the form chisel matches a
// chisel.yaml public-key `id:` against (packet.PublicKey.KeyIdString).
func (s *Signer) KeyID() string {
	return s.entity.PrimaryKey.KeyIdString()
}

// ArmoredPublicKey returns the primary public key as an armored block.
func (s *Signer) ArmoredPublicKey() (string, error) {
	var buf bytes.Buffer
	w, err := armor.Encode(&buf, openpgp.PublicKeyType, nil)
	if err != nil {
		return "", err
	}
	if err := s.entity.Serialize(w); err != nil {
		return "", err
	}
	if err := w.Close(); err != nil {
		return "", err
	}
	return buf.String(), nil
}

// ClearSign wraps body in an OpenPGP clearsigned message, the InRelease form
// chisel decodes with pgputil.DecodeClearSigned.
func (s *Signer) ClearSign(body []byte) ([]byte, error) {
	var buf bytes.Buffer
	w, err := clearsign.Encode(&buf, s.entity.PrivateKey, s.cfg)
	if err != nil {
		return nil, err
	}
	if _, err := w.Write(body); err != nil {
		return nil, err
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	buf.WriteByte('\n')
	return buf.Bytes(), nil
}
