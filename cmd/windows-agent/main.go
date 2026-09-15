package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"runtime"
	"time"

	"github.com/kingsh2012/aiagent-ssh-proxy/internal/agentwire"
	"github.com/kingsh2012/aiagent-ssh-proxy/internal/winagent"
)

var version = "dev"

func main() {
	path := flag.String("config", "", "可选：读取旧TOML配置")
	token := flag.String("token", "", "服务设置中的服务器自注册Token")
	server := flag.String("server", "", "可选：覆盖HTTPS主域名（兼容WSS地址），或与旧设备凭证配合使用")
	hostname := flag.String("hostname", "", "可选：指定代理登录名，默认使用Windows主机名")
	upgrade := flag.Bool("upgrade", false, "升级到最新正式版本并退出")
	showVersion := flag.Bool("version", false, "打印版本号并退出")
	flag.Parse()
	if runtime.GOOS != "windows" {
		log.Fatal("此Agent仅支持Windows")
	}
	if *showVersion {
		fmt.Println(version)
		return
	}
	if *upgrade {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		result, err := winagent.Upgrade(ctx, version)
		if err != nil {
			log.Fatalf("客户端升级失败：%v", err)
		}
		if !result.Updated {
			log.Printf("客户端无需升级；当前版本%s", result.Version)
			return
		}
		log.Printf("已下载并校验%s；退出后将在后台替换当前程序", result.Version)
		log.Printf("升级结果记录在%s", result.LogPath)
		return
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
	go func() {
		<-ctx.Done()
		log.Print("收到关闭信号，正在停止Windows Agent")
	}()
	winagent.Run(ctx, c, func(event winagent.Event) {
		switch event.Type {
		case winagent.EventConnected:
			if event.Reconnected {
				log.Print("Agent重连成功")
			} else {
				log.Print("Agent连接成功")
			}
		case winagent.EventConnectionFailed:
			log.Printf("Agent连接失败：%v；5秒后重试", event.Err)
		case winagent.EventDisconnected:
			log.Printf("Agent连接断开：%v；5秒后重连", event.Err)
		case winagent.EventTaskStarted:
			log.Printf("收到任务，开始执行；任务ID=%q\n命令内容：\n%s", event.TaskID, event.Command)
		case winagent.EventTaskOutput:
			stream := "标准输出"
			if event.Stream == "stderr" {
				stream = "标准错误"
			}
			log.Printf("任务%s；任务ID=%q\n%s", stream, event.TaskID, event.Data)
		case winagent.EventTaskCanceled:
			log.Printf("收到取消操作；任务ID=%q", event.TaskID)
		case winagent.EventTaskFinished:
			log.Printf("任务执行结束；任务ID=%q，退出码=%d，耗时=%s", event.TaskID, event.ExitCode, event.Duration.Round(time.Millisecond))
		}
	})
	log.Print("Windows Agent已停止")
}
