package statemachine

import (
	"encoding/json"
	"fmt"
	"math/big"
	"sort"

	"github.com/imattau/frg/core/node"
	"github.com/imattau/frg/core/tree"
	"github.com/imattau/frg/core/tx"
	bolt "go.etcd.io/bbolt"
)

var blockTelemetryBucket = []byte("block_telemetry")

const maxStoredBlockTelemetryBytes = 2 << 20

type BlockTelemetry struct {
	Height                uint64             `json:"height"`
	StateRoot             [32]byte           `json:"state_root"`
	ProposerPubKey        [32]byte           `json:"proposer_pubkey"`
	TxCount               uint64             `json:"tx_count"`
	TotalValue            string             `json:"total_value"`
	MeanValue             string             `json:"mean_value"`
	Variance              string             `json:"variance"`
	TxTypes               []TxTypeTelemetry  `json:"tx_types"`
	Levels                []RGLevelTelemetry `json:"levels"`
	ContractStateIncluded bool               `json:"contract_state_included"`
	Warning               string             `json:"warning,omitempty"`
}

type TxTypeTelemetry struct {
	Type  uint32 `json:"type"`
	Name  string `json:"name"`
	Count uint64 `json:"count"`
}

type SignatureTelemetry struct {
	Signature uint32 `json:"signature"`
	Name      string `json:"name"`
	Count     uint64 `json:"count"`
}

type RGLevelTelemetry struct {
	Level             uint32               `json:"level"`
	Scale             uint32               `json:"scale"`
	NodeCount         uint64               `json:"node_count"`
	SignatureCounts   []SignatureTelemetry `json:"signature_counts"`
	ContractDensity   float64              `json:"contract_density"`
	VolatileRegions   []uint64             `json:"volatile_regions"`
	StagnantRegions   []uint64             `json:"stagnant_regions"`
	TotalVolume       string               `json:"total_volume"`
	Variance          string               `json:"variance"`
	ContractTxCount   uint64               `json:"contract_tx_count"`
	ContractNodeCount uint64               `json:"contract_node_count"`
}

func BuildBlockTelemetry(block *Block, contractNodes []*node.RGNode, exactContractState bool) (*BlockTelemetry, error) {
	if block == nil {
		return nil, fmt.Errorf("block not found")
	}
	tr, err := tree.BuildTree(block.Txs, contractNodes)
	if err != nil {
		return nil, err
	}
	return buildBlockTelemetryFromTree(block, tr, exactContractState), nil
}

func buildBlockTelemetryFromTree(block *Block, tr *tree.Tree, exactContractState bool) *BlockTelemetry {
	total := big.NewInt(0)
	sumSquares := big.NewInt(0)
	txTypes := make(map[tx.TxType]uint64)
	contractTxCount := uint64(0)
	for _, t := range block.Txs {
		txTypes[t.Type]++
		if t.Type == tx.TxTypeContractDeploy || t.Type == tx.TxTypeContractCall {
			contractTxCount++
		}
		if t.Value == nil {
			continue
		}
		total.Add(total, t.Value)
		sq := new(big.Int).Mul(t.Value, t.Value)
		sumSquares.Add(sumSquares, sq)
	}

	mean := big.NewInt(0)
	variance := big.NewInt(0)
	if len(block.Txs) > 0 {
		count := big.NewInt(int64(len(block.Txs)))
		mean.Div(new(big.Int).Set(total), count)
		avgSquares := new(big.Int).Div(sumSquares, count)
		meanSquare := new(big.Int).Mul(mean, mean)
		variance.Sub(avgSquares, meanSquare)
		if variance.Sign() < 0 {
			variance.SetInt64(0)
		}
	}

	out := &BlockTelemetry{
		Height:                block.Height,
		StateRoot:             block.StateRoot,
		ProposerPubKey:        block.ProposerPubKey,
		TxCount:               uint64(len(block.Txs)),
		TotalValue:            total.String(),
		MeanValue:             mean.String(),
		Variance:              variance.String(),
		TxTypes:               formatTxTypeTelemetry(txTypes),
		Levels:                formatRGTelemetryLevels(tr, contractTxCount),
		ContractStateIncluded: exactContractState,
	}
	if !exactContractState {
		for _, t := range block.Txs {
			if t.Type == tx.TxTypeContractDeploy || t.Type == tx.TxTypeContractCall {
				out.Warning = "historical contract-state RG nodes are not persisted for this block; telemetry reconstructs transaction structure only"
				break
			}
		}
	}
	return out
}

func (sm *StateMachine) BlockTelemetryAt(height uint64) (*BlockTelemetry, error) {
	var telemetry *BlockTelemetry
	err := sm.db.View(func(btx *bolt.Tx) error {
		data := btx.Bucket(blockTelemetryBucket).Get(blockKey(height))
		if data == nil {
			return nil
		}
		var decoded BlockTelemetry
		if err := json.Unmarshal(data, &decoded); err != nil {
			return err
		}
		telemetry = &decoded
		return nil
	})
	return telemetry, err
}

func putBlockTelemetryTx(btx *bolt.Tx, telemetry *BlockTelemetry) error {
	data, err := json.Marshal(telemetry)
	if err != nil {
		return err
	}
	if len(data) > maxStoredBlockTelemetryBytes {
		return fmt.Errorf("serialized block telemetry exceeds %d bytes", maxStoredBlockTelemetryBytes)
	}
	return btx.Bucket(blockTelemetryBucket).Put(blockKey(telemetry.Height), data)
}

func formatTxTypeTelemetry(counts map[tx.TxType]uint64) []TxTypeTelemetry {
	types := make([]int, 0, len(counts))
	for typ := range counts {
		types = append(types, int(typ))
	}
	sort.Ints(types)
	out := make([]TxTypeTelemetry, 0, len(types))
	for _, typ := range types {
		t := tx.TxType(typ)
		out = append(out, TxTypeTelemetry{Type: uint32(t), Name: txTypeName(t), Count: counts[t]})
	}
	return out
}

func formatRGTelemetryLevels(tr *tree.Tree, contractTxCount uint64) []RGLevelTelemetry {
	levels := make([]RGLevelTelemetry, 0, tr.LayerCount())
	for level := 0; level < tr.LayerCount(); level++ {
		layer := tr.Layer(level)
		scale := uint32(0)
		if len(layer) > 0 {
			scale = layer[0].Scale
		}
		_, contractNodes, density := tr.ContractDensity(level)
		levels = append(levels, RGLevelTelemetry{
			Level:             uint32(level),
			Scale:             scale,
			NodeCount:         uint64(len(layer)),
			SignatureCounts:   formatSignatureTelemetry(tr.SignatureHistogram(level)),
			ContractDensity:   density,
			VolatileRegions:   intSliceToUint64(tr.VolatilityRegions(level)),
			StagnantRegions:   intSliceToUint64(tr.StagnantRegions(level)),
			TotalVolume:       levelVolume(layer).String(),
			Variance:          levelVariance(layer).String(),
			ContractTxCount:   contractTxCountForLevel(level, contractTxCount),
			ContractNodeCount: uint64(contractNodes),
		})
	}
	return levels
}

func contractTxCountForLevel(level int, contractTxCount uint64) uint64 {
	if level != 0 {
		return 0
	}
	return contractTxCount
}

func formatSignatureTelemetry(counts map[node.Signature]int) []SignatureTelemetry {
	sigs := make([]int, 0, len(counts))
	for sig := range counts {
		sigs = append(sigs, int(sig))
	}
	sort.Ints(sigs)
	out := make([]SignatureTelemetry, 0, len(sigs))
	for _, sig := range sigs {
		s := node.Signature(sig)
		out = append(out, SignatureTelemetry{Signature: uint32(s), Name: signatureName(s), Count: uint64(counts[s])})
	}
	return out
}

func levelVolume(layer []*node.RGNode) *big.Int {
	total := big.NewInt(0)
	for _, n := range layer {
		total.Add(total, node.BytesToUint256(n.Volume))
	}
	return total
}

func levelVariance(layer []*node.RGNode) *big.Int {
	total := big.NewInt(0)
	for _, n := range layer {
		total.Add(total, node.BytesToUint256(n.Variance))
	}
	return total
}

func intSliceToUint64(in []int) []uint64 {
	out := make([]uint64, len(in))
	for i, v := range in {
		out[i] = uint64(v)
	}
	return out
}

func txTypeName(t tx.TxType) string {
	switch t {
	case tx.TxTypeTransfer:
		return "transfer"
	case tx.TxTypeMissEvidence:
		return "miss_evidence"
	case tx.TxTypeContractDeploy:
		return "contract_deploy"
	case tx.TxTypeContractCall:
		return "contract_call"
	case tx.TxTypeBond:
		return "bond"
	default:
		return fmt.Sprintf("unknown_%d", t)
	}
}

func signatureName(sig node.Signature) string {
	switch sig {
	case node.SigAtomic:
		return "atomic"
	case node.SigNullPad:
		return "null_pad"
	case node.SigStagnantState:
		return "stagnant_state"
	case node.SigLaminarFlow:
		return "laminar_flow"
	case node.SigVolatileShock:
		return "volatile_shock"
	case node.SigContract:
		return "contract"
	default:
		return fmt.Sprintf("unknown_%d", sig)
	}
}
