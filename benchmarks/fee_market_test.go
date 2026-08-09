package benchmarks

import (
	"math/big"
	"testing"

	"github.com/imattau/frg/core/gas"
)

func BenchmarkFeeMarket(b *testing.B) {
	const blocks = 100000

	b.Run("ExtremeUtilization", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			baseFee := new(big.Int).Set(gas.MinBaseFee)
			for h := uint64(0); h < blocks; h++ {
				ga := gas.TargetGasPerBlock * 500 / 100
				baseFee = gas.BaseFee(baseFee, ga)
			}
			_ = baseFee
		}
	})

	b.Run("ZeroDemand", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			baseFee := new(big.Int).Set(gas.MinBaseFee)
			for h := uint64(0); h < blocks; h++ {
				baseFee = gas.BaseFee(baseFee, 0)
			}
			_ = baseFee
		}
	})

	b.Run("Oscillating", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			baseFee := new(big.Int).Set(gas.MinBaseFee)
			for h := uint64(0); h < blocks; h++ {
				var pct uint64
				if h%2 == 0 {
					pct = 0
				} else {
					pct = 1000
				}
				ga := gas.TargetGasPerBlock * pct / 100
				baseFee = gas.BaseFee(baseFee, ga)
			}
			_ = baseFee
		}
	})

	b.Run("RoundingEdges", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			baseFee := new(big.Int).Set(gas.MinBaseFee)
			cycle := []uint64{1, gas.TargetGasPerBlock - 1, gas.TargetGasPerBlock + 1}
			for h := uint64(0); h < blocks; h++ {
				ga := cycle[h%uint64(len(cycle))]
				baseFee = gas.BaseFee(baseFee, ga)
			}
			_ = baseFee
		}
	})

	b.Run("LowDemand", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			baseFee := new(big.Int).Set(gas.MinBaseFee)
			for h := uint64(0); h < blocks; h++ {
				ga := gas.TargetGasPerBlock * 20 / 100
				baseFee = gas.BaseFee(baseFee, ga)
			}
			_ = baseFee
		}
	})

	b.Run("NormalDemand", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			baseFee := new(big.Int).Set(gas.MinBaseFee)
			for h := uint64(0); h < blocks; h++ {
				ga := gas.TargetGasPerBlock * 90 / 100
				baseFee = gas.BaseFee(baseFee, ga)
			}
			_ = baseFee
		}
	})

	b.Run("HighDemand", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			baseFee := new(big.Int).Set(gas.MinBaseFee)
			for h := uint64(0); h < blocks; h++ {
				ga := gas.TargetGasPerBlock * 175 / 100
				baseFee = gas.BaseFee(baseFee, ga)
			}
			_ = baseFee
		}
	})

	b.Run("BurstyDemand", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			baseFee := new(big.Int).Set(gas.MinBaseFee)
			demands := []uint64{20, 200, 30, 180, 10, 150, 5, 220, 15, 190}
			for h := uint64(0); h < blocks; h++ {
				pct := demands[h%uint64(len(demands))]
				ga := gas.TargetGasPerBlock * pct / 100
				baseFee = gas.BaseFee(baseFee, ga)
			}
			_ = baseFee
		}
	})
}

func BenchmarkFeeMarketStability(b *testing.B) {
	const blocks = 100000

	b.Run("OscillationAmplitude", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			baseFee := new(big.Int).Set(gas.MinBaseFee)
			var minFee, maxFee *big.Int
			demands := []uint64{20, 200, 30, 180, 10, 150, 5, 220, 15, 190}
			for h := uint64(0); h < blocks; h++ {
				pct := demands[h%uint64(len(demands))]
				ga := gas.TargetGasPerBlock * pct / 100
				baseFee = gas.BaseFee(baseFee, ga)
				if minFee == nil || baseFee.Cmp(minFee) < 0 {
					minFee = new(big.Int).Set(baseFee)
				}
				if maxFee == nil || baseFee.Cmp(maxFee) > 0 {
					maxFee = new(big.Int).Set(baseFee)
				}
			}
			_ = minFee
			_ = maxFee
		}
	})

	b.Run("ExtremeOscillation", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			baseFee := new(big.Int).Set(gas.MinBaseFee)
			for h := uint64(0); h < blocks; h++ {
				var pct uint64
				if h%2 == 0 {
					pct = 0
				} else {
					pct = 1000
				}
				ga := gas.TargetGasPerBlock * pct / 100
				baseFee = gas.BaseFee(baseFee, ga)
			}
			_ = baseFee
		}
	})
}

func TestFeeMarketEdgeCases(t *testing.T) {
	bf := new(big.Int).Set(gas.MinBaseFee)

	bf = gas.BaseFee(bf, 0)
	if bf.Cmp(gas.MinBaseFee) < 0 {
		t.Fatal("base fee fell below minimum after zero demand")
	}

	for h := 0; h < 10000; h++ {
		bf = gas.BaseFee(bf, 0)
	}
	if bf.Cmp(gas.MinBaseFee) < 0 {
		t.Fatal("base fee fell below minimum after sustained zero demand")
	}

	bf.SetUint64(1000000)
	bf = gas.BaseFee(bf, gas.TargetGasPerBlock*500/100)
	if bf.Sign() <= 0 {
		t.Fatal("base fee became non-positive under extreme utilization")
	}

	bf.Set(gas.MinBaseFee)
	bf = gas.BaseFee(bf, gas.TargetGasPerBlock)
	if bf.Cmp(gas.MinBaseFee) != 0 {
		t.Fatal("base fee changed at exactly target utilization")
	}
}
