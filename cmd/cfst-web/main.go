package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
	_ "time/tzdata"

	"github.com/masgzy/CloudflareST/internal/management"
)

func main() {
	listen := flag.String("listen", env("CFST_LISTEN", "127.0.0.1:8080"), "HTTP 监听地址")
	dataDir := flag.String("data", env("CFST_DATA_DIR", "./data"), "独占管理数据目录")
	secure := flag.Bool("secure-cookie", env("CFST_SECURE_COOKIE", "false") == "true", "通过 HTTPS 访问时启用安全 Cookie")
	flag.Parse()
	service, err := management.Open(management.Config{DataDir: *dataDir, RuntimeDir: os.Getenv("CFST_RUNTIME_DIR"), TrustedProxies: strings.Split(os.Getenv("CFST_TRUSTED_PROXIES"), ","), Password: os.Getenv("CFST_ADMIN_PASSWORD"), SecureCookie: *secure})
	if err != nil {
		fmt.Fprintln(os.Stderr, "管理服务启动失败：", err)
		os.Exit(1)
	}
	defer service.Close()
	if passwordFile := service.PasswordFile(); passwordFile != "" {
		fmt.Fprintln(os.Stderr, "已生成本次启动的管理密码，请在本机受保护文件中读取：", passwordFile)
	}
	server := &http.Server{Addr: *listen, Handler: service.Handler(), ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 30 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 1 << 20}
	stopped := make(chan os.Signal, 1)
	signal.Notify(stopped, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-stopped
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		_ = server.Shutdown(ctx)
	}()
	fmt.Fprintln(os.Stderr, "IP 优选管理服务监听：", *listen)
	if err = server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		fmt.Fprintln(os.Stderr, "管理服务运行失败：", err)
	}
}

func env(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
