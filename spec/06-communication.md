# Inter-Node Communication

## RPC

```go
rpc := app.RPC()

rpc.Handle("getStatus", func(ctx context.Context, req []byte) ([]byte, error) {
    return json.Marshal(myStatus)
})

resp, err := rpc.Call(ctx, "node-2", "getStatus", nil)
resp, err := rpc.CallLeader(ctx, "runMigration", payload)

responses := rpc.Broadcast(ctx, "clearCache", nil)
for _, r := range responses {
    fmt.Println(r.NodeID, r.Data, r.Error)
}
```

---

## Events

```go
events := app.Events()

events.Publish(ctx, "cache.invalidated", payload)

sub := events.Subscribe("config.>")
for msg := range sub.C {
    fmt.Println(msg.Subject, msg.Data)
}
sub.Unsubscribe()

// Durable subscription (survives restarts)
sub := events.SubscribeDurable("orders.created", "order-processor")
```

---

## Cross-App Communication

```go
convex := platform.API("convex")
resp, err := convex.Call(ctx, "query", QueryRequest{Table: "users"})

cache := platform.API("cache")
data, err := cache.Call(ctx, "get", GetRequest{Key: "user:123"},
    cluster.ForKey("user:123"),
)
```

---

## Platform-Wide Events

```go
platform.Events().Publish(ctx, "user.created", userData)

platform.Events().Subscribe("user.*", func(e Event) {
    log.Printf("Event from %s: %s", e.SourceApp, e.Type)
})
```

---

## Routing Rules

| Target Mode | Routing |
|-------------|---------|
| Singleton | → Active instance |
| Spread/Pool | → Any healthy instance (load balanced) |
| Ring | → Instance owning the key (if key provided) |
| Ring (no key) | → Any instance |
