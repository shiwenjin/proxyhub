package cmd

import (
	"context"
	"net"
	"proxyhub/pkg/log"
	"proxyhub/services"

	"github.com/spf13/cobra"
	"github.com/things-go/go-socks5"
	"go.uber.org/zap"
)

// setupSocksTrafficReporter 初始化SOCKS流量报告器
func setupSocksTrafficReporter() (*services.TrafficReporter, chan services.TrafficRecord) {
	reporter := services.NewTrafficReporter(&args, "socks")
	var recordsCh chan services.TrafficRecord

	if reporter != nil && reporter.ReporterModeFast() && reporter.ReporterFastGlobal() {
		recordsCh = make(chan services.TrafficRecord, 1024)
		reporter.StartGlobalBatch(recordsCh)
	}

	return reporter, recordsCh
}

// handleSocksConnection 处理SOCKS连接并进行流量统计
func handleSocksConnection(ctx context.Context, network, addr string, request *socks5.Request,
	reporter *services.TrafficReporter, recordsCh chan services.TrafficRecord) (net.Conn, error) {
	log.Info("socks request", zap.Any("remote", request.RemoteAddr), zap.Any("dest", request.DestAddr))

	// 连接真实服务器
	outConn, err := net.Dial(network, addr)
	if err != nil {
		return nil, err
	}

	if reporter == nil {
		return outConn, nil
	}

	// 包装连接以进行计数
	c := &services.CountingConn{Conn: outConn}
	serverAddr := *args.Local
	clientAddr := ""
	if request.RemoteAddr != nil {
		clientAddr = request.RemoteAddr.String()
	}
	targetAddr := ""
	if request.DestAddr != nil {
		targetAddr = request.DestAddr.String()
	}

	// 为每个连接创建独立的上下文，随连接生命周期存在
	ctxConn, cancel := context.WithCancel(context.Background())

	// 普通模式：在连接关闭时报告总字节数
	if !reporter.ReporterModeFast() {
		c.SetOnClose(func(total int64) {
			// 结束统计协程（若有），再做收尾上报
			defer cancel()
			rec := reporter.BuildRecord(serverAddr, clientAddr, targetAddr, outConn, "", "", total)
			_ = reporter.ReportOnce(rec)
		})
		return c, nil
	}

	// 快速模式（全局或每连接）
	interval := reporter.ReporterInterval()
	if reporter.ReporterFastGlobal() && recordsCh != nil {
		// 发送增量到全局通道
		c.SetOnClose(func(total int64) { cancel() })
		go c.PerConnDeltaToChan(ctxConn, interval, func(d int64) {
			if d > 0 {
				rec := reporter.BuildRecord(serverAddr, clientAddr, targetAddr, outConn, "", "", d)
				recordsCh <- rec
			}
		})
	} else {
		// 每连接快速模式：直接GET请求增量
		c.SetOnClose(func(total int64) { cancel() })
		go c.PerConnDeltaToReport(ctxConn, interval, func(d int64) {
			if d > 0 {
				rec := reporter.BuildRecord(serverAddr, clientAddr, targetAddr, outConn, "", "", d)
				_ = reporter.ReportOnce(rec)
			}
		})
	}

	return c, nil
}

// createSocksServer 创建并配置SOCKS5服务器
func createSocksServer(reporter *services.TrafficReporter, recordsCh chan services.TrafficRecord) *socks5.Server {
	return socks5.NewServer(
		socks5.WithLogger(log.Default.Sugar()),
		socks5.WithDialAndRequest(func(ctx context.Context, network, addr string, request *socks5.Request) (net.Conn, error) {
			return handleSocksConnection(ctx, network, addr, request, reporter, recordsCh)
		}),
	)
}

// startSocksServer 启动SOCKS5服务器
func startSocksServer(server *socks5.Server) {
	log.Info("SOCKS5 server started", zap.String("addr", *args.Local))
	if err := server.ListenAndServe("tcp", *args.Local); err != nil {
		panic(err)
	}
}

// socksCmd represents the socks command
var socksCmd = &cobra.Command{
	Use:   "socks",
	Short: "SOCKS 代理",
	Run: func(cmd *cobra.Command, _ []string) {
		// 设置流量报告器
		reporter, recordsCh := setupSocksTrafficReporter()

		// 创建SOCKS5服务器
		server := createSocksServer(reporter, recordsCh)

		// 启动服务器
		startSocksServer(server)
	},
}

func init() {
	rootCmd.AddCommand(socksCmd)
}
