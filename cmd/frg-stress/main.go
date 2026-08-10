package main

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"math/big"
	"os"
	"os/signal"
	"sync"
	"sync/atomic"
	"time"

	"github.com/imattau/frg/core/keys"
	"github.com/imattau/frg/core/tx"
	frgpb "github.com/imattau/frg/proto"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

type stressAccount struct {
	Seed   string `json:"seed"`
	Pubkey string `json:"pubkey"`
}

type stats struct {
	submitted atomic.Int64
	confirmed atomic.Int64
	failed    atomic.Int64
	startTime time.Time
	latencies []time.Duration
	latMu     sync.Mutex
}

func main() {
	accountsPath := flag.String("accounts", "stress_accounts.json", "JSON file with stress accounts (from frg-devnet --stress-accounts)")
	txPerAccount := flag.Int("tx-per-account", 100, "number of tx per account")
	rate := flag.Int("rate", 100, "target tps")
	duration := flag.Duration("duration", 60*time.Second, "run duration")
	addr := flag.String("addr", "localhost:50051", "FRG node gRPC address")
	flag.Parse()

	data, err := os.ReadFile(*accountsPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "read accounts: %v\n", err)
		os.Exit(1)
	}

	var accounts []stressAccount
	if err := json.Unmarshal(data, &accounts); err != nil {
		fmt.Fprintf(os.Stderr, "parse accounts: %v\n", err)
		os.Exit(1)
	}

	if len(accounts) == 0 {
		fmt.Fprintln(os.Stderr, "no stress accounts found")
		os.Exit(1)
	}

	acctKeys := make([]*keys.Keypair, len(accounts))
	for i, a := range accounts {
		seedBytes, err := hex.DecodeString(a.Seed)
		if err != nil || len(seedBytes) != 32 {
			fmt.Fprintf(os.Stderr, "invalid seed for account %d\n", i)
			os.Exit(1)
		}
		var seed [32]byte
		copy(seed[:], seedBytes)
		acctKeys[i] = keys.NewKeypairFromSeed(seed)
	}

	st := &stats{startTime: time.Now()}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt)
	go func() {
		<-sigCh
		cancel()
	}()

	ticker := time.NewTicker(time.Second / time.Duration(*rate/len(accounts)+1))
	defer ticker.Stop()

	deadline := time.After(*duration)

	var wg sync.WaitGroup
	for i, kp := range acctKeys {
		wg.Add(1)
		go func(idx int, sender *keys.Keypair) {
			defer wg.Done()

			nonce, err := getNonce(*addr, sender.PublicKey)
			if err != nil {
				fmt.Fprintf(os.Stderr, "[acct %d] get nonce: %v\n", idx, err)
				return
			}

			count := 0
			for count < *txPerAccount {
				select {
				case <-ctx.Done():
					return
				case <-deadline:
					return
				case <-ticker.C:
				}

				rcvrKP, err := keys.GenerateKeypair()
				if err != nil {
					continue
				}

				nonce++
				tr := &tx.Tx{
					Type:           tx.TxTypeTransfer,
					Sender:         fmt.Sprintf("stress-%d", idx),
					Receiver:       fmt.Sprintf("stress-%d-rcvr", idx),
					Value:          big.NewInt(1),
					Nonce:          nonce,
					SenderPubKey:   sender.PublicKey,
					ReceiverPubKey: rcvrKP.PublicKey,
				}

				sig, err := tr.SignSender(sender)
				if err != nil {
					st.failed.Add(1)
					continue
				}
				tr.SenderSig = sig

				rsig, err := tr.SignReceiver(rcvrKP)
				if err != nil {
					st.failed.Add(1)
					continue
				}
				tr.ReceiverSig = rsig

				txBytes, err := tr.Serialize()
				if err != nil {
					st.failed.Add(1)
					continue
				}

				tstart := time.Now()
				if err := submitTx(*addr, txBytes); err != nil {
					st.failed.Add(1)
					continue
				}

				latency := time.Since(tstart)
				st.latMu.Lock()
				st.latencies = append(st.latencies, latency)
				st.latMu.Unlock()

				st.submitted.Add(1)
				count++
			}
		}(i, kp)
	}

	go progressReporter(st)

	wg.Wait()
	printSummary(st)
}

func getNonce(addr string, pubkey [32]byte) (uint64, error) {
	conn, err := dialNode(addr)
	if err != nil {
		return 0, err
	}
	defer conn.Close()

	client := frgpb.NewFRGClient(conn)
	resp, err := client.GetAccount(context.Background(), &frgpb.AccountRequest{Pubkey: pubkey[:]}, callOpt()...)
	if err != nil {
		return 0, err
	}
	return resp.Nonce, nil
}

func submitTx(addr string, txBytes []byte) error {
	conn, err := dialNode(addr)
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
		return fmt.Errorf("rejected: %s", resp.Error)
	}
	return nil
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
	return nil
}

func progressReporter(st *stats) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for range ticker.C {
		sub := st.submitted.Load()
		fail := st.failed.Load()
		elapsed := time.Since(st.startTime).Seconds()
		tps := float64(sub) / elapsed
		fmt.Printf("[%s] submitted=%d failed=%d tps=%.1f\n",
			time.Now().Format("15:04:05"), sub, fail, tps)
	}
}

func printSummary(st *stats) {
	sub := st.submitted.Load()
	fail := st.failed.Load()
	elapsed := time.Since(st.startTime).Seconds()

	fmt.Println("\n--- Load Test Summary ---")
	fmt.Printf("Duration:      %.1fs\n", elapsed)
	fmt.Printf("Submitted:     %d\n", sub)
	fmt.Printf("Failed:        %d\n", fail)
	fmt.Printf("Overall TPS:   %.1f\n", float64(sub)/elapsed)

	st.latMu.Lock()
	if len(st.latencies) > 0 {
		var total time.Duration
		var minLat, maxLat time.Duration
		minLat = st.latencies[0]
		for _, l := range st.latencies {
			total += l
			if l < minLat {
				minLat = l
			}
			if l > maxLat {
				maxLat = l
			}
		}
		avgLat := total / time.Duration(len(st.latencies))
		fmt.Printf("Avg latency:   %v\n", avgLat)
		fmt.Printf("Min latency:   %v\n", minLat)
		fmt.Printf("Max latency:   %v\n", maxLat)
		fmt.Printf("Samples:       %d\n", len(st.latencies))
	}
	st.latMu.Unlock()
}
