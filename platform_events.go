package cluster

import (
	"context"
	"fmt"
	"sync"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

// PlatformEvents provides platform-wide pub/sub event communication.
type PlatformEvents struct {
	platform *Platform

	subs   []*nats.Subscription
	subsMu sync.Mutex
}

// PlatformEvent represents a platform-wide event.
type PlatformEvent struct {
	Subject    string
	Data       []byte
	SourceApp  string
	SourceNode string
}

// PlatformEventHandler is a function that handles platform events.
type PlatformEventHandler func(PlatformEvent)

// PlatformSubscription represents a subscription to platform events.
type PlatformSubscription struct {
	sub *nats.Subscription
	ch  chan *PlatformEvent
}

// NewPlatformEvents creates a new platform events manager.
func NewPlatformEvents(p *Platform) *PlatformEvents {
	return &PlatformEvents{
		platform: p,
		subs:     make([]*nats.Subscription, 0),
	}
}

// Publish publishes an event to the platform-wide event stream.
func (e *PlatformEvents) Publish(ctx context.Context, subject string, data []byte) error {
	return e.PublishFrom(ctx, "", subject, data)
}

// PublishFrom publishes an event with a specific source app.
func (e *PlatformEvents) PublishFrom(ctx context.Context, sourceApp, subject string, data []byte) error {
	fullSubject := fmt.Sprintf("%s.events.%s", e.platform.name, subject)

	msg := &nats.Msg{
		Subject: fullSubject,
		Data:    data,
		Header:  nats.Header{},
	}
	msg.Header.Set("X-App", sourceApp)
	msg.Header.Set("X-Node", e.platform.nodeID)

	return e.platform.nc.PublishMsg(msg)
}

// Subscribe subscribes to events matching the pattern.
func (e *PlatformEvents) Subscribe(pattern string, handler PlatformEventHandler) (*PlatformSubscription, error) {
	fullPattern := fmt.Sprintf("%s.events.%s", e.platform.name, pattern)

	ch := make(chan *PlatformEvent, 64)
	sub, err := e.platform.nc.Subscribe(fullPattern, func(msg *nats.Msg) {
		event := PlatformEvent{
			Subject:    msg.Subject,
			Data:       msg.Data,
			SourceApp:  msg.Header.Get("X-App"),
			SourceNode: msg.Header.Get("X-Node"),
		}

		// Call handler if provided
		if handler != nil {
			go handler(event)
		}

		// Also send to channel
		select {
		case ch <- &event:
		default:
			// Drop if channel is full
		}
	})
	if err != nil {
		close(ch)
		return nil, err
	}

	e.subsMu.Lock()
	e.subs = append(e.subs, sub)
	e.subsMu.Unlock()

	return &PlatformSubscription{
		sub: sub,
		ch:  ch,
	}, nil
}

// SubscribeFromApp subscribes to events from a specific app.
func (e *PlatformEvents) SubscribeFromApp(appName, pattern string, handler PlatformEventHandler) (*PlatformSubscription, error) {
	wrappedHandler := func(event PlatformEvent) {
		if event.SourceApp == appName {
			handler(event)
		}
	}
	return e.Subscribe(pattern, wrappedHandler)
}

// SubscribeDurable subscribes to events with a durable consumer.
func (e *PlatformEvents) SubscribeDurable(subject, consumerName string) (*PlatformDurableSubscription, error) {
	streamName := fmt.Sprintf("%s_events", e.platform.name)
	filterSubject := fmt.Sprintf("%s.events.%s", e.platform.name, subject)

	// Create or get the platform events stream
	stream, err := e.platform.js.CreateOrUpdateStream(context.Background(), jetstream.StreamConfig{
		Name:        streamName,
		Description: fmt.Sprintf("Platform-wide events for %s", e.platform.name),
		Subjects:    []string{fmt.Sprintf("%s.events.>", e.platform.name)},
		Retention:   jetstream.LimitsPolicy,
		Storage:     jetstream.FileStorage,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create events stream: %w", err)
	}

	// Create durable consumer
	consumer, err := stream.CreateOrUpdateConsumer(context.Background(), jetstream.ConsumerConfig{
		Durable:       consumerName,
		FilterSubject: filterSubject,
		AckPolicy:     jetstream.AckExplicitPolicy,
		DeliverPolicy: jetstream.DeliverAllPolicy,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create consumer: %w", err)
	}

	return &PlatformDurableSubscription{
		events:   e,
		consumer: consumer,
	}, nil
}

// C returns the channel for receiving events.
func (s *PlatformSubscription) C() <-chan *PlatformEvent {
	return s.ch
}

// Unsubscribe stops the subscription.
func (s *PlatformSubscription) Unsubscribe() {
	if s.sub != nil {
		s.sub.Unsubscribe()
	}
	close(s.ch)
}

// PlatformDurableSubscription represents a durable subscription to platform events.
type PlatformDurableSubscription struct {
	events   *PlatformEvents
	consumer jetstream.Consumer
	msgs     jetstream.MessagesContext
}

// Messages returns a channel of events.
func (d *PlatformDurableSubscription) Messages(ctx context.Context) (<-chan *PlatformEvent, error) {
	msgs, err := d.consumer.Messages()
	if err != nil {
		return nil, err
	}
	d.msgs = msgs

	ch := make(chan *PlatformEvent, 64)
	go func() {
		defer close(ch)
		for {
			msg, err := msgs.Next()
			if err != nil {
				return
			}

			event := &PlatformEvent{
				Subject:    msg.Subject(),
				Data:       msg.Data(),
				SourceApp:  msg.Headers().Get("X-App"),
				SourceNode: msg.Headers().Get("X-Node"),
			}

			select {
			case ch <- event:
				msg.Ack()
			case <-ctx.Done():
				return
			}
		}
	}()

	return ch, nil
}

// Stop stops the durable subscription.
func (d *PlatformDurableSubscription) Stop() {
	if d.msgs != nil {
		d.msgs.Stop()
	}
}

// Stop stops all platform event subscriptions.
func (e *PlatformEvents) Stop() {
	e.subsMu.Lock()
	defer e.subsMu.Unlock()

	for _, sub := range e.subs {
		sub.Unsubscribe()
	}
	e.subs = nil
}
