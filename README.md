# Postal SendGrid

Standalone Go service that lets Plunk run in `EMAIL_PROVIDER=sendgrid` mode while delivering through a Postal mail server.

The service exposes the SendGrid-compatible endpoints used by Plunk, stores domain/message state in SQLite, translates mail sends to Postal `/api/v1/send/message`, accepts Postal webhook events, and forwards mapped SendGrid Event Webhook payloads to Plunk.

## Endpoints

- `POST /v3/whitelabel/domains`
- `POST /v3/whitelabel/domains/{id}/validate`
- `DELETE /v3/whitelabel/domains/{id}`
- `POST /v3/mail/send`
- `POST /webhooks/postal`
- `GET /healthz`

## Plunk configuration

```env
EMAIL_PROVIDER=sendgrid
SENDGRID_API_BASE_URL=http://postal-sendgrid:8080
SENDGRID_API_KEY=<SHIM_AUTH_TOKEN>
```

Do not configure Plunk with the Postal API key. Only this service needs `POSTAL_API_KEY`.

## Service configuration

| Variable | Required | Default | Description |
| --- | --- | --- | --- |
| `SHIM_AUTH_TOKEN` | Yes | | Bearer token expected from Plunk as `SENDGRID_API_KEY`. This token is independent from the Postal API key. |
| `POSTAL_BASE_URL` | Yes | | Postal base URL, for example `https://postal.example.com`. |
| `POSTAL_API_KEY` | Yes | | Postal server API key sent as `X-Server-API-Key`. |
| `PLUNK_WEBHOOK_BASE_URL` | Yes | | Plunk API base URL that receives `/webhooks/sendgrid/events`. |
| `LISTEN_ADDR` | No | `:8080` | HTTP listen address. |
| `DATABASE_PATH` | No | `postal-sendgrid.db` | SQLite database path. Use `/data/postal-sendgrid.db` in Docker. |
| `MAIL_MAX_BYTES` | No | `15728640` | Max `/v3/mail/send` request body size. |
| `WEBHOOK_MAX_BYTES` | No | `1048576` | Max `/webhooks/postal` request body size. |
| `HTTP_TIMEOUT` | No | `10s` | Postal and Plunk HTTP client timeout. |
| `FORWARD_ATTEMPTS` | No | `4` | Attempts for transient Plunk webhook forwarding failures. |
| `FORWARD_BACKOFF` | No | `250ms` | Initial exponential backoff delay. |
| `DNS_CHECK_ENABLED` | No | `false` | Enable live CNAME checks during domain validation. |
| `POSTAL_CNAME_VALUE` | No | `postal.example.invalid` | CNAME target returned in domain auth records. Set this to the Postal tracking/return-path host operators should configure. |
| `WEBHOOK_SIGNING_ENABLED` | No | `true` | Sign forwarded SendGrid webhook payloads for Plunk. Keep enabled unless Plunk signature verification is explicitly disabled. |
| `WEBHOOK_SIGNING_PRIVATE_KEY` | Yes, when signing enabled | | ECDSA P-256 private key (PEM or base64 DER) used to generate SendGrid-compatible asymmetric signatures. Configure the matching public key as `SENDGRID_EVENT_WEBHOOK_PUBLIC_KEY` in Plunk. |

## Docker

Build locally:

```sh
docker build -f Dockerfile -t postal-sendgrid .
```

Run locally:

```sh
docker run --rm -p 8080:8080 \
  -v postal-shim-data:/data \
  -e SHIM_AUTH_TOKEN=change-me \
  -e POSTAL_BASE_URL=https://postal.example.com \
  -e POSTAL_API_KEY=postal-server-api-key \
  -e PLUNK_WEBHOOK_BASE_URL=https://api.example.com \
  -e POSTAL_CNAME_VALUE=postal.example.com \
  -e WEBHOOK_SIGNING_PRIVATE_KEY="$(cat ./sendgrid-webhook-signing-key.pem)" \
  postal-sendgrid
```

Generate a compatible webhook signing key pair:

```sh
openssl ecparam -name prime256v1 -genkey -noout -out sendgrid-webhook-signing-key.pem
openssl ec -in sendgrid-webhook-signing-key.pem -pubout -out sendgrid-webhook-public-key.pem
```

Configure Plunk with the contents of `sendgrid-webhook-public-key.pem` as `SENDGRID_EVENT_WEBHOOK_PUBLIC_KEY`.

## Postal webhook configuration

Configure Postal to send message lifecycle webhooks to:

```text
http://postal-sendgrid:8080/webhooks/postal
```

Forwarded events are signed by default with the ECDSA P-256 `WEBHOOK_SIGNING_PRIVATE_KEY`; configure the matching public key (PEM or base64 DER SPKI) in Plunk as `SENDGRID_EVENT_WEBHOOK_PUBLIC_KEY` while leaving `SENDGRID_EVENT_WEBHOOK_SIGNATURE_REQUIRED=true`.

Mapped events include delivered/sent, bounce, dropped delivery failures, clicks, and opens. The service deduplicates forwarded events using Postal event IDs when present and deterministic event hashes otherwise.

## Development

```sh
test -z "$(gofmt -l .)"
CGO_ENABLED=0 go test ./...
CGO_ENABLED=0 go vet ./...
CGO_ENABLED=0 go build ./...
docker build -t postal-sendgrid .
```
