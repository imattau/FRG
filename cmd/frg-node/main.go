package main

import (
    "context"
    "flag"
    "fmt"
    "log"
    "os"
    "os/signal"
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
    bolt "go.etcd.io/bbolt"
)

type Config struct {
    Node      NodeConfig      `toml:"node"`
    P2P       P2PConfig       `toml:"p2p"`
    Consensus ConsensusConfig `toml:"consensus"`
}

type NodeConfig struct {
    KeypairPath string `toml:"keypair_path"`
    DBPath      string `toml:"db_path"`
    GenesisPath string `toml:"genesis_path"`
}

type P2PConfig struct {
    Listen string   `toml:"listen"`
    Peers  []string `toml:"peers"`
}

type ConsensusConfig struct {
    ProposeTimeoutMS   int `toml:"propose_timeout_ms"`
    PrevoteTimeoutMS   int `toml:"prevote_timeout_ms"`
    PrecommitTimeoutMS int `toml:"precommit_timeout_ms"`
}

func main() {
    configPath := flag.String("config", "config.toml", "path to config.toml")
    flag.Parse()

    var cfg Config
    if _, err := toml.DecodeFile(*configPath, &cfg); err != nil {
        log.Fatalf("Load config: %v", err)
    }

    kp, err := loadOrGenerateKeypair(cfg.Node.KeypairPath)
    if err != nil {
        log.Fatalf("Load keypair: %v", err)
    }
    log.Printf("Node started with PubKey: %x", kp.PublicKey)

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

    p2pNode, err := p2p.New(ctx, kp, p2p.Config{
        ListenAddr:     cfg.P2P.Listen,
        BootstrapPeers: cfg.P2P.Peers,
    })
    if err != nil {
        log.Fatalf("Init P2P: %v", err)
    }
    defer p2pNode.Close()

    bl := blockloop.New(kp, p2pNode)
    if err := bl.Start(ctx); err != nil {
        log.Fatalf("Start blockloop: %v", err)
    }
    defer bl.Stop()

    timeoutCfg := consensus.TimeoutConfig{
        Propose:   time.Duration(cfg.Consensus.ProposeTimeoutMS) * time.Millisecond,
        Prevote:   time.Duration(cfg.Consensus.PrevoteTimeoutMS) * time.Millisecond,
        Precommit: time.Duration(cfg.Consensus.PrecommitTimeoutMS) * time.Millisecond,
    }

    engine := consensus.New(kp, s, sm, p2pNode, bl, timeoutCfg)

    go func() {
        if err := engine.Start(ctx); err != nil {
            log.Printf("Consensus engine failed: %v", err)
            cancel()
        }
    }()

    sigCh := make(chan os.Signal, 1)
    signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
    <-sigCh

    log.Println("Shutting down...")
    engine.Stop()
}

func loadOrGenerateKeypair(path string) (*keys.Keypair, error) {
    if _, err := os.Stat(path); os.IsNotExist(err) {
        kp, err := keys.GenerateKeypair()
        if err != nil {
            return nil, err
        }
        if err := os.WriteFile(path, kp.PrivateKey[:], 0600); err != nil {
            return nil, err
        }
        return kp, nil
    }

    data, err := os.ReadFile(path)
    if err != nil {
        return nil, err
    }
    if len(data) != 32 {
        return nil, fmt.Errorf("invalid keypair file length: %d", len(data))
    }
    var seed [32]byte
    copy(seed[:], data)
    return keys.NewKeypairFromSeed(seed), nil
}
