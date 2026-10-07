package api

// 认证类接口处理器（多用户体系，2026-09-29）。
// 契约见 api/README.md 第 9 节。

import (
	"errors"
	"net/http"
	"time"

	"grammarchecker/server/store"
)

// meResponse 登录态/身份查询的统一响应体（api/README.md §9.1）。
type meResponse struct {
	Username    *string `json:"username"`   // 未登录（单用户模式）为 null
	IsAdmin     bool    `json:"is_admin"`   // 是否管理员（含用户管理入口判定）
	AuthRequired bool   `json:"auth_required"` // true = 多用户模式（库中存在用户）
}

// buildMeResponse 按「当前登录用户 + 是否存在用户」组装响应。
func (h *Handler) buildMeResponse(u *User) (meResponse, error) {
	hasUsers, err := h.store.HasUsers()
	if err != nil {
		return meResponse{}, err
	}
	resp := meResponse{AuthRequired: hasUsers}
	if u != nil {
		name := u.Username
		resp.Username = &name
		resp.IsAdmin = u.IsAdmin
	}
	return resp, nil
}

// Login POST /api/auth/login —— 登录并下发会话 Cookie。
// 失败统一延迟约 300ms 再响应（用户名错/密码错同一口径，见 store.Authenticate），
// 抬高在线爆破成本；个人部署规模不做验证码/锁定。
func (h *Handler) Login(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Username == "" || req.Password == "" {
		fail(w, http.StatusBadRequest, "bad_request", "username 与 password 均不能为空")
		return
	}
	u, err := h.store.Authenticate(req.Username, req.Password)
	if err != nil {
		time.Sleep(300 * time.Millisecond)
		fail(w, http.StatusUnauthorized, "unauthorized", "用户名或密码错误")
		return
	}
	token, expires, err := h.store.CreateSession(u.ID, sessionTTL)
	if err != nil {
		fail(w, http.StatusInternalServerError, "db_error", err.Error())
		return
	}
	http.SetCookie(w, h.sessionCookie(r, token, expires))
	resp, err := h.buildMeResponse(u)
	if err != nil {
		fail(w, http.StatusInternalServerError, "db_error", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// Logout POST /api/auth/logout —— 注销会话并清除 Cookie。
// 幂等：未登录 / 会话已失效同样返回 204。
func (h *Handler) Logout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookieName); err == nil && c.Value != "" {
		if err := h.store.DeleteSession(c.Value); err != nil {
			fail(w, http.StatusInternalServerError, "db_error", err.Error())
			return
		}
	}
	// 置空值 + 过期时间归零，指示浏览器删除 Cookie
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookieName, Value: "", Path: "/",
		Expires: time.Unix(0, 0), HttpOnly: true, SameSite: http.SameSiteLaxMode,
	})
	w.WriteHeader(http.StatusNoContent)
}

// Me GET /api/auth/me —— 查询当前身份与认证模式（豁免认证，见 auth.go）。
// 前端启动时调用：auth_required && username == null → 展示登录视图。
func (h *Handler) Me(w http.ResponseWriter, r *http.Request) {
	resp, err := h.buildMeResponse(currentUserInfo(r))
	if err != nil {
		fail(w, http.StatusInternalServerError, "db_error", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// ChangePassword POST /api/auth/password —— 本人修改密码（需验旧密码）。
// 仅多用户模式可达（中间件已拦截）；单用户模式无「本人密码」概念。
func (h *Handler) ChangePassword(w http.ResponseWriter, r *http.Request) {
	u := currentUserInfo(r)
	if u == nil {
		// 单用户模式下调用：不存在可修改的账号
		fail(w, http.StatusBadRequest, "bad_request", "当前为单用户模式，无账号可修改")
		return
	}
	var req struct {
		OldPassword string `json:"old_password"`
		NewPassword string `json:"new_password"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if len(req.NewPassword) < 6 {
		fail(w, http.StatusBadRequest, "bad_request", "新密码长度至少 6 位")
		return
	}
	if _, err := h.store.Authenticate(u.Username, req.OldPassword); err != nil {
		fail(w, http.StatusUnauthorized, "unauthorized", "旧密码错误")
		return
	}
	if err := h.store.UpdateUserPassword(u.ID, req.NewPassword); err != nil {
		if mapStoreErr(w, err) {
			return
		}
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// mapUserErr 用户管理类错误映射：ErrConflict → 409，ErrAuthFailed → 401，
// 其余走 mapStoreErr（ErrNotFound → 404）。
func mapUserErr(w http.ResponseWriter, err error) bool {
	switch {
	case err == nil:
		return false
	case errors.Is(err, store.ErrConflict):
		fail(w, http.StatusConflict, "conflict", "用户名已存在")
		return true
	case errors.Is(err, store.ErrAuthFailed):
		fail(w, http.StatusUnauthorized, "unauthorized", "用户名或密码错误")
		return true
	}
	return mapStoreErr(w, err)
}
