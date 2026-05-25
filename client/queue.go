package client

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"io"

	"github.com/imattau/frg/core/keys"
	bolt "go.etcd.io/bbolt"
	"golang.org/x/crypto/hkdf"
)

var pendingBucket = []byte("pending")

type queue struct {
	db  *bolt.DB
	key [32]byte
}

func openQueue(path string, kp *keys.Keypair) (*queue, error) {
	db, err := bolt.Open(path, 0600, nil)
	if err != nil {
		return nil, fmt.Errorf("open queue: %w", err)
	}

	if err := db.Update(func(tx *bolt.Tx) error {
		_, err := tx.CreateBucketIfNotExists(pendingBucket)
		return err
	}); err != nil {
		_ = db.Close()
		return nil, err
	}

	key, err := deriveKey(kp)
	if err != nil {
		_ = db.Close()
		return nil, err
	}

	return &queue{db: db, key: key}, nil
}

func deriveKey(kp *keys.Keypair) ([32]byte, error) {
	r := hkdf.New(sha256.New, kp.PrivateKey[:], nil, []byte("frg-queue-v1"))
	var key [32]byte
	if _, err := io.ReadFull(r, key[:]); err != nil {
		return [32]byte{}, err
	}
	return key, nil
}

func (q *queue) Enqueue(txBytes []byte) error {
	ciphertext, err := q.encrypt(txBytes)
	if err != nil {
		return err
	}
	return q.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(pendingBucket)
		seq, err := b.NextSequence()
		if err != nil {
			return err
		}
		key := make([]byte, 8)
		binary.BigEndian.PutUint64(key, seq)
		return b.Put(key, ciphertext)
	})
}

func (q *queue) Drain() ([][]byte, [][]byte, error) {
	var dbKeys [][]byte
	var txBytesSlice [][]byte
	err := q.db.View(func(tx *bolt.Tx) error {
		return tx.Bucket(pendingBucket).ForEach(func(k, v []byte) error {
			keyCopy := make([]byte, len(k))
			copy(keyCopy, k)
			dbKeys = append(dbKeys, keyCopy)

			plain, err := q.decrypt(v)
			if err != nil {
				return err
			}
			txBytesSlice = append(txBytesSlice, plain)
			return nil
		})
	})
	return dbKeys, txBytesSlice, err
}

func (q *queue) DeleteKeys(dbKeys [][]byte) error {
	return q.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(pendingBucket)
		for _, k := range dbKeys {
			if err := b.Delete(k); err != nil {
				return err
			}
		}
		return nil
	})
}

func (q *queue) encrypt(plain []byte) ([]byte, error) {
	block, err := aes.NewCipher(q.key[:])
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}
	return gcm.Seal(nonce, nonce, plain, nil), nil
}

func (q *queue) decrypt(data []byte) ([]byte, error) {
	block, err := aes.NewCipher(q.key[:])
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonceSize := gcm.NonceSize()
	if len(data) < nonceSize {
		return nil, fmt.Errorf("ciphertext too short")
	}
	return gcm.Open(nil, data[:nonceSize], data[nonceSize:], nil)
}

func (q *queue) close() error {
	return q.db.Close()
}
