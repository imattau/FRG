package statemachine

import (
	"encoding/binary"
	"fmt"

	"github.com/imattau/frg/core/tx"
	bolt "go.etcd.io/bbolt"
)

var blocksBucket = []byte("blocks")

const (
	// maxStoredBlockBytes must accommodate a full T_MAX (65536 tx) block;
	// 8MB was sized for a much smaller assumed block and silently capped
	// real throughput well below the documented per-block capacity.
	maxStoredBlockBytes = 64 << 20
	maxBlockRange       = 1024
)

var (
	blockMagicV1 = []byte("FRG_BLOCK_V1\x00")
	blockMagicV2 = []byte("FRG_BLOCK_V2\x00")
)

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
	if len(block.ProposalBytes) > maxStoredBlockBytes {
		return nil, fmt.Errorf("serialized proposal exceeds %d bytes", maxStoredBlockBytes)
	}
	const headerSize = 13 + 8 + 32 + 32 + 32 + 4 + 4
	data := make([]byte, headerSize+len(batch)+len(block.ProposalBytes))
	copy(data[:13], blockMagicV2)
	binary.BigEndian.PutUint64(data[13:21], block.Height)
	copy(data[21:53], block.ProposerPubKey[:])
	copy(data[53:85], block.PrevStateRoot[:])
	copy(data[85:117], block.StateRoot[:])
	binary.BigEndian.PutUint32(data[117:121], uint32(len(block.Txs)))
	binary.BigEndian.PutUint32(data[121:125], uint32(len(block.ProposalBytes)))
	copy(data[125:125+len(batch)], batch)
	copy(data[125+len(batch):], block.ProposalBytes)
	if len(data) > maxStoredBlockBytes {
		return nil, fmt.Errorf("serialized block exceeds %d bytes", maxStoredBlockBytes)
	}
	return data, nil
}

func deserializeBlock(data []byte) (*Block, error) {
	const v1HeaderSize = 13 + 8 + 32 + 4
	const v2HeaderSize = 13 + 8 + 32 + 32 + 32 + 4 + 4
	if len(data) < v1HeaderSize || len(data) > maxStoredBlockBytes {
		return nil, fmt.Errorf("invalid stored block")
	}
	if string(data[:13]) == string(blockMagicV1) {
		return deserializeV1Block(data)
	}
	if string(data[:13]) != string(blockMagicV2) || len(data) < v2HeaderSize {
		return nil, fmt.Errorf("invalid stored block")
	}
	block := &Block{Height: binary.BigEndian.Uint64(data[13:21])}
	copy(block.ProposerPubKey[:], data[21:53])
	copy(block.PrevStateRoot[:], data[53:85])
	copy(block.StateRoot[:], data[85:117])
	count := int(binary.BigEndian.Uint32(data[117:121]))
	proposalLen := int(binary.BigEndian.Uint32(data[121:125]))
	if proposalLen < 0 || proposalLen > len(data)-v2HeaderSize {
		return nil, fmt.Errorf("invalid stored proposal length")
	}
	batchEnd := len(data) - proposalLen
	if batchEnd < v2HeaderSize {
		return nil, fmt.Errorf("invalid stored block payload")
	}
	block.ProposalBytes = append([]byte(nil), data[batchEnd:]...)
	if count == 0 {
		if batchEnd != v2HeaderSize {
			return nil, fmt.Errorf("stored empty block has trailing bytes")
		}
		return block, nil
	}
	txs, err := tx.DeserializeBatch(data[v2HeaderSize:batchEnd])
	if err != nil {
		return nil, err
	}
	if len(txs) != count {
		return nil, fmt.Errorf("stored block transaction count mismatch")
	}
	block.Txs = txs
	return block, nil
}

func deserializeV1Block(data []byte) (*Block, error) {
	const headerSize = 13 + 8 + 32 + 4
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
