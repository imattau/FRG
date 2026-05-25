package p2p

import (
	"context"
	"fmt"

	libp2p "github.com/libp2p/go-libp2p"
	dht "github.com/libp2p/go-libp2p-kad-dht"
	pubsub "github.com/libp2p/go-libp2p-pubsub"
	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/multiformats/go-multiaddr"

	"github.com/imattau/frg/core/keys"
	"github.com/imattau/frg/core/tx"
)

const (
	topicTx    = "frg/tx/v1"
	topicBatch = "frg/batch/v1"
	topicBlock = "frg/block/v1"
)

// Config holds node configuration.
type Config struct {
	ListenAddr     string   // e.g. "/ip4/0.0.0.0/tcp/7777"
	BootstrapPeers []string // multiaddrs of bootstrap nodes
	EnableMDNS     bool     // true for testnet/local only
}

// Node is the FRG P2P node.
type Node struct {
	host       host.Host
	dht        *dht.IpfsDHT
	ps         *pubsub.PubSub
	txTopic    *pubsub.Topic
	batchTopic *pubsub.Topic
	blockTopic *pubsub.Topic
	txSub      *pubsub.Subscription
	batchSub   *pubsub.Subscription
	blockSub   *pubsub.Subscription
	txCh       chan *tx.Tx
	blockCh    chan []byte
	cancel     context.CancelFunc
}

// New creates and starts a P2P node using the given Ed25519 keypair.
func New(ctx context.Context, kp *keys.Keypair, cfg Config) (*Node, error) {
	privKey, err := crypto.UnmarshalEd25519PrivateKey(kp.PrivateKey[:])
	if err != nil {
		return nil, fmt.Errorf("p2p key: %w", err)
	}

	ma, err := multiaddr.NewMultiaddr(cfg.ListenAddr)
	if err != nil {
		return nil, fmt.Errorf("listen addr: %w", err)
	}

	h, err := libp2p.New(
		libp2p.ListenAddrs(ma),
		libp2p.Identity(privKey),
		libp2p.DefaultTransports,
		libp2p.DefaultSecurity,
		libp2p.DefaultMuxers,
	)
	if err != nil {
		return nil, fmt.Errorf("libp2p host: %w", err)
	}

	innerCtx, cancel := context.WithCancel(ctx)

	ps, err := pubsub.NewGossipSub(innerCtx, h,
		pubsub.WithFloodPublish(true),
	)
	if err != nil {
		cancel()
		h.Close()
		return nil, fmt.Errorf("gossipsub: %w", err)
	}

	txTopic, err := ps.Join(topicTx)
	if err != nil {
		cancel()
		h.Close()
		return nil, fmt.Errorf("join tx topic: %w", err)
	}
	batchTopic, err := ps.Join(topicBatch)
	if err != nil {
		cancel()
		h.Close()
		return nil, fmt.Errorf("join batch topic: %w", err)
	}
	blockTopic, err := ps.Join(topicBlock)
	if err != nil {
		cancel()
		h.Close()
		return nil, fmt.Errorf("join block topic: %w", err)
	}

	txSub, err := txTopic.Subscribe()
	if err != nil {
		cancel()
		h.Close()
		return nil, fmt.Errorf("subscribe tx: %w", err)
	}
	batchSub, err := batchTopic.Subscribe()
	if err != nil {
		cancel()
		h.Close()
		return nil, fmt.Errorf("subscribe batch: %w", err)
	}
	blockSub, err := blockTopic.Subscribe()
	if err != nil {
		cancel()
		h.Close()
		return nil, fmt.Errorf("subscribe block: %w", err)
	}

	n := &Node{
		host:       h,
		ps:         ps,
		txTopic:    txTopic,
		batchTopic: batchTopic,
		blockTopic: blockTopic,
		txSub:      txSub,
		batchSub:   batchSub,
		blockSub:   blockSub,
		txCh:       make(chan *tx.Tx, 1024),
		blockCh:    make(chan []byte, 16),
		cancel:     cancel,
	}

	go n.readTxs(innerCtx)
	go n.readBatches(innerCtx)
	go n.readBlocks(innerCtx)

	if len(cfg.BootstrapPeers) > 0 {
		kad, err := dht.New(innerCtx, h, dht.ProtocolPrefix("/frg/kad/v1"))
		if err != nil {
			cancel()
			h.Close()
			return nil, fmt.Errorf("dht: %w", err)
		}
		n.dht = kad
		for _, addr := range cfg.BootstrapPeers {
			ma, err := multiaddr.NewMultiaddr(addr)
			if err != nil {
				continue
			}
			pi, err := peer.AddrInfoFromP2pAddr(ma)
			if err != nil {
				continue
			}
			_ = h.Connect(innerCtx, *pi)
		}
		_ = kad.Bootstrap(innerCtx)
	}

	return n, nil
}

// Close shuts down the node gracefully.
func (n *Node) Close() error {
	n.cancel()
	n.txSub.Cancel()
	n.batchSub.Cancel()
	n.blockSub.Cancel()
	if n.dht != nil {
		n.dht.Close()
	}
	return n.host.Close()
}

// BroadcastTx gossips a transaction to all peers on frg/tx/v1.
func (n *Node) BroadcastTx(t *tx.Tx) error {
	b, err := t.Serialize()
	if err != nil {
		return err
	}
	return n.txTopic.Publish(context.Background(), b)
}

// BroadcastBatch gossips multiple transactions at once on frg/batch/v1.
func (n *Node) BroadcastBatch(txs []*tx.Tx) error {
	b, err := tx.SerializeBatch(txs)
	if err != nil {
		return err
	}
	return n.batchTopic.Publish(context.Background(), b)
}

// BroadcastBlockHeader gossips a serialised block header on frg/block/v1.
// header must be: prevStateRoot[32] || height[8] || stateRoot[32] || proposerPubKey[32] || proposerSig[64] = 168 bytes
func (n *Node) BroadcastBlockHeader(header []byte) error {
	if len(header) != 168 {
		return fmt.Errorf("block header must be 168 bytes, got %d", len(header))
	}
	return n.blockTopic.Publish(context.Background(), header)
}

// SubscribeTxs returns a channel of valid incoming transactions.
func (n *Node) SubscribeTxs() <-chan *tx.Tx {
	return n.txCh
}

// SubscribeBlockHeaders returns a channel of valid incoming block headers.
func (n *Node) SubscribeBlockHeaders() <-chan []byte {
	return n.blockCh
}

// PeerCount returns the number of currently connected peers.
func (n *Node) PeerCount() int {
	return len(n.host.Network().Peers())
}

// Addrs returns the multiaddrs of the node with peer ID.
func (n *Node) Addrs() []multiaddr.Multiaddr {
	peerID := n.host.ID().String()
	addrs := n.host.Addrs()
	out := make([]multiaddr.Multiaddr, len(addrs))
	for i, addr := range addrs {
		out[i], _ = multiaddr.NewMultiaddr(fmt.Sprintf("%s/p2p/%s", addr.String(), peerID))
	}
	return out
}

// Connect connects the node to a list of multiaddrs.
func (n *Node) Connect(ctx context.Context, addrs []multiaddr.Multiaddr) error {
	for _, addr := range addrs {
		pi, err := peer.AddrInfoFromP2pAddr(addr)
		if err != nil {
			return err
		}
		if err := n.host.Connect(ctx, *pi); err != nil {
			return err
		}
	}
	return nil
}

func (n *Node) readTxs(ctx context.Context) {
	for {
		msg, err := n.txSub.Next(ctx)
		if err != nil {
			return
		}
		t, err := parseTx(msg.Data)
		if err != nil {
			continue
		}
		select {
		case n.txCh <- t:
		default:
		}
	}
}

func (n *Node) readBatches(ctx context.Context) {
	for {
		msg, err := n.batchSub.Next(ctx)
		if err != nil {
			return
		}
		txs, err := tx.DeserializeBatch(msg.Data)
		if err != nil {
			continue
		}
		for _, t := range txs {
			if err := t.VerifySigs(); err != nil {
				continue
			}
			select {
			case n.txCh <- t:
			default:
			}
		}
	}
}

func (n *Node) readBlocks(ctx context.Context) {
	for {
		msg, err := n.blockSub.Next(ctx)
		if err != nil {
			return
		}
		if len(msg.Data) != 168 {
			continue
		}
		hdr := make([]byte, 168)
		copy(hdr, msg.Data)
		select {
		case n.blockCh <- hdr:
		default:
		}
	}
}

func parseTx(data []byte) (*tx.Tx, error) {
	t, err := tx.Deserialize(data)
	if err != nil {
		return nil, err
	}
	if err := t.VerifySigs(); err != nil {
		return nil, err
	}
	return t, nil
}
