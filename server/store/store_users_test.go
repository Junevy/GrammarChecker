package store

// 多用户数据层测试（2026-09-29）：
// 用户视图隔离（跨用户不可见/不可改）、旧数据认领、会话生命周期、认证口径。

import (
	"testing"
	"time"
)

// newScopedStores 建临时库并创建 admin / bob 两个用户，返回根 Store 与对应用户。
func newScopedStores(t *testing.T) (*Store, *User, *User) {
	t.Helper()
	s := newTestStore(t)
	admin := &User{Username: "admin", IsAdmin: true}
	if err := s.CreateUser(admin, "admin-pass-6"); err != nil {
		t.Fatalf("创建 admin 失败: %v", err)
	}
	bob := &User{Username: "Bob", IsAdmin: false} // 混合大小写：验证 NOCASE 唯一
	if err := s.CreateUser(bob, "bob-pass-6"); err != nil {
		t.Fatalf("创建 bob 失败: %v", err)
	}
	return s, admin, bob
}

// TestUserScopeIsolation ForUser 视图的数据隔离：A 写的数据 B 看不见、改不到、删不掉。
func TestUserScopeIsolation(t *testing.T) {
	s, admin, bob := newScopedStores(t)
	aRepo := s.ForUser(admin.ID)
	bRepo := s.ForUser(bob.ID)

	// admin 写入各类数据
	w := &WrongWord{Word: "have went", ErrorType: "主谓一致"}
	if err := aRepo.InsertWrongWord(w); err != nil {
		t.Fatalf("写入错词失败: %v", err)
	}
	sent := &Sentence{English: "I received your letter."}
	if err := aRepo.InsertSentence(sent); err != nil {
		t.Fatalf("写入例句失败: %v", err)
	}
	d := &Discrimination{Word: "receive", ResultJSON: `{"sub":"x"}`}
	if err := aRepo.InsertDiscrimination(d); err != nil {
		t.Fatalf("写入辨析失败: %v", err)
	}
	e := &Expression{Chinese: "我收到了你的信。"}
	if err := aRepo.InsertExpression(e); err != nil {
		t.Fatalf("写入表达失败: %v", err)
	}
	h := &CheckHistory{Sentence: "I have went.", ErrorCount: 1, ResultJSON: `{"errors":[{"type":"主谓一致"}]}`}
	if err := aRepo.InsertCheckHistory(h); err != nil {
		t.Fatalf("写入检查历史失败: %v", err)
	}

	// bob 的列表全部为空
	if items, err := bRepo.ListWrongWords(WrongWordFilter{}); err != nil || len(items) != 0 {
		t.Fatalf("bob 不应看到 admin 的错词, n=%d err=%v", len(items), err)
	}
	if items, _ := bRepo.ListSentences(""); len(items) != 0 {
		t.Fatalf("bob 不应看到 admin 的例句, n=%d", len(items))
	}
	if items, _ := bRepo.ListDiscriminations("", "", "", nil); len(items) != 0 {
		t.Fatalf("bob 不应看到 admin 的辨析, n=%d", len(items))
	}
	if items, _ := bRepo.ListExpressions(0, nil); len(items) != 0 {
		t.Fatalf("bob 不应看到 admin 的表达, n=%d", len(items))
	}
	if items, _ := bRepo.ListCheckHistory(0); len(items) != 0 {
		t.Fatalf("bob 不应看到 admin 的检查历史, n=%d", len(items))
	}
	if items, _ := bRepo.ListTrendPoints("2000-01-01", ""); len(items) != 0 {
		t.Fatalf("bob 不应看到 admin 的趋势, n=%d", len(items))
	}

	// 跨用户删除/收藏/查找：与「不存在」同响应（防 id 探测）
	if err := bRepo.DeleteWrongWord(w.ID); err != ErrNotFound {
		t.Fatalf("跨用户删除应 ErrNotFound, got %v", err)
	}
	if err := bRepo.SetDiscriminationFavorite(d.ID, true); err != ErrNotFound {
		t.Fatalf("跨用户收藏应 ErrNotFound, got %v", err)
	}
	if _, err := bRepo.FindDiscriminationByWord("receive"); err != ErrNotFound {
		t.Fatalf("跨用户按词查找应 ErrNotFound, got %v", err)
	}
	// bob 清空自己的（空）数据不得影响 admin
	if n, err := bRepo.DeleteAllDiscriminations(); err != nil || n != 0 {
		t.Fatalf("bob 清空辨析应 0 条, n=%d err=%v", n, err)
	}
	if items, _ := aRepo.ListDiscriminations("", "", "", nil); len(items) != 1 {
		t.Fatalf("admin 数据不得被 bob 清空波及, n=%d", len(items))
	}

	// admin 自己的视图可见、可删
	if items, _ := aRepo.ListWrongWords(WrongWordFilter{}); len(items) != 1 {
		t.Fatalf("admin 应看到自己的错词, n=%d", len(items))
	}
	if err := aRepo.DeleteWrongWord(w.ID); err != nil {
		t.Fatalf("本人删除应成功: %v", err)
	}
}

// TestClaimLegacyData 单用户时代（user_id=0）的数据在首个用户创建后划归该用户。
func TestClaimLegacyData(t *testing.T) {
	s := newTestStore(t)

	// 模拟旧库：经全局方法（uidAll → user_id=0）写入
	if err := s.InsertWrongWord(&WrongWord{Word: "old"}); err != nil {
		t.Fatalf("写入旧数据失败: %v", err)
	}
	if err := s.InsertCheckHistory(&CheckHistory{Sentence: "old"}); err != nil {
		t.Fatalf("写入旧历史失败: %v", err)
	}

	u := &User{Username: "first", IsAdmin: true}
	if err := s.CreateUser(u, "first-pass"); err != nil {
		t.Fatalf("创建首个用户失败: %v", err)
	}
	n, err := s.ClaimLegacyData(u.ID)
	if err != nil {
		t.Fatalf("认领旧数据失败: %v", err)
	}
	if n != 2 {
		t.Fatalf("应迁移 2 条, got %d", n)
	}

	// 认领后：首个用户可见；user_id=0 视图（模拟未登录单用户模式）为空
	first := s.ForUser(u.ID)
	if items, _ := first.ListWrongWords(WrongWordFilter{}); len(items) != 1 {
		t.Fatalf("首个用户应看到认领的错词, n=%d", len(items))
	}
	if items, _ := s.ForUser(0).ListCheckHistory(0); len(items) != 0 {
		t.Fatalf("认领后 uid=0 不应再有数据, n=%d", len(items))
	}
}

// TestSessionLifecycle 会话：签发 → 校验 → 注销；过期会话拒绝。
func TestSessionLifecycle(t *testing.T) {
	s, admin, _ := newScopedStores(t)

	token, exp, err := s.CreateSession(admin.ID, time.Hour)
	if err != nil {
		t.Fatalf("签发会话失败: %v", err)
	}
	if exp.Before(time.Now()) {
		t.Fatal("会话过期时间应在未来")
	}
	u, err := s.GetUserBySessionToken(token)
	if err != nil || u.Username != "admin" {
		t.Fatalf("token 应换取 admin, got %v err=%v", u, err)
	}

	// 过期会话：签发即过期 → 拒绝并顺手清理
	stale, _, err := s.CreateSession(admin.ID, -time.Minute)
	if err != nil {
		t.Fatalf("签发过期会话失败: %v", err)
	}
	if _, err := s.GetUserBySessionToken(stale); err != ErrNotFound {
		t.Fatalf("过期会话应拒绝, got %v", err)
	}

	// 注销后失效
	if err := s.DeleteSession(token); err != nil {
		t.Fatalf("注销失败: %v", err)
	}
	if _, err := s.GetUserBySessionToken(token); err != ErrNotFound {
		t.Fatalf("注销后应拒绝, got %v", err)
	}
	// 注销幂等
	if err := s.DeleteSession(token); err != nil {
		t.Fatalf("重复注销应幂等: %v", err)
	}
}

// TestAuthenticateCaseInsensitive 用户名大小写不敏感登录；
// 密码错误与用户不存在统一 ErrAuthFailed。
func TestAuthenticateCaseInsensitive(t *testing.T) {
	s, admin, _ := newScopedStores(t)

	u, err := s.Authenticate("ADMIN", "admin-pass-6")
	if err != nil || u.ID != admin.ID {
		t.Fatalf("大小写不敏感登录应成功, got %v err=%v", u, err)
	}
	if _, err := s.Authenticate("admin", "wrong"); err != ErrAuthFailed {
		t.Fatalf("密码错误应 ErrAuthFailed, got %v", err)
	}
	if _, err := s.Authenticate("ghost", "whatever"); err != ErrAuthFailed {
		t.Fatalf("用户不存在应 ErrAuthFailed, got %v", err)
	}
}

// TestDeleteUserCascade 删除用户连带清理其业务数据与会话。
func TestDeleteUserCascade(t *testing.T) {
	s, admin, bob := newScopedStores(t)
	aRepo := s.ForUser(admin.ID)

	if err := aRepo.InsertSentence(&Sentence{English: "mine"}); err != nil {
		t.Fatalf("写入失败: %v", err)
	}
	if _, _, err := s.CreateSession(admin.ID, time.Hour); err != nil {
		t.Fatalf("签发会话失败: %v", err)
	}

	if err := s.DeleteUser(admin.ID); err != nil {
		t.Fatalf("删除用户失败: %v", err)
	}
	if _, err := s.GetUserByID(admin.ID); err != ErrNotFound {
		t.Fatalf("用户应已删除, got %v", err)
	}
	if items, _ := s.ForUser(bob.ID).ListSentences(""); len(items) != 0 {
		t.Fatalf("被删用户的数据应级联清理, n=%d", len(items))
	}
	if n, _ := s.DeleteExpiredSessions(); n != 0 {
		t.Fatalf("被删用户的会话应级联清理, n=%d", n)
	}
}
