package benchmarks

import (
	"math/big"
	"testing"

	"github.com/imattau/frg/core/keys"
	"github.com/imattau/frg/core/tx"
)

func benchKeypair(tb testing.TB) *keys.Keypair {
	tb.Helper()
	kp, err := keys.GenerateKeypair()
	if err != nil {
		tb.Fatalf("GenerateKeypair: %v", err)
	}
	return kp
}

func benchTx(tb testing.TB, sender, receiver *keys.Keypair, value int64, nonce uint64) *tx.Tx {
	tb.Helper()
	tr := &tx.Tx{
		Type:           tx.TxTypeTransfer,
		Sender:         "sender",
		Receiver:       "receiver",
		Value:          big.NewInt(value),
		Nonce:          nonce,
		SenderPubKey:   sender.PublicKey,
		ReceiverPubKey: receiver.PublicKey,
	}
	recvSig, err := tr.SignReceiver(receiver)
	if err != nil {
		tb.Fatalf("SignReceiver: %v", err)
	}
	tr.ReceiverSig = recvSig
	sendSig, err := tr.SignSender(sender)
	if err != nil {
		tb.Fatalf("SignSender: %v", err)
	}
	tr.SenderSig = sendSig
	return tr
}

func benchUnsignedTx(tb testing.TB, sender, receiver *keys.Keypair, value int64, nonce uint64) *tx.Tx {
	tb.Helper()
	return &tx.Tx{
		Type:           tx.TxTypeTransfer,
		Sender:         "sender",
		Receiver:       "receiver",
		Value:          big.NewInt(value),
		Nonce:          nonce,
		SenderPubKey:   sender.PublicKey,
		ReceiverPubKey: receiver.PublicKey,
	}
}
