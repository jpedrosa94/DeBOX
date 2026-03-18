/**
 * Seal + Sui client service
 *
 * Handles lazy initialization of the SealClient and exposes
 * `encryptFile` / `decryptFile` helpers used by the upload flow.
 */
import { SealClient, SessionKey } from "@mysten/seal";
import { SuiJsonRpcClient, getJsonRpcFullnodeUrl } from "@mysten/sui/jsonRpc";
import { Transaction } from "@mysten/sui/transactions";
import { fromHex } from "@mysten/bcs";

// ─── Configuration ─────────────────────────────────────────────────────────
// Set VITE_SEAL_PACKAGE_ID in frontend/.env after deploying move/
const SEAL_PACKAGE_ID = import.meta.env.VITE_SEAL_PACKAGE_ID;

export const suiClient = new SuiJsonRpcClient({
  url: getJsonRpcFullnodeUrl("testnet"),
});

// ─── Lazy SealClient ────────────────────────────────────────────────────────
let _sealClient = null;

// Verified testnet key servers — https://seal-docs.wal.app/Pricing#verified-key-servers
//
// Independent servers (URL fetched from on-chain object, no aggregatorUrl needed):
//   Mysten Labs #1  0x73d05d62c18d9374e3ea529e8e0ed6161da1a141a94d3f76ae3fe4e99356db75
//   Mysten Labs #2  0xf5d14a81a982144ae441cd7d64b09027f116a468bd36e7eca494f750591623c8
//
// Committee server (3-of-5 nodes behind an aggregator, aggregatorUrl required):
//   0xb012378c9f3799fb5b1a7083da74a4069e3c3f1c93de0b27212a5799ce1e1e98
//     aggregatorUrl: https://seal-aggregator-testnet.mystenlabs.com
//
// NOTE: NodeInfra (0x5466b7...) removed — their server sends a duplicate CORS wildcard
// header (`Access-Control-Allow-Origin: *, *`) which browsers reject, making it
// unusable from a web app.

const SERVER_CONFIGS = [
  // Mysten Labs — independent
  {
    objectId: "0x73d05d62c18d9374e3ea529e8e0ed6161da1a141a94d3f76ae3fe4e99356db75",
    weight: 1,
  },
  // Mysten Labs — independent
  {
    objectId: "0xf5d14a81a982144ae441cd7d64b09027f116a468bd36e7eca494f750591623c8",
    weight: 1,
  },
  // Mysten Labs — committee (3-of-5 nodes behind aggregator, no browser CORS issues)
  {
    objectId: "0xb012378c9f3799fb5b1a7083da74a4069e3c3f1c93de0b27212a5799ce1e1e98",
    weight: 1,
    aggregatorUrl: "https://seal-aggregator-testnet.mystenlabs.com",
  },
];

// Threshold = minimum weight required to decrypt.
// 2-of-3: tolerates 1 server being offline.
const THRESHOLD = 2;

async function getSealClient() {
  if (_sealClient) return _sealClient;

  if (!SEAL_PACKAGE_ID) {
    throw new Error(
      "VITE_SEAL_PACKAGE_ID is not set. Deploy the Move contract first (see move/) and add the package ID to frontend/.env"
    );
  }

  _sealClient = new SealClient({
    suiClient,
    serverConfigs: SERVER_CONFIGS,
    verifyKeyServers: false, // set to true on mainnet
  });

  return _sealClient;
}


// ─── Encrypt ────────────────────────────────────────────────────────────────
/**
 * Encrypt `fileBuffer` (ArrayBuffer) with Seal, keyed to `userAddress`.
 * Only the owner (same address) can decrypt.
 * Returns a Uint8Array of encrypted bytes ready to upload to Walrus.
 */
export async function encryptFile(fileBuffer, userAddress) {
  const client = await getSealClient();
  const id = userAddress;

  const { encryptedObject } = await client.encrypt({
    threshold: THRESHOLD,
    packageId: SEAL_PACKAGE_ID,
    id,
    data: new Uint8Array(fileBuffer),
  });

  return encryptedObject; // Uint8Array
}

// ─── Decrypt ────────────────────────────────────────────────────────────────
/**
 * Decrypt `encryptedBytes` (Uint8Array) using the caller's zkLogin signer.
 * Builds a dry-run approval transaction that calls `seal_approve` on-chain.
 * The Seal key servers validate the transaction and return key shares.
 * Returns a Uint8Array of the original plaintext bytes.
 */
export async function decryptFile(encryptedBytes, userAddress, zkLoginSigner) {
  const client = await getSealClient();

  // Do NOT pass signer here — SessionKey validates signer.getPublicKey().toSuiAddress()
  // which would be the ephemeral address, not the zkLogin address.
  const sessionKey = await SessionKey.create({
    address: userAddress,
    packageId: SEAL_PACKAGE_ID,
    ttlMin: 10,
    suiClient,
  });

  // Sign the session certificate with the full zkLogin signature.
  // signPersonalMessage returns a base64 string directly (not { signature }).
  const signature = await zkLoginSigner.signPersonalMessage(
    sessionKey.getPersonalMessage()
  );
  await sessionKey.setPersonalMessageSignature(signature);

  // Build the approval transaction — key servers validate this to check policy.
  // onlyTransactionKind: true serializes just the TransactionKind (ProgrammableTransaction),
  // which is what the Seal key servers expect after the SDK strips the leading enum tag
  // via slice(1) internally before sending the PTB.
  const tx = new Transaction();
  tx.moveCall({
    target: `${SEAL_PACKAGE_ID}::identity_allowlist::seal_approve`,
    arguments: [
      tx.pure.vector("u8", fromHex(userAddress)),
    ],
  });
  const txBytes = await tx.build({ client: suiClient, onlyTransactionKind: true });

  return await client.decrypt({
    data: encryptedBytes,
    sessionKey,
    txBytes,
  });
}
