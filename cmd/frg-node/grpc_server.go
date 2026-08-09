package main

import (
	"context"
	"fmt"
	"sync"
	"time"

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
	ListValidators() (*frgpb.ValidatorList, error)
	ListMempool() (*frgpb.MempoolList, error)
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
