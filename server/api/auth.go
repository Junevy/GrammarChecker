package api

// 会话 Cookie 认证中间件（多用户体系，2026-09-29，取代原 Basic Auth 方案）。
//
// 设计取舍（相对旧 GC_AUTH_USER/GC_AUTH_PASS Basic Auth）：
//   - 多账号 + 数据隔离：凭据存 users 表（bcrypt），每个用户只看到自己的数据；
//   - 登录态走 HttpOnly Cookie（gc_session），手机浏览器不再反复弹凭据框，
//     SPA 可自绘登录页（旧方案浏览器原生弹窗无法自定义样式）；
//   - 登录页属于 SPA 的一部分，因此静态资源不再加保护（壳内无数据），
//     仅 /api/** 业务接口受保护；
//   - 单用户模式（users 表为空）认证完全关闭，桌面单机形态与开发期体验不变。
//
// 会话仅存 token 哈希（见 store/users.go）；Cookie 标志：
// HttpOnly（防脚本读取）+ SameSite=Lax（同源请求携带、跨站跳转基本不带）
// + Secure（经 TLS 反代时，按 X-Forwarded-Proto 识别）。

import (
	"context"
	"net/http"
	"strings"
	"time"

	"grammarchecker/server/store"
)

// User 别名：api 层处理器统一用短名引用用户实体。
type User = store.User

// sessionCookieName 会话 Cookie 名（前端无需感知，浏览器自动携带）。
const sessionCookieName = "gc_session"

// sessionTTL 会话有效期：登录一次 30 天内免登录。
const sessionTTL = 30 * 24 * time.Hour

// ctxKeyUser context 键类型（私有类型防碰撞）。
type ctxKeyUser struct{}

// withAuth 会话认证中间件。
//
// 豁免路径：/api/ping（探活）、/api/auth/login（登录本身）、/api/auth/me
// （前端用它判断是否要展示登录视图）、/api/auth/logout（幂等）、
// 以及一切非 /api 前缀路径（go:embed 静态资源）。
func (h *Handler) withAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		if !strings.HasPrefix(p, "/api/") ||
			p == "/api/ping" ||
			p == "/api/auth/login" || p == "/api/auth/me" || p == "/api/auth/logout" {
			next.ServeHTTP(w, r)
			return
		}

		// 单用户模式：users 表为空 → 认证关闭，以内置身份 uid=0 放行
		hasUsers, err := h.store.HasUsers()
		if err != nil {
			fail(w, http.StatusInternalServerError, "db_error", err.Error())
			return
		}
		if !hasUsers {
			next.ServeHTTP(w, withUser(r, nil))
			return
		}

		// 多用户模式：校验会话 Cookie
		c, err := r.Cookie(sessionCookieName)
		if err != nil || c.Value == "" {
			writeUnauthorized(w)
			return
		}
		u, err := h.store.GetUserBySessionToken(c.Value)
		if err != nil {
			writeUnauthorized(w)
			return
		}
		next.ServeHTTP(w, withUser(r, u))
	})
}

// writeUnauthorized 统一 401 响应。不带 WWW-Authenticate：
// 由前端 SPA 收到 401 后切换登录视图，而非浏览器原生弹框。
func writeUnauthorized(w http.ResponseWriter) {
	fail(w, http.StatusUnauthorized, "unauthorized", "未登录或登录已过期，请重新登录")
}

// withUser 把登录用户挂入请求 context；u 为 nil 表示单用户模式（未登录）。
func withUser(r *http.Request, u *User) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), ctxKeyUser{}, u))
}

// currentUserInfo 取当前登录用户；单用户模式返回 nil。
func currentUserInfo(r *http.Request) *User {
	u, _ := r.Context().Value(ctxKeyUser{}).(*User)
	return u
}

// currentUser 当前请求绑定的用户 ID：登录用户取其 ID；
// 单用户模式恒为 0（与历史数据的 user_id 默认值一致，迁移后划归首个用户）。
func currentUser(r *http.Request) int64 {
	if u := currentUserInfo(r); u != nil {
		return u.ID
	}
	return 0
}

// sessionCookie 构造会话 Cookie：经 TLS 反代访问时追加 Secure
// （服务本身只监听回环/内网，TLS 由 deploy/Caddyfile 的反代层终结）。
func (h *Handler) sessionCookie(r *http.Request, token string, expires time.Time) *http.Cookie {
	secure := r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
	return &http.Cookie{
		Name:     sessionCookieName,
		Value:    token,
		Path:     "/",
		Expires:  expires,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   secure,
	}
}
