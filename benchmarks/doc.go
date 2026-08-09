// Package benchmarks contains FRG protocol benchmarks organised into three phases:
//
// Phase 1: RG tree construction, incremental updates, contract-state scaling, fuel/gas calibration.
// Phase 2: Fee-market dynamics, consensus scaling, state-root convergence.
// Phase 3: Economic signal preservation, clustered activity, FRG vs Merkle/SMT comparison.
//
// New benchmarks (v2):
// - BenchmarkIncrementalMutation: true incremental leaf mutation on retained trees (no full rebuilds)
// - BenchmarkContractDensity: sparse-wide vs dense-narrow contract state patterns
// - BenchmarkMemoryBreakdown (non-benchmark TestMemoryBreakdown): per-component memory accounting
// - BenchmarkFeeMarket/ExtremeUtilization,ZeroDemand,Oscillating,RoundingEdges: extended fee scenarios
// - TestFuelCostModel (non-benchmark): fuel vs wall-clock correlation across workloads
// - TestProofGeometry (non-benchmark): structural proof comparison (depth, siblings, bytes)
// - BenchmarkClusteredActivity/*/SignatureWalk: post-construction layer traversal separated from build
//
// Proof verification now compares against expected root (no dead-code elimination risk).
//
// Standalone economic analysis:
//
//	go run ./cmd/frg-analysis/ -out results/economic/ -variants 100 -tx 65536
//
// Run all benchmarks:
//
//	go test ./benchmarks/... -bench=. -benchmem -benchtime=3s
//
// Run a specific phase:
//
//	go test ./benchmarks/... -bench='Benchmark(RGTree|Incremental|Contract|Fuel)' -benchmem
//	go test ./benchmarks/... -bench='Benchmark(FeeMarket|Consensus|Convergence)' -benchmem -timeout=30m
//	go test ./benchmarks/... -bench='Benchmark(Economic|Clustered|Comparison)' -benchmem -benchtime=5s
package benchmarks
