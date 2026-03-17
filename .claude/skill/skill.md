# Domain Expertise — Walrus Blob Storage Application

This document captures the full set of domain knowledge required to work on this application. It spans the Sui blockchain ecosystem, cryptographic protocols, decentralized storage, and the specific patterns and pitfalls discovered in this codebase.

---

## 1. Sui Blockchain Fundamentals

### Object Model
- Sui uses an **object-centric** model (not account-based like Ethereum). Every on-chain entity is an object with a unique ID, version, and digest.
- Objects are owned (by an address or another object) or shared.
- Gas payments reference specific coin objects: `{ objectId, version, digest }`.

### Addresses
- Sui addresses are **32 bytes** (64 hex characters), always prefixed with `0x`.
- Derived deterministically from the auth scheme — for zkLogin, the address depends on the OAuth provider, key claim (sub), salt, and audience (client ID).

### Transactions
- **Programmable Transaction Blocks (PTBs)**: Sui's native transaction format. A PTB contains `inputs[]` and `commands[]`, executed atomically.
- `Transaction` class from `@mysten/sui/transactions` is the builder.
- `tx.build()` returns full `TransactionData` BCS bytes (includes gas, sender, expiration).
- `tx.build({ onlyTransactionKind: true })` returns just the `TransactionKind` BCS bytes (no gas/sender envelope).

### BCS (Binary Canonical Serialization)
- Sui's wire format. All on-chain data and transactions are BCS-encoded.
- Vectors are length-prefixed: `[ULEB128 length][element0][element1]...`
- Enums are tag-prefixed: `[variant_index][variant_data]`
- `TransactionKind` BCS: `[0x00 = ProgrammableTransaction tag][ProgrammableTransaction struct]`
- `TransactionData` BCS: `[0x00 = V1 tag][TransactionKind][sender][gasData][expiration]`
- Import `fromHex` from `@mysten/bcs` to convert hex strings to byte arrays.
- Import `bcs` from `@mysten/sui/bcs` for parsing/serializing Sui-specific types.

### SuiClient
- `SuiJsonRpcClient` from `@mysten/sui/jsonRpc` — current JSON-RPC client (replaces old `SuiClient` from `@mysten/sui.js`).
- `getJsonRpcFullnodeUrl("testnet")` returns the public testnet RPC URL.
- Methods: `getBalance()`, `getObject()`, `dryRunTransactionBlock()`, etc.

---

## 2. zkLogin (Zero-Knowledge Login)

### Concept
- zkLogin maps an OAuth identity (e.g., Google `sub` claim) to a deterministic Sui address — no seed phrase needed.
- Uses a ZK proof to prove OAuth token ownership without revealing the JWT on-chain.
- Components: **ephemeral keypair** (short-lived, signs transactions), **salt** (links OAuth to address), **ZK proof** (proves JWT knowledge).

### Address Derivation
- `address = hash(iss, aud_or_addressSeed)` where `addressSeed = hash(salt, sub)`.
- **Different salt → different address** for the same Google account. Enoki manages a consistent salt per user.

### Prover Infrastructure
- `prover-dev.mystenlabs.com` — uses **devnet** SNARK verification key. Proofs fail on testnet.
- `prover.mystenlabs.com` — uses **testnet** key but requires whitelisted Google client ID.
- **Enoki** — manages its own prover compatible with testnet. This is what this app uses.

### Enoki (`@mysten/enoki`)
- `EnokiFlow` — manages the entire zkLogin lifecycle: OAuth redirect, salt retrieval, ZK proof generation, session management.
- Requires `apiKey` (Enoki API key) and a `store` for persistence (this app uses `localStorage`).
- `enokiFlow.createAuthorizationURL()` — generates Google OAuth redirect URL.
- `enokiFlow.handleAuthCallback(hash)` — processes the `#id_token=...` fragment after OAuth redirect.
- `enokiFlow.$zkLoginState.get()` — returns `{ address }` after successful auth.
- `enokiFlow.getKeypair({ network })` — returns an `EnokiKeypair` for signing.
- `EnokiFlow` is marked **deprecated** in favor of `registerEnokiWallets`, but is correct for non-dapp-kit integrations.

### Signer Compatibility
- `EnokiKeypair.signPersonalMessage(bytes)` returns `{ signature, bytes }` — **must destructure** to get plain base64 string.
- `EnokiKeypair.signTransaction(bytes)` — same return shape.
- Seal's `SessionKey` expects `signPersonalMessage` to return a **plain base64 string**, not `{ signature }`.
- The `getZkLoginSigner()` wrapper in `useZkLogin.js` handles this destructuring.

---

## 3. Seal Encryption (`@mysten/seal`)

### Architecture
- Identity-based encryption (IBE) built on Sui. Files are encrypted to an **identity** (in this app, a Sui address).
- Decryption requires threshold key shares from **key servers** — servers validate an on-chain policy (Move function) before releasing shares.
- **Threshold scheme**: t-of-n key servers must respond for decryption. This app uses 2-of-3.

### SealClient
- `new SealClient({ suiClient, serverConfigs, verifyKeyServers })` — initialized lazily.
- `serverConfigs`: array of `{ objectId, weight, aggregatorUrl? }` describing key servers.
- `verifyKeyServers: false` for testnet (set `true` for mainnet).
- `client.encrypt({ threshold, packageId, id, data })` — encrypts `data` (Uint8Array) to identity `id`.
- `client.decrypt({ data, sessionKey, txBytes })` — decrypts using session key + approval transaction.

### Key Servers (Testnet)
- **Mysten #1**: `0x73d05d62...` — independent server, no `aggregatorUrl`.
- **Mysten #2**: `0xf5d14a81...` — independent server, no `aggregatorUrl`.
- **Mysten Committee**: `0xb012378c...` — 3-of-5 nodes behind an aggregator, **requires `aggregatorUrl`** in config.
  - `aggregatorUrl: "https://seal-aggregator-testnet.mystenlabs.com"`
  - SDK's `retrieveKeyServers()` validates that committee servers have `aggregatorUrl`.
- **NodeInfra** (`0x5466b7...`) — **removed** because it sends `Access-Control-Allow-Origin: *, *` (double wildcard), which browsers reject.

### SessionKey
- `SessionKey.create({ address, packageId, ttlMin, suiClient })` — creates a session certificate.
- **Do NOT pass `signer`** — `SessionKey` would call `signer.getPublicKey().toSuiAddress()` which returns the ephemeral address, not the zkLogin address.
- `sessionKey.getPersonalMessage()` → bytes to sign with zkLogin signer.
- `sessionKey.setPersonalMessageSignature(signature)` → attach the zkLogin signature.

### Seal Approval Transaction (Critical)
The decryption flow requires building a "dry-run" transaction that calls `seal_approve` on the Move contract. Key servers validate this PTB to decide whether to release key shares.

**Correct approach:**
```js
const tx = new Transaction();
tx.moveCall({
  target: `${PACKAGE_ID}::identity_allowlist::seal_approve`,
  arguments: [tx.pure.vector("u8", fromHex(userAddress))],
});
const txBytes = await tx.build({ client: suiClient, onlyTransactionKind: true });
// Pass txBytes directly to client.decrypt()
```

**Why `onlyTransactionKind: true` is required:**
- Returns `TransactionKind` BCS: `[0x00 (PTx tag), ProgrammableTransaction...]`
- Seal SDK internally does `txBytes.slice(1)` before sending to key servers
- After slice: `[0x01 (inputs_count=1), inputs, commands...]` — correct `ProgrammableTransaction` format

**Why full `TransactionData` fails:**
- `tx.build()` returns `[0x00 (V1 tag), 0x00 (PTx kind), 0x01 (inputs), ...]`
- After SDK's `slice(1)`: `[0x00 (PTx kind), 0x01, ...]`
- Key server reads first byte as `inputs_count = 0` instead of `1` → **"Invalid PTB: Invalid BCS"**

**What NOT to do:**
- No `setSender()`, `setGasPrice()`, `setGasBudget()`, `setGasPayment()` — none needed with `onlyTransactionKind: true`.
- No manual prefix/slice manipulation — pass `txBytes` directly to `client.decrypt()`.

---

## 4. Walrus Blob Storage

### Concept
- Decentralized blob storage on Sui. Data is erasure-coded across storage nodes.
- Blobs are **immutable** and content-addressed by blob ID.
- Storage is paid for in **epochs** (time periods). This app stores for 5 epochs.

### API
- **Publisher** (`https://publisher.walrus-testnet.walrus.space`):
  - `PUT /v1/blobs?epochs=N` — upload blob, body is raw bytes, content-type `application/octet-stream`.
  - Response: `{ newlyCreated: { blobObject: { blobId } } }` or `{ alreadyCertified: { blobId } }`.
- **Aggregator** (`https://aggregator.walrus-testnet.walrus.space`):
  - `GET /v1/blobs/{blobId}` — download blob bytes.

### Upload Flow (This App)
1. Frontend optionally encrypts file bytes with Seal.
2. Frontend sends encrypted/raw bytes to backend via `POST /api/upload` (multipart form).
3. Backend `PUT`s bytes to Walrus publisher, extracts blob ID from response.
4. Backend returns blob ID + metadata to frontend.
5. Frontend saves file entry to backend index via `POST /api/files/{address}`.

### Download Flow
1. Frontend requests blob via `GET /api/blob/{blobId}` (proxied through backend).
2. Backend fetches from Walrus aggregator, streams back to frontend.
3. If encrypted: frontend decrypts with Seal using zkLogin signer.
4. Browser downloads the plaintext file.

---

## 5. Move Smart Contract (`identity_allowlist`)

### Purpose
Provides the on-chain policy that Seal key servers evaluate during decryption. The `seal_approve` function checks that the transaction sender matches the encryption identity.

### Contract Code
```move
module identity_allowlist::identity_allowlist {
    use sui::bcs;
    const ENotAuthorized: u64 = 0;

    public fun seal_approve(id: vector<u8>, ctx: &TxContext) {
        let sender_bytes = bcs::to_bytes(&ctx.sender());
        assert!(id == sender_bytes, ENotAuthorized);
    }
}
```

### Key Points
- `id` parameter: raw 32 bytes of the owner's Sui address (set at encryption time).
- `ctx.sender()`: the address of the user requesting decryption.
- If `id != sender_bytes`, aborts with `ENotAuthorized` — key servers refuse to release shares.
- BCS encoding of a Sui address is just the raw 32 bytes (no length prefix for fixed-size arrays).

### Deployment
- Published on Sui testnet at `0x1d1bc0019d623cc5d1c0e67e3f024a531197378c3ea32d34a36fb2f49541ebe9`.
- Upgrade capability object exists — contract can be upgraded.
- To redeploy: `sui client publish --gas-budget 100000000` from `move/` directory.

---

## 6. Go Backend Expertise

### Design Principles
- **stdlib only** — no external Go packages. Uses `net/http`, `encoding/json`, `sync`, `io`, `os`.
- Go 1.22+ routing with method+pattern: `mux.HandleFunc("POST /api/upload", handler)`.
- Path parameters via `r.PathValue("name")` (Go 1.22 feature).

### Data Persistence
- JSON file at `backend/data/files.json` — keyed by wallet address, values are arrays of `FileEntry`.
- Protected by `sync.Mutex` for concurrent access.
- `readDB()` / `writeDB()` handle file I/O + JSON marshal/unmarshal.

### CORS
- Custom `corsHandler` wraps the mux, sets `Access-Control-Allow-Origin: http://localhost:5173`.
- Handles `OPTIONS` preflight with 204 No Content.
- Allowed methods: GET, POST, DELETE, OPTIONS.

### Walrus Proxy Pattern
- Upload: receives multipart form from frontend, re-sends raw bytes to Walrus publisher via PUT.
- Download: fetches from Walrus aggregator, streams response to frontend.
- Blob responses cached with `Cache-Control: public, max-age=31536000, immutable` (blobs are immutable).

### Walrus Response Parsing
- Two possible response shapes: `newlyCreated.blobObject.blobId` or `alreadyCertified.blobId`.
- Status values: `"newly_created"`, `"already_certified"`, `"imported"` (for manual imports).

---

## 7. React Frontend Patterns

### Component Architecture
- Functional components with hooks throughout. No class components.
- Custom hooks in `hooks/`: `useZkLogin`, `useFiles`, `useBalance`.
- Single-file components in `components/`.
- `.jsx` extension for all React files (no TypeScript).

### State Management
- Local state with `useState` — no Redux, no Context (beyond what hooks provide).
- `useFiles` hook manages all file operations and file list state.
- `useZkLogin` hook manages auth session, signer, and login/logout.
- `useBalance` polls SUI balance every 30 seconds.

### File Operations
- **Upload**: `useFiles.uploadFile(file, encrypt)` — optionally encrypts, uploads to Walrus, saves to index.
- **Download**: `useFiles.downloadFile(file)` — fetches blob, optionally decrypts, triggers browser download.
- **Send**: `useFiles.sendFile(file, recipientAddress, onSuccess)` — decrypt → re-encrypt for recipient → upload → save.
- **Import**: `useFiles.importFile(blobId, filename, isEncrypted)` — add existing blob to user's index.
- **Delete**: `useFiles.deleteFile(file)` — removes from index (blob remains on Walrus — immutable).

### OAuth Flow
1. User clicks login → `useZkLogin.initLogin()` → redirects to Google.
2. Google redirects back with `#id_token=...` in URL hash.
3. `App.jsx` detects hash on mount → calls `handleCallback(hash)`.
4. Enoki processes JWT → generates ZK proof → derives Sui address.
5. Session stored in state + localStorage.

---

## 8. Testing Expertise (Vitest)

### Configuration
- `vitest` v4, config at `frontend/vitest.config.js`.
- `globals: true` — `describe`, `it`, `expect`, `vi` available without import.
- `environment: "node"` — no jsdom.
- `VITE_SEAL_PACKAGE_ID` set in config's `env` block for tests.

### Mocking Patterns

**Constructor mocks must use regular functions (not arrows):**
```js
// CORRECT — `this` binds to the new instance
vi.mock('@mysten/seal', () => ({
  SealClient: vi.fn(function () {
    this.decrypt = mockDecrypt;
    this.encrypt = mockEncrypt;
  }),
}));

// WRONG — arrow function: `this` is undefined
vi.mock('@mysten/seal', () => ({
  SealClient: vi.fn(() => { /* this.decrypt won't work */ }),
}));
```

**Mocking `tx.pure` (callable + has methods):**
```js
this.pure = Object.assign(
  vi.fn().mockReturnValue({ kind: 'Input', index: 0 }),
  { vector: mockPureVector }
);
```

**Hoisted constants for use inside `vi.mock()` factories:**
```js
const { FAKE_TX_BYTES } = vi.hoisted(() => {
  const FAKE_TX_BYTES = new Uint8Array(10);
  return { FAKE_TX_BYTES };
});
```

**Dynamic import after mocks are active:**
```js
let encryptFile, decryptFile;
beforeAll(async () => {
  const svc = await import('../sealService.js');
  encryptFile = svc.encryptFile;
  decryptFile = svc.decryptFile;
});
```

### Test Categories
- **Pure unit tests**: byte-level validation with fake data, no network.
- **Mock integration tests**: full function calls with all dependencies mocked.
- **Real SDK integration tests**: actual `tx.build()` calls (skipped in CI via `process.env.CI`).

---

## 9. Common Pitfalls & Gotchas

### Seal
1. **`onlyTransactionKind: true`** is mandatory. Full TransactionData → "Invalid PTB: Invalid BCS".
2. **Do not pass `signer` to `SessionKey.create()`** — it would use the ephemeral address.
3. **`signPersonalMessage` return value**: Enoki returns `{ signature, bytes }`, Seal expects plain string.
4. **Key server CORS**: NodeInfra sends double wildcard `*, *` — browsers block it. Only use Mysten servers.
5. **Committee server needs `aggregatorUrl`** — omitting it causes SDK validation error in `retrieveKeyServers()`.
6. **Old encrypted files**: files encrypted under [Mysten1, Mysten2, NodeInfra] can only decrypt with Mysten1+Mysten2 (threshold 2, only 2 of original 3 reachable).

### zkLogin
7. **Salt determines address**: changing salt (or switching from manual to Enoki) changes the derived address. Files uploaded under old address won't appear.
8. **Prover compatibility**: devnet prover proofs fail on testnet. Enoki handles this correctly.
9. **JWT profile extraction**: must handle base64url → base64 conversion (`-` → `+`, `_` → `/`).

### Walrus
10. **Blobs are immutable**: delete only removes from local index, not from Walrus.
11. **Response shapes differ**: `newlyCreated` vs `alreadyCertified` — must handle both.
12. **Epochs**: storage duration is epoch-based, not time-based. Currently set to 5.

### Go Backend
13. **Mutex scope**: `readDB` + `writeDB` must be inside the same `mu.Lock()` section for atomic read-modify-write.
14. **CORS origin hardcoded**: `http://localhost:5173` — must change for production.
15. **No auth on backend**: anyone can read/write file entries if they know the address. Security relies on Seal encryption.

### Testing
16. **Constructor mocks**: must use `vi.fn(function() {...})`, never arrow functions.
17. **`tx.pure` dual nature**: must be both callable and have `.vector()` method — use `Object.assign`.
18. **Module import timing**: `sealService.js` must be imported dynamically _after_ `vi.mock()` calls.

---

## 10. SDK Version Awareness

### Current Packages
| Package | Version | Notes |
|---------|---------|-------|
| `@mysten/sui` | ^2.7.0 | JSON-RPC client, Transaction builder, BCS |
| `@mysten/seal` | ^1.1.0 | Encryption/decryption, SessionKey, SealClient |
| `@mysten/enoki` | ^1.0.4 | zkLogin flow management |
| `@mysten/bcs` | (peer) | `fromHex`, `toHex` utilities |

### Import Paths (Current SDK)
```js
import { SuiJsonRpcClient, getJsonRpcFullnodeUrl } from "@mysten/sui/jsonRpc";
import { Transaction } from "@mysten/sui/transactions";
import { bcs } from "@mysten/sui/bcs";
import { fromHex } from "@mysten/bcs";
import { SealClient, SessionKey } from "@mysten/seal";
import { EnokiFlow } from "@mysten/enoki";
```

### Deprecated Patterns (Avoid)
- `SuiClient` from `@mysten/sui.js` → use `SuiJsonRpcClient` from `@mysten/sui/jsonRpc`.
- `new TransactionBlock()` → use `new Transaction()` from `@mysten/sui/transactions`.
- `bcs.vector(bcs.u8(), ...)` for pure args → use `tx.pure.vector("u8", bytes)`.
- `registerEnokiWallets` → only for dapp-kit; `EnokiFlow` is correct for this app's direct integration.

---

## 11. Security Model

### Threat Model
- **Backend is untrusted for confidentiality**: it only stores metadata and proxies encrypted blobs. It never sees plaintext.
- **Encryption at rest**: Seal encrypts before upload. Key servers enforce access policy.
- **Authentication**: zkLogin proves Google identity → Sui address. No passwords stored.
- **Authorization**: Move contract (`seal_approve`) ensures only the owner's address can decrypt.

### Limitations
- Backend has no authentication — any client can call API endpoints.
- File index (JSON) has no integrity protection — a compromised backend could alter metadata.
- Seal key server availability: if 2+ servers are down, decryption fails (threshold not met).
- Walrus blobs persist beyond epoch expiry as long as storage nodes retain them — no guaranteed deletion.
