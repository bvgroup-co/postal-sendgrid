package main

import (
	"log"
	"net/http"

	"github.com/bvgroup-co/postal-sendgrid/internal/config"
	"github.com/bvgroup-co/postal-sendgrid/internal/domain"
	"github.com/bvgroup-co/postal-sendgrid/internal/handler"
	"github.com/bvgroup-co/postal-sendgrid/internal/postal"
	"github.com/bvgroup-co/postal-sendgrid/internal/storage"
	"github.com/bvgroup-co/postal-sendgrid/internal/webhook"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("configuration error: %v", err)
	}

	store, err := storage.Open(cfg.DatabasePath)
	if err != nil {
		log.Fatalf("storage error: %v", err)
	}
	defer store.Close()

	httpClient := &http.Client{Timeout: cfg.HTTPTimeout}
	domainService := domain.NewService(store, cfg.PostalCNAMEValue, cfg.DNSCheckEnabled)
	postalClient := postal.NewClient(cfg.PostalBaseURL, cfg.PostalAPIKey, httpClient)
	forwarder := webhook.NewForwarder(store, cfg.PlunkWebhookBaseURL, httpClient, cfg.ForwardAttempts, cfg.ForwardBackoff, cfg.WebhookSigningEnabled, cfg.WebhookSigningPrivateKey)
	router := handler.NewRouter(cfg.AuthToken, cfg.MailMaxBytes, cfg.WebhookMaxBytes, domainService, postalClient, forwarder, store)

	log.Printf("postal SendGrid service listening on %s", cfg.ListenAddr)
	if err := http.ListenAndServe(cfg.ListenAddr, router); err != nil {
		log.Fatalf("server error: %v", err)
	}
}
