// Package workers runs background processing: queue consumers for imports,
// verification, scoring and email sending, plus the campaign scheduler.
// Any number of worker replicas can run; the scheduler is serialized by a
// Redis lock and every job handler is idempotent.
package workers

import (
	"context"
	"sync"
	"time"

	"github.com/google/uuid"

	"leadline/internal/app"
	"leadline/internal/campaigns"
	"leadline/internal/email"
	"leadline/internal/jobs"
	"leadline/pkg/queue"
)

// Run blocks until ctx is cancelled and in-flight jobs have finished.
func Run(ctx context.Context, a *app.App) {
	srv := queue.NewServer(a.Redis, a.Log)
	srv.Handle(jobs.TypeProcessImport, a.Imports.HandleProcess)
	srv.Handle(jobs.TypeVerifyContacts, a.Verification.HandleVerifyContacts)
	srv.Handle(jobs.TypeScoreLeads, a.Scoring.HandleScoreLeads)
	srv.Handle(jobs.TypeRescoreOrg, a.Scoring.HandleRescoreOrg)
	delivery := campaigns.NewDelivery(a.Pool, a.Provider, a.Unsub, a.Log, a.Vault)
	delivery.OrgSMTP = func(ctx context.Context, orgID uuid.UUID) (email.SMTPConfig, bool, error) {
		cfg, _, ok, err := a.Integrations.OrgSMTP(ctx, orgID)
		return cfg, ok, err
	}
	srv.Handle(jobs.TypeSendEmail, delivery.Handle)
	srv.Handle(jobs.TypePersonalize, a.Personalization.HandleGenerate)
	srv.Handle(jobs.TypeInboxSync, a.Inbox.HandleSync)
	srv.Handle(jobs.TypeCRMSync, a.CRMSync.HandleSync)
	srv.Handle(jobs.TypeWorkflowEvent, a.WorkflowEngine.HandleEvent)

	n := max(1, a.Cfg.WorkerConcurrency)
	srv.Consume(jobs.QueueDefault, n)
	srv.Consume(jobs.QueueEmail, n)

	sched := campaigns.NewScheduler(a.Pool, a.Redis, a.Queue, a.Unsub, a.Audit, a.Log, a.Cfg.SchedulerInterval)

	var wg sync.WaitGroup
	wg.Add(6)
	go func() { defer wg.Done(); srv.Run(ctx) }()
	go func() { defer wg.Done(); sched.Run(ctx) }()
	go func() { defer wg.Done(); a.WebhookDispatch.Run(ctx) }()                 // outbound webhooks
	go func() { defer wg.Done(); a.Inbox.Poll(ctx, a.Cfg.InboxPollInterval) }() // IMAP reply sync
	go func() { defer wg.Done(); a.CRMSync.AutoSync(ctx, 5*time.Minute) }()     // HubSpot auto-sync
	go func() { defer wg.Done(); a.WorkflowEngine.Scan(ctx, 10*time.Minute) }() // stale / inactive deal workflows
	a.Log.Info("worker started", "concurrency", n, "email_provider", a.Provider.Name())
	wg.Wait()
	a.Log.Info("worker stopped")
}
