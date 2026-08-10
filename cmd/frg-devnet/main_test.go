package main

import (
	"fmt"
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
