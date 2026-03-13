package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	iggcli "github.com/apache/iggy/foreign/go/client"
	"github.com/apache/iggy/foreign/go/client/tcp"
	iggcon "github.com/apache/iggy/foreign/go/contracts"
	ierror "github.com/apache/iggy/foreign/go/errors"

	"github.com/weslien/maestro/internal/bridge"
)

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func main() {
	var (
		port          int
		webhookSecret string
		iggyAddr      string
		iggyUser      string
		iggyPass      string
		streamName    string
		topicName     string
		partitions    uint
	)

	flag.IntVar(&port, "port", 8080, "HTTP listen port")
	flag.StringVar(&webhookSecret, "webhook-secret", "", "GitHub webhook secret")
	flag.StringVar(&iggyAddr, "iggy-addr", "", "Iggy server TCP address")
	flag.StringVar(&iggyUser, "iggy-user", "", "Iggy username")
	flag.StringVar(&iggyPass, "iggy-pass", "", "Iggy password")
	flag.StringVar(&streamName, "stream", "", "Iggy stream name")
	flag.StringVar(&topicName, "topic", "", "Iggy topic name")
	flag.UintVar(&partitions, "partitions", 3, "Number of partitions when creating topic")
	flag.Parse()

	// Env vars take precedence, flags are fallbacks, then defaults
	if webhookSecret == "" {
		webhookSecret = envOr("WEBHOOK_SECRET", "")
	}
	if iggyAddr == "" {
		iggyAddr = envOr("IGGY_ADDR", "127.0.0.1:8090")
	}
	if iggyUser == "" {
		iggyUser = envOr("IGGY_USER", "iggy")
	}
	if iggyPass == "" {
		iggyPass = envOr("IGGY_PASS", "iggy")
	}
	if streamName == "" {
		streamName = envOr("IGGY_STREAM", "github-events")
	}
	if topicName == "" {
		topicName = envOr("IGGY_TOPIC", "projects-v2")
	}
	if s := os.Getenv("IGGY_PARTITIONS"); s != "" && partitions == 3 {
		if v, err := fmt.Sscanf(s, "%d", &partitions); err == nil && v == 1 {
			// parsed from env
		}
	}

	if webhookSecret == "" {
		log.Fatal("webhook secret is required (--webhook-secret or WEBHOOK_SECRET env)")
	}

	// Connect to Iggy
	log.Printf("connecting to iggy at %s", iggyAddr)
	client, err := iggcli.NewIggyClient(
		iggcli.WithTcp(tcp.WithServerAddress(iggyAddr)),
	)
	if err != nil {
		log.Fatalf("failed to connect to iggy: %v", err)
	}

	if _, err := client.LoginUser(iggyUser, iggyPass); err != nil {
		log.Fatalf("failed to login to iggy: %v", err)
	}
	log.Println("authenticated with iggy")

	// Ensure stream and topic exist
	streamID, topicID, err := ensureStreamAndTopic(client, streamName, topicName, uint32(partitions))
	if err != nil {
		log.Fatalf("failed to setup stream/topic: %v", err)
	}
	_ = streamID
	_ = topicID

	// Create publisher
	pub, err := bridge.NewIggyPublisher(client, streamName, topicName)
	if err != nil {
		log.Fatalf("failed to create iggy publisher: %v", err)
	}

	// Build the handler
	handler := bridge.NewWebhookHandler([]byte(webhookSecret), nil, pub)

	mux := http.NewServeMux()
	mux.Handle("/webhook", handler)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprintln(w, "ok")
	})
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, r *http.Request) {
		if err := client.Ping(); err != nil {
			http.Error(w, "iggy unreachable", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
		fmt.Fprintln(w, "ok")
	})

	srv := &http.Server{
		Addr:         fmt.Sprintf(":%d", port),
		Handler:      mux,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		log.Printf("listening on :%d", port)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
		close(errCh)
	}()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	select {
	case <-sigCh:
		log.Println("shutting down...")
	case err := <-errCh:
		log.Fatalf("server error: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		log.Printf("shutdown error: %v", err)
	}
	if err := pub.Close(); err != nil {
		log.Printf("publisher close error: %v", err)
	}
}

func ensureStreamAndTopic(client iggcon.Client, streamName, topicName string, partitions uint32) (iggcon.Identifier, iggcon.Identifier, error) {
	streamID, err := iggcon.NewIdentifier(streamName)
	if err != nil {
		return iggcon.Identifier{}, iggcon.Identifier{}, fmt.Errorf("invalid stream name: %w", err)
	}
	topicID, err := iggcon.NewIdentifier(topicName)
	if err != nil {
		return iggcon.Identifier{}, iggcon.Identifier{}, fmt.Errorf("invalid topic name: %w", err)
	}

	// Create stream if it doesn't exist
	_, err = client.GetStream(streamID)
	if err != nil {
		if isNotFound(err) {
			log.Printf("creating stream %q", streamName)
			if _, err := client.CreateStream(streamName); err != nil {
				return iggcon.Identifier{}, iggcon.Identifier{}, fmt.Errorf("failed to create stream: %w", err)
			}
		} else {
			return iggcon.Identifier{}, iggcon.Identifier{}, fmt.Errorf("failed to get stream: %w", err)
		}
	}

	// Create topic if it doesn't exist
	_, err = client.GetTopic(streamID, topicID)
	if err != nil {
		if isNotFound(err) {
			log.Printf("creating topic %q with %d partitions", topicName, partitions)
			if _, err := client.CreateTopic(
				streamID,
				topicName,
				partitions,
				iggcon.CompressionAlgorithmNone,
				0,    // no expiry
				0,    // unlimited size
				nil,  // default replication
			); err != nil {
				return iggcon.Identifier{}, iggcon.Identifier{}, fmt.Errorf("failed to create topic: %w", err)
			}
		} else {
			return iggcon.Identifier{}, iggcon.Identifier{}, fmt.Errorf("failed to get topic: %w", err)
		}
	}

	log.Printf("stream=%q topic=%q ready", streamName, topicName)
	return streamID, topicID, nil
}

func isNotFound(err error) bool {
	if err == ierror.ErrStreamIdNotFound || err == ierror.ErrTopicIdNotFound {
		return true
	}
	// Iggy may return other error types for not-found
	return false
}
