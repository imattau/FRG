package client

import (
	"bytes"
	"context"
	"testing"
	"time"
)

func TestTransportSubmitTx(t *testing.T) {
	srv := newTestFRGServer()
	tr, cleanup := newBufconnTransport(t, srv)
	defer cleanup()

	senderKP, receiverKP := newTestKeypairs(t)
	tx := signedTx(t, senderKP, receiverKP, "alice", "bob", 1, 0)
	raw, err := tx.Serialize()
	if err != nil {
		t.Fatalf("Serialize: %v", err)
	}

	if err := tr.submitTx(context.Background(), raw); err != nil {
		t.Fatalf("submitTx: %v", err)
	}

	got := waitForBytes(t, srv.submitTxCh)
	if !bytes.Equal(got, raw) {
		t.Fatalf("submitTx bytes mismatch")
	}
}

func TestTransportSubmitBatch(t *testing.T) {
	srv := newTestFRGServer()
	tr, cleanup := newBufconnTransport(t, srv)
	defer cleanup()

	batch := [][]byte{[]byte("tx1"), []byte("tx2")}
	if err := tr.submitBatch(context.Background(), batch); err != nil {
		t.Fatalf("submitBatch: %v", err)
	}

	got := waitForBytes(t, srv.submitBatchCh)
	if len(got) != len(batch) {
		t.Fatalf("batch length mismatch: got %d want %d", len(got), len(batch))
	}
	for i := range batch {
		if !bytes.Equal(got[i], batch[i]) {
			t.Fatalf("batch item %d mismatch", i)
		}
	}
}

func TestTransportDisconnect(t *testing.T) {
	tr, err := dialTransport("127.0.0.1:1")
	if err != nil {
		t.Fatalf("dialTransport: %v", err)
	}
	defer tr.close()

	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()

	err = tr.submitTx(ctx, []byte("tx"))
	if err == nil {
		t.Fatal("expected transport error, got nil")
	}
}

func TestTransportSubscribe(t *testing.T) {
	srv := newTestFRGServer()
	tr, cleanup := newBufconnTransport(t, srv)
	defer cleanup()

	srv.streamCh <- []byte("block-bytes")
	close(srv.streamCh)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	ch, err := tr.subscribe(ctx)
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	got := waitForBytes(t, ch)
	if !bytes.Equal(got, []byte("block-bytes")) {
		t.Fatalf("subscribe bytes mismatch: %q", got)
	}
}
