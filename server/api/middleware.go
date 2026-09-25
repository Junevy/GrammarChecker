package api

// 中间件：panic 兜底与超时兜底的 JSON 化。
//
// chi 自带的 Recoverer / Timeout 在触发时返回纯文本（http.Error /
// http.TimeoutHandler 固定响应），与「非 2xx 统一 {code,message} 结构」的
// 前端约定不符（api/readme.md §0），故自行实现等价逻辑并输出 JSON 错误体。

import (
	"context"
	"log"
	"net/http"
	"runtime"
	"sync"
	"time"
)

// recoverJSON panic 兜底：捕获处理器 panic，记录堆栈并返回 500 JSON。
func recoverJSON(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				buf := make([]byte, 4096)
				n := runtime.Stack(buf, false)
				log.Printf("api panic: %v\n%s", rec, buf[:n])
				fail(w, http.StatusInternalServerError, "internal_error", "服务器内部错误，请重试")
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// withJSONTimeout 超时兜底：超过 d 未完成则中断处理器并返回 503 JSON。
// 参考 http.TimeoutHandler 实现：缓冲 Header、超时后禁止处理器写入、panic 透传。
func withJSONTimeout(d time.Duration) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx, cancel := context.WithTimeout(r.Context(), d)
			defer cancel()
			r = r.WithContext(ctx)

			tw := &timeoutWriter{w: w, h: make(http.Header)}
			done := make(chan struct{})
			panicChan := make(chan any, 1)

			go func() {
				defer func() {
					if p := recover(); p != nil {
						panicChan <- p
					}
					close(done)
				}()
				next.ServeHTTP(tw, r)
			}()

			select {
			case p := <-panicChan:
				panic(p) // 透传给外层 recoverJSON，保持统一 500 JSON
			case <-done:
				tw.flush()
			case <-ctx.Done():
				// 处理器可能恰好同时完成：已写响应则不再叠加超时响应
				if tw.markTimedOut() {
					fail(w, http.StatusServiceUnavailable, "timeout", "请求处理超时，请重试")
				}
			}
		})
	}
}

// timeoutWriter 包装 ResponseWriter：缓冲 Header，超时后丢弃处理器的一切写入，
// 保证超时响应体不被污染（模式同标准库 http.timeoutWriter）。
type timeoutWriter struct {
	w           http.ResponseWriter
	h           http.Header // 缓冲的 Header，超时时整体丢弃
	mu          sync.Mutex
	wroteHeader bool
	timedOut    bool
}

func (t *timeoutWriter) Header() http.Header { return t.h }

func (t *timeoutWriter) WriteHeader(code int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.timedOut {
		t.writeHeaderLocked(code)
	}
}

func (t *timeoutWriter) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.timedOut {
		return 0, http.ErrHandlerTimeout
	}
	if !t.wroteHeader {
		t.writeHeaderLocked(http.StatusOK)
	}
	return t.w.Write(p)
}

// writeHeaderLocked 把缓冲 Header 落到真实响应（需持锁调用）。
func (t *timeoutWriter) writeHeaderLocked(code int) {
	if t.wroteHeader {
		return
	}
	t.wroteHeader = true
	dst := t.w.Header()
	for k, vv := range t.h {
		dst[k] = vv
	}
	t.w.WriteHeader(code)
}

// flush 正常完成时调用：处理器未显式写状态码则补 200 并落缓冲 Header。
func (t *timeoutWriter) flush() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.timedOut && !t.wroteHeader {
		t.writeHeaderLocked(http.StatusOK)
	}
}

// markTimedOut 置超时标记；返回 false 表示处理器已写过响应头（不叠加超时响应）。
func (t *timeoutWriter) markTimedOut() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.timedOut = true
	return !t.wroteHeader
}
