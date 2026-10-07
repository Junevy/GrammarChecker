#!/usr/bin/env bash
#
# GrammarChecker · 数据备份（由 systemd timer 每日调用，也可手工执行）
#
# 为什么要用 sqlite3 .backup 而不是 cp：
#   数据库运行在 WAL 模式下，最新事务可能还留在 grammar.db-wal 里，
#   单 cp 一个 grammar.db 会得到一个「旧且看似完整」的坏备份。
#   .backup 走 SQLite 在线备份 API，产出一致性快照，无需停服。

set -euo pipefail

DB="${GC_DB:-/var/lib/grammarchecker/grammar.db}"
DEST_DIR="${GC_BACKUP_DIR:-/var/backups/grammarchecker}"
KEEP_DAYS="${GC_BACKUP_KEEP_DAYS:-14}"

if ! command -v sqlite3 >/dev/null 2>&1; then
  echo "错误：未找到 sqlite3 命令。Debian/Ubuntu: apt install -y sqlite3；RHEL 系: dnf install -y sqlite" >&2
  exit 1
fi
if [[ ! -f "$DB" ]]; then
  echo "错误：数据库不存在 $DB" >&2
  exit 1
fi

install -d -m 0700 "$DEST_DIR"
STAMP="$(date +%Y%m%d-%H%M%S)"
OUT="$DEST_DIR/grammar-$STAMP.db"

sqlite3 "file:$DB?mode=ro" ".backup '$OUT'"
chmod 0600 "$OUT"

# 备份本身也要校验：打不开或表结构缺失则视为失败并退出非 0，
# 让 systemd 记录失败状态（journalctl -u grammarchecker-backup）
if ! sqlite3 "$OUT" "PRAGMA integrity_check;" | grep -q '^ok$'; then
  echo "错误：备份完整性校验失败 $OUT" >&2
  exit 1
fi

# 清理超期备份
find "$DEST_DIR" -maxdepth 1 -type f -name 'grammar-*.db' -mtime "+$KEEP_DAYS" -delete

echo "备份完成：$OUT（保留最近 $KEEP_DAYS 天）"
ls -lh "$DEST_DIR" | tail -n 5
