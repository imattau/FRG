package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"math/big"
	"strings"
	"testing"
)

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
	err = p.allowSpend("transfer", strings.Repeat("0", 64), big.NewInt(1), false, false)
	if err == nil || !strings.Contains(err.Error(), "allow_submit is false") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestPolicyRequiresExplicitPositiveLimits(t *testing.T) {
	p, err := loadPolicy("", true)
	if err != nil {
		t.Fatal(err)
	}
	err = p.allowSpend("transfer", strings.Repeat("0", 64), big.NewInt(1), false, false)
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
	p.maxTransfer, err = parsePolicyAmount(p.MaxTransfer)
	if err != nil {
		t.Fatal(err)
	}
	p.dailyLimit, err = parsePolicyAmount(p.DailyLimit)
	if err != nil {
		t.Fatal(err)
	}
	p.allowedRecipients = map[string]struct{}{}
	p.allowedContracts = map[string]struct{}{}
	p.spentToday = big.NewInt(0)

	target := strings.Repeat("a", 64)
	if err := p.allowSpend("transfer", target, big.NewInt(10), false, false); err != nil {
		t.Fatal(err)
	}
	p.recordSpend(big.NewInt(10))
	err = p.allowSpend("transfer", target, big.NewInt(6), false, false)
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
