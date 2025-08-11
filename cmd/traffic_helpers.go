package cmd

import (
	"context"
	"proxyhub/services"
	"time"
)

func reporterModeFast(tr *services.TrafficReporter) bool {
	return tr != nil && *args.TrafficMode == services.TRAFFIC_MODE_FAST
}
func reporterFastGlobal(tr *services.TrafficReporter) bool {
	return tr != nil && args.FastGlobal != nil && *args.FastGlobal
}
func reporterInterval(tr *services.TrafficReporter) time.Duration {
	if tr == nil || args.TrafficInterval == nil || *args.TrafficInterval <= 0 {
		return 5 * time.Second
	}
	return time.Duration(*args.TrafficInterval) * time.Second
}

func perConnDeltaToChan(ctx context.Context, c *services.CountingConn, interval time.Duration, emit func(d int64)) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			// Plan A: do not emit final delta on close; only periodic reports
			return
		case <-ticker.C:
			if d := c.Delta(); d > 0 {
				emit(d)
			}
		}
	}
}

func perConnDeltaToReport(ctx context.Context, c *services.CountingConn, interval time.Duration, report func(d int64)) {
	perConnDeltaToChan(ctx, c, interval, report)
}
