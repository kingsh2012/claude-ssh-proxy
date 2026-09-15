package main

import (
	"flag"
	"fmt"
	"log"
	"os"
)

// version 由 release 构建时通过 -ldflags "-X main.version=vX.Y.Z" 注入,本地构建默认是 "dev"。
var version = "dev"

func main() {
	dbPath := flag.String("db", "aiagent-ssh-proxy.db", "SQLite数据库文件路径")
	hostKeyPath := flag.String("host-key", "host_key", "proxy自身SSH host key文件路径")
	webAddr := flag.String("web-addr", "", "覆盖并保存Web管理后台监听地址（仅HTTP）")
	agentAddr := flag.String("agent-addr", "", "覆盖并保存独立Agent监听地址")
	sshAddr := flag.String("ssh-addr", os.Getenv("SSH_LISTEN_ADDR"), "覆盖并保存SSH代理监听地址(留空时使用数据库配置,首次默认 :2222)")
	adminUser := flag.String("bootstrap-admin-user", "admin", "首次启动时自动创建的管理员用户名(仅当数据库里还没有任何管理员账号时生效)")
	adminPassword := flag.String("bootstrap-admin-password", "admin", "首次启动时自动创建的管理员初始密码(仅当数据库里还没有任何管理员账号时生效,登录后会被强制要求修改)")
	showVersion := flag.Bool("version", false, "打印版本号并退出")
	flag.Parse()

	if *showVersion {
		fmt.Println(version)
		os.Exit(0)
	}

	if *dbPath == "aiagent-ssh-proxy.db" {
		explicitDB := false
		flag.Visit(func(f *flag.Flag) {
			if f.Name == "db" {
				explicitDB = true
			}
		})
		for _, legacy := range []string{"ops-ssh-proxy.db", "claude-ssh-proxy.db"} {
			if _, err := os.Stat(legacy); err == nil && !explicitDB {
				log.Fatalf("检测到旧数据库，请显式传入 -db %s，避免更名后创建空数据库", legacy)
			}
		}
	}
	log.Printf("aiagent-ssh-proxy %s启动中...", version)

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
		log.Fatalf("初始化aiagent-ssh-proxy失败: %v", err)
	}
	listenAddr := store.GetSetting("listen_addr", ":2222")
	if *sshAddr != "" {
		listenAddr = *sshAddr
	}
	if err := proxy.Start(listenAddr); err != nil {
		log.Fatalf("启动aiagent-ssh-proxy失败: %v", err)
	}
	if *sshAddr != "" {
		if err := store.SetSetting("listen_addr", listenAddr); err != nil {
			log.Fatalf("保存SSH监听地址失败: %v", err)
		}
	}

	settings := store.listenerSettings()
	// Import legacy environment defaults only until settings are first saved.
	if store.GetSetting("web_listen_addr", "") == "" {
		settings.WebAddr = envOrDefault("WEB_LISTEN_ADDR", settings.WebAddr)
	}
	if store.GetSetting("agent_listen_addr", "") == "" {
		settings.AgentAddr = envOrDefault("AGENT_LISTEN_ADDR", settings.AgentAddr)
	}
	if override := os.Getenv("WEB_LISTEN_ADDR_OVERRIDE"); override != "" {
		settings.WebAddr = override
	}
	if *webAddr != "" {
		settings.WebAddr = *webAddr
	}
	if *agentAddr != "" {
		settings.AgentAddr = *agentAddr
	}
	api := NewAPI(store, proxy)
	listeners, err := openListeners(settings, webRouter(api.Router()), agentRouter(proxy.agents))
	if err != nil {
		log.Fatalf("初始化Web/Agent监听失败: %v", err)
	}
	if err := store.saveListenerSettings(settings); err != nil {
		listeners.Close()
		log.Fatalf("保存Web/Agent监听设置失败: %v", err)
	}
	log.Printf("Web管理后台正在监听http://%s", settings.WebAddr)
	log.Printf("独立Agent正在监听%s，内置TLS=%t", settings.AgentAddr, settings.AgentTLSEnabled)
	if err := listeners.Serve(); err != nil {
		log.Fatalf("Web/Agent监听停止: %v", err)
	}

}

func envOrDefault(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
