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

func TestRunNodeInitCreatesFirstRunFiles(t *testing.T) {
	dir := t.TempDir()
	var out strings.Builder
	err := runNodeInit([]string{
		"--data-dir", dir,
		"--chain-id", "frg-test-1",
		"--p2p-listen", "/ip4/0.0.0.0/tcp/17777",
		"--grpc-listen", "127.0.0.1:15051",
		"--grpc-tls-cert-file", "/var/lib/frg/tls/server.crt",
		"--grpc-tls-key-file", "/var/lib/frg/tls/server.key",
		"--grpc-tls-client-ca-file", "/var/lib/frg/tls/client-ca.crt",
		"--metrics-listen", "127.0.0.1:19090",
		"--peers", "/ip4/127.0.0.1/tcp/17778/p2p/peer-a,/dns4/bootstrap.example/tcp/17777/p2p/peer-b",
		"--bootstrap-genesis",
	}, &out)
	if err != nil {
		t.Fatal(err)
	}

	for _, name := range []string{"frg.key", "config.toml", ".env", "genesis.json"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Fatalf("%s was not created: %v", name, err)
		}
	}

	config, err := os.ReadFile(filepath.Join(dir, "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	configText := string(config)
	for _, want := range []string{
		`chain_id = "frg-test-1"`,
		`listen = "/ip4/0.0.0.0/tcp/17777"`,
		`tls_cert_file = "/var/lib/frg/tls/server.crt"`,
		`"/dns4/bootstrap.example/tcp/17777/p2p/peer-b"`,
	} {
		if !strings.Contains(configText, want) {
			t.Fatalf("config.toml missing %q:\n%s", want, configText)
		}
	}
	loaded, err := loadConfig(filepath.Join(dir, "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if loaded.ChainID != "frg-test-1" {
		t.Fatalf("loaded chain ID = %q, want frg-test-1", loaded.ChainID)
	}
	if loaded.GRPC.TLSCertFile != "/var/lib/frg/tls/server.crt" {
		t.Fatalf("loaded TLS cert = %q", loaded.GRPC.TLSCertFile)
	}

	envData, err := os.ReadFile(filepath.Join(dir, ".env"))
	if err != nil {
		t.Fatal(err)
	}
	envText := string(envData)
	for _, want := range []string{"FRG_VALIDATOR_PUBKEY=", "FRG_PEER_ID=", "FRG_ADVERTISED_MULTIADDR=", "FRG_GRPC_TLS_CERT_FILE=/var/lib/frg/tls/server.crt"} {
		if !strings.Contains(envText, want) {
			t.Fatalf(".env missing %q:\n%s", want, envText)
		}
	}
	if !strings.Contains(out.String(), "validator_pubkey=") || !strings.Contains(out.String(), "peer_id=") {
		t.Fatalf("init output missing identity details:\n%s", out.String())
	}
}

func TestRunNodeInitDoesNotCreateGenesisByDefault(t *testing.T) {
	dir := t.TempDir()
	var out strings.Builder
	if err := runNodeInit([]string{"--data-dir", dir}, &out); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "genesis.json")); !os.IsNotExist(err) {
		t.Fatalf("genesis should not be created by default: %v", err)
	}
}

func containsString(s, substr string) bool {
	return strings.Contains(s, substr)
}
