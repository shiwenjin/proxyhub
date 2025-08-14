package cmd

import (
	"os"
	"proxyhub/pkg/log"
	"proxyhub/services"
	"proxyhub/utils"

	"github.com/spf13/cobra"
)

var args = services.Args{}
var local string

// rootCmd represents the base command when called without any subcommands
var rootCmd = &cobra.Command{
	Use:   "proxyhub",
	Short: "ProxyHub 代理服务",
	Run: func(cmd *cobra.Command, _ []string) {
	},
}

// Execute adds all child commands to the root command and sets flags appropriately.
// This is called by main.main(). It only needs to happen once to the rootCmd.
func Execute() {
	err := rootCmd.Execute()
	if err != nil {
		os.Exit(1)
	}
}

// 确保日志系统在首次使用前已初始化
func initConfig() {
	if log.Default == nil {
		log.Default = log.InitZap(*args.LogFile, *args.LogWarn, *args.Env)
	}

	if services.DefaultAPI == nil {
		services.NewAPI(*args.AuthURL, *args.TrafficURL)
	}
}

func init() {
	cobra.OnInitialize(initConfig)

	//keygen
	if len(os.Args) > 1 {
		if os.Args[1] == "keygen" {
			utils.Keygen()
			os.Exit(0)
		}
	}

	//build srvice args
	args.Local = rootCmd.PersistentFlags().StringP("local", "p", ":33080", "local ip:port to listen")
	args.Verbose = rootCmd.PersistentFlags().BoolP("verbose", "v", true, "verbose mode")
	args.LogFile = rootCmd.PersistentFlags().StringP("log", "l", "", "log file path, empty means output to console")
	args.LogWarn = rootCmd.PersistentFlags().Bool("warn", false, "only log warn mode")
	args.Env = rootCmd.PersistentFlags().String("env", "dev", "env")
	args.BindListen = rootCmd.PersistentFlags().Bool("bind-listen", false, "use listening IP as outgoing IP for proxy connections")
	args.AuthURL = rootCmd.PersistentFlags().String("auth-url", "", "HTTP API address for proxy authentication, 204 means success")
	args.AuthNoUser = rootCmd.PersistentFlags().Bool("auth-nouser", false, "allow auth without Proxy-Authorization user/pass")
	args.AuthCacheSec = rootCmd.PersistentFlags().Int("auth-cache", 0, "cache success auth result seconds (0=disabled)")
	args.AuthFailCacheSec = rootCmd.PersistentFlags().Int("auth-fail-cache", 0, "cache failed auth result seconds (0=disabled)")

	// 初始化 Resty 鉴权客户端（在 flags 解析后）
	cobra.OnInitialize(func() { services.InitAuth(args) })

	// traffic report flags
	args.TrafficURL = rootCmd.PersistentFlags().String("traffic-url", "", "traffic report http endpoint URL")
	args.TrafficMode = rootCmd.PersistentFlags().String("traffic-mode", "normal", "traffic report mode <normal|fast>")
	args.TrafficInterval = rootCmd.PersistentFlags().Int("traffic-interval", 5, "traffic report interval seconds when --traffic-mode=fast")
	args.FastGlobal = rootCmd.PersistentFlags().Bool("fast-global", false, "enable global fast report, only effective when --traffic-mode=fast")
	args.ServiceID = rootCmd.PersistentFlags().String("id", "", "service id used in traffic report (default inferred by command)")

	certTLS := rootCmd.PersistentFlags().StringP("cert", "C", "proxy.crt", "cert file for tls")
	keyTLS := rootCmd.PersistentFlags().StringP("key", "K", "proxy.key", "key file for tls")

	// 日志系统将在Execute函数中初始化
	if *certTLS != "" && *keyTLS != "" {
		args.CertBytes, args.KeyBytes = tlsBytes(*certTLS, *keyTLS)
	}
}

func tlsBytes(cert, key string) (certBytes, keyBytes []byte) {
	certBytes, err := os.ReadFile(cert)
	if err != nil {
		println("读取证书文件失败:", err.Error())
		return
	}
	keyBytes, err = os.ReadFile(key)
	if err != nil {
		println("读取密钥文件失败:", err.Error())
		return
	}
	return
}
