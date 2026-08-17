# document-decryptor

A small, narrowly-scoped HTTP service that decrypts password-protected PDFs.
It is designed to be called from an n8n workflow running inside the same
Kubernetes cluster: n8n handles email retrieval, attachment download, and
Google Drive upload; this service does exactly one thing — pick the right
password from a small configured list and run `qpdf` to decrypt the PDF.

## Purpose

Given an encrypted PDF and a list of "identifying strings" (sender email,
email subject, filename, etc.), the service:

1. matches the identifiers against a small set of configured substrings
2. picks the password for the single matching entry
3. decrypts the PDF with `qpdf`
4. streams the decrypted PDF back

There is no database, no authentication, no outbound network calls, and no
retry logic. Configuration is a flat JSON file re-read on every request, so
it can be updated in place (e.g. via a Kubernetes Secret volume) without a
restart.

## Architecture / flow

```text
Gmail
  |
n8n
  |
POST PDF + identifiers
  |
document-decryptor
  |-- load patterns.json
  |-- identify exactly one profile
  |-- run qpdf
  |-- return decrypted PDF
  |
n8n
  |
Google Drive
```

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
├── Dockerfile
└── .dockerignore
```

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

## `patterns.json` format

```json
{
  "payslip": {
    "name": "company-payslip",
    "password": "PASSWORD_A"
  },
  "statements@example-bank.com": {
    "name": "example-bank-account",
    "password": "PASSWORD_B"
  },
  "credit card statement": {
    "name": "example-bank-credit-card",
    "password": "PASSWORD_C"
  }
}
```

- The top-level JSON object maps a **matching substring** to a profile.
- `name` is an opaque label returned in the `X-Document-Profile` response
  header and used in logs — it never contains the password.
- `password` is the qpdf password used when this pattern is the single
  match.
- The `Profile` Go struct only has `name` and `password` today but is
  designed so additional metadata fields can be added later without
  changing matching or decryption logic.

Matching is a case-insensitive substring search of each pattern against
every supplied identifier (email address, subject line, filename, etc.).
There is no regex or fuzzy matching. Exactly one pattern must match; zero or
multiple matches are treated as errors (see below).

## `curl` example

```bash
curl \
  -F 'identifiers=["statements@example-bank.com","August 2026 Credit Card Statement","statement-202608.pdf"]' \
  -F 'file=@encrypted.pdf;type=application/pdf' \
  http://localhost:8080/decrypt \
  -o decrypted.pdf
```

## Environment variables

| Variable               | Default                 | Description                                            |
|-------------------------|--------------------------|----------------------------------------------------------|
| `LISTEN_ADDR`           | `:8080`                 | HTTP listen address                                      |
| `CONFIG_PATH`           | `/config/patterns.json` | Path to the pattern configuration file                   |
| `MAX_UPLOAD_BYTES`      | `10485760` (10 MiB)     | Maximum accepted PDF upload size                          |
| `QPDF_TIMEOUT_SECONDS`  | `30`                    | Timeout for a single `qpdf` invocation                    |
| `QPDF_BIN`              | `qpdf`                  | Path/name of the `qpdf` binary to invoke                  |

## Kubernetes deployment

Example manifests are provided in `k8s/`:

- `k8s/secret.example.yaml` — placeholder Secret containing `patterns.json`
- `k8s/deployment.yaml` — single-replica Deployment mounting the Secret at
  `/config`, with resource limits, readiness/liveness probes on `/healthz`,
  `runAsNonRoot`, `allowPrivilegeEscalation: false`, all capabilities
  dropped, a read-only root filesystem, and `automountServiceAccountToken:
  false`
- `k8s/service.yaml` — `ClusterIP` Service (no Ingress; this service is only
  intended to be called from inside the cluster, e.g. by n8n)

Apply with real secret values filled in:

```bash
kubectl apply -f k8s/secret.example.yaml   # after editing REPLACE_ME values
kubectl apply -f k8s/deployment.yaml
kubectl apply -f k8s/service.yaml
```

To roll out an updated `patterns.json`, update the Secret and let the
kubelet sync the mounted volume — no pod restart is required, since
configuration is re-read from disk on every `/decrypt` request.

## Expected n8n request format

n8n's HTTP Request node should issue a `multipart/form-data POST` to
`http://document-decryptor.<namespace>.svc.cluster.local:8080/decrypt`
with two parts:

- `identifiers` — a JSON-encoded array of strings (sender address, subject,
  filename, or any other useful identifying text)
- `file` — the encrypted PDF binary

On success it receives the decrypted PDF back as the response body
(`Content-Type: application/pdf`), along with `X-Document-Profile` and
`X-Matched-Pattern` headers describing which configuration entry was used.

## Error responses

All errors are returned as JSON: `{"error": "...", "details": [...]}`
(`details` is omitted when not applicable — e.g. it lists the matched
pattern names on a `409`).

| Status | Meaning                                                       |
|--------|----------------------------------------------------------------|
| 400    | Malformed request: invalid/malformed `identifiers` JSON, `identifiers` not an array, missing `file` or `identifiers` field |
| 409    | Multiple configured patterns matched the supplied identifiers  |
| 413    | Uploaded file exceeds `MAX_UPLOAD_BYTES`                        |
| 415    | Uploaded file does not begin with the PDF magic header (`%PDF-`) |
| 422    | No configured pattern matched, or `qpdf` could not decrypt the PDF (e.g. wrong password) |
| 500    | Configuration could not be read/parsed, or another internal failure |
| 504    | `qpdf` did not finish within `QPDF_TIMEOUT_SECONDS`             |

Raw `qpdf` stderr output is never returned to the caller. It may be logged
at debug level for operator troubleshooting, but passwords, full
configuration contents, PDF content, and raw multipart bodies are never
logged.

## Security assumptions

- The service has **no authentication** of its own. It is expected to run
  behind Kubernetes network policy / cluster-internal access only, called
  solely by n8n. It is deliberately **not** exposed via an Ingress.
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
