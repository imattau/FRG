package main

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"math/big"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/imattau/frg/core/keys"
	"github.com/imattau/frg/core/tx"
	frgpb "github.com/imattau/frg/proto"
	bolt "go.etcd.io/bbolt"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

const defaultFaucetAmount = 100

var (
	requestsBucket = []byte("requests")
)

type faucet struct {
	kp       *keys.Keypair
	db       *bolt.DB
	nodeAddr string
	mu       sync.Mutex
}

type faucetRequest struct {
	Pubkey string `json:"pubkey"`
}

type faucetResponse struct {
	Ok           bool   `json:"ok"`
	Txid         string `json:"txid,omitempty"`
	Error        string `json:"error,omitempty"`
	ReceiverSeed string `json:"receiver_seed,omitempty"`
}

func main() {
	keyPath := flag.String("key", "faucet.key", "faucet keypair seed (32 bytes)")
	dbPath := flag.String("db", "faucet.db", "faucet rate-limit database")
	nodeAddr := flag.String("node", "127.0.0.1:50051", "FRG node gRPC address")
	listenAddr := flag.String("listen", "0.0.0.0:8088", "HTTP listen address")
	flag.Parse()

	kp, err := loadKeypair(*keyPath)
	if err != nil {
		log.Fatalf("load keypair: %v", err)
	}
	log.Printf("Faucet pubkey: %x", kp.PublicKey[:])

	db, err := bolt.Open(*dbPath, 0600, nil)
	if err != nil {
		log.Fatalf("open db: %v", err)
	}
	defer db.Close()

	if err := db.Update(func(tx *bolt.Tx) error {
		_, err := tx.CreateBucketIfNotExists(requestsBucket)
		return err
	}); err != nil {
		log.Fatalf("create bucket: %v", err)
	}

	f := &faucet{kp: kp, db: db, nodeAddr: *nodeAddr}

	mux := http.NewServeMux()
	mux.HandleFunc("/faucet", f.handleFaucet)
	mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })

	srv := &http.Server{
		Addr:         *listenAddr,
		Handler:      mux,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	log.Printf("Faucet listening on http://%s (node: %s)", *listenAddr, *nodeAddr)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("serve: %v", err)
	}
}

func loadKeypair(path string) (*keys.Keypair, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read key: %w", err)
	}
	switch len(data) {
	case 32:
		var seed [32]byte
		copy(seed[:], data)
		return keys.NewKeypairFromSeed(seed), nil
	case 64:
		var priv [64]byte
		copy(priv[:], data)
		return keys.NewKeypairFromPrivateKey(priv), nil
	default:
		return nil, fmt.Errorf("invalid key length: %d (expected 32 or 64)", len(data))
	}
}

func (f *faucet) handleFaucet(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, faucetResponse{Ok: false, Error: "POST only"})
		return
	}

	var req faucetRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, faucetResponse{Ok: false, Error: "invalid JSON: " + err.Error()})
		return
	}

	pubkeyBytes, err := hex.DecodeString(req.Pubkey)
	if err != nil || len(pubkeyBytes) != 32 {
		writeJSON(w, http.StatusBadRequest, faucetResponse{Ok: false, Error: "pubkey must be 32-byte hex"})
		return
	}
	var recipient [32]byte
	copy(recipient[:], pubkeyBytes)

	if f.isRateLimited(recipient) {
		writeJSON(w, http.StatusTooManyRequests, faucetResponse{Ok: false, Error: "rate limited (1 request per 60s per pubkey)"})
		return
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	nonce, err := f.getNonce()
	if err != nil {
		writeJSON(w, http.StatusBadGateway, faucetResponse{Ok: false, Error: "failed to get faucet nonce: " + err.Error()})
		return
	}

	rcvrKP, err := keys.GenerateKeypair()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, faucetResponse{Ok: false, Error: "generate receiver key: " + err.Error()})
		return
	}
	rcvrSeed := kpToSeed(rcvrKP)

	tr := &tx.Tx{
		Type:           tx.TxTypeTransfer,
		Sender:         "faucet",
		Receiver:       "user",
		Value:          big.NewInt(defaultFaucetAmount),
		Nonce:          nonce,
		SenderPubKey:   f.kp.PublicKey,
		ReceiverPubKey: rcvrKP.PublicKey,
	}
	sig, err := tr.SignSender(f.kp)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, faucetResponse{Ok: false, Error: "sign sender: " + err.Error()})
		return
	}
	tr.SenderSig = sig

	rsig, err := tr.SignReceiver(rcvrKP)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, faucetResponse{Ok: false, Error: "sign receiver: " + err.Error()})
		return
	}
	tr.ReceiverSig = rsig

	txBytes, err := tr.Serialize()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, faucetResponse{Ok: false, Error: "serialize: " + err.Error()})
		return
	}

	if err := f.submitTx(txBytes); err != nil {
		writeJSON(w, http.StatusBadGateway, faucetResponse{Ok: false, Error: "submit tx: " + err.Error()})
		return
	}

	f.recordRequest(recipient)

	txid, _ := tr.ID()
	writeJSON(w, http.StatusOK, faucetResponse{
		Ok:           true,
		Txid:         hex.EncodeToString(txid[:]),
		ReceiverSeed: hex.EncodeToString(rcvrSeed[:]),
	})
}

func (f *faucet) getNonce() (uint64, error) {
	conn, err := dialNode(f.nodeAddr)
	if err != nil {
		return 0, err
	}
	defer conn.Close()

	client := frgpb.NewFRGClient(conn)
	resp, err := client.GetAccount(context.Background(), &frgpb.AccountRequest{Pubkey: f.kp.PublicKey[:]}, callOpt()...)
	if err != nil {
		return 0, err
	}
	return resp.Nonce + 1, nil
}

func (f *faucet) submitTx(txBytes []byte) error {
	conn, err := dialNode(f.nodeAddr)
	if err != nil {
		return err
	}
	defer conn.Close()

	client := frgpb.NewFRGClient(conn)
	resp, err := client.SubmitTx(context.Background(), &frgpb.RawBytes{Data: txBytes}, callOpt()...)
	if err != nil {
		return err
	}
	if !resp.Ok {
		return fmt.Errorf("node rejected: %s", resp.Error)
	}
	return nil
}

func (f *faucet) isRateLimited(pubkey [32]byte) bool {
	var last int64
	_ = f.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(requestsBucket)
		v := b.Get(pubkey[:])
		if v != nil {
			last = int64FromBytes(v)
		}
		return nil
	})
	return time.Now().Unix()-last < 60
}

func (f *faucet) recordRequest(pubkey [32]byte) {
	now := time.Now().Unix()
	_ = f.db.Update(func(tx *bolt.Tx) error {
		return tx.Bucket(requestsBucket).Put(pubkey[:], int64ToBytes(now))
	})
}

func dialNode(addr string) (*grpc.ClientConn, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return grpc.DialContext(ctx, addr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithBlock(),
	)
}

func callOpt() []grpc.CallOption {
	return []grpc.CallOption{grpc.CallContentSubtype("frg-json")}
}

func int64ToBytes(v int64) []byte {
	b := make([]byte, 8)
	b[0] = byte(v >> 56)
	b[1] = byte(v >> 48)
	b[2] = byte(v >> 40)
	b[3] = byte(v >> 32)
	b[4] = byte(v >> 24)
	b[5] = byte(v >> 16)
	b[6] = byte(v >> 8)
	b[7] = byte(v)
	return b
}

func int64FromBytes(b []byte) int64 {
	return int64(b[0])<<56 | int64(b[1])<<48 | int64(b[2])<<40 | int64(b[3])<<32 |
		int64(b[4])<<24 | int64(b[5])<<16 | int64(b[6])<<8 | int64(b[7])
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func kpToSeed(kp *keys.Keypair) [32]byte {
	priv := [64]byte(kp.PrivateKey)
	seed := [32]byte{}
	copy(seed[:], priv[:32])
	return seed
}
