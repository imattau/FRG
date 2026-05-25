package client

import (
	"bytes"
	"path/filepath"
	"testing"

	"github.com/imattau/frg/core/keys"
)

func TestQueuePersistence(t *testing.T) {
	t.Parallel()

	kp, _ := keys.GenerateKeypair()
	qpath := filepath.Join(t.TempDir(), "queue.db")

	q, err := openQueue(qpath, kp)
	if err != nil {
		t.Fatalf("openQueue: %v", err)
	}

	txBytes := []byte("persistent tx bytes")
	if err := q.Enqueue(txBytes); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	if err := q.close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	q2, err := openQueue(qpath, kp)
	if err != nil {
		t.Fatalf("openQueue reopen: %v", err)
	}
	defer q2.close()

	_, got, err := q2.Drain()
	if err != nil {
		t.Fatalf("Drain: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 queued tx, got %d", len(got))
	}
	if !bytes.Equal(got[0], txBytes) {
		t.Fatalf("queued bytes mismatch: got %q want %q", got[0], txBytes)
	}
}

func TestQueueEncryptDecrypt(t *testing.T) {
	t.Parallel()

	kp, _ := keys.GenerateKeypair()
	qpath := filepath.Join(t.TempDir(), "queue.db")

	q, err := openQueue(qpath, kp)
	if err != nil {
		t.Fatalf("openQueue: %v", err)
	}
	defer q.close()

	txBytes := []byte("encrypted tx bytes")
	if err := q.Enqueue(txBytes); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	_, got, err := q.Drain()
	if err != nil {
		t.Fatalf("Drain: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 queued tx, got %d", len(got))
	}
	if !bytes.Equal(got[0], txBytes) {
		t.Fatalf("decrypted bytes mismatch: got %q want %q", got[0], txBytes)
	}
}

func TestQueueWrongKey(t *testing.T) {
	t.Parallel()

	kp1, _ := keys.GenerateKeypair()
	kp2, _ := keys.GenerateKeypair()
	qpath := filepath.Join(t.TempDir(), "queue.db")

	q, err := openQueue(qpath, kp1)
	if err != nil {
		t.Fatalf("openQueue: %v", err)
	}
	if err := q.Enqueue([]byte("secret tx bytes")); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	if err := q.close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	q2, err := openQueue(qpath, kp2)
	if err != nil {
		t.Fatalf("openQueue wrong key: %v", err)
	}
	defer q2.close()

	_, _, err = q2.Drain()
	if err == nil {
		t.Fatal("expected error with wrong key, got nil")
	}
}
