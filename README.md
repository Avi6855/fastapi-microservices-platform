<div align="center">

# FastAPI Microservices Platform

**A production-shaped, event-driven e-commerce backend delivered as 13 independently deployable services.**

Python 3.14 · FastAPI · PostgreSQL · Kafka · RabbitMQ · Redis · S3 · Kubernetes

[Overview](#overview) · [Architecture](#architecture) · [Services](#service-catalog) · [Quick start](#quick-start) · [Evidence](#engineering-competencies) · [Docs](#documentation)

</div>

---

## Overview

This repository is a **backend-only distributed system**, not a collection of FastAPI processes sharing a database. Each of its 13 services owns a business capability end to end: its own HTTP contract, durable schema and migrations, tests, container image, background workers, and operational runbook.

E-commerce is used as a realistic vehicle for the problems that matter in production software — distributed consistency, idempotency, failure recovery, observability, security boundaries, and independent delivery — rather than as an excuse to expose as many endpoints as possible.

The platform has completed **20 delivery phases (0–19)** and is verified locally with Docker Compose and in CI with a disposable Kubernetes (`Kind`) cluster. It is deliberately a portfolio repository: CI publishes immutable, scanned container images as build artifacts, but no public production environment is claimed.

> **Reviewer entry point:** [docs/portfolio/reviewer-guide.md](docs/portfolio/reviewer-guide.md) maps every material claim in this README to its implementation, contract, runbook, or executable CI evidence.

---

## Table of contents

- [Engineering goals](#engineering-goals)
- [Architecture](#architecture)
- [Service catalog](#service-catalog)
- [Technology stack](#technology-stack)
- [Reliability and consistency](#reliability-and-consistency)
- [Security model](#security-model)
- [Observability](#observability)
- [Testing and quality gates](#testing-and-quality-gates)
- [CI/CD and delivery](#cicd-and-delivery)
- [Quick start](#quick-start)
- [Common operations](#common-operations)
- [Repository map](#repository-map)
- [Documentation](#documentation)
- [Delivery roadmap](#delivery-roadmap)
- [Engineering competencies](#engineering-competencies)
- [Scope and limitations](#scope-and-limitations)
- [License](#license)

---

## Engineering goals

| Goal | How it is enforced |
|---|---|
| Honest service boundaries | One bounded context per service; no service reads another service's tables or migrations |
| Predictable failure behaviour | Transactional Outbox, idempotent consumers with an Inbox, retries with backoff and jitter, dead-letter queues |
| Contract-driven integration | Versioned OpenAPI and JSON Schema artifacts under `contracts/` are canonical, not generated afterthoughts |
| Operable by default | Health probes, structured JSON logs, Prometheus metrics, OpenTelemetry traces, graceful shutdown |
| Deployable independently | Independent image, version, and release cadence per service; Kubernetes-first workload design |
| Verifiable claims | Every architectural assertion has a test, a runbook, or an executable CI job behind it |

---

## Architecture

```text
                                   Clients
                                      |
                                      v
                        Edge / API Gateway (Nginx, TLS)
                        /                          \
                 REST API pods                 WebSocket Chat pods
                       |                              |
                       | local ACID transaction       | persist first
                       v                              v
         service-owned PostgreSQL              chat PostgreSQL
                       |                              |
                       | outbox                       | publish second
                       v                              v
              Kafka domain events              Redis fan-out
                       |
        +--------------+---------------+
        |                              |
        v                              v
 independent consumers        task intent -> RabbitMQ -> Celery workers

 Object data plane: clients and workers <-> S3-compatible object storage (MinIO)
```

The platform follows non-negotiable rules:

1. **Every service owns its durable data.** Schema, migrations, credentials, and audit history are service-local.
2. **REST is for synchronous request/response.** **Kafka** carries durable domain facts (*what happened*). **RabbitMQ + Celery** carry executable work (*what needs to be done*).
3. **Redis is disposable.** Cache, rate limits, session/OTP state, presence, and fan-out — never the source of business truth.
4. **Critical events use a Transactional Outbox.** Consumers are idempotent and deduplicate through an Inbox or equivalent durable record.
5. **Binary data lives in S3-compatible storage** behind an `ObjectStorage` port and an `S3ObjectStorage` adapter — no vendor lock-in to MinIO.
6. **API, event schema, and service versions evolve independently.**
7. **Workloads are Kubernetes-first**, even while local development runs on Compose.

The API services are private to the Compose network. The edge gateway is the only public entry point, providing TLS termination, edge headers, request limits, WebSocket forwarding, and local-only Swagger pages under `https://localhost/docs/`.

---

## Service catalog

| Service | Responsibility | Notable engineering |
|---|---|---|
| `identity-service` | Registration, phone-OTP and staff authentication, device sessions | Hashed refresh tokens, staff lockout policy, signed session metadata |
| `customer-service` | Customer profile and address ownership | Profile projection from identity events |
| `catalog-service` | Products, media references, reviews and moderation | One-level reply limit, explicit administrator moderation |
| `search-service` | Read-optimised search projection | Idempotent projection maintained from catalog events |
| `inventory-service` | Stock, reservations, restoration | Reservation fencing; restore-exactly-once on returns |
| `cart-service` | Cart state and pricing snapshots | Degrades safely when Redis is unavailable |
| `order-service` | Checkout Saga, order lifecycle, returns, invoices | Authoritative price snapshots, receipt-gated refunds, outbox-per-transition |
| `payment-service` | Online payment adapters (Zarinpal, Zibal) | Provider failover only after a *persisted definitive* rejection |
| `shipping-service` | Shipment lifecycle and transition audit | Order-issued short-lived authorization for every command |
| `notification-service` | Email and SMS delivery workers | Retry, backoff, and bounded attempts with dead-letter handling |
| `media-service` | Upload lifecycle, assets, cleanup | Presigned S3 flows; scheduled cleanup of orphaned objects |
| `chat-service` | Realtime support chat, queue assignment | Persist-then-publish WebSocket delivery; atomic agent claim |
| `reference-service` | Non-domain platform probe | Proves the shared HTTP, health, metrics, and image path |

Each service directory contains its own `README.md`, `src/`, `tests/`, migrations, and `Dockerfile`. Detailed ownership rules live in [docs/architecture/service-boundaries.md](docs/architecture/service-boundaries.md).

---

## Technology stack

| Layer | Technology |
|---|---|
| Language / runtime | Python 3.14.7, `uv` workspace with a locked, reproducible dependency graph |
| Web framework | FastAPI 0.141, Uvicorn 0.52, Pydantic 2.13 |
| Persistence | PostgreSQL 18.6, SQLAlchemy 2.0, Alembic 1.19 (one owner per schema) |
| Messaging | Apache Kafka 4.2 (KRaft) via `aiokafka`; RabbitMQ 4.3 with Celery 5.6 |
| Ephemeral state | Redis 8.10 — cache, rate limits, presence, WebSocket fan-out |
| Object storage | S3-compatible API (MinIO locally) through an `ObjectStorage` port |
| Edge | Nginx 1.30 with TLS termination, rate limits, and WebSocket proxying |
| Delivery | Docker Compose locally; Kubernetes manifests + Helm charts; disposable `Kind` conformance cluster |
| Observability | OpenTelemetry Collector 0.159, Prometheus 3.14, Loki 3.7, Tempo 2.9, Grafana 13.1 |
| Quality tooling | pytest 9.1, mypy (strict), Ruff, pip-audit, Trivy image scanning |

The exact pinned versions live in [uv.lock](uv.lock) and [infrastructure/compose/docker-compose.yml](infrastructure/compose/docker-compose.yml); the policy is documented in [docs/development/dependency-policy.md](docs/development/dependency-policy.md).

---

## Reliability and consistency

The interesting engineering in this repository is what happens when something goes wrong.

- **Transactional Outbox** — domain state and the event that describes it commit in one local transaction, so a crash between commit and publish cannot lose a fact. See [ADR-005](docs/adr/ADR-005-outbox-inbox-and-idempotency.md) and the [Outbox/Inbox diagram](docs/diagrams/outbox-inbox.md).
- **Idempotent consumers** — at-least-once delivery is assumed everywhere; each consumer keeps an Inbox or durable deduplication record, so replay is safe by design.
- **Checkout Saga** — order, inventory, payment, invoice, and notification are coordinated with explicit compensating steps instead of a distributed transaction. See the [checkout Saga diagram](docs/diagrams/checkout-saga.md) and the [E2E workflow](tests/e2e/test_phase6_checkout_notification.py).
- **Payment correctness** — provider fallback is allowed only after a persisted definitive rejection; an unknown outcome is never retried as a failure. See [ADR-034](docs/adr/ADR-034-online-payment-provider-routing.md).
- **Return gating** — a delivered-order return cannot restore stock before physical receipt, and an unsupported provider reversal remains an auditable failure rather than a silent retry. See [ADR-040](docs/adr/ADR-040-post-delivery-returns-and-refund-gating.md).
- **Dead-letter policy** — bounded attempts, backoff with jitter, and an explicit DLQ contract for poison messages. See [ADR-016](docs/adr/ADR-016-kafka-consumer-dead-letter-policy.md).
- **Graceful degradation** — a Redis outage degrades cart caching without corrupting durable state ([ADR-013](docs/adr/ADR-013-cart-cache-degradation.md)).

---

## Security model

- Passwords and refresh tokens are stored hashed; access tokens are short-lived and verified per request.
- Internal service trust is explicit: OTP hand-off uses a shared secret, and inter-service commands (for example Shipping transitions) require a short-lived Order-issued authorization.
- Authorization is enforced in the owning service, not at the edge alone.
- Browser origin and token boundaries are documented in [ADR-036](docs/adr/ADR-036-browser-origin-and-token-boundary.md); CORS stays disabled until a reviewed client origin exists.
- Secrets are environment-injected; `.env` is ignored and only [.env.example](.env.example) is committed.
- Every published image passes a `HIGH`/`CRITICAL` vulnerability gate before any registry push.

Baseline: [docs/architecture/security-baseline.md](docs/architecture/security-baseline.md).

---

## Observability

Every service exposes the same operational surface:

- **Health and readiness** endpoints wired to container probes.
- **Structured JSON logs** with correlation and request identifiers.
- **Prometheus metrics** for request rate, latency, errors, and worker outcomes.
- **OpenTelemetry traces** propagated across HTTP and messaging boundaries.
- **Grafana dashboards** backed by Prometheus, Loki, and Tempo through a single collector.

The local `observability` profile is bounded and opt-in: [infrastructure/observability/README.md](infrastructure/observability/README.md). Design notes: [docs/architecture/observability.md](docs/architecture/observability.md).

---

## Testing and quality gates

| Layer | What it proves |
|---|---|
| Unit and service tests (85 test modules) | Domain rules, validation, authorization, idempotency, and error paths per service |
| Contract tests | OpenAPI and event schemas in `contracts/` stay compatible with implementations |
| Integration tests | Real PostgreSQL, Kafka, RabbitMQ, Redis, and S3 adapters — not mocks of the platform |
| Cross-service E2E | Checkout → invoice → email, shipping projection, resilience, and post-delivery returns |
| Kubernetes conformance | Helm release into a disposable `Kind` cluster, controlled migrations, in-cluster API and workflow proof |
| Static and security gates | Ruff, strict mypy, `uv lock --check`, pip-audit, Trivy image scan, structural validation |

```bash
uv run --all-packages pytest -m "not e2e" -q     # fast, hermetic suite
uv run --all-packages ruff check .               # lint
uv run --all-packages mypy                       # strict typing
```

E2E suites are opt-in and require a running topology. Strategy: [docs/development/testing-strategy.md](docs/development/testing-strategy.md).

---

## CI/CD and delivery

`.github/workflows/platform-ci.yml` is the single source of delivery truth:

1. **Source validation** — structural checks, lockfile verification, lint, strict typing.
2. **Test matrix** — unit, contract, and integration suites.
3. **Compose E2E** — the full local topology proves checkout, invoice, notification, shipping, and returns.
4. **Kubernetes conformance** — a disposable `Kind` cluster installs the Helm charts, waits for controlled migrations and workloads, then proves API readiness and in-cluster workflows.
5. **Image delivery** — exact service images are built, scanned for `HIGH`/`CRITICAL` findings, and published to GHCR in one sequential gate, so nothing reaches the registry unscanned.

Delivery rationale: [docs/development/ci-cd.md](docs/development/ci-cd.md) and [ADR-038](docs/adr/ADR-038-single-job-ghcr-publication.md).

---

## Quick start

### Prerequisites

| Tool | Version | Notes |
|---|---|---|
| Docker Desktop (or Docker Engine + Compose v2) | 24+ | Must be running before any Compose command; budget ~4 GB of memory for the full topology |
| `uv` | 0.13+ | Installs Python 3.14.7 from `.python-version` and the locked workspace |
| PowerShell 7 (`pwsh`) | 7.4+ | Required by `scripts/validate_phase0.ps1`; the Bash equivalents below work without it |
| OpenSSL | 3.x | Optional — only for generating the local edge certificate on non-Windows hosts |

### 1. Configure the environment

```bash
cp .env.example .env
```

Set `PLATFORM_INTERNAL_OTP_SHARED_SECRET` and `IDENTITY_SESSION_METADATA_HMAC_SECRET` to distinct random values of 32+ characters:

```bash
python -c "import secrets; print(secrets.token_hex(32))"
```

`.env` is git-ignored. Only `.env.example` is committed.

### 2. Install the pinned toolchain

```bash
uv sync --locked --all-packages
```

### 3. Create the local edge certificate (once)

```powershell
pwsh -NoProfile -File .\scripts\new_local_edge_certificate.ps1
```

Without PowerShell 7, the equivalent OpenSSL command is:

```bash
openssl req -x509 -newkey rsa:2048 -nodes -days 30 \
  -keyout infrastructure/edge/tls/tls.key \
  -out infrastructure/edge/tls/tls.crt \
  -subj "/CN=localhost" \
  -addext "subjectAltName=DNS:localhost,IP:127.0.0.1,IP:0:0:0:0:0:0:0:1"
```

Both outputs are git-ignored.

### 4. Build and start the platform

```bash
docker compose up -d --build
```

The root `.env` sets `COMPOSE_FILE`, so no `-f` or `--env-file` flag is needed. API services publish no host ports; the edge gateway is the only public entry point.

### 5. Apply service-owned migrations

Migrations are explicit and never run implicitly at container start:

```powershell
foreach ($s in 'identity','customer','catalog','search','media','inventory','cart','order','payment','shipping','notification','chat') {
    pwsh -File .\scripts\platform.ps1 -Task "migrate-$s"
}
```

Or directly through `uv` on macOS, Linux, or Git Bash:

```bash
for s in identity customer catalog search media inventory cart order payment shipping notification chat; do
  uv run --package "$s-service" alembic -c "services/$s-service/alembic.ini" upgrade head
done
```

### 6. Verify

| Surface | URL |
|---|---|
| Edge gateway (HTTPS, self-signed) | `https://localhost/api/v1/...` |
| Swagger / OpenAPI (local only) | `https://localhost/docs/` |
| Realtime chat WebSocket | `wss://localhost/api/v1/chat/ws` |
| MinIO S3 API / console | `http://localhost:9000` · `http://localhost:9001` |
| Mailpit (captured email) | `http://localhost:8025` |

```bash
docker compose ps                 # every service should report healthy
curl -k https://localhost/docs/   # edge gateway is serving
```

Optional telemetry stack: [infrastructure/observability/README.md](infrastructure/observability/README.md).

---

## Common operations

```bash
docker compose stop                    # halt containers, keep state and environment
docker compose start                   # resume with the same state
docker compose up -d --force-recreate  # required after changing .env or compose config
docker compose down                    # remove containers
docker compose logs -f <service>       # follow a single service
```

Create an administrator account for staff workflows:

```powershell
pwsh -File .\scripts\platform.ps1 -Task provision-admin -AdminEmail admin@example.com
```

Opt-in E2E run against the local topology:

```powershell
$env:RUN_E2E = "1"; uv run pytest tests/e2e/test_phase6_checkout_notification.py
```

Operational runbooks: [docs/runbooks/](docs/runbooks/).

---

## Repository map

```text
services/       13 independently deployable bounded contexts + documented service template
libs/           platform-auth, platform-messaging, platform-observability (technical primitives only)
contracts/      canonical OpenAPI and JSON Schema event artifacts (49 files)
infrastructure/ Compose, edge gateway, MinIO, Kubernetes, Helm, observability
docs/           111 documents: architecture, 41 ADRs, diagrams, events, runbooks, phase plans
scripts/        repeatable developer, migration, and delivery automation
tests/e2e/      cross-service workflow tests
.github/        CI/CD workflows
```

Full layout: [docs/architecture/repository-structure.md](docs/architecture/repository-structure.md).

---

## Documentation

**Architecture** — [overview](docs/architecture/overview.md) · [communication and consistency](docs/architecture/communication-and-consistency.md) · [service boundaries](docs/architecture/service-boundaries.md) · [security baseline](docs/architecture/security-baseline.md) · [observability](docs/architecture/observability.md) · [diagrams](docs/diagrams/README.md)

**Decisions** — [Architecture Decision Records](docs/adr/README.md) (41 records covering transport roles, consistency, identity, payments, shipping, delivery, and scaling)

**Contracts** — [event contracts](docs/events/README.md) · [OpenAPI catalog](contracts/README.md)

**Engineering practice** — [coding standards](docs/development/coding-standards.md) · [dependency policy](docs/development/dependency-policy.md) · [testing strategy](docs/development/testing-strategy.md) · [CI/CD strategy](docs/development/ci-cd.md)

**Operations** — [Kubernetes deployment](docs/runbooks/kubernetes-deployment.md) · [Helm charts](infrastructure/helm/README.md) · [post-delivery returns](docs/runbooks/post-delivery-returns.md) · [payment routing](docs/runbooks/online-payment-provider-routing.md) · [invoice and notification](docs/runbooks/invoice-notification.md) · [SMS OTP](docs/runbooks/sms-otp.md) · [staff operations](docs/runbooks/admin-operations.md) · [search projection](docs/runbooks/search-projection.md) · [edge gateway](docs/runbooks/edge-gateway.md)

**Review** — [portfolio reviewer guide](docs/portfolio/reviewer-guide.md)

---

## Delivery roadmap

| Phase | Outcome |
|---|---|
| 0–1 | Architecture, repository foundation, shared platform primitives |
| 2–3 | Identity & Customer, Catalog & Media |
| 4–5 | Inventory & Cart, Order & Payment |
| 6–8 | Invoice & Notification, Realtime Chat, Search |
| 9–12 | Kubernetes, Helm, complete observability, hardening |
| 13–15 | Staff password reset and device sessions, catalog reviews and moderation, API autoscaling |
| 16–17 | Online payment provider routing, portfolio evidence and reviewer guide |
| 18 | Shipping ownership extraction |
| 19 | Post-delivery returns and refund lifecycle |

Each phase has an approved plan under [docs/development/](docs/development/), plus the ADRs it introduced.

---

## Engineering competencies

This project was built as a focused demonstration of the skills an **Applications Developer I** role depends on: shipping reliable services, understanding distributed failure modes, and leaving systems that other engineers can operate.

| Competency | Evidence in this repository |
|---|---|
| **Service design and API engineering** | 13 bounded contexts with versioned OpenAPI contracts under [`contracts/openapi/`](contracts/openapi/), resource-level authorization, consistent error envelopes |
| **Data modelling and migrations** | Service-owned PostgreSQL schemas with reviewed, ordered Alembic migrations and a CI migration-head check |
| **Distributed systems reasoning** | Transactional Outbox, Inbox deduplication, at-least-once delivery, Saga compensation, ordering keys, and dead-letter policy |
| **Asynchronous processing** | Kafka domain events, RabbitMQ/Celery workers with retry, backoff, jitter, maximum attempts, and idempotency |
| **Reliability under failure** | Resilience E2E suite, Redis degradation path, payment-outcome fencing, receipt-gated returns |
| **Testing discipline** | 85 test modules across unit, contract, integration, and cross-service E2E layers |
| **Performance and scale awareness** | Stateless API pods, caching and rate limiting in Redis, read-optimised search projection, horizontal autoscaling policy |
| **Observability and debugging** | Probes, structured logs, Prometheus metrics, OpenTelemetry traces, Grafana dashboards, and per-incident runbooks |
| **Security fundamentals** | Hashed credentials, short-lived tokens, inter-service authorization, secret handling, and image vulnerability gating |
| **CI/CD and cloud-native delivery** | Automated gate chain, disposable `Kind` conformance cluster, Helm packaging, immutable scanned images |
| **Technical communication** | 41 ADRs, 111 documents, diagrams, runbooks, and a claim-to-evidence reviewer guide |

---

## Scope and limitations

Documented boundaries, stated explicitly rather than hidden behind placeholders:

- No browser storefront — the platform is backend-only, and CORS remains disabled until a reviewed client origin exists.
- No public production deployment — GHCR publication is artifact delivery, and `Kind` is disposable conformance evidence.
- Payment adapters run against sandbox/test configuration; no claim is made that a live merchant transaction has occurred.
- Carrier integration (labels, webhooks, rate shopping, address validation, reverse logistics) is out of scope; Shipping owns the internal shipment lifecycle.
- A real target environment still requires owned secrets, an ingress/TLS issuer, managed durable dependencies, egress policy, retention, access control, and a promotion/rollback process.

---

## License

Released under the [MIT License](LICENSE). Copyright (c) 2026 Avinash Rajendra Patil.
