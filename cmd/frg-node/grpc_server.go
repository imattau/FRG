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

type nodeGRPCServer struct {
	frgpb.UnimplementedFRGServer
	node nodeAPI
	stat nodeStatusAPI
}

func (s *nodeGRPCServer) SubmitTx(ctx context.Context, in *frgpb.RawBytes) (*frgpb.SubmitResponse, error) {
	_ = ctx

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

	batch := make([]*tx.Tx, 0, len(in.Data))
	for _, raw := range in.Data {
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
