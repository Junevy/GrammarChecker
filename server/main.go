// GrammarChecker 后端入口。
//
// 启动后监听 127.0.0.1:8899（仅本机回环，AGENTS.md 第 3 节技术决策），提供：
//  1. /api/** REST JSON 接口（契约见 api/readme.md，共 17 个接口路径）
//  2. 由 go:embed 嵌入的前端 SPA 静态服务
//
// 运行模式：
//   - 默认启用系统托盘（server/tray）：左键单击/菜单「打开页面」唤起浏览器，
//     菜单「退出」优雅停机；配合 `go build -ldflags "-H windowsgui"` 可无 cmd 窗口运行；
//   - `-no-tray`：控制台模式，便于开发期观察日志与调试。
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"syscall"
	"time"

	assets "grammarchecker"
	"grammarchecker/server/api"
	"grammarchecker/server/llm"
	"grammarchecker/server/store"
	"grammarchecker/server/tray"
)

func main() {
	log.SetFlags(log.LstdFlags)

	var (
		addr      = flag.String("addr", "127.0.0.1:8899", "HTTP 监听地址（默认仅本机回环）")
		dbPath    = flag.String("db", "", "SQLite 文件路径（默认：可执行文件同目录 grammar.db）")
		noBrowser = flag.Bool("no-browser", false, "启动后不自动打开浏览器")
		noTray    = flag.Bool("no-tray", false, "以控制台模式运行（默认启用系统托盘）")
	)
	flag.Parse()

	// 数据层：打开并自动建表（幂等）
	st, err := store.Open(*dbPath)
	if err != nil {
		log.Fatalf("初始化数据库失败: %v", err)
	}
	defer st.Close()

	// 装配 LLM 客户端与 HTTP 路由
	llmClient := llm.NewClient(llm.DefaultConfig())
	srv := &http.Server{
		Addr:              *addr,
		Handler:           api.NewHandler(st, llmClient).Routes(assets.FS),
		ReadHeaderTimeout: 5 * time.Second,
	}

	// 先监听成功，再对外宣告并拉起浏览器
	ln, err := net.Listen("tcp", *addr)
	if err != nil {
		log.Fatalf("监听 %s 失败: %v", *addr, err)
	}
	url := fmt.Sprintf("http://%s", *addr)
	log.Printf("GrammarChecker 已启动: %s", url)

	// shutdown 优雅停机：等待在途请求完成（最长 5s）
	shutdown := func(reason string) {
		log.Printf("%s，正在退出…", reason)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	}

	if *noTray {
		// 控制台模式：Ctrl+C / 终止信号触发停机
		idle := make(chan struct{})
		go func() {
			sig := make(chan os.Signal, 1)
			signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
			<-sig
			shutdown("收到终止信号")
			close(idle)
		}()
		if !*noBrowser {
			openBrowser(url)
		}
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("HTTP 服务异常退出: %v", err)
		}
		<-idle
		log.Println("已退出")
		return
	}

	// 托盘模式：HTTP 服务在后台协程，主协程阻塞在托盘事件循环；
	// 退出路径有三——托盘菜单「退出」、终止信号、服务异常，均汇聚到优雅停机。
	go func() {
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
		<-sig
		shutdown("收到终止信号")
		tray.Quit()
	}()
	go func() {
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("HTTP 服务异常退出: %v", err)
		}
	}()
	if !*noBrowser {
		openBrowser(url)
	}
	tray.Run(tray.Options{
		URL:    url,
		Title:  "GrammarChecker",
		OnOpen: func() { openBrowser(url) },
		OnQuit: func() { shutdown("收到退出请求") },
	})
	log.Println("已退出")
}

// openBrowser 调用系统默认浏览器打开 URL；失败静默处理（不影响服务本身）。
func openBrowser(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	case "darwin":
		cmd = exec.Command("open", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	_ = cmd.Start()
}
