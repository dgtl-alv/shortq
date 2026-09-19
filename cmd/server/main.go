package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"time"
	_ "time/tzdata" // Embed IANA zones for the minimal production image.

	"shortq/internal/auth"
	"shortq/internal/config"
	"shortq/internal/db"
	"shortq/internal/handlers"
	"shortq/internal/redirectcache"
	"shortq/internal/store"
)

func main() {
	cfg := config.Load()
	if err := config.Validate(cfg); err != nil {
		log.Fatalf("invalid security configuration: %v", err)
	}
	database, err := db.Open(cfg.DatabaseURL)
	if err != nil {
		log.Fatal(err)
	}
	if err := db.Migrate(database); err != nil {
		log.Fatal(err)
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
		log.Fatal(err)
	}
	pass, err := auth.HashPassword(cfg.SuperPassword)
	if err != nil {
		log.Fatal(err)
	}
	if err := st.EnsureSuperadmin(cfg.SuperEmail, pass); err != nil {
		log.Fatal(err)
	}
	if err := st.AssignUserTenant(cfg.SuperEmail, alvaTenant.ID); err != nil {
		log.Fatal(err)
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
	h := handlers.NewWithRedirectCache(cfg, st, os.DirFS("web"), resolver, cacheMetrics)
	log.Printf("shortq listening on %s", cfg.Addr)
	server := &http.Server{
		Addr:              cfg.Addr,
		Handler:           h.Routes(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	log.Fatal(server.ListenAndServe())
}
