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

	if reporter != nil && reporterModeFast(reporter) && reporterFastGlobal(reporter) {
		recordsCh = make(chan services.TrafficRecord, 2048)
		reporter.StartGlobalBatch(recordsCh)
	}

	return reporter, recordsCh
}

// createDialHandler 创建处理普通HTTP请求的拨号处理器
func createDialHandler(reporter *services.TrafficReporter, recordsCh chan services.TrafficRecord) func(ctx context.Context, network, addr string) (net.Conn, error) {
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		return wrapConnection(ctx, network, addr, reporter, recordsCh)
	}
}

// createConnectDialHandler 创建处理HTTPS CONNECT请求的拨号处理器
func createConnectDialHandler(reporter *services.TrafficReporter, recordsCh chan services.TrafficRecord) func(network, addr string) (net.Conn, error) {
	return func(network, addr string) (net.Conn, error) {
		return wrapConnection(context.Background(), network, addr, reporter, recordsCh)
	}
}

// wrapConnection 包装连接以进行流量统计
func wrapConnection(ctx context.Context, network, addr string, reporter *services.TrafficReporter, recordsCh chan services.TrafficRecord) (net.Conn, error) {
	outConn, err := net.Dial(network, addr)
	if err != nil {
		return nil, err
	}

	if reporter == nil {
		return outConn, nil
	}

	c := &services.CountingConn{Conn: outConn}
	serverAddr := *args.Local
	clientAddr := "" // 在这里获取客户端地址不太容易
	targetAddr := addr

	if !reporterModeFast(reporter) {
		c.SetOnClose(func(total int64) {
			rec := reporter.BuildRecord(serverAddr, clientAddr, targetAddr, outConn, "", "", total)
			_ = reporter.ReportOnce(rec)
		})
		return c, nil
	}

	interval := reporterInterval(reporter)
	if reporterFastGlobal(reporter) && recordsCh != nil {
		go perConnDeltaToChan(ctx, c, interval, func(d int64) {
			if d > 0 {
				recordsCh <- reporter.BuildRecord(serverAddr, clientAddr, targetAddr, outConn, "", "", d)
			}
		})
	} else {
		go perConnDeltaToReport(ctx, c, interval, func(d int64) {
			if d > 0 {
				_ = reporter.ReportOnce(reporter.BuildRecord(serverAddr, clientAddr, targetAddr, outConn, "", "", d))
			}
		})
	}

	return c, nil
}

// startHTTPServer 启动HTTP代理服务器
func startHTTPServer(proxy *goproxy.ProxyHttpServer) {
	log.Info("http ", zap.String("port", *args.Local))

	err := http.ListenAndServe(*args.Local, proxy)
	if err != nil {
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

		// 设置流量报告器
		reporter, recordsCh := setupTrafficReporter()

		// 设置HTTP传输
		tr := &http.Transport{}
		tr.DialContext = createDialHandler(reporter, recordsCh)
		proxy.Tr = tr

		// 设置HTTPS CONNECT处理
		proxy.ConnectDial = createConnectDialHandler(reporter, recordsCh)

		// 启动服务器
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
