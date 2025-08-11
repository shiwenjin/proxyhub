package services

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"proxyhub/pkg/log"
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
		client: &http.Client{Timeout: 5 * time.Second},
	}
	return tr
}

// ReportOnce sends a single GET request with record as query params
func (tr *TrafficReporter) ReportOnce(rec TrafficRecord) error {
	if tr == nil {
		return nil
	}
	u, err := url.Parse(tr.trafficURL)
	if err != nil { return err }
	q := u.Query()
	q.Set("id", rec.ID)
	q.Set("server_addr", rec.ServerAddr)
	q.Set("client_addr", rec.ClientAddr)
	if rec.TargetAddr != "" { q.Set("target_addr", rec.TargetAddr) }
	if rec.Username != "" { q.Set("username", rec.Username) }
	q.Set("bytes", strconv.FormatInt(rec.Bytes, 10))
	if rec.OutLocalAddr != "" { q.Set("out_local_addr", rec.OutLocalAddr) }
	if rec.OutRemoteAddr != "" { q.Set("out_remote_addr", rec.OutRemoteAddr) }
	if rec.Upstream != "" { q.Set("upstream", rec.Upstream) }
	if rec.SniffDomain != "" { q.Set("sniff_domain", rec.SniffDomain) }
	u.RawQuery = q.Encode()
	req, err := http.NewRequest(http.MethodGet, u.String(), nil)
	if err != nil { return err }
	log.Info("traffic report (GET)",
		zap.String("mode", tr.mode),
		zap.String("url", u.String()),
		zap.String("id", rec.ID),
		zap.Int64("bytes", rec.Bytes),
		zap.String("server_addr", rec.ServerAddr),
		zap.String("client_addr", rec.ClientAddr),
		zap.String("target_addr", rec.TargetAddr),
	)
	resp, err := tr.client.Do(req)
	if err != nil { return err }
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		log.Warn("traffic report failed",
			zap.Int("status", resp.StatusCode),
			zap.String("url", u.String()),
			zap.Int64("bytes", rec.Bytes),
		)
		return fmt.Errorf("traffic report failed, status=%d", resp.StatusCode)
	}
	log.Info("traffic report ok",
		zap.Int("status", resp.StatusCode),
		zap.String("url", u.String()),
		zap.Int64("bytes", rec.Bytes),
	)
	return nil
}

// StartGlobalBatch ensures a single ticker that POSTs JSON array records
func (tr *TrafficReporter) StartGlobalBatch(recordsCh <-chan TrafficRecord) {
	if tr == nil || !tr.fastGlobal || strings.ToLower(tr.mode) != TRAFFIC_MODE_FAST {
		return
	}
	tr.mu.Lock()
	defer tr.mu.Unlock()
	if tr.started { return }
	tr.started = true
	tr.batchStop = make(chan struct{})
	go func() {
		ticker := time.NewTicker(tr.interval)
		defer ticker.Stop()
		batch := make([]TrafficRecord, 0, 64)
		flush := func() {
			if len(batch) == 0 { return }
			// sum bytes for logging
			var total int64
			for i := range batch { total += batch[i].Bytes }
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
				flush(); return
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
	tr.mu.Lock(); defer tr.mu.Unlock()
	if tr.started {
		close(tr.batchStop)
		tr.started = false
	}
}

// CountingConn wraps a net.Conn to count read/write bytes
// The sum of read+write approximates total traffic for the connection
// which suits the requirement of reporting "bytes".

type CountingConn struct {
	net.Conn
	readN  int64
	writeN int64
	last   int64
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
func (c *CountingConn) Total() int64 { return atomic.LoadInt64(&c.readN) + atomic.LoadInt64(&c.writeN) }
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
func (c *CountingConn) SetOnClose(fn func(total int64)) { c.onClose = fn }

// BuildRecord builds a TrafficRecord with common fields
func (tr *TrafficReporter) BuildRecord(serverAddr, clientAddr, targetAddr string, outConn net.Conn, username, sniffDomain string, bytes int64) TrafficRecord {
	rec := TrafficRecord{
		ID:           tr.serviceID,
		ServerAddr:   serverAddr,
		ClientAddr:   clientAddr,
		TargetAddr:   targetAddr,
		Username:     username,
		Bytes:        bytes,
		Upstream:     "",
		SniffDomain:  sniffDomain,
	}
	if outConn != nil {
		rec.OutLocalAddr = hostPort(outConn.LocalAddr())
		rec.OutRemoteAddr = hostPort(outConn.RemoteAddr())
	}
	return rec
}

func hostPort(a net.Addr) string {
	if a == nil { return "" }
	return a.String()
}
