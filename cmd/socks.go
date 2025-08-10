package cmd

import (
	"context"
	"net"
	"proxyhub/pkg/log"

	"github.com/spf13/cobra"
	"github.com/things-go/go-socks5"
	"go.uber.org/zap"
)

// socksCmd represents the socks command
var socksCmd = &cobra.Command{
	Use:   "socks",
	Short: "SOCKS 代理",
	Run: func(cmd *cobra.Command, _ []string) {
		// Create a SOCKS5 server
		server := socks5.NewServer(
			socks5.WithLogger(log.Default.Sugar()),
			socks5.WithDialAndRequest(func(ctx context.Context, network, addr string, request *socks5.Request) (net.Conn, error) {
				log.Info("Request from %s to %s", zap.Any("remote", request.RemoteAddr), zap.Any("dest", request.DestAddr))
				return net.Dial(network, addr)
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
