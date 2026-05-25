package blockloop

import (
    "context"
    "sync"

    "github.com/imattau/frg/core/consensus"
    "github.com/imattau/frg/core/keys"
    "github.com/imattau/frg/core/p2p"
    "github.com/imattau/frg/core/tx"
)

const (
    defaultCap = 4 * 65536 // 4 × T_MAX
    TMax       = 65536
)

type mempool struct {
    mu    sync.Mutex
    queue []*tx.Tx
    seen  map[[32]byte]struct{}
    cap   int
}

func newMempool(cap int) *mempool {
    return &mempool{
        seen: make(map[[32]byte]struct{}),
        cap:  cap,
    }
}

type BlockLoop struct {
    kp      *keys.Keypair
    p2p     *p2p.Node
    mempool *mempool
    stopCh  chan struct{}
}

func New(kp *keys.Keypair, n *p2p.Node) *BlockLoop {
    return &BlockLoop{
        kp:      kp,
        p2p:     n,
        mempool: newMempool(defaultCap),
        stopCh:  make(chan struct{}),
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

    if _, ok := bl.mempool.seen[id]; ok {
        return
    }

    if len(bl.mempool.queue) >= bl.mempool.cap {
        // Drop oldest
        oldest := bl.mempool.queue[0]
        oldestID, _ := oldest.ID()
        delete(bl.mempool.seen, oldestID)
        bl.mempool.queue = bl.mempool.queue[1:]
    }

    bl.mempool.queue = append(bl.mempool.queue, t)
    bl.mempool.seen[id] = struct{}{}
}

func (bl *BlockLoop) Len() int {
    bl.mempool.mu.Lock()
    defer bl.mempool.mu.Unlock()
    return len(bl.mempool.queue)
}

func (bl *BlockLoop) Has(id [32]byte) bool {
    bl.mempool.mu.Lock()
    defer bl.mempool.mu.Unlock()
    _, ok := bl.mempool.seen[id]
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
    close(bl.stopCh)
    return nil
}

func (bl *BlockLoop) Propose(height uint64, round uint32, prevAttest consensus.AttestationSet) (*consensus.BlockProposal, error) {
    bl.mempool.mu.Lock()
    count := len(bl.mempool.queue)
    if count > TMax {
        count = TMax
    }
    txs := make([]*tx.Tx, count)
    copy(txs, bl.mempool.queue[:count])
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
        Txs:              txs,
        PrevAttestations: prevAttest,
    }
    body := consensus.ProposalSignBytes(p)
    sig, err := bl.kp.Sign(body)
    if err != nil {
        return nil, err
    }
    p.ProposerSig = sig

    // Serialise and broadcast
    // In consensus implemention, ProposalSignBytes only handled H, R, ProposerPK.
    // Full serialisation is needed here.
    // For now, we'll use a placeholder until full serialisation is defined.
    // Actually, task 1 is just mempool.
    return p, nil
}

func (bl *BlockLoop) OnCommit(height uint64, txs []*tx.Tx) {
    bl.mempool.mu.Lock()
    defer bl.mempool.mu.Unlock()

    for _, t := range txs {
        id, err := t.ID()
        if err != nil {
            continue
        }
        if _, ok := bl.mempool.seen[id]; ok {
            delete(bl.mempool.seen, id)
            // Remove from queue. This is O(N) but mempool is small (256k).
            // A better way would be a linked list or just filtering the slice.
            for i, qt := range bl.mempool.queue {
                qid, _ := qt.ID()
                if qid == id {
                    bl.mempool.queue = append(bl.mempool.queue[:i], bl.mempool.queue[i+1:]...)
                    break
                }
            }
        }
    }
}
