package client

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSubmitTxQueued(t *testing.T) {
	senderKP, receiverKP := newTestKeypairs(t)
	qpath := filepath.Join(t.TempDir(), "queue.db")

	c, err := New("127.0.0.1:1", senderKP, qpath)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer c.Close()

	tx := signedTx(t, senderKP, receiverKP, "alice", "bob", 1, 0)
	if err := c.SubmitTx(context.Background(), tx); err != nil {
		t.Fatalf("SubmitTx: %v", err)
	}

	_, got, err := c.q.Drain()
	if err != nil {
		t.Fatalf("Drain: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 queued tx, got %d", len(got))
	}

	expected, err := tx.Serialize()
	if err != nil {
		t.Fatalf("Serialize: %v", err)
	}
	if !bytes.Equal(got[0], expected) {
		t.Fatalf("queued bytes mismatch")
	}
}

func TestQueueFlushOnReconnect(t *testing.T) {
	senderKP, receiverKP := newTestKeypairs(t)
	srv := newTestFRGServer()
	tr, cleanup := newBufconnTransport(t, srv)
	defer cleanup()

	qpath := filepath.Join(t.TempDir(), "queue.db")
	q, err := openQueue(qpath, senderKP)
	if err != nil {
		t.Fatalf("openQueue: %v", err)
	}
	defer q.close()

	c := &Client{
		kp: senderKP,
		q:  q,
		t:  tr,
	}

	tx := signedTx(t, senderKP, receiverKP, "alice", "bob", 1, 0)
	raw, err := tx.Serialize()
	if err != nil {
		t.Fatalf("Serialize: %v", err)
	}
	if err := c.q.Enqueue(raw); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	c.flush(context.Background())

	got := waitForBytes(t, srv.submitBatchCh)
	if len(got) != 1 {
		t.Fatalf("expected 1 tx in batch, got %d", len(got))
	}
	if !bytes.Equal(got[0], raw) {
		t.Fatalf("batch bytes mismatch")
	}

	_, remaining, err := c.q.Drain()
	if err != nil {
		t.Fatalf("Drain after flush: %v", err)
	}
	if len(remaining) != 0 {
		t.Fatalf("expected empty queue after flush, got %d", len(remaining))
	}
}

func TestSubscribeReceivesBlock(t *testing.T) {
	srv := newTestFRGServer()
	tr, cleanup := newBufconnTransport(t, srv)
	defer cleanup()

	srv.streamCh <- []byte("block-bytes")
	close(srv.streamCh)

	c := &Client{
		t: tr,
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	ch, err := c.Subscribe(ctx)
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	select {
	case blk := <-ch:
		if blk == nil {
			t.Fatal("expected block, got nil")
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for block")
	}
}

func TestEncryptedQueuePrivacy(t *testing.T) {
	senderKP, receiverKP := newTestKeypairs(t)
	qpath := filepath.Join(t.TempDir(), "queue.db")

	c, err := New("127.0.0.1:1", senderKP, qpath)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer c.Close()

	tx := signedTx(t, senderKP, receiverKP, "alice", "bob", 1, 0)
	if err := c.SubmitTx(context.Background(), tx); err != nil {
		t.Fatalf("SubmitTx: %v", err)
	}

	fileBytes, err := os.ReadFile(qpath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if bytes.Contains(fileBytes, []byte("alice")) || bytes.Contains(fileBytes, []byte("bob")) {
		t.Fatal("queue file contains plaintext tx data")
	}
}
