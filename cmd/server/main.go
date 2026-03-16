package main

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"html/template"
	"log"
	"net/http"
	"os"
	"strconv"
	"sync"
	"time"
)

type User struct {
	Username string
	Role     string
}

type SessionStore struct {
	mu       sync.RWMutex
	sessions map[string]User
}

func NewSessionStore() *SessionStore {
	return &SessionStore{sessions: make(map[string]User)}
}

func (s *SessionStore) Create(user User) string {
	tokenBytes := make([]byte, 32)
	_, _ = rand.Read(tokenBytes)
	token := hex.EncodeToString(tokenBytes)
	s.mu.Lock()
	s.sessions[token] = user
	s.mu.Unlock()
	return token
}

func (s *SessionStore) Get(token string) (User, bool) {
	s.mu.RLock()
	user, ok := s.sessions[token]
	s.mu.RUnlock()
	return user, ok
}

func (s *SessionStore) Delete(token string) {
	s.mu.Lock()
	delete(s.sessions, token)
	s.mu.Unlock()
}

type navItem struct {
	Key   string
	Label string
	Href  string
}

type navGroup struct {
	Title string
	Items []navItem
}

type overviewCard struct {
	Title string
	Value string
	Trend string
}

type badge struct {
	Text  string
	Class string
}

type tableRow struct {
	Cols  []string
	Badge badge
}

type tablePanel struct {
	Title   string
	Headers []string
	Rows    []tableRow
}

type quickAction struct {
	Name string
	Desc string
	Href string
}

type dashboardData struct {
	Title        string
	User         User
	Now          string
	Active       string
	PageDesc     string
	NavGroups    []navGroup
	Overview     []overviewCard
	QuickActions []quickAction
	Panels       []tablePanel
}

type pageConfig struct {
	Title       string
	Desc        string
	Overview    []overviewCard
	Panels      []tablePanel
	QuickAction []quickAction
}

type Server struct {
	templates    *template.Template
	sessions     *SessionStore
	adminUser    string
	adminHash    [32]byte
	httpAddr     string
	cookieSecure bool
	pages        map[string]pageConfig
}

func NewServer() (*Server, error) {
	tmpl, err := template.ParseGlob("web/templates/*.html")
	if err != nil {
		return nil, err
	}

	adminUser := getenv("ADMIN_USER", "admin")
	adminPass := getenv("ADMIN_PASSWORD", "spug.cc")
	hash := sha256.Sum256([]byte(adminPass))

	cookieSecure, _ := strconv.ParseBool(getenv("COOKIE_SECURE", "false"))

	s := &Server{
		templates:    tmpl,
		sessions:     NewSessionStore(),
		adminUser:    adminUser,
		adminHash:    hash,
		httpAddr:     getenv("HTTP_ADDR", ":8080"),
		cookieSecure: cookieSecure,
	}
	s.pages = s.buildPages()
	return s, nil
}

func (s *Server) routes() http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.Dir("web/static"))))
	mux.HandleFunc("/login", s.login)
	mux.HandleFunc("/logout", s.logout)

	mux.HandleFunc("/", s.auth(s.page("dashboard")))
	mux.HandleFunc("/apps", s.auth(s.page("apps")))
	mux.HandleFunc("/hosts", s.auth(s.page("hosts")))
	mux.HandleFunc("/scripts", s.auth(s.page("scripts")))
	mux.HandleFunc("/crontab", s.auth(s.page("crontab")))
	mux.HandleFunc("/pipelines", s.auth(s.page("pipelines")))
	mux.HandleFunc("/approvals", s.auth(s.page("approvals")))
	mux.HandleFunc("/alarms", s.auth(s.page("alarms")))
	mux.HandleFunc("/users", s.auth(s.page("users")))
	mux.HandleFunc("/roles", s.auth(s.page("roles")))
	mux.HandleFunc("/audit", s.auth(s.page("audit")))
	mux.HandleFunc("/settings", s.auth(s.page("settings")))
	return mux
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		if _, ok := s.currentUser(r); ok {
			http.Redirect(w, r, "/", http.StatusSeeOther)
			return
		}
		s.render(w, "login.html", map[string]any{"Title": "登录 Spug 风格控制台"})
		return
	}

	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	username := r.FormValue("username")
	password := r.FormValue("password")

	given := sha256.Sum256([]byte(password))
	if username != s.adminUser || subtle.ConstantTimeCompare(s.adminHash[:], given[:]) != 1 {
		s.render(w, "login.html", map[string]any{
			"Title": "登录 Spug 风格控制台",
			"Error": "用户名或密码错误",
		})
		return
	}

	token := s.sessions.Create(User{Username: username, Role: "超级管理员"})
	http.SetCookie(w, &http.Cookie{
		Name:     "spug_session",
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   s.cookieSecure,
		SameSite: http.SameSiteLaxMode,
		Expires:  time.Now().Add(8 * time.Hour),
	})
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	cookie, err := r.Cookie("spug_session")
	if err == nil {
		s.sessions.Delete(cookie.Value)
	}
	http.SetCookie(w, &http.Cookie{Name: "spug_session", Value: "", MaxAge: -1, Path: "/"})
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

func (s *Server) page(key string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cfg, ok := s.pages[key]
		if !ok {
			http.NotFound(w, r)
			return
		}

		user, _ := s.currentUser(r)
		data := dashboardData{
			Title:        cfg.Title,
			User:         user,
			Now:          time.Now().Format("2006-01-02 15:04:05"),
			Active:       key,
			PageDesc:     cfg.Desc,
			NavGroups:    navigationGroups(),
			Overview:     cfg.Overview,
			QuickActions: cfg.QuickAction,
			Panels:       cfg.Panels,
		}
		s.render(w, "dashboard.html", data)
	}
}

func (s *Server) auth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, ok := s.currentUser(r); !ok {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		next(w, r)
	}
}

func (s *Server) currentUser(r *http.Request) (User, bool) {
	cookie, err := r.Cookie("spug_session")
	if err != nil {
		return User{}, false
	}
	return s.sessions.Get(cookie.Value)
}

func (s *Server) render(w http.ResponseWriter, name string, data any) {
	if err := s.templates.ExecuteTemplate(w, name, data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func navigationGroups() []navGroup {
	return []navGroup{
		{Title: "总览", Items: []navItem{{Key: "dashboard", Label: "控制台", Href: "/"}}},
		{Title: "资源中心", Items: []navItem{{Key: "apps", Label: "应用发布", Href: "/apps"}, {Key: "hosts", Label: "主机管理", Href: "/hosts"}, {Key: "scripts", Label: "脚本库", Href: "/scripts"}}},
		{Title: "任务编排", Items: []navItem{{Key: "crontab", Label: "计划任务", Href: "/crontab"}, {Key: "pipelines", Label: "流水线", Href: "/pipelines"}, {Key: "approvals", Label: "发布审批", Href: "/approvals"}}},
		{Title: "治理审计", Items: []navItem{{Key: "alarms", Label: "告警中心", Href: "/alarms"}, {Key: "users", Label: "用户管理", Href: "/users"}, {Key: "roles", Label: "角色权限", Href: "/roles"}, {Key: "audit", Label: "操作审计", Href: "/audit"}, {Key: "settings", Label: "系统设置", Href: "/settings"}}},
	}
}

func (s *Server) buildPages() map[string]pageConfig {
	ok := badge{Text: "正常", Class: "success"}
	running := badge{Text: "运行中", Class: "running"}
	warn := badge{Text: "告警", Class: "warning"}
	wait := badge{Text: "待审批", Class: "pending"}

	return map[string]pageConfig{
		"dashboard": {
			Title:       "控制台总览",
			Desc:        "聚合发布、主机、任务和告警数据，快速洞察系统健康度。",
			Overview:    []overviewCard{{"在线主机", "126", "+5"}, {"今日发布", "18", "+2"}, {"运行任务", "42", "+6"}, {"待处理告警", "3", "-1"}},
			QuickAction: []quickAction{{"新建应用", "创建发布配置并绑定仓库", "/apps"}, {"执行脚本", "选择主机组批量执行", "/scripts"}, {"查看告警", "定位故障并触发通知", "/alarms"}},
			Panels: []tablePanel{
				{Title: "最近发布记录", Headers: []string{"应用", "环境", "发布人", "时间"}, Rows: []tableRow{{Cols: []string{"payment-api", "生产", "ops_admin", "10:26"}, Badge: running}, {Cols: []string{"website", "预发", "dev_lead", "09:50"}, Badge: ok}, {Cols: []string{"risk-engine", "生产", "release_bot", "08:12"}, Badge: warn}}},
				{Title: "主机健康巡检", Headers: []string{"主机", "IP", "CPU", "内存"}, Rows: []tableRow{{Cols: []string{"app-prod-1", "10.0.0.15", "32%", "48%"}, Badge: ok}, {Cols: []string{"db-prod-1", "10.0.0.22", "72%", "81%"}, Badge: warn}, {Cols: []string{"cache-prod-1", "10.0.0.27", "21%", "40%"}, Badge: ok}}},
			},
		},
		"apps": {
			Title:       "应用发布",
			Desc:        "管理应用仓库、发布流程和环境策略。",
			Overview:    []overviewCard{{"应用总数", "24", "+1"}, {"生产环境", "12", "0"}, {"发布成功率", "98.6%", "+0.4%"}, {"回滚次数", "2", "0"}},
			Panels:      []tablePanel{{Title: "应用列表", Headers: []string{"应用", "仓库", "分支", "负责人"}, Rows: []tableRow{{Cols: []string{"website", "git@gitlab/website.git", "main", "alice"}, Badge: ok}, {Cols: []string{"payment-api", "git@gitlab/payment.git", "release", "bob"}, Badge: running}, {Cols: []string{"risk-engine", "git@gitlab/risk.git", "master", "charlie"}, Badge: wait}}}},
			QuickAction: []quickAction{{"创建应用", "配置仓库、构建与发布脚本", "/apps"}, {"查看流水线", "与 CI/CD 流程联动", "/pipelines"}},
		},
		"hosts": {
			Title:       "主机管理",
			Desc:        "统一管理主机资产、标签和远程连接策略。",
			Overview:    []overviewCard{{"主机总数", "126", "+3"}, {"在线率", "99.2%", "+0.2%"}, {"主机组", "16", "+1"}, {"离线主机", "1", "-2"}},
			Panels:      []tablePanel{{Title: "主机资产", Headers: []string{"主机", "IP", "系统", "标签"}, Rows: []tableRow{{Cols: []string{"app-prod-1", "10.0.0.15", "Ubuntu 22.04", "prod/web"}, Badge: ok}, {Cols: []string{"db-prod-1", "10.0.0.22", "Debian 12", "prod/db"}, Badge: warn}, {Cols: []string{"runner-1", "10.0.0.31", "CentOS Stream", "ci/runner"}, Badge: ok}}}},
			QuickAction: []quickAction{{"导入主机", "通过 CSV 批量导入资产", "/hosts"}, {"终端会话", "快速连接在线主机", "/hosts"}},
		},
		"scripts": {
			Title:       "脚本库",
			Desc:        "维护运维脚本，支持分组、版本和执行审计。",
			Overview:    []overviewCard{{"脚本总数", "87", "+4"}, {"共享脚本", "29", "+2"}, {"最近执行", "236", "+18"}, {"失败率", "1.8%", "-0.5%"}},
			Panels:      []tablePanel{{Title: "脚本清单", Headers: []string{"名称", "分类", "最后更新", "作者"}, Rows: []tableRow{{Cols: []string{"restart-nginx", "服务管理", "2026-03-10", "ops_admin"}, Badge: ok}, {Cols: []string{"backup-mysql", "备份恢复", "2026-03-09", "dba"}, Badge: running}, {Cols: []string{"clear-logs", "系统清理", "2026-03-07", "sre"}, Badge: ok}}}},
			QuickAction: []quickAction{{"新建脚本", "在线编辑并保存版本", "/scripts"}, {"执行历史", "回溯参数与输出日志", "/audit"}},
		},
		"crontab": {
			Title:       "计划任务",
			Desc:        "按时间表达式调度任务，支持失败重试和告警。",
			Overview:    []overviewCard{{"任务总数", "41", "+2"}, {"启用中", "36", "+1"}, {"今日执行", "518", "+45"}, {"失败任务", "4", "-2"}},
			Panels:      []tablePanel{{Title: "任务列表", Headers: []string{"任务", "表达式", "目标", "下次执行"}, Rows: []tableRow{{Cols: []string{"nightly-backup", "0 2 * * *", "db-prod", "02:00"}, Badge: ok}, {Cols: []string{"sync-assets", "*/10 * * * *", "web-group", "14:40"}, Badge: running}, {Cols: []string{"collect-metrics", "*/5 * * * *", "all-hosts", "14:35"}, Badge: warn}}}},
			QuickAction: []quickAction{{"创建计划", "配置表达式与执行策略", "/crontab"}},
		},
		"pipelines": {
			Title:       "流水线",
			Desc:        "以阶段化方式编排构建、测试、发布任务。",
			Overview:    []overviewCard{{"流水线", "12", "+1"}, {"运行实例", "7", "+2"}, {"平均耗时", "8m42s", "-23s"}, {"失败率", "3.2%", "-0.8%"}},
			Panels:      []tablePanel{{Title: "流水线运行", Headers: []string{"名称", "阶段", "触发方式", "开始时间"}, Rows: []tableRow{{Cols: []string{"web-release", "deploy", "git push", "14:15"}, Badge: running}, {Cols: []string{"api-release", "test", "manual", "13:20"}, Badge: ok}, {Cols: []string{"risk-release", "build", "schedule", "12:00"}, Badge: warn}}}},
			QuickAction: []quickAction{{"新建流水线", "拖拽式配置阶段节点", "/pipelines"}},
		},
		"approvals": {
			Title:       "发布审批",
			Desc:        "发布前审批流程，保障高风险操作可追踪可复核。",
			Overview:    []overviewCard{{"待审批", "6", "+1"}, {"今日已批", "19", "+4"}, {"拒绝", "2", "0"}, {"平均处理", "12m", "-2m"}},
			Panels:      []tablePanel{{Title: "审批队列", Headers: []string{"工单", "申请人", "目标环境", "创建时间"}, Rows: []tableRow{{Cols: []string{"#A-1024 payment-api", "bob", "生产", "14:20"}, Badge: wait}, {Cols: []string{"#A-1021 website", "alice", "预发", "13:10"}, Badge: ok}, {Cols: []string{"#A-1019 risk-engine", "charlie", "生产", "11:45"}, Badge: warn}}}},
			QuickAction: []quickAction{{"审批历史", "按应用和审批人检索", "/audit"}},
		},
		"alarms": {
			Title:       "告警中心",
			Desc:        "集中查看告警事件，支持多通道通知和升级策略。",
			Overview:    []overviewCard{{"当前告警", "3", "-1"}, {"今日恢复", "8", "+2"}, {"通知规则", "11", "+0"}, {"升级事件", "1", "0"}},
			Panels:      []tablePanel{{Title: "告警事件", Headers: []string{"规则", "级别", "目标", "触发时间"}, Rows: []tableRow{{Cols: []string{"db-cpu-high", "高", "db-prod-1", "14:08"}, Badge: warn}, {Cols: []string{"api-error-rate", "中", "payment-api", "13:41"}, Badge: running}, {Cols: []string{"disk-usage", "低", "backup-1", "09:12"}, Badge: ok}}}},
			QuickAction: []quickAction{{"通知配置", "配置钉钉、飞书、邮件", "/settings"}},
		},
		"users": {
			Title:       "用户管理",
			Desc:        "管理用户账号、登录策略和多因素认证。",
			Overview:    []overviewCard{{"用户数", "36", "+2"}, {"启用账号", "34", "+1"}, {"MFA 开启", "21", "+4"}, {"异常登录", "0", "0"}},
			Panels:      []tablePanel{{Title: "用户列表", Headers: []string{"用户名", "姓名", "角色", "最后登录"}, Rows: []tableRow{{Cols: []string{"ops_admin", "王磊", "超级管理员", "今天 14:00"}, Badge: ok}, {Cols: []string{"dev_lead", "李晨", "发布负责人", "今天 13:17"}, Badge: ok}, {Cols: []string{"intern", "赵新", "只读", "昨天 18:20"}, Badge: wait}}}},
			QuickAction: []quickAction{{"新增用户", "设置角色与主机权限", "/users"}, {"权限策略", "细粒度控制操作范围", "/roles"}},
		},
		"roles": {
			Title:       "角色权限",
			Desc:        "通过角色模板管理资源访问和操作权限。",
			Overview:    []overviewCard{{"角色数", "9", "+0"}, {"策略模板", "14", "+1"}, {"自定义权限", "22", "+3"}, {"越权拦截", "5", "+1"}},
			Panels:      []tablePanel{{Title: "角色配置", Headers: []string{"角色", "成员", "资源范围", "最近更新"}, Rows: []tableRow{{Cols: []string{"超级管理员", "3", "全部", "2026-03-10"}, Badge: ok}, {Cols: []string{"发布负责人", "8", "应用/流水线", "2026-03-08"}, Badge: running}, {Cols: []string{"只读访客", "12", "监控/审计", "2026-03-05"}, Badge: ok}}}},
			QuickAction: []quickAction{{"新建角色", "按模块和操作权限授权", "/roles"}},
		},
		"audit": {
			Title:       "操作审计",
			Desc:        "记录登录、发布、脚本执行等操作，支持检索与追溯。",
			Overview:    []overviewCard{{"今日日志", "1,286", "+156"}, {"高危操作", "4", "-1"}, {"登录成功率", "99.8%", "+0.1%"}, {"留存天数", "180", "+0"}},
			Panels:      []tablePanel{{Title: "审计日志", Headers: []string{"时间", "用户", "动作", "对象"}, Rows: []tableRow{{Cols: []string{"14:19:03", "ops_admin", "发布应用", "payment-api@prod"}, Badge: ok}, {Cols: []string{"13:56:44", "bob", "执行脚本", "restart-nginx"}, Badge: running}, {Cols: []string{"12:30:12", "intern", "查看主机", "db-prod-1"}, Badge: ok}}}},
			QuickAction: []quickAction{{"导出审计", "按条件导出 CSV/JSON", "/audit"}},
		},
		"settings": {
			Title:       "系统设置",
			Desc:        "配置系统参数、通知渠道、SSO 和安全策略。",
			Overview:    []overviewCard{{"通知渠道", "5", "+1"}, {"SSO 提供商", "2", "+0"}, {"密码策略", "强", "+0"}, {"系统版本", "v0.3.0", "+"}},
			Panels:      []tablePanel{{Title: "配置项", Headers: []string{"模块", "当前值", "说明", "状态"}, Rows: []tableRow{{Cols: []string{"登录安全", "MFA 可选", "控制二次验证", "已启用"}, Badge: ok}, {Cols: []string{"邮件通知", "smtp.company.com", "告警与审批消息", "已启用"}, Badge: running}, {Cols: []string{"Webhook", "3 条规则", "第三方自动化集成", "需复核"}, Badge: warn}}}},
			QuickAction: []quickAction{{"安全策略", "配置登录/密码策略", "/settings"}, {"通知模板", "维护消息模板", "/settings"}},
		},
	}
}

func getenv(key, fallback string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return fallback
}

func main() {
	srv, err := NewServer()
	if err != nil {
		log.Fatalf("init server: %v", err)
	}

	server := &http.Server{
		Addr:              srv.httpAddr,
		Handler:           srv.routes(),
		ReadHeaderTimeout: 5 * time.Second,
	}

	log.Printf("server listening on %s", srv.httpAddr)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
}
