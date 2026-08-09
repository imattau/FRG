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
	"github.com/libp2p/go-libp2p/core/protocol"
	"github.com/libp2p/go-libp2p/p2p/discovery/mdns"
	"github.com/multiformats/go-multiaddr"

	"github.com/imattau/frg/core/keys"
	"github.com/imattau/frg/core/tx"
)

const (
	topicTxSuffix    = "/tx/v1"
	topicBatchSuffix = "/batch/v1"
	topicBlockSuffix = "/block/v1"
	topicVoteSuffix  = "/vote/v1"
	defaultChainID   = "frg-mainnet-1"
)

// Config holds node configuration.
type Config struct {
	ListenAddr     string   // e.g. "/ip4/0.0.0.0/tcp/7777"
	BootstrapPeers []string // multiaddrs of bootstrap nodes
	ChainID        string   // network isolation, default "frg-mainnet-1"
	EnableMDNS     bool     // true for testnet/local only
}

func (c *Config) chainID() string {
	if c.ChainID == "" {
		return defaultChainID
	}
	return c.ChainID
}

func (c *Config) topicTx() string    { return "frg/" + c.chainID() + topicTxSuffix }
func (c *Config) topicBatch() string { return "frg/" + c.chainID() + topicBatchSuffix }
func (c *Config) topicBlock() string { return "frg/" + c.chainID() + topicBlockSuffix }
func (c *Config) topicVote() string  { return "frg/" + c.chainID() + topicVoteSuffix }
func (c *Config) dhtPrefix() string  { return "/frg/" + c.chainID() + "/kad/v1" }

// Node is the FRG P2P node.
type Node struct {
	cfg        Config
	host       host.Host
	dht        *dht.IpfsDHT
	ps         *pubsub.PubSub
	txTopic    *pubsub.Topic
	batchTopic *pubsub.Topic
	blockTopic *pubsub.Topic
	voteTopic  *pubsub.Topic
	txSub      *pubsub.Subscription
	batchSub   *pubsub.Subscription
	blockSub   *pubsub.Subscription
	voteSub    *pubsub.Subscription
	txCh       chan *tx.Tx
	blockCh    chan []byte
	voteCh     chan []byte
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

	txTopic, err := ps.Join(cfg.topicTx())
	if err != nil {
		cancel()
		h.Close()
		return nil, fmt.Errorf("join tx topic: %w", err)
	}
	batchTopic, err := ps.Join(cfg.topicBatch())
	if err != nil {
		cancel()
		h.Close()
		return nil, fmt.Errorf("join batch topic: %w", err)
	}
	blockTopic, err := ps.Join(cfg.topicBlock())
	if err != nil {
		cancel()
		h.Close()
		return nil, fmt.Errorf("join block topic: %w", err)
	}
	voteTopic, err := ps.Join(cfg.topicVote())
	if err != nil {
		cancel()
		h.Close()
		return nil, fmt.Errorf("join vote topic: %w", err)
	}

	txSub, err := txTopic.Subscribe(pubsub.WithBufferSize(8192))
	if err != nil {
		cancel()
		h.Close()
		return nil, fmt.Errorf("subscribe tx: %w", err)
	}
	batchSub, err := batchTopic.Subscribe(pubsub.WithBufferSize(4096))
	if err != nil {
		cancel()
		h.Close()
		return nil, fmt.Errorf("subscribe batch: %w", err)
	}
	blockSub, err := blockTopic.Subscribe(pubsub.WithBufferSize(256))
	if err != nil {
		cancel()
		h.Close()
		return nil, fmt.Errorf("subscribe block: %w", err)
	}
	voteSub, err := voteTopic.Subscribe(pubsub.WithBufferSize(4096))
	if err != nil {
		cancel()
		h.Close()
		return nil, fmt.Errorf("subscribe vote: %w", err)
	}

	n := &Node{
		cfg:        cfg,
		host:       h,
		ps:         ps,
		txTopic:    txTopic,
		batchTopic: batchTopic,
		blockTopic: blockTopic,
		voteTopic:  voteTopic,
		txSub:      txSub,
		batchSub:   batchSub,
		blockSub:   blockSub,
		voteSub:    voteSub,
		txCh:       make(chan *tx.Tx, 16384),
		blockCh:    make(chan []byte, 16),
		voteCh:     make(chan []byte, 1024),
		cancel:     cancel,
	}

	go n.readTxs(innerCtx)
	go n.readBatches(innerCtx)
	go n.readBlocks(innerCtx)
	go n.readVotes(innerCtx)

	if len(cfg.BootstrapPeers) > 0 {
		kad, err := dht.New(innerCtx, h, dht.ProtocolPrefix(protocol.ID(cfg.dhtPrefix())))
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

	if cfg.EnableMDNS {
		_ = n.setupMDNS(innerCtx)
	}

	return n, nil
}

// Close shuts down the node gracefully.
func (n *Node) Close() error {
	n.cancel()
	n.txSub.Cancel()
	n.batchSub.Cancel()
	n.blockSub.Cancel()
	n.voteSub.Cancel()
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

// BroadcastBlockHeader gossips a serialised block header or proposal on frg/block/v1.
func (n *Node) BroadcastBlockHeader(header []byte) error {
	return n.blockTopic.Publish(context.Background(), header)
}

// BroadcastVote gossips a serialised vote on frg/vote/v1.
func (n *Node) BroadcastVote(vote []byte) error {
	return n.voteTopic.Publish(context.Background(), vote)
}

// SubscribeTxs returns a channel of valid incoming transactions.
func (n *Node) SubscribeTxs() <-chan *tx.Tx {
	return n.txCh
}

// SubscribeBlockHeaders returns a channel of valid incoming block headers.
func (n *Node) SubscribeBlockHeaders() <-chan []byte {
	return n.blockCh
}

// SubscribeVotes returns a channel of incoming votes.
func (n *Node) SubscribeVotes() <-chan []byte {
	return n.voteCh
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
		data := make([]byte, len(msg.Data))
		copy(data, msg.Data)
		select {
		case n.blockCh <- data:
		default:
		}
	}
}

func (n *Node) readVotes(ctx context.Context) {
	for {
		msg, err := n.voteSub.Next(ctx)
		if err != nil {
			return
		}
		// Votes are 141 bytes fixed-width
		if len(msg.Data) != 141 {
			continue
		}
		vote := make([]byte, 141)
		copy(vote, msg.Data)
		select {
		case n.voteCh <- vote:
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

type discoveryNotifee struct {
	h host.Host
}

func (n *discoveryNotifee) HandlePeerFound(pi peer.AddrInfo) {
	if pi.ID == n.h.ID() {
		return
	}
	_ = n.h.Connect(context.Background(), pi)
}

func (node *Node) setupMDNS(ctx context.Context) error {
	tag := node.cfg.chainID()
	svc := mdns.NewMdnsService(node.host, tag, &discoveryNotifee{h: node.host})
	return svc.Start()
}
