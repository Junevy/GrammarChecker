# GrammarChecker

本地优先的英语语法学习工具。**Go + Vue 3 编译为单个可执行文件**，数据全部存在本机 SQLite，语法检查 / 近义词辨析 / 地道表达由 DeepSeek 大模型驱动，无需安装任何运行时依赖。

> 个人电脑本地部署：启动后监听 `127.0.0.1:8899`（仅本机回环），自动拉起浏览器，支持系统托盘常驻。

<img width="1905" height="899" alt="image" src="https://github.com/user-attachments/assets/e951fce8-1f98-4ec4-86c4-3ef15cc15dff" />

## 功能

### 📝 检查
- 输入英文句子一键检查（Ctrl+Enter），返回**翻译 / 错误原因 / 正确示例 / 地道表达建议**
- 错误词**标红**，悬停弹出**替换气泡**，一键修正
- 句子无错时句尾追加**绿色对号**；有错时出现「加入错词本」5 秒倒计时（可再点取消）
- **句子结构分析**：句子成分 / 句子结构 / 从句分析 / 时态；成分胶囊可点击查看**用法说明**（内置 23 类语法成分知识库，本地查询不调 LLM）
- 一键**收藏例句**（含结构分析缓存），复习时直接回看

### 📚 仓库
- 三个选项卡：**先前错误**（错词本）/ **例句** / **辨析**
- 错误类型胶囊 + 日期区间 + 关键词**三重 AND 组合筛选**
- 点击卡片弹出结构分析弹窗，回顾错误语境与正确改法

### 📈 趋势
- 近 14 天错误数量折线图，可按错误类型筛选

### 🔍 辨析
- 输入单词（或近义词组），LLM 输出**核心区别 + 各词适用场景 + 场景例句**
- 「复用历史记录」开关开启时优先命中本地缓存，不重复消耗 LLM
- 结果可收藏，进入仓库回顾

### 💬 表达
- 输入中文，获得**推荐译法**及口语 / 书面 / 简洁三种风格的场景变体
- 收藏常用表达

### ⚙️ 配置
- DeepSeek API KEY 管理（仅保存在本地数据库）
- 辨析复用历史记录开关

## 技术栈

| 层 | 选型 |
|---|---|
| 后端 | Go 1.27 · [chi](https://github.com/go-chi/chi) 路由 · [modernc.org/sqlite](https://pkg.go.dev/modernc.org/sqlite)（纯 Go 免 CGO）· [fyne.io/systray](https://github.com/fyne-io/systray) 系统托盘 |
| 前端 | Vue 3.4（依赖本地化，配合 `go:embed` 离线可用）· 原生 CSS（毛玻璃 / 液化视觉效果） |
| LLM | DeepSeek（`deepseek-chat`，JSON 输出模式，OpenAI 兼容格式） |
| 存储 | SQLite 单文件 `grammar.db`（错词本 / 例句 / 辨析 / 表达 / 历史 / 设置） |
| 交付 | `go:embed` 嵌入前端，编译产物为**单一可执行文件** |

## 快速开始

### 环境要求
- [Go](https://go.dev/) 1.27+（仅构建需要）
- [DeepSeek API Key](https://platform.deepseek.com/)（使用 LLM 功能需要）

### 构建与运行

```bash
# 在仓库根目录执行（前端经 go:embed 打包进二进制）
go build -o grammarchecker.exe ./server

# Windows 下隐藏 cmd 窗口（托盘模式）
go build -ldflags "-H windowsgui" -o grammarchecker.exe ./server
```

直接运行可执行文件即可：启动后自动打开浏览器，并在系统托盘常驻（托盘菜单可「打开页面」/「退出」）。

### 命令行参数

| 参数 | 默认值 | 说明 |
|---|---|---|
| `-addr` | `127.0.0.1:8899` | HTTP 监听地址 |
| `-db` | exe 同目录 `grammar.db` | SQLite 文件路径 |
| `-no-browser` | `false` | 启动后不自动打开浏览器 |
| `-no-tray` | `false` | 控制台模式运行（便于开发调试） |

### 首次使用
1. 启动后进入 **配置** 页，填入 DeepSeek API Key 并保存（仅存本机）；
2. 回到 **检查** 页输入句子即可开始。

## 数据与隐私

- 所有用户数据（错词、例句、历史、API Key）只存储在本机 SQLite 文件中；
- 仅语法检查 / 辨析 / 表达 / 用法示例四类请求会发送至 DeepSeek；
- 服务仅监听本机回环地址 `127.0.0.1`，不暴露局域网；
- ⚠️ `grammar.db` 含 API Key，**请勿提交到版本库或分享给他人**（已在 `.gitignore` 中排除）。

## 项目结构

```
GrammarChecker/
├── assets.go            # go:embed 嵌入 app/vue
├── app/
│   ├── base_style.html  # 视觉基准稿
│   └── vue/             # 前端 SPA（index.html / css / js，Vue 本地化）
├── server/
│   ├── main.go          # 入口：监听 / 托盘 / 优雅停机
│   ├── api/             # REST 处理器（18 个接口路径）+ 中间件 + 单测
│   ├── store/           # SQLite 数据层（含成分说明 seed、单测）
│   ├── llm/             # DeepSeek 客户端与 prompt 模板
│   └── tray/            # 系统托盘
├── api/README.md        # 接口契约（前端对接唯一依据）
├── AGENTS.md            # 架构决策 / 编码约定 / 验收方式
└── docs/                # 产品技术文档与交互说明
```

## 开发

```bash
go build ./...     # 构建
go vet ./...       # 静态检查
go test ./...      # 单测（store 数据层 + api 处理器层，httptest 全链路）
```

- 前端经 `go:embed` 由后端同源托管，**请通过后端地址访问**（直接双击 `index.html` 无法加载数据）；改动前端后需重新构建才能生效。
- 接口契约以 `api/README.md` 为准；架构与编码约定见 `AGENTS.md`。

## License

[MIT](./LICENSE) © 2026 Junevy
