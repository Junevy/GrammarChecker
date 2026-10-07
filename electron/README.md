# GrammarChecker · Electron 桌面壳

把现有「Go 后端 + 内嵌前端」原样包成桌面 App：**后端与前端零改动**，壳只负责拉起子进程与承载窗口，页面布局与浏览器访问完全一致。

## 架构

```
Electron 主进程（本目录）
  ├─ 挑空闲端口 → spawn 后端子进程：
  │     grammarchecker-<平台>-<架构> -addr 127.0.0.1:<port> \
  │         -db <userData>/grammar.db -no-tray -no-browser
  ├─ 轮询 /api/ping 就绪 → BrowserWindow 加载 http://127.0.0.1:<port>
  ├─ 托盘（tray.png）：关窗隐藏常驻，菜单「打开页面 / 退出」
  ├─ 单实例锁：二次启动唤起已有窗口
  └─ 退出时杀掉后端子进程
```

要点：
- **两种模式（托盘菜单切换，配置存 `%APPDATA%/GrammarChecker/shell-config.json`）**：
  - **本地模式（默认）**：拉起本地后端，数据在本机 SQLite；
  - **服务器模式**：不拉起后端，窗口直接加载已部署服务器的 URL（`deploy/` 部署形态），
    登录服务器账号，数据全部在云端——与手机浏览器访问完全同一套账号与数据。
    启动时先探测 `/api/ping`，不可达时给「重试 / 改用本地模式 / 退出」选择，绝不静默换数据源。
- **自签证书**：服务器模式仅对配置中的那台服务器放行证书错误（deploy/Caddyfile 的
  `tls internal` 自签 IP 场景），其余站点一律拒绝；要更正规可从服务器导出 Caddy 根 CA
  装入本机信任。
- **数据库落 userData 目录**（本地模式 `%APPDATA%/GrammarChecker/grammar.db`），与程序目录解耦；
- 托盘由壳实现（后端传 `-no-tray`），避免双图标；本地形态仍是单用户模式，登录体系只在服务器模式出现；
- 后端异常退出会弹窗告知并退出壳，不留僵尸进程（已实测）；
- **开发态与已安装版数据隔离**：dev 的 userData 为 `%APPDATA%/GrammarChecker-dev`，
  调试不会动正式数据，也不会与正在运行的正式版互顶单实例锁。

## 开发

```bash
cd electron
npm install          # 下载 Electron（国内网络可加环境变量
                     #   ELECTRON_MIRROR=https://npmmirror.com/mirrors/electron/）
npm run backend      # 编译当前平台后端到 bin/
npm start            # 启动桌面 App（开发态）
npm run smoke        # 无界面冒烟：拉起后端→加载页面→1.5s 后自动退出，输出 SMOKE_OK
npx electron . --smoke --server-url https://<服务器IP>   # 服务器模式冒烟（不可达输出 SMOKE_FAIL）
```

## 打包分发

```bash
npm run backend:all  # win32-amd64 + linux-amd64/arm64（darwin 见下方限制）
npm run dist         # electron-builder → dist-electron/（win: NSIS 安装包；linux: AppImage；mac: DMG）
```

**平台限制**：后端托盘库 `fyne.io/systray` 的 macOS 实现走 cgo（objc），**darwin 目标无法从 Windows/Linux 交叉编译**——请在 macOS 本机进 `electron/` 执行 `npm run backend`（脚本会自动跳过并提示非本平台目标）。Linux 与 Windows 目标均为纯 Go，任意平台可交叉编译。

**签名**：对外分发需按平台签名（macOS 还需 Apple 开发者账号公证）， electron-builder 配置见 `package.json` 的 `build` 段。

## 文件

| 文件 | 说明 |
|---|---|
| `main.js` | 壳主进程（spawn / 端口 / 就绪轮询 / 托盘 / 模式切换 / 单实例 / 退出联动 / `--smoke`） |
| `preload.js` | 服务器地址输入窗的 IPC 桥（仅暴露 submit，无 Node 能力） |
| `scripts/build-backend.js` | 后端交叉编译脚本（`npm run backend` / `backend:all`） |
| `bin/` | 构建产物（gitignore，不入库） |
| `icon.png` / `assets/tray.png` | 应用图标（512²）/ 托盘图标（256²）——均由 `app/Grammar_checker_icon.png` 中心裁切生成（PowerShell System.Drawing） |
| `dist-electron/` | 打包产物（gitignore，不入库） |
