# NATS Subject Design

## Naming Convention

```
{platform}.{app}.{component}.{target}
```

**App-Isolated Resources:**
```
prod.convex.kv.users/123             # App's own KV
prod.convex.lock.migration           # App's own lock
prod.convex.events.user.created      # App's internal events
prod.convex.election.leader          # App's leader election
prod.convex.wal.frames               # App's WAL replication
```

**Platform-Wide Resources:**
```
prod._.api.convex.query              # Cross-app API call
prod._.events.user.created           # Platform-wide event
prod._.audit.convex.query            # Platform audit log
prod._.lock.global-migration         # Platform-wide lock
```

---

## NATS Resources

| Resource | Name | Scope | Purpose |
|----------|------|-------|---------|
| KV | `{platform}_{app}` | App | App's isolated KV, locks, election |
| KV | `{platform}_platform` | Platform | Platform-wide locks, config |
| Stream | `{platform}_{app}_wal` | App | App's Litestream WAL |
| Stream | `{platform}_events` | Platform | Platform-wide events |
| Stream | `{platform}_audit` | Platform | Unified audit log |
| ObjectStore | `{platform}_{app}_snapshots` | App | App's DB snapshots |

---

## App Isolation via NATS

```
Platform: prod
├── App: convex
│   ├── KV: prod_convex
│   ├── Stream: prod_convex_wal
│   └── ObjectStore: prod_convex_snapshots
├── App: cache
│   └── KV: prod_cache
├── App: api
│   └── KV: prod_api
└── Platform-Wide
    ├── KV: prod_platform
    ├── Stream: prod_events
    └── Stream: prod_audit
```

---

## Multi-Tenancy with NATS Accounts

```hcl
accounts {
  PROD {
    jetstream: enabled
    users: [
      { user: "prod-node-1", pass: "..." }
      { user: "prod-node-2", pass: "..." }
    ]
  }
  STAGING {
    jetstream: enabled
    users: [{ user: "staging-node-1", pass: "..." }]
  }
  MONITOR {
    users: [{ user: "monitor", pass: "..." }]
    imports: [
      { stream: { account: PROD, subject: "prod.*.health.>" } }
      { stream: { account: PROD, subject: "prod._.audit.>" } }
    ]
  }
}
```

---

## Authorization

```hcl
authorization {
  PROD_NODE = {
    publish: ["prod.>", "$JS.API.>"]
    subscribe: ["prod.>", "_INBOX.>"]
  }
  PROD_CONVEX_ONLY = {
    publish: ["prod.convex.>"]
    subscribe: ["prod.convex.>", "prod._.events.>", "_INBOX.>"]
  }
  MONITOR = {
    subscribe: ["prod.*.health.>", "prod._.audit.>"]
  }
}
```
