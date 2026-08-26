# ShortQ

URL shortener + QR code generator. Backend plain Go stdlib router. Frontend static offline-ready vanilla JS. DB MySQL.

## Run

```bash
cp .env.example .env
docker compose up --build
```

Open `http://localhost:8080`.

Development server 54 should use:

```env
APP_BASE_URL=https://s.ilectraev.com
```

Default superadmin from `.env`:

- Email: `admin@shortq.local`
- Password: `ChangeMe123!`

Change both before production.

## Features

- Landing page
- Microsoft Entra SSO for dashboard access; password auth disabled in production setup
- URL shortener with globally unique custom slug
- QR PNG generator for URL or arbitrary text
- API token/key management with account- or user-scoped permissions
- Server-enforced Admin/User dashboard switch for superadmins and department admins
- Single ALVA department backed by legacy internal `tenant` tables/API names
- Role model: `superadmin`, `tenant` (department admin), `customer` (department user)
- Superadmin manages ALVA users and domains; additional departments are disabled
- Department admins manage users under same department
- Department users manage their own links and can view links explicitly shared with the ALVA department
- Department users must have deletion access activated by a superadmin before deleting links or domains or revoking API keys; superadmins always retain deletion access.
- Administrative writes and denied deletion attempts are retained in the superadmin audit log for 365 days.
- All departments use the same shortened domain in production, for example `https://s.alvaauto.com/<slug>`; `/r/<slug>` remains backward compatible
- Slugs are globally unique across all departments because shared domain has no department path segment
- Private-by-default link dashboards with optional read-only sharing across the ALVA department
- Comprehensive per-link and per-user reports with local-time charts, human/bot and routing breakdowns, privacy-safe event tables, and summary/event CSV downloads
- Detailed click reporting for the latest 90 days; all-time counters remain available
- Soft-deleted links stop redirecting but keep their analytics and reserve their slugs
- Role-scoped overview analytics at `/api/v1/analytics` and detailed reports at `/api/v1/reports`
- Offline API docs page at `/docs`, OpenAPI YAML at `/docs/openapi.yaml`
- Health check at `/healthz`

## Auth

Dashboard uses Microsoft Entra SSO sessions. Admin accounts start in Admin mode after each fresh login and can switch to a server-enforced User mode from the dashboard. Public API can use either SSO/JWT session auth or API key:

```bash
curl -H 'X-API-Key: ***' http://localhost:8080/api/v1/links
```

## Documentation

- User guide per role: `docs/user-guide.md`
- API docs page: `/docs`
- OpenAPI YAML: `docs/openapi.yaml`
