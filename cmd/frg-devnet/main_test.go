package main

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
)

func TestConfigTemplateWritesTopLevelChainID(t *testing.T) {
	cfg := fmt.Sprintf(configTemplate, "frg-devnet-test", 17777, "", 50051)
	var parsed map[string]any
	if err := toml.Unmarshal([]byte(cfg), &parsed); err != nil {
		t.Fatal(err)
	}
	if got := parsed["chain_id"]; got != "frg-devnet-test" {
		t.Fatalf("top-level chain_id = %v", got)
	}
	consensus, ok := parsed["consensus"].(map[string]any)
	if !ok {
		t.Fatalf("missing consensus table: %#v", parsed["consensus"])
	}
	if _, ok := consensus["chain_id"]; ok {
		t.Fatalf("chain_id must not be scoped under [consensus]: %s", cfg)
	}
	if !strings.HasPrefix(cfg, "chain_id = ") {
		t.Fatalf("chain_id should be emitted before any table: %s", cfg)
	}
}

func TestWriteDockerComposeUsesPublishedImagesAndDataDir(t *testing.T) {
	outputDir := t.TempDir()
	nodes := []devNode{{index: 0, p2pPort: 17777, grpcPort: 50051}}
	if err := writeDockerCompose(outputDir, nodes, true, "ghcr.io/imattau/frg-node:latest", "ghcr.io/imattau/frg-faucet:latest"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(outputDir + "/docker-compose.yml")
	if err != nil {
		t.Fatal(err)
	}
	compose := string(data)
	if strings.Contains(compose, "\t") {
		t.Fatal("generated Compose file contains a tab")
	}
	for _, want := range []string{
		"image: ghcr.io/imattau/frg-node:latest",
		"image: ghcr.io/imattau/frg-faucet:latest",
		"FRG_DATA_DIR: /data",
		"command: frg-faucet --key faucet.key",
	} {
		if !strings.Contains(compose, want) {
			t.Fatalf("generated Compose file missing %q:\n%s", want, compose)
		}
	}
}
