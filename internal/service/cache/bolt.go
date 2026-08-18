// Package cache Bolt KV 缓存（对应 TS SimpleCache.ts）
package cache

import (
	"fmt"
	"path/filepath"
	"sync"
	"time"

	bolt "go.etcd.io/bbolt"
)

// ==================== 全局单例 ====================

var (
	inst *bolt.DB
	lock sync.Mutex
)

// 默认 bucket
const defaultBucket = "faststrm"

// Open 打开/获取 Bolt DB。全局单例，线程安全。
func Open(cacheDir string) (*bolt.DB, error) {
	lock.Lock()
	defer lock.Unlock()
	if inst != nil {
		return inst, nil
	}
	file := filepath.Join(cacheDir, "cache.bolt")
	db, err := bolt.Open(file, 0644, &bolt.Options{Timeout: 5 * time.Second})
	if err != nil {
		return nil, fmt.Errorf("bolt open: %w", err)
	}
	// 确保默认 bucket 存在
	err = db.Update(func(tx *bolt.Tx) error {
		_, berr := tx.CreateBucketIfNotExists([]byte(defaultBucket))
		return berr
	})
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	inst = db
	return inst, nil
}

// Close 关闭 Bolt
func Close() {
	lock.Lock()
	defer lock.Unlock()
	if inst != nil {
		_ = inst.Close()
		inst = nil
	}
}

// ==================== 便捷 API（对应 SimpleCache.ts） ====================

// Get 读 key，不存在返回 (nil, nil)
func Get(db *bolt.DB, key string) ([]byte, error) {
	var val []byte
	err := db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(defaultBucket))
		if b == nil {
			return nil
		}
		v := b.Get([]byte(key))
		if v != nil {
			val = append([]byte(nil), v...) // 拷贝，避免事务结束后失效
		}
		return nil
	})
	return val, err
}

// Set 写 key/value
func Set(db *bolt.DB, key string, value []byte) error {
	return db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(defaultBucket))
		if b == nil {
			var err error
			b, err = tx.CreateBucketIfNotExists([]byte(defaultBucket))
			if err != nil {
				return err
			}
		}
		return b.Put([]byte(key), value)
	})
}

// Delete 删除 key
func Delete(db *bolt.DB, key string) error {
	return db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(defaultBucket))
		if b == nil {
			return nil
		}
		return b.Delete([]byte(key))
	})
}

// Exists 判断 key 是否存在
func Exists(db *bolt.DB, key string) (bool, error) {
	found := false
	err := db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(defaultBucket))
		if b == nil {
			return nil
		}
		if v := b.Get([]byte(key)); v != nil {
			found = true
		}
		return nil
	})
	return found, err
}
