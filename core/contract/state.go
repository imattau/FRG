package contract

import (
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

	h := hash.Hash([]byte(hash.DomainContractState))
	state := []byte(hash.DomainContractState)
	for _, k := range keys {
		_ = h
		state = append(state, []byte(k)...)
		state = append(state, s.data[k]...)
	}
	return hash.Hash(state)
}
