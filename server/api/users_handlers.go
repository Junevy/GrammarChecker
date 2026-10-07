package api

// 用户管理接口处理器（多用户体系，2026-09-29）。
// 仅管理员可达（requireAdmin 守卫）；单用户模式下中间件不会放行到此
// （无用户即无管理员身份，统一 401）。
// 契约见 api/README.md §9.5。

import (
	"net/http"
	"strconv"
	"strings"
)

// minPasswordLen 密码最短长度（本人改密与管理员创建/重置共用）。
const minPasswordLen = 6

// requireAdmin 校验当前登录用户为管理员，不满足时写 401/403 并返回 false。
func (h *Handler) requireAdmin(w http.ResponseWriter, r *http.Request) bool {
	if !h.authRequired() {
		// 单用户模式：不存在管理员身份，用户管理整体不可用
		fail(w, http.StatusUnauthorized, "unauthorized", "单用户模式下无用户管理")
		return false
	}
	u := currentUserInfo(r)
	if u == nil {
		writeUnauthorized(w)
		return false
	}
	if !u.IsAdmin {
		fail(w, http.StatusForbidden, "forbidden", "仅管理员可管理用户")
		return false
	}
	return true
}

// authRequired 报告当前是否多用户模式（users 表非空）。
func (h *Handler) authRequired() bool {
	has, err := h.store.HasUsers()
	return err == nil && has
}

// ListUsers GET /api/users —— 用户列表（不含密码哈希）。
func (h *Handler) ListUsers(w http.ResponseWriter, r *http.Request) {
	if !h.requireAdmin(w, r) {
		return
	}
	items, err := h.store.ListUsers()
	if err != nil {
		fail(w, http.StatusInternalServerError, "db_error", err.Error())
		return
	}
	writeList(w, items)
}

// CreateUser POST /api/users —— 创建用户（管理员操作）。
func (h *Handler) CreateUser(w http.ResponseWriter, r *http.Request) {
	if !h.requireAdmin(w, r) {
		return
	}
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
		IsAdmin  bool   `json:"is_admin"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	req.Username = strings.TrimSpace(req.Username)
	if req.Username == "" || len(req.Password) < minPasswordLen {
		fail(w, http.StatusBadRequest, "bad_request",
			"username 不能为空，password 长度至少 "+strconv.Itoa(minPasswordLen)+" 位")
		return
	}
	u := &User{Username: req.Username, IsAdmin: req.IsAdmin}
	if err := h.store.CreateUser(u, req.Password); err != nil {
		if mapUserErr(w, err) {
			return
		}
		return
	}
	writeJSON(w, http.StatusCreated, u)
}

// DeleteUser DELETE /api/users/{id} —— 删除用户及其全部数据。
// 守卫：不能删自己；不能删最后一名管理员。
func (h *Handler) DeleteUser(w http.ResponseWriter, r *http.Request) {
	if !h.requireAdmin(w, r) {
		return
	}
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if id == currentUser(r) {
		fail(w, http.StatusBadRequest, "bad_request", "不能删除当前登录的账号")
		return
	}
	target, err := h.store.GetUserByID(id)
	if mapStoreErr(w, err) {
		return
	}
	if target.IsAdmin {
		users, err := h.store.ListUsers()
		if err != nil {
			fail(w, http.StatusInternalServerError, "db_error", err.Error())
			return
		}
		admins := 0
		for _, u := range users {
			if u.IsAdmin {
				admins++
			}
		}
		if admins <= 1 {
			fail(w, http.StatusBadRequest, "bad_request", "不能删除最后一名管理员")
			return
		}
	}
	if err := h.store.DeleteUser(id); err != nil {
		if mapStoreErr(w, err) {
			return
		}
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ResetUserPassword POST /api/users/{id}/password —— 管理员重置任意用户密码。
func (h *Handler) ResetUserPassword(w http.ResponseWriter, r *http.Request) {
	if !h.requireAdmin(w, r) {
		return
	}
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var req struct {
		Password string `json:"password"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if len(req.Password) < minPasswordLen {
		fail(w, http.StatusBadRequest, "bad_request",
			"password 长度至少 "+strconv.Itoa(minPasswordLen)+" 位")
		return
	}
	if err := h.store.UpdateUserPassword(id, req.Password); err != nil {
		if mapStoreErr(w, err) {
			return
		}
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
