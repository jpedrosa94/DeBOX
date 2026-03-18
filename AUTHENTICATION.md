# DeBOX Authentication Architecture

End-to-end authentication flow covering zkLogin (frontend), JWT verification (backend), and Seal encryption (on-chain access control).

## Overview

DeBOX uses three layers of identity that must all align for a user to access their files:

1. **Google Identity** — OAuth JWT with `sub` claim (Google user ID)
2. **Sui Address** — deterministic address derived from Google identity via Enoki zkLogin
3. **Encryption Identity** — files encrypted under the Sui address using Seal

```
Google OAuth (JWT)
       |
       v
  Enoki zkLogin ──> Sui Address (deterministic)
       |                   |
       v                   v
  Backend JWT auth    Seal encrypt/decrypt
  (JWKS + TOFU)       (on-chain policy)
```

---

## Frontend: zkLogin via Enoki

Authentication is handled by [`useZkLogin.js`](frontend/src/hooks/useZkLogin.js) using Mysten's [Enoki](https://portal.enoki.mystenlabs.com) SDK.

### Login Flow

```
User clicks Login
       |
       v
EnokiFlow.createAuthorizationURL()
       |
       v
Redirect to Google OAuth  ──>  User authorizes
       |
       v
Google redirects back with #id_token=<JWT>
       |
       v
App.jsx detects hash, clears it (prevents replay)
       |
       v
EnokiFlow.handleAuthCallback(hash)
       |
       v
Enoki internally:
  1. Extracts Google JWT from hash
  2. Generates salt (deterministic per user)
  3. Generates ZK proof (testnet-compatible SNARK key)
  4. Derives Sui address from (JWT, salt)
       |
       v
Session established: { address, jwt, email, name, picture }
```

### Key Design Decisions

- **Enoki manages salt + ZK proof generation** — the backend has no `/auth/salt` or `/auth/proof` endpoints. This avoids needing to whitelist the Google client ID with Mysten's prover.
- **JWT stored in React state** — `setAuthToken(jwt)` is called synchronously before `setSession()` to avoid race conditions with hooks that depend on the session.
- **Session persistence** — Enoki uses `localStorage` via a custom store adapter. On page refresh, `enokiFlow.getSession()` restores the session without re-authentication.
- **Google JWTs expire after 1 hour** — expired tokens cause 401s; the user must re-login.

### Session Restore (page refresh)

```javascript
// useZkLogin.js — runs on mount
enokiFlow.getSession().then((zkpSession) => {
  const { address } = enokiFlow.$zkLoginState.get();
  if (address) {
    setAuthToken(zkpSession?.jwt || null);  // set BEFORE setSession
    setSession({ address, jwt, ...profile });
  }
});
```

---

## Frontend: JWT Transmission

[`api.js`](frontend/src/api.js) manages a module-level auth token:

```javascript
let _authToken = null;
export function setAuthToken(token) { _authToken = token; }

function authHeaders(extra = {}) {
  const headers = { ...extra };
  if (_authToken) {
    headers["Authorization"] = `Bearer ${_authToken}`;
  }
  return headers;
}
```

Every authenticated API call includes the Google JWT as a Bearer token:

| Endpoint | Auth | Purpose |
|----------|------|---------|
| `POST /api/upload` | Required | Upload encrypted file to Walrus |
| `GET /api/files/{address}` | Required | List user's files |
| `POST /api/files/{address}` | Required | Save file metadata |
| `DELETE /api/files/{address}/{blobId}` | Required | Remove file metadata |
| `GET /api/blob/{blobId}` | **None** | Download blob (encrypted, content-addressed) |
| `GET /health` | **None** | Health check |

Blob download is unauthenticated because the data is encrypted — downloading it without the decryption key is useless.

---

## Backend: JWT Verification

[`auth.go`](backend/auth.go) verifies Google JWTs using RSA public keys fetched from Google's JWKS endpoint.

### JWKS Cache

```
GET https://www.googleapis.com/oauth2/v3/certs
       |
       v
Parse JSON Web Key Set → map[kid]*rsa.PublicKey
       |
       v
Cache in memory for 1 hour (thread-safe RWMutex)
```

The cache uses a **double-check locking pattern**: read lock to check freshness, then write lock only when refresh is needed.

### JWT Verification Steps

```go
func verifyGoogleJWT(tokenString string) (jwt.MapClaims, error)
```

1. Extract `kid` (key ID) from JWT header
2. Look up RSA public key from JWKS cache (auto-refresh if stale)
3. Verify RSA-SHA256 signature against Google's public key
4. Validate `iss` is `https://accounts.google.com`
5. Validate `aud` matches `GOOGLE_CLIENT_ID` env var
6. Validate `exp` > now (handled by `golang-jwt` library)
7. Return `sub` (Google user ID) and `email` claims

### Auth Middleware

```go
func authMiddleware(next http.HandlerFunc) http.HandlerFunc
```

Applied to protected endpoints. Extracts the Bearer token, verifies it, and injects `sub` and `email` into the request context for downstream handlers.

---

## Backend: Trust-on-First-Use (TOFU) Address Mapping

The backend needs to know which Google account owns which Sui address. Since Enoki derives the address client-side, the backend uses a **TOFU pattern** stored in MongoDB:

### `users` Collection

```json
{
  "sub": "google-uid-118234...",
  "address": "0x933f50ab092fc...",
  "email": "user@gmail.com",
  "createdAt": "2026-03-17T..."
}
```

Unique indexes on both `sub` and `address` enforce 1:1 mapping.

### Verification Flow

```
Request: GET /api/files/0x933f...
JWT sub: "google-uid-118234"
              |
              v
   Find user by address "0x933f..."
              |
     +--------+--------+
     |                  |
  Not found          Found
     |                  |
     v                  v
  Check if sub       sub matches?
  has different       |         |
  address            Yes        No
     |                |         |
  +--+--+          Allow    "address belongs
  |     |                   to another user"
 Yes    No
  |     |
Reject  Create mapping
"bound  (TOFU)
to different
address"
```

### Why TOFU?

- The backend cannot independently derive the Enoki address (it doesn't have the salt)
- On first authenticated request, the mapping is locked permanently
- Prevents address enumeration attacks — you can't list someone else's files
- Trade-off: if a user somehow gets a different Enoki address (unlikely), they lose access to old files

---

## Seal: On-Chain Encryption Access Control

[Seal](https://seal-docs.wal.app) provides identity-based encryption on Sui. Files are encrypted client-side before upload.

### Key Servers (2-of-3 threshold)

| Server | Type |
|--------|------|
| Mysten Labs #1 (`0x73d05d62...`) | Independent |
| Mysten Labs #2 (`0xf5d14a81...`) | Independent |
| Mysten Labs Committee (`0xb012378c...`) | 3-of-5 committee with aggregator |

Any 2 of 3 servers must agree to release key shares for decryption.

### Encryption

```javascript
// sealService.js
const encrypted = await client.encrypt({
  threshold: 2,
  packageId: SEAL_PACKAGE_ID,
  id: userAddress,        // encryption identity = Sui address
  data: plainBytes
});
```

The `id` parameter binds the ciphertext to the user's Sui address. Only someone who can prove they own that address can decrypt.

### Decryption

```
1. Create SessionKey (10-min ephemeral key)
       |
       v
2. Sign session certificate with zkLogin signer
   (proves ownership of Sui address via ZK proof)
       |
       v
3. Build approval transaction (PTB):
   Call seal_approve(userAddress) with onlyTransactionKind: true
       |
       v
4. Send to Seal key servers
       |
       v
5. Each key server dry-runs the transaction:
   - Executes seal_approve(userAddress) as sender = userAddress
   - Move contract asserts: sender == id
   - If match: return encrypted key share
       |
       v
6. Collect 2-of-3 key shares → reconstruct key → decrypt
```

### The Move Contract

[`identity_allowlist.move`](move/sources/identity_allowlist.move) implements the access control policy:

```move
public fun seal_approve(id: vector<u8>, ctx: &TxContext) {
    let sender_bytes = bcs::to_bytes(&ctx.sender());
    assert!(id == sender_bytes, ENotAuthorized);
}
```

This function is executed by key servers in dry-run mode. It enforces one rule: **the transaction sender must match the encryption identity**. Since the sender's identity is proven by the zkLogin signature (backed by the Google JWT), this creates an unbroken chain of trust from Google account to decryption capability.

### txBytes Format (Critical Detail)

```javascript
const txBytes = await tx.build({
  client: suiClient,
  onlyTransactionKind: true  // returns TransactionKind, not full TransactionData
});
```

- `onlyTransactionKind: true` produces `[0x00 (enum tag), ProgrammableTransaction...]`
- The Seal SDK internally calls `txBytes.slice(1)` to strip the enum tag
- Key servers then parse the raw `ProgrammableTransaction` BCS bytes
- Using full `TransactionData` (without `onlyTransactionKind`) causes "Invalid PTB: Invalid BCS" errors

---

## Complete Chain of Trust

```
Google Account (OAuth)
       |
       | Google JWT (signed by Google, verified by backend via JWKS)
       v
Backend: JWT sub claim
       |
       | TOFU mapping (sub → address, locked on first use)
       v
Sui Address (Enoki-derived)
       |
       | zkLogin signature (proves address ownership via ZK proof)
       v
Seal key servers
       |
       | dry-run seal_approve() — Move contract verifies sender == id
       v
Decryption key shares released
       |
       | 2-of-3 threshold reconstruction
       v
File decrypted client-side
```

### Security Properties

| Property | Mechanism |
|----------|-----------|
| Authentication | Google OAuth JWT, verified via RSA + JWKS |
| Address binding | TOFU mapping in MongoDB (permanent 1:1) |
| Authorization | `verifyAddressOwnership()` checks JWT sub → address |
| File privacy | Seal encryption, key release requires on-chain proof |
| Replay prevention | OAuth hash cleared before processing |
| Key server trust | 2-of-3 threshold — no single server can decrypt |
| Transport security | HTTPS for all external calls |
| Content addressing | Blobs are immutable, identified by hash |

---

## Sequence Diagram: File Upload

```
Browser                Frontend              Backend             Walrus          MongoDB
   |                      |                     |                   |               |
   |-- drop file -------->|                     |                   |               |
   |                      |-- encrypt(file, addr)                   |               |
   |                      |   (Seal client-side)                    |               |
   |                      |                     |                   |               |
   |                      |-- POST /api/upload --|                  |               |
   |                      |   [Bearer JWT]       |                  |               |
   |                      |                      |-- verify JWT     |               |
   |                      |                      |   (JWKS/RSA)     |               |
   |                      |                      |                  |               |
   |                      |                      |-- PUT /v1/blobs--|               |
   |                      |                      |                  |               |
   |                      |                      |<-- { blobId } ---|               |
   |                      |<-- { blobId, url } --|                  |               |
   |                      |                      |                  |               |
   |                      |-- POST /api/files/addr                  |               |
   |                      |   [Bearer JWT]       |                  |               |
   |                      |                      |-- verify JWT     |               |
   |                      |                      |-- verify TOFU--->|               |
   |                      |                      |-- insertOne ---->|-------------->|
   |                      |<-- { entry } --------|                  |               |
   |<-- show file card ---|                      |                  |               |
```

## Sequence Diagram: File Download (Encrypted)

```
Browser                Frontend              Backend             Walrus       Seal Key Servers
   |                      |                     |                   |               |
   |-- click download --->|                     |                   |               |
   |                      |-- GET /api/blob/id -|                   |               |
   |                      |   (no auth)         |-- GET /v1/blobs--|               |
   |                      |                     |<-- encrypted ----|               |
   |                      |<-- encrypted bytes -|                   |               |
   |                      |                     |                   |               |
   |                      |-- SessionKey.create()                   |               |
   |                      |-- sign(zkLoginSigner)                   |               |
   |                      |-- build seal_approve PTB                |               |
   |                      |                                         |               |
   |                      |-- decrypt(txBytes, encryptedBytes) -----|-------------->|
   |                      |                                         |  dry-run PTB  |
   |                      |                                         |  seal_approve |
   |                      |                                         |  assert ok    |
   |                      |<-- key shares (2 of 3) ----------------|---------------|
   |                      |                                         |               |
   |                      |-- reconstruct key + decrypt locally     |               |
   |<-- download file ----|                                         |               |
```
