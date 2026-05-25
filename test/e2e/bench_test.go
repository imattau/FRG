package e2e_test

import (
	"math/big"
	"testing"

	"github.com/imattau/frg/core/gas"
	"github.com/imattau/frg/core/hash"
	"github.com/imattau/frg/core/mint"
	"github.com/imattau/frg/core/node"
	"github.com/imattau/frg/core/tree"
	"github.com/imattau/frg/core/tx"
)

func BenchmarkSingleTxBlock(b *testing.B) {
	sender := makeKeypair(b)
	receiver := makeKeypair(b)
	tr := makeTx(b, sender, receiver, 100, 1)
	block := &tree.Block{Height: 1, Txs: []*tx.Tx{tr}}
	
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = block.BuildRoot()
	}
}

func BenchmarkFullBlock(b *testing.B) {
	sender := makeKeypair(b)
	receiver := makeKeypair(b)
	txs := make([]*tx.Tx, tree.TMax)
	for i := range txs {
		txs[i] = makeTx(b, sender, receiver, int64(i+1), uint64(i))
	}
	block := &tree.Block{Height: 1, Txs: txs}
	
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = block.BuildRoot()
	}
}

func BenchmarkTxSerialise(b *testing.B) {
	sender := makeKeypair(b)
	receiver := makeKeypair(b)
	tr := &tx.Tx{
		Sender:         "alice",
		Receiver:       "bob",
		Value:          big.NewInt(42),
		Nonce:          7,
		SenderPubKey:   sender.PublicKey,
		ReceiverPubKey: receiver.PublicKey,
	}
	
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = tr.Serialize()
	}
}

func BenchmarkNodeSerialise(b *testing.B) {
	n := &node.RGNode{
		Scale:    1,
		Volume:   big.NewInt(100),
		Variance: big.NewInt(0),
		Sig:      node.SigAtomic,
		Children: [][32]byte{hash.Hash([]byte("test"))},
	}
	
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = n.Serialize()
	}
}

func BenchmarkAccrue1000Validators(b *testing.B) {
	h := newHarness(b)
	defer h.Ledger.Close()
	defer h.Staking.Close()
	
	validators := make([][32]byte, 1000)
	bonds := make([]*big.Int, 1000)
	for i := range validators {
		kp := makeKeypair(b)
		validators[i] = kp.PublicKey
		bonds[i] = big.NewInt(1000 + int64(i))
		_ = h.Ledger.Seed(gas.FeeAccount(validators[i]), big.NewInt(0))
	}
	totalFees := big.NewInt(1000000)
	
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = gas.Accrue(h.Ledger, totalFees, validators, bonds)
	}
}

func BenchmarkMintPerBlock(b *testing.B) {
	supply := new(big.Int).Mul(big.NewInt(400_000_000), new(big.Int).Exp(big.NewInt(10), big.NewInt(18), nil))
	staked := new(big.Int).Div(supply, big.NewInt(4)) // 25%
	
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = mint.MintPerBlock(supply, staked)
	}
}
