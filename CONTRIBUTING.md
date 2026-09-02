# Contributing

The versioned machine envelope, exit codes, command and flag names, explicit
profile selection, bounded transport, typed Slack method matrix, credential
boundary, and write recovery behavior are public API.

Before changing authentication, profiles, policy, request construction,
logging, output, or writes:

- keep every network command behind an explicit `--profile`;
- prove a credential sentinel cannot enter stdout, stderr, errors, logs, argv,
  environment variables, or generated documentation;
- preserve fixed Slack origins and typed operations with no raw API escape;
- preserve exact identity/generation-bound targets and `write ⊆ read`;
- keep Slack-controlled content marked untrusted;
- ensure a write is dispatched at most once and ambiguous outcomes remain exit
  9;
- test network behavior with `httptest` or injected interfaces, never a live
  workspace.

Run the full gate before opening a pull request:

```sh
test -z "$(gofmt -l .)"
go mod verify
go test ./...
go test -race ./...
go vet ./...
staticcheck ./...
govulncheck ./...
gitleaks dir . --redact
gitleaks git . --redact
actionlint
shellcheck tools/release/*.sh
tools/release/test-source-bundle.sh
```

macOS is the v0.1 runtime target because credentials use Security.framework.
Linux is build/test-only. Windows runtime support needs a separately reviewed
native credential and lock boundary.
