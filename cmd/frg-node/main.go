package main

import (
	"context"
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/imattau/frg/core/blockloop"
	"github.com/imattau/frg/core/consensus"
	"github.com/imattau/frg/core/genesis"
	"github.com/imattau/frg/core/keys"
	"github.com/imattau/frg/core/ledger"
	"github.com/imattau/frg/core/p2p"
	"github.com/imattau/frg/core/staking"
	"github.com/imattau/frg/core/statemachine"
	"github.com/imattau/frg/core/tx"
	frgpb "github.com/imattau/frg/proto"
	bolt "go.etcd.io/bbolt"
	"google.golang.org/grpc"
)

const (
	defaultKeypairPath    = "frg.key"
	defaultDBPath         = "frg.db"
	defaultGenesisPath    = "genesis.json"
	defaultListenAddr     = "/ip4/127.0.0.1/tcp/7777"
	defaultGRPCListenAddr = "127.0.0.1:50051"
	defaultTimeoutMS      = 3000
	defaultProposeDelayMS = 500
	defaultGenesisBond    = "1000"
	defaultGenesisBalance = "10000"
	defaultChainID        = "frg-mainnet-1"
)

type Config struct {
	Node      NodeConfig      `toml:"node"`
	P2P       P2PConfig       `toml:"p2p"`
	GRPC      GRPCConfig      `toml:"grpc"`
	Consensus ConsensusConfig `toml:"consensus"`
	ChainID   string          `toml:"chain_id"`
}

type NodeConfig struct {
	KeypairPath string `toml:"keypair_path"`
	DBPath      string `toml:"db_path"`
	GenesisPath string `toml:"genesis_path"`
}

type P2PConfig struct {
	Listen     string   `toml:"listen"`
	Peers      []string `toml:"peers"`
	EnableMDNS bool     `toml:"enable_mdns"`
}

type GRPCConfig struct {
	Listen string `toml:"listen"`
}

type ConsensusConfig struct {
	ProposeDelayMS     int `toml:"propose_delay_ms"`
	ProposeTimeoutMS   int `toml:"propose_timeout_ms"`
	PrevoteTimeoutMS   int `toml:"prevote_timeout_ms"`
	PrecommitTimeoutMS int `toml:"precommit_timeout_ms"`
}

func main() {
	configPath := flag.String("config", "config.toml", "path to config.toml")
	grpcOnly := flag.Bool("grpc-only", false, "start only the gRPC admin API and skip P2P/blockloop startup")
	flag.Parse()

	cfg, err := loadConfig(*configPath)
	if err != nil {
		log.Fatalf("Load config: %v", err)
	}

	kp, err := loadOrGenerateKeypair(cfg.Node.KeypairPath)
	if err != nil {
		log.Fatalf("Load keypair: %v", err)
	}
	log.Printf("Node started with PubKey: %x", kp.PublicKey)

	if err := ensureGenesis(cfg.Node.GenesisPath, cfg.ChainID, kp); err != nil {
		log.Fatalf("Prepare genesis: %v", err)
	}

	db, err := bolt.Open(cfg.Node.DBPath, 0600, nil)
	if err != nil {
		log.Fatalf("Open DB: %v", err)
	}
	defer db.Close()

	l, err := ledger.New(db)
	if err != nil {
		log.Fatalf("Init ledger: %v", err)
	}

	s, err := staking.New(db, l)
	if err != nil {
		log.Fatalf("Init staking: %v", err)
	}

	sm, err := statemachine.New(db, l, s)
	if err != nil {
		log.Fatalf("Init statemachine: %v", err)
	}

	g, err := genesis.Load(cfg.Node.GenesisPath)
	if err != nil {
		log.Fatalf("Load genesis: %v", err)
	}

	if err := genesis.Apply(sm, l, s, g); err != nil {
		log.Fatalf("Apply genesis: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	runtime := &nodeRuntime{grpcOnly: *grpcOnly, sm: sm, staking: s, ledger: l}
	var p2pNode *p2p.Node
	if !*grpcOnly {
		p2pNode, err = p2p.New(ctx, kp, p2p.Config{
			ListenAddr:     cfg.P2P.Listen,
			BootstrapPeers: cfg.P2P.Peers,
			ChainID:        cfg.ChainID,
			EnableMDNS:     cfg.P2P.EnableMDNS,
		})
		if err != nil {
			log.Fatalf("Init P2P: %v", err)
		}
		defer p2pNode.Close()
		runtime.p2p = p2pNode
	} else {
		log.Printf("grpc-only mode enabled; skipping P2P/blockloop startup")
	}

	grpcServer, grpcAddr, err := startGRPCServer(cfg.GRPC.Listen, runtime)
	if err != nil {
		log.Fatalf("Init gRPC: %v", err)
	}
	defer grpcServer.GracefulStop()
	log.Printf("gRPC admin API listening on %s", grpcAddr)

	var bl *blockloop.BlockLoop
	var engine *consensus.Engine
	if !*grpcOnly && p2pNode != nil {
		bl = blockloop.New(kp, p2pNode)
		runtime.blockloop = bl
		if err := bl.Start(ctx); err != nil {
			log.Fatalf("Start blockloop: %v", err)
		}
		defer bl.Stop()

		timeoutCfg := consensus.TimeoutConfig{
			ProposeDelay: time.Duration(cfg.Consensus.ProposeDelayMS) * time.Millisecond,
			Propose:      time.Duration(cfg.Consensus.ProposeTimeoutMS) * time.Millisecond,
			Prevote:      time.Duration(cfg.Consensus.PrevoteTimeoutMS) * time.Millisecond,
			Precommit:    time.Duration(cfg.Consensus.PrecommitTimeoutMS) * time.Millisecond,
		}

		engine = consensus.New(kp, s, sm, p2pNode, bl, timeoutCfg)
		runtime.engine = engine

		go func() {
			if err := engine.Start(ctx); err != nil {
				log.Printf("Consensus engine failed: %v", err)
				cancel()
			}
		}()
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh

	log.Println("Shutting down...")
	if engine != nil {
		engine.Stop()
	}
}

func loadConfig(path string) (Config, error) {
	cfg := defaultConfig()

	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			log.Printf("Config %s not found; using built-in defaults", path)
			normalizeConfig(&cfg)
			return cfg, nil
		}
		return Config{}, fmt.Errorf("stat config: %w", err)
	}

	if _, err := toml.DecodeFile(path, &cfg); err != nil {
		return Config{}, fmt.Errorf("decode config: %w", err)
	}
	normalizeConfig(&cfg)
	return cfg, nil
}

func defaultConfig() Config {
	return Config{
		Node: NodeConfig{
			KeypairPath: defaultKeypairPath,
			DBPath:      defaultDBPath,
			GenesisPath: defaultGenesisPath,
		},
		P2P: P2PConfig{
			Listen: defaultListenAddr,
		},
		GRPC: GRPCConfig{
			Listen: defaultGRPCListenAddr,
		},
		Consensus: ConsensusConfig{
			ProposeDelayMS:     defaultProposeDelayMS,
			ProposeTimeoutMS:   defaultTimeoutMS,
			PrevoteTimeoutMS:   defaultTimeoutMS,
			PrecommitTimeoutMS: defaultTimeoutMS,
		},
	}
}

func normalizeConfig(cfg *Config) {
	if strings.TrimSpace(cfg.Node.KeypairPath) == "" {
		cfg.Node.KeypairPath = defaultKeypairPath
	}
	if strings.TrimSpace(cfg.Node.DBPath) == "" {
		cfg.Node.DBPath = defaultDBPath
	}
	if strings.TrimSpace(cfg.Node.GenesisPath) == "" {
		cfg.Node.GenesisPath = defaultGenesisPath
	}
	if strings.TrimSpace(cfg.P2P.Listen) == "" {
		cfg.P2P.Listen = defaultListenAddr
	}
	if strings.TrimSpace(cfg.GRPC.Listen) == "" {
		cfg.GRPC.Listen = defaultGRPCListenAddr
	}
	if strings.TrimSpace(cfg.ChainID) == "" {
		cfg.ChainID = defaultChainID
	}
	if cfg.Consensus.ProposeDelayMS <= 0 {
		cfg.Consensus.ProposeDelayMS = defaultProposeDelayMS
	}
	if cfg.Consensus.ProposeTimeoutMS <= 0 {
		cfg.Consensus.ProposeTimeoutMS = defaultTimeoutMS
	}
	if cfg.Consensus.PrevoteTimeoutMS <= 0 {
		cfg.Consensus.PrevoteTimeoutMS = defaultTimeoutMS
	}
	if cfg.Consensus.PrecommitTimeoutMS <= 0 {
		cfg.Consensus.PrecommitTimeoutMS = defaultTimeoutMS
	}
}

func loadOrGenerateKeypair(path string) (*keys.Keypair, error) {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		kp, err := keys.GenerateKeypair()
		if err != nil {
			return nil, err
		}
		if err := ensureParentDir(path); err != nil {
			return nil, err
		}
		seed := ed25519.PrivateKey(kp.PrivateKey[:]).Seed()
		if err := os.WriteFile(path, seed, 0600); err != nil {
			return nil, err
		}
		return kp, nil
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
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
		return nil, fmt.Errorf("invalid keypair file length: %d", len(data))
	}
}

func ensureGenesis(path string, chainID string, kp *keys.Keypair) error {
	if _, err := os.Stat(path); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("stat genesis: %w", err)
	}

	if err := ensureParentDir(path); err != nil {
		return err
	}

	g := genesis.Genesis{
		ChainID: chainID,
		Validators: []genesis.ValidatorEntry{
			{
				PubKey: hex.EncodeToString(kp.PublicKey[:]),
				Bond:   defaultGenesisBond,
			},
		},
		Balances: []genesis.BalanceEntry{
			{
				Account: hex.EncodeToString(kp.PublicKey[:]),
				Amount:  defaultGenesisBalance,
			},
		},
	}

	data, err := json.MarshalIndent(g, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal genesis: %w", err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("write genesis: %w", err)
	}
	log.Printf("Created bootstrap genesis at %s", path)
	return nil
}

func ensureParentDir(path string) error {
	dir := filepath.Dir(path)
	if dir == "." || dir == "" {
		return nil
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("create parent dir %s: %w", dir, err)
	}
	return nil
}

func startGRPCServer(listenAddr string, node nodeRuntimeAPI) (*grpc.Server, string, error) {
	ln, err := net.Listen("tcp", listenAddr)
	if err != nil {
		return nil, "", fmt.Errorf("listen %s: %w", listenAddr, err)
	}

	server := grpc.NewServer(grpc.MaxRecvMsgSize(8 << 20))
	frgpb.RegisterFRGServer(server, &nodeGRPCServer{node: node, stat: node, query: node})

	go func() {
		if err := server.Serve(ln); err != nil {
			log.Printf("gRPC server stopped: %v", err)
		}
	}()

	return server, ln.Addr().String(), nil
}

type nodeRuntimeAPI interface {
	nodeAPI
	nodeStatusAPI
	nodeQueryAPI
}

type nodeRuntime struct {
	grpcOnly  bool
	p2p       *p2p.Node
	blockloop *blockloop.BlockLoop
	sm        *statemachine.StateMachine
	staking   *staking.Store
	ledger    *ledger.Ledger
	engine    *consensus.Engine
}

func (n *nodeRuntime) BroadcastTx(t *tx.Tx) error {
	if n.p2p == nil {
		return nil
	}
	return n.p2p.BroadcastTx(t)
}

func (n *nodeRuntime) BroadcastBatch(txs []*tx.Tx) error {
	if n.p2p == nil {
		return nil
	}
	return n.p2p.BroadcastBatch(txs)
}

func (n *nodeRuntime) SubscribeBlockHeaders() <-chan []byte {
	if n.p2p == nil {
		ch := make(chan []byte)
		close(ch)
		return ch
	}
	return n.p2p.SubscribeBlockHeaders()
}

func (n *nodeRuntime) Status() (*frgpb.StatusResponse, error) {
	var height uint64
	var stateRoot [32]byte
	var peerCount uint64
	var mempoolLen uint64
	var validatorCount uint64
	var consensusRound uint32
	var consensusPhase string

	if n.sm != nil {
		h, err := n.sm.CurrentHeight()
		if err != nil {
			return nil, err
		}
		root, err := n.sm.CurrentStateRoot()
		if err != nil {
			return nil, err
		}
		height = h
		stateRoot = root
	}
	if n.p2p != nil {
		peerCount = uint64(n.p2p.PeerCount())
	}
	if n.blockloop != nil {
		mempoolLen = uint64(n.blockloop.Len())
	}
	if n.staking != nil {
		validators, err := n.staking.ValidatorSet()
		if err != nil {
			return nil, err
		}
		validatorCount = uint64(len(validators))
	}
	if n.engine != nil {
		status := n.engine.Status()
		consensusRound = status.Round
		consensusPhase = consensusPhaseName(status.Phase)
		if height == 0 && status.Height > 0 {
			height = status.Height - 1
		}
	}

	return &frgpb.StatusResponse{
		Height:         height,
		StateRoot:      stateRoot[:],
		PeerCount:      peerCount,
		MempoolLen:     mempoolLen,
		ValidatorCount: validatorCount,
		ConsensusRound: consensusRound,
		ConsensusPhase: consensusPhase,
		GrpcOnly:       n.grpcOnly,
	}, nil
}

func (n *nodeRuntime) GetAccount(pubkey [32]byte) (*frgpb.AccountResponse, error) {
	bal, err := n.ledger.BalanceOf(pubkey)
	if err != nil {
		return nil, fmt.Errorf("balance: %w", err)
	}
	nonce, err := n.ledger.NonceOf(pubkey)
	if err != nil {
		return nil, fmt.Errorf("nonce: %w", err)
	}
	return &frgpb.AccountResponse{
		Pubkey:  pubkey[:],
		Balance: bal.String(),
		Nonce:   nonce,
	}, nil
}

func (n *nodeRuntime) ListValidators() (*frgpb.ValidatorList, error) {
	pubkeys, bonds, err := n.staking.BondedAmounts()
	if err != nil {
		return nil, fmt.Errorf("bonded amounts: %w", err)
	}
	entries := make([]*frgpb.ValidatorEntry, len(pubkeys))
	for i, pk := range pubkeys {
		pkCopy := pk
		bond := "0"
		if i < len(bonds) && bonds[i] != nil {
			bond = bonds[i].String()
		}
		entries[i] = &frgpb.ValidatorEntry{
			Pubkey: pkCopy[:],
			Bond:   bond,
		}
	}
	return &frgpb.ValidatorList{Validators: entries}, nil
}

func (n *nodeRuntime) ListMempool() (*frgpb.MempoolList, error) {
	if n.blockloop == nil {
		return &frgpb.MempoolList{}, nil
	}
	txs := n.blockloop.Snapshot()
	entries := make([]*frgpb.MempoolEntry, 0, len(txs))
	for _, t := range txs {
		txid, err := t.ID()
		if err != nil {
			continue
		}
		id := txid
		entries = append(entries, &frgpb.MempoolEntry{
			Txid:   id[:],
			Sender: t.Sender,
			Nonce:  t.Nonce,
		})
	}
	return &frgpb.MempoolList{Entries: entries}, nil
}

func consensusPhaseName(p consensus.Phase) string {
	switch p {
	case consensus.PhasePropose:
		return "propose"
	case consensus.PhasePrevote:
		return "prevote"
	case consensus.PhasePrecommit:
		return "precommit"
	case consensus.PhaseCommit:
		return "commit"
	default:
		return "unknown"
	}
}
