#!/usr/bin/env bash
#
# GrammarChecker · 本地交叉编译（产出云服务器用的 Linux 单文件）
#
# 在仓库根目录执行：bash deploy/build-linux.sh [amd64|arm64]
# Windows 上可用 Git Bash / WSL 执行；等价 PowerShell 命令见 deploy/README.md。
#
# 为什么能免工具链交叉编译：SQLite 驱动现用 modernc.org/sqlite（纯 Go），
# 全项目 CGO_ENABLED=0，不依赖 gcc 与目标平台的 C 库。

set -euo pipefail

ARCH="${1:-amd64}"
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
OUT="$ROOT/dist/grammarchecker-linux-$ARCH"

mkdir -p "$ROOT/dist"
cd "$ROOT"

echo "==> 交叉编译 GOOS=linux GOARCH=$ARCH（CGO_ENABLED=0）"
# -trimpath：剥离本机构建路径，产物可复现
# -s -w   ：去掉符号表与 DWARF 调试信息，体积约减半
GOOS=linux GOARCH="$ARCH" CGO_ENABLED=0 \
  go build -trimpath -ldflags "-s -w" -o "$OUT" ./server

echo "==> 产物：$OUT"
ls -lh "$OUT"
echo
echo "上传并安装："
echo "  scp \"$OUT\" root@<服务器IP>:/tmp/grammarchecker"
echo "  scp -r \"$ROOT/deploy\" root@<服务器IP>:/tmp/"
echo "  ssh root@<服务器IP> 'cd /tmp/deploy && bash install.sh /tmp/grammarchecker'"
