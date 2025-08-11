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

var httpArgs services.HTTPArgs

// httpCmd represents the http command
var httpCmd = &cobra.Command{
	Use:              "http",
	Short:            "HTTP 代理",
	TraverseChildren: true,
	Run: func(cmd *cobra.Command, _ []string) {
		// 创建代理服务器并开始监听
		proxy := goproxy.NewProxyHttpServer()
		proxy.Verbose = *args.Verbose

		log.Info("http 模式启动", zap.String("port", *args.Local))

		// HTTP 请求鉴权（普通请求与 CONNECT）
		proxy.OnRequest().DoFunc(func(r *http.Request, ctx *goproxy.ProxyCtx) (*http.Request, *http.Response) {
			// 从 Proxy-Authorization 读取凭证
			user, pass, ok := services.ParseBasicAuth(r.Header.Get("Proxy-Authorization"))
			if !ok && !*args.AuthNoUser {
				return r, goproxy.NewResponse(r, goproxy.ContentTypeText, http.StatusProxyAuthRequired, "Proxy Authentication Required")
			}
			clientIP := services.ExtractIP(r.RemoteAddr)
			allowed, _ := services.Authorize(r.Context(), "http", user, pass, clientIP, map[string]string{
				"Method": r.Method,
				"Host":   r.Host,
				"Path":   r.URL.Path,
			})
			if !allowed {
				return r, goproxy.NewResponse(r, goproxy.ContentTypeText, http.StatusProxyAuthRequired, "Proxy Authentication Failed")
			}
			return r, nil
		})

		proxy.OnRequest().HandleConnectFunc(func(host string, ctx *goproxy.ProxyCtx) (*goproxy.ConnectAction, string) {
			r := ctx.Req
			user, pass, ok := services.ParseBasicAuth(r.Header.Get("Proxy-Authorization"))
			if !ok && !*args.AuthNoUser {
				ctx.Resp = goproxy.NewResponse(r, goproxy.ContentTypeText, http.StatusProxyAuthRequired, "Proxy Authentication Required")
				return goproxy.RejectConnect, host
			}
			clientIP := services.ExtractIP(r.RemoteAddr)
			allowed, _ := services.Authorize(r.Context(), "http-connect", user, pass, clientIP, map[string]string{"Host": host})
			if !allowed {
				ctx.Resp = goproxy.NewResponse(r, goproxy.ContentTypeText, http.StatusProxyAuthRequired, "Proxy Authentication Failed")
				return goproxy.RejectConnect, host
			}
			return goproxy.OkConnect, host
		})

		// 包一层 handler，在请求进入 goproxy 前注入入口 IP
		handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if localAddr, ok := r.Context().Value(http.LocalAddrContextKey).(net.Addr); ok {
				if tcpAddr, ok := localAddr.(*net.TCPAddr); ok {
					r = r.WithContext(context.WithValue(r.Context(), localIPKey, tcpAddr.IP))
				}
			}
			proxy.ServeHTTP(w, r)
		})

		// 如果设置了bind-listen标志，配置代理使用监听IP作为外出IP
		if *args.BindListen {
			// 自定义 Transport，根据入口 IP 出口
			proxy.Tr = &http.Transport{
				DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
					if ip, ok := ctx.Value(localIPKey).(net.IP); ok && ip != nil {
						dialer := &net.Dialer{
							LocalAddr: &net.TCPAddr{IP: ip},
						}
						return dialer.DialContext(ctx, network, address)
					}
					// 没取到就默认
					return new(net.Dialer).DialContext(ctx, network, address)
				},
			}
		}

		err := http.ListenAndServe(*args.Local, handler)
		if err != nil {
			log.Fatal(err.Error())
		}
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
