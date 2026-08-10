package main

import (
	"strings"
	"testing"
)

func TestContractCallDataRejectsAmbiguousSelectorAndRawCalldata(t *testing.T) {
	if _, err := contractCallData(contractCallRequest{Function: "sett", CallDataHex: "0102"}); err == nil || !strings.Contains(err.Error(), "not both") {
		t.Fatalf("expected mutually exclusive contract call fields error, got %v", err)
	}
	got, err := contractCallData(contractCallRequest{Function: "sett"})
	if err != nil || string(got) != "sett" {
		t.Fatalf("function calldata = %x, err=%v", got, err)
	}
	got, err = contractCallData(contractCallRequest{CallDataHex: "736574740102"})
	if err != nil || string(got) != "sett\x01\x02" {
		t.Fatalf("raw calldata = %x, err=%v", got, err)
	}
}
