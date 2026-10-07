/* 后端二进制构建脚本：交叉编译 Go 后端到 electron/bin/
 *
 * 用法：
 *   node scripts/build-backend.js        当前平台（npm run backend，开发态够用）
 *   node scripts/build-backend.js --all  全部 5 个目标（打包分发前用，npm run backend:all）
 *
 * 产物命名 grammarchecker-<goos>-<goarch>[.exe]，与 main.js 的 backendBin() 对齐；
 * 纯 Go（CGO_ENABLED=0）+ -trimpath -ldflags "-s -w"，Windows 另加 -H windowsgui 隐藏控制台。
 */
const { spawnSync } = require('child_process');
const path = require('path');
const fs = require('fs');

const ROOT = path.resolve(__dirname, '..', '..'); // 仓库根（go.mod 所在）
const OUT = path.join(__dirname, '..', 'bin');
const ALL = process.argv.includes('--all');

/* [goos, goarch] 组合；产物名统一用 Electron 的 process.platform 命名
 * （windows→win32），与 main.js 的 backendBin() 严格对齐。
 * 限制：darwin 目标走 cgo（fyne.io/systray 的 macOS 实现用 objc），
 * 无法从 Windows/Linux 交叉编译（需 osxcross），只能在 macOS 本机构建。 */
const TARGETS = [
  ['windows', 'amd64', 'win32'],
  ['darwin', 'amd64', 'darwin'],
  ['darwin', 'arm64', 'darwin'],
  ['linux', 'amd64', 'linux'],
  ['linux', 'arm64', 'linux'],
];

function build([goos, goarch, platName]) {
  const exe = goos === 'windows' ? '.exe' : '';
  const out = path.join(OUT, `grammarchecker-${platName}-${goarch}${exe}`);
  const ldflags = goos === 'windows' ? '-s -w -H windowsgui' : '-s -w';
  // CGO 仅 darwin 需要（且只能在 darwin 本机开）；其余目标纯 Go 关闭以保交叉编译
  const env = Object.assign({}, process.env, { GOOS: goos, GOARCH: goarch });
  if (goos !== 'darwin') env.CGO_ENABLED = '0';
  const r = spawnSync('go', ['build', '-trimpath', '-ldflags', ldflags, '-o', out, './server'], {
    cwd: ROOT,
    stdio: 'inherit',
    env,
  });
  if (r.status !== 0) {
    throw new Error(`go build 失败: ${goos}/${goarch}`);
  }
  console.log(`[ok] ${path.relative(ROOT, out)}`);
}

fs.mkdirSync(OUT, { recursive: true });
const curGoarch = process.arch === 'x64' ? 'amd64' : process.arch;
let targets = ALL ? TARGETS : TARGETS.filter(([g, a, p]) => process.platform === p && a === curGoarch);
// darwin 目标只能在 macOS 本机构建（systray cgo）；非 darwin 宿主上跳过并说明
const skipped = [];
targets = targets.filter(([goos, goarch]) => {
  if (goos === 'darwin' && process.platform !== 'darwin') {
    skipped.push(`${goos}/${goarch}`);
    return false;
  }
  return true;
});
for (const s of skipped) {
  console.log(`[skip] grammarchecker-${s}：darwin 走 cgo，请在 macOS 本机执行 npm run backend 生成`);
}
if (!targets.length) {
  if (skipped.length) {
    console.log('当前宿主无可构建目标（仅剩 darwin），见上方说明');
    process.exit(0);
  }
  console.error(`当前平台 ${process.platform}/${process.arch} 不在支持列表，请用 --all`);
  process.exit(1);
}
for (const t of targets) build(t);
