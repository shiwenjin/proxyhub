package services

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"proxyhub/pkg/log"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"go.uber.org/zap"
)

type TrafficRecord struct {
	ID            string `json:"id"`
	ServerAddr    string `json:"server_addr"`
	ClientAddr    string `json:"client_addr"`
	TargetAddr    string `json:"target_addr"`
	Username      string `json:"username"`
	Bytes         int64  `json:"bytes"`
	OutLocalAddr  string `json:"out_local_addr"`
	OutRemoteAddr string `json:"out_remote_addr"`
	Upstream      string `json:"upstream"`
	SniffDomain   string `json:"sniff_domain"`
}

type TrafficReporter struct {
	trafficURL string
	mode       string // normal|fast
	interval   time.Duration
	fastGlobal bool
	serviceID  string

	client *http.Client

	// global fast mode
	mu        sync.Mutex
	started   bool
	batchStop chan struct{}
}

// SetupTrafficReporter 初始化流量报告器
func SetupTrafficReporter(a *Args, defaultID string) (*TrafficReporter, chan TrafficRecord) {
	reporter := NewTrafficReporter(a, defaultID)
	var recordsCh chan TrafficRecord

	if reporter != nil && reporter.ReporterFastGlobal() {
		recordsCh = make(chan TrafficRecord, 2048)
		reporter.StartGlobalBatch(recordsCh)
	}

	return reporter, recordsCh
}

func NewTrafficReporter(a *Args, defaultID string) *TrafficReporter {
	if a == nil || a.TrafficURL == nil || *a.TrafficURL == "" {
		return nil
	}
	mode := TRAFFIC_MODE_NORMAL
	if a.TrafficMode != nil && *a.TrafficMode != "" {
		mode = strings.ToLower(*a.TrafficMode)
	}
	interval := 5 * time.Second
	if a.TrafficInterval != nil && *a.TrafficInterval > 0 {
		interval = time.Duration(*a.TrafficInterval) * time.Second
	}
	id := defaultID
	if a.ServiceID != nil && *a.ServiceID != "" {
		id = *a.ServiceID
	}
	tr := &TrafficReporter{
		trafficURL: *a.TrafficURL,
		mode:       mode,
		interval:   interval,
		fastGlobal: a.FastGlobal != nil && *a.FastGlobal,
		serviceID:  id,
		client:     &http.Client{Timeout: 5 * time.Second},
	}
	return tr
}

// ReportOnce sends a single GET request with record as query params
func (tr *TrafficReporter) ReportOnce(rec TrafficRecord) error {
	if tr == nil {
		return nil
	}
	return DefaultAPI.Report(rec)
}

// StartGlobalBatch ensures a single ticker that POSTs JSON array records
func (tr *TrafficReporter) StartGlobalBatch(recordsCh <-chan TrafficRecord) {
	if tr == nil || !tr.fastGlobal || strings.ToLower(tr.mode) != TRAFFIC_MODE_FAST {
		return
	}
	tr.mu.Lock()
	defer tr.mu.Unlock()
	if tr.started {
		return
	}
	tr.started = true
	tr.batchStop = make(chan struct{})
	go func() {
		ticker := time.NewTicker(tr.interval)
		defer ticker.Stop()
		batch := make([]TrafficRecord, 0, 64)
		flush := func() {
			if len(batch) == 0 {
				return
			}
			// sum bytes for logging
			var total int64
			for i := range batch {
				total += batch[i].Bytes
			}
			buf, _ := json.Marshal(batch)
			req, err := http.NewRequest(http.MethodPost, tr.trafficURL, bytes.NewReader(buf))
			if err != nil {
				log.Warn("traffic batch build request failed", zap.Error(err))
				batch = batch[:0]
				return
			}
			req.Header.Set("Content-Type", "application/json")
			log.Info("traffic report batch (POST)",
				zap.Int("records", len(batch)),
				zap.Int64("bytes", total),
				zap.String("url", tr.trafficURL),
			)
			resp, err := tr.client.Do(req)
			if err != nil {
				log.Warn("traffic batch request error", zap.Error(err))
				batch = batch[:0]
				return
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusNoContent {
				log.Warn("traffic batch failed",
					zap.Int("status", resp.StatusCode),
					zap.Int("records", len(batch)),
					zap.Int64("bytes", total),
				)
			} else {
				log.Info("traffic batch ok",
					zap.Int("status", resp.StatusCode),
					zap.Int("records", len(batch)),
					zap.Int64("bytes", total),
				)
			}
			batch = batch[:0]
		}
		for {
			select {
			case <-tr.batchStop:
				flush()
				return
			case rec := <-recordsCh:
				batch = append(batch, rec)
			case <-ticker.C:
				flush()
			}
		}
	}()
}

// StopGlobalBatch stops the global batch goroutine
func (tr *TrafficReporter) StopGlobalBatch() {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	if tr.started {
		close(tr.batchStop)
		tr.started = false
	}
}

func (tr *TrafficReporter) ReporterModeFast() bool {
	return tr != nil && tr.mode == TRAFFIC_MODE_FAST
}

func (tr *TrafficReporter) ReporterFastGlobal() bool {
	return tr.ReporterModeFast() && tr.fastGlobal
}

func (tr *TrafficReporter) ReporterInterval() time.Duration {
	if tr == nil || tr.interval <= 0 {
		return 5 * time.Second
	}
	return tr.interval
}

// BuildRecord builds a TrafficRecord with common fields
func (tr *TrafficReporter) BuildRecord(serverAddr, clientAddr, targetAddr string, outConn net.Conn, username, sniffDomain string, bytes int64) TrafficRecord {
	rec := TrafficRecord{
		ID:          tr.serviceID,
		ServerAddr:  serverAddr,
		ClientAddr:  clientAddr,
		TargetAddr:  targetAddr,
		Username:    username,
		Bytes:       bytes,
		Upstream:    "",
		SniffDomain: sniffDomain,
	}
	if outConn != nil {
		rec.OutLocalAddr = hostPort(outConn.LocalAddr())
		rec.OutRemoteAddr = hostPort(outConn.RemoteAddr())
	}
	return rec
}

func hostPort(a net.Addr) string {
	if a == nil {
		return ""
	}
	return a.String()
}

// CountingConn wraps a net.Conn to count read/write bytes
// The sum of read+write approximates total traffic for the connection
// which suits the requirement of reporting "bytes".

type CountingConn struct {
	net.Conn
	readN   int64
	writeN  int64
	last    int64
	onClose func(total int64)
}

func (c *CountingConn) Read(b []byte) (int, error) {
	n, err := c.Conn.Read(b)
	atomic.AddInt64(&c.readN, int64(n))
	return n, err
}
func (c *CountingConn) Write(b []byte) (int, error) {
	n, err := c.Conn.Write(b)
	atomic.AddInt64(&c.writeN, int64(n))
	return n, err
}

func (c *CountingConn) Total() int64 {
	return atomic.LoadInt64(&c.readN) + atomic.LoadInt64(&c.writeN)
}

func (c *CountingConn) Delta() int64 {
	t := c.Total()
	d := t - atomic.LoadInt64(&c.last)
	atomic.StoreInt64(&c.last, t)
	return d
}
func (c *CountingConn) Close() error {
	if c.onClose != nil {
		c.onClose(c.Total())
	}
	return c.Conn.Close()
}

// SetOnClose sets a callback invoked right before underlying Conn.Close()
func (c *CountingConn) SetOnClose(fn func(total int64)) {
	log.Info("set on close")
	c.onClose = fn
}

func (c *CountingConn) PerConnDeltaToChan(ctx context.Context, interval time.Duration, emit func(d int64)) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			if d := c.Delta(); d > 0 {
				log.Info("traffic delta done-------------", zap.Int64("delta", d))
				emit(d)
			}
			return
		case <-ticker.C:
			if d := c.Delta(); d > 0 {
				log.Info("traffic delta ticker-------------", zap.Int64("delta", d))
				emit(d)
			}
		}
	}
}

func (c *CountingConn) PerConnDeltaToReport(ctx context.Context, interval time.Duration, report func(d int64)) {
	c.PerConnDeltaToChan(ctx, interval, report)
}
