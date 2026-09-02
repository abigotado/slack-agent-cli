<!--
The title becomes the commit subject on main because pull requests are
squash-merged. Write it in the imperative.
-->

## What changes, and why

## Contract impact

- [ ] Additive or none
- [ ] Breaking and agreed separately

## Validation

- [ ] `test -z "$(gofmt -l .)"`
- [ ] `go mod verify`
- [ ] `go test ./...`
- [ ] `go test -race ./...`
- [ ] `go vet ./...`
- [ ] Security and release-tooling checks pass

## Checklist

- [ ] Boundary behavior is covered by tests
- [ ] No credential, local report evidence, or private workspace content is in
      the diff
- [ ] Network commands still require an explicit profile
- [ ] No raw Slack API surface was introduced
