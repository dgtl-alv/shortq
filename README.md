# ShortQ

URL shortener + QR code generator. Backend plain Go stdlib router. Frontend static offline-ready vanilla JS. DB MySQL.

## Run

```bash
cp .env.example .env
docker compose up --build
```

Open `http://localhost:8080`.

Default superadmin from `.env`:

- Email: `admin@shortq.local`
- Password: `ChangeMe123!`

Change both before production.

## Features

- Landing page
- Microsoft Entra SSO for dashboard access; password auth disabled in production setup
- URL shortener with globally unique custom slug
- QR PNG generator for URL or arbitrary text
- API token/key management
- Department model backed by legacy internal `tenant` tables/API names
- Role model: `superadmin`, `tenant` (department admin), `customer` (department user)
- Superadmin manages departments and users
- Department admins manage users under same department
- Department users can create, edit, delete, list, and view analytics for every link in their own department
- All departments use the same shortened domain, for example `https://s.alvaauto.com/<slug>`; `/r/<slug>` remains backward compatible
- Slugs are globally unique across all departments because shared domain has no department path segment
- Role-scoped analytics at `/api/v1/analytics`
- Offline API docs page at `/docs`, OpenAPI YAML at `/docs/openapi.yaml`
- Health check at `/healthz`

## Auth

Dashboard uses Microsoft Entra SSO sessions. Public API can use either SSO/JWT session auth or API key:

```bash
curl -H 'X-API-Key: ***' http://localhost:8080/api/v1/links
```

## Documentation

- User guide per role: `docs/user-guide.md`
- API docs page: `/docs`
- OpenAPI YAML: `docs/openapi.yaml`
