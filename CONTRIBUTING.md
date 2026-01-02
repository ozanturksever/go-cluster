# Contributing to go-cluster

Thank you for your interest in contributing to go-cluster! This document provides guidelines and instructions for contributing.

## Code of Conduct

Please be respectful and constructive in all interactions. We welcome contributors of all experience levels.

## Getting Started

### Prerequisites

- Go 1.21 or later
- NATS Server 2.10+ with JetStream
- Make (optional, for running scripts)

### Setting Up Development Environment

1. Fork the repository
2. Clone your fork:
   ```bash
   git clone https://github.com/YOUR_USERNAME/go-cluster.git
   cd go-cluster
   ```

3. Start NATS server:
   ```bash
   nats-server -js
   ```

4. Run tests:
   ```bash
   go test ./...
   ```

## How to Contribute

### Reporting Bugs

1. Check existing issues to avoid duplicates
2. Create a new issue with:
   - Clear title and description
   - Steps to reproduce
   - Expected vs actual behavior
   - Go version, OS, NATS version

### Suggesting Features

1. Check existing issues and discussions
2. Create a new issue with:
   - Clear description of the feature
   - Use case and motivation
   - Proposed API (if applicable)

### Submitting Pull Requests

1. Create a feature branch:
   ```bash
   git checkout -b feature/my-feature
   ```

2. Make your changes following our coding standards

3. Write or update tests

4. Run tests and lints:
   ```bash
   go test ./...
   go vet ./...
   ```

5. Commit with clear messages:
   ```bash
   git commit -m "Add feature: description"
   ```

6. Push and create a pull request

## Coding Standards

### Code Style

- Follow standard Go formatting (`gofmt`)
- Use meaningful variable and function names
- Keep functions focused and small
- Add comments for exported types and functions

### Error Handling

- Always handle errors explicitly
- Wrap errors with context using `fmt.Errorf("...: %w", err)`
- Use the defined error variables where appropriate

### Testing

- Write unit tests for new functionality
- Use table-driven tests where appropriate
- Mock external dependencies (NATS) using `testutil` package
- Aim for meaningful test coverage

### Documentation

- Update README if adding new features
- Add godoc comments to exported types and functions
- Update relevant documentation in `docs/`
- Include examples for new APIs

## Project Structure

```
go-cluster/
├── cmd/go-cluster/    # CLI application
│   └── cmd/           # Cobra commands
├── backend/           # Backend controllers
├── snapshot/          # Snapshot manager
├── vip/               # VIP manager
├── wal/               # WAL replication
├── testutil/          # Test utilities
├── examples/          # Example applications
├── docs/              # Documentation
├── scripts/           # Validation scripts
└── spec/              # Specifications
```

## Key Files

| File | Description |
|------|-------------|
| `cluster.go` | Core Platform and App types |
| `options.go` | Configuration options |
| `election.go` | Leader election |
| `membership.go` | Cluster membership |
| `kv.go` | Distributed KV store |
| `lock.go` | Distributed locks |
| `rpc.go` | Inter-node RPC |
| `events.go` | Event pub/sub |
| `ring.go` | Consistent hash ring |
| `placement.go` | App placement/scheduling |
| `service_discovery.go` | Service registry |
| `leaf.go` | Leaf node management |
| `health.go` | Health checks |
| `metrics.go` | Prometheus metrics |
| `audit.go` | Audit logging |

## Running Validation Scripts

We have validation scripts in `scripts/` for testing various components:

```bash
# Run all validations
./scripts/validate-all.sh

# Run specific validations
./scripts/validate-election.sh
./scripts/validate-replication.sh
./scripts/validate-failover.sh
./scripts/validate-ring.sh
./scripts/validate-discovery.sh
```

## Testing Guidelines

### Unit Tests

```go
func TestMyFeature(t *testing.T) {
    // Setup
    natsServer := testutil.StartNATS(t)
    defer natsServer.Shutdown()

    // Test
    platform, err := cluster.NewPlatform("test", "node-1", natsServer.ClientURL())
    require.NoError(t, err)

    // Assert
    assert.Equal(t, expected, actual)
}
```

### E2E Tests

For integration tests, use the `testutil` package:

```go
func TestE2E_LeaderElection(t *testing.T) {
    cluster := testutil.NewCluster(t, 3)  // 3 nodes
    defer cluster.Shutdown()

    // Test leader election
    leader := cluster.WaitForLeader(t, "myapp", 10*time.Second)
    assert.NotEmpty(t, leader)
}
```

## Commit Message Format

Use clear, descriptive commit messages:

```
Type: Brief description

Longer description if needed.

Fixes #123
```

Types:
- `Add` - New feature
- `Fix` - Bug fix
- `Update` - Update existing feature
- `Refactor` - Code refactoring
- `Docs` - Documentation only
- `Test` - Test additions/updates
- `Chore` - Build, CI, dependencies

## Review Process

1. All PRs require at least one review
2. CI must pass (tests, lints)
3. Documentation must be updated if needed
4. Breaking changes need discussion

## Release Process

Releases are created using GoReleaser:

1. Tag the release:
   ```bash
   git tag v1.0.0
   git push origin v1.0.0
   ```

2. GitHub Actions will build and publish:
   - Binary releases for Linux, macOS, Windows
   - Docker images to ghcr.io
   - Checksums and signatures

## Getting Help

- Open an issue for bugs or features
- Start a discussion for questions
- Check existing issues and documentation first

## License

By contributing, you agree that your contributions will be licensed under the Apache License 2.0.
