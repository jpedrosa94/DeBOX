# CLAUDE.md — DeBOX

## Project Overview

Decentralized encrypted file storage built on Sui + Walrus. Users authenticate via Google OAuth (zkLogin through Enoki), encrypt files client-side with Seal, and store them on Walrus testnet. A Go backend proxies Walrus, stores file metadata in MongoDB, and authenticates requests via Google JWT verification.

## Architecture

| Layer        | Tech                          | Location       |
|-------------|-------------------------------|----------------|
| Frontend    | React 18 + Vite 5             | `frontend/`    |
| Backend     | Go 1.22 + MongoDB driver      | `backend/`     |
| Database    | MongoDB 7 (Docker)            | `docker-compose.yml` |
| Storage     | Walrus testnet                | external       |
| Encryption  | Seal (`@mysten/seal`)         | frontend       |
| Auth        | zkLogin via Enoki + JWT       | frontend + backend |
| Contract    | Sui Move (`identity_allowlist`) | `move/`      |

## Getting Started

### Prerequisites
- Node.js, npm, Go 1.22+, Docker
- Copy env files and fill in values:
  - `frontend/.env` — needs `VITE_GOOGLE_CLIENT_ID`, `VITE_ENOKI_API_KEY`, `VITE_SEAL_PACKAGE_ID`
  - `backend/.env` — needs `MONGODB_URI`, `GOOGLE_CLIENT_ID`

### Run with Docker Compose (recommended)

```bash
docker compose up --build
```

This starts all three services:
- **MongoDB** on `:27017` (persistent volume)
- **Backend** on `:3001` (Go server)
- **Frontend** on `:5173` (Vite dev server)

### Run Locally (without Docker)

```bash
# Start MongoDB separately (e.g., via Docker)
docker run -d -p 27017:27017 mongo:7

# Install all dependencies
npm run install:all

# Run both frontend and backend concurrently
npm run dev
```

### Build Backend

```bash
cd backend
go build -o server main.go && ./server
```

## Testing

Tests use **Vitest** (v4) in the frontend:

```bash
cd frontend
npm test              # Single run
npm run test:watch    # Watch mode
npm run test:ui       # Browser UI runner
```

Config: `frontend/vitest.config.js` — globals enabled, node environment, `VITE_SEAL_PACKAGE_ID` set automatically.

Test files:
- `frontend/src/__tests__/ptb-format.test.js` — Seal PTB byte format validation (real SDK)
- `frontend/src/__tests__/sealService.test.js` — Seal encrypt/decrypt mock suite

## Project Structure

```
├── docker-compose.yml           # Full stack: mongo + backend + frontend
├── frontend/
│   ├── Dockerfile               # Vite dev server container
│   └── src/
│       ├── App.jsx              # Main app + OAuth callback + auth token sync
│       ├── api.js               # Backend API calls (with JWT auth headers)
│       ├── sealService.js       # Seal encrypt/decrypt + SuiClient
│       ├── utils.js             # formatBytes, fileTypeIcon, shortAddress
│       ├── components/
│       │   ├── Header.jsx       # Profile, balance, logout
│       │   ├── LoginPage.jsx    # Google OAuth login
│       │   ├── DropZone.jsx     # Drag-drop upload w/ encrypt toggle
│       │   ├── FileCard.jsx     # File display + download/send/delete
│       │   ├── FilePreview.jsx  # Media preview (image/pdf/video/audio)
│       │   ├── SendModal.jsx    # Re-encrypt & send to recipient
│       │   └── ImportForm.jsx   # Import file by blob ID
│       ├── hooks/
│       │   ├── useZkLogin.js    # EnokiFlow zkLogin hook (stores JWT in session)
│       │   ├── useFiles.js      # Upload/download/send/delete logic
│       │   └── useBalance.js    # SUI balance polling
│       └── __tests__/
├── backend/
│   ├── Dockerfile               # Multi-stage Go build
│   ├── main.go                  # HTTP server: MongoDB, JWT auth, Walrus proxy
│   ├── go.mod
│   └── .env                     # MONGODB_URI, GOOGLE_CLIENT_ID
└── move/
    └── sources/identity_allowlist.move  # seal_approve access control
```

## Backend API

| Method | Endpoint                       | Auth | Description                    |
|--------|-------------------------------|------|--------------------------------|
| POST   | `/api/upload`                 | Yes  | Upload file to Walrus          |
| GET    | `/api/blob/{blobId}`          | No   | Download blob from Walrus      |
| GET    | `/api/files/{address}`        | Yes  | List files for wallet address  |
| POST   | `/api/files/{address}`        | Yes  | Save file entry to index       |
| DELETE | `/api/files/{address}/{blobId}` | Yes | Remove file entry from index |
| GET    | `/health`                     | No   | Health check                   |

Authentication: `Authorization: Bearer <google-jwt>` header. Backend verifies JWT signature against Google's JWKS, checks issuer/audience/expiry, and maps `sub` claim to Sui address via trust-on-first-use (TOFU) pattern in the `users` MongoDB collection.

## Backend Configuration (Environment Variables)

| Variable | Default | Description |
|----------|---------|-------------|
| `BACKEND_PORT` | `3001` | Server port |
| `CORS_ORIGIN` | `http://localhost:5173` | Allowed CORS origin |
| `WALRUS_PUBLISHER` | testnet URL | Walrus upload endpoint |
| `WALRUS_AGGREGATOR` | testnet URL | Walrus download endpoint |
| `WALRUS_EPOCHS` | `5` | Storage duration in epochs |
| `MONGODB_URI` | `mongodb://localhost:27017` | MongoDB connection string |
| `MONGODB_DATABASE` | `debox` | Database name |
| `GOOGLE_CLIENT_ID` | (required) | For JWT audience verification |

## MongoDB Collections

- **`files`** — file entries indexed by `{address, uploadedAt}`. Fields: address, blobId, filename, mimeType, size, status, isEncrypted, uploadedAt.
- **`users`** — TOFU address-to-identity mapping. Fields: sub (unique), address (unique), email, createdAt.

## Key Technical Details

### Seal txBytes Format (Critical)

Always build transactions with `onlyTransactionKind: true`:

```js
const txBytes = await tx.build({ client: suiClient, onlyTransactionKind: true });
```

- Returns `TransactionKind` BCS: `[0x00 (PTx tag), ProgrammableTransaction...]`
- Seal SDK calls `txBytes.slice(1)` internally before sending to key servers
- Do NOT set sender, gas price, gas budget, or gas payment — none needed
- Do NOT pass full `TransactionData` bytes — causes "Invalid PTB: Invalid BCS"
- Use `tx.pure.vector("u8", fromHex(userAddress))` for the `id` argument

### Seal Key Servers

Three testnet servers with 2-of-3 threshold:
- Mysten #1 and #2 (independent servers)
- Mysten Committee (requires `aggregatorUrl` in config)

### zkLogin (Enoki)

- `EnokiFlow` manages the full OAuth → salt → ZK proof → Sui address pipeline
- `EnokiKeypair.signPersonalMessage()` returns `{ signature, bytes }` — destructure to get plain base64
- The wrapper in `useZkLogin.js` → `getZkLoginSigner()` handles this
- JWT is stored in session state and sent to backend as `Authorization: Bearer` header

### JWT Authentication Flow

1. Frontend logs in via Enoki → receives Google ID Token (JWT)
2. JWT stored in `session.jwt` by `useZkLogin` hook
3. `App.jsx` syncs JWT to `api.js` via `setAuthToken(session.jwt)`
4. All authenticated API calls include `Authorization: Bearer <jwt>` header
5. Backend verifies JWT signature against Google's JWKS (cached 1hr)
6. Backend checks `iss`, `aud`, `exp` claims
7. Backend maps JWT `sub` claim to Sui address via TOFU in `users` collection

## Conventions

- Frontend uses `.jsx` extension for React components
- No TypeScript — plain JavaScript throughout
- Hooks follow `useXxx` naming in `hooks/` directory
- Components are in `components/` — one component per file
- Backend uses `go.mongodb.org/mongo-driver/v2` and `github.com/golang-jwt/jwt/v5`
- Vitest constructor mocks must use `vi.fn(function() { ... })`, not arrow functions
- Move contract is minimal — single `seal_approve` function for identity-based access
