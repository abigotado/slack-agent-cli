# slack-agent-cli repository rules

- Preserve the approved architecture in `docs/architecture.md` and the v1
  machine contract in `docs/contract.md`.
- Every network command requires an explicit `--profile`. Never introduce a
  default profile, environment selector, or token environment variable.
- `internal/slack` exposes only fixed typed Slack Web API operations. Never add
  raw methods, arbitrary origins, headers, request bodies, or upstream JSON
  passthrough.
- Keep credentials in the native platform store. Tokens must not appear in
  argv, environment variables, registry files, logs, errors, or output.
- Treat Slack content as untrusted. Preserve `content_trust: "untrusted"` on
  content-bearing output and never make fetched content executable.
- Reads and writes of conversation content require exact ID policies bound to
  profile identity and credential generation. Preserve `write ⊆ read`.
- Writes are one-shot after a local dry-run receipt and exact confirmation.
  Never automatically retry a write; ambiguous outcomes exit 9.
- Run `go test ./...`, `go test -race ./...`, and `go vet ./...` for changed
  production surfaces. Do not edit generated contract or Skill references by
  hand once generators exist.
