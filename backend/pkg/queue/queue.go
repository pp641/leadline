// Package queue is a small Redis/Valkey-backed job queue with delayed retries,
// exponential backoff, a dead-letter list and graceful shutdown.
//
// Redis only carries job envelopes. Handlers must treat PostgreSQL as the
// source of truth and be idempotent: a job may be delivered more than once
// (e.g. re-enqueued by a recovery sweep), and a crash can lose an in-flight job.
package queue

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"math/rand/v2"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"

	"leadline/pkg/metrics"
)

type Job struct {
	ID          string          `json:"id"`
	Type        string          `json:"type"`
	Payload     json.RawMessage `json:"payload"`
	Attempt     int             `json:"attempt"` // 1 on first run
	MaxAttempts int             `json:"max_attempts"`
	EnqueuedAt  time.Time       `json:"enqueued_at"`
	LastError   string          `json:"last_error,omitempty"`
}

// Decode unmarshals the payload into v.
func (j Job) Decode(v any) error { return json.Unmarshal(j.Payload, v) }

// IsFinalAttempt reports whether a failure now will dead-letter the job.
func (j Job) IsFinalAttempt() bool { return j.Attempt >= j.MaxAttempts }

type Handler func(ctx context.Context, job Job) error

// permanentError marks a failure that must not be retried.
type permanentError struct{ err error }

func (p permanentError) Error() string { return p.err.Error() }
func (p permanentError) Unwrap() error { return p.err }

// Permanent wraps err so the job goes straight to the dead-letter list.
func Permanent(err error) error { return permanentError{err} }

func IsPermanent(err error) bool {
	var p permanentError
	return errors.As(err, &p)
}

func readyKey(q string) string   { return "q:" + q + ":ready" }
func delayedKey(q string) string { return "q:" + q + ":delayed" }
func deadKey(q string) string    { return "q:" + q + ":dead" }

// Client enqueues jobs. It is safe for concurrent use.
type Client struct{ rdb *redis.Client }

func NewClient(rdb *redis.Client) *Client { return &Client{rdb: rdb} }

type EnqueueOpts struct {
	MaxAttempts int
	Delay       time.Duration
}

func (c *Client) Enqueue(ctx context.Context, queueName, jobType string, payload any, opts ...EnqueueOpts) error {
	var o EnqueueOpts
	if len(opts) > 0 {
		o = opts[0]
	}
	if o.MaxAttempts <= 0 {
		o.MaxAttempts = 5
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	job := Job{ID: uuid.NewString(), Type: jobType, Payload: raw, MaxAttempts: o.MaxAttempts, EnqueuedAt: time.Now().UTC()}
	data, err := json.Marshal(job)
	if err != nil {
		return err
	}
	if o.Delay > 0 {
		return c.rdb.ZAdd(ctx, delayedKey(queueName), redis.Z{
			Score: float64(time.Now().Add(o.Delay).UnixMilli()), Member: data,
		}).Err()
	}
	return c.rdb.LPush(ctx, readyKey(queueName), data).Err()
}

// Depth returns ready, delayed and dead counts for a queue.
func (c *Client) Depth(ctx context.Context, queueName string) (ready, delayed, dead int64, err error) {
	pipe := c.rdb.Pipeline()
	r := pipe.LLen(ctx, readyKey(queueName))
	d := pipe.ZCard(ctx, delayedKey(queueName))
	x := pipe.LLen(ctx, deadKey(queueName))
	if _, err = pipe.Exec(ctx); err != nil {
		return
	}
	return r.Val(), d.Val(), x.Val(), nil
}

// Backoff returns the delay before retry number attempt (1-based) with ±20% jitter.
func Backoff(attempt int) time.Duration {
	base := 15 * time.Second
	d := time.Duration(float64(base) * math.Pow(2, float64(attempt-1)))
	if d > time.Hour {
		d = time.Hour
	}
	jitter := 0.8 + rand.Float64()*0.4
	return time.Duration(float64(d) * jitter)
}

var promoteScript = redis.NewScript(`
local due = redis.call('ZRANGEBYSCORE', KEYS[1], '-inf', ARGV[1], 'LIMIT', 0, 200)
for _, v in ipairs(due) do
  redis.call('ZREM', KEYS[1], v)
  redis.call('LPUSH', KEYS[2], v)
end
return #due`)

// Server consumes queues and dispatches jobs to registered handlers.
type Server struct {
	rdb         *redis.Client
	client      *Client
	log         *slog.Logger
	handlers    map[string]Handler
	concurrency map[string]int
	jobTimeout  time.Duration
	timeouts    map[string]time.Duration // per job type overrides
}

func NewServer(rdb *redis.Client, log *slog.Logger) *Server {
	return &Server{
		rdb: rdb, client: NewClient(rdb), log: log,
		handlers: map[string]Handler{}, concurrency: map[string]int{},
		jobTimeout: 2 * time.Minute, timeouts: map[string]time.Duration{},
	}
}

// Handle registers a handler for a job type.
func (s *Server) Handle(jobType string, h Handler) { s.handlers[jobType] = h }

// Timeout overrides the default 2-minute limit for one job type.
func (s *Server) Timeout(jobType string, d time.Duration) { s.timeouts[jobType] = d }

// Consume makes the server pull from queueName with n concurrent workers.
func (s *Server) Consume(queueName string, n int) { s.concurrency[queueName] = n }

// Run blocks until ctx is cancelled, then waits for in-flight jobs to finish.
func (s *Server) Run(ctx context.Context) {
	var wg sync.WaitGroup
	for q, n := range s.concurrency {
		for i := 0; i < n; i++ {
			wg.Add(1)
			go func() { defer wg.Done(); s.loop(ctx, q) }()
		}
		wg.Add(1)
		go func() { defer wg.Done(); s.promoter(ctx, q) }()
	}
	<-ctx.Done()
	s.log.Info("queue: shutting down, waiting for in-flight jobs")
	wg.Wait()
}

func (s *Server) promoter(ctx context.Context, q string) {
	t := time.NewTicker(time.Second)
	defer t.Stop()
	gauge := time.NewTicker(15 * time.Second)
	defer gauge.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			now := fmt.Sprint(time.Now().UnixMilli())
			if err := promoteScript.Run(ctx, s.rdb, []string{delayedKey(q), readyKey(q)}, now).Err(); err != nil && ctx.Err() == nil {
				s.log.Warn("queue: promote failed", "queue", q, "error", err)
			}
		case <-gauge.C:
			if r, d, x, err := s.client.Depth(ctx, q); err == nil {
				metrics.QueueDepth.WithLabelValues(q, "ready").Set(float64(r))
				metrics.QueueDepth.WithLabelValues(q, "delayed").Set(float64(d))
				metrics.QueueDepth.WithLabelValues(q, "dead").Set(float64(x))
			}
		}
	}
}

func (s *Server) loop(ctx context.Context, q string) {
	for ctx.Err() == nil {
		res, err := s.rdb.BRPop(ctx, 2*time.Second, readyKey(q)).Result()
		if err != nil {
			if errors.Is(err, redis.Nil) || ctx.Err() != nil {
				continue
			}
			s.log.Warn("queue: fetch failed", "queue", q, "error", err)
			time.Sleep(time.Second)
			continue
		}
		var job Job
		if err := json.Unmarshal([]byte(res[1]), &job); err != nil {
			s.log.Error("queue: dropping malformed job", "queue", q, "error", err)
			continue
		}
		// Jobs already fetched run to completion even during shutdown.
		s.process(context.WithoutCancel(ctx), q, job)
	}
}

func (s *Server) process(ctx context.Context, q string, job Job) {
	job.Attempt++
	log := s.log.With("job_id", job.ID, "job_type", job.Type, "attempt", job.Attempt)
	h, ok := s.handlers[job.Type]
	if !ok {
		log.Error("queue: no handler registered")
		s.deadLetter(ctx, q, job, "no handler")
		return
	}

	timeout := s.jobTimeout
	if d, ok := s.timeouts[job.Type]; ok {
		timeout = d
	}
	jctx, cancel := context.WithTimeout(ctx, timeout)
	start := time.Now()
	err := safeCall(jctx, h, job)
	cancel()

	if err == nil {
		metrics.JobsProcessed.WithLabelValues(job.Type).Inc()
		log.Debug("job done", "duration_ms", time.Since(start).Milliseconds())
		return
	}

	job.LastError = err.Error()
	if IsPermanent(err) || job.IsFinalAttempt() {
		metrics.JobsFailed.WithLabelValues(job.Type, "true").Inc()
		log.Error("job failed permanently", "error", err)
		s.deadLetter(ctx, q, job, err.Error())
		return
	}

	metrics.JobsFailed.WithLabelValues(job.Type, "false").Inc()
	delay := Backoff(job.Attempt)
	log.Warn("job failed, will retry", "error", err, "retry_in", delay.String())
	data, _ := json.Marshal(job)
	if err := s.rdb.ZAdd(ctx, delayedKey(q), redis.Z{
		Score: float64(time.Now().Add(delay).UnixMilli()), Member: data,
	}).Err(); err != nil {
		log.Error("queue: could not schedule retry", "error", err)
	}
}

func (s *Server) deadLetter(ctx context.Context, q string, job Job, reason string) {
	job.LastError = reason
	data, _ := json.Marshal(job)
	pipe := s.rdb.Pipeline()
	pipe.LPush(ctx, deadKey(q), data)
	pipe.LTrim(ctx, deadKey(q), 0, 9999)
	if _, err := pipe.Exec(ctx); err != nil {
		s.log.Error("queue: dead-letter write failed", "error", err)
	}
}

func safeCall(ctx context.Context, h Handler, job Job) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("panic: %v", r)
		}
	}()
	return h(ctx, job)
}
