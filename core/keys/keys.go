package keys

import (
	"crypto/ed25519"
	"crypto/rand"
	"fmt"
)

// Keypair holds an Ed25519 public/private key pair.
type Keypair struct {
	PublicKey  [32]byte
	PrivateKey [64]byte
}

// GenerateKeypair generates a new random Ed25519 keypair.
func GenerateKeypair() (*Keypair, error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generate keypair: %w", err)
	}

	kp := &Keypair{}
	copy(kp.PublicKey[:], pub)
	copy(kp.PrivateKey[:], priv)
	return kp, nil
}

// Sign signs msg with the private key. Returns a 64-byte signature.
func (k *Keypair) Sign(msg []byte) ([64]byte, error) {
	priv := ed25519.PrivateKey(k.PrivateKey[:])
	sig := ed25519.Sign(priv, msg)
	var out [64]byte
	copy(out[:], sig)
	return out, nil
}

// NewKeypairFromSeed creates a keypair from a 32-byte seed.
func NewKeypairFromSeed(seed [32]byte) *Keypair {
    priv := ed25519.NewKeyFromSeed(seed[:])
    pub := priv.Public().(ed25519.PublicKey)
    kp := &Keypair{}
    copy(kp.PublicKey[:], pub)
    copy(kp.PrivateKey[:], priv)
    return kp
}

// NewKeypairFromPrivateKey creates a keypair from a 64-byte private key.
func NewKeypairFromPrivateKey(privKey [64]byte) *Keypair {
    priv := ed25519.PrivateKey(privKey[:])
    pub := priv.Public().(ed25519.PublicKey)
    kp := &Keypair{}
    copy(kp.PublicKey[:], pub)
    copy(kp.PrivateKey[:], priv)
    return kp
}

// Verify verifies a 64-byte signature against msg using pubKey.
func Verify(pubKey [32]byte, msg []byte, sig [64]byte) bool {
	return ed25519.Verify(ed25519.PublicKey(pubKey[:]), msg, sig[:])
}
