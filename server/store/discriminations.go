package store

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

// Discrimination 辨析记录（discriminations 表）。
// 辨析页历史 = 本表按时间倒序，不单独建表（技术文档第 3 节注）。
type Discrimination struct {
	ID           int64  `json:"id"`
	Word         string `json:"word"`
	SynonymsJSON string `json:"synonyms_json"` // 近义词列表（空值序列化为 ""，字段恒存在）
	ResultJSON   string `json:"result_json"`   // 场景辨析结果（空值序列化为 ""，字段恒存在）
	Favorite     bool   `json:"favorite"`      // 收藏标记（POST/DELETE favorite 接口维护，取消收藏不清除记录）
	CreatedAt    string `json:"created_at"`
}

// ListDiscriminations 辨析收藏/历史列表：q 搜索单词、日期 AND 筛选，时间倒序。
// favorite 非 nil 时按收藏状态过滤（nil = 不过滤）。
func (s *Store) ListDiscriminations(q, from, to string, favorite *bool) ([]Discrimination, error) {
	where := []string{"1 = 1"}
	args := []any{}
	if q != "" {
		// 同搜单词与近义词列表（api/readme.md §2.6：搜索单词 / 近义词）
		where = append(where, "(word LIKE ? OR synonyms_json LIKE ?)")
		args = append(args, "%"+q+"%", "%"+q+"%")
	}
	if from != "" {
		where = append(where, "substr(created_at, 1, 10) >= ?")
		args = append(args, from)
	}
	if to != "" {
		where = append(where, "substr(created_at, 1, 10) <= ?")
		args = append(args, to)
	}
	if favorite != nil {
		where = append(where, "favorite = ?")
		if *favorite {
			args = append(args, 1)
		} else {
			args = append(args, 0)
		}
	}

	rows, err := s.db.Query(
		`SELECT id, word, synonyms_json, result_json, favorite, created_at
		 FROM discriminations WHERE `+strings.Join(where, " AND ")+`
		 ORDER BY created_at DESC, id DESC`, args...)
	if err != nil {
		return nil, fmt.Errorf("查询辨析记录失败: %w", err)
	}
	defer rows.Close()

	items := []Discrimination{}
	for rows.Next() {
		var d Discrimination
		if err := rows.Scan(&d.ID, &d.Word, &d.SynonymsJSON, &d.ResultJSON, &d.Favorite, &d.CreatedAt); err != nil {
			return nil, fmt.Errorf("读取辨析记录失败: %w", err)
		}
		items = append(items, d)
	}
	return items, rows.Err()
}

// FindDiscriminationByWord 取某单词最近一次辨析记录（「辨析复用历史记录」开关命中时使用）。
// 未命中返回 ErrNotFound。
func (s *Store) FindDiscriminationByWord(word string) (*Discrimination, error) {
	row := s.db.QueryRow(
		`SELECT id, word, synonyms_json, result_json, favorite, created_at
		 FROM discriminations WHERE word = ?
		 ORDER BY created_at DESC, id DESC LIMIT 1`, word)
	var d Discrimination
	if err := row.Scan(&d.ID, &d.Word, &d.SynonymsJSON, &d.ResultJSON, &d.Favorite, &d.CreatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("查询辨析记录失败: %w", err)
	}
	return &d, nil
}

// InsertDiscrimination 写入辨析记录（/api/discriminate 接通 LLM 后调用）。
func (s *Store) InsertDiscrimination(d *Discrimination) error {
	if d.CreatedAt == "" {
		d.CreatedAt = nowISO()
	}
	res, err := s.db.Exec(
		`INSERT INTO discriminations (word, synonyms_json, result_json, created_at)
		 VALUES (?, ?, ?, ?)`,
		d.Word, d.SynonymsJSON, d.ResultJSON, d.CreatedAt)
	if err != nil {
		return fmt.Errorf("写入辨析记录失败: %w", err)
	}
	if d.ID, err = res.LastInsertId(); err != nil {
		return fmt.Errorf("获取新辨析记录 ID 失败: %w", err)
	}
	return nil
}

// SetDiscriminationFavorite 置位/取消辨析收藏（POST/DELETE favorite 接口）。
// 幂等：重复置位结果一致；记录不存在返回 ErrNotFound。
func (s *Store) SetDiscriminationFavorite(id int64, fav bool) error {
	return s.setFavorite("discriminations", id, fav)
}

// DeleteDiscrimination 按 ID 删除单条辨析记录（仓库·辨析选项卡编辑模式）。
// 记录不存在返回 ErrNotFound。
func (s *Store) DeleteDiscrimination(id int64) error {
	return s.deleteByID("discriminations", id)
}

// DeleteAllDiscriminations 清空辨析历史，返回删除条数。
func (s *Store) DeleteAllDiscriminations() (int64, error) {
	return s.deleteAll("discriminations")
}
