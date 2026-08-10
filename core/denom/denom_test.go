package denom

import (
	"math/big"
	"testing"
)

func TestParseFRG(t *testing.T) {
	tests := []struct {
		raw  string
		want string
	}{
		{"0", "0"},
		{"1", "1000000000000000000"},
		{"1.5", "1500000000000000000"},
		{"0.000000000000000001", "1"},
		{".25", "250000000000000000"},
		{"10.000000000000000000", "10000000000000000000"},
	}
	for _, tt := range tests {
		got, err := ParseFRG(tt.raw)
		if err != nil {
			t.Fatalf("ParseFRG(%q): %v", tt.raw, err)
		}
		if got.String() != tt.want {
			t.Fatalf("ParseFRG(%q) = %s, want %s", tt.raw, got, tt.want)
		}
	}
}

func TestParseFRGRejectsInvalidValues(t *testing.T) {
	for _, raw := range []string{"", "-", "-1", "1.0000000000000000001", "1.2.3", "abc", "1_000"} {
		if _, err := ParseFRG(raw); err == nil {
			t.Fatalf("ParseFRG(%q) succeeded", raw)
		}
	}
}

func TestFormatFRG(t *testing.T) {
	tests := []struct {
		raw  string
		want string
	}{
		{"0", "0"},
		{"1", "0.000000000000000001"},
		{"1000000000000000000", "1"},
		{"1500000000000000000", "1.5"},
		{"1000000000000000001", "1.000000000000000001"},
	}
	for _, tt := range tests {
		q, _ := new(big.Int).SetString(tt.raw, 10)
		if got := FormatFRG(q); got != tt.want {
			t.Fatalf("FormatFRG(%s) = %q, want %q", tt.raw, got, tt.want)
		}
	}
}
