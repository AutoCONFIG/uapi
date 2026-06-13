package helperapp

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/atotto/clipboard"
	"github.com/gen2brain/beeep"
	"github.com/getlantern/systray"

	"github.com/AutoCONFIG/uapi-helper/internal/helperclient"
)

const refreshInterval = 30 * time.Minute

type App struct {
	store     *Store
	autostart AutoStarter

	mu           sync.Mutex
	cfg          Config
	accessToken  string
	summary      *helperclient.Summary
	lastRefresh  time.Time
	lastErr      string
	failures     int
	refreshing   bool
	lastMenuOpen time.Time
	loginRunning bool

	menu menuItems
}

type menuItems struct {
	status    *systray.MenuItem
	user      *systray.MenuItem
	plan      *systray.MenuItem
	valid     *systray.MenuItem
	buckets   []*systray.MenuItem
	updated   *systray.MenuItem
	copyBase  *systray.MenuItem
	copyKey   *systray.MenuItem
	login     *systray.MenuItem
	logout    *systray.MenuItem
	autostart *systray.MenuItem
	quit      *systray.MenuItem
}

func NewApp() (*App, error) {
	store, err := NewStore()
	if err != nil {
		return nil, err
	}
	cfg, _ := store.Load()
	auto := NewAutoStarter()
	cfg.Autostart = auto.IsEnabled()
	return &App{store: store, autostart: auto, cfg: cfg}, nil
}

func (a *App) Run() {
	systray.Run(a.onReady, func() {})
}

func (a *App) onReady() {
	if icon := trayIconPNG(); len(icon) > 0 {
		systray.SetIcon(icon)
	}
	systray.SetTitle("UAPI")
	systray.SetTooltip("UAPI 小助手 | 未登录")
	systray.SetOnMenuOpen(func() {
		go a.refreshForMenuOpen()
	})
	a.buildMenu()
	a.updateMenu()

	go a.restoreAndRefresh()
	go a.refreshLoop()
	go a.handleMenuClicks()
}

func (a *App) buildMenu() {
	a.menu.status = systray.AddMenuItem("状态: 未登录", "")
	a.menu.status.Disable()
	a.menu.user = systray.AddMenuItem("用户: -", "")
	a.menu.user.Disable()
	a.menu.plan = systray.AddMenuItem("套餐: -", "")
	a.menu.plan.Disable()
	a.menu.valid = systray.AddMenuItem("有效期: -", "")
	a.menu.valid.Disable()
	systray.AddSeparator()
	for i := 0; i < 4; i++ {
		item := systray.AddMenuItem("-", "")
		item.Disable()
		a.menu.buckets = append(a.menu.buckets, item)
	}
	a.menu.updated = systray.AddMenuItem("最后更新: -", "")
	a.menu.updated.Disable()
	systray.AddSeparator()
	a.menu.copyBase = systray.AddMenuItem("复制 Base URL", "")
	a.menu.copyKey = systray.AddMenuItem("复制默认 API Key", "")
	systray.AddSeparator()
	a.menu.login = systray.AddMenuItem("登录", "")
	a.menu.logout = systray.AddMenuItem("退出登录", "")
	a.menu.autostart = systray.AddMenuItemCheckbox("开机自启", "", a.cfg.Autostart)
	a.menu.quit = systray.AddMenuItem("关闭", "")
}

func (a *App) handleMenuClicks() {
	for {
		select {
		case <-a.menu.login.ClickedCh:
			go a.startLogin()
		case <-a.menu.logout.ClickedCh:
			a.logout()
		case <-a.menu.copyBase.ClickedCh:
			a.copyBaseURL()
		case <-a.menu.copyKey.ClickedCh:
			a.copyDefaultKey()
		case <-a.menu.autostart.ClickedCh:
			a.toggleAutostart()
		case <-a.menu.quit.ClickedCh:
			systray.Quit()
			return
		}
	}
}

func (a *App) restoreAndRefresh() {
	a.mu.Lock()
	cfg := a.cfg
	a.mu.Unlock()
	if cfg.ServerURL == "" || cfg.Email == "" {
		return
	}
	token, err := a.store.RefreshToken(cfg)
	if err != nil || token == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	client := helperclient.New(cfg.ServerURL)
	login, err := client.Refresh(ctx, token)
	if err != nil {
		a.setError("登录已失效")
		notify("UAPI 小助手", "登录已失效，请重新登录")
		return
	}
	a.mu.Lock()
	a.accessToken = login.AccessToken
	a.mu.Unlock()
	_ = a.store.SaveRefreshToken(cfg, login.RefreshToken)
	a.refresh()
}

func (a *App) refreshLoop() {
	ticker := time.NewTicker(refreshInterval)
	defer ticker.Stop()
	for range ticker.C {
		a.refreshWithTimeout(25*time.Second, false)
	}
}

func (a *App) refresh() {
	a.refreshWithTimeout(25*time.Second, false)
}

func (a *App) refreshForMenuOpen() {
	a.mu.Lock()
	if a.accessToken == "" || a.cfg.ServerURL == "" || a.refreshing || time.Since(a.lastMenuOpen) < 15*time.Second {
		a.mu.Unlock()
		return
	}
	a.lastMenuOpen = time.Now()
	a.mu.Unlock()
	a.refreshWithTimeout(8*time.Second, true)
}

func (a *App) refreshWithTimeout(timeout time.Duration, markRefreshing bool) {
	a.mu.Lock()
	cfg := a.cfg
	token := a.accessToken
	a.mu.Unlock()
	if cfg.ServerURL == "" || token == "" {
		return
	}
	if markRefreshing {
		a.setRefreshing(true)
		defer a.setRefreshing(false)
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	summary, err := helperclient.New(cfg.ServerURL).Summary(ctx, token)
	a.mu.Lock()
	if err != nil {
		a.failures++
		a.lastErr = err.Error()
		a.mu.Unlock()
		a.updateMenu()
		if a.failures >= 3 {
			notify("UAPI 小助手", "连续刷新失败: "+err.Error())
		}
		return
	}
	a.summary = summary
	a.lastRefresh = time.Now()
	a.lastErr = ""
	a.failures = 0
	a.mu.Unlock()
	a.updateMenu()
	a.notifyIfNeeded(summary)
}

func (a *App) setRefreshing(refreshing bool) {
	a.mu.Lock()
	a.refreshing = refreshing
	a.mu.Unlock()
	a.updateMenu()
}

func (a *App) setError(message string) {
	a.mu.Lock()
	a.lastErr = message
	a.failures++
	a.mu.Unlock()
	a.updateMenu()
}

func (a *App) updateMenu() {
	a.mu.Lock()
	defer a.mu.Unlock()
	authenticated := a.accessToken != ""
	loggedIn := authenticated && a.summary != nil
	a.menu.login.Show()
	a.menu.logout.Hide()
	a.menu.copyBase.Hide()
	a.menu.copyKey.Hide()
	a.menu.user.Hide()
	a.menu.plan.Hide()
	a.menu.valid.Hide()
	a.menu.updated.Hide()
	for _, item := range a.menu.buckets {
		item.Hide()
	}

	if !authenticated {
		if a.lastErr != "" {
			a.menu.status.SetTitle("状态: " + a.lastErr)
			systray.SetTooltip("UAPI | " + a.lastErr)
		} else {
			a.menu.status.SetTitle("状态: 未登录")
			systray.SetTooltip("UAPI | 未登录")
		}
		a.menu.login.Show()
		if a.cfg.Autostart {
			a.menu.autostart.Check()
		} else {
			a.menu.autostart.Uncheck()
		}
		return
	}
	if !loggedIn {
		if a.refreshing {
			a.menu.status.SetTitle("状态: 更新中...")
			systray.SetTooltip("UAPI | 更新中...")
		} else if a.lastErr != "" {
			a.menu.status.SetTitle("状态: " + a.lastErr)
			systray.SetTooltip("UAPI | " + a.lastErr)
		} else {
			a.menu.status.SetTitle("状态: 已登录")
			systray.SetTooltip("UAPI | 已登录")
		}
		a.menu.login.Hide()
		a.menu.logout.Show()
		if a.cfg.Autostart {
			a.menu.autostart.Check()
		} else {
			a.menu.autostart.Uncheck()
		}
		return
	}

	s := a.summary
	a.menu.status.SetTitle("状态: 已登录")
	a.menu.user.SetTitle("用户: " + displayUser(s.Profile))
	a.menu.plan.SetTitle("套餐: " + valueOrDash(s.Subscription.PlanName))
	a.menu.valid.SetTitle("有效期: " + compactRange(s.Subscription.StartsAt, s.Subscription.ExpiresAt))
	a.menu.user.Show()
	a.menu.plan.Show()
	a.menu.valid.Show()
	for i, line := range bucketLines(s.Subscription.Windows) {
		if i >= len(a.menu.buckets) {
			break
		}
		a.menu.buckets[i].SetTitle(line)
		a.menu.buckets[i].Show()
	}
	if a.lastRefresh.IsZero() {
		a.menu.updated.SetTitle("最后更新: -")
	} else {
		a.menu.updated.SetTitle("最后更新: " + a.lastRefresh.Format("15:04"))
	}
	if a.refreshing {
		a.menu.status.SetTitle("状态: 更新中...")
	} else if a.lastErr != "" {
		a.menu.status.SetTitle("状态: 刷新失败")
	}
	a.menu.updated.Show()
	a.menu.copyBase.Show()
	a.menu.copyKey.Show()
	a.menu.login.Hide()
	a.menu.logout.Show()
	if a.cfg.Autostart {
		a.menu.autostart.Check()
	} else {
		a.menu.autostart.Uncheck()
	}
	systray.SetTooltip(tooltipFor(s))
}

func (a *App) logout() {
	a.mu.Lock()
	cfg := a.cfg
	a.accessToken = ""
	a.summary = nil
	a.lastErr = ""
	a.failures = 0
	a.mu.Unlock()
	a.store.DeleteRefreshToken(cfg)
	a.updateMenu()
}

func (a *App) copyBaseURL() {
	a.mu.Lock()
	summary := a.summary
	a.mu.Unlock()
	if summary == nil || strings.TrimSpace(summary.PublicBaseURL) == "" {
		notify("UAPI 小助手", "服务端未配置 Public Base URL")
		return
	}
	if err := clipboard.WriteAll(strings.TrimRight(summary.PublicBaseURL, "/")); err != nil {
		notify("UAPI 小助手", "复制 Base URL 失败: "+err.Error())
	}
}

func (a *App) copyDefaultKey() {
	a.mu.Lock()
	summary := a.summary
	a.mu.Unlock()
	if summary == nil {
		return
	}
	key, ok := summary.DefaultKey()
	if !ok {
		notify("UAPI 小助手", "当前账号没有可用 API Key")
		return
	}
	if err := clipboard.WriteAll(key.Key); err != nil {
		notify("UAPI 小助手", "复制 API Key 失败: "+err.Error())
	}
}

func (a *App) toggleAutostart() {
	next := !a.autostart.IsEnabled()
	if err := a.autostart.SetEnabled(next); err != nil {
		notify("UAPI 小助手", "开机自启设置失败: "+err.Error())
	}
	a.mu.Lock()
	a.cfg.Autostart = a.autostart.IsEnabled()
	cfg := a.cfg
	a.mu.Unlock()
	_ = a.store.Save(cfg)
	a.updateMenu()
}

func (a *App) completeLogin(serverURL, email string, login *helperclient.LoginResponse) error {
	cfg := Config{
		ServerURL: strings.TrimRight(strings.TrimSpace(serverURL), "/"),
		Email:     strings.TrimSpace(email),
		Autostart: a.autostart.IsEnabled(),
	}
	if err := a.store.Save(cfg); err != nil {
		return err
	}
	if err := a.store.SaveRefreshToken(cfg, login.RefreshToken); err != nil {
		return err
	}
	a.mu.Lock()
	a.cfg = cfg
	a.accessToken = login.AccessToken
	a.summary = nil
	a.lastErr = ""
	a.failures = 0
	a.loginRunning = false
	a.mu.Unlock()
	a.updateMenu()
	go a.refresh()
	return nil
}

func (a *App) notifyIfNeeded(summary *helperclient.Summary) {
	if strings.TrimSpace(summary.PublicBaseURL) == "" {
		notify("UAPI 小助手", "服务端未配置 Public Base URL")
	}
	if _, ok := summary.DefaultKey(); !ok {
		notify("UAPI 小助手", "当前账号没有可用 API Key")
	}
	for _, bucket := range summary.Subscription.Windows {
		if bucket.Limit <= 0 {
			continue
		}
		remaining := bucket.Remaining * 100 / bucket.Limit
		if remaining <= 20 {
			notify("UAPI 小助手", fmt.Sprintf("%s额度剩余 %d%%", bucketLabel(bucket.Type), remaining))
			return
		}
	}
}

func notify(title, message string) {
	_ = beeep.Notify(title, message, "")
}

func displayUser(profile helperclient.Profile) string {
	if profile.Email != "" {
		return profile.Email
	}
	return valueOrDash(profile.Username)
}

func valueOrDash(value string) string {
	if strings.TrimSpace(value) == "" {
		return "-"
	}
	return strings.TrimSpace(value)
}

func bucketLines(windows []helperclient.SubscriptionWindow) []string {
	items := append([]helperclient.SubscriptionWindow(nil), windows...)
	sort.SliceStable(items, func(i, j int) bool {
		return bucketOrder(items[i].Type) < bucketOrder(items[j].Type)
	})
	lines := make([]string, 0, len(items))
	for _, bucket := range items {
		percent := 0
		if bucket.Limit > 0 {
			percent = bucket.Remaining * 100 / bucket.Limit
		}
		lines = append(lines, fmt.Sprintf("%s: %d%% | %d/%d | 重置 %s",
			bucketLabel(bucket.Type), percent, bucket.Remaining, bucket.Limit, compactTime(bucket.ResetAt)))
	}
	if len(lines) == 0 {
		lines = append(lines, "额度: 无")
	}
	return lines
}

func bucketOrder(kind string) int {
	switch kind {
	case "hour":
		return 1
	case "week":
		return 2
	case "month":
		return 3
	default:
		return 9
	}
}

func bucketLabel(kind string) string {
	switch kind {
	case "hour":
		return "小时"
	case "week":
		return "周"
	case "month":
		return "月"
	default:
		return kind
	}
}

func tooltipFor(summary *helperclient.Summary) string {
	best := helperclient.SubscriptionWindow{}
	for _, bucket := range summary.Subscription.Windows {
		if bucket.Type == "month" {
			best = bucket
			break
		}
		if best.Type == "" || bucketOrder(bucket.Type) > bucketOrder(best.Type) {
			best = bucket
		}
	}
	if best.Type == "" || best.Limit <= 0 {
		return "UAPI " + valueOrDash(summary.Subscription.PlanName)
	}
	return fmt.Sprintf("UAPI %s | %s %d%% | 重置 %s",
		valueOrDash(summary.Subscription.PlanName),
		bucketLabel(best.Type),
		best.Remaining*100/best.Limit,
		compactTime(best.ResetAt),
	)
}
