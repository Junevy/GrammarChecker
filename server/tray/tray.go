// Package tray 封装系统托盘：图标、菜单与单击行为。
//
// 采用 fyne.io/systray（纯 Go，Windows 走 win32 syscall，无 CGO 依赖），
// 图标由 icon.go 动态生成，不引入二进制资源。
package tray

import "fyne.io/systray"

// Options 托盘配置。
type Options struct {
	URL    string // 服务地址，用于托盘提示文字
	Title  string // 托盘标题（macOS 菜单栏文字；Windows 忽略）
	OnOpen func() // 「打开页面」与左键单击图标的回调
	OnQuit func() // 「退出」回调：在 systray.Quit 之前执行，用于服务优雅停机
}

// Run 阻塞运行托盘直至 Quit；返回即表示托盘已销毁、进程可收尾。
func Run(opts Options) {
	onOpen := opts.OnOpen
	if onOpen == nil {
		onOpen = func() {}
	}
	onQuit := opts.OnQuit
	if onQuit == nil {
		onQuit = func() {}
	}

	systray.Run(func() {
		systray.SetIcon(iconICO())
		systray.SetTitle(opts.Title)
		systray.SetTooltip("GrammarChecker " + opts.URL)

		mOpen := systray.AddMenuItem("打开页面", "在默认浏览器中打开 GrammarChecker")
		systray.AddSeparator()
		mQuit := systray.AddMenuItem("退出", "停止服务并退出")

		// 左键单击图标 = 打开页面
		systray.SetOnTapped(onOpen)

		// 菜单事件循环
		go func() {
			for {
				select {
				case <-mOpen.ClickedCh:
					onOpen()
				case <-mQuit.ClickedCh:
					onQuit()
					systray.Quit()
					return
				}
			}
		}()
	}, func() {})
}

// Quit 请求退出托盘（可从任意协程调用，用于信号触发的退出路径）。
func Quit() { systray.Quit() }
