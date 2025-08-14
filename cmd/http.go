/*
Copyright © 2025 NAME HERE <EMAIL ADDRESS>
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

type localIPKeyType struct{}

var localIPKey = localIPKeyType{}

type inboundConnKeyType struct{}

var inboundConnKey = inboundConnKeyType{}

var httpArgs services.HTTPArgs

// httpCmd represents the http command
var httpCmd = &cobra.Command{
	Use:              "http",
	Short:            "HTTP 代理",
	TraverseChildren: true,
	Run: func(cmd *cobra.Command, _ []string) {
		// 创建代理服务器并开始监听
		proxy := initProxy()

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

func initProxy() *goproxy.ProxyHttpServer {
	proxy := goproxy.NewProxyHttpServer()
	proxy.Verbose = *args.Verbose
	proxy.Logger = log.Default

	// HTTP 请求鉴权（普通请求与 CONNECT）
	proxy.OnRequest().DoFunc(func(r *http.Request, ctx *goproxy.ProxyCtx) (*http.Request, *http.Response) {
		// 从 Proxy-Authorization 读取凭证
		if args.AuthURL != nil && *args.AuthURL != "" {
			user, pass, ok := services.ParseBasicAuth(r.Header.Get("Proxy-Authorization"))
			if !ok && !*args.AuthNoUser {
				return r, goproxy.NewResponse(r, goproxy.ContentTypeText, http.StatusProxyAuthRequired, "Proxy Authentication Required")
			}
			clientIP := services.ExtractIP(r.RemoteAddr)
			allowed, _ := services.Authorize(r.Context(), "http", user, pass, clientIP)
			if !allowed {
				return r, goproxy.NewResponse(r, goproxy.ContentTypeText, http.StatusProxyAuthRequired, "Proxy Authentication Failed")
			}
		}
		return r, nil
	})

	proxy.OnRequest().HandleConnectFunc(func(host string, ctx *goproxy.ProxyCtx) (*goproxy.ConnectAction, string) {
		r := ctx.Req

		if args.AuthURL != nil && *args.AuthURL != "" {
			user, pass, ok := services.ParseBasicAuth(r.Header.Get("Proxy-Authorization"))
			if !ok && !*args.AuthNoUser {
				ctx.Resp = goproxy.NewResponse(r, goproxy.ContentTypeText, http.StatusProxyAuthRequired, "Proxy Authentication Required")
				return goproxy.RejectConnect, host
			}
			clientIP := services.ExtractIP(r.RemoteAddr)
			allowed, _ := services.Authorize(r.Context(), "http-connect", user, pass, clientIP)
			if !allowed {
				ctx.Resp = goproxy.NewResponse(r, goproxy.ContentTypeText, http.StatusProxyAuthRequired, "Proxy Authentication Failed")
				return goproxy.RejectConnect, host
			}
		}
		return goproxy.OkConnect, host
	})

	return proxy
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

	ctxConn, cancel := context.WithCancel(context.Background())

	if !l.reporter.ReporterModeFast() {
		c.SetOnClose(func(total int64) {
			// 先结束统计协程（若有），再做收尾上报
			defer cancel()
			rec := l.reporter.BuildRecord(serverAddr, clientAddr, targetAddr, conn, "", "", total)
			_ = l.reporter.ReportOnce(rec)
		})
		return c, nil
	}

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
				_ = l.reporter.ReportOnce(rec)
			}
		})
	}
	return c, nil
}

// startHTTPServer 启动HTTP代理服务器（入站连接统计）
func startHTTPServer(proxy *goproxy.ProxyHttpServer) {
	log.Info("http 模式启动", zap.String("port", *args.Local))

	// 包一层 handler，在请求进入 goproxy 前：
	// 1) 注入入口 IP
	// 2) 若为 CONNECT，请禁用入站侧统计回调
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if localAddr, ok := r.Context().Value(http.LocalAddrContextKey).(net.Addr); ok {
			if tcpAddr, ok := localAddr.(*net.TCPAddr); ok {
				r = r.WithContext(context.WithValue(r.Context(), localIPKey, tcpAddr.IP))
			}
		}
		if r.Method == http.MethodConnect {
			if connAny := r.Context().Value(inboundConnKey); connAny != nil {
				if cc, ok := connAny.(*services.CountingConn); ok {
					cc.SetOnClose(func(total int64) {})
				}
			}
		}
		proxy.ServeHTTP(w, r)
	})

	ln, err := net.Listen("tcp", *args.Local)
	if err != nil {
		log.Fatal(err.Error())
		return
	}

	// 设置流量报告器
	reporter, recordsCh := services.SetupTrafficReporter(&args, "http")

	il := &inboundListener{Listener: ln, reporter: reporter, recordsCh: recordsCh}
	srv := &http.Server{
		Handler: handler,
		ConnContext: func(ctx context.Context, c net.Conn) context.Context {
			// 将入站连接对象注入到请求上下文，便于在 CONNECT 时禁用入站统计
			return context.WithValue(ctx, inboundConnKey, c)
		},
	}

	if err := srv.Serve(il); err != nil {
		log.Fatal(err.Error())
	}
}

// createDialHandler 创建处理普通HTTP请求的拨号处理器
func createDialHandler() func(ctx context.Context, network, addr string) (net.Conn, error) {
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		return wrapConnection(ctx, network, addr)
	}
}

// wrapConnection 包装连接以进行流量统计
func wrapConnection(ctx context.Context, network, addr string) (net.Conn, error) {

	// 如果设置了bind-listen标志，配置代理使用监听IP作为外出IP
	var dialer net.Dialer
	if *args.BindListen {
		if ip, ok := ctx.Value(localIPKey).(net.IP); ok && ip != nil {
			dialer = net.Dialer{
				LocalAddr: &net.TCPAddr{IP: ip},
			}
		}
	}

	outConn, err := dialer.Dial(network, addr)
	if err != nil {
		return nil, err
	}

	c := &services.CountingConn{Conn: outConn}

	return c, nil
}
