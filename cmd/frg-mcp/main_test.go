package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"math/big"
	"path/filepath"
	"strings"
	"testing"

	"github.com/imattau/frg/core/denom"
)

func q(frg int64) *big.Int {
	return new(big.Int).Mul(big.NewInt(frg), denom.QuantaPerFRG)
}

func TestMCPMessageRoundTrip(t *testing.T) {
	var out bytes.Buffer
	resp := rpcResponse{
		JSONRPC: "2.0",
		ID:      json.RawMessage(`1`),
		Result:  map[string]string{"ok": "true"},
	}
	if err := writeMessage(&out, resp); err != nil {
		t.Fatal(err)
	}
	got, err := readMessage(bufio.NewReader(&out))
	if err != nil {
		t.Fatal(err)
	}
	var decoded rpcResponse
	if err := json.Unmarshal(got, &decoded); err != nil {
		t.Fatal(err)
	}
	if string(decoded.ID) != "1" {
		t.Fatalf("id = %s", decoded.ID)
	}
}

func TestPolicyDeniesSubmitByDefault(t *testing.T) {
	p, err := loadPolicy("", false)
	if err != nil {
		t.Fatal(err)
	}
	err = p.allowSpend("transfer", strings.Repeat("0", 64), q(1), false, false)
	if err == nil || !strings.Contains(err.Error(), "allow_submit is false") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestLoadMCPKeypairCreatesMissingKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent.key")
	kp, created, err := loadMCPKeypair(path, true)
	if err != nil {
		t.Fatal(err)
	}
	if !created {
		t.Fatal("expected key to be created")
	}
	if kp == nil {
		t.Fatal("nil keypair")
	}
	loaded, created, err := loadMCPKeypair(path, true)
	if err != nil {
		t.Fatal(err)
	}
	if created {
		t.Fatal("existing key should not be recreated")
	}
	if loaded.PublicKey != kp.PublicKey {
		t.Fatalf("loaded pubkey %x != created %x", loaded.PublicKey, kp.PublicKey)
	}
}

func TestLoadMCPKeypairMissingWithoutCreateFails(t *testing.T) {
	_, created, err := loadMCPKeypair(filepath.Join(t.TempDir(), "agent.key"), false)
	if err == nil {
		t.Fatal("expected missing key error")
	}
	if created {
		t.Fatal("created should be false on error")
	}
}

func TestPolicyRequiresExplicitPositiveLimits(t *testing.T) {
	p, err := loadPolicy("", true)
	if err != nil {
		t.Fatal(err)
	}
	err = p.allowSpend("transfer", strings.Repeat("0", 64), q(1), false, false)
	if err == nil || !strings.Contains(err.Error(), "max_transfer is zero") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestPolicyAllowsBoundedTransferAndTracksDailyLimit(t *testing.T) {
	p := &policy{
		AllowSubmit: true,
		MaxTransfer: "10",
		DailyLimit:  "15",
	}
	var err error
	p.maxTransfer, err = parsePolicyAmount(p.MaxTransfer, "", "max_transfer")
	if err != nil {
		t.Fatal(err)
	}
	p.dailyLimit, err = parsePolicyAmount(p.DailyLimit, "", "daily_limit")
	if err != nil {
		t.Fatal(err)
	}
	p.allowedRecipients = map[string]struct{}{}
	p.allowedContracts = map[string]struct{}{}
	p.spentToday = big.NewInt(0)

	target := strings.Repeat("a", 64)
	if err := p.allowSpend("transfer", target, q(10), false, false); err != nil {
		t.Fatal(err)
	}
	p.recordSpend(q(10))
	err = p.allowSpend("transfer", target, q(6), false, false)
	if err == nil || !strings.Contains(err.Error(), "daily_limit") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestDecodeKeyRejectsAmbiguousInput(t *testing.T) {
	_, err := decodeKey("count", "636f756e74")
	if err == nil || !strings.Contains(err.Error(), "not both") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestWorkActionSelector(t *testing.T) {
	got, err := workActionSelector("approve")
	if err != nil {
		t.Fatal(err)
	}
	if got != "aprv" {
		t.Fatalf("selector = %q", got)
	}
	if _, err := workActionSelector("unknown"); err == nil {
		t.Fatal("expected unknown action error")
	}
}

func TestWorkActionCalldataAppendsPayload(t *testing.T) {
	got, err := workActionCalldata("subm", "0102ff")
	if err != nil {
		t.Fatal(err)
	}
	if string(got[:4]) != "subm" || string(got[4:]) != string([]byte{1, 2, 0xff}) {
		t.Fatalf("calldata = %x", got)
	}
	got, err = workActionCalldata("post", "")
	if err != nil || string(got) != "post" {
		t.Fatalf("empty payload calldata = %x, err=%v", got, err)
	}
	if _, err := workActionCalldata("post", "not-hex"); err == nil {
		t.Fatal("expected invalid payload hex error")
	}
}

func TestContractCallDataRejectsAmbiguousSelectorAndRawCalldata(t *testing.T) {
	if _, err := contractCallData("sett", "0102"); err == nil || !strings.Contains(err.Error(), "not both") {
		t.Fatalf("expected mutually exclusive contract call fields error, got %v", err)
	}
	got, err := contractCallData("sett", "")
	if err != nil || string(got) != "sett" {
		t.Fatalf("function calldata = %x, err=%v", got, err)
	}
	got, err = contractCallData("", "736574740102")
	if err != nil || string(got) != "sett\x01\x02" {
		t.Fatalf("raw calldata = %x, err=%v", got, err)
	}
}

func TestBuildWorkTermsStableHash(t *testing.T) {
	terms := workTerms{
		Description:        "render report",
		Reward:             "25",
		Deadline:           "120",
		Verifier:           strings.Repeat("a", 64),
		ResultRequirements: "sha256 artifact hash",
	}
	first, err := buildWorkTerms(terms)
	if err != nil {
		t.Fatal(err)
	}
	second, err := buildWorkTerms(terms)
	if err != nil {
		t.Fatal(err)
	}
	if first.TermsHash == "" || first.TermsHash != second.TermsHash {
		t.Fatalf("unstable terms hash: %q vs %q", first.TermsHash, second.TermsHash)
	}
	if !strings.Contains(first.Canonical, "render report") {
		t.Fatalf("canonical terms missing description: %s", first.Canonical)
	}
}

func TestWorkSchemaContainsSelectors(t *testing.T) {
	schema := workSchema()
	selectors, ok := schema["selectors"].(map[string]string)
	if !ok {
		t.Fatalf("selectors type = %T", schema["selectors"])
	}
	if selectors["accept"] != "acpt" || selectors["claim"] != "clai" {
		t.Fatalf("unexpected selectors: %#v", selectors)
	}
}
