// Package grammarchecker 提供嵌入的前端静态资源。
//
// go:embed 要求被嵌入的文件位于模块目录内，而前端 SPA 位于仓库根的 app/，
// 故嵌入声明放在仓库根模块完成，由 server/ 引用，
// 实现「单可执行文件、双击即用、无运行时依赖」的交付形态（AGENTS.md 第 3 节）。
package grammarchecker

import "embed"

// FS 为嵌入的前端 SPA（app/vue 目录：Vue 3 全局构建，零打包依赖）。
// 注意：目录内以下划线开头的条目（如 _verify_tmp 截图临时目录）不会被嵌入。
//
//go:embed app/vue
var FS embed.FS
