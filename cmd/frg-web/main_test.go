package main

import (
	"context"
	"encoding/hex"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/imattau/frg/core/keys"
	"github.com/imattau/frg/core/tx"
	frgpb "github.com/imattau/frg/proto"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
)

type webTestServer struct {
	frgpb.UnimplementedFRGServer
	submitTxCh    chan []byte
	submitBatchCh chan [][]byte
	blockCh       chan []byte
	status        *frgpb.StatusResponse
}

func (s *webTestServer) SubmitTx(ctx context.Context, in *frgpb.RawBytes) (*frgpb.SubmitResponse, error) {
	s.submitTxCh <- append([]byte(nil), in.Data...)
	return &frgpb.SubmitResponse{Ok: true}, nil
}

func (s *webTestServer) SubmitBatch(ctx context.Context, in *frgpb.RawBytesArray) (*frgpb.SubmitResponse, error) {
	batch := make([][]byte, len(in.Data))
	for i := range in.Data {
		batch[i] = append([]byte(nil), in.Data[i]...)
	}
	s.submitBatchCh <- batch
	return &frgpb.SubmitResponse{Ok: true}, nil
}

func (s *webTestServer) SubscribeBlocks(_ *frgpb.Empty, stream frgpb.FRG_SubscribeBlocksServer) error {
	for {
		select {
		case <-stream.Context().Done():
			return nil
		case data := <-s.blockCh:
			if err := stream.Send(&frgpb.RawBytes{Data: data}); err != nil {
				return err
			}
			return nil
		}
	}
}

func (s *webTestServer) GetStatus(context.Context, *frgpb.Empty) (*frgpb.StatusResponse, error) {
	if s.status != nil {
		return s.status, nil
	}
	return &frgpb.StatusResponse{}, nil
}

func TestHandleSubmitTx(t *testing.T) {
	_, dial, srv := startWebTestGRPC(t)

	senderKP := keys.NewKeypairFromSeed([32]byte{1})
	receiverKP := keys.NewKeypairFromSeed([32]byte{2})
	txBytes := mustSignedTx(t, senderKP, receiverKP, 1, 100)
	raw, err := txBytes.Serialize()
	if err != nil {
		t.Fatal(err)
	}

	app := &server{defaultAddr: "bufconn", dial: dial}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/submit-tx", app.handleSubmitTx)
	req := httptest.NewRequest(http.MethodPost, "/api/submit-tx?addr=bufconn", strings.NewReader(`{"tx_hex":"`+hex.EncodeToString(raw)+`"}`))
	rec := httptest.NewRecorder()

	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `"ok":true`) {
		t.Fatalf("unexpected response body: %s", rec.Body.String())
	}
	select {
	case got := <-srv.submitTxCh:
		if string(got) != string(raw) {
			t.Fatalf("unexpected tx payload")
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for tx submit")
	}
}

func TestHandleSubmitBatch(t *testing.T) {
	_, dial, srv := startWebTestGRPC(t)

	senderKP := keys.NewKeypairFromSeed([32]byte{1})
	receiverKP := keys.NewKeypairFromSeed([32]byte{2})
	tx1 := mustSignedTx(t, senderKP, receiverKP, 1, 100)
	tx2 := mustSignedTx(t, senderKP, receiverKP, 2, 200)
	raw1, err := tx1.Serialize()
	if err != nil {
		t.Fatal(err)
	}
	raw2, err := tx2.Serialize()
	if err != nil {
		t.Fatal(err)
	}

	app := &server{defaultAddr: "bufconn", dial: dial}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/submit-batch", app.handleSubmitBatch)
	req := httptest.NewRequest(http.MethodPost, "/api/submit-batch?addr=bufconn", strings.NewReader(`{"tx_hexes":["`+hex.EncodeToString(raw1)+`","`+hex.EncodeToString(raw2)+`"]}`))
	rec := httptest.NewRecorder()

	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `"ok":true`) {
		t.Fatalf("unexpected response body: %s", rec.Body.String())
	}
	select {
	case got := <-srv.submitBatchCh:
		if len(got) != 2 {
			t.Fatalf("unexpected batch length: %d", len(got))
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for batch submit")
	}
}

func TestHandleBlocks(t *testing.T) {
	_, dial, srv := startWebTestGRPC(t)
	srv.blockCh <- []byte("block-header")
	app := &server{defaultAddr: "bufconn", dial: dial}

	mux := http.NewServeMux()
	mux.HandleFunc("/api/blocks", app.handleBlocks)
	req := httptest.NewRequest(http.MethodGet, "/api/blocks?addr=bufconn", nil)
	rec := httptest.NewRecorder()

	done := make(chan struct{})
	go func() {
		defer close(done)
		mux.ServeHTTP(rec, req)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for stream response")
	}

	if !strings.Contains(rec.Body.String(), `"hex":"626c6f636b2d686561646572"`) {
		t.Fatalf("unexpected SSE body: %s", rec.Body.String())
	}
}

func TestHandleStatus(t *testing.T) {
	_, dial, srv := startWebTestGRPC(t)
	srv.status = &frgpb.StatusResponse{
		Height:         9,
		StateRoot:      []byte{0xde, 0xad, 0xbe, 0xef},
		PeerCount:      3,
		MempoolLen:     4,
		ValidatorCount: 5,
		ConsensusRound: 6,
		ConsensusPhase: "precommit",
		GrpcOnly:       true,
	}

	app := &server{defaultAddr: "bufconn", dial: dial}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/status", app.handleStatus)
	req := httptest.NewRequest(http.MethodGet, "/api/status?addr=bufconn", nil)
	rec := httptest.NewRecorder()

	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `"height":9`) {
		t.Fatalf("unexpected response body: %s", rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"state_root_hex":"deadbeef"`) {
		t.Fatalf("unexpected response body: %s", rec.Body.String())
	}
}

func startWebTestGRPC(t *testing.T) (*grpc.Server, func(context.Context, string) (*grpc.ClientConn, error), *webTestServer) {
	t.Helper()

	lis := bufconn.Listen(1024 * 1024)
	srv := &webTestServer{
		submitTxCh:    make(chan []byte, 1),
		submitBatchCh: make(chan [][]byte, 1),
		blockCh:       make(chan []byte, 1),
	}
	server := grpc.NewServer()
	frgpb.RegisterFRGServer(server, srv)
	go func() {
		_ = server.Serve(lis)
	}()

	dial := func(ctx context.Context, addr string) (*grpc.ClientConn, error) {
		_ = addr
		return grpc.DialContext(
			ctx,
			"bufconn",
			grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) {
				return lis.Dial()
			}),
			grpc.WithTransportCredentials(insecure.NewCredentials()),
			grpc.WithDefaultCallOptions(grpc.CallContentSubtype("frg-json")),
		)
	}

	t.Cleanup(func() {
		server.Stop()
		_ = lis.Close()
	})
	return server, dial, srv
}

func mustSignedTx(t *testing.T, senderKP, receiverKP *keys.Keypair, nonce uint64, value int64) *tx.Tx {
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

	hash, err := msg.UnsignedHash()
	if err != nil {
		t.Fatal(err)
	}
	sig1, err := senderKP.Sign(hash[:])
	if err != nil {
		t.Fatal(err)
	}
	sig2, err := receiverKP.Sign(hash[:])
	if err != nil {
		t.Fatal(err)
	}
	msg.SenderSig = sig1
	msg.ReceiverSig = sig2
	return msg
}
