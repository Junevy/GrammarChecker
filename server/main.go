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
	"strings"
	"syscall"
	"time"

	assets "grammarchecker"
	"grammarchecker/server/api"
	"grammarchecker/server/llm"
	"grammarchecker/server/store"
	"grammarchecker/server/tray"

	"golang.org/x/term"
)

func main() {
	log.SetFlags(log.LstdFlags)

	var (
		addr      = flag.String("addr", "127.0.0.1:8899", "HTTP 监听地址（默认仅本机回环）")
		dbPath    = flag.String("db", "", "SQLite 文件路径（默认：可执行文件同目录 grammar.db）")
		noBrowser = flag.Bool("no-browser", false, "启动后不自动打开浏览器")
		noTray    = flag.Bool("no-tray", false, "以控制台模式运行（默认启用系统托盘）")
		addUser   = flag.String("add-user", "", "创建用户后退出（不启动服务），配合 -admin / -password；用于部署形态引导首个管理员账号")
		isAdmin   = flag.Bool("admin", false, "配合 -add-user：创建的用户为管理员")
		password  = flag.String("password", "", "配合 -add-user：直接指定密码（缺省时交互式输入，不回显）")
	)
	flag.Parse()

	// 数据层：打开并自动建表（幂等）
	st, err := store.Open(*dbPath)
	if err != nil {
		log.Fatalf("初始化数据库失败: %v", err)
	}
	defer st.Close()

	// 账号管理 CLI：创建用户后立即退出。首个用户会自动认领
	// 单用户时代的历史数据（user_id=0 → 该用户），见 store.ClaimLegacyData。
	if *addUser != "" {
		if err := runAddUser(st, *addUser, *password, *isAdmin); err != nil {
			log.Fatalf("创建用户失败: %v", err)
		}
		return
	}

	// 装配 LLM 客户端与 HTTP 路由
	llmClient := llm.NewClient(llm.DefaultConfig())

	// 安全告警（2026-09-29 加固）：无用户 = 单用户开放模式（API 完全无鉴权），
	// 此时若监听非回环地址，等于把数据与 LLM 额度向所在网络敞开——明确提示而非静默。
	if host, _, err := net.SplitHostPort(*addr); err == nil &&
		host != "127.0.0.1" && host != "::1" && host != "localhost" {
		if hasUsers, err := st.HasUsers(); err == nil && !hasUsers {
			log.Printf("安全警告: 监听地址 %s 为非回环地址，且尚未创建任何用户（接口完全开放）。"+
				"请尽快执行 -add-user 创建账号开启登录保护，或改回 127.0.0.1。", *addr)
		}
	}
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

// runAddUser -add-user CLI 的执行体：校验用户名/密码 → 首个用户认领旧数据 → 落库。
// 密码来源：-password 参数（便于脚本）；缺省时终端交互式输入两次（不回显）。
func runAddUser(st *store.Store, username, password string, admin bool) error {
	username = strings.TrimSpace(username)
	if username == "" {
		return fmt.Errorf("用户名不能为空")
	}
	if len(password) < 6 {
		if password != "" {
			return fmt.Errorf("密码长度至少 6 位")
		}
		// 交互式输入（不回显）；非终端环境（如管道）直接报错
		if !term.IsTerminal(int(os.Stdin.Fd())) {
			return fmt.Errorf("非终端环境必须用 -password 指定密码")
		}
		fmt.Printf("为 %s 设置密码（至少 6 位，输入不回显）:\n", username)
		pw1, err := term.ReadPassword(int(os.Stdin.Fd()))
		if err != nil {
			return fmt.Errorf("读取密码失败: %w", err)
		}
		fmt.Println("再次输入以确认:")
		pw2, err := term.ReadPassword(int(os.Stdin.Fd()))
		if err != nil {
			return fmt.Errorf("读取密码失败: %w", err)
		}
		fmt.Println()
		if string(pw1) != string(pw2) {
			return fmt.Errorf("两次输入的密码不一致")
		}
		password = string(pw1)
		if len(password) < 6 {
			return fmt.Errorf("密码长度至少 6 位")
		}
	}

	u := &store.User{Username: username, IsAdmin: admin}
	if err := st.CreateUser(u, password); err != nil {
		if errors.Is(err, store.ErrConflict) {
			return fmt.Errorf("用户名 %s 已存在", username)
		}
		return err
	}
	// 首个用户 = 多用户模式开启点：把单用户时代（user_id=0）的数据划归该用户
	if has, err := firstUserNow(st); err == nil && has {
		n, err := st.ClaimLegacyData(u.ID)
		if err != nil {
			log.Printf("警告：旧数据认领失败: %v", err)
		} else if n > 0 {
			log.Printf("已将 %d 条历史数据划归用户 %s", n, username)
		}
	}
	role := "普通用户"
	if admin {
		role = "管理员"
	}
	log.Printf("用户 %s（%s，ID=%d）创建成功", username, role, u.ID)
	return nil
}

// firstUserNow 判断刚创建的是否为库中首个用户（仅当用户总数为 1）。
func firstUserNow(st *store.Store) (bool, error) {
	users, err := st.ListUsers()
	if err != nil {
		return false, err
	}
	return len(users) == 1, nil
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
