// Package store 提供 GrammarChecker 的 SQLite 数据访问层。
//
// 设计要点：
//   - 首次启动幂等建表：技术文档第 3 节的 6 张业务表（含索引）
//     + 成分说明字典表 comp_infos（运行期只读，内容来自 seed 预置）；
//   - 时间字段统一 TEXT（ISO 8601，本地时区）；
//   - 驱动 modernc.org/sqlite（纯 Go、免 CGO，技术文档第 1/2 节推荐）；
//   - SQLite 为单写者模型：连接池上限 1 + busy_timeout，规避并发写锁冲突。
package store

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite" // 纯 Go SQLite 驱动（免 CGO，利于单文件交叉编译）
)

// ErrNotFound 表示按 ID / 条件未找到记录，由 HTTP 层映射为 404。
var ErrNotFound = errors.New("record not found")

// Store 封装 SQLite 连接，所有仓储方法挂在其上。
type Store struct {
	db *sql.DB
}

// Open 打开（必要时创建）数据库并完成建表迁移。
//
// dbPath 为空时默认「可执行文件同目录 / grammar.db」，
// 对应验收口径：卸载仅删文件即数据全清（技术文档第 9 节）。
func Open(dbPath string) (*Store, error) {
	if dbPath == "" {
		exe, err := os.Executable()
		if err != nil {
			return nil, fmt.Errorf("定位可执行文件失败: %w", err)
		}
		dbPath = filepath.Join(filepath.Dir(exe), "grammar.db")
	}
	// busy_timeout：写锁短暂占用时等待而非立即报 database is locked；
	// WAL：读写不互斥，提升本机并发体验。
	dsn := fmt.Sprintf("file:%s?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)", filepath.ToSlash(dbPath))
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("打开数据库失败: %w", err)
	}
	// 单连接串行化全部访问：本地单用户场景吞吐足够，可彻底规避写锁冲突。
	db.SetMaxOpenConns(1)

	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

// Close 关闭底层连接。
func (s *Store) Close() error { return s.db.Close() }

// nowISO 返回本地时区 ISO 8601 时间串（时间字段统一 TEXT 存储口径）。
func nowISO() string { return time.Now().Format(time.RFC3339) }

// migrate 幂等建表（技术文档第 3 节）并预置设置项。
func (s *Store) migrate() error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS wrong_words (
			id                 INTEGER PRIMARY KEY AUTOINCREMENT,
			word               TEXT NOT NULL,
			error_type         TEXT NOT NULL,
			original_sentence  TEXT NOT NULL DEFAULT '',
			corrected_sentence TEXT NOT NULL DEFAULT '',
			analysis_json      TEXT NOT NULL DEFAULT '',
			created_at         TEXT NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_wrong_words_type ON wrong_words (error_type)`,
		`CREATE INDEX IF NOT EXISTS idx_wrong_words_created_at ON wrong_words (created_at)`,

		`CREATE TABLE IF NOT EXISTS sentences (
			id            INTEGER PRIMARY KEY AUTOINCREMENT,
			english       TEXT NOT NULL,
			chinese       TEXT NOT NULL DEFAULT '',
			source        TEXT NOT NULL DEFAULT 'manual',
			analysis_json TEXT NOT NULL DEFAULT '',
			created_at    TEXT NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_sentences_created_at ON sentences (created_at)`,

		`CREATE TABLE IF NOT EXISTS discriminations (
			id            INTEGER PRIMARY KEY AUTOINCREMENT,
			word          TEXT NOT NULL,
			synonyms_json TEXT NOT NULL DEFAULT '',
			result_json   TEXT NOT NULL DEFAULT '',
			favorite      INTEGER NOT NULL DEFAULT 0,
			created_at    TEXT NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_discriminations_word ON discriminations (word)`,
		`CREATE INDEX IF NOT EXISTS idx_discriminations_created_at ON discriminations (created_at)`,

		`CREATE TABLE IF NOT EXISTS expressions (
			id            INTEGER PRIMARY KEY AUTOINCREMENT,
			chinese       TEXT NOT NULL,
			recommended   TEXT NOT NULL DEFAULT '',
			variants_json TEXT NOT NULL DEFAULT '',
			favorite      INTEGER NOT NULL DEFAULT 0,
			created_at    TEXT NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_expressions_created_at ON expressions (created_at)`,

		`CREATE TABLE IF NOT EXISTS check_history (
			id          INTEGER PRIMARY KEY AUTOINCREMENT,
			sentence    TEXT NOT NULL,
			error_count INTEGER NOT NULL DEFAULT 0,
			result_json TEXT NOT NULL DEFAULT '',
			created_at  TEXT NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_check_history_created_at ON check_history (created_at)`,

		`CREATE TABLE IF NOT EXISTS settings (
			key   TEXT PRIMARY KEY,
			value TEXT NOT NULL DEFAULT ''
		)`,

		// 成分说明字典表（GET /api/comp-info 数据源）：建表后由 seedCompInfos 预置内容，运行期只读
		`CREATE TABLE IF NOT EXISTS comp_infos (
			role TEXT PRIMARY KEY,
			what TEXT NOT NULL DEFAULT '',
			why  TEXT NOT NULL DEFAULT '',
			how  TEXT NOT NULL DEFAULT '',
			demo TEXT NOT NULL DEFAULT ''
		)`,

		// 预置设置项（技术文档第 3 节）：api_key 本地密钥；辨析复用历史开关默认开
		`INSERT OR IGNORE INTO settings (key, value) VALUES ('api_key', '')`,
		`INSERT OR IGNORE INTO settings (key, value) VALUES ('reuse_discrimination_history', '1')`,
	}
	for _, stmt := range stmts {
		if _, err := s.db.Exec(stmt); err != nil {
			return fmt.Errorf("建表/预置失败: %w（语句: %s）", err, stmt)
		}
	}
	// 老库升级：favorite 列（收藏持久化，2026-09-25 新增）；新库由上方 DDL 直接建列
	if err := s.ensureColumn("discriminations", "favorite INTEGER NOT NULL DEFAULT 0"); err != nil {
		return err
	}
	if err := s.ensureColumn("expressions", "favorite INTEGER NOT NULL DEFAULT 0"); err != nil {
		return err
	}
	// 预置成分说明 seed（OR REPLACE：文案升级后重启即生效）
	return s.seedCompInfos()
}

// ensureColumn 老库升级辅助：目标表缺列时补列（幂等）。
// table 与 colDDL 仅来自本包内固定的字面量，无注入风险。
func (s *Store) ensureColumn(table, colDDL string) error {
	col := strings.SplitN(colDDL, " ", 2)[0]
	rows, err := s.db.Query(fmt.Sprintf("PRAGMA table_info(%s)", table))
	if err != nil {
		return fmt.Errorf("读取表结构失败(%s): %w", table, err)
	}
	exists := false
	for rows.Next() {
		var (
			cid, notNull, pk int
			name, typ        string
			dflt             any
		)
		if err := rows.Scan(&cid, &name, &typ, &notNull, &dflt, &pk); err != nil {
			_ = rows.Close()
			return fmt.Errorf("读取表结构失败(%s): %w", table, err)
		}
		if name == col {
			exists = true
			break
		}
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return fmt.Errorf("读取表结构失败(%s): %w", table, err)
	}
	_ = rows.Close()
	if exists {
		return nil
	}
	if _, err := s.db.Exec(fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s", table, colDDL)); err != nil {
		return fmt.Errorf("补列失败(%s): %w", col, err)
	}
	return nil
}

// deleteByID 按 ID 删除记录；未命中返回 ErrNotFound。
// table 仅来自本包内固定的表名字面量，无注入风险。
func (s *Store) deleteByID(table string, id int64) error {
	res, err := s.db.Exec("DELETE FROM "+table+" WHERE id = ?", id)
	if err != nil {
		return fmt.Errorf("删除记录失败: %w", err)
	}
	if n, err := res.RowsAffected(); err == nil && n == 0 {
		return ErrNotFound
	}
	return nil
}

// setFavorite 置位/清除指定记录的收藏标记；未命中返回 ErrNotFound。
// table 仅来自本包内固定的表名字面量，无注入风险。
func (s *Store) setFavorite(table string, id int64, fav bool) error {
	res, err := s.db.Exec("UPDATE "+table+" SET favorite = ? WHERE id = ?", fav, id)
	if err != nil {
		return fmt.Errorf("更新收藏状态失败: %w", err)
	}
	if n, err := res.RowsAffected(); err == nil && n == 0 {
		return ErrNotFound
	}
	return nil
}

// deleteAll 清空表，返回删除条数。table 仅来自本包内固定的表名字面量。
func (s *Store) deleteAll(table string) (int64, error) {
	res, err := s.db.Exec("DELETE FROM " + table)
	if err != nil {
		return 0, fmt.Errorf("清空表失败: %w", err)
	}
	return res.RowsAffected()
}
