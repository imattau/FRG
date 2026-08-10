package wallet

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"os"
	"time"

	"github.com/imattau/frg/core/contract"
	"github.com/imattau/frg/core/keys"
	"github.com/imattau/frg/core/tx"
	frgpb "github.com/imattau/frg/proto"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

type Wallet struct {
	kp      *keys.Keypair
	client  frgpb.FRGClient
	chainID string
}

type Client struct {
	*Wallet
	conn *grpc.ClientConn
}

type TransferResult struct {
	TxID string `json:"txid"`
}

type DeployResult struct {
	TxID            string `json:"txid"`
	ContractAddress string `json:"contract_address"`
}

type FaucetResult struct {
	OK    bool   `json:"ok"`
	TxID  string `json:"txid,omitempty"`
	Error string `json:"error,omitempty"`
}

func New(kp *keys.Keypair, client frgpb.FRGClient, chainID string) (*Wallet, error) {
	if kp == nil {
		return nil, fmt.Errorf("keypair is nil")
	}
	if client == nil {
		return nil, fmt.Errorf("client is nil")
	}
	if chainID == "" {
		chainID = tx.DefaultChainID
	}
	return &Wallet{kp: kp, client: client, chainID: chainID}, nil
}

func Dial(ctx context.Context, addr string, kp *keys.Keypair, chainID string, opts ...grpc.DialOption) (*Client, error) {
	dialOpts := make([]grpc.DialOption, 0, len(opts)+3)
	dialOpts = append(dialOpts, grpc.WithTransportCredentials(insecure.NewCredentials()))
	dialOpts = append(dialOpts, opts...)
	dialOpts = append(dialOpts, grpc.WithDefaultCallOptions(grpc.CallContentSubtype("frg-json")))
	conn, err := grpc.DialContext(ctx, addr, dialOpts...)
	if err != nil {
		return nil, fmt.Errorf("dial %s: %w", addr, err)
	}
	w, err := New(kp, frgpb.NewFRGClient(conn), chainID)
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	return &Client{Wallet: w, conn: conn}, nil
}

func (c *Client) Close() error {
	return c.conn.Close()
}

func GenerateKeypair() (*keys.Keypair, error) {
	return keys.GenerateKeypair()
}

func LoadKeypair(path string) (*keys.Keypair, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read key %s: %w", path, err)
	}
	switch len(data) {
	case ed25519.SeedSize:
		var seed [32]byte
		copy(seed[:], data)
		return keys.NewKeypairFromSeed(seed), nil
	case ed25519.PrivateKeySize:
		var priv [64]byte
		copy(priv[:], data)
		return keys.NewKeypairFromPrivateKey(priv), nil
	default:
		return nil, fmt.Errorf("invalid key length %d", len(data))
	}
}

func SaveSeed(path string, kp *keys.Keypair) error {
	if kp == nil {
		return fmt.Errorf("keypair is nil")
	}
	seed := ed25519.PrivateKey(kp.PrivateKey[:]).Seed()
	return os.WriteFile(path, seed, 0600)
}

func DecodePubKey(pubkeyHex string) ([32]byte, error) {
	var pub [32]byte
	raw, err := hex.DecodeString(pubkeyHex)
	if err != nil || len(raw) != 32 {
		return pub, fmt.Errorf("pubkey must be 32-byte hex")
	}
	copy(pub[:], raw)
	return pub, nil
}

func (w *Wallet) PublicKey() [32]byte {
	return w.kp.PublicKey
}

func (w *Wallet) PublicKeyHex() string {
	return hex.EncodeToString(w.kp.PublicKey[:])
}

func (w *Wallet) ChainID() string {
	return w.chainID
}

func (w *Wallet) Account(ctx context.Context, pubkey [32]byte) (*frgpb.AccountResponse, error) {
	return w.client.GetAccount(ctx, &frgpb.AccountRequest{Pubkey: pubkey[:]}, grpc.CallContentSubtype("frg-json"))
}

func (w *Wallet) OwnAccount(ctx context.Context) (*frgpb.AccountResponse, error) {
	return w.Account(ctx, w.kp.PublicKey)
}

func (w *Wallet) Status(ctx context.Context) (*frgpb.StatusResponse, error) {
	return w.client.GetStatus(ctx, &frgpb.Empty{}, grpc.CallContentSubtype("frg-json"))
}

func (w *Wallet) Validators(ctx context.Context) (*frgpb.ValidatorList, error) {
	return w.client.ListValidators(ctx, &frgpb.Empty{}, grpc.CallContentSubtype("frg-json"))
}

func (w *Wallet) Mempool(ctx context.Context) (*frgpb.MempoolList, error) {
	return w.client.ListMempool(ctx, &frgpb.Empty{}, grpc.CallContentSubtype("frg-json"))
}

func (w *Wallet) BlockTelemetry(ctx context.Context, height uint64) (*frgpb.BlockTelemetryResponse, error) {
	return w.client.GetBlockTelemetry(ctx, &frgpb.BlockTelemetryRequest{Height: height}, grpc.CallContentSubtype("frg-json"))
}

func (w *Wallet) ContractState(ctx context.Context, contractAddr [32]byte, key []byte) (*frgpb.ContractStateResponse, error) {
	return w.client.GetContractState(ctx, &frgpb.ContractStateRequest{
		ContractAddress: contractAddr[:],
		Key:             append([]byte(nil), key...),
	}, grpc.CallContentSubtype("frg-json"))
}

func (w *Wallet) Transfer(ctx context.Context, to [32]byte, amount *big.Int) (*TransferResult, error) {
	if amount == nil || amount.Sign() <= 0 {
		return nil, fmt.Errorf("amount must be positive")
	}
	acct, err := w.OwnAccount(ctx)
	if err != nil {
		return nil, err
	}
	tr := &tx.Tx{
		Type:           tx.TxTypeTransfer,
		Sender:         "wallet",
		Receiver:       "wallet",
		Value:          new(big.Int).Set(amount),
		Nonce:          acct.Nonce + 1,
		SenderPubKey:   w.kp.PublicKey,
		ReceiverPubKey: to,
	}
	return w.signAndSubmit(ctx, tr)
}

func (w *Wallet) Bond(ctx context.Context, amount *big.Int) (*TransferResult, error) {
	if amount == nil || amount.Sign() <= 0 {
		return nil, fmt.Errorf("amount must be positive")
	}
	acct, err := w.OwnAccount(ctx)
	if err != nil {
		return nil, err
	}
	tr := &tx.Tx{
		Type:           tx.TxTypeBond,
		Sender:         "validator",
		Receiver:       "staking",
		Value:          new(big.Int).Set(amount),
		Nonce:          acct.Nonce + 1,
		SenderPubKey:   w.kp.PublicKey,
		ReceiverPubKey: w.kp.PublicKey,
	}
	return w.signAndSubmit(ctx, tr)
}

func (w *Wallet) DeployContract(ctx context.Context, wasm []byte, value *big.Int) (*DeployResult, error) {
	if len(wasm) == 0 {
		return nil, fmt.Errorf("wasm is required")
	}
	acct, err := w.OwnAccount(ctx)
	if err != nil {
		return nil, err
	}
	if value == nil {
		value = big.NewInt(0)
	}
	if value.Sign() < 0 {
		return nil, fmt.Errorf("value must not be negative")
	}
	nonce := acct.Nonce + 1
	tr := &tx.Tx{
		Type:         tx.TxTypeContractDeploy,
		Sender:       "wallet",
		Receiver:     "contract",
		Value:        new(big.Int).Set(value),
		Nonce:        nonce,
		SenderPubKey: w.kp.PublicKey,
		WasmBytes:    append([]byte(nil), wasm...),
	}
	res, err := w.signAndSubmit(ctx, tr)
	if err != nil {
		return nil, err
	}
	addr := contract.ContractAddr(w.kp.PublicKey, nonce)
	return &DeployResult{TxID: res.TxID, ContractAddress: hex.EncodeToString(addr[:])}, nil
}

func (w *Wallet) CallContract(ctx context.Context, contractAddr [32]byte, callData []byte, value *big.Int) (*TransferResult, error) {
	acct, err := w.OwnAccount(ctx)
	if err != nil {
		return nil, err
	}
	if value == nil {
		value = big.NewInt(0)
	}
	if value.Sign() < 0 {
		return nil, fmt.Errorf("value must not be negative")
	}
	tr := &tx.Tx{
		Type:           tx.TxTypeContractCall,
		Sender:         "wallet",
		Receiver:       "contract",
		Value:          new(big.Int).Set(value),
		Nonce:          acct.Nonce + 1,
		SenderPubKey:   w.kp.PublicKey,
		ReceiverPubKey: contractAddr,
		CallData:       append([]byte(nil), callData...),
	}
	return w.signAndSubmit(ctx, tr)
}

func (w *Wallet) ContractAddress(nonce uint64) [32]byte {
	return contract.ContractAddr(w.kp.PublicKey, nonce)
}

func (w *Wallet) signAndSubmit(ctx context.Context, tr *tx.Tx) (*TransferResult, error) {
	sig, err := tr.SignSenderForChain(w.kp, w.chainID)
	if err != nil {
		return nil, err
	}
	tr.SenderSig = sig
	raw, err := tr.Serialize()
	if err != nil {
		return nil, err
	}
	resp, err := w.client.SubmitTx(ctx, &frgpb.RawBytes{Data: raw}, grpc.CallContentSubtype("frg-json"))
	if err != nil {
		return nil, err
	}
	if !resp.Ok {
		return nil, fmt.Errorf("node rejected transaction: %s", resp.Error)
	}
	txid, err := tr.ID()
	if err != nil {
		return nil, err
	}
	return &TransferResult{TxID: hex.EncodeToString(txid[:])}, nil
}

func RequestFaucet(ctx context.Context, faucetURL string, pubkey [32]byte) (*FaucetResult, error) {
	body, err := json.Marshal(map[string]string{"pubkey": hex.EncodeToString(pubkey[:])})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, faucetURL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("content-type", "application/json")
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var out FaucetResult
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 || !out.OK {
		if out.Error == "" {
			out.Error = resp.Status
		}
		return &out, fmt.Errorf("faucet rejected request: %s", out.Error)
	}
	return &out, nil
}
