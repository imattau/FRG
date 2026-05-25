package keys_test

import (
	"bytes"
	"testing"

	"github.com/imattau/frg/core/keys"
)

func TestGenerateKeypair(t *testing.T) {
	kp, err := keys.GenerateKeypair()
	if err != nil {
		t.Fatalf("GenerateKeypair() error: %v", err)
	}
	if kp == nil {
		t.Fatal("GenerateKeypair() returned nil keypair")
	}

	allZeroPub := true
	for _, b := range kp.PublicKey {
		if b != 0 {
			allZeroPub = false
			break
		}
	}
	if allZeroPub {
		t.Fatal("PublicKey is all zeros")
	}

	allZeroPriv := true
	for _, b := range kp.PrivateKey {
		if b != 0 {
			allZeroPriv = false
			break
		}
	}
	if allZeroPriv {
		t.Fatal("PrivateKey is all zeros")
	}
}

func TestSignVerify(t *testing.T) {
	kp, err := keys.GenerateKeypair()
	if err != nil {
		t.Fatalf("GenerateKeypair() error: %v", err)
	}
	msg := []byte("hello frg")
	sig, err := kp.Sign(msg)
	if err != nil {
		t.Fatalf("Sign() error: %v", err)
	}
	if !keys.Verify(kp.PublicKey, msg, sig) {
		t.Fatal("Verify() returned false for a valid signature")
	}
}

func TestVerifyWrongKey(t *testing.T) {
	kp1, err := keys.GenerateKeypair()
	if err != nil {
		t.Fatalf("GenerateKeypair() error: %v", err)
	}
	kp2, err := keys.GenerateKeypair()
	if err != nil {
		t.Fatalf("GenerateKeypair() error: %v", err)
	}
	msg := []byte("hello frg")
	sig, err := kp1.Sign(msg)
	if err != nil {
		t.Fatalf("Sign() error: %v", err)
	}
	if keys.Verify(kp2.PublicKey, msg, sig) {
		t.Fatal("Verify() returned true with wrong public key")
	}
}

func TestVerifyTamperedMsg(t *testing.T) {
	kp, err := keys.GenerateKeypair()
	if err != nil {
		t.Fatalf("GenerateKeypair() error: %v", err)
	}
	msg := []byte("hello frg")
	sig, err := kp.Sign(msg)
	if err != nil {
		t.Fatalf("Sign() error: %v", err)
	}
	tampered := bytes.Clone(msg)
	tampered[0] ^= 0xFF
	if keys.Verify(kp.PublicKey, tampered, sig) {
		t.Fatal("Verify() returned true with tampered message")
	}
}
