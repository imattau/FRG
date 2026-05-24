# FRG Core — Design Document

**Date:** 2026-05-25
**Scope:** Core library only (Validator, Client, Testnet are separate sub-projects)
**Spec:** CLAUDE.md — Fractal RG Ledger Protocol v1.0.0 (frozen)

---

## 1. Package Structure

```
frg/
├── go.mod                          # module: github.com/imattau/frg
├── core/
│   ├── errors/
│   │   └── errors.go               # RGError type, ERR_001–ERR_010 codes
│   ├── hash/
│   │   └── hash.go                 # H(x), domain prefix constants, UINT256_MAX
│   ├── tx/
│   │   └── tx.go                   # Tx type, serialisation, txid
│   ├── node/
│   │   └── node.go                 # RGNode type, serialisation, RecomputeSig, NullNode, EmptyBlockRoot
│   └── tree/
│       └── tree.go                 # Block type, BuildRoot, K-ary chunking, coarse-graining
└── core/testdata/
    ├── vectors_test.go             # spec-derived test vectors
    └── property_test.go            # rapid property tests
```

Dependencies flow one way: `hash` and `errors` are leaves. `tx` and `node` depend on `hash` and `errors`. `tree` depends on all four.

---

## 2. Key Types

### errors/errors.go

```go
type ErrorCode string

const (
    ErrArithmeticOverflow          ErrorCode = "ERR_001"
    ErrInvalidChildArity           ErrorCode = "ERR_002"
    ErrScaleDomainFault            ErrorCode = "ERR_003"
    ErrHashBoundaryMismatch        ErrorCode = "ERR_004"
    ErrMaskOutOfBounds             ErrorCode = "ERR_005"
    ErrPaddingSubstitutionFraud    ErrorCode = "ERR_006"
    ErrSignatureMisrepresentation  ErrorCode = "ERR_007"
    ErrNamespaceEscapeFault        ErrorCode = "ERR_008"
    ErrCanonicalEncodingDistortion ErrorCode = "ERR_009"
    ErrDosSizeExceeded             ErrorCode = "ERR_010"
)

type RGError struct {
    Code ErrorCode
    Msg  string
}

func (e *RGError) Error() string
```

### hash/hash.go

```go
// Domain prefixes — exact bytes from spec
const (
    DomainTx         = "\x54\x58\x5F\x56\x31\x00"
    DomainRGNode     = "\x52\x47\x5F\x4E\x4F\x44\x45\x5F\x56\x31\x00"
    DomainNullPad    = "\x4E\x55\x4C\x4C\x5F\x50\x41\x44\x5F\x56\x31\x00"
    DomainEmptyBlock = "\x45\x4D\x50\x54\x59\x5F\x42\x4C\x4F\x43\x4B\x5F\x56\x31\x00"
)

// UINT256_MAX = 2^256 - 1
var UINT256_MAX *big.Int

// Hash performs a single-pass SHA2-256 over data.
func Hash(data []byte) [32]byte

// ValidScale returns true if Λ = K^n for n ∈ {0..8}.
func ValidScale(lambda uint32) bool
```

### tx/tx.go

```go
// Value is stored pre-scaled: raw = userValue × 10^18 (SCALE).
// Callers must multiply by SCALE before constructing a Tx.
type Tx struct {
    Sender   string   // NFC-normalised UTF-8
    Receiver string   // NFC-normalised UTF-8
    Value    *big.Int // uint256, pre-scaled by 10^18
    Nonce    uint64
}

// Serialize returns Tx_Bytes per spec:
//   TX_V1\x00 | uint16(len(Sender)) | Sender | uint16(len(Receiver)) | Receiver | uint256(Value) | uint64(Nonce)
// Returns ERR_009 if encoding is malformed, ERR_010 if total > 70000 bytes.
func (t *Tx) Serialize() ([]byte, error)

// ID returns txid = H(Tx_Bytes).
func (t *Tx) ID() ([32]byte, error)
```

### node/node.go

```go
type Signature uint8

const (
    SigAtomic        Signature = 1
    SigNullPad       Signature = 2
    SigStagnantState Signature = 3
    SigLaminarFlow   Signature = 4
    SigVolatileShock Signature = 5
)

type RGNode struct {
    Scale    uint32
    Volume   *big.Int   // serialised — sum of active leaf values
    Variance *big.Int   // serialised — population variance (no Bessel correction)
    Sig      Signature  // serialised — recomputed independently, declared value untrusted
    Children [][32]byte // 1 child for atomic/empty, 4 for macro

    // Internal propagation fields — not serialised
    sumValues  *big.Int // sum of active leaf values
    sumSquares *big.Int // sum of squares of active leaf values
    count      uint64   // number of active leaves
}

// Serialize returns Node_Bytes per spec.
// Returns ERR_002 if child_count invalid, ERR_003 if Scale invalid.
func (n *RGNode) Serialize() ([]byte, error)

// Root returns node_root = H(Node_Bytes).
func (n *RGNode) Root() ([32]byte, error)

// RecomputeSig derives the correct Signature from Volume and Variance.
// Returns ERR_007 if recomputed value != n.Sig.
func (n *RGNode) RecomputeSig() (Signature, error)

// NullNode returns the canonical NULL_Λ node at the given scale.
// Returns ERR_003 if scale is invalid.
func NullNode(scale uint32) (*RGNode, error)

// EmptyBlockRoot returns the normative empty-block state root constant.
func EmptyBlockRoot() [32]byte
```

### tree/tree.go

```go
type Block struct {
    Height uint64     // out-of-band routing parameter — not serialised, used for ERR_008
    Txs    []*tx.Tx
}

// BuildRoot executes the full bottom-up pipeline:
//   - Zero txs → EmptyBlockRoot()
//   - ERR_010 if len(Txs) > 65536
//   - Atomic ingestion → K-ary chunking → coarse-graining until single root
// Returns ERR_001–ERR_010 as appropriate; on any error discards all state.
func (b *Block) BuildRoot() ([32]byte, error)
```

---

## 3. Data Flow

### Non-empty block

```
[]*tx.Tx
  │  ERR_010: len > 65536
  ▼
tx.Serialize() → H(Tx_Bytes) = txid                  [tx]
  ▼
atomic RGNode{Λ=1, sig=ATOMIC, children=[txid]}       [node]
  ▼  repeat until single root:
K-ary chunk (size 4)                                  [tree]
  ├─ trailing short chunk → append NullNode(Λ), set padding_mask bits
  │    ERR_005: padding_mask ≥ 16
  │    ERR_006: masked child ≠ NullNode(Λ).Root()
  └─ coarse_grain(chunk) → parent RGNode{Λ×4}
       volume  = sum of active child sumValues
       variance = (sumSquares/count) - (sumValues/count)²  [floor division]
       RecomputeSig() → ERR_007 if mismatch
       ERR_001: any result > UINT256_MAX or < 0
  ▼
single RGNode → Root() = state_root
```

### Zero-tx block

```
Block{Txs: []} → EmptyBlockRoot() → state_root
```

---

## 4. Variance Computation

Variance is computed via **sum propagation** — the most efficient approach for a bottom-up tree:

Each `RGNode` carries three internal fields during construction:
- `sumValues` — sum of active leaf values
- `sumSquares` — sum of squares of active leaf values
- `count` — number of active leaves

At coarse-graining: parent aggregates children's sums, then:

```
variance = floor(sumSquares / count) - floor(sumValues / count)²
```

These fields are not serialised into `Node_Bytes`. Only the final `Variance` value appears in the wire format.

---

## 5. Error Handling

All functions return `(*Result, error)` where `error` is `*RGError`. On any non-nil error, the entire block is discarded — no partial state is retained.

| Code | Raised in | Condition |
|---|---|---|
| ERR_001 | `node`, `tree` | Any arithmetic result > UINT256_MAX or < 0 |
| ERR_002 | `node`, `tree` | child_count ≠ 1 (atomic/empty) or ≠ 4 (macro) |
| ERR_003 | `node` | Scale ∉ {1,4,16,64,256,1024,4096,16384,65536} |
| ERR_004 | `hash` | Hash output ≠ 32 bytes |
| ERR_005 | `tree` | padding_mask ≥ 16 |
| ERR_006 | `tree` | Masked child root ≠ NullNode(Λ).Root() |
| ERR_007 | `node` | RecomputeSig() ≠ declared Sig byte |
| ERR_008 | `tree` | Node injected from different block height |
| ERR_009 | `tx` | Serialised layout ≠ spec, bad NFC, wrong length prefix |
| ERR_010 | `tree` | len(Txs) > 65536 or Tx_Bytes > 70000 |

---

## 6. Testing

**Track 1 — Spec test vectors** (`core/testdata/vectors_test.go`):

| Test | What it pins |
|---|---|
| `TestTxSerialize` | known input → exact Tx_Bytes hex |
| `TestTxID` | known Tx_Bytes → exact txid hex |
| `TestNullNode` | NullNode(Λ) at each valid scale → exact root hex |
| `TestEmptyBlockRoot` | zero-tx block → normative constant from spec |
| `TestAtomicNode` | single tx → atomic RGNode → exact root hex |
| `TestCoarseGrain` | 4 known atomic nodes → exact parent root hex |
| `TestPaddingMask` | 3-tx chunk → correct NULL_Λ padding + mask bits |
| `TestSignatureRecompute` | each Signature variant → correct recomputation |
| `TestFullBlock` | 5 known txs → known state_root hex |

**Track 2 — Property tests** (`core/testdata/property_test.go`, using `pgregory.net/rapid`):

| Property | Invariant |
|---|---|
| `PropSingleRootAlways` | any 1–65536 txs → exactly one 32-byte root |
| `PropOrderMatters` | same txs, different order → different root |
| `PropNoPaddingFraud` | padding positions always equal canonical NullNode(Λ) |
| `PropNoOverflow` | random large values never produce ERR_001 silently |
| `PropScaleDomain` | coarse-graining always produces valid Λ at each layer |
| `PropEmptyBlockStable` | zero-tx block always returns same root |
| `PropVarianceNonNegative` | variance never negative regardless of input |

---

## 7. Out of Scope (Core)

- Block proposer, peer discovery, P2P transport
- Persistent storage
- Validator logic (ERR_008 is enforced in Core; full validation is a separate layer)
- Client, Testnet
