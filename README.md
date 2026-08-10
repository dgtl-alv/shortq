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
- Register, login, forgot password, change password
- URL shortener with custom slug
- QR PNG generator for URL or arbitrary text
- API token/key management
- Role model: `superadmin`, `tenant`, `customer`
- Superadmin manages tenants and customers
- Tenant manages customers under same tenant
- Role-scoped analytics at `/api/v1/analytics`
- Offline API docs page at `/docs`, OpenAPI YAML at `/docs/openapi.yaml`
- Health check at `/healthz`

## Auth

Dashboard uses JWT bearer token from login. Public API can use either JWT or API key:

```bash
curl -H 'X-API-Key: sq_live_xxx' http://localhost:8080/api/v1/links
```

## Documentation

- User guide per role: `docs/user-guide.md`
- API docs page: `/docs`
- OpenAPI YAML: `docs/openapi.yaml`
