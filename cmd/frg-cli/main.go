package main

import (
	"context"
	"encoding/hex"
	"fmt"
	"math/big"
	"os"
	"time"

	"github.com/imattau/frg/core/denom"
	"github.com/imattau/frg/core/keys"
	"github.com/imattau/frg/core/tx"
	frgpb "github.com/imattau/frg/proto"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

const defaultNodeAddr = "localhost:50051"

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	cmd := os.Args[1]
	switch cmd {
	case "keygen":
		keygenCmd(os.Args[2:])
	case "balance":
		balanceCmd(os.Args[2:])
	case "prepare":
		prepareCmd(os.Args[2:])
	case "countersign":
		countersignCmd(os.Args[2:])
	case "submit":
		submitCmd(os.Args[2:])
	case "send":
		sendCmd(os.Args[2:])
	case "bond":
		bondCmd(os.Args[2:])
	case "unbond":
		protocolZeroValueCmd(os.Args[2:], "unbond", tx.TxTypeUnbond)
	case "finalize-unbond":
		protocolZeroValueCmd(os.Args[2:], "finalize-unbond", tx.TxTypeFinalizeUnbond)
	case "claim-rewards":
		protocolZeroValueCmd(os.Args[2:], "claim-rewards", tx.TxTypeClaimRewards)
	case "status":
		statusCmd(os.Args[2:])
	case "validators":
		validatorsCmd(os.Args[2:])
	case "help", "-h", "--help":
		printUsage()
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n", cmd)
		printUsage()
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Print(`frg-cli — FRG command-line wallet

Commands:
  keygen                         Generate a new keypair and save to frg-cli.key
  balance <pubkey_hex>           Query account balance and nonce
  prepare --to <pubkey> --amount <frg> Create a sender-signed partial tx
  countersign --tx <partial_hex>       Add receiver sig to partial tx
  submit --tx <full_hex>               Submit fully-signed tx to network
  send --to <pubkey> --amount <frg>    Send tokens to a pubkey
  bond --amount <frg>                  Bond this key as an active validator
  unbond                         Start validator unbonding lockup
  finalize-unbond                Release stake after unbonding lockup
  claim-rewards                  Claim validator rewards
  status                        Show network node status
  validators                    List active bonded validators

Flags:
  --key <path>       Keypair file (default: frg-cli.key)
  --addr <host:port> FRG node gRPC address (default: localhost:50051)
  --to <pubkey_hex>  Recipient 32-byte Ed25519 pubkey
  --amount <frg>     Transfer/bond amount in FRG decimal units
  --amount-quanta <n> Raw integer quanta amount (advanced)
  --chain-id <id>    Chain ID for transaction signatures (default: frg-mainnet-1)
  --tx <hex>         Serialized tx for sign/submit
  --sender <name>    Sender label in tx (default: "cli")
  --receiver <name>  Receiver label in tx (default: "cli")
  --output <path>    Output key file (keygen only, default: frg-cli.key)
`)
}

func flagSet(args []string) map[string]string {
	m := map[string]string{}
	for i := 0; i < len(args); i++ {
		a := args[i]
		if len(a) < 2 || a[0] != '-' {
			m[a] = ""
			continue
		}
		if len(a) > 2 && a[1] != '-' {
			if len(a) > 3 && a[2] == '=' {
				m[a[:2]] = a[3:]
			} else if i+1 < len(args) && (len(args[i+1]) < 2 || args[i+1][0] != '-') {
				m[a] = args[i+1]
				i++
			} else {
				m[a] = "true"
			}
		} else {
			k := a[:stringsIndexByte(a, '=')]
			if len(a) > len(k)+1 {
				m[k] = a[len(k)+1:]
			} else if i+1 < len(args) && (len(args[i+1]) < 2 || args[i+1][0] != '-') {
				m[k] = args[i+1]
				i++
			} else {
				m[k] = "true"
			}
		}
	}
	return m
}

func stringsIndexByte(s string, c byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == c {
			return i
		}
	}
	return len(s)
}

func getFlag(m map[string]string, keys ...string) string {
	for _, k := range keys {
		if v, ok := m[k]; ok && v != "" {
			return v
		}
	}
	return ""
}

func resolveKey(path string) (*keys.Keypair, error) {
	if path == "" {
		path = "frg-cli.key"
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read key %s: %w", path, err)
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
		return nil, fmt.Errorf("invalid key length %d", len(data))
	}
}

func keygenCmd(args []string) {
	flags := flagSet(args)
	output := getFlag(flags, "--output", "-o")
	if output == "" {
		output = "frg-cli.key"
	}
	kp, err := keys.GenerateKeypair()
	if err != nil {
		fmt.Fprintf(os.Stderr, "keygen: %v\n", err)
		os.Exit(1)
	}
	seed := kpToSeed(kp)
	if err := os.WriteFile(output, seed[:], 0600); err != nil {
		fmt.Fprintf(os.Stderr, "write key: %v\n", err)
		os.Exit(1)
	}
	pubHex := hex.EncodeToString(kp.PublicKey[:])
	fmt.Printf("pubkey: %s\n", pubHex)
	fmt.Printf("saved:  %s\n", output)
}

func balanceCmd(args []string) {
	flags := flagSet(args)
	addr := getFlag(flags, "--addr", "-a")
	if addr == "" {
		addr = defaultNodeAddr
	}

	pubkeyHex := getFlag(flags, "--pubkey", "-p")
	if pubkeyHex == "" {
		for _, v := range flags {
			pubkeyHex = v
			break
		}
		for _, a := range args {
			if a != "" && a[0] != '-' {
				pubkeyHex = a
				break
			}
		}
	}
	if pubkeyHex == "" {
		fmt.Fprintln(os.Stderr, "balance: missing pubkey argument")
		os.Exit(1)
	}

	pubkeyBytes, err := hex.DecodeString(pubkeyHex)
	if err != nil || len(pubkeyBytes) != 32 {
		fmt.Fprintf(os.Stderr, "balance: invalid pubkey (need 32-byte hex)\n")
		os.Exit(1)
	}

	conn, err := dialNode(addr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "dial %s: %v\n", addr, err)
		os.Exit(1)
	}
	defer conn.Close()

	client := frgpb.NewFRGClient(conn)
	resp, err := client.GetAccount(context.Background(), &frgpb.AccountRequest{Pubkey: pubkeyBytes}, callOpt()...)
	if err != nil {
		fmt.Fprintf(os.Stderr, "GetAccount: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("pubkey:  %s\n", pubkeyHex)
	if bal, ok := new(big.Int).SetString(resp.Balance, 10); ok {
		fmt.Printf("balance: %s FRG\n", denom.FormatFRG(bal))
	}
	fmt.Printf("quanta:  %s\n", resp.Balance)
	fmt.Printf("nonce:   %d\n", resp.Nonce)
}

func prepareCmd(args []string) {
	flags := flagSet(args)
	keyPath := getFlag(flags, "--key", "-k")
	kp, err := resolveKey(keyPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "load key: %v\n", err)
		os.Exit(1)
	}

	toHex := getFlag(flags, "--to", "-t")
	chainID := getFlag(flags, "--chain-id")
	if chainID == "" {
		chainID = tx.DefaultChainID
	}
	senderLabel := getFlag(flags, "--sender")
	if senderLabel == "" {
		senderLabel = "cli"
	}
	receiverLabel := getFlag(flags, "--receiver")
	if receiverLabel == "" {
		receiverLabel = "cli"
	}

	if toHex == "" {
		fmt.Fprintln(os.Stderr, "prepare: --to required")
		os.Exit(1)
	}
	amount, err := parseCLIAmount(flags, "prepare")
	if err != nil {
		fmt.Fprintf(os.Stderr, "prepare: %v\n", err)
		os.Exit(1)
	}

	receiverBytes, err := hex.DecodeString(toHex)
	if err != nil || len(receiverBytes) != 32 {
		fmt.Fprintf(os.Stderr, "prepare: invalid --to pubkey\n")
		os.Exit(1)
	}

	var receiverPubkey [32]byte
	copy(receiverPubkey[:], receiverBytes)

	tr := &tx.Tx{
		Type:           tx.TxTypeTransfer,
		Sender:         senderLabel,
		Receiver:       receiverLabel,
		Value:          amount,
		Nonce:          0,
		SenderPubKey:   kp.PublicKey,
		ReceiverPubKey: receiverPubkey,
	}
	sig, err := tr.SignSenderForChain(kp, chainID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sign: %v\n", err)
		os.Exit(1)
	}
	tr.SenderSig = sig

	txBytes, err := tr.Serialize()
	if err != nil {
		fmt.Fprintf(os.Stderr, "serialize: %v\n", err)
		os.Exit(1)
	}

	txid, _ := tr.ID()
	fmt.Printf("txid:   %x\n", txid[:])
	fmt.Printf("tx_hex: %x\n", txBytes)
	fmt.Fprintln(os.Stderr, "Note: This tx is only sender-signed. Share it with the receiver to countersign.")
}

func countersignCmd(args []string) {
	flags := flagSet(args)
	keyPath := getFlag(flags, "--key", "-k")
	kp, err := resolveKey(keyPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "load key: %v\n", err)
		os.Exit(1)
	}

	txHex := getFlag(flags, "--tx", "-x")
	if txHex == "" {
		fmt.Fprintln(os.Stderr, "countersign: --tx required")
		os.Exit(1)
	}

	txBytes, err := hex.DecodeString(txHex)
	if err != nil {
		fmt.Fprintf(os.Stderr, "decode tx hex: %v\n", err)
		os.Exit(1)
	}

	tr, err := tx.Deserialize(txBytes)
	if err != nil {
		fmt.Fprintf(os.Stderr, "deserialize tx: %v\n", err)
		os.Exit(1)
	}

	if tr.ReceiverPubKey != kp.PublicKey {
		fmt.Fprintf(os.Stderr, "countersign: this key is not the receiver (receiver=%x, our=%x)\n",
			tr.ReceiverPubKey[:8], kp.PublicKey[:8])
		os.Exit(1)
	}

	sig, err := tr.SignReceiver(kp)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sign: %v\n", err)
		os.Exit(1)
	}
	tr.ReceiverSig = sig

	fullTx, err := tr.Serialize()
	if err != nil {
		fmt.Fprintf(os.Stderr, "serialize: %v\n", err)
		os.Exit(1)
	}

	txid, _ := tr.ID()
	fmt.Printf("txid:   %x\n", txid[:])
	fmt.Printf("tx_hex: %x\n", fullTx)
}

func submitCmd(args []string) {
	flags := flagSet(args)
	addr := getFlag(flags, "--addr", "-a")
	if addr == "" {
		addr = defaultNodeAddr
	}

	txHex := getFlag(flags, "--tx", "-x")
	if txHex == "" {
		fmt.Fprintln(os.Stderr, "submit: --tx required")
		os.Exit(1)
	}

	txBytes, err := hex.DecodeString(txHex)
	if err != nil {
		fmt.Fprintf(os.Stderr, "decode tx hex: %v\n", err)
		os.Exit(1)
	}

	conn, err := dialNode(addr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "dial %s: %v\n", addr, err)
		os.Exit(1)
	}
	defer conn.Close()

	client := frgpb.NewFRGClient(conn)
	resp, err := client.SubmitTx(context.Background(), &frgpb.RawBytes{Data: txBytes}, callOpt()...)
	if err != nil {
		fmt.Fprintf(os.Stderr, "SubmitTx: %v\n", err)
		os.Exit(1)
	}
	if !resp.Ok {
		fmt.Fprintf(os.Stderr, "rejected: %s\n", resp.Error)
		os.Exit(1)
	}
	fmt.Println("ok")
}

func sendCmd(args []string) {
	flags := flagSet(args)
	keyPath := getFlag(flags, "--key", "-k")
	addr := getFlag(flags, "--addr", "-a")
	if addr == "" {
		addr = defaultNodeAddr
	}
	toHex := getFlag(flags, "--to", "-t")
	chainID := getFlag(flags, "--chain-id")
	if chainID == "" {
		chainID = tx.DefaultChainID
	}
	senderLabel := getFlag(flags, "--sender")
	if senderLabel == "" {
		senderLabel = "cli"
	}
	receiverLabel := getFlag(flags, "--receiver")
	if receiverLabel == "" {
		receiverLabel = "cli"
	}

	if toHex == "" {
		fmt.Fprintln(os.Stderr, "send: --to required")
		os.Exit(1)
	}
	amount, err := parseCLIAmount(flags, "send")
	if err != nil {
		fmt.Fprintf(os.Stderr, "send: %v\n", err)
		os.Exit(1)
	}

	senderKP, err := resolveKey(keyPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "load sender key: %v\n", err)
		os.Exit(1)
	}

	receiverBytes, err := hex.DecodeString(toHex)
	if err != nil || len(receiverBytes) != 32 {
		fmt.Fprintf(os.Stderr, "send: invalid --to pubkey\n")
		os.Exit(1)
	}

	var receiverPubkey [32]byte
	copy(receiverPubkey[:], receiverBytes)

	conn, err := dialNode(addr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "dial %s: %v\n", addr, err)
		os.Exit(1)
	}
	defer conn.Close()

	client := frgpb.NewFRGClient(conn)
	acct, err := client.GetAccount(context.Background(), &frgpb.AccountRequest{Pubkey: senderKP.PublicKey[:]}, callOpt()...)
	if err != nil {
		fmt.Fprintf(os.Stderr, "GetAccount: %v\n", err)
		os.Exit(1)
	}

	tr := &tx.Tx{
		Type:           tx.TxTypeTransfer,
		Sender:         senderLabel,
		Receiver:       receiverLabel,
		Value:          amount,
		Nonce:          acct.Nonce + 1,
		SenderPubKey:   senderKP.PublicKey,
		ReceiverPubKey: receiverPubkey,
	}

	sig, err := tr.SignSenderForChain(senderKP, chainID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sign sender: %v\n", err)
		os.Exit(1)
	}
	tr.SenderSig = sig

	txBytes, err := tr.Serialize()
	if err != nil {
		fmt.Fprintf(os.Stderr, "serialize: %v\n", err)
		os.Exit(1)
	}

	resp, err := client.SubmitTx(context.Background(), &frgpb.RawBytes{Data: txBytes}, callOpt()...)
	if err != nil {
		fmt.Fprintf(os.Stderr, "SubmitTx: %v\n", err)
		os.Exit(1)
	}
	if !resp.Ok {
		fmt.Fprintf(os.Stderr, "rejected: %s\n", resp.Error)
		os.Exit(1)
	}

	txid, _ := tr.ID()
	fmt.Printf("ok  txid=%x\n", txid[:])
}

func bondCmd(args []string) {
	flags := flagSet(args)
	keyPath := getFlag(flags, "--key", "-k")
	addr := getFlag(flags, "--addr", "-a")
	if addr == "" {
		addr = defaultNodeAddr
	}
	chainID := getFlag(flags, "--chain-id")
	if chainID == "" {
		chainID = tx.DefaultChainID
	}
	amount, err := parseCLIAmount(flags, "bond")
	if err != nil {
		fmt.Fprintf(os.Stderr, "bond: %v\n", err)
		os.Exit(1)
	}
	kp, err := resolveKey(keyPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "load key: %v\n", err)
		os.Exit(1)
	}

	conn, err := dialNode(addr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "dial %s: %v\n", addr, err)
		os.Exit(1)
	}
	defer conn.Close()

	client := frgpb.NewFRGClient(conn)
	acct, err := client.GetAccount(context.Background(), &frgpb.AccountRequest{Pubkey: kp.PublicKey[:]}, callOpt()...)
	if err != nil {
		fmt.Fprintf(os.Stderr, "GetAccount: %v\n", err)
		os.Exit(1)
	}

	tr := &tx.Tx{
		Type:           tx.TxTypeBond,
		Sender:         "validator",
		Receiver:       "staking",
		Value:          amount,
		Nonce:          acct.Nonce + 1,
		SenderPubKey:   kp.PublicKey,
		ReceiverPubKey: kp.PublicKey,
	}
	sig, err := tr.SignSenderForChain(kp, chainID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sign: %v\n", err)
		os.Exit(1)
	}
	tr.SenderSig = sig
	txBytes, err := tr.Serialize()
	if err != nil {
		fmt.Fprintf(os.Stderr, "serialize: %v\n", err)
		os.Exit(1)
	}
	resp, err := client.SubmitTx(context.Background(), &frgpb.RawBytes{Data: txBytes}, callOpt()...)
	if err != nil {
		fmt.Fprintf(os.Stderr, "SubmitTx: %v\n", err)
		os.Exit(1)
	}
	if !resp.Ok {
		fmt.Fprintf(os.Stderr, "rejected: %s\n", resp.Error)
		os.Exit(1)
	}
	txid, _ := tr.ID()
	fmt.Printf("ok  validator=%x amount=%s FRG quanta=%s txid=%x\n", kp.PublicKey[:], denom.FormatFRG(amount), amount.String(), txid[:])
}

func parseCLIAmount(flags map[string]string, command string) (*big.Int, error) {
	amountRaw := getFlag(flags, "--amount", "-m")
	quantaRaw := getFlag(flags, "--amount-quanta", "--quanta")
	if amountRaw != "" && quantaRaw != "" {
		return nil, fmt.Errorf("use --amount or --amount-quanta, not both")
	}
	if amountRaw == "" && quantaRaw == "" {
		return nil, fmt.Errorf("--amount required")
	}
	if quantaRaw != "" {
		amount, err := denom.ParsePositiveQuanta(quantaRaw)
		if err != nil {
			return nil, err
		}
		return amount, nil
	}
	amount, err := denom.ParsePositiveFRG(amountRaw)
	if err != nil {
		return nil, err
	}
	return amount, nil
}

func protocolZeroValueCmd(args []string, name string, typ tx.TxType) {
	flags := flagSet(args)
	keyPath := getFlag(flags, "--key", "-k")
	addr := getFlag(flags, "--addr", "-a")
	if addr == "" {
		addr = defaultNodeAddr
	}
	chainID := getFlag(flags, "--chain-id")
	if chainID == "" {
		chainID = tx.DefaultChainID
	}
	kp, err := resolveKey(keyPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "load key: %v\n", err)
		os.Exit(1)
	}

	conn, err := dialNode(addr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "dial %s: %v\n", addr, err)
		os.Exit(1)
	}
	defer conn.Close()

	client := frgpb.NewFRGClient(conn)
	acct, err := client.GetAccount(context.Background(), &frgpb.AccountRequest{Pubkey: kp.PublicKey[:]}, callOpt()...)
	if err != nil {
		fmt.Fprintf(os.Stderr, "GetAccount: %v\n", err)
		os.Exit(1)
	}

	tr := &tx.Tx{
		Type:           typ,
		Sender:         "validator",
		Receiver:       "protocol",
		Value:          big.NewInt(0),
		Nonce:          acct.Nonce + 1,
		SenderPubKey:   kp.PublicKey,
		ReceiverPubKey: kp.PublicKey,
	}
	sig, err := tr.SignSenderForChain(kp, chainID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sign: %v\n", err)
		os.Exit(1)
	}
	tr.SenderSig = sig
	txBytes, err := tr.Serialize()
	if err != nil {
		fmt.Fprintf(os.Stderr, "serialize: %v\n", err)
		os.Exit(1)
	}
	resp, err := client.SubmitTx(context.Background(), &frgpb.RawBytes{Data: txBytes}, callOpt()...)
	if err != nil {
		fmt.Fprintf(os.Stderr, "SubmitTx: %v\n", err)
		os.Exit(1)
	}
	if !resp.Ok {
		fmt.Fprintf(os.Stderr, "rejected: %s\n", resp.Error)
		os.Exit(1)
	}
	txid, _ := tr.ID()
	fmt.Printf("ok  action=%s validator=%x txid=%x\n", name, kp.PublicKey[:], txid[:])
}

func statusCmd(args []string) {
	flags := flagSet(args)
	addr := getFlag(flags, "--addr", "-a")
	if addr == "" {
		addr = defaultNodeAddr
	}

	conn, err := dialNode(addr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "dial %s: %v\n", addr, err)
		os.Exit(1)
	}
	defer conn.Close()

	client := frgpb.NewFRGClient(conn)
	resp, err := client.GetStatus(context.Background(), &frgpb.Empty{}, callOpt()...)
	if err != nil {
		fmt.Fprintf(os.Stderr, "GetStatus: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("height:            %d\n", resp.Height)
	fmt.Printf("state_root:        %s\n", hex.EncodeToString(resp.StateRoot))
	fmt.Printf("peers:             %d\n", resp.PeerCount)
	fmt.Printf("mempool:           %d\n", resp.MempoolLen)
	fmt.Printf("validators:        %d\n", resp.ValidatorCount)
	fmt.Printf("consensus_phase:   %s\n", resp.ConsensusPhase)
	fmt.Printf("consensus_round:   %d\n", resp.ConsensusRound)
	fmt.Printf("grpc_only:         %v\n", resp.GrpcOnly)
}

func validatorsCmd(args []string) {
	flags := flagSet(args)
	addr := getFlag(flags, "--addr", "-a")
	if addr == "" {
		addr = defaultNodeAddr
	}

	conn, err := dialNode(addr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "dial %s: %v\n", addr, err)
		os.Exit(1)
	}
	defer conn.Close()

	client := frgpb.NewFRGClient(conn)
	resp, err := client.ListValidators(context.Background(), &frgpb.Empty{}, callOpt()...)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ListValidators: %v\n", err)
		os.Exit(1)
	}
	for i, v := range resp.Validators {
		bond := v.Bond
		bondFRG := "0"
		if q, ok := new(big.Int).SetString(v.Bond, 10); ok {
			bondFRG = denom.FormatFRG(q)
		}
		fmt.Printf("%d pubkey=%s bond=%s FRG quanta=%s\n", i+1, hex.EncodeToString(v.Pubkey), bondFRG, bond)
	}
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

func kpToSeed(kp *keys.Keypair) [32]byte {
	priv := [64]byte(kp.PrivateKey)
	seed := [32]byte{}
	copy(seed[:], priv[:32])
	return seed
}
