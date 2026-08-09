package p2p

import (
	"bufio"
	"context"
	"encoding/binary"
	"fmt"
	"io"

	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/protocol"
)

const (
	blockSyncVersion          = byte(1)
	blockSyncMaxRange         = uint64(1024)
	blockSyncMaxBlockBytes    = 8 << 20
	blockSyncMaxResponseBytes = 16 << 20
	blockSyncHeaderBytes      = 8 + 1 + 8 + 8
)

var blockSyncMagic = [8]byte{'F', 'R', 'G', 'S', 'Y', 'N', 'C', 1}

// BlockProvider returns the canonical serialized block for a committed height.
// A nil block means the requested height is not available.
type BlockProvider func(height uint64) ([]byte, error)

func blockSyncProtocol(chainID string) protocol.ID {
	return protocol.ID("/frg/" + chainID + "/blocks/sync/1")
}

func (n *Node) SetBlockProvider(provider BlockProvider) {
	n.blockProvider = provider
}

// SyncBlocks requests a contiguous committed block range from a connected peer.
// The peer may return fewer blocks when its local history ends.
func (n *Node) SyncBlocks(ctx context.Context, id peer.ID, from, to uint64) ([][]byte, error) {
	if to < from || to-from >= blockSyncMaxRange {
		return nil, fmt.Errorf("invalid block sync range")
	}
	stream, err := n.host.NewStream(ctx, id, blockSyncProtocol(n.cfg.chainID()))
	if err != nil {
		return nil, fmt.Errorf("open block sync stream: %w", err)
	}
	defer stream.Close()
	if err := writeBlockSyncRequest(stream, from, to); err != nil {
		return nil, err
	}
	blocks, err := readBlockSyncResponse(stream)
	if err != nil {
		return nil, err
	}
	return blocks, nil
}

func (n *Node) handleBlockSync(stream network.Stream) {
	defer stream.Close()
	from, to, err := readBlockSyncRequest(stream)
	if err != nil {
		return
	}
	if n.blockProvider == nil {
		_ = writeBlockSyncResponse(stream, nil)
		return
	}
	blocks := make([][]byte, 0, to-from+1)
	total := 0
	for height := from; height <= to; height++ {
		data, providerErr := n.blockProvider(height)
		if providerErr != nil || data == nil {
			break
		}
		if len(data) == 0 || len(data) > blockSyncMaxBlockBytes || total+len(data) > blockSyncMaxResponseBytes {
			break
		}
		blocks = append(blocks, append([]byte(nil), data...))
		total += len(data)
		if height == ^uint64(0) {
			break
		}
	}
	_ = writeBlockSyncResponse(stream, blocks)
}

func writeBlockSyncRequest(w io.Writer, from, to uint64) error {
	var request [blockSyncHeaderBytes]byte
	copy(request[:8], blockSyncMagic[:])
	request[8] = blockSyncVersion
	binary.BigEndian.PutUint64(request[9:17], from)
	binary.BigEndian.PutUint64(request[17:25], to)
	_, err := w.Write(request[:])
	return err
}

func readBlockSyncRequest(r io.Reader) (uint64, uint64, error) {
	var request [blockSyncHeaderBytes]byte
	if _, err := io.ReadFull(r, request[:]); err != nil {
		return 0, 0, err
	}
	if string(request[:8]) != string(blockSyncMagic[:]) || request[8] != blockSyncVersion {
		return 0, 0, fmt.Errorf("invalid block sync request")
	}
	from := binary.BigEndian.Uint64(request[9:17])
	to := binary.BigEndian.Uint64(request[17:25])
	if to < from || to-from >= blockSyncMaxRange {
		return 0, 0, fmt.Errorf("invalid block sync range")
	}
	return from, to, nil
}

func writeBlockSyncResponse(w io.Writer, blocks [][]byte) error {
	var count [4]byte
	binary.BigEndian.PutUint32(count[:], uint32(len(blocks)))
	if _, err := w.Write(count[:]); err != nil {
		return err
	}
	for _, block := range blocks {
		if len(block) == 0 || len(block) > blockSyncMaxBlockBytes {
			return fmt.Errorf("invalid block sync response block")
		}
		var size [4]byte
		binary.BigEndian.PutUint32(size[:], uint32(len(block)))
		if _, err := w.Write(size[:]); err != nil {
			return err
		}
		if _, err := w.Write(block); err != nil {
			return err
		}
	}
	return nil
}

func readBlockSyncResponse(r io.Reader) ([][]byte, error) {
	reader := bufio.NewReader(io.LimitReader(r, blockSyncMaxResponseBytes+4+4))
	var countBuf [4]byte
	if _, err := io.ReadFull(reader, countBuf[:]); err != nil {
		return nil, err
	}
	count := binary.BigEndian.Uint32(countBuf[:])
	if count > uint32(blockSyncMaxRange) {
		return nil, fmt.Errorf("block sync response count exceeds limit")
	}
	blocks := make([][]byte, 0, count)
	total := 0
	for i := uint32(0); i < count; i++ {
		var size [4]byte
		if _, err := io.ReadFull(reader, size[:]); err != nil {
			return nil, err
		}
		length := int(binary.BigEndian.Uint32(size[:]))
		if length == 0 || length > blockSyncMaxBlockBytes || total+length > blockSyncMaxResponseBytes {
			return nil, fmt.Errorf("block sync response exceeds limit")
		}
		block := make([]byte, length)
		if _, err := io.ReadFull(reader, block); err != nil {
			return nil, err
		}
		blocks = append(blocks, block)
		total += length
	}
	return blocks, nil
}
