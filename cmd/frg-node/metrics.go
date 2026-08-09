package main

import (
	"fmt"
	"log"
	"net"
	"net/http"
	"strings"
	"sync/atomic"
	"time"
)

type nodeMetrics struct {
	started       time.Time
	rpcRequests   atomic.Uint64
	rpcRejected   atomic.Uint64
	txAccepted    atomic.Uint64
	batchAccepted atomic.Uint64
	syncAttempts  atomic.Uint64
	syncFailures  atomic.Uint64
}

func newNodeMetrics() *nodeMetrics {
	return &nodeMetrics{started: time.Now()}
}

func startMetricsServer(listenAddr string, runtime *nodeRuntime, metrics *nodeMetrics) (*http.Server, string, error) {
	if strings.TrimSpace(listenAddr) == "" {
		return nil, "", nil
	}
	if !isLoopbackTCPAddress(listenAddr) {
		return nil, "", fmt.Errorf("metrics listener must be loopback")
	}
	server := &http.Server{
		Addr:              listenAddr,
		Handler:           metricsHandler(runtime, metrics),
		ReadHeaderTimeout: 2 * time.Second,
		IdleTimeout:       30 * time.Second,
	}
	listener, err := net.Listen("tcp", listenAddr)
	if err != nil {
		return nil, "", fmt.Errorf("listen metrics %s: %w", listenAddr, err)
	}
	go func() {
		if err := server.Serve(listener); err != nil && err != http.ErrServerClosed {
			log.Printf("metrics server stopped: %v", err)
		}
	}()
	return server, listener.Addr().String(), nil
}

func isLoopbackTCPAddress(listenAddr string) bool {
	host, _, err := net.SplitHostPort(listenAddr)
	if err != nil {
		return false
	}
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func metricsHandler(runtime *nodeRuntime, metrics *nodeMetrics) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/metrics" && r.URL.Path != "/readyz" {
			http.NotFound(w, r)
			return
		}
		if r.URL.Path == "/readyz" {
			if runtime == nil || runtime.sm == nil {
				http.Error(w, "not ready", http.StatusServiceUnavailable)
				return
			}
			if _, err := runtime.sm.CurrentHeight(); err != nil {
				http.Error(w, "not ready", http.StatusServiceUnavailable)
				return
			}
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("ready\n"))
			return
		}
		status, err := runtime.Status()
		if err != nil {
			http.Error(w, "metrics unavailable", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "text/plain; version=0.0.4")
		up := time.Since(metrics.started).Seconds()
		fmt.Fprintf(w, "# TYPE frg_uptime_seconds gauge\nfrg_uptime_seconds %f\n", up)
		fmt.Fprintf(w, "# TYPE frg_block_height gauge\nfrg_block_height %d\n", status.Height)
		fmt.Fprintf(w, "# TYPE frg_peer_count gauge\nfrg_peer_count %d\n", status.PeerCount)
		fmt.Fprintf(w, "# TYPE frg_mempool_length gauge\nfrg_mempool_length %d\n", status.MempoolLen)
		fmt.Fprintf(w, "# TYPE frg_validator_count gauge\nfrg_validator_count %d\n", status.ValidatorCount)
		fmt.Fprintf(w, "# TYPE frg_rpc_requests_total counter\nfrg_rpc_requests_total %d\n", metrics.rpcRequests.Load())
		fmt.Fprintf(w, "# TYPE frg_rpc_rejected_total counter\nfrg_rpc_rejected_total %d\n", metrics.rpcRejected.Load())
		fmt.Fprintf(w, "# TYPE frg_transactions_accepted_total counter\nfrg_transactions_accepted_total %d\n", metrics.txAccepted.Load())
		fmt.Fprintf(w, "# TYPE frg_batches_accepted_total counter\nfrg_batches_accepted_total %d\n", metrics.batchAccepted.Load())
		fmt.Fprintf(w, "# TYPE frg_sync_attempts_total counter\nfrg_sync_attempts_total %d\n", metrics.syncAttempts.Load())
		fmt.Fprintf(w, "# TYPE frg_sync_failures_total counter\nfrg_sync_failures_total %d\n", metrics.syncFailures.Load())
	})
}
