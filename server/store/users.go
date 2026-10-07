package store

// 用户与会话存储（多用户体系，2026-09-29）。
//
// 设计要点：
//   - 密码仅存 bcrypt 哈希（golang.org/x/crypto/bcrypt，纯 Go），不落明文；
//   - 会话存 token 的 SHA-256 哈希：库泄露也无法伪造出可用 token；
//   - 登录校验「用户不存在」与「密码错误」均返回 ErrAuthFailed，且用户不存在时
//     也执行一次等价 bcrypt 比较（dummyHash），避免响应耗时侧信道区分两种失败；
//   - users 表为空 = 单用户开放模式（api 层据此关闭认证），语义见 userscope.go。

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"golang.org/x/crypto/bcrypt"
)

// ErrConflict 唯一键冲突（如用户名已存在），由 HTTP 层映射为 409。
var ErrConflict = errors.New("conflict")

// ErrAuthFailed 认证失败（用户不存在或密码错误，统一口径）。
var ErrAuthFailed = errors.New("authentication failed")

// dummyHash 「用户不存在」时用于消耗等价 bcrypt 计算量的固定哈希，
// 使两种失败路径的响应耗时接近（包初始化时计算一次）。
var dummyHash = func() []byte {
	h, err := bcrypt.GenerateFromPassword([]byte("grammarchecker-timing-equalizer"), bcrypt.DefaultCost)
	if err != nil {
		panic("store: 生成 dummy bcrypt 哈希失败: " + err.Error())
	}
	return h
}()

// User 用户（users 表）。PasswordHash 不对外序列化（json:"-"）。
type User struct {
	ID           int64  `json:"id"`
	Username     string `json:"username"`
	PasswordHash string `json:"-"`
	IsAdmin      bool   `json:"is_admin"`
	CreatedAt    string `json:"created_at"`
}

// HasUsers 报告是否已存在用户（false = 单用户开放模式）。
func (s *Store) HasUsers() (bool, error) {
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM users`).Scan(&n); err != nil {
		return false, fmt.Errorf("统计用户数失败: %w", err)
	}
	return n > 0, nil
}

// CreateUser 创建用户（密码明文入参，本函数内哈希后落库）。
// 用户名重复返回 ErrConflict；写入后回填 u.ID 与 u.CreatedAt。
func (s *Store) CreateUser(u *User, password string) error {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return fmt.Errorf("生成密码哈希失败: %w", err)
	}
	if u.CreatedAt == "" {
		u.CreatedAt = nowISO()
	}
	// 单连接串行访问下，先查后插足以避免唯一键竞态；
	// 显式预检可把冲突映射为 ErrConflict 而非裸的约束错误。
	var exists int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM users WHERE username = ?`, u.Username).Scan(&exists); err != nil {
		return fmt.Errorf("检查用户名失败: %w", err)
	}
	if exists > 0 {
		return ErrConflict
	}
	admin := 0
	if u.IsAdmin {
		admin = 1
	}
	res, err := s.db.Exec(
		`INSERT INTO users (username, password_hash, is_admin, created_at) VALUES (?, ?, ?, ?)`,
		u.Username, string(hash), admin, u.CreatedAt)
	if err != nil {
		return fmt.Errorf("写入用户失败: %w", err)
	}
	if u.ID, err = res.LastInsertId(); err != nil {
		return fmt.Errorf("获取新用户 ID 失败: %w", err)
	}
	return nil
}

// GetUserByID 按 ID 查用户；未命中返回 ErrNotFound。
func (s *Store) GetUserByID(id int64) (*User, error) {
	row := s.db.QueryRow(
		`SELECT id, username, password_hash, is_admin, created_at FROM users WHERE id = ?`, id)
	return scanUser(row)
}

// ListUsers 用户列表（按 ID 升序，首个用户即管理员排最前）。
func (s *Store) ListUsers() ([]User, error) {
	rows, err := s.db.Query(`SELECT id, username, password_hash, is_admin, created_at FROM users ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("查询用户列表失败: %w", err)
	}
	defer rows.Close()

	items := []User{}
	for rows.Next() {
		var u User
		var admin int
		if err := rows.Scan(&u.ID, &u.Username, &u.PasswordHash, &admin, &u.CreatedAt); err != nil {
			return nil, fmt.Errorf("读取用户失败: %w", err)
		}
		u.IsAdmin = admin == 1
		items = append(items, u)
	}
	return items, rows.Err()
}

// Authenticate 登录校验：用户名 + 密码，成功返回用户。
// 「用户不存在」与「密码错误」统一返回 ErrAuthFailed（口径见文件头注释）。
func (s *Store) Authenticate(username, password string) (*User, error) {
	row := s.db.QueryRow(
		`SELECT id, username, password_hash, is_admin, created_at FROM users WHERE username = ?`, username)
	u, err := scanUser(row)
	if errors.Is(err, ErrNotFound) {
		// 用户不存在：跑等价 bcrypt 比较抹平耗时差异
		_ = bcrypt.CompareHashAndPassword(dummyHash, []byte(password))
		return nil, ErrAuthFailed
	}
	if err != nil {
		return nil, err
	}
	if bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(password)) != nil {
		return nil, ErrAuthFailed
	}
	return u, nil
}

// UpdateUserPassword 重置用户密码（管理员重置 / 本人改密共用）。
// 密码明文入参，本函数内哈希；用户不存在返回 ErrNotFound。
// 同时吊销该用户全部会话：密码轮换后旧会话立即失效（含本人当前会话，
// 前端据此引导重新登录），被盗会话无法靠残留有效期续命。
func (s *Store) UpdateUserPassword(id int64, password string) error {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return fmt.Errorf("生成密码哈希失败: %w", err)
	}
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("开启事务失败: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	res, err := tx.Exec(`UPDATE users SET password_hash = ? WHERE id = ?`, string(hash), id)
	if err != nil {
		return fmt.Errorf("更新密码失败: %w", err)
	}
	if n, err := res.RowsAffected(); err == nil && n == 0 {
		return ErrNotFound
	}
	if _, err := tx.Exec(`DELETE FROM sessions WHERE user_id = ?`, id); err != nil {
		return fmt.Errorf("吊销用户会话失败: %w", err)
	}
	return tx.Commit()
}

// DeleteUser 删除用户及其全部数据（业务数据 + 会话），事务保证原子性。
// 用户不存在返回 ErrNotFound。
func (s *Store) DeleteUser(id int64) error {
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("开启事务失败: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	res, err := tx.Exec(`DELETE FROM users WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("删除用户失败: %w", err)
	}
	if n, err := res.RowsAffected(); err == nil && n == 0 {
		return ErrNotFound
	}
	// 连带清理：会话与 6 张业务表中该用户的行
	if _, err := tx.Exec(`DELETE FROM sessions WHERE user_id = ?`, id); err != nil {
		return fmt.Errorf("清理用户会话失败: %w", err)
	}
	for _, t := range userTables {
		if _, err := tx.Exec("DELETE FROM "+t+" WHERE user_id = ?", id); err != nil {
			return fmt.Errorf("清理用户数据失败(%s): %w", t, err)
		}
	}
	return tx.Commit()
}

// CreateSession 为用户签发会话：返回原始 token（仅此一次可见，交给 Cookie）
// 与过期时间。库内只存 SHA-256 哈希。
func (s *Store) CreateSession(userID int64, ttl time.Duration) (token string, expiresAt time.Time, err error) {
	raw := make([]byte, 32)
	if _, err = rand.Read(raw); err != nil {
		return "", time.Time{}, fmt.Errorf("生成会话 token 失败: %w", err)
	}
	token = hex.EncodeToString(raw)
	sum := sha256.Sum256([]byte(token))
	expiresAt = time.Now().Add(ttl)
	if _, err = s.db.Exec(
		`INSERT INTO sessions (token_hash, user_id, expires_at, created_at) VALUES (?, ?, ?, ?)`,
		hex.EncodeToString(sum[:]), userID, expiresAt.Format(time.RFC3339), nowISO()); err != nil {
		return "", time.Time{}, fmt.Errorf("写入会话失败: %w", err)
	}
	return token, expiresAt, nil
}

// GetUserBySessionToken 校验会话 token（查找哈希 + 过期检查），返回所属用户。
// token 无效 / 过期一律返回 ErrNotFound（过期记录顺手清理）。
func (s *Store) GetUserBySessionToken(token string) (*User, error) {
	sum := sha256.Sum256([]byte(token))
	hash := hex.EncodeToString(sum[:])
	row := s.db.QueryRow(
		`SELECT u.id, u.username, u.password_hash, u.is_admin, u.created_at, s.expires_at
		 FROM sessions s JOIN users u ON u.id = s.user_id
		 WHERE s.token_hash = ?`, hash)
	var u User
	var expStr string
	var admin int
	if err := row.Scan(&u.ID, &u.Username, &u.PasswordHash, &admin, &u.CreatedAt, &expStr); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("查询会话失败: %w", err)
	}
	u.IsAdmin = admin == 1
	exp, err := time.Parse(time.RFC3339, expStr)
	if err != nil {
		return nil, fmt.Errorf("解析会话过期时间失败: %w", err)
	}
	if time.Now().After(exp) {
		_, _ = s.db.Exec(`DELETE FROM sessions WHERE token_hash = ?`, hash)
		return nil, ErrNotFound
	}
	return &u, nil
}

// DeleteSession 注销会话（按 token 哈希删，幂等）。
func (s *Store) DeleteSession(token string) error {
	sum := sha256.Sum256([]byte(token))
	if _, err := s.db.Exec(`DELETE FROM sessions WHERE token_hash = ?`, hex.EncodeToString(sum[:])); err != nil {
		return fmt.Errorf("删除会话失败: %w", err)
	}
	return nil
}

// DeleteExpiredSessions 清理过期会话，返回删除条数（启动时调用一次）。
func (s *Store) DeleteExpiredSessions() (int64, error) {
	res, err := s.db.Exec(`DELETE FROM sessions WHERE expires_at < ?`, nowISO())
	if err != nil {
		return 0, fmt.Errorf("清理过期会话失败: %w", err)
	}
	return res.RowsAffected()
}

// ClaimLegacyData 把单用户时代（user_id = 0）的数据划归指定用户，
// 返回迁移总行数。仅在创建首个用户时调用一次。
func (s *Store) ClaimLegacyData(userID int64) (int64, error) {
	var total int64
	for _, t := range userTables {
		res, err := s.db.Exec("UPDATE "+t+" SET user_id = ? WHERE user_id = 0", userID)
		if err != nil {
			return total, fmt.Errorf("迁移旧数据失败(%s): %w", t, err)
		}
		if n, err := res.RowsAffected(); err == nil {
			total += n
		}
	}
	return total, nil
}

// scanUser 从单行结果扫描用户（GetUserByID / Authenticate 共用）。
func scanUser(row *sql.Row) (*User, error) {
	var u User
	var admin int
	if err := row.Scan(&u.ID, &u.Username, &u.PasswordHash, &admin, &u.CreatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("读取用户失败: %w", err)
	}
	u.IsAdmin = admin == 1
	return &u, nil
}
