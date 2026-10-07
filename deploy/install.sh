#!/usr/bin/env bash
#
# GrammarChecker · 服务器端安装脚本（在云服务器上以 root 执行）
#
# 前置：二进制与整个 deploy/ 目录已上传，例如
#   scp dist/grammarchecker-linux-amd64 root@<IP>:/tmp/grammarchecker
#   scp -r deploy root@<IP>:/tmp/
#   ssh root@<IP> 'cd /tmp/deploy && bash install.sh /tmp/grammarchecker'
#
# 幂等：重复执行用于升级二进制；已存在的 /etc/grammarchecker/env 不会被覆盖。
# 安全：首次安装（env 文件不存在）**不会启动服务**——避免认证未配置好就暴露端口。

set -euo pipefail

BIN_SRC="${1:-/tmp/grammarchecker}"
APP_DIR=/opt/grammarchecker
DATA_DIR=/var/lib/grammarchecker
CONF_DIR=/etc/grammarchecker
ENV_FILE="$CONF_DIR/env"
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

if [[ $EUID -ne 0 ]]; then
  echo "错误：请以 root 执行（sudo bash install.sh <二进制路径>）" >&2
  exit 1
fi
if [[ ! -f "$BIN_SRC" ]]; then
  echo "错误：找不到二进制文件 $BIN_SRC" >&2
  exit 1
fi
for f in grammarchecker.service backup.sh grammarchecker-backup.service grammarchecker-backup.timer; do
  if [[ ! -f "$HERE/$f" ]]; then
    echo "错误：缺少 $HERE/$f，请确认整个 deploy/ 目录已上传" >&2
    exit 1
  fi
done

echo "==> 创建专用系统用户 grammar（无登录 shell，仅用于运行服务）"
id -u grammar >/dev/null 2>&1 || useradd -r -s /usr/sbin/nologin -d "$APP_DIR" grammar

echo "==> 创建目录"
install -d -m 0755 "$APP_DIR"
install -d -m 0750 -o grammar -g grammar "$DATA_DIR"
install -d -m 0700 "$CONF_DIR"

echo "==> 安装二进制与备份脚本"
install -m 0755 "$BIN_SRC" "$APP_DIR/grammarchecker"
install -m 0750 "$HERE/backup.sh" "$APP_DIR/backup.sh"

echo "==> 安装 systemd 单元"
install -m 0644 "$HERE/grammarchecker.service" /etc/systemd/system/grammarchecker.service
install -m 0644 "$HERE/grammarchecker-backup.service" /etc/systemd/system/
install -m 0644 "$HERE/grammarchecker-backup.timer" /etc/systemd/system/

if ! command -v sqlite3 >/dev/null 2>&1; then
  echo "警告：未检测到 sqlite3 命令，每日备份将失败。" >&2
  echo "      Debian/Ubuntu: apt install -y sqlite3 ；RHEL 系: dnf install -y sqlite" >&2
fi

systemctl daemon-reload

if [[ ! -f "$ENV_FILE" ]]; then
  echo "==> 首次安装：生成环境变量文件 $ENV_FILE"
  install -m 0600 "$HERE/env.example" "$ENV_FILE"
  cat <<EOF

────────────────────────────────────────────────────────────
 安装完成，服务尚未启动。
 请依次执行：

 1) 填写 LLM 密钥（对外服务强烈建议配置）：
      nano $ENV_FILE

 2) 启动并设置开机自启：
      systemctl enable --now grammarchecker

 3) 创建首个管理员账号（开启登录保护，手机/公网访问必做）：
      sudo -u grammar $APP_DIR/grammarchecker -db $DATA_DIR/grammar.db -add-user admin -admin
    （按提示输入两次密码；之后可在网页「配置」页增删用户）
    注意：创建首个账号后，访问需登录——这是唯一入口保护，请务必执行。

 4) 确认服务正常：
      curl -s http://127.0.0.1:8899/api/ping
      journalctl -u grammarchecker -n 30 --no-pager

 5) （可选）启用每日自动备份：
      systemctl enable --now grammarchecker-backup.timer

 6) 安装反向代理（TLS + 公网入口）：
      cp Caddyfile /etc/caddy/Caddyfile && systemctl reload caddy
    手机浏览器访问 https://<服务器IP> ，自签证书点「高级 → 继续前往」。
────────────────────────────────────────────────────────────
EOF
else
  echo "==> 环境变量文件已存在，保留不覆盖：$ENV_FILE"
  echo "==> 重启服务以加载新二进制"
  systemctl enable grammarchecker >/dev/null 2>&1 || true
  systemctl restart grammarchecker
  sleep 1
  systemctl --no-pager --lines=10 status grammarchecker || true
fi
