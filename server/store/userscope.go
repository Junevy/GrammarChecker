package store

import "time"

// userStore：绑定到具体用户的 Repository 视图（装饰器模式，2026-09-29）。
//
// api 层只依赖 Repository 接口；Handler 在每个请求开头通过
// h.store.ForUser(currentUser(r)) 取得本视图，后续取数全部落在当前用户的
// 数据范围内（SQL 注入 user_id 条目），接口签名与全局实现完全一致——
// 换库或调整隔离策略时 api 层零改动。
//
// 归属口径：
//   - 业务数据（错词/例句/辨析/表达/检查历史）：按 user_id 严格隔离，
//     跨用户的读写、删除、收藏一律不可见（删除/收藏未命中返回 ErrNotFound，
//     与「记录不存在」同响应，避免 id 探测）；
//   - settings（API KEY、复用开关）与 comp_infos（成分字典）：全局共用，
//     原样透传；
//   - uid = uidAll（-1）仅限 *Store 公开方法（测试 / CLI 场景）使用，
//     表示不过滤；api 层永远拿到 uid >= 0 的视图。

// uidAll 内部通配：不过滤用户（*Store 全局方法专用）。
const uidAll = int64(-1)

// userTables 带 user_id 列的 6 张业务表（迁移索引、认领旧数据、删用户共用）。
var userTables = []string{
	"wrong_words", "sentences", "discriminations", "expressions", "check_history",
}

// ForUser 返回绑定 uid 的用户视图。
func (s *Store) ForUser(uid int64) Repository { return &userStore{s: s, uid: uid} }

// ForUser 视图上再绑定：等价于在根 Store 上重新取视图（不叠加隔离条件）。
func (u *userStore) ForUser(uid int64) Repository { return u.s.ForUser(uid) }

// userStore 编译期断言：视图必须满足同一契约。
var _ Repository = (*userStore)(nil)

type userStore struct {
	s   *Store
	uid int64
}

// ---- 错词本 ----

func (u *userStore) ListWrongWords(f WrongWordFilter) ([]WrongWord, error) {
	return u.s.listWrongWords(f, u.uid)
}
func (u *userStore) InsertWrongWord(w *WrongWord) error { return u.s.insertWrongWord(w, u.uid) }
func (u *userStore) DeleteWrongWord(id int64) error     { return u.s.deleteByIDFor("wrong_words", id, u.uid) }

// ---- 例句仓库 ----

func (u *userStore) ListSentences(q string) ([]Sentence, error) {
	return u.s.listSentences(q, u.uid)
}
func (u *userStore) InsertSentence(x *Sentence) error { return u.s.insertSentence(x, u.uid) }
func (u *userStore) DeleteSentence(id int64) error    { return u.s.deleteByIDFor("sentences", id, u.uid) }

// ---- 辨析历史与收藏 ----

func (u *userStore) ListDiscriminations(q, from, to string, favorite *bool) ([]Discrimination, error) {
	return u.s.listDiscriminations(q, from, to, favorite, u.uid)
}
func (u *userStore) FindDiscriminationByWord(word string) (*Discrimination, error) {
	return u.s.findDiscriminationByWord(word, u.uid)
}
func (u *userStore) InsertDiscrimination(d *Discrimination) error {
	return u.s.insertDiscrimination(d, u.uid)
}
func (u *userStore) SetDiscriminationFavorite(id int64, fav bool) error {
	return u.s.setFavoriteFor("discriminations", id, u.uid, fav)
}
func (u *userStore) DeleteDiscrimination(id int64) error {
	return u.s.deleteByIDFor("discriminations", id, u.uid)
}
func (u *userStore) DeleteAllDiscriminations() (int64, error) {
	return u.s.deleteAllFor("discriminations", u.uid)
}

// ---- 表达历史与收藏 ----

func (u *userStore) ListExpressions(limit int, favorite *bool) ([]Expression, error) {
	return u.s.listExpressions(limit, favorite, u.uid)
}
func (u *userStore) InsertExpression(x *Expression) error { return u.s.insertExpression(x, u.uid) }
func (u *userStore) SetExpressionFavorite(id int64, fav bool) error {
	return u.s.setFavoriteFor("expressions", id, u.uid, fav)
}
func (u *userStore) DeleteAllExpressions() (int64, error) {
	return u.s.deleteAllFor("expressions", u.uid)
}

// ---- 检查历史与趋势 ----

func (u *userStore) ListCheckHistory(limit int) ([]CheckHistory, error) {
	return u.s.listCheckHistory(limit, u.uid)
}
func (u *userStore) InsertCheckHistory(x *CheckHistory) error {
	return u.s.insertCheckHistory(x, u.uid)
}
func (u *userStore) DeleteAllCheckHistory() (int64, error) {
	return u.s.deleteAllFor("check_history", u.uid)
}
func (u *userStore) ListTrendPoints(from, errType string) ([]TrendPoint, error) {
	return u.s.listTrendPoints(from, errType, u.uid)
}

// ---- 设置与成分字典：全局共用，原样透传 ----

func (u *userStore) GetAllSettings() (map[string]string, error) { return u.s.GetAllSettings() }
func (u *userStore) GetSetting(key string) (string, error)      { return u.s.GetSetting(key) }
func (u *userStore) SetSetting(key, value string) error         { return u.s.SetSetting(key, value) }
func (u *userStore) GetCompInfo(role string) (*CompInfo, error) { return u.s.GetCompInfo(role) }

// Close 透传关闭（视图不持有独立连接；api 层生命周期由根 Store 管理）。
func (u *userStore) Close() error { return u.s.Close() }

// ---- 用户与会话管理：全局语义透传（api 层只在根 Store 上调用，不经过视图） ----

func (u *userStore) HasUsers() (bool, error) { return u.s.HasUsers() }
func (u *userStore) CreateUser(x *User, password string) error {
	return u.s.CreateUser(x, password)
}
func (u *userStore) GetUserByID(id int64) (*User, error)  { return u.s.GetUserByID(id) }
func (u *userStore) ListUsers() ([]User, error)           { return u.s.ListUsers() }
func (u *userStore) Authenticate(username, password string) (*User, error) {
	return u.s.Authenticate(username, password)
}
func (u *userStore) UpdateUserPassword(id int64, password string) error {
	return u.s.UpdateUserPassword(id, password)
}
func (u *userStore) DeleteUser(id int64) error { return u.s.DeleteUser(id) }
func (u *userStore) CreateSession(userID int64, ttl time.Duration) (string, time.Time, error) {
	return u.s.CreateSession(userID, ttl)
}
func (u *userStore) GetUserBySessionToken(token string) (*User, error) {
	return u.s.GetUserBySessionToken(token)
}
func (u *userStore) DeleteSession(token string) error { return u.s.DeleteSession(token) }
func (u *userStore) DeleteExpiredSessions() (int64, error) {
	return u.s.DeleteExpiredSessions()
}
func (u *userStore) ClaimLegacyData(userID int64) (int64, error) {
	return u.s.ClaimLegacyData(userID)
}
