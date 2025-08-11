package cmd

import (
    "context"
    "net"
    "time"
    "proxyhub/pkg/log"
    "proxyhub/services"

    "github.com/spf13/cobra"
    "github.com/things-go/go-socks5"
    "go.uber.org/zap"
)

// socksCmd represents the socks command
var socksCmd = &cobra.Command{
    Use:   "socks",
    Short: "SOCKS 代理",
    Run: func(cmd *cobra.Command, _ []string) {
        // Traffic reporter (optional)
        reporter := services.NewTrafficReporter(&args, "socks")
        var recordsCh chan services.TrafficRecord
        if reporter != nil && reporterModeFast(reporter) && reporterFastGlobal(reporter) {
            recordsCh = make(chan services.TrafficRecord, 1024)
            reporter.StartGlobalBatch(recordsCh)
        }

        // Create a SOCKS5 server
        server := socks5.NewServer(
            socks5.WithLogger(log.Default.Sugar()),
            socks5.WithDialAndRequest(func(ctx context.Context, network, addr string, request *socks5.Request) (net.Conn, error) {
                log.Info("socks request", zap.Any("remote", request.RemoteAddr), zap.Any("dest", request.DestAddr))
                // dial real server
                outConn, err := net.Dial(network, addr)
                if err != nil { return nil, err }

                if reporter == nil {
                    return outConn, nil
                }

                // wrap counting
                c := &services.CountingConn{Conn: outConn}
                serverAddr := *args.Local
                clientAddr := ""
                if request.RemoteAddr != nil { clientAddr = request.RemoteAddr.String() }
                targetAddr := ""
                if request.DestAddr != nil { targetAddr = request.DestAddr.String() }

                // normal: report on close total bytes
                if !reporterModeFast(reporter) {
                    c.SetOnClose(func(total int64) {
                        rec := reporter.BuildRecord(serverAddr, clientAddr, targetAddr, outConn, "", "", total)
                        _ = reporter.ReportOnce(rec)
                    })
                    return c, nil
                }

                // fast (per-conn or global)
                interval := reporterInterval(reporter)
                if reporterFastGlobal(reporter) && recordsCh != nil {
                    // send deltas to global channel
                    go func() {
                        ticker := time.NewTicker(interval)
                        defer ticker.Stop()
                        for {
                            select {
                            case <-ctx.Done():
                                // no final report in plan A
                                return
                            case <-ticker.C:
                                d := c.Delta()
                                if d > 0 {
                                    rec := reporter.BuildRecord(serverAddr, clientAddr, targetAddr, outConn, "", "", d)
                                    recordsCh <- rec
                                }
                            }
                        }
                    }()
                } else {
                    // per-connection fast: direct GET with delta
                    go func() {
                        ticker := time.NewTicker(interval)
                        defer ticker.Stop()
                        for {
                            select {
                            case <-ctx.Done():
                                // no final report in plan A
                                return
                            case <-ticker.C:
                                d := c.Delta()
                                if d > 0 {
                                    rec := reporter.BuildRecord(serverAddr, clientAddr, targetAddr, outConn, "", "", d)
                                    _ = reporter.ReportOnce(rec)
                                }
                            }
                        }
                    }()
                }

                return c, nil
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
