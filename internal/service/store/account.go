// Package store 账号持久化存储
// 对齐 frontend/src/lib/passwordCrypto.ts encryptAccounts/decryptAccounts
// 和 frontend/src/lib/serverUtils.ts readAccounts
package store

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/wabisabi926/faststrm/internal/model"
	"github.com/wabisabi926/faststrm/internal/service/pwdcrypto"
	"github.com/wabisabi926/faststrm/pkg/logger"
)

// 敏感字段列表（对齐 TS ACCOUNT_ENCRYPTED_FIELDS）
var accountEncryptedFields = []string{"cookie", "password"}

// AccountStore 账号存储（JSON 文件 + AES-256-GCM 加密）
type AccountStore struct {
	salt string
	path string
}

// NewAccountStore 创建账号存储
func NewAccountStore(salt, configDir string) *AccountStore {
	return &AccountStore{
		salt: salt,
		path: filepath.Join(configDir, "account.json"),
	}
}

// ReadAccounts 读取账号列表（自动解密敏感字段）
func (s *AccountStore) ReadAccounts() ([]model.AccountInfo, error) {
	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return []model.AccountInfo{}, nil
		}
		return nil, err
	}

	// 先解析为通用 map 以便解密
	var raw []map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		// 可能是空 JSON 对象 {}
		if string(data) == "{}" {
			return []model.AccountInfo{}, nil
		}
		return nil, err
	}

	// 解密敏感字段
	for _, acc := range raw {
		for _, field := range accountEncryptedFields {
			if val, ok := acc[field].(string); ok && pwdcrypto.IsEncrypted(val) {
				acc[field] = pwdcrypto.DecryptCredential(s.salt, val)
			}
		}
	}

	// 重新序列化再解析为目标类型
	out, err := json.Marshal(raw)
	if err != nil {
		return nil, err
	}
	var accounts []model.AccountInfo
	if err := json.Unmarshal(out, &accounts); err != nil {
		return nil, err
	}
	if accounts == nil {
		accounts = []model.AccountInfo{}
	}
	return accounts, nil
}

// WriteAccounts 写入账号列表（自动加密敏感字段）
func (s *AccountStore) WriteAccounts(accounts []model.AccountInfo) error {
	// 先序列化为通用 map 以便加密
	data, err := json.Marshal(accounts)
	if err != nil {
		return err
	}
	var raw []map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}

	// 加密敏感字段
	for _, acc := range raw {
		for _, field := range accountEncryptedFields {
			if val, ok := acc[field].(string); ok && val != "" && !pwdcrypto.IsEncrypted(val) {
				encrypted, err := pwdcrypto.EncryptCredential(s.salt, val)
				if err != nil {
					logger.S().Warnf("encrypt account field %s failed: %v", field, err)
					continue
				}
				acc[field] = encrypted
			}
		}
	}

	out, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.path, out, 0644)
}
