// Package api 实现 GrammarChecker 的 HTTP 路由与处理器。
//
// 接口契约严格对照《产品技术文档与交互说明》第 4 节的接口草案；
// 联调字段口径见 api/readme.md。新增或调整接口时，必须先同步更新技术文档，
// 再同步 AGENTS.md 第 3 节。
package api

import (
	"io/fs"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"grammarchecker/server/llm"
	"grammarchecker/server/store"
)

// Handler 聚合全部处理器依赖：数据层与 LLM 客户端。
type Handler struct {
	store store.Repository
	llm   *llm.Client
}

// NewHandler 构造 Handler。
func NewHandler(s store.Repository, c *llm.Client) *Handler {
	return &Handler{store: s, llm: c}
}

// repo 当前请求可用的数据视图：绑定到登录用户（多用户模式）或
// 内置单用户身份 uid=0（单用户模式），见 store.ForUser / auth.go。
// 全部业务数据 handler 必须经此取数，禁止直接用 h.store（会绕过用户隔离）；
// settings / comp-info / 用户管理属全局语义，仍用 h.store。
func (h *Handler) repo(r *http.Request) store.Repository {
	return h.store.ForUser(currentUser(r))
}

// Routes 组装完整路由：/api 前缀的业务接口 + 嵌入的前端静态资源。
func (h *Handler) Routes(assets fs.FS) http.Handler {
	r := chi.NewRouter()
	r.Use(recoverJSON)                       // panic 兜底，返回 500 JSON（替代 chimw.Recoverer 纯文本）
	r.Use(h.withAuth)                        // 会话认证：单用户模式关闭；多用户模式校验 gc_session（auth.go）
	r.Use(withJSONTimeout(60 * time.Second)) // 文档约定：LLM 同步长请求 60s 上限，超时返回 503 JSON

	r.Route("/api", func(r chi.Router) {
		// 健康检查（供托盘/脚本探活，非文档 13 接口；认证豁免）
		r.Get("/ping", h.Ping)

		// 认证与用户管理（多用户体系，处理器见 auth_handlers.go / users_handlers.go）
		r.Post("/auth/login", h.Login)
		r.Post("/auth/logout", h.Logout)
		r.Get("/auth/me", h.Me)
		r.Post("/auth/password", h.ChangePassword)
		r.Get("/users", h.ListUsers)
		r.Post("/users", h.CreateUser)
		r.Delete("/users/{id}", h.DeleteUser)
		r.Post("/users/{id}/password", h.ResetUserPassword)

		// LLM 类（DeepSeek 已接通，处理器见 llm_handlers.go）
		r.Post("/check", h.Check)
		r.Post("/discriminate", h.Discriminate)
		r.Post("/express", h.Express)
		r.Post("/llm/usage-example", h.UsageExample) // 成分用法示例（一次性生成，不入库）

		// 成分说明（预置 seed，只读字典）
		r.Get("/comp-info", h.GetCompInfo)

		// 错词本（先前错误）
		r.Get("/wrong-words", h.ListWrongWords)
		r.Post("/wrong-words", h.AddWrongWord)
		r.Delete("/wrong-words/{id}", h.DeleteWrongWord)

		// 例句
		r.Get("/sentences", h.ListSentences)
		r.Post("/sentences", h.AddSentence)
		r.Delete("/sentences/{id}", h.DeleteSentence)

		// 辨析
		r.Get("/discriminations", h.ListDiscriminations)
		r.Post("/discriminations/{id}/favorite", h.FavoriteDiscrimination)
		r.Delete("/discriminations/{id}/favorite", h.UnfavoriteDiscrimination)
		r.Delete("/discriminations/{id}", h.DeleteDiscrimination)
		r.Delete("/discrimination-history", h.ClearDiscriminationHistory)

		// 表达
		r.Get("/expression-history", h.ListExpressionHistory)
		r.Post("/expression-history/{id}/favorite", h.FavoriteExpression)
		r.Delete("/expression-history/{id}/favorite", h.UnfavoriteExpression)
		r.Delete("/expression-history", h.ClearExpressionHistory)

		// 检查历史
		r.Get("/history/check", h.ListCheckHistory)
		r.Delete("/history/check", h.ClearCheckHistory)

		// 趋势统计
		r.Get("/trends", h.Trends)

		// 设置
		r.Get("/settings", h.GetSettings)
		r.Put("/settings", h.PutSettings)
	})

	// 前端 SPA：由 go:embed 注入的 app/vue 提供静态服务（index.html 兜底 /）
	sub, err := fs.Sub(assets, "app/vue")
	if err != nil {
		panic("嵌入资源缺少 app/vue 目录: " + err.Error())
	}
	fileServer := http.FileServerFS(sub)
	r.Handle("/*", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 目录列表禁用（2026-09-29 安全加固）：/css/、/js/ 等子目录的文件名清单
		// 不对外暴露；"/" 由 FileServer 正常回 index.html，页面路由不受影响。
		if r.URL.Path != "/" && strings.HasSuffix(r.URL.Path, "/") {
			http.NotFound(w, r)
			return
		}
		fileServer.ServeHTTP(w, r)
	}))
	return r
}

// Ping GET /api/ping —— 健康检查。
func (h *Handler) Ping(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
