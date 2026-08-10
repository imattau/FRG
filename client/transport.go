package client

import (
	"context"
	"errors"
	"fmt"
	"io"

	frgpb "github.com/imattau/frg/proto"
	"google.golang.org/grpc"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/credentials/insecure"
)

var errNodeRejected = errors.New("node rejected tx")

type transport struct {
	conn   *grpc.ClientConn
	client frgpb.FRGClient
}

func dialTransport(addr string, opts ...grpc.DialOption) (*transport, error) {
	dialOpts := make([]grpc.DialOption, 0, len(opts)+2)
	dialOpts = append(dialOpts, grpc.WithTransportCredentials(insecure.NewCredentials()))
	dialOpts = append(dialOpts, opts...)

	conn, err := grpc.DialContext(context.Background(), addr, dialOpts...)
	if err != nil {
		return nil, fmt.Errorf("dial %s: %w", addr, err)
	}
	return &transport{conn: conn, client: frgpb.NewFRGClient(conn)}, nil
}

func (t *transport) submitTx(ctx context.Context, txBytes []byte) error {
	resp, err := t.client.SubmitTx(ctx, &frgpb.RawBytes{Data: txBytes})
	if err != nil {
		return err
	}
	if !resp.Ok {
		return fmt.Errorf("%w: %s", errNodeRejected, resp.Error)
	}
	return nil
}

func (t *transport) submitBatch(ctx context.Context, txBytesSlice [][]byte) error {
	resp, err := t.client.SubmitBatch(ctx, &frgpb.RawBytesArray{Data: txBytesSlice})
	if err != nil {
		return err
	}
	if !resp.Ok {
		return fmt.Errorf("%w: %s", errNodeRejected, resp.Error)
	}
	return nil
}

func (t *transport) subscribe(ctx context.Context) (<-chan []byte, error) {
	stream, err := t.client.SubscribeBlocks(ctx, &frgpb.Empty{})
	if err != nil {
		return nil, err
	}

	ch := make(chan []byte, 16)
	go func() {
		defer close(ch)
		for {
			msg, err := stream.Recv()
			if err == io.EOF || ctx.Err() != nil {
				return
			}
			if err != nil {
				return
			}
			ch <- msg.Data
		}
	}()
	return ch, nil
}

func (t *transport) isReady() bool {
	return t.conn.GetState() == connectivity.Ready
}

func (t *transport) close() error {
	return t.conn.Close()
}
