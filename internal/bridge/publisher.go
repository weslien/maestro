package bridge

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sync"

	iggcon "github.com/apache/iggy/foreign/go/contracts"
)

// LogPublisher writes event envelopes to stdout as JSON. Useful for testing.
type LogPublisher struct{}

func NewLogPublisher() *LogPublisher {
	return &LogPublisher{}
}

func (p *LogPublisher) Publish(_ context.Context, env EventEnvelope) error {
	data, err := json.Marshal(env)
	if err != nil {
		return fmt.Errorf("failed to marshal envelope: %w", err)
	}
	fmt.Println(string(data))
	return nil
}

func (p *LogPublisher) Close() error { return nil }

// FilePublisher appends event envelopes as NDJSON to a file.
type FilePublisher struct {
	mu   sync.Mutex
	file *os.File
}

func NewFilePublisher(path string) (*FilePublisher, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, fmt.Errorf("failed to open file %s: %w", path, err)
	}
	return &FilePublisher{file: f}, nil
}

func (p *FilePublisher) Publish(_ context.Context, env EventEnvelope) error {
	data, err := json.Marshal(env)
	if err != nil {
		return fmt.Errorf("failed to marshal envelope: %w", err)
	}
	data = append(data, '\n')

	p.mu.Lock()
	defer p.mu.Unlock()

	if _, err := p.file.Write(data); err != nil {
		return fmt.Errorf("failed to write to file: %w", err)
	}
	return nil
}

func (p *FilePublisher) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.file.Close()
}

// IggyPublisher sends event envelopes to an Iggy message stream.
type IggyPublisher struct {
	client   iggcon.Client
	streamID iggcon.Identifier
	topicID  iggcon.Identifier
}

// NewIggyPublisher creates a publisher that sends to the given Iggy stream and topic.
// The caller is responsible for creating the client, logging in, and ensuring the
// stream/topic exist before calling Publish.
func NewIggyPublisher(client iggcon.Client, streamName, topicName string) (*IggyPublisher, error) {
	streamID, err := iggcon.NewIdentifier(streamName)
	if err != nil {
		return nil, fmt.Errorf("failed to create stream identifier: %w", err)
	}
	topicID, err := iggcon.NewIdentifier(topicName)
	if err != nil {
		return nil, fmt.Errorf("failed to create topic identifier: %w", err)
	}
	return &IggyPublisher{
		client:   client,
		streamID: streamID,
		topicID:  topicID,
	}, nil
}

func (p *IggyPublisher) Publish(_ context.Context, env EventEnvelope) error {
	payload, err := json.Marshal(env)
	if err != nil {
		return fmt.Errorf("failed to marshal envelope: %w", err)
	}

	msg, err := iggcon.NewIggyMessage(payload)
	if err != nil {
		return fmt.Errorf("failed to create iggy message: %w", err)
	}

	partitioning, err := iggcon.EntityIdString(env.ProjectID)
	if err != nil {
		// Fall back to balanced partitioning if project ID is invalid.
		partitioning = iggcon.None()
	}

	if err := p.client.SendMessages(p.streamID, p.topicID, partitioning, []iggcon.IggyMessage{msg}); err != nil {
		return fmt.Errorf("failed to send message to iggy: %w", err)
	}
	return nil
}

func (p *IggyPublisher) Close() error {
	return p.client.Close()
}
