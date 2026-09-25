package store

// Repository 数据访问的抽象契约：api 层（server/api）只依赖本接口，
// 不感知底层是哪种数据库。当前唯一实现为 *Store（SQLite）；
// 后续若更换 MySQL / PostgreSQL 等主流数据库，只需按同一契约新增实现，
// 并在装配点（server/main.go 的 store.Open 调用处）替换，api 层零改动。
//
// 语义约定（各实现必须保持一致，否则 api 层行为会漂移）：
//   - 未找到记录时返回 ErrNotFound（api 层统一映射为 404 not_found）；
//   - 其余错误原样返回（api 层映射为 500 db_error）；
//   - Insert* 方法通过指针参数回填自增 ID（如 x.ID），供收藏等接口使用；
//   - 时间字段统一 TEXT（RFC3339 本地时区），favorite 以 bool 表达；
//   - 列表恒返回非 nil 切片（空结果为空切片），并按 created_at 倒序；
//   - favorite 过滤参数 *bool：nil=不过滤，true=仅收藏，false=仅未收藏。
//
// 注意：本接口刻意不暴露 *sql.DB 与驱动细节；方言差异（日期过滤函数、
// UPSERT 写法、自增主键语义、连接池策略）由各实现自行消化。
type Repository interface {
	Close() error

	// ---- 错词本 ----
	ListWrongWords(f WrongWordFilter) ([]WrongWord, error)
	InsertWrongWord(w *WrongWord) error
	DeleteWrongWord(id int64) error

	// ---- 例句仓库 ----
	ListSentences(q string) ([]Sentence, error)
	InsertSentence(x *Sentence) error
	DeleteSentence(id int64) error

	// ---- 辨析历史与收藏 ----
	ListDiscriminations(q, from, to string, favorite *bool) ([]Discrimination, error)
	FindDiscriminationByWord(word string) (*Discrimination, error)
	InsertDiscrimination(d *Discrimination) error
	SetDiscriminationFavorite(id int64, fav bool) error
	DeleteDiscrimination(id int64) error
	DeleteAllDiscriminations() (int64, error)

	// ---- 表达历史与收藏 ----
	ListExpressions(limit int, favorite *bool) ([]Expression, error)
	InsertExpression(x *Expression) error
	SetExpressionFavorite(id int64, fav bool) error
	DeleteAllExpressions() (int64, error)

	// ---- 检查历史与趋势 ----
	ListCheckHistory(limit int) ([]CheckHistory, error)
	InsertCheckHistory(x *CheckHistory) error
	DeleteAllCheckHistory() (int64, error)
	ListTrendPoints(from, errType string) ([]TrendPoint, error)

	// ---- 设置（KV）----
	GetAllSettings() (map[string]string, error)
	GetSetting(key string) (string, error)
	SetSetting(key, value string) error

	// ---- 成分说明字典（seed 预置，运行期只读）----
	GetCompInfo(role string) (*CompInfo, error)
}

// 编译期断言：*Store 必须始终满足 Repository 契约，
// 新增 Store 方法或修改签名时若破坏契约会在编译期立即暴露。
var _ Repository = (*Store)(nil)
