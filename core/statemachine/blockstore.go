package statemachine

import (
	"encoding/binary"
	"fmt"

	"github.com/imattau/frg/core/tx"
	bolt "go.etcd.io/bbolt"
)

var blocksBucket = []byte("blocks")

const (
	maxStoredBlockBytes = 8 << 20
	maxBlockRange       = 1024
)

var blockMagic = []byte("FRG_BLOCK_V1\x00")

// BlockAt returns a committed block by height. Height zero is genesis and has
// no stored block.
func (sm *StateMachine) BlockAt(height uint64) (*Block, error) {
	var block *Block
	err := sm.db.View(func(btx *bolt.Tx) error {
		data := btx.Bucket(blocksBucket).Get(blockKey(height))
		if data == nil {
			return nil
		}
		var err error
		block, err = deserializeBlock(data)
		return err
	})
	return block, err
}

// Blocks returns committed blocks in the inclusive height range.
func (sm *StateMachine) Blocks(from, to uint64) ([]*Block, error) {
	if to < from {
		return nil, fmt.Errorf("invalid block range")
	}
	if to-from >= maxBlockRange {
		return nil, fmt.Errorf("block range exceeds %d blocks", maxBlockRange)
	}
	blocks := make([]*Block, 0, to-from+1)
	for height := from; height <= to; height++ {
		block, err := sm.BlockAt(height)
		if err != nil {
			return nil, err
		}
		if block == nil {
			return blocks, nil
		}
		blocks = append(blocks, block)
		if height == ^uint64(0) {
			break
		}
	}
	return blocks, nil
}

func blockKey(height uint64) []byte {
	key := make([]byte, 8)
	binary.BigEndian.PutUint64(key, height)
	return key
}

// SerializeBlock returns the canonical persisted representation of a block.
func SerializeBlock(block *Block) ([]byte, error) {
	return serializeBlock(block)
}

// DeserializeBlock parses the canonical persisted representation of a block.
func DeserializeBlock(data []byte) (*Block, error) {
	return deserializeBlock(data)
}

func serializeBlock(block *Block) ([]byte, error) {
	var batch []byte
	var err error
	if len(block.Txs) > 0 {
		batch, err = tx.SerializeBatch(block.Txs)
		if err != nil {
			return nil, err
		}
	}
	const headerSize = 13 + 8 + 32 + 4
	data := make([]byte, headerSize+len(batch))
	copy(data[:len(blockMagic)], blockMagic)
	binary.BigEndian.PutUint64(data[13:21], block.Height)
	copy(data[21:53], block.ProposerPubKey[:])
	binary.BigEndian.PutUint32(data[53:57], uint32(len(block.Txs)))
	copy(data[57:], batch)
	if len(data) > maxStoredBlockBytes {
		return nil, fmt.Errorf("serialized block exceeds %d bytes", maxStoredBlockBytes)
	}
	return data, nil
}

func deserializeBlock(data []byte) (*Block, error) {
	const headerSize = 13 + 8 + 32 + 4
	if len(data) < headerSize || len(data) > maxStoredBlockBytes || string(data[:len(blockMagic)]) != string(blockMagic) {
		return nil, fmt.Errorf("invalid stored block")
	}
	block := &Block{Height: binary.BigEndian.Uint64(data[13:21])}
	copy(block.ProposerPubKey[:], data[21:53])
	count := int(binary.BigEndian.Uint32(data[53:57]))
	if count == 0 {
		if len(data) != headerSize {
			return nil, fmt.Errorf("stored empty block has trailing bytes")
		}
		return block, nil
	}
	txs, err := tx.DeserializeBatch(data[headerSize:])
	if err != nil {
		return nil, err
	}
	if len(txs) != count {
		return nil, fmt.Errorf("stored block transaction count mismatch")
	}
	block.Txs = txs
	return block, nil
}

func putBlockTx(btx *bolt.Tx, block *Block) error {
	data, err := serializeBlock(block)
	if err != nil {
		return err
	}
	return btx.Bucket(blocksBucket).Put(blockKey(block.Height), data)
}
