package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"runtime"

	"github.com/kingsh2012/aiagent-ssh-proxy/internal/agentwire"
	"github.com/kingsh2012/aiagent-ssh-proxy/internal/winagent"
)

func main() {
	path := flag.String("config", "", "可选：读取旧TOML配置")
	token := flag.String("token", "", "服务设置中的服务器自注册Token")
	server := flag.String("server", "", "可选：覆盖HTTPS主域名（兼容WSS地址），或与旧设备凭证配合使用")
	hostname := flag.String("hostname", "", "可选：指定代理登录名，默认使用Windows主机名")
	flag.Parse()
	if runtime.GOOS != "windows" {
		log.Fatal("此Agent仅支持Windows")
	}
	var c winagent.Config
	var err error
	if *path != "" {
		if *token != "" || *server != "" {
			log.Fatal("-config不能与 -token/-server混用")
		}
		c, err = winagent.LoadConfig(*path)
	} else if *token != "" {
		c.ServerURL, c.Token, err = agentwire.ParseEnrollmentToken(*token)
		if *server != "" {
			if err != nil && len(*token) == 64 {
				c.Token = *token
				err = nil
			}
			c.ServerURL = *server
		}
		if err == nil {
			err = winagent.ValidateConfig(c)
		}
	} else {
		flag.Usage()
		log.Fatal("请使用 -token指定网页申请的接入Token")
	}
	if *hostname != "" {
		c.Hostname = *hostname
	}
	if err == nil {
		err = winagent.ValidateConfig(c)
	}
	if err != nil {
		log.Fatal(err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	log.Print("Windows Agent启动；退出程序将终止当前任务")
	winagent.Run(ctx, c, func(err error) { log.Printf("%v；5 秒后重连", err) })
}
