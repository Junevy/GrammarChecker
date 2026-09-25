package store

import (
	"database/sql"
	"errors"
	"fmt"
)

// GetAllSettings 读取全部设置项（键值对）。
// 迁移后至少包含预置的 api_key 与 reuse_discrimination_history。
func (s *Store) GetAllSettings() (map[string]string, error) {
	rows, err := s.db.Query(`SELECT key, value FROM settings`)
	if err != nil {
		return nil, fmt.Errorf("读取设置失败: %w", err)
	}
	defer rows.Close()

	items := map[string]string{}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, fmt.Errorf("读取设置失败: %w", err)
		}
		items[k] = v
	}
	return items, rows.Err()
}

// GetSetting 读取单个设置项；键不存在时返回空字符串（与预置空值语义一致）。
func (s *Store) GetSetting(key string) (string, error) {
	var v string
	err := s.db.QueryRow(`SELECT value FROM settings WHERE key = ?`, key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("读取设置 %s 失败: %w", key, err)
	}
	return v, nil
}

// SetSetting 保存设置项（UPSERT）。
func (s *Store) SetSetting(key, value string) error {
	if _, err := s.db.Exec(
		`INSERT INTO settings (key, value) VALUES (?, ?)
		 ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, value); err != nil {
		return fmt.Errorf("保存设置 %s 失败: %w", key, err)
	}
	return nil
}
