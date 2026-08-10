package main

import (
	"context"
	"math/big"
	"net"
	"testing"
	"time"

	"github.com/imattau/frg/core/keys"
	"github.com/imattau/frg/core/statemachine"
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
	txCh            chan *tx.Tx
	batchCh         chan []*tx.Tx
	blockCh         chan []byte
	status          *frgpb.StatusResponse
	contractState   *frgpb.ContractStateResponse
	telemetry       *frgpb.BlockTelemetryResponse
	telemetryHeight uint64
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

func (f *fakeNode) GetAccount(pubkey [32]byte) (*frgpb.AccountResponse, error) {
	return &frgpb.AccountResponse{Pubkey: pubkey[:], Balance: "0"}, nil
}

func (f *fakeNode) GetContractState(contractAddr [32]byte, key []byte) (*frgpb.ContractStateResponse, error) {
	if f.contractState != nil {
		return f.contractState, nil
	}
	return &frgpb.ContractStateResponse{
		ContractAddress: contractAddr[:],
		Key:             append([]byte(nil), key...),
	}, nil
}

func (f *fakeNode) ListValidators() (*frgpb.ValidatorList, error) {
	return &frgpb.ValidatorList{}, nil
}

func (f *fakeNode) ListMempool() (*frgpb.MempoolList, error) {
	return &frgpb.MempoolList{}, nil
}

func (f *fakeNode) GetBlockTelemetry(height uint64) (*frgpb.BlockTelemetryResponse, error) {
	f.telemetryHeight = height
	if f.telemetry != nil {
		return f.telemetry, nil
	}
	return &frgpb.BlockTelemetryResponse{Height: height}, nil
}

type nodeTestAPI interface {
	nodeAPI
	nodeStatusAPI
	nodeQueryAPI
}

func newGRPCBufconnServer(t *testing.T, node nodeTestAPI) (*grpc.ClientConn, func()) {
	t.Helper()

	lis := bufconn.Listen(1024 * 1024)
	server := grpc.NewServer()
	frgpb.RegisterFRGServer(server, &nodeGRPCServer{node: node, stat: node, query: node})

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

	resp, err := client.SubmitTx(context.Background(), &frgpb.RawBytes{Data: raw})
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

	resp, err := client.SubmitBatch(context.Background(), &frgpb.RawBytesArray{Data: [][]byte{tx1, tx2}})
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
	stream, err := client.SubscribeBlocks(context.Background(), &frgpb.Empty{})
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
	resp, err := client.GetStatus(context.Background(), &frgpb.Empty{})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Height != 11 || resp.ConsensusPhase != "commit" {
		t.Fatalf("unexpected status response: %+v", resp)
	}
}

func TestNodeGRPCGetContractState(t *testing.T) {
	var addr [32]byte
	for i := range addr {
		addr[i] = byte(100 + i)
	}
	node := &fakeNode{
		txCh:    make(chan *tx.Tx, 1),
		batchCh: make(chan []*tx.Tx, 1),
		blockCh: make(chan []byte, 1),
		contractState: &frgpb.ContractStateResponse{
			ContractAddress: addr[:],
			Exists:          true,
			StateRoot:       []byte{0xaa},
			Key:             []byte("count"),
			Found:           true,
			Value:           []byte{9},
		},
	}
	conn, cleanup := newGRPCBufconnServer(t, node)
	defer cleanup()

	client := frgpb.NewFRGClient(conn)
	resp, err := client.GetContractState(context.Background(), &frgpb.ContractStateRequest{
		ContractAddress: addr[:],
		Key:             []byte("count"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !resp.Exists || !resp.Found || len(resp.Value) != 1 || resp.Value[0] != 9 {
		t.Fatalf("unexpected contract state response: %+v", resp)
	}
}

func TestNodeGRPCGetBlockTelemetry(t *testing.T) {
	node := &fakeNode{
		txCh:    make(chan *tx.Tx, 1),
		batchCh: make(chan []*tx.Tx, 1),
		blockCh: make(chan []byte, 1),
		telemetry: &frgpb.BlockTelemetryResponse{
			Height:     9,
			TxCount:    2,
			TotalValue: "300",
			Levels: []*frgpb.RGLevelTelemetry{
				{Level: 0, Scale: 1, NodeCount: 2},
			},
		},
	}
	conn, cleanup := newGRPCBufconnServer(t, node)
	defer cleanup()

	client := frgpb.NewFRGClient(conn)
	resp, err := client.GetBlockTelemetry(context.Background(), &frgpb.BlockTelemetryRequest{Height: 9})
	if err != nil {
		t.Fatal(err)
	}
	if node.telemetryHeight != 9 {
		t.Fatalf("height passed to node = %d, want 9", node.telemetryHeight)
	}
	if resp.Height != 9 || resp.TxCount != 2 || len(resp.Levels) != 1 {
		t.Fatalf("unexpected telemetry response: %+v", resp)
	}
}

func TestBlockTelemetryBuildsRGSummary(t *testing.T) {
	senderKP := keys.NewKeypairFromSeed([32]byte{1})
	receiverKP := keys.NewKeypairFromSeed([32]byte{2})
	block := &statemachine.Block{
		Height: 7,
		Txs: []*tx.Tx{
			signedTransferTx(t, senderKP, receiverKP, 1, 100),
			signedTransferTx(t, senderKP, receiverKP, 2, 200),
		},
	}
	block.StateRoot[0] = 0xaa
	block.ProposerPubKey[0] = 0xbb

	resp, err := blockTelemetry(block)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Height != 7 || resp.TxCount != 2 || resp.TotalValue != "300" || resp.MeanValue != "150" {
		t.Fatalf("unexpected telemetry totals: %+v", resp)
	}
	if len(resp.TxTypes) != 1 || resp.TxTypes[0].Name != "transfer" || resp.TxTypes[0].Count != 2 {
		t.Fatalf("unexpected tx type counts: %+v", resp.TxTypes)
	}
	if len(resp.Levels) == 0 || resp.Levels[0].NodeCount != 2 || len(resp.Levels[0].SignatureCounts) == 0 {
		t.Fatalf("missing RG level telemetry: %+v", resp.Levels)
	}
}
