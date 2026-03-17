# DeBOX

Decentralized encrypted file storage built on [Sui](https://sui.io) + [Walrus](https://walrus.xyz). Files are encrypted client-side with [Seal](https://seal-docs.wal.app) before being stored on the Walrus network. Authentication uses [zkLogin](https://docs.sui.io/concepts/cryptography/zklogin) via Google OAuth through [Enoki](https://portal.enoki.mystenlabs.com).

## Architecture

```
                   Google OAuth
                       |
  Browser ──── Frontend (React) ──── Backend (Go) ──── Walrus (testnet)
                   |                      |
              Seal encrypt           MongoDB 7
              zkLogin (Enoki)        JWT auth
```

| Component | Tech | Port |
|-----------|------|------|
| Frontend  | React 18, Vite 5 | 5173 |
| Backend   | Go 1.22, mongo-driver v2, golang-jwt v5 | 3001 |
| Database  | MongoDB 7 | 27017 |
| Storage   | Walrus testnet | external |
| Contract  | Sui Move (`identity_allowlist`) | Sui testnet |

## Quick Start

### Docker Compose (recommended)

```bash
make up
```

Or equivalently:

```bash
docker compose up --build
```

This starts MongoDB, the Go backend, and the Vite frontend. Open http://localhost:5173.

### Local Development

```bash
# Install dependencies
make install

# Start MongoDB (requires Docker)
make db

# Run backend + frontend concurrently
make dev
```

### Run Tests

```bash
make test
```

See the [Makefile](Makefile) for all available targets.

## Prerequisites

- **Node.js** (v18+) and npm
- **Go** 1.22+
- **Docker** and Docker Compose

## Configuration

### Frontend (`frontend/.env`)

```bash
# Google OAuth client ID
VITE_GOOGLE_CLIENT_ID=your-client-id.apps.googleusercontent.com

# Enoki API key (get one at https://portal.enoki.mystenlabs.com)
VITE_ENOKI_API_KEY=enoki_public_...

# Deployed Move package ID (see "Deploy the Move Contract" below)
VITE_SEAL_PACKAGE_ID=0x...
```

### Backend (`backend/.env`)

```bash
# MongoDB connection
MONGODB_URI=mongodb://localhost:27017
MONGODB_DATABASE=debox

# Must match the frontend VITE_GOOGLE_CLIENT_ID
GOOGLE_CLIENT_ID=your-client-id.apps.googleusercontent.com
```

Additional backend env vars (all have sensible defaults):

| Variable | Default | Description |
|----------|---------|-------------|
| `BACKEND_PORT` | `3001` | Server port |
| `CORS_ORIGIN` | `http://localhost:5173` | Allowed CORS origin |
| `WALRUS_PUBLISHER` | testnet URL | Walrus upload endpoint |
| `WALRUS_AGGREGATOR` | testnet URL | Walrus download endpoint |
| `WALRUS_EPOCHS` | `5` | Storage duration in epochs |

## API

| Method | Endpoint | Auth | Description |
|--------|----------|------|-------------|
| `POST` | `/api/upload` | Yes | Upload file to Walrus |
| `GET` | `/api/blob/{blobId}` | No | Download blob from Walrus |
| `GET` | `/api/files/{address}` | Yes | List files for a wallet address |
| `POST` | `/api/files/{address}` | Yes | Save file metadata |
| `DELETE` | `/api/files/{address}/{blobId}` | Yes | Remove file metadata |
| `GET` | `/health` | No | Health check |

Authenticated endpoints require an `Authorization: Bearer <google-jwt>` header. The backend verifies the JWT against Google's JWKS and maps the `sub` claim to a Sui address using a trust-on-first-use pattern.

## Deploy the Move Contract

The `identity_allowlist` contract provides on-chain access control for Seal decryption. To deploy your own:

```bash
cd move
sui client publish --gas-budget 100000000
```

Copy the published package ID into `frontend/.env` as `VITE_SEAL_PACKAGE_ID`.

## Project Structure

```
debox/
├── docker-compose.yml        # Full stack (mongo + backend + frontend)
├── Makefile                  # Build, test, and run targets
├── frontend/
│   ├── Dockerfile
│   ├── src/
│   │   ├── App.jsx           # Main app, OAuth callback, auth sync
│   │   ├── api.js            # API client with JWT auth
│   │   ├── sealService.js    # Seal encrypt/decrypt
│   │   ├── components/       # React UI components
│   │   ├── hooks/            # useZkLogin, useFiles, useBalance
│   │   └── __tests__/        # Vitest test suites
│   └── .env
├── backend/
│   ├── Dockerfile
│   ├── main.go               # Server: MongoDB, JWT auth, Walrus proxy
│   ├── go.mod
│   └── .env
└── move/
    └── sources/
        └── identity_allowlist.move
```

## How It Works

1. **Login** — User authenticates with Google via Enoki's zkLogin flow, which derives a deterministic Sui address from the OAuth identity.

2. **Upload** — Files can be uploaded encrypted (Seal) or public. Encrypted files are encrypted client-side to the user's Sui address before upload. The backend proxies the encrypted bytes to Walrus and stores metadata in MongoDB.

3. **Download** — The backend fetches the blob from Walrus. If the file is encrypted, the frontend builds a Seal approval transaction, obtains key shares from Seal key servers, and decrypts locally.

4. **Send** — Encrypted files can be re-encrypted to a different Sui address. The file is decrypted with the sender's key, re-encrypted to the recipient's address, uploaded as a new blob, and saved to the recipient's file index.

## License

MIT
