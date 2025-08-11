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

		// Enable SOCKS5 username/password handshake and capture credentials
		methods := []socks5.Authenticator{}
		if args.AuthURL != nil && *args.AuthURL != "" {
			methods = append(methods, socks5.UserPassAuthenticator{Credentials: acceptAllCredentials{}})
		}
		if args.AuthNoUser != nil && *args.AuthNoUser {
			methods = append(methods, socks5.NoAuthAuthenticator{})
		}

		server := socks5.NewServer(
			socks5.WithLogger(log.Default.Sugar()),
			socks5.WithAuthMethods(methods),
			socks5.WithDialAndRequest(func(ctx context.Context, network, addr string, request *socks5.Request) (net.Conn, error) {
				log.Info("Request from %s to %s", zap.Any("remote", request.RemoteAddr), zap.Any("dest", request.DestAddr))

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
					allowed, _ := services.Authorize(ctx, "socks5", username, password, clientIP, map[string]string{
						"Dest": request.DestAddr.String(),
					})
					if !allowed {
						return nil, statute.ErrUserAuthFailed
					}
				}

				// 如果设置了bind-listen标志，使用入口(本地监听)IP作为出口IP
				if *args.BindListen {
					if tcpAddr, ok := request.LocalAddr.(*net.TCPAddr); ok {
						dialer := &net.Dialer{
							LocalAddr: &net.TCPAddr{IP: tcpAddr.IP},
						}
						return dialer.DialContext(ctx, network, addr)
					}
				}

				// 默认连接方式（支持上下文取消/超时）
				return new(net.Dialer).DialContext(ctx, network, addr)
			}),
		)

		log.Info("SOCKS5 server started", zap.String("addr", *args.Local))
		if err := server.ListenAndServe("tcp", *args.Local); err != nil {
			panic(err)
		}
	},
}

func init() {
	rootCmd.AddCommand(socksCmd)
}
