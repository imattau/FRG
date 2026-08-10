package wallet

import (
	"context"
	"encoding/hex"
	"errors"
	"math/big"
	"os"
	"path/filepath"
	"testing"

	"github.com/imattau/frg/core/keys"
	"github.com/imattau/frg/core/tx"
	frgpb "github.com/imattau/frg/proto"
	"google.golang.org/grpc"
)

type fakeClient struct {
	account       *frgpb.AccountResponse
	contractState *frgpb.ContractStateResponse
	status        *frgpb.StatusResponse
	vals          *frgpb.ValidatorList
	tx            *tx.Tx
}

func (f *fakeClient) SubmitTx(_ context.Context, in *frgpb.RawBytes, _ ...grpc.CallOption) (*frgpb.SubmitResponse, error) {
	tr, err := tx.Deserialize(in.Data)
	if err != nil {
		return nil, err
	}
	f.tx = tr
	return &frgpb.SubmitResponse{Ok: true}, nil
}

func (f *fakeClient) SubmitBatch(context.Context, *frgpb.RawBytesArray, ...grpc.CallOption) (*frgpb.SubmitResponse, error) {
	return nil, errors.New("not implemented")
}

func (f *fakeClient) SubscribeBlocks(context.Context, *frgpb.Empty, ...grpc.CallOption) (frgpb.FRG_SubscribeBlocksClient, error) {
	return nil, errors.New("not implemented")
}

func (f *fakeClient) GetStatus(context.Context, *frgpb.Empty, ...grpc.CallOption) (*frgpb.StatusResponse, error) {
	if f.status == nil {
		return &frgpb.StatusResponse{}, nil
	}
	return f.status, nil
}

func (f *fakeClient) GetAccount(context.Context, *frgpb.AccountRequest, ...grpc.CallOption) (*frgpb.AccountResponse, error) {
	if f.account == nil {
		return &frgpb.AccountResponse{Balance: "0"}, nil
	}
	return f.account, nil
}

func (f *fakeClient) GetContractState(_ context.Context, req *frgpb.ContractStateRequest, _ ...grpc.CallOption) (*frgpb.ContractStateResponse, error) {
	if f.contractState == nil {
		return &frgpb.ContractStateResponse{
			ContractAddress: append([]byte(nil), req.ContractAddress...),
			Key:             append([]byte(nil), req.Key...),
		}, nil
	}
	return f.contractState, nil
}

func (f *fakeClient) ListValidators(context.Context, *frgpb.Empty, ...grpc.CallOption) (*frgpb.ValidatorList, error) {
	if f.vals == nil {
		return &frgpb.ValidatorList{}, nil
	}
	return f.vals, nil
}

func (f *fakeClient) ListMempool(context.Context, *frgpb.Empty, ...grpc.CallOption) (*frgpb.MempoolList, error) {
	return nil, errors.New("not implemented")
}

func TestSaveSeedAndLoadKeypair(t *testing.T) {
	kp, err := keys.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "wallet.key")
	if err := SaveSeed(path, kp); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("key permissions = %v, want 0600", info.Mode().Perm())
	}
	loaded, err := LoadKeypair(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.PublicKey != kp.PublicKey {
		t.Fatalf("loaded pubkey %x != %x", loaded.PublicKey, kp.PublicKey)
	}
}

func TestDecodePubKey(t *testing.T) {
	var want [32]byte
	for i := range want {
		want[i] = byte(i)
	}
	got, err := DecodePubKey(hex.EncodeToString(want[:]))
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("decoded %x, want %x", got, want)
	}
	if _, err := DecodePubKey("abcd"); err == nil {
		t.Fatal("expected short pubkey error")
	}
}

func TestTransferSignsAndSubmitsSenderOnly(t *testing.T) {
	kp, err := keys.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	var to [32]byte
	for i := range to {
		to[i] = byte(200 + i)
	}
	fc := &fakeClient{account: &frgpb.AccountResponse{Balance: "1000", Nonce: 7}}
	w, err := New(kp, fc, "test-chain")
	if err != nil {
		t.Fatal(err)
	}
	res, err := w.Transfer(context.Background(), to, big.NewInt(123))
	if err != nil {
		t.Fatal(err)
	}
	if res.TxID == "" {
		t.Fatal("empty txid")
	}
	if fc.tx == nil {
		t.Fatal("no tx submitted")
	}
	if fc.tx.Type != tx.TxTypeTransfer {
		t.Fatalf("type = %d", fc.tx.Type)
	}
	if fc.tx.Nonce != 8 {
		t.Fatalf("nonce = %d, want 8", fc.tx.Nonce)
	}
	if fc.tx.SenderPubKey != kp.PublicKey {
		t.Fatalf("sender pubkey mismatch")
	}
	if fc.tx.ReceiverPubKey != to {
		t.Fatalf("receiver pubkey mismatch")
	}
	if fc.tx.Value.Cmp(big.NewInt(123)) != 0 {
		t.Fatalf("value = %s", fc.tx.Value)
	}
	if err := fc.tx.VerifySigsForChain("test-chain"); err != nil {
		t.Fatalf("sender signature did not verify: %v", err)
	}
}

func TestBondUsesOwnPubkeyAsReceiver(t *testing.T) {
	kp, err := keys.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	fc := &fakeClient{account: &frgpb.AccountResponse{Balance: "1000", Nonce: 2}}
	w, err := New(kp, fc, "")
	if err != nil {
		t.Fatal(err)
	}
	if w.ChainID() != tx.DefaultChainID {
		t.Fatalf("chain id = %s", w.ChainID())
	}
	if _, err := w.Bond(context.Background(), big.NewInt(1000)); err != nil {
		t.Fatal(err)
	}
	if fc.tx.Type != tx.TxTypeBond {
		t.Fatalf("type = %d", fc.tx.Type)
	}
	if fc.tx.ReceiverPubKey != kp.PublicKey {
		t.Fatalf("bond receiver pubkey mismatch")
	}
	if err := fc.tx.VerifySigsForChain(tx.DefaultChainID); err != nil {
		t.Fatalf("bond signature did not verify: %v", err)
	}
}

func TestDeployContractReturnsDeterministicAddress(t *testing.T) {
	kp, err := keys.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	fc := &fakeClient{account: &frgpb.AccountResponse{Balance: "1000", Nonce: 10}}
	w, err := New(kp, fc, "contracts")
	if err != nil {
		t.Fatal(err)
	}
	wasm := []byte{0x00, 0x61, 0x73, 0x6d}
	res, err := w.DeployContract(context.Background(), wasm, big.NewInt(7))
	if err != nil {
		t.Fatal(err)
	}
	if fc.tx.Type != tx.TxTypeContractDeploy {
		t.Fatalf("type = %d", fc.tx.Type)
	}
	if fc.tx.Nonce != 11 {
		t.Fatalf("nonce = %d, want 11", fc.tx.Nonce)
	}
	if string(fc.tx.WasmBytes) != string(wasm) {
		t.Fatalf("wasm mismatch")
	}
	if fc.tx.Value.Cmp(big.NewInt(7)) != 0 {
		t.Fatalf("value = %s", fc.tx.Value)
	}
	wantAddr := w.ContractAddress(11)
	if res.ContractAddress != hex.EncodeToString(wantAddr[:]) {
		t.Fatalf("contract address = %s, want %x", res.ContractAddress, wantAddr)
	}
	if err := fc.tx.VerifySigsForChain("contracts"); err != nil {
		t.Fatalf("deploy signature did not verify: %v", err)
	}
}

func TestCallContractUsesContractReceiver(t *testing.T) {
	kp, err := keys.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	var addr [32]byte
	for i := range addr {
		addr[i] = byte(50 + i)
	}
	fc := &fakeClient{account: &frgpb.AccountResponse{Balance: "1000", Nonce: 3}}
	w, err := New(kp, fc, "contracts")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.CallContract(context.Background(), addr, []byte("call"), nil); err != nil {
		t.Fatal(err)
	}
	if fc.tx.Type != tx.TxTypeContractCall {
		t.Fatalf("type = %d", fc.tx.Type)
	}
	if fc.tx.ReceiverPubKey != addr {
		t.Fatalf("receiver pubkey mismatch")
	}
	if string(fc.tx.CallData) != "call" {
		t.Fatalf("call data = %q", fc.tx.CallData)
	}
	if fc.tx.Value.Sign() != 0 {
		t.Fatalf("value = %s, want zero", fc.tx.Value)
	}
	if err := fc.tx.VerifySigsForChain("contracts"); err != nil {
		t.Fatalf("call signature did not verify: %v", err)
	}
}

func TestContractStateQueriesNode(t *testing.T) {
	kp, err := keys.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	var addr [32]byte
	for i := range addr {
		addr[i] = byte(i)
	}
	fc := &fakeClient{contractState: &frgpb.ContractStateResponse{
		ContractAddress: addr[:],
		Exists:          true,
		Key:             []byte("count"),
		Found:           true,
		Value:           []byte{7},
	}}
	w, err := New(kp, fc, "")
	if err != nil {
		t.Fatal(err)
	}
	resp, err := w.ContractState(context.Background(), addr, []byte("count"))
	if err != nil {
		t.Fatal(err)
	}
	if !resp.Exists || !resp.Found || string(resp.Key) != "count" || len(resp.Value) != 1 || resp.Value[0] != 7 {
		t.Fatalf("unexpected contract state response: %+v", resp)
	}
}
