package cluster

import (
	"context"
	"fmt"
	"sync"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

// Events provides pub/sub event communication for an app.
type Events struct {
	app *App

	subs   []*nats.Subscription
	subsMu sync.Mutex
}

// Event represents a published event.
type Event struct {
	Subject   string
	Data      []byte
	SourceApp string
	SourceNode string
}

// Subscription represents a subscription to events.
type Subscription struct {
	sub *nats.Subscription
	ch  chan *Event
}

// NewEvents creates a new Events instance for the app.
func NewEvents(app *App) (*Events, error) {
	return &Events{
		app:  app,
		subs: make([]*nats.Subscription, 0),
	}, nil
}

// Publish publishes an event to the app's event stream.
func (e *Events) Publish(ctx context.Context, subject string, data []byte) error {
	fullSubject := fmt.Sprintf("%s.%s.events.%s", e.app.platform.name, e.app.name, subject)

	msg := &nats.Msg{
		Subject: fullSubject,
		Data:    data,
		Header:  nats.Header{},
	}
	msg.Header.Set("X-App", e.app.name)
	msg.Header.Set("X-Node", e.app.platform.nodeID)

	return e.app.platform.nc.PublishMsg(msg)
}

// Subscribe subscribes to events matching the pattern.
func (e *Events) Subscribe(pattern string) *Subscription {
	fullPattern := fmt.Sprintf("%s.%s.events.%s", e.app.platform.name, e.app.name, pattern)

	ch := make(chan *Event, 64)
	sub, err := e.app.platform.nc.Subscribe(fullPattern, func(msg *nats.Msg) {
		event := &Event{
			Subject:    msg.Subject,
			Data:       msg.Data,
			SourceApp:  msg.Header.Get("X-App"),
			SourceNode: msg.Header.Get("X-Node"),
		}
		select {
		case ch <- event:
		default:
			// Drop if channel is full
		}
	})
	if err != nil {
		close(ch)
		return &Subscription{ch: ch}
	}

	e.subsMu.Lock()
	e.subs = append(e.subs, sub)
	e.subsMu.Unlock()

	return &Subscription{
		sub: sub,
		ch:  ch,
	}
}

// SubscribeDurable subscribes to events with a durable consumer.
func (e *Events) SubscribeDurable(subject, consumerName string) (*DurableSubscription, error) {
	streamName := fmt.Sprintf("%s_%s_events", e.app.platform.name, e.app.name)
	filterSubject := fmt.Sprintf("%s.%s.events.%s", e.app.platform.name, e.app.name, subject)

	// Create or get the events stream
	stream, err := e.app.platform.js.CreateOrUpdateStream(context.Background(), jetstream.StreamConfig{
		Name:        streamName,
		Description: fmt.Sprintf("Events for %s/%s", e.app.platform.name, e.app.name),
		Subjects:    []string{fmt.Sprintf("%s.%s.events.>", e.app.platform.name, e.app.name)},
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

	return &DurableSubscription{
		events:   e,
		consumer: consumer,
	}, nil
}

// C returns the channel for receiving events.
func (s *Subscription) C() <-chan *Event {
	return s.ch
}

// Unsubscribe stops the subscription.
func (s *Subscription) Unsubscribe() {
	if s.sub != nil {
		s.sub.Unsubscribe()
	}
	close(s.ch)
}

// DurableSubscription represents a durable subscription to events.
type DurableSubscription struct {
	events   *Events
	consumer jetstream.Consumer
	msgs     jetstream.MessagesContext
}

// Messages returns a channel of events.
func (d *DurableSubscription) Messages(ctx context.Context) (<-chan *Event, error) {
	msgs, err := d.consumer.Messages()
	if err != nil {
		return nil, err
	}
	d.msgs = msgs

	ch := make(chan *Event, 64)
	go func() {
		defer close(ch)
		for {
			msg, err := msgs.Next()
			if err != nil {
				return
			}

			event := &Event{
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
func (d *DurableSubscription) Stop() {
	if d.msgs != nil {
		d.msgs.Stop()
	}
}

// Stop stops all subscriptions.
func (e *Events) Stop() {
	e.subsMu.Lock()
	defer e.subsMu.Unlock()

	for _, sub := range e.subs {
		sub.Unsubscribe()
	}
	e.subs = nil
}
