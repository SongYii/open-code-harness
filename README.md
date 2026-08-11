# Open Code Harness

An open, model-neutral, protocol-aligned harness for building, evaluating, and operating code agents.

The project is currently in its architecture-first phase. See the [foundational architecture design](docs/superpowers/specs/2026-08-11-open-code-harness-architecture-design.md).

## Development

The internal session and turn contract is documented in [Domain Events and State Machine](docs/architecture/domain-events.md).

```bash
gofmt -w .
go vet ./...
go test -race ./... -count=1
```
