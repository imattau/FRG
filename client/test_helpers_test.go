package client

import (
	"context"
	"math/big"
	"net"
	"testing"
	"time"

	"github.com/imattau/frg/core/keys"
	"github.com/imattau/frg/core/tx"
	frgpb "github.com/imattau/frg/proto"
	"google.golang.org/grpc"
	"google.golang.org/grpc/test/bufconn"
)

func signedTx(t *testing.T, senderKP, receiverKP *keys.Keypair, sender, receiver string, value int64, nonce uint64) *tx.Tx {
	t.Helper()

	scale := new(big.Int).Exp(big.NewInt(10), big.NewInt(18), nil)
	txObj := &tx.Tx{
		Sender:         sender,
		Receiver:       receiver,
		Value:          new(big.Int).Mul(big.NewInt(value), scale),
		Nonce:          nonce,
		SenderPubKey:   senderKP.PublicKey,
		ReceiverPubKey: receiverKP.PublicKey,
	}
	msg, err := txObj.UnsignedHash()
	if err != nil {
		t.Fatalf("UnsignedHash() error: %v", err)
	}
	sig, err := senderKP.Sign(msg[:])
	if err != nil {
		t.Fatalf("Sign sender: %v", err)
	}
	rsig, err := receiverKP.Sign(msg[:])
	if err != nil {
		t.Fatalf("Sign receiver: %v", err)
	}
	txObj.SenderSig = sig
	txObj.ReceiverSig = rsig
	return txObj
}

func newTestKeypairs(t *testing.T) (*keys.Keypair, *keys.Keypair) {
	t.Helper()
	senderKP, err := keys.GenerateKeypair()
	if err != nil {
		t.Fatalf("GenerateKeypair sender: %v", err)
	}
	receiverKP, err := keys.GenerateKeypair()
	if err != nil {
		t.Fatalf("GenerateKeypair receiver: %v", err)
	}
	return senderKP, receiverKP
}

type testFRGServer struct {
	frgpb.UnimplementedFRGServer
	submitTxCh    chan []byte
	submitBatchCh chan [][]byte
	streamCh      chan []byte
}

func newTestFRGServer() *testFRGServer {
	return &testFRGServer{
		submitTxCh:    make(chan []byte, 16),
		submitBatchCh: make(chan [][]byte, 16),
		streamCh:      make(chan []byte, 16),
	}
}

func (s *testFRGServer) SubmitTx(ctx context.Context, in *frgpb.RawBytes) (*frgpb.SubmitResponse, error) {
	data := append([]byte(nil), in.Data...)
	s.submitTxCh <- data
	return &frgpb.SubmitResponse{Ok: true}, nil
}

func (s *testFRGServer) SubmitBatch(ctx context.Context, in *frgpb.RawBytesArray) (*frgpb.SubmitResponse, error) {
	batch := make([][]byte, len(in.Data))
	for i := range in.Data {
		batch[i] = append([]byte(nil), in.Data[i]...)
	}
	s.submitBatchCh <- batch
	return &frgpb.SubmitResponse{Ok: true}, nil
}

func (s *testFRGServer) SubscribeBlocks(in *frgpb.Empty, stream frgpb.FRG_SubscribeBlocksServer) error {
	for data := range s.streamCh {
		if err := stream.Send(&frgpb.RawBytes{Data: append([]byte(nil), data...)}); err != nil {
			return err
		}
		break
	}
	return nil
}

func newBufconnTransport(t *testing.T, srv *testFRGServer) (*transport, func()) {
	t.Helper()
	lis := bufconn.Listen(1024 * 1024)
	server := grpc.NewServer()
	frgpb.RegisterFRGServer(server, srv)
	go func() {
		_ = server.Serve(lis)
	}()

	dialer := func(context.Context, string) (net.Conn, error) {
		return lis.Dial()
	}

	tr, err := dialTransport(
		"bufnet",
		grpc.WithContextDialer(dialer),
		grpc.WithBlock(),
	)
	if err != nil {
		t.Fatalf("dialTransport: %v", err)
	}

	cleanup := func() {
		_ = tr.close()
		server.Stop()
		_ = lis.Close()
	}
	return tr, cleanup
}

func waitForBytes[T any](t *testing.T, ch <-chan T) T {
	t.Helper()
	select {
	case v := <-ch:
		return v
	case <-time.After(3 * time.Second):
		t.Fatal("timeout waiting for value")
		var zero T
		return zero
	}
}
