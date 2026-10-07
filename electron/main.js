/* ============================================================
 * GrammarChecker · Electron 桌面壳
 *
 * 两种模式（托盘菜单切换，配置持久化在 userData/shell-config.json）：
 *   - 本地模式（默认）：拉起 Go 后端子进程，数据在本机 SQLite；
 *   - 服务器模式：不拉起后端，窗口直接加载已部署服务器的 URL，
 *     登录服务器账号，数据全部在服务器（与手机端同一套账号/数据）。
 *
 * 自签证书：服务器模式若目标主机证书校验失败（deploy/Caddyfile 的 tls internal
 * 自签 IP 场景），仅对配置中的那台服务器放行，其余一律拒绝（防误信任任意站点）。
 *
 * 行为对齐原托盘形态：关窗 → 隐藏到托盘常驻；托盘菜单「打开页面 / 退出」；
 * 单实例锁防多开；本地模式退出时杀后端子进程。
 * ============================================================ */

const { app, BrowserWindow, Tray, Menu, nativeImage, dialog, ipcMain, shell } = require('electron');
const { spawn } = require('child_process');
const net = require('net');
const http = require('http');
const https = require('https');
const path = require('path');
const fs = require('fs');
const { URL } = require('url');

const SMOKE = process.argv.includes('--smoke');
const SMOKE_SERVER_URL = (() => {
  const i = process.argv.indexOf('--server-url');
  return i >= 0 ? process.argv[i + 1] : null;
})();
const DEBUG = !!process.env.GC_ELECTRON_DEBUG;

/* 统一应用名：让 userData（数据库/配置所在）稳定为 %APPDATA%/GrammarChecker，
 * 不随 dev（package.json name=grammarchecker-desktop）与打包形态漂移 */
app.setName('GrammarChecker');

/* 开发态与已安装版数据隔离：独立 userData（GrammarChecker-dev），
 * 避免开发调试动到正式数据，也避免与正在运行的正式版互相顶掉单实例锁 */
if (!app.isPackaged) {
  app.setPath('userData', path.join(app.getPath('appData'), 'GrammarChecker-dev'));
}

let win = null;
let tray = null;
let backend = null;
let quitting = false; // 区分「用户点退出/切模式」与「关窗隐藏到托盘」

/* 单实例锁：二次启动时唤起已有窗口而非开第二个后端 */
const gotLock = app.requestSingleInstanceLock();
if (!gotLock) {
  app.quit();
}

/* ---- 壳配置（模式与服务器地址），存 userData/shell-config.json ---- */
function configPath() {
  return path.join(app.getPath('userData'), 'shell-config.json');
}
function loadConfig() {
  try {
    const c = JSON.parse(fs.readFileSync(configPath(), 'utf8'));
    return {
      mode: c.mode === 'server' ? 'server' : 'local',
      serverUrl: typeof c.serverUrl === 'string' ? c.serverUrl : '',
    };
  } catch (e) {
    return { mode: 'local', serverUrl: '' };
  }
}
function saveConfig(cfg) {
  try { fs.writeFileSync(configPath(), JSON.stringify(cfg, null, 2), 'utf8'); } catch (e) { /* 只读等极端情况忽略 */ }
}
function serverHost(serverUrl) {
  try { return new URL(serverUrl).host; } catch (e) { return null; }
}

/* ---- 后端二进制定位（仅本地模式） ----
 * 开发态 electron/bin/；打包态 resources/bin/
 * 命名 grammarchecker-<platform>-<arch>[.exe]，由 scripts/build-backend.js 产出 */
function backendBin() {
  const arch = process.arch === 'x64' ? 'amd64' : process.arch;
  const exe = process.platform === 'win32' ? '.exe' : '';
  const name = `grammarchecker-${process.platform}-${arch}${exe}`;
  const root = app.isPackaged ? process.resourcesPath : __dirname;
  const p = path.join(root, 'bin', name);
  if (!fs.existsSync(p)) {
    throw new Error(`未找到后端二进制 ${name}，请先在 electron/ 目录执行 npm run backend`);
  }
  return p;
}

/* 挑一个空闲回环端口（先占后放，竞争窗口极小；后端绑定失败会自然报错退出） */
function freePort() {
  return new Promise((resolve, reject) => {
    const srv = net.createServer();
    srv.unref();
    srv.on('error', reject);
    srv.listen(0, '127.0.0.1', () => {
      const { port } = srv.address();
      srv.close(() => resolve(port));
    });
  });
}

/* 轮询本地后端 /api/ping 直到就绪（建表迁移完成后才放行窗口加载） */
function waitReady(port, timeoutMs = 15000) {
  const url = `http://127.0.0.1:${port}/api/ping`;
  const t0 = Date.now();
  return new Promise((resolve, reject) => {
    const tick = () => {
      const req = http.get(url, (resp) => {
        resp.resume();
        if (resp.statusCode === 200) return resolve();
        retry();
      });
      req.on('error', retry);
      req.setTimeout(1500, () => { req.destroy(); retry(); });
    };
    const retry = () => {
      if (Date.now() - t0 > timeoutMs) return reject(new Error('后端启动超时（15s）'));
      setTimeout(tick, 250);
    };
    tick();
  });
}

/* 探测服务器可达性（自签证书场景 rejectUnauthorized=false，仅探测连通） */
function probeServer(serverUrl, timeoutMs = 6000) {
  return new Promise((resolve) => {
    let u;
    try { u = new URL('/api/ping', serverUrl); } catch (e) { return resolve(false); }
    const mod = u.protocol === 'https:' ? https : http;
    const req = mod.get(u, { rejectUnauthorized: false, timeout: timeoutMs }, (resp) => {
      resp.resume();
      resolve(resp.statusCode === 200);
    });
    req.on('error', () => resolve(false));
    req.on('timeout', () => { req.destroy(); resolve(false); });
  });
}

/* ---- 本地后端生命周期 ---- */
function startBackend(port) {
  const dbPath = path.join(app.getPath('userData'), 'grammar.db');
  const bin = backendBin();
  backend = spawn(bin, [
    '-addr', `127.0.0.1:${port}`,
    '-db', dbPath,
    '-no-tray',      // 托盘由本壳实现，避免双图标
    '-no-browser',   // 窗口即页面，不另开浏览器
  ], { stdio: ['ignore', 'pipe', 'pipe'], windowsHide: true });

  const forward = (buf) => { if (DEBUG) process.stdout.write('[backend] ' + buf); };
  backend.stdout.on('data', forward);
  backend.stderr.on('data', forward);
  backend.on('exit', (code) => {
    backend = null;
    if (!quitting) {
      // 后端异常退出：弹窗告知并退出壳（正常退出路径不会走到这里）
      dialog.showErrorBox('GrammarChecker', `后端服务异常退出（code=${code}），应用即将关闭。`);
      app.quit();
    }
  });
  if (DEBUG) console.log('[shell] backend spawned:', bin, 'db:', dbPath);
}

function killBackend() {
  if (backend) {
    try { backend.kill(); } catch (e) { /* 进程已死则忽略 */ }
    backend = null;
  }
}

/* ---- 自签证书放行：仅限配置中的服务器主机 ---- */
function decideCertificate(urlStr) {
  const cfg = loadConfig();
  if (cfg.mode !== 'server' || !cfg.serverUrl) return false;
  const host = serverHost(cfg.serverUrl);
  let reqHost = null;
  try { reqHost = new URL(urlStr).host; } catch (e) { return false; }
  return !!host && reqHost === host; // 其余站点证书错误一律拒绝，防误信任
}
app.on('certificate-error-event', (event, wc, url, error, certificate, callback) => {
  if (decideCertificate(url)) {
    event.preventDefault();
    callback(true);
    return;
  }
  callback(false);
});

/* ---- 窗口 ---- */
function attachWindowHandlers() {
  /* 关窗 = 隐藏到托盘（对齐原「运行后最小化」形态）；托盘菜单「退出」才真正退出 */
  win.on('close', (e) => {
    if (!quitting) {
      e.preventDefault();
      win.hide();
    }
  });
  /* 页面内 window.open 一律走系统浏览器，不开新 Electron 窗口 */
  win.webContents.setWindowOpenHandler(({ url }) => {
    shell.openExternal(url);
    return { action: 'deny' };
  });
  /* 渲染进程发起的请求的证书错误：仅放行配置中的服务器（自签 IP 部署形态） */
  win.webContents.on('certificate-error', (event, url, error, certificate, callback) => {
    event.preventDefault();
    callback(decideCertificate(url));
  });
  win.on('closed', () => { win = null; });
}

/* 窗口图标：Windows 用多尺寸 ico（标题栏 16px 最清晰），缺省回退 png。
 * 注意 icon.ico/icon.png 必须在 package.json 的 build.files 里，否则打包后路径失效 */
function windowIcon() {
  const ico = path.join(__dirname, 'icon.ico');
  if (fs.existsSync(ico)) return ico;
  return path.join(__dirname, 'icon.png');
}

function createWindow(targetUrl) {
  win = new BrowserWindow({
    width: 1440,
    height: 900,
    minWidth: 900,
    minHeight: 600,
    backgroundColor: '#F5F5F7',
    title: 'GrammarChecker',
    autoHideMenuBar: true, // Windows/Linux 隐藏默认菜单栏（页面自带导航）
    icon: windowIcon(),
    show: false,           // ready-to-show 后再显示，避免白屏闪烁
    webPreferences: {
      spellcheck: false,
      nodeIntegration: false,      // 默认值，显式声明安全基线
      contextIsolation: true,
    },
  });
  win.loadURL(targetUrl);
  win.once('ready-to-show', () => win.show());
  attachWindowHandlers();
}

/* ---- 服务器地址输入窗（一个只有输入框的小模态） ---- */
function askServerUrl(existing) {
  return new Promise((resolve) => {
    let answered = false;
    const iw = new BrowserWindow({
      width: 430, height: 180, resizable: false, autoHideMenuBar: true,
      title: '服务器地址',
      backgroundColor: '#F5F5F7',
      webPreferences: {
        preload: path.join(__dirname, 'preload.js'),
        contextIsolation: true,
        nodeIntegration: false,
      },
    });
    ipcMain.once('input-submit', (e, value) => {
      answered = true;
      iw.destroy();
      resolve(String(value || '').trim());
    });
    iw.on('closed', () => { if (!answered) resolve(null); });
    const html = `<!doctype html><html><head><meta charset="utf-8"><style>
      body{font-family:'Segoe UI','Microsoft YaHei',sans-serif;background:#F5F5F7;margin:0;padding:18px;}
      label{font-size:12px;color:#7A7A7E;display:block;margin-bottom:8px;}
      input{width:100%;box-sizing:border-box;padding:9px 12px;border:1px solid #E0E0E0;border-radius:8px;
            font-size:13px;background:#fff;outline:none;}
      input:focus{border-color:#0066CC;box-shadow:0 0 0 3px rgba(0,102,204,.12);}
      button{margin-top:12px;width:100%;padding:9px 0;border:0;border-radius:999px;background:#0066CC;
             color:#fff;font-size:13px;font-weight:600;cursor:pointer;}
    </style></head><body>
      <label>服务器地址（部署手册第 6 步的 https://服务器IP）</label>
      <input id="u" placeholder="https://123.45.67.89" value="${existing || ''}" autofocus>
      <button id="b">保 存</button>
      <script>
        const go = () => window.shell.submit(document.getElementById('u').value);
        document.getElementById('b').onclick = go;
        document.getElementById('u').addEventListener('keydown', e => { if (e.key === 'Enter') go(); });
      </script>
    </body></html>`;
    iw.loadURL('data:text/html;charset=utf-8,' + encodeURIComponent(html));
  });
}

/* 校验并保存服务器地址；返回是否有效 */
async function ensureServerUrl(cfg) {
  if (cfg.serverUrl && /^https?:\/\//i.test(cfg.serverUrl)) return true;
  const input = await askServerUrl(cfg.serverUrl);
  if (!input) return false; // 用户取消
  if (!/^https?:\/\//i.test(input)) {
    dialog.showErrorBox('服务器地址无效', '地址需以 http:// 或 https:// 开头，例如 https://123.45.67.89');
    return false;
  }
  cfg.serverUrl = input.replace(/\/+$/, ''); // 去尾斜杠，统一拼接口径
  saveConfig(cfg);
  return true;
}

/* 切模式 = 保存配置后重启壳（退出前先杀本地后端） */
function switchMode(mode) {
  const cfg = loadConfig();
  cfg.mode = mode;
  saveConfig(cfg);
  app.relaunch();
  quitting = true;
  app.quit();
}

async function onSwitchToServer() {
  const cfg = loadConfig();
  if (cfg.mode === 'server') return;
  if (!(await ensureServerUrl(cfg))) { buildTray(); return; } // 取消输入：还原菜单勾选态
  switchMode('server');
}

/* ---- 托盘 ---- */
function buildTray() {
  const cfg = loadConfig();
  const modeText = cfg.mode === 'server' ? `服务器模式（${cfg.serverUrl || '未配置地址'}）` : '本地模式（数据在本机）';
  const icon = nativeImage.createFromPath(path.join(__dirname, 'assets', 'tray.png'));
  if (!tray) {
    tray = new Tray(icon);
    tray.on('click', () => { if (process.platform !== 'darwin' && win) { win.show(); win.focus(); } });
  }
  tray.setImage(icon);
  tray.setToolTip('GrammarChecker · ' + modeText);
  tray.setContextMenu(Menu.buildFromTemplate([
    { label: '本地模式（数据在本机）', type: 'radio', checked: cfg.mode !== 'server',
      click: () => { if (cfg.mode !== 'local') switchMode('local'); } },
    { label: '服务器模式（数据在云端）', type: 'radio', checked: cfg.mode === 'server', click: onSwitchToServer },
    { label: cfg.serverUrl ? `服务器地址：${cfg.serverUrl}` : '设置服务器地址…',
      click: async () => { const c = loadConfig(); if (await ensureServerUrl(c)) buildTray(); } },
    { type: 'separator' },
    { label: '打开页面', click: () => { if (win) { win.show(); win.focus(); } } },
    { type: 'separator' },
    { label: '退出', click: () => { quitting = true; app.quit(); } },
  ]));
}

/* ---- 启动流程 ---- */
async function startServerMode(serverUrl) {
  if (DEBUG) console.log('[shell] server mode:', serverUrl);
  createWindow(serverUrl);
}

async function startLocalMode() {
  const port = await freePort();
  startBackend(port);
  await waitReady(port);
  if (DEBUG) console.log('[shell] local mode, port:', port);
  createWindow(`http://127.0.0.1:${port}`);
  return port;
}

/* 服务器不可达时的选择：重试（重启壳）/ 临时切本地 / 退出 */
async function serverUnreachable(serverUrl) {
  if (SMOKE) { // 冒烟模式不弹框，直接判失败便于自动化
    console.error('SMOKE_FAIL 服务器不可达: ' + serverUrl);
    app.exit(1);
    return 'exit';
  }
  const r = await dialog.showMessageBox({
    type: 'warning',
    title: '无法连接服务器',
    message: `无法连接服务器 ${serverUrl}`,
    detail: '请确认：服务器已启动（systemctl status grammarchecker）、地址正确、网络可达（手机浏览器能否打开同地址）。',
    buttons: ['重试', '改用本地模式', '退出'],
    defaultId: 0,
    cancelId: 2,
  });
  if (r.response === 0) { app.relaunch(); quitting = true; app.quit(); return 'retry'; }
  if (r.response === 1) {
    const cfg = loadConfig();
    cfg.mode = 'local'; // 保留 serverUrl，方便下次切回
    saveConfig(cfg);
    return 'local';
  }
  return 'exit';
}

app.on('second-instance', () => {
  if (win) { win.show(); win.focus(); }
});

app.whenReady().then(async () => {
  try {
    /* 冒烟：--server-url 指定则直接验证服务器模式加载，否则走本地模式 */
    const smokeServer = SMOKE && SMOKE_SERVER_URL;

    let mode = 'local';
    let serverUrl = '';
    if (!smokeServer) {
      const cfg = loadConfig();
      mode = cfg.mode;
      serverUrl = cfg.serverUrl;
      if (mode === 'server') {
        if (!serverUrl) {
          if (!(await ensureServerUrl(cfg))) mode = 'local'; // 取消输入 → 本地模式
        }
      }
    } else {
      mode = 'server';
      serverUrl = smokeServer;
    }

    if (mode === 'server') {
      /* 先探测可达性，失败给明确选择（避免静默落到本地数据造成误解） */
      if (!(await probeServer(serverUrl))) {
        const act = await serverUnreachable(serverUrl);
        if (act === 'retry' || act === 'exit') return;
        mode = 'local'; // 改用本地模式
      }
    }

    let localPort = null;
    if (mode === 'server') {
      await startServerMode(serverUrl);
    } else {
      localPort = await startLocalMode();
    }
    buildTray();

    if (SMOKE) {
      console.log(mode === 'server' ? `SMOKE_OK server=${serverUrl}` : `SMOKE_OK port=${localPort}`);
      setTimeout(() => app.quit(), 1500);
    }
  } catch (err) {
    killBackend();
    if (SMOKE) {
      console.error('SMOKE_FAIL', err.message);
      app.exit(1);
    } else {
      dialog.showErrorBox('GrammarChecker', '启动失败：' + err.message);
      app.exit(1);
    }
  }
});

app.on('before-quit', () => {
  quitting = true;
  killBackend();
});

/* 常驻托盘：所有窗口关闭不退出（退出只走托盘菜单 / before-quit） */
app.on('window-all-closed', () => { /* no-op */ });
