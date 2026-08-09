package main

import (
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/imattau/frg/core/keys"
)

func TestLoadConfigUsesBuiltInDefaultsWhenMissing(t *testing.T) {
	dir := t.TempDir()
	cfg, err := loadConfig(filepath.Join(dir, "config.toml"))
	if err != nil {
		t.Fatal(err)
	}

	if cfg.Node.KeypairPath != defaultKeypairPath {
		t.Fatalf("unexpected keypair path: %s", cfg.Node.KeypairPath)
	}
	if cfg.Node.DBPath != defaultDBPath {
		t.Fatalf("unexpected db path: %s", cfg.Node.DBPath)
	}
	if cfg.Node.GenesisPath != defaultGenesisPath {
		t.Fatalf("unexpected genesis path: %s", cfg.Node.GenesisPath)
	}
	if cfg.P2P.Listen != defaultListenAddr {
		t.Fatalf("unexpected listen addr: %s", cfg.P2P.Listen)
	}
	if cfg.Consensus.ProposeTimeoutMS != defaultTimeoutMS {
		t.Fatalf("unexpected timeout: %d", cfg.Consensus.ProposeTimeoutMS)
	}
}

func TestValidateConfigRejectsUnsafeProductionSettings(t *testing.T) {
	cfg := defaultConfig()
	cfg.ChainID = "bad chain"
	if err := validateConfig(cfg); err == nil {
		t.Fatal("invalid chain ID was accepted")
	}
	cfg = defaultConfig()
	cfg.GRPC.Listen = "0.0.0.0:50051"
	if err := validateConfig(cfg); err == nil {
		t.Fatal("remote gRPC without mTLS was accepted")
	}
	cfg = defaultConfig()
	cfg.Metrics.Listen = "0.0.0.0:9090"
	if err := validateConfig(cfg); err == nil {
		t.Fatal("non-loopback metrics listener was accepted")
	}
}

func TestEnsureGenesisCreatesBootstrapFile(t *testing.T) {
	dir := t.TempDir()
	genesisPath := filepath.Join(dir, "genesis.json")
	kp := keys.NewKeypairFromSeed([32]byte{1})

	if err := ensureGenesis(genesisPath, defaultChainID, kp); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(genesisPath)
	if err != nil {
		t.Fatal(err)
	}

	pubHex := hex.EncodeToString(kp.PublicKey[:])
	if !containsString(string(data), pubHex) {
		t.Fatalf("bootstrap genesis did not include pubkey %s: %s", pubHex, string(data))
	}
}

func TestLoadOrGenerateKeypairCreatesFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nested", "frg.key")

	kp, err := loadOrGenerateKeypair(path)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) != 32 {
		t.Fatalf("expected 32-byte seed file, got %d bytes", len(data))
	}

	var seed [32]byte
	copy(seed[:], data)
	loaded := keys.NewKeypairFromSeed(seed)
	if loaded == nil || kp == nil {
		t.Fatal("unexpected nil keypair")
	}
	if loaded.PublicKey != kp.PublicKey {
		t.Fatal("loaded keypair does not match written seed")
	}
}

func containsString(s, substr string) bool {
	return strings.Contains(s, substr)
}
