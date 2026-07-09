package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
)

// version 由 release 构建时通过 -ldflags "-X main.version=vX.Y.Z" 注入,本地构建默认是 "dev"。
var version = "dev"

func main() {
	dbPath := flag.String("db", "claude-ssh-proxy.db", "SQLite 数据库文件路径")
	hostKeyPath := flag.String("host-key", "host_key", "proxy 自身 SSH host key 文件路径")
	webAddr := flag.String("web-addr", ":8080", "Web 管理后台监听地址")
	adminUser := flag.String("bootstrap-admin-user", "admin", "首次启动时自动创建的管理员用户名(仅当数据库里还没有任何管理员账号时生效)")
	adminPassword := flag.String("bootstrap-admin-password", "admin", "首次启动时自动创建的管理员初始密码(仅当数据库里还没有任何管理员账号时生效,登录后会被强制要求修改)")
	showVersion := flag.Bool("version", false, "打印版本号并退出")
	flag.Parse()

	if *showVersion {
		fmt.Println(version)
		os.Exit(0)
	}

	log.Printf("claude-ssh-proxy %s 启动中...", version)

	store, err := OpenStore(*dbPath)
	if err != nil {
		log.Fatalf("初始化数据库失败: %v", err)
	}

	if n, _ := store.CountAdminUsers(); n == 0 {
		if err := store.CreateAdminUser(*adminUser, *adminPassword); err != nil {
			log.Fatalf("创建初始管理员账号失败: %v", err)
		}
		log.Printf("========================================")
		log.Printf("已创建初始管理员账号,首次登录后会强制要求修改密码:")
		log.Printf("  用户名: %s", *adminUser)
		log.Printf("  密码:   %s", *adminPassword)
		log.Printf("========================================")
	}

	proxy, err := NewProxy(store, *hostKeyPath)
	if err != nil {
		log.Fatalf("初始化 claude-ssh-proxy 失败: %v", err)
	}
	listenAddr := store.GetSetting("listen_addr", ":2222")
	if err := proxy.Start(listenAddr); err != nil {
		log.Fatalf("启动 claude-ssh-proxy 失败: %v", err)
	}

	api := NewAPI(store, proxy)
	mux := http.NewServeMux()
	mux.Handle("/api/", api.Router())
	mux.Handle("/", webUIHandler())

	log.Printf("Web 管理后台正在监听 %s", *webAddr)
	if err := http.ListenAndServe(*webAddr, mux); err != nil {
		log.Fatalf("Web 服务启动失败: %v", err)
	}
}
