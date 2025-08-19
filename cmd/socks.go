package cmd

import (
	"context"
	"net"
	"proxyhub/pkg/log"
	"proxyhub/services"

	"github.com/spf13/cobra"
	"github.com/things-go/go-socks5"
	"github.com/things-go/go-socks5/statute"
	"go.uber.org/zap"
)

// acceptAllCredentials accepts any user/password during the SOCKS5 handshake
// so we can perform authorization ourselves based on captured credentials.
type acceptAllCredentials struct{}

func (acceptAllCredentials) Valid(user, password, userAddr string) bool { return true }

// socksCmd represents the socks command
var socksCmd = &cobra.Command{
	Use:   "socks",
	Short: "SOCKS 代理",
	Run: func(cmd *cobra.Command, _ []string) {

		// 创建SOCKS5服务器
		server := createSocksServer()

		startSocksServer(server)
	},
}

func init() {
	rootCmd.AddCommand(socksCmd)
}

// startSocksServer 启动SOCKS5服务器（使用入站统计 listener）
func startSocksServer(server *socks5.Server) {
	log.Info("SOCKS5 server started", zap.String("addr", *args.Local))
	ln, err := net.Listen("tcp", *args.Local)
	if err != nil {
		panic(err)
	}

	// 设置流量报告器
	reporter, recordsCh := services.SetupTrafficReporter(&args, "socks")

	il := &inboundListener{Listener: ln, reporter: reporter, recordsCh: recordsCh}
	if err := server.Serve(il); err != nil {
		panic(err)
	}
}

func createSocksServer() *socks5.Server {

	// Enable SOCKS5 username/password handshake and capture credentials
	methods := []socks5.Authenticator{}
	if args.AuthURL != nil && *args.AuthURL != "" {
		methods = append(methods, socks5.UserPassAuthenticator{Credentials: acceptAllCredentials{}})
	}
	if args.AuthNoUser != nil && *args.AuthNoUser {
		methods = append(methods, socks5.NoAuthAuthenticator{})
	}

	return socks5.NewServer(
		socks5.WithLogger(log.Default.Sugar()),
		socks5.WithAuthMethods(methods),
		socks5.WithDialAndRequest(func(ctx context.Context, network, addr string, request *socks5.Request) (net.Conn, error) {

			if args.AuthURL != nil && *args.AuthURL != "" {
				var username, password string
				if request.AuthContext != nil {
					username = request.AuthContext.Payload["username"]
					password = request.AuthContext.Payload["password"]
				}
				if username == "" && password == "" && !*args.AuthNoUser {
					return nil, statute.ErrUserAuthFailed
				}
				clientIP := services.ExtractIP(request.RemoteAddr)
				allowed, authResult, _ := services.AuthorizeWithResult(ctx, "socks5", username, password, clientIP)
				if !allowed {
					return nil, statute.ErrUserAuthFailed
				}
				if !services.DefaultLimits.AllowQPS(username, clientIP) {
					return nil, statute.ErrUserAuthFailed
				}
				// 保存鉴权信息到 context，便于 Dial 之后拿到进行限速
				ctx = services.SetAuthInfoOnContext(ctx, "socks5", username, clientIP, authResult.UserId, authResult.TeamId)
			}

			return handleSocksConnection(ctx, network, addr, request)
		}),
	)
}

func handleSocksConnection(ctx context.Context, network, addr string, request *socks5.Request) (net.Conn, error) {
	log.Info("socks request", zap.Any("remote", request.RemoteAddr), zap.Any("dest", request.DestAddr))
	// 连接真实服务器
	// 如果设置了bind-listen标志，使用入口(本地监听)IP作为出口IP
	var dialer net.Dialer
	if *args.BindListen {
		if tcpAddr, ok := request.LocalAddr.(*net.TCPAddr); ok {
			dialer = net.Dialer{
				LocalAddr: &net.TCPAddr{IP: tcpAddr.IP},
			}
		}
	}

	outConn, err := dialer.Dial(network, addr)
	if err != nil {
		return nil, err
	}

	// 从 context 提取鉴权信息，构建限速器
	username, clientIP, userId, teamId := services.ExtractAuthInfo(ctx)
	shared := services.DefaultLimits.BuildConnLimiters(username, clientIP)
	limited := services.NewLimiterConn(ctx, outConn, shared)
	// 包装连接以进行计数
	c := &services.CountingConn{Conn: limited}

	// 设置鉴权信息到 CountingConn
	c.SetAuthInfo(username, userId, teamId)

	return c, nil
}
