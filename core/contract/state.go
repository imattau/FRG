package contract

import (
	"encoding/binary"
	"sort"

	"github.com/imattau/frg/core/hash"
)

const maxStateKeyLen = 32
const maxStateValLen = 64 * 1024

type StateStore struct {
	data map[string][]byte
}

func NewStateStore() *StateStore {
	return &StateStore{data: make(map[string][]byte)}
}

func (s *StateStore) Get(key []byte) ([]byte, bool) {
	v, ok := s.data[string(key)]
	return v, ok
}

func (s *StateStore) Set(key, value []byte) {
	k := string(key)
	if len(value) == 0 {
		delete(s.data, k)
		return
	}
	cp := make([]byte, len(value))
	copy(cp, value)
	s.data[k] = cp
}

func (s *StateStore) Delete(key []byte) {
	delete(s.data, string(key))
}

func (s *StateStore) Clone() *StateStore {
	clone := NewStateStore()
	for k, v := range s.data {
		clone.data[k] = v
	}
	return clone
}

func (s *StateStore) Len() int {
	return len(s.data)
}

func (s *StateStore) StateRoot() [32]byte {
	keys := make([]string, 0, len(s.data))
	for k := range s.data {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	state := []byte(hash.DomainContractState)
	for _, k := range keys {
		state = append(state, []byte(k)...)
		state = append(state, s.data[k]...)
	}
	return hash.Hash(state)
}

// Serialize encodes the state store as a binary blob:
//   [key_count uint16] [key_len uint8] [key_bytes] [val_len uint16] [val_bytes]...
func (s *StateStore) Serialize() []byte {
	keys := make([]string, 0, len(s.data))
	for k := range s.data {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	size := 2 // key_count
	for _, k := range keys {
		size += 1 + len(k) + 2 + len(s.data[k])
	}
	buf := make([]byte, size)
	pos := 0
	binary.BigEndian.PutUint16(buf[pos:], uint16(len(keys)))
	pos += 2
	for _, k := range keys {
		buf[pos] = uint8(len(k))
		pos++
		copy(buf[pos:], k)
		pos += len(k)
		v := s.data[k]
		binary.BigEndian.PutUint16(buf[pos:], uint16(len(v)))
		pos += 2
		copy(buf[pos:], v)
		pos += len(v)
	}
	return buf
}

// DeserializeState reverses Serialize.
func DeserializeState(raw []byte) *StateStore {
	s := NewStateStore()
	if len(raw) < 2 {
		return s
	}
	count := int(binary.BigEndian.Uint16(raw))
	pos := 2
	for i := 0; i < count && pos < len(raw); i++ {
		if pos+1 > len(raw) {
			break
		}
		keyLen := int(raw[pos])
		pos++
		if pos+keyLen > len(raw) {
			break
		}
		key := string(raw[pos : pos+keyLen])
		pos += keyLen
		if pos+2 > len(raw) {
			break
		}
		valLen := int(binary.BigEndian.Uint16(raw[pos:]))
		pos += 2
		if pos+valLen > len(raw) {
			break
		}
		val := make([]byte, valLen)
		copy(val, raw[pos:pos+valLen])
		pos += valLen
		s.data[key] = val
	}
	return s
}
