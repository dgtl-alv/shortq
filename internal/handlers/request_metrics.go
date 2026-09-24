package handlers

import (
	"net/http"
	"sync/atomic"
)

type RequestMetrics struct {
	inFlight    atomic.Uint64
	maxInFlight atomic.Uint64
	total       atomic.Uint64
}

type RequestMetricsSnapshot struct {
	InFlight    uint64 `json:"in_flight"`
	MaxInFlight uint64 `json:"max_in_flight"`
	Total       uint64 `json:"total"`
}

func (m *RequestMetrics) Wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		current := m.inFlight.Add(1)
		m.total.Add(1)
		for maximum := m.maxInFlight.Load(); current > maximum && !m.maxInFlight.CompareAndSwap(maximum, current); maximum = m.maxInFlight.Load() {
		}
		defer m.inFlight.Add(^uint64(0))
		next.ServeHTTP(w, r)
	})
}

func (m *RequestMetrics) Snapshot() RequestMetricsSnapshot {
	if m == nil {
		return RequestMetricsSnapshot{}
	}
	return RequestMetricsSnapshot{
		InFlight:    m.inFlight.Load(),
		MaxInFlight: m.maxInFlight.Load(),
		Total:       m.total.Load(),
	}
}
