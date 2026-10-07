package api

// API KEY 解析层：统一「环境变量优先、数据库回退」的取值口径。
//
// 背景（部署形态，2026-09-26）：应用默认设计为桌面单机使用，密钥存本地 SQLite。
// 部署到云服务器后，密钥若继续存库 + 随 GET /api/settings 明文回传，
// 则数据库文件与 HTTP 响应都成为泄露面。故引入环境变量 GC_API_KEY：
// 非空时优先采用且不回退数据库，密钥只存在于进程内存与部署环境的 env 文件中。

import (
	"os"
	"strings"

	"grammarchecker/server/store"
)

// EnvAPIKey 环境变量名：部署时由 systemd 通过 EnvironmentFile 注入。
const EnvAPIKey = "GC_API_KEY"

// 密钥来源标识，随 GET /api/settings 以 api_key_source 字段回传，
// 供前端区分「页面可编辑」与「由部署环境托管」两种状态。
const (
	apiKeySourceEnv = "env"
	apiKeySourceDB  = "db"
)

// resolveAPIKey 解析当前生效的 API KEY 及其来源。
// 环境变量非空时优先采用并直接返回，不再读取数据库——两处同时配置时以环境变量为准，
// 避免出现「页面显示一份、实际调用另一份」的歧义。
func resolveAPIKey(st store.Repository) (key, source string, err error) {
	if v := strings.TrimSpace(os.Getenv(EnvAPIKey)); v != "" {
		return v, apiKeySourceEnv, nil
	}
	v, err := st.GetSetting("api_key")
	if err != nil {
		return "", "", err
	}
	return v, apiKeySourceDB, nil
}

// apiKeyManagedByEnv 报告密钥是否由环境变量托管（供配置写入校验复用，
// 与 resolveAPIKey 共用同一判断口径，避免两处逻辑漂移）。
func apiKeyManagedByEnv() bool {
	return strings.TrimSpace(os.Getenv(EnvAPIKey)) != ""
}
