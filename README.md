# GoSpug

一个使用 **Golang** 实现的「Spug 风格」运维控制台示例项目。

> 说明：本项目是从零实现的相似风格 Demo，不包含原站点私有源码。

## 功能

- 登录页（默认 `admin / spug.cc`，可配置）
- 登录会话（HttpOnly Cookie）
- 控制台总览、主机管理、任务中心、系统设置
- 深色后台样式，接近 Spug 的视觉布局

## 本地启动

```bash
go mod tidy
go run ./cmd/server
```

默认访问：`http://127.0.0.1:8080/login`

## 环境变量

- `HTTP_ADDR`：监听地址（默认 `:8080`）
- `ADMIN_USER`：管理员用户名（默认 `admin`）
- `ADMIN_PASSWORD`：管理员密码（默认 `spug.cc`）
- `COOKIE_SECURE`：是否仅 HTTPS 发送 Cookie（默认 `false`）

## Docker 部署

```bash
docker build -t gospug:latest .
docker run --rm -p 8080:8080 \
  -e ADMIN_USER=admin \
  -e ADMIN_PASSWORD=spug.cc \
  gospug:latest
```

## Docker Compose 部署

```bash
docker compose up -d --build
```

## 目录结构

```text
cmd/server/main.go        # 主程序
web/templates/*.html      # 页面模板
web/static/style.css      # 样式
Dockerfile                # 镜像构建
compose.yml               # 一键启动
```
