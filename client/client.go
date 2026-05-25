package client

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/imattau/frg/core/keys"
	"github.com/imattau/frg/core/tree"
	"github.com/imattau/frg/core/tx"
	"google.golang.org/grpc"
	"google.golang.org/grpc/connectivity"
)

type Client struct {
	kp     *keys.Keypair
	q      *queue
	t      *transport
	ctx    context.Context
	cancel context.CancelFunc
	mu     sync.Mutex
}

func New(addr string, kp *keys.Keypair, queuePath string, opts ...grpc.DialOption) (*Client, error) {
	q, err := openQueue(queuePath, kp)
	if err != nil {
		return nil, fmt.Errorf("open queue: %w", err)
	}

	tr, err := dialTransport(addr, opts...)
	if err != nil {
		_ = q.close()
		return nil, err
	}

	ctx, cancel := context.WithCancel(context.Background())
	c := &Client{
		kp:     kp,
		q:      q,
		t:      tr,
		ctx:    ctx,
		cancel: cancel,
	}
	go c.reconnectLoop()
	return c, nil
}

func (c *Client) SubmitTx(ctx context.Context, t *tx.Tx) error {
	t.SenderPubKey = c.kp.PublicKey

	msg, err := t.UnsignedHash()
	if err != nil {
		return err
	}
	sig, err := c.kp.Sign(msg[:])
	if err != nil {
		return err
	}
	t.SenderSig = sig

	txBytes, err := t.Serialize()
	if err != nil {
		return err
	}

	if c.t.isReady() {
		if err := c.t.submitTx(ctx, txBytes); err == nil {
			return nil
		} else if !errors.Is(err, errNodeRejected) {
			if c.q == nil {
				return err
			}
			return c.q.Enqueue(txBytes)
		} else {
			return err
		}
	}

	if c.q == nil {
		return fmt.Errorf("queue is not initialized")
	}
	return c.q.Enqueue(txBytes)
}

func (c *Client) Subscribe(ctx context.Context) (<-chan *tree.Block, error) {
	raw, err := c.t.subscribe(ctx)
	if err != nil {
		return nil, err
	}

	ch := make(chan *tree.Block, 16)
	go func() {
		defer close(ch)
		for {
			select {
			case <-ctx.Done():
				return
			case _, ok := <-raw:
				if !ok {
					return
				}
				ch <- &tree.Block{}
			}
		}
	}()
	return ch, nil
}

func (c *Client) Close() error {
	if c.cancel != nil {
		c.cancel()
	}
	c.flush(context.Background())

	var firstErr error
	if err := c.t.close(); err != nil && firstErr == nil {
		firstErr = err
	}
	if err := c.q.close(); err != nil && firstErr == nil {
		firstErr = err
	}
	return firstErr
}

func (c *Client) reconnectLoop() {
	for {
		state := c.t.conn.GetState()
		if state == connectivity.Ready {
			c.flush(c.ctx)
		}
		if !c.t.conn.WaitForStateChange(c.ctx, state) {
			return
		}
		if c.ctx.Err() != nil {
			return
		}
	}
}

func (c *Client) flush(ctx context.Context) {
	if c.q == nil || c.t == nil {
		return
	}
	dbKeys, txBytesSlice, err := c.q.Drain()
	if err != nil || len(txBytesSlice) == 0 {
		return
	}
	if err := c.t.submitBatch(ctx, txBytesSlice); err != nil {
		return
	}
	_ = c.q.DeleteKeys(dbKeys)
}
