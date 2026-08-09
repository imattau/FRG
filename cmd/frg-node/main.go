package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"math/big"
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
	libp2pCrypto "github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/peer"
	bolt "go.etcd.io/bbolt"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/health"
	"google.golang.org/grpc/health/grpc_health_v1"
)

const (
	defaultKeypairPath       = "frg.key"
	defaultDBPath            = "frg.db"
	defaultGenesisPath       = "genesis.json"
	defaultListenAddr        = "/ip4/127.0.0.1/tcp/7777"
	defaultGRPCListenAddr    = "127.0.0.1:50051"
	defaultMetricsListenAddr = "127.0.0.1:9090"
	defaultTimeoutMS         = 3000
	defaultProposeDelayMS    = 500
	defaultGenesisBond       = "1000"
	defaultGenesisBalance    = "10000"
	defaultChainID           = "frg-mainnet-1"
)

type Config struct {
	Node      NodeConfig      `toml:"node"`
	P2P       P2PConfig       `toml:"p2p"`
	GRPC      GRPCConfig      `toml:"grpc"`
	Metrics   MetricsConfig   `toml:"metrics"`
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
	Listen          string            `toml:"listen"`
	TLSCertFile     string            `toml:"tls_cert_file"`
	TLSKeyFile      string            `toml:"tls_key_file"`
	TLSClientCAFile string            `toml:"tls_client_ca_file"`
	ClientRoles     map[string]string `toml:"client_roles"`
}

type MetricsConfig struct {
	Listen string `toml:"listen"`
}

type ConsensusConfig struct {
	ProposeDelayMS     int `toml:"propose_delay_ms"`
	ProposeTimeoutMS   int `toml:"propose_timeout_ms"`
	PrevoteTimeoutMS   int `toml:"prevote_timeout_ms"`
	PrecommitTimeoutMS int `toml:"precommit_timeout_ms"`
}

func main() {
	if len(os.Args) > 1 && os.Args[1] == "init" {
		if err := runNodeInit(os.Args[2:], os.Stdout); err != nil {
			log.Fatalf("Init: %v", err)
		}
		return
	}

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
	if g.ChainID != "" && g.ChainID != cfg.ChainID {
		log.Fatalf("genesis chain ID %q does not match configured chain ID %q", g.ChainID, cfg.ChainID)
	}

	if err := genesis.Apply(sm, l, s, g); err != nil {
		log.Fatalf("Apply genesis: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	metrics := newNodeMetrics()
	runtime := &nodeRuntime{grpcOnly: *grpcOnly, sm: sm, staking: s, ledger: l, metrics: metrics}
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
		p2pNode.SetBlockProvider(func(height uint64) ([]byte, error) {
			block, err := sm.BlockAt(height)
			if err != nil || block == nil {
				return nil, err
			}
			return statemachine.SerializeBlock(block)
		})
		catchupCtx, catchupCancel := context.WithTimeout(ctx, 5*time.Second)
		if err := catchUpCommittedBlocks(catchupCtx, p2pNode, sm, s, cfg.ChainID, metrics); err != nil {
			catchupCancel()
			log.Fatalf("Catch up committed blocks: %v", err)
		}
		catchupCancel()
		defer p2pNode.Close()
		runtime.p2p = p2pNode
	} else {
		log.Printf("grpc-only mode enabled; skipping P2P/blockloop startup")
	}

	metricsServer, metricsAddr, err := startMetricsServer(cfg.Metrics.Listen, runtime, metrics)
	if err != nil {
		log.Fatalf("Init metrics server: %v", err)
	}
	if metricsServer != nil {
		defer func() { _ = metricsServer.Shutdown(context.Background()) }()
		log.Printf("metrics endpoint listening on %s", metricsAddr)
	}

	grpcServer, grpcAddr, err := startGRPCServer(cfg.GRPC.Listen, cfg.GRPC.TLSCertFile, cfg.GRPC.TLSKeyFile, cfg.GRPC.TLSClientCAFile, cfg.GRPC.ClientRoles, cfg.ChainID, runtime, metrics)
	if err != nil {
		log.Fatalf("Init gRPC: %v", err)
	}
	defer grpcServer.GracefulStop()
	log.Printf("gRPC admin API listening on %s", grpcAddr)

	var bl *blockloop.BlockLoop
	var engine *consensus.Engine
	if !*grpcOnly && p2pNode != nil {
		bl = blockloop.NewWithChainID(kp, p2pNode, cfg.ChainID)
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

		engine = consensus.NewWithChainID(kp, s, sm, p2pNode, bl, timeoutCfg, cfg.ChainID)
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

func runNodeInit(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	fs.SetOutput(out)
	dataDir := fs.String("data-dir", ".", "node data directory")
	chainID := fs.String("chain-id", defaultChainID, "chain identifier")
	p2pListen := fs.String("p2p-listen", "/ip4/0.0.0.0/tcp/7777", "P2P listen multiaddr")
	grpcListen := fs.String("grpc-listen", defaultGRPCListenAddr, "gRPC listen address")
	grpcTLSCertFile := fs.String("grpc-tls-cert-file", "", "gRPC TLS server certificate path")
	grpcTLSKeyFile := fs.String("grpc-tls-key-file", "", "gRPC TLS server key path")
	grpcTLSClientCAFile := fs.String("grpc-tls-client-ca-file", "", "gRPC client CA path for mTLS")
	metricsListen := fs.String("metrics-listen", defaultMetricsListenAddr, "metrics/readiness listen address")
	peers := fs.String("peers", "", "comma-separated bootstrap peer multiaddrs")
	enableMDNS := fs.Bool("enable-mdns", false, "enable mDNS discovery")
	bootstrapGenesis := fs.Bool("bootstrap-genesis", false, "create a private single-validator genesis if missing")
	force := fs.Bool("force", false, "overwrite generated config.toml and .env")
	if err := fs.Parse(args); err != nil {
		return err
	}

	if err := os.MkdirAll(*dataDir, 0755); err != nil {
		return fmt.Errorf("create data dir: %w", err)
	}

	keyPath := filepath.Join(*dataDir, defaultKeypairPath)
	configPath := filepath.Join(*dataDir, "config.toml")
	envPath := filepath.Join(*dataDir, ".env")
	genesisPath := filepath.Join(*dataDir, defaultGenesisPath)
	dbPath := filepath.Join(*dataDir, defaultDBPath)

	kp, err := loadOrGenerateKeypair(keyPath)
	if err != nil {
		return fmt.Errorf("keypair: %w", err)
	}
	peerID, err := peerIDForKeypair(kp)
	if err != nil {
		return err
	}

	cfg := defaultConfig()
	cfg.Node.KeypairPath = keyPath
	cfg.Node.DBPath = dbPath
	cfg.Node.GenesisPath = genesisPath
	cfg.P2P.Listen = *p2pListen
	cfg.P2P.Peers = splitCommaList(*peers)
	cfg.P2P.EnableMDNS = *enableMDNS
	cfg.GRPC.Listen = *grpcListen
	cfg.GRPC.TLSCertFile = *grpcTLSCertFile
	cfg.GRPC.TLSKeyFile = *grpcTLSKeyFile
	cfg.GRPC.TLSClientCAFile = *grpcTLSClientCAFile
	cfg.Metrics.Listen = *metricsListen
	cfg.ChainID = *chainID
	normalizeConfig(&cfg)
	if err := validateConfig(cfg); err != nil {
		return err
	}

	if *bootstrapGenesis {
		if err := ensureGenesis(genesisPath, cfg.ChainID, kp); err != nil {
			return err
		}
	}

	if err := writeConfigFile(configPath, cfg, *force); err != nil {
		return err
	}
	if err := writeEnvFile(envPath, cfg, kp, peerID, *force); err != nil {
		return err
	}

	fmt.Fprintf(out, "FRG node initialized in %s\n", *dataDir)
	fmt.Fprintf(out, "  validator_pubkey=%x\n", kp.PublicKey)
	fmt.Fprintf(out, "  peer_id=%s\n", peerID)
	fmt.Fprintf(out, "  p2p_listen=%s\n", cfg.P2P.Listen)
	fmt.Fprintf(out, "  advertised_multiaddr=%s/p2p/%s\n", cfg.P2P.Listen, peerID)
	fmt.Fprintf(out, "  config=%s\n", configPath)
	fmt.Fprintf(out, "  env=%s\n", envPath)
	if !*bootstrapGenesis {
		fmt.Fprintf(out, "  genesis=%s (mount or copy network genesis before starting)\n", genesisPath)
	}
	return nil
}

func loadConfig(path string) (Config, error) {
	cfg := defaultConfig()

	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			log.Printf("Config %s not found; using built-in defaults", path)
			normalizeConfig(&cfg)
			if err := validateConfig(cfg); err != nil {
				return Config{}, err
			}
			return cfg, nil
		}
		return Config{}, fmt.Errorf("stat config: %w", err)
	}

	if _, err := toml.DecodeFile(path, &cfg); err != nil {
		return Config{}, fmt.Errorf("decode config: %w", err)
	}
	normalizeConfig(&cfg)
	if err := validateConfig(cfg); err != nil {
		return Config{}, err
	}
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
		Metrics: MetricsConfig{
			Listen: defaultMetricsListenAddr,
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
	if strings.TrimSpace(cfg.Metrics.Listen) == "" {
		cfg.Metrics.Listen = defaultMetricsListenAddr
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

func validateConfig(cfg Config) error {
	if cfg.ChainID == "" || len(cfg.ChainID) > 64 {
		return fmt.Errorf("chain_id must be 1-64 characters")
	}
	for _, r := range cfg.ChainID {
		if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' || r == '.') {
			return fmt.Errorf("chain_id contains unsupported character %q", r)
		}
	}
	if cfg.Node.KeypairPath == cfg.Node.DBPath || cfg.Node.KeypairPath == cfg.Node.GenesisPath || cfg.Node.DBPath == cfg.Node.GenesisPath {
		return fmt.Errorf("node keypair, database, and genesis paths must be distinct")
	}
	if cfg.Consensus.ProposeDelayMS >= cfg.Consensus.ProposeTimeoutMS || cfg.Consensus.ProposeTimeoutMS <= 0 || cfg.Consensus.PrevoteTimeoutMS <= 0 || cfg.Consensus.PrecommitTimeoutMS <= 0 {
		return fmt.Errorf("consensus timeouts are invalid")
	}
	if requiresGRPCTLS(cfg.GRPC.Listen) && (cfg.GRPC.TLSCertFile == "" || cfg.GRPC.TLSKeyFile == "" || cfg.GRPC.TLSClientCAFile == "") {
		return fmt.Errorf("non-loopback gRPC listeners require mutual TLS")
	}
	if cfg.Metrics.Listen != "" && !isLoopbackTCPAddress(cfg.Metrics.Listen) {
		return fmt.Errorf("metrics listener must be loopback")
	}
	if _, err := newRPCAuthorizer(cfg.GRPC.ClientRoles, requiresGRPCTLS(cfg.GRPC.Listen) || len(cfg.GRPC.ClientRoles) > 0); err != nil {
		return err
	}
	return nil
}

func loadOrGenerateKeypair(path string) (*keys.Keypair, error) {
	info, err := os.Stat(path)
	if os.IsNotExist(err) {
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
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("keypair path is not a regular file")
	}
	if info.Mode().Perm()&0077 != 0 {
		return nil, fmt.Errorf("keypair file permissions are too broad: %04o", info.Mode().Perm())
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

func peerIDForKeypair(kp *keys.Keypair) (string, error) {
	privKey, err := libp2pCrypto.UnmarshalEd25519PrivateKey(kp.PrivateKey[:])
	if err != nil {
		return "", fmt.Errorf("p2p key: %w", err)
	}
	pid, err := peer.IDFromPrivateKey(privKey)
	if err != nil {
		return "", fmt.Errorf("peer id: %w", err)
	}
	return pid.String(), nil
}

func splitCommaList(value string) []string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	parts := strings.Split(value, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func writeConfigFile(path string, cfg Config, force bool) error {
	if !force {
		if _, err := os.Stat(path); err == nil {
			return nil
		} else if !os.IsNotExist(err) {
			return fmt.Errorf("stat config: %w", err)
		}
	}
	if err := ensureParentDir(path); err != nil {
		return err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "chain_id = %q\n\n", cfg.ChainID)
	fmt.Fprintf(&b, "[node]\n")
	fmt.Fprintf(&b, "keypair_path = %q\n", cfg.Node.KeypairPath)
	fmt.Fprintf(&b, "db_path = %q\n", cfg.Node.DBPath)
	fmt.Fprintf(&b, "genesis_path = %q\n\n", cfg.Node.GenesisPath)
	fmt.Fprintf(&b, "[p2p]\n")
	fmt.Fprintf(&b, "listen = %q\n", cfg.P2P.Listen)
	fmt.Fprintf(&b, "peers = [\n")
	for _, p := range cfg.P2P.Peers {
		fmt.Fprintf(&b, "  %q,\n", p)
	}
	fmt.Fprintf(&b, "]\n")
	fmt.Fprintf(&b, "enable_mdns = %t\n\n", cfg.P2P.EnableMDNS)
	fmt.Fprintf(&b, "[grpc]\n")
	fmt.Fprintf(&b, "listen = %q\n", cfg.GRPC.Listen)
	if cfg.GRPC.TLSCertFile != "" {
		fmt.Fprintf(&b, "tls_cert_file = %q\n", cfg.GRPC.TLSCertFile)
	}
	if cfg.GRPC.TLSKeyFile != "" {
		fmt.Fprintf(&b, "tls_key_file = %q\n", cfg.GRPC.TLSKeyFile)
	}
	if cfg.GRPC.TLSClientCAFile != "" {
		fmt.Fprintf(&b, "tls_client_ca_file = %q\n", cfg.GRPC.TLSClientCAFile)
	}
	fmt.Fprintf(&b, "\n[metrics]\n")
	fmt.Fprintf(&b, "listen = %q\n\n", cfg.Metrics.Listen)
	fmt.Fprintf(&b, "[consensus]\n")
	fmt.Fprintf(&b, "propose_delay_ms = %d\n", cfg.Consensus.ProposeDelayMS)
	fmt.Fprintf(&b, "propose_timeout_ms = %d\n", cfg.Consensus.ProposeTimeoutMS)
	fmt.Fprintf(&b, "prevote_timeout_ms = %d\n", cfg.Consensus.PrevoteTimeoutMS)
	fmt.Fprintf(&b, "precommit_timeout_ms = %d\n", cfg.Consensus.PrecommitTimeoutMS)
	return os.WriteFile(path, []byte(b.String()), 0644)
}

func writeEnvFile(path string, cfg Config, kp *keys.Keypair, peerID string, force bool) error {
	if !force {
		if _, err := os.Stat(path); err == nil {
			return nil
		} else if !os.IsNotExist(err) {
			return fmt.Errorf("stat env: %w", err)
		}
	}
	if err := ensureParentDir(path); err != nil {
		return err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "FRG_CHAIN_ID=%s\n", cfg.ChainID)
	fmt.Fprintf(&b, "FRG_DATA_DIR=%s\n", filepath.Dir(cfg.Node.DBPath))
	fmt.Fprintf(&b, "FRG_CONFIG=%s\n", filepath.Join(filepath.Dir(cfg.Node.DBPath), "config.toml"))
	fmt.Fprintf(&b, "FRG_KEY_PATH=%s\n", cfg.Node.KeypairPath)
	fmt.Fprintf(&b, "FRG_DB_PATH=%s\n", cfg.Node.DBPath)
	fmt.Fprintf(&b, "FRG_GENESIS_PATH=%s\n", cfg.Node.GenesisPath)
	fmt.Fprintf(&b, "FRG_P2P_LISTEN=%s\n", cfg.P2P.Listen)
	fmt.Fprintf(&b, "FRG_P2P_PEERS=%s\n", strings.Join(cfg.P2P.Peers, ","))
	fmt.Fprintf(&b, "FRG_P2P_ENABLE_MDNS=%t\n", cfg.P2P.EnableMDNS)
	fmt.Fprintf(&b, "FRG_GRPC_LISTEN=%s\n", cfg.GRPC.Listen)
	if cfg.GRPC.TLSCertFile != "" {
		fmt.Fprintf(&b, "FRG_GRPC_TLS_CERT_FILE=%s\n", cfg.GRPC.TLSCertFile)
	}
	if cfg.GRPC.TLSKeyFile != "" {
		fmt.Fprintf(&b, "FRG_GRPC_TLS_KEY_FILE=%s\n", cfg.GRPC.TLSKeyFile)
	}
	if cfg.GRPC.TLSClientCAFile != "" {
		fmt.Fprintf(&b, "FRG_GRPC_TLS_CLIENT_CA_FILE=%s\n", cfg.GRPC.TLSClientCAFile)
	}
	fmt.Fprintf(&b, "FRG_METRICS_LISTEN=%s\n", cfg.Metrics.Listen)
	fmt.Fprintf(&b, "FRG_VALIDATOR_PUBKEY=%s\n", hex.EncodeToString(kp.PublicKey[:]))
	fmt.Fprintf(&b, "FRG_PEER_ID=%s\n", peerID)
	fmt.Fprintf(&b, "FRG_ADVERTISED_MULTIADDR=%s\n", cfg.P2P.Listen+"/p2p/"+peerID)
	return os.WriteFile(path, []byte(b.String()), 0644)
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

func startGRPCServer(listenAddr, certFile, keyFile, clientCAFile string, clientRoles map[string]string, chainID string, node nodeRuntimeAPI, metrics *nodeMetrics) (*grpc.Server, string, error) {
	ln, err := net.Listen("tcp", listenAddr)
	if err != nil {
		return nil, "", fmt.Errorf("listen %s: %w", listenAddr, err)
	}

	if (certFile == "") != (keyFile == "") {
		_ = ln.Close()
		return nil, "", fmt.Errorf("TLS certificate and key must be configured together")
	}
	if certFile == "" && clientCAFile != "" {
		_ = ln.Close()
		return nil, "", fmt.Errorf("TLS client CA requires a server certificate")
	}
	if requiresGRPCTLS(listenAddr) && (certFile == "" || keyFile == "" || clientCAFile == "") {
		_ = ln.Close()
		return nil, "", fmt.Errorf("non-loopback gRPC listeners require mutual TLS")
	}
	authorizer, err := newRPCAuthorizer(clientRoles, requiresGRPCTLS(listenAddr) || len(clientRoles) > 0)
	if err != nil {
		_ = ln.Close()
		return nil, "", fmt.Errorf("configure RPC authorization: %w", err)
	}
	serverOpts := []grpc.ServerOption{grpc.MaxRecvMsgSize(8 << 20)}
	if certFile != "" {
		cert, err := tls.LoadX509KeyPair(certFile, keyFile)
		if err != nil {
			_ = ln.Close()
			return nil, "", fmt.Errorf("load gRPC TLS certificate: %w", err)
		}
		tlsConfig := &tls.Config{
			MinVersion:   tls.VersionTLS13,
			Certificates: []tls.Certificate{cert},
		}
		if clientCAFile != "" {
			caPEM, err := os.ReadFile(clientCAFile)
			if err != nil {
				_ = ln.Close()
				return nil, "", fmt.Errorf("read gRPC client CA: %w", err)
			}
			clientCAs := x509.NewCertPool()
			if !clientCAs.AppendCertsFromPEM(caPEM) {
				_ = ln.Close()
				return nil, "", fmt.Errorf("parse gRPC client CA")
			}
			tlsConfig.ClientCAs = clientCAs
			tlsConfig.ClientAuth = tls.RequireAndVerifyClientCert
		}
		serverOpts = append(serverOpts, grpc.Creds(credentials.NewTLS(tlsConfig)))
	}
	server := grpc.NewServer(serverOpts...)
	frgpb.RegisterFRGServer(server, &nodeGRPCServer{node: node, stat: node, query: node, chainID: chainID, limiter: newSubmitLimiter(), metrics: metrics, authorizer: authorizer})
	healthServer := health.NewServer()
	healthServer.SetServingStatus("", grpc_health_v1.HealthCheckResponse_SERVING)
	grpc_health_v1.RegisterHealthServer(server, healthServer)

	go func() {
		if err := server.Serve(ln); err != nil {
			log.Printf("gRPC server stopped: %v", err)
		}
	}()

	return server, ln.Addr().String(), nil
}

func catchUpCommittedBlocks(ctx context.Context, node *p2p.Node, sm *statemachine.StateMachine, stakingStore *staking.Store, chainID string, metrics *nodeMetrics) error {
	validators, stakes, err := stakingStore.BondedAmounts()
	if err != nil {
		return err
	}
	validatorSet := make(map[[32]byte]struct{}, len(validators))
	for _, validator := range validators {
		validatorSet[validator] = struct{}{}
	}
	for {
		current, err := sm.CurrentHeight()
		if err != nil {
			return err
		}
		if current == ^uint64(0) {
			return nil
		}
		peers := node.Peers()
		if len(peers) == 0 {
			return nil
		}
		from := current + 1
		to := from + 1023
		if to < from {
			to = ^uint64(0)
		}
		var selected []*statemachine.Block
		var selectedCount int
		candidateCount := 0
		for attempt := 0; attempt < 3 && selected == nil; attempt++ {
			candidates := make(map[[32]byte]struct {
				blocks []*statemachine.Block
				count  int
			})
			candidateCount = 0
			for _, id := range peers {
				if metrics != nil {
					metrics.syncAttempts.Add(1)
				}
				requestCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
				rawBlocks, syncErr := node.SyncBlocks(requestCtx, id, from, to)
				cancel()
				if syncErr != nil || len(rawBlocks) == 0 {
					if syncErr != nil && metrics != nil {
						metrics.syncFailures.Add(1)
					}
					continue
				}
				decoded, valid := validateSyncedBlocks(rawBlocks, from, validators, stakes, validatorSet, chainID)
				if !valid {
					continue
				}
				candidateCount++
				key := syncedBlockRangeKey(rawBlocks)
				candidate := candidates[key]
				candidate.blocks = decoded
				candidate.count++
				candidates[key] = candidate
			}
			for _, candidate := range candidates {
				if candidate.count > selectedCount {
					selected = candidate.blocks
					selectedCount = candidate.count
				}
			}
			if selected != nil && (candidateCount == 1 || selectedCount >= 2) {
				break
			}
			selected = nil
			selectedCount = 0
			if attempt < 2 {
				delay := time.Duration(1<<attempt) * 100 * time.Millisecond
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-time.After(delay):
				}
			}
		}
		if selected == nil {
			if candidateCount == 0 {
				return nil
			}
			return fmt.Errorf("connected peers did not agree on blocks at height %d", from)
		}
		for _, block := range selected {
			if _, applyErr := sm.ApplyBlockForChain(block, chainID); applyErr != nil {
				return fmt.Errorf("apply synced block %d: %w", block.Height, applyErr)
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
	}
}

func validateSyncedBlocks(rawBlocks [][]byte, from uint64, validators [][32]byte, stakes []*big.Int, validatorSet map[[32]byte]struct{}, chainID string) ([]*statemachine.Block, bool) {
	decoded := make([]*statemachine.Block, 0, len(rawBlocks))
	for i, raw := range rawBlocks {
		block, err := statemachine.DeserializeBlock(raw)
		if err != nil || block.Height != from+uint64(i) {
			return nil, false
		}
		proposal, err := consensus.DeserializeProposal(block.ProposalBytes)
		if err != nil || !consensus.VerifyProposalForChain(proposal, chainID) {
			return nil, false
		}
		if _, ok := validatorSet[proposal.ProposerPK]; !ok || proposal.Height != block.Height || proposal.ProposerPK != block.ProposerPubKey || proposal.PrevStateRoot != block.PrevStateRoot {
			return nil, false
		}
		proposalTxs, txErr := tx.SerializeBatch(proposal.Txs)
		blockTxs, blockErr := tx.SerializeBatch(block.Txs)
		if txErr != nil || blockErr != nil || !bytes.Equal(proposalTxs, blockTxs) {
			return nil, false
		}
		if len(proposal.PrevAttestations.Votes) > 0 {
			if err := consensus.VerifyAttestationForChain(&proposal.PrevAttestations, validators, stakes, chainID); err != nil {
				return nil, false
			}
		}
		decoded = append(decoded, block)
	}
	return decoded, true
}

func syncedBlockRangeKey(blocks [][]byte) [32]byte {
	hash := sha256.New()
	for _, block := range blocks {
		var size [8]byte
		binary.BigEndian.PutUint64(size[:], uint64(len(block)))
		hash.Write(size[:])
		hash.Write(block)
	}
	return sha256.Sum256(hash.Sum(nil))
}

func requiresGRPCTLS(listenAddr string) bool {
	host, _, err := net.SplitHostPort(listenAddr)
	if err != nil {
		return true
	}
	if strings.EqualFold(host, "localhost") {
		return false
	}
	ip := net.ParseIP(host)
	return ip == nil || !ip.IsLoopback()
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
	metrics   *nodeMetrics
}

func (n *nodeRuntime) BroadcastTx(t *tx.Tx) error {
	if n.p2p == nil {
		return fmt.Errorf("transaction submission unavailable in grpc-only mode")
	}
	return n.p2p.BroadcastTx(t)
}

func (n *nodeRuntime) BroadcastBatch(txs []*tx.Tx) error {
	if n.p2p == nil {
		return fmt.Errorf("transaction submission unavailable in grpc-only mode")
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
