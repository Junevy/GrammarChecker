# GrammarChecker 模块交接文档

> 面向后续接手其他模块（Go 后端 / 数据层 / LLM 接入 / 打包发布）的开发者或 Agent。

---

## 1. 项目介绍

**轻量英语语法检查网页工具**：前后端分离，Go 后端 + 本地 SQLite，浏览器访问，LLM 仅做外呼。默认部署在个人电脑（API KEY 只存本地）；亦可部署到私有云服务器（见 `deploy/`），此时密钥与访问口令由服务器环境变量托管、对外经 TLS 反代访问。

## 2. 交付物与现状

| 模块 | 状态 | 位置 |
|---|---|---|
| UI 设计稿（零依赖单文件，**仅作视觉基准，非交付前端**） | ✅ 完成 | `app/base_style.html` |
| 前端SPA - Vue 3 版（正式前端：动画/毛玻璃/液化，经四轮交互迭代 + 2026-09-29 移动端响应式适配） | ✅ 完成 | `app/vue/`（index.html + css/ + js/，Vue 3.4 本地化于 js/vendor/） |
| 产品技术文档 + 交互说明（含 API 草案、表设计、验收清单） | ✅ 完成 | `docs/GrammarChecker-产品技术文档与交互说明.html` |
| 前端对接 API 手册（按页整理，含请求/响应 JSON 示例，标注已实现/待实现） | ✅ 完成 | `api/README.md`（联调对接的前端侧唯一依据；新接口须先更新此文件） |
| Go 后端 | 🔶 功能完成（可运行；26 接口路径全通，LLM 四接口已接通 DeepSeek——prompt 初稿待人员审改；2026-09-29 多用户体系落地） | `server/` |
| SQLite 数据层 | ✅ 完成（6 业务表 + comp_infos 字典表 DDL+索引+CRUD+seed + users/sessions 多用户表，单测覆盖；业务表含 `user_id` 列，`ensureColumn` 老库自动补列） | `server/store/` |
| 系统托盘（运行后最小化，无 cmd 窗口） | ✅ 完成（fyne.io/systray 纯 Go；动态生成图标；`go build -ldflags "-H windowsgui"` 构建无控制台版本，`-no-tray` 保留控制台开发模式） | `server/tray/` |
| 前后端联调（真实 API 替换 mock） | ✅ 完成（2026-09-25，全部页面走真实接口，playwright 无头实测通过） | `app/vue/js/api.js`（fetch 封装）+ `app/vue/js/app.js` |
| 云服务器部署套件（可选形态） | ✅ 完成（2026-09-26：交叉编译脚本 + systemd 单元 + Caddy 自签 TLS 反代 + 每日备份 timer；密钥与访问口令全部环境变量化。2026-09-29：认证改为后端登录页会话，Caddy basic_auth 与 GC_AUTH_* 已移除） | `deploy/`（手册见 `deploy/README.md`） |
| 桌面壳 Electron（可选形态，2026-09-30） | ✅ 完成（后端/前端零改动：壳拉起后端子进程 `-addr 临时端口 -db userData -no-tray -no-browser`，窗口加载现有页面；托盘/单实例/退出联动由壳实现，smoke 实测通过。**限制：darwin 后端走 cgo 须在 macOS 本机构建**） | `electron/`（说明见 `electron/README.md`） |

## 3. 技术决策（已定，勿推翻）

- **后端**：Go，单可执行文件，运行后最小化到托盘。监听 `127.0.0.1:8899`（REST + JSON）。
- **部署形态**（2026-09-26，2026-09-29 认证升级）：除桌面单机外支持部署到云服务器（`deploy/`）。桌面与服务器的差异**全部由启动参数与环境变量表达，不改代码分支**：服务器侧用 `-no-tray -no-browser`（Linux 无桌面 dbus 会话，托盘不可用、无图形界面）+ `-db` 显式指定数据路径（默认落可执行文件同目录，升级覆盖 `/opt` 时不利于备份与权限隔离）；密钥改由环境变量 `GC_API_KEY` 注入（不落库、不回传页面）。公网访问必须经反向代理 + TLS，`8899` 永不出公网。
- **多用户体系**（2026-09-29 人员确认四决策）：① 账号管理 = CLI 建首个账号（`-add-user <名> [-admin] [-password]`）+ 管理员配置页网页管理（增删/重置密码，`api/users_handlers.go`）；② API KEY 与「辨析复用」等设置**全局共用**（不按用户隔离）；③ **users 表为空 = 单用户开放模式**，认证中间件完全关闭（桌面形态零变化），存在用户后即强制登录；④ 旧单用户数据（user_id=0）在创建首个用户时自动划归该用户。认证为会话 Cookie `gc_session`（HttpOnly/SameSite=Lax/TLS 下 Secure，30 天，库内只存 SHA-256 哈希，密码 bcrypt）；Basic Auth（GC_AUTH_USER/PASS）已被取代并整体移除。数据隔离经 `store.ForUser(uid)` 用户视图（装饰器，接口签名不变）；登录失败统一 401 且延迟 300ms；401 不带 WWW-Authenticate（SPA 自绘登录视图接管）。
- **移动端适配**（2026-09-29）：CSS 断点 1080 / 860 / 480px（style.css 末尾响应式区）；≤860px 侧边栏变抽屉（transform 移出屏幕而非 display:none，保住液态滑块 offsetTop 测量；开合后手动补测）+ 顶部栏；`.app` 用 `100dvh` 防移动端地址栏跳动；替换气泡 hover 规则包 `@media (hover: hover)`，触屏走 `.bubble-open` 类（app.js 按 `matchMedia('(hover: hover)')` 分支挂 click）。
- **安全加固基线**（2026-09-29 审查后固化，勿回退）：SQL 全占位符（表名仅代码字面量）；改密/重置即吊销本人全部会话（UpdateUserPassword 事务内删 sessions）；全局设置多用户下仅管理员可写、密钥明文仅管理员可读（非管理员 GET 掩码、PUT 403，单用户模式不变）；前端唯一 innerHTML 动态插值点已改 textContent（气泡 chip，LLM 输出不可信）；静态资源禁目录列表；监听非回环 + 无用户启动时打安全告警；Caddy 加 X-Frame-Options/nosniff/Referrer-Policy（不加 CSP——Vue 运行时编译需 unsafe-eval）。
- **HTTP 路由**：`chi`（v5，2026-09-25 人员确认）；中间件为自研 JSON 版——panic 兜底（500 `internal_error`）与 60s 超时（503 `timeout`），保证错误响应统一 `{code,message}` 结构（chi 自带组件触发时返回纯文本，已弃用）。
- **SQLite 驱动**：`modernc.org/sqlite`（纯 Go 免 CGO，技术文档推荐且本机无 gcc），全局 `CGO_ENABLED=0`。
- **SQLite 连接**：单连接（`SetMaxOpenConns(1)`）+ `busy_timeout(5000)` + WAL，规避本地单用户写锁竞争。
- **数据访问抽象**（2026-09-25）：`server/store/repository.go` 定义 `Repository` 接口（24 方法），api 层只依赖接口、零 SQL 感知；`*Store`（SQLite）为唯一实现，编译期断言保证契约。换库时按契约新增实现并在 `main.go` 装配点替换，方言差异（日期过滤/UPSERT/自增主键）由实现自行消化。
- **响应出口约定**（2026-09-25 结构审查）：列表接口一律经 `respond.go` 的泛型 `writeList` 输出（nil 统一序列化为 `[]`，禁止单写 nil 兜底）；错误一律经 `fail`；LLM 结果入库序列化一律经 `mustMarshal`。文件命名：LLM 处理器在 `api/llm_handlers.go`（旧 `placeholder.go` 已更名）。
- **前端嵌入**：`go:embed` 将 `app/vue` 打进二进制（仓库根 `assets.go`），双击即用，无运行时依赖。模块根必须在仓库根（`go.mod`），否则 embed 无法引用 `app/`。
- **数据库**：SQLite 单文件 `grammar.db`，本地存储，6 张业务表 + 1 张字典表：
  错词本 / 例句 / 辨析 / 表达 / 检查历史 / 设置 KV + 成分说明字典 `comp_infos`（启动时预置 23 角色语法知识 seed，运行期只读，查询不调 LLM——2026-09-25 人员确认）。
  完整 DDL 字段见技术文档第 3 节；默认落位「可执行文件同目录」，`-db` 启动参数可覆盖。
- **API**：26 个接口路径在技术文档第 4 节接口表（检查、收藏 CRUD、历史、设置、趋势、成分说明、收藏标记、单条辨析删除、用法示例；认证 login/logout/me/password + 用户管理 users 增删改查；另含 /api/ping 探活）；前端对接契约与请求/响应示例见 `api/README.md`（冲突时以 `api/README.md` 为准）。
- **收藏语义**（2026-09-25 人员确认）：收藏 = 记录上的 `favorite` 标记位（POST/DELETE favorite 子资源，幂等写入、取消不删记录），不单独建收藏表；列表用 `?favorite=1/0` 过滤。
- **LLM**：唯一外呼点。供应商 **DeepSeek**（`deepseek-chat`，OpenAI 兼容 + JSON 输出模式，2026-09-25 人员确认）；API KEY 取值口径为「环境变量 `GC_API_KEY` 优先、回退设置表」（集中在 `server/api/apikey.go`，2026-09-26）：桌面形态存设置表、配置页掩码输入 + 显隐；部署形态由环境变量托管，`GET /api/settings` 只回 `api_key:""` + `api_key_source:"env"`，配置页输入框自动置灰只读。prompt 模板初稿在 `server/llm/prompts.go`（待人员审改固化）。

## 4. 约定

### 基本约定
- 模糊、含有歧义的需求必须进行提问，确定清楚后再进行开发。
- 动手之前先检索是否存在更好的实现方式，若存在更好的实现方式则必须由人员确认。
- 代码必须模块化，进行解耦，要考虑扩展性和维护性。
- 每次开发完成后必须更新 memory.md。
- 务必将前端文件分类存放，不得随意放置文件。可参考目录约定，对目录进行扩展。例如：引用vue后，新建app/vue文件夹。

### 目录约定
```
GrammarChecker/
├── AGENTS.md          ← 本文件
├── go.mod             ← Go 模块定义（必须在仓库根，embed 依赖）
├── assets.go          ← go:embed app/vue，静态资源打进二进制
├── app/
│   ├── base_style.html ← UI 设计稿（零依赖单文件，视觉基准）
│   └── vue/            ← 前端SPA（Vue 3 版，文件分类存放）
│       ├── index.html  ← 页面模板（Vue 根组件挂载点）
│       ├── img/logo.png ← 品牌 logo（app/Grammar_checker_icon.png 压缩 128px 版，favicon 同源）
│       ├── css/style.css ← 设计 Token + 毛玻璃 + 液化背景 + 动效
│       └── js/
│           ├── vendor/vue.global.prod.js ← Vue 3.4 本地化（离线可用，配合 go:embed）
│           ├── api.js    ← API 请求层（fetch 封装，契约唯一对照 api/README.md）
│           └── app.js    ← Vue 应用（状态 / 数据加载映射 / 液态滑块 / 倒计时 / 涟漪指令）
├── docs/…交互说明.html ← 技术文档（API/表/验收清单，实现时的唯一依据）
├── api/README.md      ← 前端对接 API 手册（按页面整理，标注已实现/待实现）
├── deploy/            ← 云服务器部署套件（build-linux.sh / install.sh / systemd 单元 / Caddyfile / backup.sh / README.md）
├── electron/          ← 桌面壳（可选形态）：main.js 主进程 + scripts/build-backend.js 交叉编译 + icon/tray 资源 + README
├── memory/            ← 该项目的修改记录与记忆
└── server/            ← Go 后端（功能完成，可运行）
    ├── main.go        ← 入口：-addr / -db / -no-browser / -no-tray / -add-user（CLI 建用户）；默认托盘模式，优雅停机
    ├── api/           ← chi 路由 + handler + 统一响应 + JSON 化超时/panic 中间件 + 会话认证（auth.go，Basic Auth 已移除）+ 密钥解析（apikey.go）
    ├── store/         ← SQLite 6 业务表（含 user_id）+ users/sessions 多用户表 + comp_infos 字典（含 seed 数据 comp_seed.go）+ ForUser 用户视图（userscope.go）+ 迁移 + 单测
    ├── llm/           ← DeepSeek 客户端（JSON 输出模式）+ 四套 prompt 模板（初稿待审改）
    └── tray/          ← 系统托盘（fyne.io/systray；icon.go 动态生成图标，打开页面/退出菜单，左键单击打开）
```

## 5. 前端设计总结（Vue 3 版，已实现）

**架构**：Vue 3.4（`js/vendor/` 本地化，禁止 CDN，配合 go:embed 离线打包）+ Options API 单应用；
页面切换用 `<transition mode="out-in">`，全部动效带 `prefers-reduced-motion` 降级；
数据层已对接真实 API（2026-09-25）：`js/api.js` 封装全部 fetch（同源相对路径），`app.js` 启动时并行加载并映射后端字段；
API 报错统一走顶部 toast（`api_key_missing` 自动跳配置页），空态/加载态齐备。原 mock 文件 `js/data.js` 已废弃（内容仅余说明注释，可删除）。

**视觉体系**（Token 已写进 `css/style.css` CSS 变量，新颜色/圆角一律走变量）：

- 基础色：主蓝 `#0066CC`；纸面灰 `#F5F5F7`；发丝线 `#E0E0E0`；错误红文字 `#C62A2F` / 实心 `#E5484D`；正向绿 `#1F9D55`；灰字 `#7A7A7E`
- 字体 Inter；圆角：大卡 18 / 内衬卡 12 / 按钮 8 / 胶囊 999
- **毛玻璃**：侧边栏、卡片、历史弹出卡、弹窗、下拉菜单统一 `backdrop-filter: blur(22px) saturate(1.7)` + 半透明白 + 发丝高光边框
- **液化**：背景 4 个持续变形流动的渐变斑点（blur 70px，毛玻璃的光源）；导航与仓库选项卡为「液态滑块」——测量 DOM 位置后弹簧曲线滑动
- **侧边栏**：128px 窄栏（人员确认收窄），顶部为品牌 logo 图标（`img/logo.png`，40px 居中 + 悬停 title 提示产品名，2026-09-25 人员提供新图标替换原文字 logo），导航激活态蓝底白字圆角 8
- **紧凑模式**：检查页保持 `padding:40px`；其余五页挂 `.compact`（padding 26px 32px、正文字号 15px、标题 30px）
- 类型筛选胶囊：**背景**红色透明度递减 1→0.7→0.42→0.24→0.12，文字统一深红 `#A02227`
- 历史记录卡统一外壳：宽 520、标题行（历史记录 + 条数 + ✕）、内缩 20px 分隔线、清空按钮

**动效**：页面转场（位移+模糊）、卡片交错入场（`--i` 变量）、列表增删过渡、按钮涟漪（`v-ripple` 自定义指令）、5s 倒计时进度环、趋势折线 drawLine + 数据点弹出 + 统计数字滚动、Toggle 弹性开关。

## 6. 页面与交互规则摘要（细节以技术文档第 6/7 节为准 + 四轮迭代增量）

- **6 个功能页**：检查 / 仓库（三选项卡：先前错误、例句、辨析）/ 趋势 / 辨析 / 表达 / 配置。
- **检查页**：Ctrl+Enter 检查；**全部错误逐个标红**（2026-09-30 起，此前仅标红第一条），每个标红词弹出**各自的替换气泡**（错误类型 + 一键替换，只替换自身；全部替换完才取消倒计时并显示句尾绿色对号）——桌面 hover 触发、触屏（`hover: none`）点按触发、点空白处收起；标红定位按 fix 左片段在原文搜索（LLM 不回传位置），已占用区间去重 + 重叠段丢弃（`computeMarks` 定位 / `wrapMarks` 包裹 / `renderEditorErrors` 编排，位置存 `checkMarks`，业务基准恒为本次送检原文）；**切页往返后标红/气泡/对号按状态重画**（2026-10-07 起，`restoreEditorView` 挂 after-enter——contenteditable 被 out-in 转场销毁，恢复副本经 `src` 回链同步替换状态，业务标记不动）；句子无错时句尾追加**圆形绿色对号** + 绿色结果卡，有错才出现「加入错词本」5 秒倒计时 + 进度环（可再点取消；结束逐条 POST **未替换**的错误，corrected_sentence 只修自身单差异点）；结果卡标题行有「收藏例句」按钮（POST /api/sentences，source=check + analysis_json=struct，按 checkMarks 位置精确修正，双向切换可取消）。
- **结构分析**：检查页为**内嵌结构卡**；仓库页先前错误卡片 → went 结构分析弹窗、例句卡片 → received 结构分析弹窗。三种结构卡/弹窗内的**成分胶囊均可点击**，弹出成分说明卡（是什么/有什么用/怎么用，DB 存储）+「生成用法示例」（LLM）。
- **仓库页**：类型胶囊 + 日期下拉（全部/最近7/14/30天）+ 搜索框 **AND 组合筛选**；三个搜索框占位文案——搜索单词 / 搜索例句 / 搜索收藏的解析。三个子选项卡的列表头均有「编辑」按钮（编辑 ⇄ 完成，切换子选项卡自动退出）：编辑态下卡片右上角浮出「删除」角标，**二次点击确认**后调 DELETE 接口（4s 未确认自动还原），编辑态点击卡片不弹详情窗；`DELETE /api/discriminations/{id}` 为本轮新增（2.8）。
- **趋势页**：按错误类型下拉重算折线（后端 `type` 筛选已实现，口径见 `api/README.md` 5.1）。
- **辨析页 / 表达页**：结果卡「收藏」按钮**双向切换**（收藏 ⇄ 已收藏，走 `POST/DELETE …/favorite`，占位响应不带 id 时按单词/原文从历史列表反查最新记录）；历史复用行为受配置页「辨析复用历史记录」Toggle 控制（改动即 PUT 保存）。
- **弹窗共 4 种**：结构分析（统一动态弹窗，内容解析记录的 `analysis_json`，无缓存时展示词条基础信息）/ 场景辨析（解析辨析记录的 `result_json`）/ 成分说明（`GET /api/comp-info`，404 回退本地默认说明，role 按「·」分段归一化后请求）/ 重置用户密码（管理员，配置页入口）。
- **登录与用户管理**（2026-09-29）：启动先 `GET /api/auth/me`——`auth_required && 未登录` 时展示**登录视图**（居中毛玻璃卡，替代 `.app` 主壳）；任意接口 401 `unauthorized` 时经 `gc:unauthorized` 事件切回登录视图。侧边栏底部显示当前用户名；配置页多用户模式下显示「账号（退出登录）」「用户管理（仅管理员：列表/添加/删除/重置密码，删除二次确认）」「修改密码」三张卡，单用户模式全部隐藏。
- **层叠约定**：弹层一律挂在 `.page-header`（z-index:10）或 `.list-head`（z-index:5）内，避免被带入场动画的卡片遮挡（Chrome 对 transform 动画元素持续保留层叠上下文）。
- **命名澄清**：「生词本」是早期废弃叫法，正式名称为「例句」。

## 7. 编码与协作约定

- 前端 HTML/CSS/JS 分类存放于 `app/vue/`；Vue 依赖本地化于 `js/vendor/`，不得改为 CDN 外链（需离线 + go:embed）。
- 新颜色/圆角一律走 CSS 变量，不写裸值；新增动效须遵守 `prefers-reduced-motion` 降级。
- **响应式约定**（2026-09-29）：断点只有 1080/860/480 三个（style.css 末尾「响应式区」），新组件须在三个断点下检查；hover 类交互必须同时提供触屏路径，`:hover` 规则包 `@media (hover: hover)`；固定宽度禁止超出 `min(NNpx, calc(100vw - 32px))` 形态。
- **液态滑块防回归**：测量必须挂页面 transition 的 `@after-enter`（out-in 模式下 nextTick 时新 DOM 尚未插入），并在 `document.fonts.ready` 后重测一次（字体就绪会改变文本宽度）；移动端抽屉开合后需手动补测（watch drawerOpen）。
- **弹层防遮挡**：新弹层挂在 `.page-header` / `.list-head` 内（见第 6 节层叠约定），勿裸挂在页面卡片里。
- 接口对接以 `api/README.md` 为前端侧唯一依据；新增/改动接口顺序：先更新 `api/README.md` → 再更新技术文档 → 再改后端 → 最后同步本文件第 3 节。
- 交互行为（倒计时、Toggle 联动、筛选组合）以技术文档第 7 节「通用交互规范」为验收标准。
- 后端仅监听 127.0.0.1；不要引入需管理员权限或系统服务的依赖。

## 8. 验收 / 验证方式

1. 前端视觉：`app/vue/index.html` 直接浏览器打开核对（视觉基准仍为 `app/base_style.html`）。
2. 交互验收：技术文档末尾 11 条验收清单 + 四轮迭代新增交互（替换气泡、绿色对号、成分弹窗、收藏切换、日期下拉筛选）逐条核对；可用 playwright-core + 系统 Edge 无头脚本实测（本机已装于 `C:\Users\COWAIN\.workbuddy\binaries\node\workspace`），重点断言控制台零错误。
3. 后端：`go build ./... && go vet ./... && go test ./...` 全绿（store 单测 + api 处理器层单测 `server/api/router_test.go`：辨析复用短路、wrong-words POST、错误码归一、text 500 字符校验、近义词搜索、comp-info 查询/404、收藏幂等与过滤、LLM 四接口全链路——fake LLM 注入不外呼；`store_users_test.go`：用户隔离/旧数据认领/会话生命周期/认证口径；`deploy_test.go`：会话认证 401 边界 + 单用户模式回归 + 管理员守卫 + API KEY 环境变量托管）；运行后 `curl 127.0.0.1:8899/api/ping` 及各接口冒烟。已通过全量冒烟（静态 200、设置读写回环、例句 CRUD、LLM 类接口无 KEY 400 / 假 KEY 对 DeepSeek 真实端点 502 llm_upstream——证明上游链路打通、趋势 type 筛选、成分说明与收藏契约、windowsgui 托盘模式服务存活）。
4. 无窗口运行验证：`go build -ldflags "-H windowsgui" -o grammarchecker.exe ./server` 后双击——无 cmd 窗口、托盘出现蓝底白勾图标、左键单击打开页面、菜单「退出」可停机；开发期用 `go build ./server`（带控制台看日志）或 `-no-tray`。
5. 多用户 / 移动端（2026-09-29）：CLI 建号 → 登录视图 → 双用户数据隔离 → 网页用户管理逐条手测；playwright 无头脚本分 1440×900（回归）与 390×844（无横向溢出、抽屉开合、tap 气泡）两档视口实测。

## 9. 已知待办 / 开放问题

- [ ] LLM prompt 初稿待人员审改固化：四接口已接通 DeepSeek（`deepseek-chat`，JSON 输出模式），模板在 `server/llm/prompts.go`，审改后只需改常量文案，不动结构。
- [ ] CORS：调试环境暂不加（同源无跨域）；若前端改用独立 dev server 跨端口联调或后续上线服务器，需增加仅限本机的 CORS 中间件（2026-09-25 人员确认挂待办）。
- [ ] 趋势「正确率」口径未定：暂按「零错误次数 / 总检查次数」（type 筛选时为「不含该类型错误 / 总数」）计算，需产品确认。
- [ ] 趋势折线渲染已修复（2026-09-25）：补空档 + y 轴 clamp（<60% 贴底）+ 有效点 <2 显示单点提示；累积 ≥2 天数据后自动连成曲线。
- [ ] 自动备份策略未定（数据库路径已定：可执行文件同目录，`-db` 可覆盖）。
- [ ] 空状态 / loading / 错误三态：✅ 已随联调落地（toast + 空态提示 + 按钮 loading），如需骨架屏另行迭代。
- [ ] 清理 `_verify_tmp/`、`_verify.tmp`、根目录 `node_modules`/`package.json`（沙箱禁止删除，可手动清理；`_verify_tmp/` 截图可留作效果参考）。

## 10. 项目记忆

工作日志与约定存于 `memory/`（`MEMORY.md` 为长期约定，日期文件为日志），接手前可快速浏览。
