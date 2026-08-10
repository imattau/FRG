package main

import (
	"context"
	"fmt"
	"math/big"
	"sort"
	"sync"
	"time"

	"github.com/imattau/frg/core/node"
	"github.com/imattau/frg/core/statemachine"
	"github.com/imattau/frg/core/tree"
	"github.com/imattau/frg/core/tx"
	frgpb "github.com/imattau/frg/proto"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"
)

const (
	submitWindow          = time.Second
	maxSubmitTxPerPeer    = 2048
	maxSubmitReqsPerPeer  = 128
	maxTrackedSubmitPeers = 4096
)

type submitWindowState struct {
	started  time.Time
	txs      int
	requests int
}

type submitLimiter struct {
	mu    sync.Mutex
	peers map[string]submitWindowState
	now   func() time.Time
}

func newSubmitLimiter() *submitLimiter {
	return &submitLimiter{peers: make(map[string]submitWindowState), now: time.Now}
}

func (l *submitLimiter) allow(ctx context.Context, txCount int) error {
	if l == nil {
		return nil
	}
	if txCount < 1 || txCount > maxSubmitTxPerPeer {
		return status.Error(codes.ResourceExhausted, "submission rate limit exceeded")
	}
	key := "unknown"
	if p, ok := peer.FromContext(ctx); ok && p.Addr != nil {
		key = p.Addr.String()
	}
	now := l.now()
	l.mu.Lock()
	defer l.mu.Unlock()
	state, ok := l.peers[key]
	if !ok {
		if len(l.peers) >= maxTrackedSubmitPeers {
			for peerKey, peerState := range l.peers {
				if now.Sub(peerState.started) >= submitWindow {
					delete(l.peers, peerKey)
				}
			}
			if len(l.peers) >= maxTrackedSubmitPeers {
				return status.Error(codes.ResourceExhausted, "too many submission clients")
			}
		}
		state = submitWindowState{started: now}
	}
	if now.Sub(state.started) >= submitWindow {
		state = submitWindowState{started: now}
	}
	if state.requests >= maxSubmitReqsPerPeer || state.txs+txCount > maxSubmitTxPerPeer {
		l.peers[key] = state
		return status.Error(codes.ResourceExhausted, "submission rate limit exceeded")
	}
	state.requests++
	state.txs += txCount
	l.peers[key] = state
	return nil
}

type nodeAPI interface {
	BroadcastTx(*tx.Tx) error
	BroadcastBatch([]*tx.Tx) error
	SubscribeBlockHeaders() <-chan []byte
}

type nodeStatusAPI interface {
	Status() (*frgpb.StatusResponse, error)
}

type nodeQueryAPI interface {
	GetAccount(pubkey [32]byte) (*frgpb.AccountResponse, error)
	GetContractState(contractAddr [32]byte, key []byte) (*frgpb.ContractStateResponse, error)
	ListValidators() (*frgpb.ValidatorList, error)
	ListMempool() (*frgpb.MempoolList, error)
	GetBlockTelemetry(height uint64) (*frgpb.BlockTelemetryResponse, error)
}

type nodeGRPCServer struct {
	frgpb.UnimplementedFRGServer
	node       nodeAPI
	stat       nodeStatusAPI
	query      nodeQueryAPI
	chainID    string
	limiter    *submitLimiter
	metrics    *nodeMetrics
	authorizer *rpcAuthorizer
}

func (s *nodeGRPCServer) SubmitTx(ctx context.Context, in *frgpb.RawBytes) (*frgpb.SubmitResponse, error) {
	if s.metrics != nil {
		s.metrics.rpcRequests.Add(1)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := s.authorizer.authorize(ctx, roleSubmitter); err != nil {
		if s.metrics != nil {
			s.metrics.rpcRejected.Add(1)
		}
		return nil, err
	}
	if in == nil || len(in.Data) > tx.MaxSerializedBytes {
		return &frgpb.SubmitResponse{Ok: false, Error: "transaction payload exceeds size limit"}, nil
	}
	if err := s.limiter.allow(ctx, 1); err != nil {
		if s.metrics != nil {
			s.metrics.rpcRejected.Add(1)
		}
		return nil, err
	}

	t, err := tx.Deserialize(in.Data)
	if err != nil {
		return &frgpb.SubmitResponse{Ok: false, Error: err.Error()}, nil
	}
	if err := t.VerifySigsForChain(s.chainID); err != nil {
		return &frgpb.SubmitResponse{Ok: false, Error: err.Error()}, nil
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := s.node.BroadcastTx(t); err != nil {
		return &frgpb.SubmitResponse{Ok: false, Error: err.Error()}, nil
	}
	if s.metrics != nil {
		s.metrics.txAccepted.Add(1)
	}
	return &frgpb.SubmitResponse{Ok: true}, nil
}

func (s *nodeGRPCServer) SubmitBatch(ctx context.Context, in *frgpb.RawBytesArray) (*frgpb.SubmitResponse, error) {
	if s.metrics != nil {
		s.metrics.rpcRequests.Add(1)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := s.authorizer.authorize(ctx, roleSubmitter); err != nil {
		if s.metrics != nil {
			s.metrics.rpcRejected.Add(1)
		}
		return nil, err
	}
	if in == nil || len(in.Data) == 0 || len(in.Data) > 1024 {
		return &frgpb.SubmitResponse{Ok: false, Error: "batch size is invalid"}, nil
	}
	if err := s.limiter.allow(ctx, len(in.Data)); err != nil {
		if s.metrics != nil {
			s.metrics.rpcRejected.Add(1)
		}
		return nil, err
	}

	batch := make([]*tx.Tx, 0, len(in.Data))
	for _, raw := range in.Data {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if len(raw) > tx.MaxSerializedBytes {
			return &frgpb.SubmitResponse{Ok: false, Error: "transaction payload exceeds size limit"}, nil
		}
		t, err := tx.Deserialize(raw)
		if err != nil {
			return &frgpb.SubmitResponse{Ok: false, Error: err.Error()}, nil
		}
		if err := t.VerifySigsForChain(s.chainID); err != nil {
			return &frgpb.SubmitResponse{Ok: false, Error: err.Error()}, nil
		}
		batch = append(batch, t)
	}
	if err := s.node.BroadcastBatch(batch); err != nil {
		return &frgpb.SubmitResponse{Ok: false, Error: err.Error()}, nil
	}
	if s.metrics != nil {
		s.metrics.batchAccepted.Add(1)
	}
	return &frgpb.SubmitResponse{Ok: true}, nil
}

func (s *nodeGRPCServer) SubscribeBlocks(_ *frgpb.Empty, stream frgpb.FRG_SubscribeBlocksServer) error {
	if err := s.authorizer.authorize(stream.Context(), roleObserver); err != nil {
		return err
	}
	blocks := s.node.SubscribeBlockHeaders()
	for {
		select {
		case <-stream.Context().Done():
			return nil
		case data, ok := <-blocks:
			if !ok {
				return nil
			}
			payload := append([]byte(nil), data...)
			if err := stream.Send(&frgpb.RawBytes{Data: payload}); err != nil {
				return fmt.Errorf("send block header: %w", err)
			}
		}
	}
}

func (s *nodeGRPCServer) GetStatus(ctx context.Context, _ *frgpb.Empty) (*frgpb.StatusResponse, error) {
	if err := s.authorizer.authorize(ctx, roleObserver); err != nil {
		return nil, err
	}
	if s.stat == nil {
		return &frgpb.StatusResponse{}, nil
	}
	return s.stat.Status()
}

func (s *nodeGRPCServer) GetAccount(ctx context.Context, req *frgpb.AccountRequest) (*frgpb.AccountResponse, error) {
	if err := s.authorizer.authorize(ctx, roleObserver); err != nil {
		return nil, err
	}
	if s.query == nil {
		return nil, fmt.Errorf("query backend not available")
	}
	if req == nil || len(req.Pubkey) != 32 {
		return nil, fmt.Errorf("pubkey must be 32 bytes")
	}
	var pubkey [32]byte
	copy(pubkey[:], req.Pubkey)
	return s.query.GetAccount(pubkey)
}

func (s *nodeGRPCServer) GetContractState(ctx context.Context, req *frgpb.ContractStateRequest) (*frgpb.ContractStateResponse, error) {
	if err := s.authorizer.authorize(ctx, roleObserver); err != nil {
		return nil, err
	}
	if s.query == nil {
		return nil, fmt.Errorf("query backend not available")
	}
	if req == nil || len(req.ContractAddress) != 32 {
		return nil, fmt.Errorf("contract_address must be 32 bytes")
	}
	if len(req.Key) > 32 {
		return nil, fmt.Errorf("contract state key must be at most 32 bytes")
	}
	var contractAddr [32]byte
	copy(contractAddr[:], req.ContractAddress)
	return s.query.GetContractState(contractAddr, req.Key)
}

func (s *nodeGRPCServer) ListValidators(ctx context.Context, _ *frgpb.Empty) (*frgpb.ValidatorList, error) {
	if err := s.authorizer.authorize(ctx, roleObserver); err != nil {
		return nil, err
	}
	if s.query == nil {
		return nil, fmt.Errorf("query backend not available")
	}
	return s.query.ListValidators()
}

func (s *nodeGRPCServer) ListMempool(ctx context.Context, _ *frgpb.Empty) (*frgpb.MempoolList, error) {
	if err := s.authorizer.authorize(ctx, roleObserver); err != nil {
		return nil, err
	}
	if s.query == nil {
		return nil, fmt.Errorf("query backend not available")
	}
	return s.query.ListMempool()
}

func (s *nodeGRPCServer) GetBlockTelemetry(ctx context.Context, req *frgpb.BlockTelemetryRequest) (*frgpb.BlockTelemetryResponse, error) {
	if err := s.authorizer.authorize(ctx, roleObserver); err != nil {
		return nil, err
	}
	if s.query == nil {
		return nil, fmt.Errorf("query backend not available")
	}
	height := uint64(0)
	if req != nil {
		height = req.Height
	}
	return s.query.GetBlockTelemetry(height)
}

func blockTelemetry(block *statemachine.Block) (*frgpb.BlockTelemetryResponse, error) {
	if block == nil {
		return nil, fmt.Errorf("block not found")
	}
	tr, err := tree.BuildTree(block.Txs, nil)
	if err != nil {
		return nil, err
	}
	total := big.NewInt(0)
	sumSquares := big.NewInt(0)
	txTypes := make(map[tx.TxType]uint64)
	for _, t := range block.Txs {
		txTypes[t.Type]++
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

	out := &frgpb.BlockTelemetryResponse{
		Height:                block.Height,
		StateRoot:             block.StateRoot[:],
		ProposerPubkey:        block.ProposerPubKey[:],
		TxCount:               uint64(len(block.Txs)),
		TotalValue:            total.String(),
		MeanValue:             mean.String(),
		Variance:              variance.String(),
		TxTypes:               formatTxTypeCounts(txTypes),
		Levels:                formatRGLevels(tr),
		ContractStateIncluded: false,
	}
	for _, t := range block.Txs {
		if t.Type == tx.TxTypeContractDeploy || t.Type == tx.TxTypeContractCall {
			out.Warning = "historical contract-state RG nodes are not persisted yet; telemetry reconstructs transaction structure only"
			break
		}
	}
	return out, nil
}

func formatTxTypeCounts(counts map[tx.TxType]uint64) []*frgpb.TxTypeCount {
	types := make([]int, 0, len(counts))
	for typ := range counts {
		types = append(types, int(typ))
	}
	sort.Ints(types)
	out := make([]*frgpb.TxTypeCount, 0, len(types))
	for _, typ := range types {
		t := tx.TxType(typ)
		out = append(out, &frgpb.TxTypeCount{Type: uint32(t), Name: txTypeName(t), Count: counts[t]})
	}
	return out
}

func formatRGLevels(tr *tree.Tree) []*frgpb.RGLevelTelemetry {
	levels := make([]*frgpb.RGLevelTelemetry, 0, tr.LayerCount())
	for level := 0; level < tr.LayerCount(); level++ {
		layer := tr.Layer(level)
		scale := uint32(0)
		if len(layer) > 0 {
			scale = layer[0].Scale
		}
		_, contractNodes, density := tr.ContractDensity(level)
		levels = append(levels, &frgpb.RGLevelTelemetry{
			Level:             uint32(level),
			Scale:             scale,
			NodeCount:         uint64(len(layer)),
			SignatureCounts:   formatSignatureCounts(tr.SignatureHistogram(level)),
			ContractDensity:   density,
			VolatileRegions:   intSliceToUint64(tr.VolatilityRegions(level)),
			StagnantRegions:   intSliceToUint64(tr.StagnantRegions(level)),
			TotalVolume:       levelVolume(layer).String(),
			Variance:          levelVariance(layer).String(),
			ContractTxCount:   uint64(contractNodes),
			ContractNodeCount: uint64(contractNodes),
		})
	}
	return levels
}

func formatSignatureCounts(counts map[node.Signature]int) []*frgpb.SignatureCount {
	sigs := make([]int, 0, len(counts))
	for sig := range counts {
		sigs = append(sigs, int(sig))
	}
	sort.Ints(sigs)
	out := make([]*frgpb.SignatureCount, 0, len(sigs))
	for _, sig := range sigs {
		s := node.Signature(sig)
		out = append(out, &frgpb.SignatureCount{Signature: uint32(s), Name: signatureName(s), Count: uint64(counts[s])})
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
