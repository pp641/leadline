// Package app wires configuration, infrastructure and domain services
// together. Both the API server and the worker build on it.
package app

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/redis/go-redis/v9"

	"leadline/config"
	"leadline/internal/agency"
	"leadline/internal/analytics"
	"leadline/internal/apikeys"
	"leadline/internal/audit"
	"leadline/internal/auth"
	"leadline/internal/campaigns"
	"leadline/internal/catalog"
	"leadline/internal/contacts"
	"leadline/internal/crm"
	"leadline/internal/dashboards"
	"leadline/internal/deals"
	"leadline/internal/email"
	"leadline/internal/icp"
	"leadline/internal/imports"
	"leadline/internal/inbox"
	"leadline/internal/integrations"
	"leadline/internal/leads"
	"leadline/internal/mailboxes"
	"leadline/internal/onboarding"
	"leadline/internal/organization"
	"leadline/internal/personalization"
	"leadline/internal/scoring"
	"leadline/internal/sources"
	"leadline/internal/suppression"
	"leadline/internal/users"
	"leadline/internal/vault"
	"leadline/internal/verification"
	"leadline/internal/webhooks"
	"leadline/internal/workflows"
	"leadline/migrations"
	"leadline/pkg/database"
	"leadline/pkg/httpx"
	"leadline/pkg/queue"
	"leadline/pkg/redisx"
	"leadline/pkg/storage"
)

type App struct {
	Cfg   *config.Config
	Log   *slog.Logger
	Pool  *pgxpool.Pool
	Redis *redis.Client
	Queue *queue.Client

	Audit        *audit.Log
	Tokens       *auth.TokenIssuer
	AuthMW       *auth.Middleware
	Auth         *auth.Service
	Orgs         *organization.Service
	OrgRepo      *organization.Repository
	Mailboxes    *mailboxes.Service
	Suppression  *suppression.Service
	Scoring      *scoring.Service
	Verification *verification.Service
	Contacts     *contacts.Service
	Leads        *leads.Service
	Imports      *imports.Service
	Unsub        *email.Unsubscriber
	Events       *email.Events
	Campaigns    *campaigns.Service
	Analytics    *analytics.Service
	Provider     email.EmailProvider

	Vault           *vault.Vault
	LLM             integrations.LLMProvider
	Integrations    *integrations.Registry
	APIKeys         *apikeys.Service
	Agency          *agency.Service
	CRM             *crm.Service
	CRMSync         *crm.Syncer
	ICPs            *icp.Service
	Catalog         *catalog.Service
	Sources         *sources.Service
	Dashboards      *dashboards.Service
	Onboarding      *onboarding.Service
	Deals           *deals.Service
	Workflows       *workflows.Service
	WorkflowEngine  *workflows.Engine
	catalogDB       *catalog.PGSource
	Personalization *personalization.Service
	Inbox           *inbox.Service
	Webhooks        *webhooks.Service
	WebhookDispatch *webhooks.Dispatcher
}

// New connects to PostgreSQL and Redis, applies migrations and builds services.
func New(ctx context.Context, cfg *config.Config, log *slog.Logger) (*App, error) {
	pool, err := database.Connect(ctx, cfg.DatabaseURL, cfg.DatabaseMaxConns)
	if err != nil {
		return nil, err
	}
	if err := database.Migrate(ctx, pool, migrations.FS, log); err != nil {
		pool.Close()
		return nil, err
	}
	rdb, err := redisx.Connect(ctx, cfg.RedisURL)
	if err != nil {
		pool.Close()
		return nil, err
	}
	store, err := storage.NewLocal(cfg.StorageDir)
	if err != nil {
		return nil, err
	}
	provider, err := email.NewProvider(cfg.EmailProvider, email.SMTPConfig{
		Host: cfg.SMTPHost, Port: cfg.SMTPPort, Username: cfg.SMTPUsername, Password: cfg.SMTPPassword, TLS: cfg.SMTPTLS,
	}, log)
	if err != nil {
		return nil, err
	}
	httpx.TrustProxyHeaders = cfg.TrustProxy
	v, err := vault.New(cfg.EncryptionKey)
	if err != nil {
		return nil, err
	}
	llm, err := integrations.NewLLM(cfg.LLMProvider, cfg.LLMAPIKey, cfg.LLMModel)
	if err != nil {
		return nil, err
	}

	a := &App{Cfg: cfg, Log: log, Pool: pool, Redis: rdb, Queue: queue.NewClient(rdb), Provider: provider, Vault: v, LLM: llm}
	a.Audit = audit.New(pool)
	userRepo := users.NewRepository(pool)
	a.OrgRepo = organization.NewRepository(pool)
	a.Tokens = auth.NewTokenIssuer(cfg.JWTSecret, cfg.AccessTokenTTL)
	a.APIKeys = apikeys.NewService(pool, a.Audit)
	a.AuthMW = auth.NewMiddleware(a.Tokens, a.OrgRepo, a.APIKeys)
	a.Auth = auth.NewService(userRepo, auth.NewSessionRepository(pool), a.Tokens, a.Audit, cfg.RefreshTokenTTL)
	a.Orgs = organization.NewService(pool, a.OrgRepo, userRepo, a.Audit)
	a.Mailboxes = mailboxes.NewService(pool, a.Audit, v)
	a.Integrations = integrations.NewRegistry(pool, v, a.Audit)
	sources.RegisterTesters(a.Integrations)
	a.Suppression = suppression.NewService(pool, a.Audit)
	a.Scoring = scoring.NewService(pool, a.Queue, a.Audit)
	verifier := verification.NewBasic(cfg.VerifierMXLookup)
	verifier.AllowTestDomains = cfg.VerifierAllowTestDomain
	a.Verification = verification.NewService(pool, verifier, a.Queue)
	a.Contacts = contacts.NewService(pool, a.Queue)
	a.Leads = leads.NewService(pool, a.Contacts, a.Scoring, a.Queue, a.Audit)
	a.Imports = imports.NewService(pool, store, a.Queue, a.Audit)
	a.Unsub = email.NewUnsubscriber(pool, cfg.SessionSecret, cfg.AppURL, a.Audit)
	a.Events = email.NewEvents(pool, cfg.WebhookSecret)
	a.Campaigns = campaigns.NewService(pool, a.Leads, a.Unsub, a.Audit)
	a.Analytics = analytics.NewService(pool)
	a.Agency = agency.NewService(pool, a.Audit)
	a.CRM = crm.NewService(pool)
	a.CRMSync = crm.NewSyncer(pool, a.Integrations, a.Queue, log)
	a.ICPs = icp.NewService(pool, a.Queue)
	a.Sources = sources.NewService(pool, a.Integrations, a.Queue, a.Audit)
	a.Dashboards = dashboards.NewService(pool, a.Audit)
	a.Onboarding = onboarding.NewService(pool, userRepo, a.Provider, onboarding.Config{
		AppURL: cfg.AppURL, FromEmail: cfg.SystemEmailFrom, FromName: cfg.SystemEmailName}, a.Audit, log)
	a.Auth.OnRegistered = a.Onboarding.SendVerificationAsync
	a.Deals = deals.NewService(pool, a.Audit)
	a.Workflows = workflows.NewService(pool, a.Audit, a.Deals)
	a.WorkflowEngine = workflows.NewEngine(a.Workflows, a.Deals, a.Queue, a.Onboarding, log)
	a.Deals.OnEvent = a.WorkflowEngine.OnDealEvent
	a.Events.OnReply = a.WorkflowEngine.OnReply
	a.Catalog = catalog.NewService(pool, nil, a.Queue, a.Audit)
	if cfg.ScraperDatabaseURL != "" {
		if a.catalogDB, err = catalog.NewPGSource(cfg.ScraperDatabaseURL); err != nil {
			return nil, err
		}
		a.Catalog.Source = a.catalogDB
	} else {
		log.Warn("SCRAPER_DATABASE_URL is not set; importing from the company database is disabled")
	}
	a.Personalization = personalization.NewService(pool, llm, a.Queue, log)
	a.Inbox = inbox.NewService(pool, rdb, a.Events, llm, v, a.Queue, log)
	a.Webhooks = webhooks.NewService(pool, v, a.Audit, !cfg.IsProduction())
	a.WebhookDispatch = webhooks.NewDispatcher(pool, v, log)
	return a, nil
}

func (a *App) Close() {
	if a.catalogDB != nil {
		a.catalogDB.Close()
	}
	a.Redis.Close()
	a.Pool.Close()
}

// Router builds the HTTP handler with all routes and middleware.
func (a *App) Router() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /health", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})
	mux.HandleFunc("GET /ready", a.ready)
	mux.Handle("GET /metrics", promhttp.Handler())

	ipLimiter := redisx.NewLimiter(a.Redis, "auth-ip", 20, time.Minute)
	emailLimiter := redisx.NewLimiter(a.Redis, "auth-email", 5, time.Minute)

	auth.NewHandler(a.Auth, a.AuthMW, ipLimiter, emailLimiter, a.Cfg.IsProduction()).Register(mux)
	organization.NewHandler(a.Orgs, a.AuthMW, a.Audit).Register(mux)
	mailboxes.NewHandler(a.Mailboxes, a.AuthMW).Register(mux)
	suppression.NewHandler(a.Suppression, a.AuthMW).Register(mux)
	scoring.NewHandler(a.Scoring, a.AuthMW).Register(mux)
	verification.NewHandler(a.Verification, a.AuthMW).Register(mux)
	contacts.NewHandler(a.Contacts, a.AuthMW).Register(mux)
	leads.NewHandler(a.Leads, a.Contacts, a.Scoring, a.Audit, a.AuthMW).Register(mux)
	imports.NewHandler(a.Imports, a.AuthMW).Register(mux)
	campaigns.NewHandler(a.Campaigns, a.AuthMW).Register(mux)
	analytics.NewHandler(a.Analytics, a.AuthMW).Register(mux)
	email.NewEventsHandler(a.Events, a.AuthMW).Register(mux)
	a.Unsub.Register(mux)
	apikeys.NewHandler(a.APIKeys, a.AuthMW).Register(mux)
	agency.NewHandler(a.Agency, a.AuthMW).Register(mux)
	crm.NewHandler(a.CRM, a.AuthMW).Register(mux)
	crm.NewSyncHandler(a.CRMSync, a.AuthMW).Register(mux)
	icp.NewHandler(a.ICPs, a.AuthMW).Register(mux)
	catalog.NewHandler(a.Catalog, a.AuthMW).Register(mux)
	sources.NewHandler(a.Sources, a.AuthMW).Register(mux)
	dashboards.NewHandler(a.Dashboards, a.AuthMW).Register(mux)
	onboarding.NewHandler(a.Onboarding, a.AuthMW).Register(mux)
	deals.NewHandler(a.Deals, a.AuthMW).Register(mux)
	workflows.NewHandler(a.Workflows, a.WorkflowEngine, a.AuthMW).Register(mux)
	personalization.NewHandler(a.Personalization, a.AuthMW).Register(mux)
	inbox.NewHandler(a.Inbox, a.AuthMW, a.Cfg.WebhookSecret).Register(mux)
	integrations.NewHandler(a.Integrations, a.AuthMW, a.LLM).Register(mux)
	webhooks.NewHandler(a.Webhooks, a.AuthMW).Register(mux)

	apiLimiter := redisx.NewLimiter(a.Redis, "api", 600, time.Minute)
	limited := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !apiLimiter.Allow(r.Context(), httpx.ClientIP(r)) {
				httpx.Error(w, r, errRateLimited)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
	return httpx.Chain(httpx.CaptureRoute(mux), httpx.WithRequestID, httpx.LogAndMeasure, httpx.Recover, httpx.SecureHeaders, limited)
}

func (a *App) ready(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	status, code := map[string]string{"database": "ok", "redis": "ok"}, http.StatusOK
	if err := a.Pool.Ping(ctx); err != nil {
		status["database"], code = "unavailable", http.StatusServiceUnavailable
	}
	if err := a.Redis.Ping(ctx).Err(); err != nil {
		status["redis"], code = "unavailable", http.StatusServiceUnavailable
	}
	httpx.JSON(w, code, status, nil)
}
