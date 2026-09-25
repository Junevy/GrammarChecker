package store

import (
	"fmt"
	"strings"
)

// WrongWord 错词本条目（wrong_words 表，对应文档 6.2「先前错误」）。
type WrongWord struct {
	ID                int64  `json:"id"`
	Word              string `json:"word"`
	ErrorType         string `json:"error_type"`
	OriginalSentence  string `json:"original_sentence"`
	CorrectedSentence string `json:"corrected_sentence"`
	AnalysisJSON      string `json:"analysis_json"` // 结构分析缓存（空值序列化为 ""，字段恒存在，便于前端取值）
	CreatedAt         string `json:"created_at"`
}

// WrongWordFilter 先前错误列表筛选条件：类型与日期为 AND 组合（文档 6.2）。
type WrongWordFilter struct {
	Query string // q：按单词模糊搜索
	Type  string // type：错误类型精确匹配，空为全部
	From  string // from：日期下界（含），格式 2006-01-02
	To    string // to：日期上界（含）
}

// ListWrongWords 查询先前错误列表，按创建时间倒序。
func (s *Store) ListWrongWords(f WrongWordFilter) ([]WrongWord, error) {
	where := []string{"1 = 1"}
	args := []any{}
	if f.Query != "" {
		where = append(where, "word LIKE ?")
		args = append(args, "%"+f.Query+"%")
	}
	if f.Type != "" {
		where = append(where, "error_type = ?")
		args = append(args, f.Type)
	}
	if f.From != "" {
		// created_at 为 RFC3339 全时间串，截取前 10 位（yyyy-mm-dd）参与日期比较
		where = append(where, "substr(created_at, 1, 10) >= ?")
		args = append(args, f.From)
	}
	if f.To != "" {
		where = append(where, "substr(created_at, 1, 10) <= ?")
		args = append(args, f.To)
	}

	rows, err := s.db.Query(
		`SELECT id, word, error_type, original_sentence, corrected_sentence, analysis_json, created_at
		 FROM wrong_words WHERE `+strings.Join(where, " AND ")+`
		 ORDER BY created_at DESC, id DESC`, args...)
	if err != nil {
		return nil, fmt.Errorf("查询先前错误失败: %w", err)
	}
	defer rows.Close()

	items := []WrongWord{}
	for rows.Next() {
		var w WrongWord
		if err := rows.Scan(&w.ID, &w.Word, &w.ErrorType, &w.OriginalSentence,
			&w.CorrectedSentence, &w.AnalysisJSON, &w.CreatedAt); err != nil {
			return nil, fmt.Errorf("读取先前错误失败: %w", err)
		}
		items = append(items, w)
	}
	return items, rows.Err()
}

// InsertWrongWord 写入错词本（检查页「加入错词本」5s 倒计时结束后调用）。
// 写入后回填 w.ID 与 w.CreatedAt。
func (s *Store) InsertWrongWord(w *WrongWord) error {
	if w.CreatedAt == "" {
		w.CreatedAt = nowISO()
	}
	res, err := s.db.Exec(
		`INSERT INTO wrong_words (word, error_type, original_sentence, corrected_sentence, analysis_json, created_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		w.Word, w.ErrorType, w.OriginalSentence, w.CorrectedSentence, w.AnalysisJSON, w.CreatedAt)
	if err != nil {
		return fmt.Errorf("写入错词本失败: %w", err)
	}
	if w.ID, err = res.LastInsertId(); err != nil {
		return fmt.Errorf("获取新错词 ID 失败: %w", err)
	}
	return nil
}

// DeleteWrongWord 按 ID 删除错词；未命中返回 ErrNotFound。
func (s *Store) DeleteWrongWord(id int64) error {
	return s.deleteByID("wrong_words", id)
}
