# document-decryptor

A small, narrowly-scoped HTTP service that decrypts password-protected
PDFs. Given an encrypted PDF and a list of identifying strings, it picks a
password from a small configured list based on substring matches, decrypts
the PDF with `qpdf`, and streams the result back.

## Purpose

Given an encrypted PDF and a list of "identifying strings" (e.g. sender
email, email subject, filename), the service:

1. checks whether the uploaded PDF is actually encrypted; if it isn't, it's
   streamed straight back unmodified (see [Echo mode](#echo-mode) below)
2. otherwise, matches the identifiers against a small set of configured
   substrings
3. picks the password for the single matching entry
4. decrypts the PDF with `qpdf`
5. streams the decrypted PDF back

There is no database, no authentication, no outbound network calls, and no
retry logic. Configuration is a flat JSON file re-read on every request, so
it can be updated in place (e.g. via a mounted Kubernetes Secret) without a
restart.

## Project layout

```text
document-decryptor/
├── cmd/decryptor/main.go   entrypoint, env config, HTTP server lifecycle
├── internal/
│   ├── config.go           pattern configuration loading/validation
│   ├── matcher.go          case-insensitive substring matching
│   ├── decrypt.go          qpdf invocation via os/exec (Decryptor interface)
│   └── handler.go          HTTP handlers (/healthz, /decrypt)
├── example/patterns.json   example configuration
├── k8s/                    example Deployment/Service/Secret manifests
├── .github/workflows/      CI and release GitHub Actions
├── Dockerfile
└── .dockerignore
```

## API

### `GET /healthz`

Returns `200 OK` with:

```json
{ "status": "ok" }
```

### `POST /decrypt`

`Content-Type: multipart/form-data` with two parts:

- `identifiers` — a JSON-encoded array of strings (any identifying text:
  sender address, subject line, filename, etc.)
- `file` — the encrypted PDF binary

On success, returns `200 OK` with `Content-Type: application/pdf` and the
decrypted PDF as the body, plus:

- `X-Document-Encrypted` — `true` if the upload was decrypted, `false` if it
  was echoed back as-is (see [Echo mode](#echo-mode))
- `X-Document-Profile` — the configured profile name that was used (omitted
  in echo mode)
- `X-Matched-Pattern` — the specific substring (from that profile's
  `patterns` list) that matched (omitted in echo mode)

See [`patterns.json` format](#patternsjson-format) and [Error
responses](#error-responses) below.

### Echo mode

Before doing any pattern matching, the service checks whether the uploaded
PDF is actually encrypted (`qpdf --is-encrypted`). If it's already
unencrypted, the service skips matching and decryption entirely and streams
the upload straight back with `X-Document-Encrypted: false` — a noop
pass-through for documents that don't need decrypting. `identifiers` is
still required on the request (it's part of the multipart contract), but
its contents are not used in this path.

## `curl` example

```bash
curl \
  -F 'identifiers=["statements@example-bank.com","August 2026 Credit Card Statement","statement-202608.pdf"]' \
  -F 'file=@encrypted.pdf;type=application/pdf' \
  http://localhost:8080/decrypt \
  -o decrypted.pdf
```

## `patterns.json` format

```json
{
  "company-payslip": {
    "patterns": ["payslip"],
    "password": "PASSWORD_A"
  },
  "example-bank-account": {
    "patterns": ["statements@example-bank.com"],
    "password": "PASSWORD_B"
  },
  "example-bank-credit-card": {
    "patterns": ["credit card statement", "statement-202608"],
    "password": "PASSWORD_C"
  }
}
```

- The top-level JSON object maps a **profile name** to a profile
  definition. The profile name is an opaque label returned in the
  `X-Document-Profile` response header and used in logs — it never
  contains the password.
- `patterns` is a list of case-insensitive matching substrings for that
  profile — e.g. sender addresses, subject-line snippets, filename
  fragments. Any one of them matching is enough to select the profile.
- `password` is the qpdf password used when this profile is the single
  match.
- The `Profile` Go struct only has `patterns` and `password` today but is
  designed so additional metadata fields can be added later without
  changing matching or decryption logic.

Matching is a case-insensitive substring search of each profile's patterns
against every supplied identifier. There is no regex or fuzzy matching. A
profile counts as matched if any of its patterns match; the response's
`X-Matched-Pattern` reports the first pattern (in list order) that hit.
Exactly one **profile** must match; zero or multiple matching profiles are
treated as errors (see below).

## Error responses

All errors are returned as JSON: `{"error": "...", "details": [...]}`
(`details` is omitted when not applicable — e.g. it lists the matched
profile names on a `409`).

| Status | Meaning                                                       |
|--------|----------------------------------------------------------------|
| 400    | Malformed request: invalid/malformed `identifiers` JSON, `identifiers` not an array, missing `file` or `identifiers` field |
| 404    | No configured profile matched the supplied identifiers         |
| 409    | Multiple configured profiles matched the supplied identifiers  |
| 413    | Uploaded file exceeds `MAX_UPLOAD_BYTES`                        |
| 415    | Uploaded file does not begin with the PDF magic header (`%PDF-`) |
| 422    | `qpdf` could not inspect or decrypt the PDF (e.g. wrong password, corrupted file) |
| 500    | Configuration could not be read/parsed, or another internal failure |
| 504    | `qpdf` did not finish within `QPDF_TIMEOUT_SECONDS`             |

Raw `qpdf` stderr output is never returned to the caller. It's logged at
`debug` level (see `LOG_LEVEL` below) for operator troubleshooting — e.g. to
see *why* a particular upload got a `422`, restart with `LOG_LEVEL=debug`
and retry the request — but passwords, full configuration contents, PDF
content, and raw multipart bodies are never logged, at any level.

## Environment variables

| Variable               | Default                 | Description                                            |
|-------------------------|--------------------------|----------------------------------------------------------|
| `LISTEN_ADDR`           | `:8080`                 | HTTP listen address                                      |
| `CONFIG_PATH`           | `/config/patterns.json` | Path to the pattern configuration file                   |
| `MAX_UPLOAD_BYTES`      | `10485760` (10 MiB)     | Maximum accepted PDF upload size                          |
| `QPDF_TIMEOUT_SECONDS`  | `30`                    | Timeout for a single `qpdf` invocation                    |
| `QPDF_BIN`              | `qpdf`                  | Path/name of the `qpdf` binary to invoke                  |
| `LOG_LEVEL`             | `info`                  | `debug`, `info`, `warn`, or `error`. `debug` additionally logs qpdf's raw stderr output for every `qpdf --is-encrypted`/`qpdf --decrypt` invocation |

## Local build

Requires Go 1.23+.

```bash
go build -o document-decryptor ./cmd/decryptor
```

Run the unit tests:

```bash
go test ./...
```

`qpdf`-specific integration tests are skipped automatically if `qpdf` is not
found on `PATH`. All HTTP handler tests use a fake `Decryptor` and do not
require `qpdf` to be installed.

## Local run

Install `qpdf` locally (e.g. `apt-get install qpdf` / `brew install qpdf`),
then:

```bash
mkdir -p /tmp/decryptor-config
cp example/patterns.json /tmp/decryptor-config/patterns.json

CONFIG_PATH=/tmp/decryptor-config/patterns.json \
  ./document-decryptor
```

The server listens on `:8080` by default.

## Local Docker run

Build the image:

```bash
docker build -t document-decryptor:latest .
```

Run it, mounting a local `patterns.json` at `/config`:

```bash
docker run --rm -p 8080:8080 \
  -v "$(pwd)/example/patterns.json:/config/patterns.json:ro" \
  document-decryptor:latest
```

## Kubernetes deployment

Example manifests are provided in `k8s/`:

- `k8s/secret.example.yaml` — placeholder Secret containing `patterns.json`
- `k8s/deployment.yaml` — single-replica Deployment mounting the Secret at
  `/config`, with resource limits, readiness/liveness probes on `/healthz`,
  `runAsNonRoot`, `allowPrivilegeEscalation: false`, all capabilities
  dropped, a read-only root filesystem, and `automountServiceAccountToken:
  false`
- `k8s/service.yaml` — `ClusterIP` Service (no Ingress; the service is only
  intended to be reachable from inside the cluster)

Apply with real secret values filled in:

```bash
kubectl apply -f k8s/secret.example.yaml   # after editing REPLACE_ME values
kubectl apply -f k8s/deployment.yaml
kubectl apply -f k8s/service.yaml
```

To roll out an updated `patterns.json`, update the Secret and let the
kubelet sync the mounted volume — no pod restart is required, since
configuration is re-read from disk on every `/decrypt` request.

## CI / releases

- `.github/workflows/ci.yml` runs on every push/PR to `main`/`master`: `gofmt`
  check, `go vet`, `go build`, `go test -race` (with `qpdf` installed so the
  integration tests run), and a Docker image build (not pushed).
- `.github/workflows/release.yml` builds and pushes a multi-arch
  (`linux/amd64`, `linux/arm64`) image to GHCR
  (`ghcr.io/<owner>/<repo>`) whenever a `vX.Y.Z` tag is pushed or a GitHub
  Release is published. It authenticates with the built-in `GITHUB_TOKEN`
  — no additional registry secrets need to be configured.

## Security assumptions

- The service has **no authentication** of its own. It is expected to run
  behind cluster-internal network access only, and is deliberately **not**
  exposed via an Ingress.
- `patterns.json` is expected to be mounted read-only from a Kubernetes
  Secret. The application does not manage, rotate, or expose credentials;
  it only reads them from a normal filesystem path.
- `qpdf` is invoked directly via `exec.CommandContext` with explicit
  argument separation — never through a shell — so upload contents or
  identifiers cannot inject shell commands.
- Uploaded files are streamed to a per-request temporary directory
  (removed afterward, including on error paths) rather than buffered
  entirely in memory.
- Passwords are never included in HTTP responses or log output.
