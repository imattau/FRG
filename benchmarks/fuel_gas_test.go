package benchmarks

import (
	"embed"
	"fmt"
	"math/big"
	"strings"
	"testing"
	"time"

	"github.com/imattau/frg/core/contract"
	"github.com/imattau/frg/core/keys"
	"github.com/imattau/frg/core/ledger"
	bolt "go.etcd.io/bbolt"
)

//go:embed workloads/*.wasm
var workloads embed.FS

func BenchmarkFuelCalibration(b *testing.B) {
	workloadList := []string{"trivial", "arithmetic", "memory", "hashing", "state_read", "state_write", "heavy"}
	for _, wl := range workloadList {
		b.Run(wl, func(b *testing.B) {
			db, err := bolt.Open(b.TempDir()+"/fuel_bench.db", 0600, nil)
			if err != nil {
				b.Fatalf("bolt.Open: %v", err)
			}
			defer db.Close()

			l, err := ledger.New(db)
			if err != nil {
				b.Fatalf("ledger.New: %v", err)
			}

			if err := db.Update(func(btx *bolt.Tx) error {
				if _, err := btx.CreateBucketIfNotExists([]byte("contract_bytecode")); err != nil {
					return err
				}
				_, err := btx.CreateBucketIfNotExists([]byte("contract_state"))
				return err
			}); err != nil {
				b.Fatalf("create buckets: %v", err)
			}

			wasmBytes, err := workloads.ReadFile(fmt.Sprintf("workloads/%s.wasm", wl))
			if err != nil {
				b.Fatalf("read wasm: %v", err)
			}

			kp, _ := keys.GenerateKeypair()
			l.Seed(kp.PublicKey, big.NewInt(1_000_000))

			store := contract.NewStateStore()
			for i := 0; i < 200; i++ {
				key := fmt.Sprintf("key-%08d", i)
				store.Set([]byte(key), []byte(fmt.Sprintf("value-%d", i)))
			}

			b.ResetTimer()
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				_ = db.Update(func(btx *bolt.Tx) error {
					cfg := &contract.RuntimeConfig{
						WasmBytes:   wasmBytes,
						Caller:      kp.PublicKey,
						SelfAddr:    kp.PublicKey,
						Value:       big.NewInt(0),
						BlockHeight: uint64(i),
						State:       store.Clone(),
						Ledger:      l,
						BoltTx:      btx,
					}
					rt, err := contract.NewRuntime(cfg)
					if err != nil {
						b.Fatalf("NewRuntime: %v", err)
					}
					_, err = rt.Call("call")
					if err != nil {
						b.Logf("call failed: %v", err)
					}
					_ = rt.FuelConsumed()
					return nil
				})
			}
		})
	}
}

func TestFuelCostModel(t *testing.T) {
	workloadList := []string{"trivial", "arithmetic", "memory", "hashing", "state_read", "state_write", "heavy"}
	const reps = 50

	type result struct {
		workload  string
		avgFuel   float64
		avgTimeUs float64
	}

	var results []result

	for _, wl := range workloadList {
		db, err := bolt.Open(t.TempDir()+"/fuel_model.db", 0600, nil)
		if err != nil {
			t.Fatalf("bolt.Open: %v", err)
		}
		defer db.Close()

		l, err := ledger.New(db)
		if err != nil {
			t.Fatalf("ledger.New: %v", err)
		}

		if err := db.Update(func(btx *bolt.Tx) error {
			if _, err := btx.CreateBucketIfNotExists([]byte("contract_bytecode")); err != nil {
				return err
			}
			_, err := btx.CreateBucketIfNotExists([]byte("contract_state"))
			return err
		}); err != nil {
			t.Fatalf("create buckets: %v", err)
		}

		wasmBytes, err := workloads.ReadFile(fmt.Sprintf("workloads/%s.wasm", wl))
		if err != nil {
			t.Fatalf("read wasm: %v", err)
		}

		kp, _ := keys.GenerateKeypair()
		l.Seed(kp.PublicKey, big.NewInt(1_000_000))

		store := contract.NewStateStore()
		for i := 0; i < 200; i++ {
			key := fmt.Sprintf("key-%08d", i)
			store.Set([]byte(key), []byte(fmt.Sprintf("value-%d", i)))
		}

		var totalFuel uint64
		var totalTime time.Duration

		for i := 0; i < reps; i++ {
			_ = db.Update(func(btx *bolt.Tx) error {
				cfg := &contract.RuntimeConfig{
					WasmBytes:   wasmBytes,
					Caller:      kp.PublicKey,
					SelfAddr:    kp.PublicKey,
					Value:       big.NewInt(0),
					BlockHeight: uint64(i),
					State:       store.Clone(),
					Ledger:      l,
					BoltTx:      btx,
				}
				start := time.Now()
				rt, err := contract.NewRuntime(cfg)
				if err != nil {
					return nil
				}
				if _, err := rt.Call("call"); err != nil {
					return nil
				}
				elapsed := time.Since(start)
				totalTime += elapsed
				totalFuel += rt.FuelConsumed()
				return nil
			})
		}

		results = append(results, result{
			workload:  wl,
			avgFuel:   float64(totalFuel) / float64(reps),
			avgTimeUs: float64(totalTime.Microseconds()) / float64(reps),
		})
	}

	var b strings.Builder
	b.WriteString("\nFuel vs Wall-Clock Cost Model:\n")
	b.WriteString(fmt.Sprintf("%-15s %10s %12s %10s\n", "Workload", "Fuel", "Time(us)", "Fuel/us"))
	for _, r := range results {
		ratio := r.avgFuel / r.avgTimeUs
		b.WriteString(fmt.Sprintf("%-15s %10.0f %12.1f %10.1f\n",
			r.workload, r.avgFuel, r.avgTimeUs, ratio))
	}
	t.Log(b.String())
}
