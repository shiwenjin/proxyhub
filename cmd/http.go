/*
Copyright 2025 NAME HERE EMAIL ADDRESS
*/
package cmd

import (
	"context"
	"net"
	"net/http"
	"proxyhub/pkg/log"
	"proxyhub/services"

	"github.com/elazarl/goproxy"
	"github.com/spf13/cobra"
	"go.uber.org/zap"
)

var httpArgs services.HTTPArgs

// setupTrafficReporter 初始化流量报告器
func setupTrafficReporter() (*services.TrafficReporter, chan services.TrafficRecord) {
	reporter := services.NewTrafficReporter(&args, "http")
	var recordsCh chan services.TrafficRecord

	if reporter != nil && reporter.ReporterFastGlobal() {
		recordsCh = make(chan services.TrafficRecord, 2048)
		reporter.StartGlobalBatch(recordsCh)
	}

	return reporter, recordsCh
}

// createDialHandler 创建处理普通HTTP请求的拨号处理器
func createDialHandler() func(ctx context.Context, network, addr string) (net.Conn, error) {
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		return wrapConnection(ctx, network, addr)
	}
}

// wrapConnection 包装连接以进行流量统计
func wrapConnection(_ context.Context, network, addr string) (net.Conn, error) {
	outConn, err := net.Dial(network, addr)
	if err != nil {
		return nil, err
	}

	return outConn, nil
}

// inboundListener 包装入站连接以统计用户(客户端)流量
type inboundListener struct {
	net.Listener
	reporter  *services.TrafficReporter
	recordsCh chan services.TrafficRecord
}

func (l *inboundListener) Accept() (net.Conn, error) {
	conn, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	if l.reporter == nil {
		return conn, nil
	}
	// 入站侧统计：按连接总量在关闭时上报（用户维度更贴近客户端）
	c := &services.CountingConn{Conn: conn}
	serverAddr := *args.Local
	clientAddr := ""
	if conn.RemoteAddr() != nil {
		clientAddr = conn.RemoteAddr().String()
	}
	// 入站连接没有目标主机概念（可能多次请求多个目标），这里留空或后续在 HTTP 层补充
	targetAddr := ""

	if !l.reporter.ReporterModeFast() {
		c.SetOnClose(func(total int64) {
			rec := l.reporter.BuildRecord(serverAddr, clientAddr, targetAddr, conn, "", "", total)
			if err := l.reporter.ReportOnce(rec); err != nil {
				log.Error("report inbound traffic", zap.Error(err))
			}
		})
	}

	// 为每个连接创建独立的上下文，随连接生命周期存在
	ctxConn, cancel := context.WithCancel(context.Background())

	interval := l.reporter.ReporterInterval()
	if l.reporter.ReporterFastGlobal() && l.recordsCh != nil {
		// fast + global: 按连接启动统计协程，输出到全局通道
		c.SetOnClose(func(total int64) { cancel() })
		go c.PerConnDeltaToChan(ctxConn, interval, func(d int64) {
			if d > 0 {
				rec := l.reporter.BuildRecord(serverAddr, clientAddr, targetAddr, conn, "", "", d)
				l.recordsCh <- rec
			}
		})
	} else {
		// fast 非全局：按连接启动统计协程，直接上报
		c.SetOnClose(func(total int64) { cancel() })
		go c.PerConnDeltaToReport(ctxConn, interval, func(d int64) {
			if d > 0 {
				rec := l.reporter.BuildRecord(serverAddr, clientAddr, targetAddr, conn, "", "", d)
				err = l.reporter.ReportOnce(rec)
				if err != nil {
					log.Error("report traffic", zap.Error(err))
				}
			}
		})
	}

	return c, nil
}

// startHTTPServer 启动HTTP代理服务器（入站连接统计）
func startHTTPServer(proxy *goproxy.ProxyHttpServer) {
	log.Info("http ", zap.String("port", *args.Local))

	ln, err := net.Listen("tcp", *args.Local)
	if err != nil {
		log.Fatal(err.Error())
		return
	}

	// 设置流量报告器
	reporter, recordsCh := setupTrafficReporter()

	il := &inboundListener{Listener: ln, reporter: reporter, recordsCh: recordsCh}
	if err := http.Serve(il, proxy); err != nil {
		log.Fatal(err.Error())
	}
}

// httpCmd represents the http command
var httpCmd = &cobra.Command{
	Use:              "http",
	TraverseChildren: true,
	Run: func(cmd *cobra.Command, _ []string) {
		// 创建代理服务器
		proxy := goproxy.NewProxyHttpServer()
		proxy.Verbose = *args.Verbose
		proxy.Logger = log.Default

		// 设置HTTP传输
		tr := &http.Transport{}
		tr.DialContext = createDialHandler()
		proxy.Tr = tr

		// 启动服务器（入站连接统计）
		startHTTPServer(proxy)
	},
}

func init() {
	rootCmd.AddCommand(httpCmd)
	httpArgs = services.HTTPArgs{}
	httpArgs.LocalType = httpCmd.Flags().StringP("local-type", "t", "tcp", "parent protocol type <tls|tcp>")
	httpArgs.ParentType = httpCmd.Flags().StringP("parent-type", "T", "tcp", "parent protocol type <tls|tcp>")
	httpArgs.Always = httpCmd.Flags().Bool("always", false, "always use parent proxy")
	httpArgs.Timeout = httpCmd.Flags().Int("timeout", 2000, "tcp timeout milliseconds when connect to real server or parent proxy")
	httpArgs.HTTPTimeout = httpCmd.Flags().Int("http-timeout", 3000, "check domain if blocked , http request timeout milliseconds when connect to host")
	httpArgs.Interval = httpCmd.Flags().Int("interval", 10, "check domain if blocked every interval seconds")
	httpArgs.Blocked = httpCmd.Flags().StringP("blocked", "b", "blocked", "blocked domain file , one domain each line")
	httpArgs.Direct = httpCmd.Flags().StringP("direct", "d", "direct", "direct domain file , one domain each line")
	httpArgs.AuthFile = httpCmd.Flags().StringP("auth-file", "F", "auth-file", "http basic auth file,\"username:password\" each line in file")
	httpArgs.Auth = httpCmd.Flags().StringSliceP("auth", "a", []string{}, "http basic auth username and password, mutiple user repeat -a ,such as: -a user1:pass1 -a user2:pass2")
	httpArgs.PoolSize = httpCmd.Flags().IntP("pool-size", "L", 20, "conn pool size , which connect to parent proxy, zero: means turn off pool")
	httpArgs.CheckParentInterval = httpCmd.Flags().IntP("check-parent-interval", "I", 3, "check if proxy is okay every interval seconds,zero: means no check")
}
