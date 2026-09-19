package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
	_ "time/tzdata" // Embed IANA zones for the minimal production image.

	"shortq/internal/auth"
	"shortq/internal/clickqueue"
	"shortq/internal/config"
	"shortq/internal/db"
	"shortq/internal/handlers"
	"shortq/internal/redirectcache"
	"shortq/internal/store"
)

func main() {
	cfg := config.Load()
	command, err := commandFromArgs(os.Args[1:])
	if err != nil {
		log.Fatal(err)
	}
	if command == "analytics-worker" {
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		if err := runAnalyticsWorker(ctx, cfg); err != nil {
			log.Fatal(err)
		}
		return
	}
	if err := runServer(cfg); err != nil {
		log.Fatal(err)
	}
}

func commandFromArgs(args []string) (string, error) {
	if len(args) == 0 {
		return "server", nil
	}
	if len(args) == 1 && args[0] == "analytics-worker" {
		return args[0], nil
	}
	return "", fmt.Errorf("usage: shortq [analytics-worker]")
}

func runAnalyticsWorker(ctx context.Context, cfg config.Config) error {
	if cfg.DatabaseURL == "" {
		return errors.New("DATABASE_URL is required")
	}
	database, err := db.Open(cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer database.Close()
	if err := db.Migrate(database); err != nil {
		return err
	}
	metrics := &clickqueue.WorkerMetrics{}
	worker, err := clickqueue.NewRabbitMQWorker(cfg.RabbitMQURL, store.New(database), metrics)
	if err != nil {
		return err
	}
	log.Printf("analytics_worker event=started queue=%s batch_size=%d flush_interval_ms=%d prefetch=%d", clickqueue.QueueName, clickqueue.DefaultWorkerBatchSize, clickqueue.DefaultWorkerFlushInterval.Milliseconds(), clickqueue.DefaultWorkerPrefetch)
	defer func() { log.Printf("analytics_worker event=stopped metrics=%v", metrics.Snapshot()) }()
	return worker.Run(ctx)
}

func runServer(cfg config.Config) error {
	if err := config.Validate(cfg); err != nil {
		return fmt.Errorf("invalid security configuration: %w", err)
	}
	database, err := db.Open(cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer database.Close()
	if err := db.Migrate(database); err != nil {
		return err
	}
	st := store.New(database)
	if err := st.PurgeOldClicks(); err != nil {
		log.Printf("click retention cleanup: %v", err)
	}
	if err := st.PurgeOldAuditEvents(); err != nil {
		log.Printf("audit retention cleanup: %v", err)
	}
	go func() {
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		for range ticker.C {
			if err := st.PurgeOldClicks(); err != nil {
				log.Printf("click retention cleanup: %v", err)
			}
		}
	}()
	go func() {
		ticker := time.NewTicker(24 * time.Hour)
		defer ticker.Stop()
		for range ticker.C {
			if err := st.PurgeOldAuditEvents(); err != nil {
				log.Printf("audit retention cleanup: %v", err)
			}
		}
	}()
	alvaTenant, err := st.EnsureTenant("ALVA", "alva")
	if err != nil {
		return err
	}
	pass, err := auth.HashPassword(cfg.SuperPassword)
	if err != nil {
		return err
	}
	if err := st.EnsureSuperadmin(cfg.SuperEmail, pass); err != nil {
		return err
	}
	if err := st.AssignUserTenant(cfg.SuperEmail, alvaTenant.ID); err != nil {
		return err
	}
	var resolver *redirectcache.Resolver
	var cacheMetrics *redirectcache.Metrics
	if cfg.RedirectCacheEnabled {
		redisCache, err := redirectcache.NewRedis(cfg.RedisURL, cfg.RedirectCacheTimeout)
		if err != nil {
			log.Printf("redirect_cache event=configuration_error: %v", err)
		} else {
			defer redisCache.Close()
			cacheMetrics = &redirectcache.Metrics{}
			resolver = redirectcache.NewResolver(redisCache, cacheMetrics)
			if err := redisCache.Ping(context.Background()); err != nil {
				log.Printf("redirect_cache event=startup_unavailable fallback=postgresql: %v", err)
			}
		}
	}
	var clickPublisher clickqueue.Publisher
	var clickMetrics *clickqueue.Metrics
	if cfg.ClickQueueEnabled {
		clickMetrics = &clickqueue.Metrics{}
		publisher, err := clickqueue.NewRabbitMQPublisher(cfg.RabbitMQURL, cfg.ClickQueueConfirmTimeout, clickMetrics)
		if err != nil {
			log.Printf("click_queue event=configuration_error fallback=postgresql: %v", err)
		} else {
			defer publisher.Close()
			clickPublisher = publisher
		}
	}
	h := handlers.NewWithInfrastructure(cfg, st, os.DirFS("web"), resolver, cacheMetrics, clickPublisher, clickMetrics)
	log.Printf("shortq listening on %s", cfg.Addr)
	server := &http.Server{
		Addr:              cfg.Addr,
		Handler:           h.Routes(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	return server.ListenAndServe()
}
