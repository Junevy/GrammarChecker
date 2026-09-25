package store

// comp_infos 表访问：句子成分说明字典（GET /api/comp-info 数据源）。
//
// 内容策略（2026-09-25 人员确认）：说明为固定语法知识，启动时预置 seed，
// 查询时只读 DB、不调 LLM；未收录角色返回 ErrNotFound，由前端回退通用说明。
// 字段名 what / why / how / demo 与前端 compInfo mock 结构一致。

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

// CompInfo 句子成分说明（comp_infos 表，role 为主键）。
type CompInfo struct {
	Role string `json:"role"`  // 成分名（主语、谓语、让步状语从句……）
	What string `json:"what"`  // 是什么：定义与充当词性
	Why  string `json:"why"`   // 有什么用：语法功能
	How  string `json:"how"`   // 怎么用：位置、搭配与常见错误
	Demo string `json:"demo"`  // 示例：英文例句 + 成分点评
}

// GetCompInfo 按角色名精确查询成分说明；未收录返回 ErrNotFound（HTTP 404）。
func (s *Store) GetCompInfo(role string) (*CompInfo, error) {
	role = strings.TrimSpace(role)
	row := s.db.QueryRow(
		`SELECT role, what, why, how, demo FROM comp_infos WHERE role = ?`, role)
	var c CompInfo
	if err := row.Scan(&c.Role, &c.What, &c.Why, &c.How, &c.Demo); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("查询成分说明失败: %w", err)
	}
	return &c, nil
}

// seedCompInfos 预置成分说明 seed。
// INSERT OR REPLACE：文案升级后重启即生效（本表运行期只读，无用户数据可覆盖）。
func (s *Store) seedCompInfos() error {
	for _, c := range compInfoSeeds {
		if _, err := s.db.Exec(
			`INSERT OR REPLACE INTO comp_infos (role, what, why, how, demo)
			 VALUES (?, ?, ?, ?, ?)`,
			c.Role, c.What, c.Why, c.How, c.Demo); err != nil {
			return fmt.Errorf("预置成分说明失败(%s): %w", c.Role, err)
		}
	}
	return nil
}
