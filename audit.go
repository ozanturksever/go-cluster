package cluster

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/nats-io/nats.go/jetstream"
)

// Audit manages audit logging for the platform.
type Audit struct {
	platform *Platform
	stream   jetstream.Stream
}

// AuditEntry represents an audit log entry.
type AuditEntry struct {
	Timestamp time.Time      `json:"ts"`
	NodeID    string         `json:"node"`
	Category  string         `json:"category"`
	Action    string         `json:"action"`
	Data      map[string]any `json:"data,omitempty"`
	TraceID   string         `json:"trace_id,omitempty"`
}

// AuditFilter defines criteria for querying audit logs.
type AuditFilter struct {
	Since    time.Time
	Until    time.Time
	Category string
	Action   string
	NodeID   string
}

// NewAudit creates a new audit logger.
func NewAudit(p *Platform) *Audit {
	return &Audit{
		platform: p,
	}
}

// Start initializes the audit stream.
func (a *Audit) Start(ctx context.Context) error {
	streamName := fmt.Sprintf("%s_audit", a.platform.name)
	subject := fmt.Sprintf("%s._.audit.>", a.platform.name)

	stream, err := a.platform.js.CreateOrUpdateStream(ctx, jetstream.StreamConfig{
		Name:        streamName,
		Description: fmt.Sprintf("Audit log for platform %s", a.platform.name),
		Subjects:    []string{subject},
		Retention:   jetstream.LimitsPolicy,
		MaxAge:      7 * 24 * time.Hour, // 7 days retention
		Storage:     jetstream.FileStorage,
		Replicas:    1,
	})
	if err != nil {
		return fmt.Errorf("failed to create audit stream: %w", err)
	}

	a.stream = stream
	return nil
}

// Log writes an audit entry.
func (a *Audit) Log(ctx context.Context, entry AuditEntry) error {
	if a.platform == nil || a.platform.nc == nil {
		return nil
	}

	entry.Timestamp = time.Now()
	entry.NodeID = a.platform.nodeID

	data, err := json.Marshal(entry)
	if err != nil {
		return err
	}

	subject := fmt.Sprintf("%s._.audit.%s.%s", a.platform.name, entry.Category, entry.Action)
	return a.platform.nc.Publish(subject, data)
}

// Query retrieves audit entries matching the filter.
func (a *Audit) Query(ctx context.Context, filter AuditFilter) ([]AuditEntry, error) {
	if a.stream == nil {
		return nil, fmt.Errorf("audit stream not initialized")
	}

	// Build subject filter
	category := "*"
	if filter.Category != "" {
		category = filter.Category
	}
	action := "*"
	if filter.Action != "" {
		action = filter.Action
	}
	subject := fmt.Sprintf("%s._.audit.%s.%s", a.platform.name, category, action)

	// Create consumer for query
	consumerName := fmt.Sprintf("audit-query-%d", time.Now().UnixNano())
	
	startTime := filter.Since
	if startTime.IsZero() {
		startTime = time.Now().Add(-1 * time.Hour)
	}

	consumer, err := a.stream.CreateOrUpdateConsumer(ctx, jetstream.ConsumerConfig{
		Name:          consumerName,
		FilterSubject: subject,
		DeliverPolicy: jetstream.DeliverByStartTimePolicy,
		OptStartTime:  &startTime,
		AckPolicy:     jetstream.AckNonePolicy,
		MaxDeliver:    1,
		MemoryStorage: true,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create consumer: %w", err)
	}
	defer a.stream.DeleteConsumer(ctx, consumerName)

	var entries []AuditEntry
	
	// Fetch messages
	msgs, err := consumer.Fetch(1000, jetstream.FetchMaxWait(2*time.Second))
	if err != nil {
		return entries, nil // Return what we have
	}

	for msg := range msgs.Messages() {
		var entry AuditEntry
		if err := json.Unmarshal(msg.Data(), &entry); err != nil {
			continue
		}

		// Apply filters
		if !filter.Until.IsZero() && entry.Timestamp.After(filter.Until) {
			continue
		}
		if filter.NodeID != "" && entry.NodeID != filter.NodeID {
			continue
		}

		entries = append(entries, entry)
	}

	return entries, nil
}

// Stream returns a channel of audit entries as they occur.
func (a *Audit) Stream(ctx context.Context, filter AuditFilter) (<-chan AuditEntry, error) {
	if a.stream == nil {
		return nil, fmt.Errorf("audit stream not initialized")
	}

	// Build subject filter
	category := "*"
	if filter.Category != "" {
		category = filter.Category
	}
	action := "*"
	if filter.Action != "" {
		action = filter.Action
	}
	subject := fmt.Sprintf("%s._.audit.%s.%s", a.platform.name, category, action)

	// Create ephemeral consumer
	consumerName := fmt.Sprintf("audit-stream-%d", time.Now().UnixNano())
	consumer, err := a.stream.CreateOrUpdateConsumer(ctx, jetstream.ConsumerConfig{
		Name:          consumerName,
		FilterSubject: subject,
		DeliverPolicy: jetstream.DeliverNewPolicy,
		AckPolicy:     jetstream.AckNonePolicy,
		MemoryStorage: true,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create consumer: %w", err)
	}

	ch := make(chan AuditEntry, 64)
	go func() {
		defer close(ch)
		defer a.stream.DeleteConsumer(context.Background(), consumerName)

		msgs, err := consumer.Messages()
		if err != nil {
			return
		}
		defer msgs.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			default:
				msg, err := msgs.Next()
				if err != nil {
					return
				}

				var entry AuditEntry
				if err := json.Unmarshal(msg.Data(), &entry); err != nil {
					continue
				}

				// Apply filters
				if filter.NodeID != "" && entry.NodeID != filter.NodeID {
					continue
				}

				select {
				case ch <- entry:
				case <-ctx.Done():
					return
				}
			}
		}
	}()

	return ch, nil
}
