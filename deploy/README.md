# GrammarChecker 云服务器部署手册

面向「一台轻量云服务器 + 无域名 + 仅自己使用」的部署形态。全程约 10 分钟。

## 0. 部署形态

```
浏览器/手机 ──HTTPS(自签)──> Caddy :443 ──HTTP 回环──> grammarchecker :8899 ──> SQLite
                                                │              │
                                          登录页(会话)     DeepSeek API（外呼）
```

- **后端只监听 `127.0.0.1:8899`**，公网永远够不到它，由 Caddy 对外；
- **认证由后端登录页完成**（2026-09-29 多用户体系）：首个管理员用命令行创建，
  之后管理员在网页「配置」页增删用户；登录后下发 30 天会话 Cookie，手机/电脑同体验；
  Caddy 层不再配置 basic_auth（避免双层登录）；
- **API KEY 由环境变量注入**，不写数据库、不回传页面；
- **数据按用户隔离**：错词本/例句/辨析/表达/检查历史各用户独立；设置与成分字典全局共用。

## 1. 前置条件

| 项 | 要求 |
|---|---|
| 服务器 | Linux（Debian/Ubuntu 或 RHEL 系均可），1 核 1G 足够（Go + SQLite 常驻约 30MB） |
| 本机 | Go 1.27+（用于交叉编译） |
| 出网 | 服务器需能访问 `api.deepseek.com:443`（LLM 调用） |
| 安全组 | 只放行 **22 / 80 / 443**；**不要放行 8899** |

## 2. 本机交叉编译

```bash
# Git Bash / WSL / macOS
bash deploy/build-linux.sh            # 默认 amd64，ARM 机器用 arm64
```

PowerShell 等价命令：

```powershell
$env:GOOS="linux"; $env:GOARCH="amd64"; $env:CGO_ENABLED="0"
go build -trimpath -ldflags "-s -w" -o dist/grammarchecker ./server
```

产物约 10–11 MB（未加 `-s -w` 约 21 MB）。

## 3. 上传

```bash
scp dist/grammarchecker-linux-amd64 root@<服务器IP>:/tmp/grammarchecker
scp -r deploy root@<服务器IP>:/tmp/
```

## 4. 安装（服务器上）

```bash
ssh root@<服务器IP>
cd /tmp/deploy && bash install.sh /tmp/grammarchecker
```

脚本会：建专用系统用户 `grammar` → 装二进制到 `/opt/grammarchecker` → 装 systemd 单元 →
生成 env 文件模板。**首次安装不会启动服务**，这是刻意的（先建好账号再对外）。

> 首次务必先设置服务器时区，否则趋势页按天分组会偏移（时间字段存的是本地时区）：
> ```bash
> timedatectl set-timezone Asia/Shanghai
> ```

## 5. 填写密钥并启动

```bash
nano /etc/grammarchecker/env
```

```bash
GC_API_KEY=sk-你的DeepSeek密钥
```

> 2026-09-29 多用户改造后，`GC_AUTH_USER` / `GC_AUTH_PASS` 环境变量已废弃（改为登录页会话认证）。
> 从旧版本升级的部署请把这两行从 env 文件删掉，避免困惑。

```bash
systemctl enable --now grammarchecker
systemctl enable --now grammarchecker-backup.timer   # 每日备份（需先 apt install -y sqlite3）
curl -s http://127.0.0.1:8899/api/ping               # 期望 {"ok":true}
```

### 5.1 创建首个管理员账号（必做）

没有用户时服务是「单用户开放模式」，公网访问等于无保护。创建第一个用户后即开启登录：

```bash
sudo -u grammar /opt/grammarchecker/grammarchecker \
  -db /var/lib/grammarchecker/grammar.db -add-user admin -admin
# 按提示输入两次密码；也可用 -password 'xxx' 便于脚本化
```

- 首个用户自动**认领升级前单用户时代的历史数据**；
- 之后增删用户、重置密码都在网页「配置」页操作（管理员登录可见），不必再登服务器；
- 验证：`curl -s http://127.0.0.1:8899/api/auth/me` 应返回
  `{"username":null,"is_admin":false,"auth_required":true}`，浏览器打开页面会进入登录视图。

## 6. 反向代理 + 自签 HTTPS

```bash
# Debian/Ubuntu 安装 Caddy（官方源）
apt install -y debian-keyring debian-archive-keyring apt-transport-https curl
curl -1sLf 'https://dl.cloudsmith.io/public/caddy/stable/gpg.key' | gpg --dearmor -o /usr/share/keyrings/caddy-stable-archive-keyring.gpg
curl -1sLf 'https://dl.cloudsmith.io/public/caddy/stable/debian.deb.txt' | tee /etc/apt/sources.list.d/caddy-stable.list
apt update && apt install -y caddy

cp /tmp/deploy/Caddyfile /etc/caddy/Caddyfile
systemctl reload caddy
```

浏览器或手机访问 `https://<服务器IP>`，提示证书不受信任时点「高级 → 继续前往」——
自签证书的正常表现；随后是应用自己的登录页。

### 替代方案：SSH 隧道（零公网暴露）

若不想开放任何端口，可跳过第 6 步，在本地执行：

```bash
ssh -N -L 8899:127.0.0.1:8899 root@<服务器IP>
```

然后浏览器打开 `http://127.0.0.1:8899`。此时安全组只需放行 22；
已创建用户时隧道内同样需要登录。

## 7. 升级

```bash
# 本机重新编译后
scp dist/grammarchecker-linux-amd64 root@<IP>:/tmp/grammarchecker
ssh root@<IP> 'cd /tmp/deploy && bash install.sh /tmp/grammarchecker'
```

脚本检测到 env 文件已存在，会直接重启服务。数据库在 `/var/lib/grammarchecker`，
与程序目录分离，升级不会触碰。

## 8. 备份与恢复

备份由 timer 每日执行，落在 `/var/backups/grammarchecker`，保留 14 天。

```bash
systemctl list-timers grammarchecker-backup.timer   # 查看下次执行
systemctl start grammarchecker-backup.service       # 手工触发一次
journalctl -u grammarchecker-backup -n 20           # 查看结果
```

恢复：

```bash
systemctl stop grammarchecker
rm -f /var/lib/grammarchecker/grammar.db-wal /var/lib/grammarchecker/grammar.db-shm   # 必须删，否则与新库不匹配
cp /var/backups/grammarchecker/grammar-<时间戳>.db /var/lib/grammarchecker/grammar.db
chown grammar:grammar /var/lib/grammarchecker/grammar.db
systemctl start grammarchecker
```

> **不要用 `cp grammar.db` 做备份**。WAL 模式下最新事务可能还在 `-wal` 文件里，
> 单独拷主库会得到「看起来完整、实际丢数据」的备份。脚本用的是 `sqlite3 .backup`，已规避。

## 9. 排障

| 现象 | 排查方向 |
|---|---|
| 服务起不来 | `journalctl -u grammarchecker -n 50 --no-pager`；确认 8899 未被占用 |
| 打开页面停在登录视图 | 未登录属正常；无法登录用 `5.1` 的命令行再建一个管理员 |
| 忘记管理员密码 | 用 `5.1` 命令行新建管理员（`-admin`），登录后重置旧账号密码 |
| 登录后立刻又跳回登录页 | 手机浏览器禁了 Cookie（gc_session）；允许 Cookie 后重试 |
| 接口 401 unauthorized | 未登录或 30 天会话过期，重新登录即可 |
| 检查/辨析报 `502 llm_upstream` | 服务器出网被限，或 `GC_API_KEY` 无效：`curl -s https://api.deepseek.com` |
| 报 `api_key_missing` | `GC_API_KEY` 未填且数据库里也没有密钥 |
| 趋势页日期错位 | 时区不是 `Asia/Shanghai`；改时区后需 `systemctl restart grammarchecker` |
| 想确认密钥有没有泄漏面 | 登录后 `curl -s -H "Cookie: gc_session=<会话>" http://127.0.0.1:8899/api/settings`，`api_key` 应为空串、`api_key_source` 为 `env` |

## 10. 安全边界（务必知道）

- **`8899` 绝不能出现在公网**。多用户登录是入口保护，不是「可以裸奔」的理由——
  仍必须经 TLS 反代（口令与正文内容走 HTTP 等于明文）。
- **创建首个用户前是开放模式**。没有用户时服务不要求登录（桌面单机语义），
  服务器部署务必在启动后立刻执行 `5.1` 建管理员。
- **密钥不落盘**：`GC_API_KEY` 生效后不写数据库，`GET /api/settings` 只回空串 +
  `api_key_source: "env"`，配置页输入框自动置灰。
- **密码与会话只存哈希**：users 表存 bcrypt，sessions 表存 token 的 SHA-256，
  数据库文件泄露也无法直接得到可用凭据；会话有效期 30 天。
- **用户数据隔离**：错词本/例句/辨析/表达/检查历史按用户隔离；删除用户会连带清除其数据。
  设置（API KEY、复用开关）为全局共用，管理员与普通用户一致。
