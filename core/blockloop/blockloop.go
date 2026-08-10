package blockloop

import (
	"container/list"
	"context"
	"sync"

	"github.com/imattau/frg/core/consensus"
	"github.com/imattau/frg/core/keys"
	"github.com/imattau/frg/core/p2p"
	"github.com/imattau/frg/core/tx"
)

const (
	defaultCap = 4 * 65536
	TMax       = 65536
)

// mempool stores pending transactions in FIFO order (queue) with O(1)
// membership testing and O(1) removal by id (index maps a tx id to its
// list.Element) -- a plain slice + map[id]struct{} (the previous
// approach) made membership O(1) too, but removeCommitted (below) had to
// linearly scan the whole slice, re-hashing every entry's ID(), to find
// each committed tx to remove. At up to defaultCap (262144) entries and
// up to TMax (65536) removals per committed block, that's up to ~8.6
// billion ID() calls in the worst case -- comfortably enough to blow
// through consensus's propose/prevote/precommit timeouts every round,
// livelocking block production entirely under sustained load. Confirmed
// live: a real devnet spun at ~82% CPU with block height completely
// frozen once the mempool filled to its cap under a sustained ~4700 tx/s
// submission rate.
type mempool struct {
	mu    sync.Mutex
	queue *list.List
	index map[[32]byte]*list.Element
	cap   int
}

func newMempool(cap int) *mempool {
	return &mempool{
		queue: list.New(),
		index: make(map[[32]byte]*list.Element),
		cap:   cap,
	}
}

type BlockLoop struct {
	kp       *keys.Keypair
	p2p      *p2p.Node
	mempool  *mempool
	stopCh   chan struct{}
	stopOnce sync.Once
	chainID  string
}

func New(kp *keys.Keypair, n *p2p.Node) *BlockLoop {
	return NewWithChainID(kp, n, tx.DefaultChainID)
}

func NewWithChainID(kp *keys.Keypair, n *p2p.Node, chainID string) *BlockLoop {
	if chainID == "" {
		chainID = tx.DefaultChainID
	}
	return &BlockLoop{
		kp:      kp,
		p2p:     n,
		mempool: newMempool(defaultCap),
		stopCh:  make(chan struct{}),
		chainID: chainID,
	}
}

func NewForTest(cap int) *BlockLoop {
	return &BlockLoop{
		mempool: newMempool(cap),
	}
}

func NewWithKeyForTest(kp *keys.Keypair, cap int) *BlockLoop {
	return &BlockLoop{
		kp:      kp,
		mempool: newMempool(cap),
	}
}

func (bl *BlockLoop) Enqueue(t *tx.Tx) {
	bl.mempool.mu.Lock()
	defer bl.mempool.mu.Unlock()

	id, err := t.ID()
	if err != nil {
		return
	}

	if _, ok := bl.mempool.index[id]; ok {
		return
	}

	if bl.mempool.queue.Len() >= bl.mempool.cap {
		// Drop oldest.
		if front := bl.mempool.queue.Front(); front != nil {
			oldest := front.Value.(*tx.Tx)
			oldestID, _ := oldest.ID()
			delete(bl.mempool.index, oldestID)
			bl.mempool.queue.Remove(front)
		}
	}

	elem := bl.mempool.queue.PushBack(t)
	bl.mempool.index[id] = elem
}

func (bl *BlockLoop) Len() int {
	bl.mempool.mu.Lock()
	defer bl.mempool.mu.Unlock()
	return bl.mempool.queue.Len()
}

func (bl *BlockLoop) Snapshot() []*tx.Tx {
	bl.mempool.mu.Lock()
	defer bl.mempool.mu.Unlock()
	out := make([]*tx.Tx, 0, bl.mempool.queue.Len())
	for e := bl.mempool.queue.Front(); e != nil; e = e.Next() {
		out = append(out, e.Value.(*tx.Tx))
	}
	return out
}

func (bl *BlockLoop) Has(id [32]byte) bool {
	bl.mempool.mu.Lock()
	defer bl.mempool.mu.Unlock()
	_, ok := bl.mempool.index[id]
	return ok
}

func (bl *BlockLoop) Start(ctx context.Context) error {
	txSub := bl.p2p.SubscribeTxs()
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-bl.stopCh:
				return
			case t := <-txSub:
				bl.Enqueue(t)
			}
		}
	}()
	return nil
}

func (bl *BlockLoop) Stop() error {
	bl.stopOnce.Do(func() { close(bl.stopCh) })
	return nil
}

func (bl *BlockLoop) Propose(height uint64, round uint32, prevAttest consensus.AttestationSet) (*consensus.BlockProposal, error) {
	return bl.propose(height, round, prevAttest, [32]byte{})
}

// ProposeForState creates a proposal bound to the exact parent state root.
func (bl *BlockLoop) ProposeForState(height uint64, round uint32, prevAttest consensus.AttestationSet, prevRoot [32]byte) (*consensus.BlockProposal, error) {
	return bl.propose(height, round, prevAttest, prevRoot)
}

func (bl *BlockLoop) propose(height uint64, round uint32, prevAttest consensus.AttestationSet, prevRoot [32]byte) (*consensus.BlockProposal, error) {
	bl.mempool.mu.Lock()
	count := bl.mempool.queue.Len()
	if count > TMax {
		count = TMax
	}
	txs := make([]*tx.Tx, 0, count)
	for e := bl.mempool.queue.Front(); e != nil && len(txs) < count; e = e.Next() {
		txs = append(txs, e.Value.(*tx.Tx))
	}
	// Note: We don't dequeue here. OnCommit will cleanup.
	// Actually the design says "dequeue up to T_MAX txs".
	// But OnCommit removes committed txs.
	// If we dequeue now, and proposal fails, we lose txs.
	// If we don't dequeue, we might propose them again in next round.
	// Re-proposing is safer.
	bl.mempool.mu.Unlock()

	p := &consensus.BlockProposal{
		Height:           height,
		Round:            round,
		ProposerPK:       bl.kp.PublicKey,
		PrevStateRoot:    prevRoot,
		Txs:              txs,
		PrevAttestations: prevAttest,
	}
	body := consensus.ProposalSignBytesForChain(p, bl.chainID)
	sig, err := bl.kp.Sign(body)
	if err != nil {
		return nil, err
	}
	p.ProposerSig = sig

	// The consensus engine serializes and broadcasts the signed proposal.
	return p, nil
}

func (bl *BlockLoop) OnCommit(height uint64, txs []*tx.Tx) {
	bl.removeCommitted(txs)
}

// OnReject removes transactions from a proposal that could not be applied.
func (bl *BlockLoop) OnReject(height uint64, txs []*tx.Tx) {
	bl.removeCommitted(txs)
}

func (bl *BlockLoop) removeCommitted(txs []*tx.Tx) {
	bl.mempool.mu.Lock()
	defer bl.mempool.mu.Unlock()

	for _, t := range txs {
		id, err := t.ID()
		if err != nil {
			continue
		}
		if elem, ok := bl.mempool.index[id]; ok {
			bl.mempool.queue.Remove(elem)
			delete(bl.mempool.index, id)
		}
	}
}
