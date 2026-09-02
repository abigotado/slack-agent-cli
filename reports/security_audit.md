# Security Audit Report

## 1. Security Scoring Breakdown

- Sensitive File Protection (weight 25%): 100/100 (Strong)
- Secret Detection (weight 30%): 100/100 (Strong)
- Dependency Security (weight 20%): 100/100 (Strong)
- Supply Chain Integrity (weight 10%): 100/100 (Strong)
- Security Automation and CI/CD (weight 15%): 60/100 (Weak)
- Overall Score: 94/100 (Strong)
- Formula: round(100*0.25 + 100*0.30 + 100*0.20 + 100*0.10 + 60*0.15)
- Security Posture: Secure

## 2. Executive Summary

- Overall Score: 94/100 (Strong)
- Previous: 85/100, Change: +9 (improving)
- Top Findings:
  - [LOW]: CI has Go vulnerability and secret scanning but no second
    filesystem vulnerability scanner.
  - [LOW]: Four transitive modules have patch-level updates available; no
    reachable vulnerability or deprecated direct dependency was found.
- Priority Recommendations:
  1. Keep the required pull-request workflow and strict main ruleset enabled.
  2. Review Dependabot proposals and rerun the release gate before every tag.
  3. Consider adding a pinned second filesystem scanner when its added
     supply-chain cost is justified.

## 3. Security Automation and CI/CD

- Description: Automated technical controls applied before integration.
- Score: 60/100 (Weak)
- Score Breakdown:
  - Base: 0
  - Dependabot configured: +20
  - CI vulnerability scanning: +20
  - CI runs on pull requests: +10
  - Lock file validation in CI: +10
  - Pre-commit security hooks: +0
  - Additional filesystem scanner: +0
  - Final: 60/100 (Weak)
- Key Findings:
  - [LOW]: The workflow runs tests, race detection, vet, staticcheck,
    govulncheck, Gitleaks, actionlint, shellcheck, release-tool tests, and
    platform builds.
  - [LOW]: No Trivy or Grype scan is configured.
- Evidence:
  - `.github/workflows/go.yaml`
  - `.github/dependabot.yml`
  - `go.sum`
- Risks:
  - A vulnerability class outside the Go and secret scanners could be missed.
- Recommendations:
  1. Preserve `Build and test` as a required status check on `main`.
  2. Evaluate a pinned second scanner without weakening immutable action/tool
     version policy.

## 4. Secret Detection

- Description: Hardcoded credential patterns and working-tree/history leak
  scanning.
- Score: 100/100 (Strong)
- Score Breakdown:
  - Base: 100
  - High findings: -0
  - Medium findings: -0
  - Low findings: -0
  - Git history findings: -0
  - Final: 100/100 (Strong)
- Key Findings:
  - [LOW]: No credential or Gitleaks finding was detected in the reviewed
    source snapshot.
  - [LOW]: Test tokens are explicit sentinels used to prove redaction and do not
    authorize Slack access.
- Evidence:
  - `internal/auth/store_test.go`
  - `internal/output/output_test.go`
  - `internal/slack/client_test.go`
  - `.github/workflows/go.yaml`
- Risks:
  - A future argv, environment, raw-output, or verbose-log path could expose a
    token if the existing boundary is weakened.
- Recommendations:
  1. Retain both directory and full-history Gitleaks checks.
  2. Keep token sentinels and negative disclosure tests.

## 5. Sensitive File Protection

- Description: Protection of environment files, keys, certificates, local
  audit evidence, and generated release material.
- Score: 100/100 (Strong)
- Score Breakdown:
  - Base: 100
  - Tracked environment files: -0
  - Tracked key or certificate files: -0
  - Missing environment ignore patterns: -0
  - Final: 100/100 (Strong)
- Key Findings:
  - [LOW]: No sensitive credential file is part of the publication snapshot.
  - [LOW]: Local work, evidence, JSON audit state, release output, environment,
    key, and certificate patterns are ignored.
- Evidence:
  - `.gitignore`
  - `SECURITY.md`
- Risks:
  - A future credential export could be committed if ignore and review policy
    are weakened.
- Recommendations:
  1. Keep credentials exclusively in the native store.
  2. Fail publication when non-ignored local or generated material is present.

## 6. Dependency Security

- Description: Reachable vulnerabilities, dependency age, deprecation state,
  and checksum coverage.
- Score: 100/100 (Strong)
- Score Breakdown:
  - Base: 100
  - Critical, high, medium, or low reachable CVEs: -0
  - More than five outdated dependencies: -0
  - Deprecated packages: -0
  - Lock file with integrity hashes: +5 (clamped)
  - Final: 100/100 (Strong)
- Dependency Age Analysis:
  - Outdated count: 4 transitive modules.
  - Deprecated count: 0.
  - Patch updates: go-md2man v2.0.6 to v2.0.7, pflag v1.0.9 to
    v1.0.10, yaml/v3 v3.0.4 to v3.0.5, and check.v1 to its later pseudo-version.
- Key Findings:
  - [LOW]: `govulncheck ./...` found no reachable vulnerability.
  - [LOW]: Direct dependencies resolve through Go modules with checksums.
- Evidence:
  - `go.mod`
  - `go.sum`
  - `.github/dependabot.yml`
- Risks:
  - Transitive packages may acquire future vulnerabilities.
- Recommendations:
  1. Review grouped dependency update proposals.
  2. Rerun govulncheck and module verification before release decisions.

## 7. Supply Chain Integrity

- Description: Dependency origins, immutable checksums, CI action/tool pins,
  and deterministic release inputs.
- Score: 100/100 (Strong)
- Score Breakdown:
  - Base: 100
  - Git, path, or unknown-registry dependencies: -0
  - Missing lock file or integrity hashes: -0
  - Official Go module registry graph: +10 (clamped)
  - Verified checksums: +5 (clamped)
  - Final: 100/100 (Strong)
- Key Findings:
  - [LOW]: Dependencies use Go module versions with `go.sum` integrity data.
  - [LOW]: GitHub actions use immutable commit SHAs and CI tools use explicit
    versions.
  - [LOW]: Release tooling requires a clean annotated tag reachable from
    `origin/main` and proves deterministic archive output.
- Evidence:
  - `go.mod`
  - `go.sum`
  - `.github/workflows/go.yaml`
  - `tools/release/create-source-bundle.sh`
  - `tools/release/test-source-bundle.sh`
- Risks:
  - A moved tag or mutable CI dependency could invalidate release provenance if
    these controls are removed.
- Recommendations:
  1. Keep action SHAs and tool versions immutable.
  2. Verify release assets and immutable-release attestations after publishing.

## 8. Consolidated Findings by Severity

- [HIGH]: No high-severity findings.
- [MEDIUM]: No medium-severity findings.
- [LOW]: CI has no second filesystem vulnerability scanner.
- [LOW]: Four transitive patch updates are available.
- [LOW]: No SQL injection, XSS, path traversal, or code-injection pattern was
  identified in the Go source review.

## 9. Remediation Priority Matrix

1. Preserve required PR CI and the active main ruleset; effort low, impact high.
2. Review grouped transitive patch updates; effort low, impact low.
3. Evaluate a pinned second filesystem scanner; effort medium, impact low.

## 10. Gemini AI Analysis

- Status: Skipped.
- Gemini CLI was not available. No claim depends on AI analysis.

## 11. Project Detection Results

- Detected project type: Go.
- Runtime target: macOS.
- Build/test target: macOS and Linux.
- Package manager: Go modules.
- Repository structure: single command application with internal packages and
  an embedded provider-neutral Agent Skill.

## 12. Appendix: Evidence Index

- Sensitive file policy: `.gitignore`, `SECURITY.md`.
- Credential boundary: `internal/auth`, `internal/profile`.
- Typed request boundary: `internal/slack`, `internal/arch/arch_test.go`.
- Output and recovery: `internal/output`, `internal/errx`,
  `internal/writestate`.
- Target and write policy: `internal/policy`, `internal/intent`,
  `internal/cli/send.go`.
- Automation and release: `.github/workflows/go.yaml`,
  `tools/release`.

## 13. Scan Metadata

- Scan date: 2026-09-02.
- Project type: Go.
- Tools: Go test/race/vet, staticcheck 0.8.1, govulncheck 1.7.0, Gitleaks
  8.30.1, actionlint 1.7.12, shellcheck 0.11.0.
- Findings: high 0, medium 0, low 2.
- Scope: publication snapshot, source, dependencies, CI, and release tooling.
