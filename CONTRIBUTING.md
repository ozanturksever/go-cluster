# Contributing to go-cluster

Thank you for your interest in contributing to go-cluster! This document provides guidelines and information for contributors.

## Getting Started

1. Fork the repository on GitHub
2. Clone your fork locally:
   ```bash
   git clone https://github.com/YOUR_USERNAME/go-cluster.git
   cd go-cluster
   ```
3. Add the upstream remote:
   ```bash
   git remote add upstream https://github.com/ozanturksever/go-cluster.git
   ```

## Development Setup

### Prerequisites

- Go 1.21 or later
- NATS Server with JetStream enabled (for integration tests)
- Docker (optional, for running tests with testcontainers)

### Running Tests

```bash
# Run all tests
go test -v ./...

# Run tests with race detector
go test -race ./...

# Run specific package tests
go test -v ./health/...

# Run benchmarks
go test -bench=. ./...
```

### Running Linters

```bash
# Install golangci-lint
go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest

# Run linters
golangci-lint run
```

## Making Changes

### Branch Naming

Use descriptive branch names:
- `feature/add-new-feature`
- `fix/issue-123`
- `docs/update-readme`

### Commit Messages

Follow conventional commit messages:

```
type(scope): description

[optional body]

[optional footer]
```

Types:
- `feat`: New feature
- `fix`: Bug fix
- `docs`: Documentation changes
- `test`: Adding or updating tests
- `refactor`: Code refactoring
- `chore`: Maintenance tasks

Examples:
```
feat(election): add support for weighted leader election
fix(vip): handle interface not found error gracefully
docs: update installation instructions
```

### Code Style

- Follow standard Go conventions and idioms
- Run `gofmt` and `goimports` on your code
- Add comments for exported functions and types
- Keep functions focused and small
- Write tests for new functionality

### Testing Guidelines

- Write unit tests for new functionality
- Include table-driven tests where appropriate
- Use testcontainers for integration tests requiring NATS
- Aim for meaningful test coverage, not just high percentages

## Pull Request Process

1. Create a new branch from `main`:
   ```bash
   git checkout -b feature/your-feature
   ```

2. Make your changes and commit them

3. Push to your fork:
   ```bash
   git push origin feature/your-feature
   ```

4. Open a Pull Request against the `main` branch

5. Ensure CI passes (tests, linting, build)

6. Wait for code review and address any feedback

### PR Checklist

- [ ] Tests pass locally
- [ ] New functionality has tests
- [ ] Code follows project style guidelines
- [ ] Documentation updated if needed
- [ ] Commit messages follow conventions

## Reporting Issues

### Bug Reports

When reporting bugs, please include:

- Go version (`go version`)
- NATS Server version
- Operating system
- Steps to reproduce
- Expected vs actual behavior
- Relevant logs or error messages

### Feature Requests

When requesting features, please include:

- Use case description
- Proposed solution (if any)
- Alternative solutions considered

## Code of Conduct

- Be respectful and inclusive
- Focus on constructive feedback
- Help others learn and grow

## Questions?

If you have questions, feel free to:

- Open a GitHub issue
- Start a discussion in GitHub Discussions

Thank you for contributing!
