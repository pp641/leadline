// Package metrics declares the Prometheus metrics shared by the API and workers.
package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	APIRequests = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "api_requests_total", Help: "HTTP requests by route, method and status.",
	}, []string{"route", "method", "status"})

	APILatency = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name: "api_latency_seconds", Help: "HTTP request latency.", Buckets: prometheus.DefBuckets,
	}, []string{"route", "method"})

	QueueDepth = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "queue_depth", Help: "Jobs waiting in each queue (ready + delayed).",
	}, []string{"queue", "state"})

	JobsProcessed = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "jobs_processed_total", Help: "Jobs completed successfully.",
	}, []string{"type"})

	JobsFailed = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "jobs_failed_total", Help: "Job attempts that returned an error.",
	}, []string{"type", "final"})

	EmailsSent = promauto.NewCounter(prometheus.CounterOpts{
		Name: "emails_sent_total", Help: "Emails accepted by the provider.",
	})

	EmailsFailed = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "emails_failed_total", Help: "Email send attempts that failed.",
	}, []string{"permanent"})

	Bounces = promauto.NewCounter(prometheus.CounterOpts{
		Name: "email_bounces_total", Help: "Bounce events received.",
	})

	Replies = promauto.NewCounter(prometheus.CounterOpts{
		Name: "email_replies_total", Help: "Reply events received.",
	})
)
