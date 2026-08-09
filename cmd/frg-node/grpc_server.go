package main

import (
	"context"
	"fmt"

	"github.com/imattau/frg/core/tx"
	frgpb "github.com/imattau/frg/proto"
)

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
	node  nodeAPI
	stat  nodeStatusAPI
	query nodeQueryAPI
}

func (s *nodeGRPCServer) SubmitTx(ctx context.Context, in *frgpb.RawBytes) (*frgpb.SubmitResponse, error) {
	_ = ctx
	if in == nil || len(in.Data) > tx.MaxSerializedBytes {
		return &frgpb.SubmitResponse{Ok: false, Error: "transaction payload exceeds size limit"}, nil
	}

	t, err := tx.Deserialize(in.Data)
	if err != nil {
		return &frgpb.SubmitResponse{Ok: false, Error: err.Error()}, nil
	}
	if err := t.VerifySigs(); err != nil {
		return &frgpb.SubmitResponse{Ok: false, Error: err.Error()}, nil
	}
	if err := s.node.BroadcastTx(t); err != nil {
		return &frgpb.SubmitResponse{Ok: false, Error: err.Error()}, nil
	}
	return &frgpb.SubmitResponse{Ok: true}, nil
}

func (s *nodeGRPCServer) SubmitBatch(ctx context.Context, in *frgpb.RawBytesArray) (*frgpb.SubmitResponse, error) {
	_ = ctx
	if in == nil || len(in.Data) == 0 || len(in.Data) > 1024 {
		return &frgpb.SubmitResponse{Ok: false, Error: "batch size is invalid"}, nil
	}

	batch := make([]*tx.Tx, 0, len(in.Data))
	for _, raw := range in.Data {
		if len(raw) > tx.MaxSerializedBytes {
			return &frgpb.SubmitResponse{Ok: false, Error: "transaction payload exceeds size limit"}, nil
		}
		t, err := tx.Deserialize(raw)
		if err != nil {
			return &frgpb.SubmitResponse{Ok: false, Error: err.Error()}, nil
		}
		if err := t.VerifySigs(); err != nil {
			return &frgpb.SubmitResponse{Ok: false, Error: err.Error()}, nil
		}
		batch = append(batch, t)
	}
	if err := s.node.BroadcastBatch(batch); err != nil {
		return &frgpb.SubmitResponse{Ok: false, Error: err.Error()}, nil
	}
	return &frgpb.SubmitResponse{Ok: true}, nil
}

func (s *nodeGRPCServer) SubscribeBlocks(_ *frgpb.Empty, stream frgpb.FRG_SubscribeBlocksServer) error {
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

func (s *nodeGRPCServer) GetStatus(context.Context, *frgpb.Empty) (*frgpb.StatusResponse, error) {
	if s.stat == nil {
		return &frgpb.StatusResponse{}, nil
	}
	return s.stat.Status()
}

func (s *nodeGRPCServer) GetAccount(_ context.Context, req *frgpb.AccountRequest) (*frgpb.AccountResponse, error) {
	if s.query == nil {
		return nil, fmt.Errorf("query backend not available")
	}
	if len(req.Pubkey) != 32 {
		return nil, fmt.Errorf("pubkey must be 32 bytes")
	}
	var pubkey [32]byte
	copy(pubkey[:], req.Pubkey)
	return s.query.GetAccount(pubkey)
}

func (s *nodeGRPCServer) ListValidators(context.Context, *frgpb.Empty) (*frgpb.ValidatorList, error) {
	if s.query == nil {
		return nil, fmt.Errorf("query backend not available")
	}
	return s.query.ListValidators()
}

func (s *nodeGRPCServer) ListMempool(context.Context, *frgpb.Empty) (*frgpb.MempoolList, error) {
	if s.query == nil {
		return nil, fmt.Errorf("query backend not available")
	}
	return s.query.ListMempool()
}
