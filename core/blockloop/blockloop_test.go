package blockloop_test

import (
    "math/big"
    "testing"

    "github.com/imattau/frg/core/blockloop"
    "github.com/imattau/frg/core/consensus"
    "github.com/imattau/frg/core/keys"
    "github.com/imattau/frg/core/tx"
)

func makeTransferTx(t *testing.T, nonce uint64) *tx.Tx {
    t.Helper()
    kp, err := keys.GenerateKeypair()
    if err != nil {
        t.Fatal(err)
    }
    rcvr, err := keys.GenerateKeypair()
    if err != nil {
        t.Fatal(err)
    }
    tr := &tx.Tx{
        Type:           tx.TxTypeTransfer,
        SenderPubKey:   kp.PublicKey,
        ReceiverPubKey: rcvr.PublicKey,
        Value:          big.NewInt(1000),
        Nonce:          nonce,
    }
    senderSig, err := tr.SignSender(kp)
    if err != nil {
        t.Fatal(err)
    }
    tr.SenderSig = senderSig
    receiverSig, err := tr.SignReceiver(rcvr)
    if err != nil {
        t.Fatal(err)
    }
    tr.ReceiverSig = receiverSig
    return tr
}

func TestEnqueueDedup(t *testing.T) {
    bl := blockloop.NewForTest(10)
    tr := makeTransferTx(t, 1)
    bl.Enqueue(tr)
    bl.Enqueue(tr) // duplicate
    if bl.Len() != 1 {
        t.Fatalf("expected 1, got %d", bl.Len())
    }
}

func TestMempoolCap(t *testing.T) {
    cap := 3
    bl := blockloop.NewForTest(cap)
    txs := make([]*tx.Tx, cap+1)
    for i := range txs {
        txs[i] = makeTransferTx(t, uint64(i+1))
        bl.Enqueue(txs[i])
    }
    if bl.Len() != cap {
        t.Fatalf("expected %d, got %d", cap, bl.Len())
    }
    // newest tx must be present
    id, _ := txs[cap].ID()
    if !bl.Has(id) {
        t.Fatal("newest tx should be in mempool")
    }
    // oldest tx must be dropped
    id0, _ := txs[0].ID()
    if bl.Has(id0) {
        t.Fatal("oldest tx should have been dropped")
    }
}

func TestProposeEmptyMempool(t *testing.T) {
    kp, _ := keys.GenerateKeypair()
    bl := blockloop.NewWithKeyForTest(kp, 10)
    p, err := bl.Propose(1, 0, consensus.AttestationSet{})
    if err != nil {
        t.Fatal(err)
    }
    if len(p.Txs) != 0 {
        t.Fatalf("expected 0 txs, got %d", len(p.Txs))
    }
}

func TestProposeDrainsUpToTMax(t *testing.T) {
    kp, _ := keys.GenerateKeypair()
    bl := blockloop.NewWithKeyForTest(kp, 100)
    for i := 0; i < 10; i++ {
        bl.Enqueue(makeTransferTx(t, uint64(i+1)))
    }
    p, err := bl.Propose(1, 0, consensus.AttestationSet{})
    if err != nil {
        t.Fatal(err)
    }
    if len(p.Txs) != 10 {
        t.Fatalf("expected 10 txs, got %d", len(p.Txs))
    }
}

func TestOnCommitRemovesTxs(t *testing.T) {
    bl := blockloop.NewForTest(10)
    txs := make([]*tx.Tx, 5)
    for i := range txs {
        txs[i] = makeTransferTx(t, uint64(i+1))
        bl.Enqueue(txs[i])
    }
    bl.OnCommit(1, txs[:3])
    if bl.Len() != 2 {
        t.Fatalf("expected 2, got %d", bl.Len())
    }
    for i := 0; i < 3; i++ {
        id, _ := txs[i].ID()
        if bl.Has(id) {
            t.Fatalf("tx %d should have been removed", i)
        }
    }
}
