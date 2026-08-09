package benchmarks

import (
	"fmt"
	"math/big"
	"testing"

	"github.com/imattau/frg/core/contract"
	"github.com/imattau/frg/core/keys"
	"github.com/imattau/frg/core/ledger"
	"github.com/imattau/frg/core/node"
	"github.com/imattau/frg/core/tree"
	"github.com/imattau/frg/core/tx"
	bolt "go.etcd.io/bbolt"
)

func benchmarkDB(tb testing.TB) (*bolt.DB, *ledger.Ledger) {
	tb.Helper()
	db, err := bolt.Open(tb.TempDir()+"/contract_bench.db", 0600, nil)
	if err != nil {
		tb.Fatalf("bolt.Open: %v", err)
	}
	l, err := ledger.New(db)
	if err != nil {
		tb.Fatalf("ledger.New: %v", err)
	}
	if err := db.Update(func(btx *bolt.Tx) error {
		if _, err := btx.CreateBucketIfNotExists([]byte("contract_bytecode")); err != nil {
			return err
		}
		_, err := btx.CreateBucketIfNotExists([]byte("contract_state"))
		return err
	}); err != nil {
		tb.Fatalf("create buckets: %v", err)
	}
	return db, l
}

func BenchmarkContractDeploy(b *testing.B) {
	db, l := benchmarkDB(b)
	defer db.Close()

	kp, _ := keys.GenerateKeypair()
	l.Seed(kp.PublicKey, big.NewInt(1_000_000))
	wasmBytes, err := workloads.ReadFile("workloads/trivial.wasm")
	if err != nil {
		b.Fatalf("read wasm: %v", err)
	}

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		deployTx := &tx.Tx{
			Type:         tx.TxTypeContractDeploy,
			Sender:       "bench",
			Receiver:     "contract",
			Value:        big.NewInt(0),
			Nonce:        uint64(i + 1),
			SenderPubKey: kp.PublicKey,
			WasmBytes:    wasmBytes,
		}
		sig, _ := deployTx.SignSender(kp)
		deployTx.SenderSig = sig

		_ = db.Update(func(btx *bolt.Tx) error {
			_, _, err := contract.Deploy(btx, l, deployTx, uint64(i+1), 1000000)
			return err
		})
	}
}

func BenchmarkContractCall(b *testing.B) {
	db, l := benchmarkDB(b)
	defer db.Close()

	kp, _ := keys.GenerateKeypair()
	l.Seed(kp.PublicKey, big.NewInt(1_000_000))
	wasmBytes, err := workloads.ReadFile("workloads/trivial.wasm")
	if err != nil {
		b.Fatalf("read wasm: %v", err)
	}

	deployTx := &tx.Tx{
		Type:         tx.TxTypeContractDeploy,
		Sender:       "bench",
		Receiver:     "contract",
		Value:        big.NewInt(0),
		Nonce:        1,
		SenderPubKey: kp.PublicKey,
		WasmBytes:    wasmBytes,
	}
	sig, _ := deployTx.SignSender(kp)
	deployTx.SenderSig = sig
	var contractAddr [32]byte
	db.Update(func(btx *bolt.Tx) error {
		contractAddr = contract.ContractAddr(kp.PublicKey, 1)
		_, _, err := contract.Deploy(btx, l, deployTx, 1, 1000000)
		return err
	})

	callTx := &tx.Tx{
		Type:           tx.TxTypeContractCall,
		Sender:         "bench",
		Receiver:       "contract",
		Value:          big.NewInt(0),
		Nonce:          2,
		SenderPubKey:   kp.PublicKey,
		ReceiverPubKey: contractAddr,
	}
	sig2, _ := callTx.SignSender(kp)
	callTx.SenderSig = sig2

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = db.Update(func(btx *bolt.Tx) error {
			_, _, err := contract.Call(btx, l, callTx, 1, 1000000)
			return err
		})
	}
}

func BenchmarkContractStateKeys(b *testing.B) {
	keyCounts := []int{10, 100, 1_000, 10_000, 100_000}
	for _, n := range keyCounts {
		b.Run(fmt.Sprintf("keys=%d", n), func(b *testing.B) {
			store := contract.NewStateStore()
			for i := 0; i < n; i++ {
				key := fmt.Sprintf("key-%08d", i)
				val := fmt.Sprintf("value-%08d-value-%08d-value-%08d-value-%08d", i, i, i, i)
				store.Set([]byte(key), []byte(val))
			}
			b.ResetTimer()
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				_ = store.StateRoot()
			}
		})
	}
}

func BenchmarkContractStateSerialize(b *testing.B) {
	keyCounts := []int{10, 100, 1_000, 10_000, 100_000}
	for _, n := range keyCounts {
		b.Run(fmt.Sprintf("keys=%d", n), func(b *testing.B) {
			store := contract.NewStateStore()
			for i := 0; i < n; i++ {
				key := fmt.Sprintf("key-%08d", i)
				val := fmt.Sprintf("value-%08d-value-%08d-value-%08d-value-%08d", i, i, i, i)
				store.Set([]byte(key), []byte(val))
			}
			b.ResetTimer()
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				_ = store.Serialize()
			}
		})
	}
}

func BenchmarkContractRGIntegration(b *testing.B) {
	contractCounts := []int{1, 10, 100, 1_000}
	for _, c := range contractCounts {
		b.Run(fmt.Sprintf("contracts=%d", c), func(b *testing.B) {
			sender := benchKeypair(b)
			receiver := benchKeypair(b)
			txs := make([]*tx.Tx, 1000)
			for i := range txs {
				txs[i] = benchTx(b, sender, receiver, int64(i+1), uint64(i))
			}

			store := contract.NewStateStore()
			for i := 0; i < 100; i++ {
				key := fmt.Sprintf("key-%08d", i)
				store.Set([]byte(key), []byte(fmt.Sprintf("val-%d", i)))
			}
			stateRoot := store.StateRoot()

			contractNodes := make([]*node.RGNode, c)
			for i := range contractNodes {
				v := big.NewInt(int64(i + 1))
				contractNodes[i] = &node.RGNode{
					Scale:         1,
					Volume:        node.Uint256ToBytes(v),
					Sig:           node.SigContract,
					Children:      [][32]byte{stateRoot},
					SumSquares:    node.Uint256ToBytes(new(big.Int).Mul(v, v)),
					Count:         1,
					ContractCount: 1,
				}
			}

			b.ResetTimer()
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				_, _ = tree.BuildTreeRoot(txs, contractNodes)
			}
		})
	}
}
