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

type Server struct {
	templates    *template.Template
	sessions     *SessionStore
	adminUser    string
	adminHash    [32]byte
	httpAddr     string
	cookieSecure bool
}

type dashboardData struct {
	Title    string
	User     User
	Now      string
	Stats    map[string]int
	Tasks    []task
	Servers  []serverHost
	Active   string
	PageDesc string
}

type task struct {
	Name   string
	Status string
	Time   string
}

type serverHost struct {
	Name string
	IP   string
	OS   string
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

	return &Server{
		templates:    tmpl,
		sessions:     NewSessionStore(),
		adminUser:    adminUser,
		adminHash:    hash,
		httpAddr:     getenv("HTTP_ADDR", ":8080"),
		cookieSecure: cookieSecure,
	}, nil
}

func (s *Server) routes() http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.Dir("web/static"))))
	mux.HandleFunc("/login", s.login)
	mux.HandleFunc("/logout", s.logout)
	mux.HandleFunc("/", s.auth(s.dashboard("总览", "一眼看到系统运行情况", "dashboard")))
	mux.HandleFunc("/hosts", s.auth(s.dashboard("主机管理", "管理主机和连接信息", "hosts")))
	mux.HandleFunc("/tasks", s.auth(s.dashboard("任务中心", "查看计划任务和执行日志", "tasks")))
	mux.HandleFunc("/settings", s.auth(s.dashboard("系统设置", "管理通知、权限与配置", "settings")))
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

func (s *Server) dashboard(title, desc, active string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, _ := s.currentUser(r)
		data := dashboardData{
			Title:    title,
			User:     user,
			Now:      time.Now().Format("2006-01-02 15:04:05"),
			Active:   active,
			PageDesc: desc,
			Stats: map[string]int{
				"在线主机": 12,
				"运行任务": 8,
				"成功发布": 39,
				"告警事件": 2,
			},
			Tasks:   []task{{"夜间备份", "成功", "02:00"}, {"发布 Web 服务", "运行中", "14:12"}, {"清理日志", "等待", "18:30"}},
			Servers: []serverHost{{"app-prod-1", "10.0.0.15", "Ubuntu 22.04"}, {"db-prod-1", "10.0.0.22", "Debian 12"}, {"ci-runner-1", "10.0.0.31", "CentOS Stream"}},
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
