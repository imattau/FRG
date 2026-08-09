package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestMetricsListenerRequiresLoopback(t *testing.T) {
	if _, _, err := startMetricsServer("0.0.0.0:9090", nil, newNodeMetrics()); err == nil {
		t.Fatal("non-loopback metrics listener was accepted")
	}
}

func TestMetricsReadinessRejectsMissingRuntime(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	resp := httptest.NewRecorder()
	metricsHandler(nil, newNodeMetrics()).ServeHTTP(resp, req)
	if resp.Code != http.StatusServiceUnavailable {
		t.Fatalf("readiness status = %d, want %d", resp.Code, http.StatusServiceUnavailable)
	}
}
