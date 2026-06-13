package helperapp

import (
	"context"
	"fmt"
	"html"
	"net"
	"net/http"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/AutoCONFIG/uapi-helper/internal/helperclient"
)

func (a *App) startLogin() {
	a.mu.Lock()
	if a.loginRunning {
		a.mu.Unlock()
		return
	}
	a.loginRunning = true
	cfg := a.cfg
	a.mu.Unlock()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		a.loginDoneWithError("启动登录服务失败: " + err.Error())
		return
	}
	serverURL := "http://" + listener.Addr().String()
	mux := http.NewServeMux()
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			writeLoginPage(w, cfg, "")
		case http.MethodPost:
			if err := r.ParseForm(); err != nil {
				writeLoginPage(w, cfg, "请求格式错误")
				return
			}
			uapiServer := strings.TrimRight(strings.TrimSpace(r.FormValue("server_url")), "/")
			email := strings.TrimSpace(r.FormValue("email"))
			password := r.FormValue("password")
			if uapiServer == "" || email == "" || password == "" {
				writeLoginPage(w, Config{ServerURL: uapiServer, Email: email}, "服务器、邮箱和密码都不能为空")
				return
			}
			ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
			defer cancel()
			login, err := helperclient.New(uapiServer).Login(ctx, email, password)
			if err != nil {
				writeLoginPage(w, Config{ServerURL: uapiServer, Email: email}, "登录失败: "+err.Error())
				return
			}
			if err := a.completeLogin(uapiServer, email, login); err != nil {
				writeLoginPage(w, Config{ServerURL: uapiServer, Email: email}, "保存登录状态失败: "+err.Error())
				return
			}
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = fmt.Fprint(w, successPage)
			go func() {
				time.Sleep(500 * time.Millisecond)
				_ = srv.Shutdown(context.Background())
			}()
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})

	go func() {
		if err := srv.Serve(listener); err != nil && err != http.ErrServerClosed {
			a.loginDoneWithError("登录服务异常: " + err.Error())
		}
	}()
	if err := openURL(serverURL); err != nil {
		a.loginDoneWithError("无法打开浏览器: " + err.Error())
		_ = srv.Shutdown(context.Background())
	}
	go func() {
		time.Sleep(5 * time.Minute)
		_ = srv.Shutdown(context.Background())
		a.mu.Lock()
		if a.loginRunning {
			a.loginRunning = false
		}
		a.mu.Unlock()
	}()
}

func (a *App) loginDoneWithError(message string) {
	a.mu.Lock()
	a.loginRunning = false
	a.lastErr = message
	a.mu.Unlock()
	a.updateMenu()
	notify("UAPI 小助手", message)
}

func writeLoginPage(w http.ResponseWriter, cfg Config, message string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	body := strings.ReplaceAll(loginPage, "{{server_url}}", html.EscapeString(cfg.ServerURL))
	body = strings.ReplaceAll(body, "{{email}}", html.EscapeString(cfg.Email))
	if message != "" {
		body = strings.ReplaceAll(body, "{{message}}", `<div class="error">`+html.EscapeString(message)+`</div>`)
	} else {
		body = strings.ReplaceAll(body, "{{message}}", "")
	}
	_, _ = fmt.Fprint(w, body)
}

func openURL(url string) error {
	switch runtime.GOOS {
	case "windows":
		return exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start()
	case "darwin":
		return exec.Command("open", url).Start()
	default:
		return exec.Command("xdg-open", url).Start()
	}
}

const loginPage = `<!doctype html>
<html lang="zh-CN">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>UAPI 小助手登录</title>
  <style>
    body { font-family: system-ui, -apple-system, "Segoe UI", sans-serif; margin: 0; background: #f5f5f5; color: #222; }
    main { max-width: 360px; margin: 64px auto; padding: 20px; background: #fff; border: 1px solid #ddd; border-radius: 6px; }
    h1 { font-size: 18px; margin: 0 0 16px; }
    label { display: block; font-size: 13px; margin: 12px 0 6px; color: #444; }
    input { width: 100%; box-sizing: border-box; height: 34px; padding: 6px 8px; border: 1px solid #bbb; border-radius: 4px; font-size: 14px; }
    button { margin-top: 16px; width: 100%; height: 36px; border: 0; border-radius: 4px; background: #111; color: #fff; font-size: 14px; cursor: pointer; }
    .error { margin-bottom: 12px; padding: 8px; background: #fff0f0; border: 1px solid #e4b0b0; color: #9f1d1d; border-radius: 4px; font-size: 13px; }
  </style>
</head>
<body>
  <main>
    <h1>UAPI 小助手登录</h1>
    {{message}}
    <form method="post">
      <label for="server_url">服务器地址</label>
      <input id="server_url" name="server_url" value="{{server_url}}" placeholder="https://uapi.example.com" required>
      <label for="email">邮箱</label>
      <input id="email" name="email" value="{{email}}" autocomplete="username" required>
      <label for="password">密码</label>
      <input id="password" name="password" type="password" autocomplete="current-password" required>
      <button type="submit">登录</button>
    </form>
  </main>
</body>
</html>`

const successPage = `<!doctype html>
<html lang="zh-CN">
<head><meta charset="utf-8"><title>登录成功</title></head>
<body style="font-family: system-ui, sans-serif; padding: 32px;">
  <h1 style="font-size: 18px;">登录成功</h1>
  <p>可以关闭这个页面，UAPI 小助手会在托盘继续运行。</p>
</body>
</html>`
