package bridge

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/http"
)

// Bridge is the main webhook-to-stream bridge service.
type Bridge struct {
	handler   http.Handler
	enricher  Enricher
	publisher Publisher
	addr      string
	secret    []byte
}

// Option configures a Bridge.
type Option func(*Bridge)

// WithAddr sets the listen address (e.g. ":8080").
func WithAddr(addr string) Option {
	return func(b *Bridge) { b.addr = addr }
}

// WithWebhookSecret sets the HMAC secret for signature validation.
func WithWebhookSecret(secret string) Option {
	return func(b *Bridge) { b.secret = []byte(secret) }
}

// WithPublisher sets the event publisher.
func WithPublisher(pub Publisher) Option {
	return func(b *Bridge) { b.publisher = pub }
}

// WithEnricher sets the field value enricher.
func WithEnricher(enr Enricher) Option {
	return func(b *Bridge) { b.enricher = enr }
}

// New creates a Bridge with the given options.
func New(opts ...Option) (*Bridge, error) {
	b := &Bridge{
		addr: ":8080",
	}
	for _, opt := range opts {
		opt(b)
	}

	if len(b.secret) == 0 {
		return nil, fmt.Errorf("webhook secret is required")
	}
	if b.publisher == nil {
		b.publisher = NewLogPublisher()
	}

	b.handler = NewWebhookHandler(b.secret, b.enricher, b.publisher)
	return b, nil
}

// Run starts the HTTP server and blocks until ctx is cancelled.
// It performs graceful shutdown when the context is done.
func (b *Bridge) Run(ctx context.Context) error {
	mux := http.NewServeMux()
	mux.Handle("/webhook", b.handler)
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprintln(w, "ok")
	})

	srv := &http.Server{
		Addr:    b.addr,
		Handler: mux,
	}

	errCh := make(chan error, 1)
	go func() {
		log.Printf("bridge: listening on %s", b.addr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- fmt.Errorf("bridge server error: %w", err)
		}
		close(errCh)
	}()

	select {
	case <-ctx.Done():
		log.Println("bridge: shutting down...")
		if err := srv.Shutdown(context.Background()); err != nil {
			return fmt.Errorf("bridge shutdown error: %w", err)
		}
		if err := b.publisher.Close(); err != nil {
			return fmt.Errorf("failed to close publisher: %w", err)
		}
		return nil
	case err := <-errCh:
		return err
	}
}

// Addr returns the configured listen address.
func (b *Bridge) Addr() string {
	return b.addr
}

// ListenAddr returns the actual address the server would bind to.
// Useful for tests using port 0.
func ListenAddr(addr string) (string, error) {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return "", err
	}
	actual := ln.Addr().String()
	ln.Close()
	return actual, nil
}
