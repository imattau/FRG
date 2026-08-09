package main

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
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
)

type fakeNode struct {
	txCh    chan *tx.Tx
	batchCh chan []*tx.Tx
	blockCh chan []byte
	status  *frgpb.StatusResponse
}

func (f *fakeNode) BroadcastTx(t *tx.Tx) error {
	f.txCh <- t
	return nil
}

func (f *fakeNode) BroadcastBatch(txs []*tx.Tx) error {
	f.batchCh <- txs
	return nil
}

func (f *fakeNode) SubscribeBlockHeaders() <-chan []byte {
	return f.blockCh
}

func (f *fakeNode) Status() (*frgpb.StatusResponse, error) {
	if f.status != nil {
		return f.status, nil
	}
	return &frgpb.StatusResponse{}, nil
}

type nodeTestAPI interface {
	nodeAPI
	nodeStatusAPI
}

func newGRPCBufconnServer(t *testing.T, node nodeTestAPI) (*grpc.ClientConn, func()) {
	t.Helper()

	lis := bufconn.Listen(1024 * 1024)
	server := grpc.NewServer()
	frgpb.RegisterFRGServer(server, &nodeGRPCServer{node: node, stat: node})

	go func() {
		_ = server.Serve(lis)
	}()

	dialer := func(context.Context, string) (net.Conn, error) {
		return lis.Dial()
	}

	conn, err := grpc.DialContext(
		context.Background(),
		"bufconn",
		grpc.WithContextDialer(dialer),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithBlock(),
	)
	if err != nil {
		t.Fatalf("dial bufconn: %v", err)
	}

	cleanup := func() {
		_ = conn.Close()
		server.Stop()
		_ = lis.Close()
	}
	return conn, cleanup
}

func signedTransferTx(t *testing.T, senderKP, receiverKP *keys.Keypair, nonce uint64, value int64) *tx.Tx {
	t.Helper()

	msg := &tx.Tx{
		Type:           tx.TxTypeTransfer,
		Sender:         "alice",
		Receiver:       "bob",
		Value:          big.NewInt(value),
		Nonce:          nonce,
		SenderPubKey:   senderKP.PublicKey,
		ReceiverPubKey: receiverKP.PublicKey,
	}

	msgHash, err := msg.UnsignedHash()
	if err != nil {
		t.Fatal(err)
	}
	sig1, err := senderKP.Sign(msgHash[:])
	if err != nil {
		t.Fatal(err)
	}
	sig2, err := receiverKP.Sign(msgHash[:])
	if err != nil {
		t.Fatal(err)
	}
	msg.SenderSig = sig1
	msg.ReceiverSig = sig2
	return msg
}

func TestNodeGRPCSubmitTx(t *testing.T) {
	senderKP := keys.NewKeypairFromSeed([32]byte{1})
	receiverKP := keys.NewKeypairFromSeed([32]byte{2})
	node := &fakeNode{
		txCh:    make(chan *tx.Tx, 1),
		batchCh: make(chan []*tx.Tx, 1),
		blockCh: make(chan []byte, 1),
	}
	conn, cleanup := newGRPCBufconnServer(t, node)
	defer cleanup()

	client := frgpb.NewFRGClient(conn)
	raw, err := signedTransferTx(t, senderKP, receiverKP, 1, 100).Serialize()
	if err != nil {
		t.Fatal(err)
	}

	resp, err := client.SubmitTx(context.Background(), &frgpb.RawBytes{Data: raw}, grpc.CallContentSubtype("frg-json"))
	if err != nil {
		t.Fatal(err)
	}
	if !resp.Ok {
		t.Fatalf("expected ok response, got %+v", resp)
	}

	select {
	case got := <-node.txCh:
		if got.Nonce != 1 {
			t.Fatalf("unexpected tx nonce: %d", got.Nonce)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for broadcast tx")
	}
}

func TestNodeGRPCSubmitBatch(t *testing.T) {
	senderKP := keys.NewKeypairFromSeed([32]byte{1})
	receiverKP := keys.NewKeypairFromSeed([32]byte{2})
	node := &fakeNode{
		txCh:    make(chan *tx.Tx, 1),
		batchCh: make(chan []*tx.Tx, 1),
		blockCh: make(chan []byte, 1),
	}
	conn, cleanup := newGRPCBufconnServer(t, node)
	defer cleanup()

	client := frgpb.NewFRGClient(conn)
	tx1, err := signedTransferTx(t, senderKP, receiverKP, 1, 100).Serialize()
	if err != nil {
		t.Fatal(err)
	}
	tx2, err := signedTransferTx(t, senderKP, receiverKP, 2, 200).Serialize()
	if err != nil {
		t.Fatal(err)
	}

	resp, err := client.SubmitBatch(context.Background(), &frgpb.RawBytesArray{Data: [][]byte{tx1, tx2}}, grpc.CallContentSubtype("frg-json"))
	if err != nil {
		t.Fatal(err)
	}
	if !resp.Ok {
		t.Fatalf("expected ok response, got %+v", resp)
	}

	select {
	case got := <-node.batchCh:
		if len(got) != 2 {
			t.Fatalf("unexpected batch len: %d", len(got))
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for broadcast batch")
	}
}

func TestSubmitLimiterBoundsRequestsPerPeer(t *testing.T) {
	now := time.Unix(100, 0)
	limiter := newSubmitLimiter()
	limiter.now = func() time.Time { return now }
	ctx := peer.NewContext(context.Background(), &peer.Peer{Addr: &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 50051}})

	for i := 0; i < maxSubmitReqsPerPeer; i++ {
		if err := limiter.allow(ctx, 1); err != nil {
			t.Fatalf("request %d unexpectedly rejected: %v", i, err)
		}
	}
	if err := limiter.allow(ctx, 1); status.Code(err) != codes.ResourceExhausted {
		t.Fatalf("want ResourceExhausted after request limit, got %v", err)
	}

	now = now.Add(submitWindow)
	if err := limiter.allow(ctx, maxSubmitTxPerPeer); err != nil {
		t.Fatalf("new window unexpectedly rejected: %v", err)
	}
}

func TestNodeGRPCSubscribeBlocks(t *testing.T) {
	node := &fakeNode{
		txCh:    make(chan *tx.Tx, 1),
		batchCh: make(chan []*tx.Tx, 1),
		blockCh: make(chan []byte, 1),
	}
	conn, cleanup := newGRPCBufconnServer(t, node)
	defer cleanup()

	client := frgpb.NewFRGClient(conn)
	stream, err := client.SubscribeBlocks(context.Background(), &frgpb.Empty{}, grpc.CallContentSubtype("frg-json"))
	if err != nil {
		t.Fatal(err)
	}

	want := []byte("block-header")
	node.blockCh <- want

	got, err := stream.Recv()
	if err != nil {
		t.Fatal(err)
	}
	if string(got.Data) != string(want) {
		t.Fatalf("unexpected block data: %q", got.Data)
	}
}

func TestNodeGRPCGetStatus(t *testing.T) {
	node := &fakeNode{
		txCh:    make(chan *tx.Tx, 1),
		batchCh: make(chan []*tx.Tx, 1),
		blockCh: make(chan []byte, 1),
		status: &frgpb.StatusResponse{
			Height:         11,
			StateRoot:      []byte{0xca, 0xfe},
			PeerCount:      2,
			MempoolLen:     7,
			ValidatorCount: 3,
			ConsensusRound: 4,
			ConsensusPhase: "commit",
			GrpcOnly:       false,
		},
	}
	conn, cleanup := newGRPCBufconnServer(t, node)
	defer cleanup()

	client := frgpb.NewFRGClient(conn)
	resp, err := client.GetStatus(context.Background(), &frgpb.Empty{}, grpc.CallContentSubtype("frg-json"))
	if err != nil {
		t.Fatal(err)
	}
	if resp.Height != 11 || resp.ConsensusPhase != "commit" {
		t.Fatalf("unexpected status response: %+v", resp)
	}
}
