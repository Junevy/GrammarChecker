package api

// 部署形态测试（2026-09-26 引入；2026-09-29 Basic Auth 改造为会话认证）。
//
// 覆盖三类边界：
//  1. 无用户（单用户模式）时行为与桌面单机形态完全一致（回归保护，防止"加了认证"把本机体验改坏）；
//  2. GC_API_KEY 托管时密钥不出现在任何响应中、且拒绝经 API 改写；
//  3. 多用户模式（存在用户）下：未登录被拒、登录下发 Cookie、Cookie 放行，
//     探活与静态资源豁免，401 不带 WWW-Authenticate（由 SPA 登录页接管）。

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"grammarchecker/server/store"
)

// clearDeployEnv 清空部署相关环境变量，避免开发机既有配置干扰用例结果。
func clearDeployEnv(t *testing.T) {
	t.Helper()
	t.Setenv(EnvAPIKey, "")
}

// doReq 执行 JSON 请求；token 非空时以 gc_session Cookie 携带会话。
// 返回状态码、响应体与响应头。
func doReq(t *testing.T, ts *httptest.Server, method, path string, body any, token string) (int, []byte, http.Header) {
	t.Helper()
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("序列化请求失败: %v", err)
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, ts.URL+path, rd)
	if err != nil {
		t.Fatalf("构造请求失败: %v", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: token})
	}
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("请求失败: %v", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("读取响应失败: %v", err)
	}
	return resp.StatusCode, raw, resp.Header
}

// decodeMap 解析响应体为 map，失败即判定用例失败。
func decodeMap(t *testing.T, raw []byte) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("响应不是合法 JSON 对象: %s", raw)
	}
	return m
}

// loginAs 走登录接口换取会话 token（用例内快速登录辅助）。
func loginAs(t *testing.T, ts *httptest.Server, username, password string) string {
	t.Helper()
	code, raw, hdr := doReq(t, ts, http.MethodPost, "/api/auth/login",
		map[string]string{"username": username, "password": password}, "")
	if code != http.StatusOK {
		t.Fatalf("登录 %s 应 200, got %d: %s", username, code, raw)
	}
	cookies := hdr.Values("Set-Cookie")
	if len(cookies) == 0 {
		t.Fatal("登录成功未下发 Set-Cookie")
	}
	// 取 gc_session=<token> 的值部分（首个分号前）
	for _, c := range cookies {
		if v, ok := strings.CutPrefix(c, sessionCookieName+"="); ok {
			return strings.SplitN(v, ";", 2)[0]
		}
	}
	t.Fatalf("Set-Cookie 中缺少 %s: %v", sessionCookieName, cookies)
	return ""
}

// createTestUser 用例内直接经 store 建用户（绕过 HTTP，避免用例间依赖）。
func createTestUser(t *testing.T, s store.Repository, username string, admin bool) {
	t.Helper()
	err := s.CreateUser(&store.User{Username: username, IsAdmin: admin}, username+"-pass-6")
	if err != nil {
		t.Fatalf("创建测试用户 %s 失败: %v", username, err)
	}
}

// TestSettingsAPIKeySource 设置接口的密钥来源语义：
// 数据库托管时明文回传（原契约不变），环境变量托管时回空串 + source=env 且禁止写入。
func TestSettingsAPIKeySource(t *testing.T) {
	clearDeployEnv(t)
	ts, s := newTestHandler(t)

	// ---- 形态一：数据库托管（桌面单机，默认）----
	if err := s.SetSetting("api_key", "sk-db"); err != nil {
		t.Fatalf("写入 api_key 失败: %v", err)
	}
	code, raw, _ := doReq(t, ts, http.MethodGet, "/api/settings", nil, "")
	if code != http.StatusOK {
		t.Fatalf("GET /api/settings 应 200, got %d: %s", code, raw)
	}
	m := decodeMap(t, raw)
	if m["api_key"] != "sk-db" || m["api_key_source"] != "db" {
		t.Fatalf("数据库形态应明文回传且 source=db, got %v", m)
	}

	// ---- 形态二：环境变量托管 ----
	t.Setenv(EnvAPIKey, "sk-env")
	code, raw, _ = doReq(t, ts, http.MethodGet, "/api/settings", nil, "")
	if code != http.StatusOK {
		t.Fatalf("GET /api/settings 应 200, got %d: %s", code, raw)
	}
	m = decodeMap(t, raw)
	if m["api_key"] != "" {
		t.Fatalf("环境变量托管时不得回传明文密钥, got %q", m["api_key"])
	}
	if m["api_key_source"] != "env" {
		t.Fatalf("api_key_source 应为 env, got %v", m["api_key_source"])
	}

	// 托管时改写密钥应被拒：否则库值与进程实际生效值不一致，重启后还会被环境变量静默覆盖
	code, raw, _ = doReq(t, ts, http.MethodPut, "/api/settings", map[string]string{"api_key": "sk-new"}, "")
	if code != http.StatusBadRequest || decodeMap(t, raw)["code"] != "bad_request" {
		t.Fatalf("托管时写 api_key 应 400 bad_request, got %d: %s", code, raw)
	}

	// 未托管的其它键仍可写，且回显中密钥依然为空
	code, raw, _ = doReq(t, ts, http.MethodPut, "/api/settings", map[string]string{"reuse_discrimination_history": "0"}, "")
	if code != http.StatusOK {
		t.Fatalf("写 reuse 开关应 200, got %d: %s", code, raw)
	}
	m = decodeMap(t, raw)
	if m["reuse_discrimination_history"] != "0" || m["api_key"] != "" {
		t.Fatalf("写入后应回显最新设置且密钥仍为空, got %v", m)
	}

	// api_key_source 是只读派生字段，写入必须被白名单拒绝
	code, raw, _ = doReq(t, ts, http.MethodPut, "/api/settings", map[string]string{"api_key_source": "db"}, "")
	if code != http.StatusBadRequest {
		t.Fatalf("写只读字段应 400, got %d: %s", code, raw)
	}
}

// TestAPIKeyEnvTakesPriority 环境变量优先于数据库：
// 库中不放密钥、仅设 GC_API_KEY，LLM 类接口应能正常走通（证明取的是环境变量）。
func TestAPIKeyEnvTakesPriority(t *testing.T) {
	clearDeployEnv(t)
	t.Setenv(EnvAPIKey, "sk-env")

	reply := `{"errTotal":9,"translation":"fake 翻译","errors":[{"type":"主谓一致","fix":"have went → went","desc":"fake"}],"example":{"en":"She went.","zh":"fake"},"idiomatic":{"chip":"更地道","tip":"t","en":"e","desc":"d"},"struct":{"comps":[],"pattern":"p","clause":null,"tense":{"chip":"一般过去时","desc":"d"}}}`
	ts, _ := newTestHandlerWithLLM(t, reply)

	code, raw, _ := doReq(t, ts, http.MethodPost, "/api/check", map[string]any{"text": "She have went to school."}, "")
	if code != http.StatusOK {
		t.Fatalf("环境变量提供密钥时应走通 LLM 链路, got %d: %s", code, raw)
	}
}

// TestSessionAuth 会话认证中间件：单用户模式回归 + 多用户模式放行/拒绝边界。
func TestSessionAuth(t *testing.T) {
	clearDeployEnv(t)
	ts, s := newTestHandler(t)

	// ---- 单用户模式（无用户）：认证完全关闭，桌面形态与开发体验不受影响 ----
	if code, _, _ := doReq(t, ts, http.MethodGet, "/api/settings", nil, ""); code != http.StatusOK {
		t.Fatalf("无用户时应放行, got %d", code)
	}
	// me 指示前端：无需登录
	code, raw, _ := doReq(t, ts, http.MethodGet, "/api/auth/me", nil, "")
	m := decodeMap(t, raw)
	if m["auth_required"] != false || m["username"] != nil {
		t.Fatalf("无用户时 me 应为 {username:null, auth_required:false}, got %s", raw)
	}

	// ---- 建立用户 → 进入多用户模式 ----
	createTestUser(t, s, "alice", true)

	// 未登录访问业务接口 → 401，且不带 WWW-Authenticate（SPA 自绘登录页接管）
	code, raw, hdr := doReq(t, ts, http.MethodGet, "/api/settings", nil, "")
	if code != http.StatusUnauthorized {
		t.Fatalf("多用户模式未登录应 401, got %d: %s", code, raw)
	}
	if hdr.Get("WWW-Authenticate") != "" {
		t.Fatal("401 不应带 WWW-Authenticate（会触发浏览器原生弹框，与 SPA 登录页冲突）")
	}
	if decodeMap(t, raw)["code"] != "unauthorized" {
		t.Fatalf("401 应为统一错误结构 {code,message}, got %s", raw)
	}

	// 静态资源壳公开（SPA 登录视图本身就在壳里）
	if code, _, _ = doReq(t, ts, http.MethodGet, "/", nil, ""); code != http.StatusOK {
		t.Fatalf("静态资源应豁免认证, got %d", code)
	}
	// 探活免认证：供 systemd / 监控在鉴权前做健康检查
	if code, _, _ = doReq(t, ts, http.MethodGet, "/api/ping", nil, ""); code != http.StatusOK {
		t.Fatalf("/api/ping 应免认证, got %d", code)
	}
	// me 免认证：auth_required=true、username=null → 前端据此展示登录视图
	code, raw, _ = doReq(t, ts, http.MethodGet, "/api/auth/me", nil, "")
	m = decodeMap(t, raw)
	if m["auth_required"] != true || m["username"] != nil {
		t.Fatalf("多用户模式未登录 me 应为 {username:null, auth_required:true}, got %s", raw)
	}

	// 错误密码 → 401；正确 → 200 + Set-Cookie
	code, _, _ = doReq(t, ts, http.MethodPost, "/api/auth/login",
		map[string]string{"username": "alice", "password": "wrong-pass"}, "")
	if code != http.StatusUnauthorized {
		t.Fatalf("错误密码应 401, got %d", code)
	}
	token := loginAs(t, ts, "alice", "alice-pass-6")

	// 带 Cookie 放行
	if code, _, _ = doReq(t, ts, http.MethodGet, "/api/settings", nil, token); code != http.StatusOK {
		t.Fatalf("携带会话 Cookie 应放行, got %d", code)
	}

	// 伪造 / 过期 token → 401
	if code, _, _ = doReq(t, ts, http.MethodGet, "/api/settings", nil, "deadbeef"); code != http.StatusUnauthorized {
		t.Fatalf("伪造 token 应 401, got %d", code)
	}

	// 登出后原 token 立即失效
	if code, _, _ = doReq(t, ts, http.MethodPost, "/api/auth/logout", nil, token); code != http.StatusNoContent {
		t.Fatalf("登出应 204, got %d", code)
	}
	if code, _, _ = doReq(t, ts, http.MethodGet, "/api/settings", nil, token); code != http.StatusUnauthorized {
		t.Fatalf("登出后原 token 应失效, got %d", code)
	}
}

// TestSettingsPermissionAndRevocation 安全加固回归（2026-09-29）：
// 多用户模式下全局设置仅管理员可写、密钥明文仅管理员可读（普通用户掩码）；
// 改密后本人全部会话立即吊销。
func TestSettingsPermissionAndRevocation(t *testing.T) {
	clearDeployEnv(t)
	ts, s := newTestHandler(t)
	createTestUser(t, s, "root", true)
	createTestUser(t, s, "bob", false)
	if err := s.SetSetting("api_key", "sk-secret"); err != nil {
		t.Fatalf("写入 api_key 失败: %v", err)
	}

	rootToken := loginAs(t, ts, "root", "root-pass-6")
	bobToken := loginAs(t, ts, "bob", "bob-pass-6")

	// ---- 管理员：密钥明文可见、设置可写 ----
	code, raw, _ := doReq(t, ts, http.MethodGet, "/api/settings", nil, rootToken)
	m := decodeMap(t, raw)
	if code != http.StatusOK || m["api_key"] != "sk-secret" {
		t.Fatalf("管理员应读到明文密钥, got %d: %s", code, raw)
	}
	code, raw, _ = doReq(t, ts, http.MethodPut, "/api/settings",
		map[string]string{"reuse_discrimination_history": "0"}, rootToken)
	if code != http.StatusOK {
		t.Fatalf("管理员应可写设置, got %d: %s", code, raw)
	}

	// ---- 普通用户：密钥掩码为空串、写入 403 forbidden ----
	code, raw, _ = doReq(t, ts, http.MethodGet, "/api/settings", nil, bobToken)
	m = decodeMap(t, raw)
	if code != http.StatusOK || m["api_key"] != "" {
		t.Fatalf("非管理员不应读到密钥明文, got %d: %s", code, raw)
	}
	code, raw, _ = doReq(t, ts, http.MethodPut, "/api/settings",
		map[string]string{"reuse_discrimination_history": "1"}, bobToken)
	if code != http.StatusForbidden || decodeMap(t, raw)["code"] != "forbidden" {
		t.Fatalf("非管理员写设置应 403 forbidden, got %d: %s", code, raw)
	}

	// ---- 改密吊销全部会话：旧 token 立即失效，新密码可重新登录 ----
	code, _, _ = doReq(t, ts, http.MethodPost, "/api/auth/password",
		map[string]string{"old_password": "bob-pass-6", "new_password": "bobnew-pass"}, bobToken)
	if code != http.StatusNoContent {
		t.Fatalf("本人改密应 204, got %d", code)
	}
	code, _, _ = doReq(t, ts, http.MethodGet, "/api/settings", nil, bobToken)
	if code != http.StatusUnauthorized {
		t.Fatalf("改密后旧会话应吊销, got %d", code)
	}
	loginAs(t, ts, "bob", "bobnew-pass")
}

// TestAdminGuard 用户管理接口的管理员守卫与业务守卫。
func TestAdminGuard(t *testing.T) {
	clearDeployEnv(t)
	ts, s := newTestHandler(t)
	createTestUser(t, s, "root", true)
	createTestUser(t, s, "bob", false)

	rootToken := loginAs(t, ts, "root", "root-pass-6")
	bobToken := loginAs(t, ts, "bob", "bob-pass-6")

	// 非管理员 → 403
	code, raw, _ := doReq(t, ts, http.MethodGet, "/api/users", nil, bobToken)
	if code != http.StatusForbidden || decodeMap(t, raw)["code"] != "forbidden" {
		t.Fatalf("非管理员应 403 forbidden, got %d: %s", code, raw)
	}
	code, _, _ = doReq(t, ts, http.MethodPost, "/api/users",
		map[string]any{"username": "eve", "password": "evepass-6"}, bobToken)
	if code != http.StatusForbidden {
		t.Fatalf("非管理员建用户应 403, got %d", code)
	}

	// 管理员列表与创建
	code, raw, _ = doReq(t, ts, http.MethodGet, "/api/users", nil, rootToken)
	if code != http.StatusOK {
		t.Fatalf("管理员应可列出用户, got %d: %s", code, raw)
	}
	if bytes.Contains(raw, []byte("password_hash")) || bytes.Contains(raw, []byte("$2")) {
		t.Fatalf("用户列表不得泄露密码哈希: %s", raw)
	}
	code, raw, _ = doReq(t, ts, http.MethodPost, "/api/users",
		map[string]any{"username": "carol", "password": "short"}, rootToken)
	if code != http.StatusBadRequest {
		t.Fatalf("短密码应 400, got %d: %s", code, raw)
	}
	code, raw, _ = doReq(t, ts, http.MethodPost, "/api/users",
		map[string]any{"username": "carol", "password": "carolpass"}, rootToken)
	if code != http.StatusCreated {
		t.Fatalf("建用户应 201, got %d", code)
	}
	// 重名检测大小写不敏感（COLLATE NOCASE）——carol 仍在库中时用 CAROL 触发 409
	code, raw, _ = doReq(t, ts, http.MethodPost, "/api/users",
		map[string]any{"username": "CAROL", "password": "carolpass"}, rootToken)
	if code != http.StatusConflict {
		t.Fatalf("重名（大小写不敏感）应 409, got %d: %s", code, raw)
	}

	// 守卫：不能删自己；删除普通用户（carol=id3）；重置不存在用户 404
	code, _, _ = doReq(t, ts, http.MethodDelete, "/api/users/1", nil, rootToken)
	if code != http.StatusBadRequest {
		t.Fatalf("删除自己应 400, got %d", code)
	}
	code, _, _ = doReq(t, ts, http.MethodDelete, "/api/users/3", nil, rootToken)
	if code != http.StatusNoContent {
		t.Fatalf("删除普通用户应 204, got %d", code)
	}
	code, _, _ = doReq(t, ts, http.MethodPost, "/api/users/999/password",
		map[string]string{"password": "resetpass"}, rootToken)
	if code != http.StatusNotFound {
		t.Fatalf("重置不存在用户应 404, got %d", code)
	}

	// 本人改密：旧密码错误 401；成功 204 后新密码可登录、旧密码失效
	code, _, _ = doReq(t, ts, http.MethodPost, "/api/auth/password",
		map[string]string{"old_password": "nope", "new_password": "newpass1"}, bobToken)
	if code != http.StatusUnauthorized {
		t.Fatalf("旧密码错误应 401, got %d", code)
	}
	code, _, _ = doReq(t, ts, http.MethodPost, "/api/auth/password",
		map[string]string{"old_password": "bob-pass-6", "new_password": "bobnew-pass"}, bobToken)
	if code != http.StatusNoContent {
		t.Fatalf("本人改密应 204, got %d", code)
	}
	if _, err := s.Authenticate("bob", "bobnew-pass"); err != nil {
		t.Fatalf("新密码应可登录: %v", err)
	}
	if _, err := s.Authenticate("bob", "bob-pass-6"); err == nil {
		t.Fatal("旧密码应已失效")
	}
}
