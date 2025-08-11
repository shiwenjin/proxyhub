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

// httpCmd represents the http command
var httpCmd = &cobra.Command{
	Use:              "http",
	TraverseChildren: true,
	Run: func(cmd *cobra.Command, _ []string) {
		proxy := goproxy.NewProxyHttpServer()
		proxy.Verbose = *args.Verbose

		// Traffic reporter (optional)
		reporter := services.NewTrafficReporter(&args, "http")
		var recordsCh chan services.TrafficRecord
		if reporter != nil && reporterModeFast(reporter) && reporterFastGlobal(reporter) {
			recordsCh = make(chan services.TrafficRecord, 2048)
			reporter.StartGlobalBatch(recordsCh)
		}

		// Custom transport to wrap dials
		tr := &http.Transport{}
		tr.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
			outConn, err := net.Dial(network, addr)
			if err != nil {
				return nil, err
			}
			if reporter == nil {
				return outConn, nil
			}
			c := &services.CountingConn{Conn: outConn}
			serverAddr := *args.Local
			clientAddr := "" // not trivial to fetch here
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
		proxy.Tr = tr
		proxy.ConnectDial = func(network, addr string) (net.Conn, error) {
			// HTTPS CONNECT
			outConn, err := net.Dial(network, addr)
			if err != nil {
				return nil, err
			}
			if reporter == nil {
				return outConn, nil
			}
			c := &services.CountingConn{Conn: outConn}
			serverAddr := *args.Local
			clientAddr := ""
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
				go perConnDeltaToChan(context.Background(), c, interval, func(d int64) {
					if d > 0 {
						recordsCh <- reporter.BuildRecord(serverAddr, clientAddr, targetAddr, outConn, "", "", d)
					}
				})
			} else {
				go perConnDeltaToReport(context.Background(), c, interval, func(d int64) {
					if d > 0 {
						_ = reporter.ReportOnce(reporter.BuildRecord(serverAddr, clientAddr, targetAddr, outConn, "", "", d))
					}
				})
			}
			return c, nil
		}

		log.Info("http ", zap.String("port", *args.Local))

		err := http.ListenAndServe(*args.Local, proxy)
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
