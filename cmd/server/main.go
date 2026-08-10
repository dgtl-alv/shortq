package main

import (
	"log"
	"net/http"
	"os"

	"shortq/internal/auth"
	"shortq/internal/config"
	"shortq/internal/db"
	"shortq/internal/handlers"
	"shortq/internal/store"
)

func main() {
	cfg := config.Load()
	database, err := db.Open(cfg.MySQLDSN)
	if err != nil {
		log.Fatal(err)
	}
	if err := db.Migrate(database); err != nil {
		log.Fatal(err)
	}
	st := store.New(database)
	pass, err := auth.HashPassword(cfg.SuperPassword)
	if err != nil {
		log.Fatal(err)
	}
	if err := st.EnsureSuperadmin(cfg.SuperEmail, pass); err != nil {
		log.Fatal(err)
	}
	h := handlers.New(cfg, st, os.DirFS("web"))
	log.Printf("shortq listening on %s", cfg.Addr)
	log.Fatal(http.ListenAndServe(cfg.Addr, h.Routes()))
}
