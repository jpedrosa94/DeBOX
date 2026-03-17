/**
 * sealService unit tests
 *
 * All external dependencies (Seal SDK, Sui SDK, RPC) are mocked so tests
 * run offline in milliseconds.
 *
 * Key assertions:
 * - decryptFile() uses onlyTransactionKind: true when building the tx
 * - txBytes passed to client.decrypt() is the raw tx.build() output (no prefix hacks)
 * - No gas setup (setSender / setGasPayment etc.) is needed for a kind-only tx
 */
import { describe, it, expect, vi, beforeEach } from 'vitest'

// ─── hoisted constants (available inside vi.mock() factories) ─────────────────

const {
  FAKE_TX_BYTES,
  FAKE_ENCRYPTED,
  FAKE_PLAINTEXT,
  FAKE_ENCRYPTED_OBJECT,
  FAKE_PERSONAL_MSG,
  FAKE_SIGNATURE,
} = vi.hoisted(() => {
  // Mirrors the first bytes of a TransactionKind BCS produced by onlyTransactionKind: true:
  //   [PTx_kind=0x00][inputs_count=0x01][Pure_tag=0x00]...
  const FAKE_TX_BYTES = new Uint8Array(10)
  FAKE_TX_BYTES[0] = 0x00 // TransactionKind::ProgrammableTransaction tag
  FAKE_TX_BYTES[1] = 0x01 // inputs Vec length = 1
  FAKE_TX_BYTES[2] = 0x00 // CallArg::Pure tag

  return {
    FAKE_TX_BYTES,
    FAKE_ENCRYPTED:        new Uint8Array([0xca, 0xfe]),
    FAKE_PLAINTEXT:        new Uint8Array([0xde, 0xad, 0xbe, 0xef]),
    FAKE_ENCRYPTED_OBJECT: new Uint8Array([0x01, 0x02, 0x03]),
    FAKE_PERSONAL_MSG:     new Uint8Array([0x07, 0x08, 0x09]),
    FAKE_SIGNATURE:        'base64fakeSignature==',
  }
})

// ─── mock: @mysten/seal ───────────────────────────────────────────────────────

const mockDecrypt = vi.hoisted(() => vi.fn())
const mockEncrypt = vi.hoisted(() => vi.fn())
const mockCreate  = vi.hoisted(() => vi.fn())
const mockSetSig  = vi.hoisted(() => vi.fn())
const mockGetMsg  = vi.hoisted(() => vi.fn())

vi.mock('@mysten/seal', () => ({
  SealClient: vi.fn(function () {
    this.decrypt = mockDecrypt
    this.encrypt = mockEncrypt
  }),
  SessionKey: {
    create: mockCreate,
  },
}))

// ─── mock: @mysten/sui/jsonRpc ────────────────────────────────────────────────

vi.mock('@mysten/sui/jsonRpc', () => ({
  SuiJsonRpcClient: vi.fn(function () {
    this.core = {
      getObject: vi.fn().mockResolvedValue({ object: { version: '1' } }),
    }
  }),
  getJsonRpcFullnodeUrl: vi.fn().mockReturnValue('https://mock.sui.io'),
}))

// ─── mock: @mysten/sui/transactions ──────────────────────────────────────────

const mockPureVector = vi.hoisted(() => vi.fn().mockReturnValue({ kind: 'Input', index: 0 }))
const mockBuild      = vi.hoisted(() => vi.fn().mockResolvedValue(FAKE_TX_BYTES))

vi.mock('@mysten/sui/transactions', () => ({
  Transaction: vi.fn(function () {
    this.moveCall = vi.fn()
    this.build    = mockBuild
    // tx.pure is callable (plain arg) and has method tx.pure.vector()
    this.pure = Object.assign(vi.fn().mockReturnValue({ kind: 'Input', index: 0 }), {
      vector: mockPureVector,
    })
  }),
}))

// ─── mock: @mysten/bcs ───────────────────────────────────────────────────────

vi.mock('@mysten/bcs', () => ({
  fromHex: vi.fn().mockReturnValue(new Uint8Array(32).fill(0xbe)),
}))

// ─── test helpers ─────────────────────────────────────────────────────────────

const PACKAGE_ID = '0x1d1bc0019d623cc5d1c0e67e3f024a531197378c3ea32d34a36fb2f49541ebe9'
const USER_ADDR  = '0xf0cd89c152a8f3d37e90ec81b7e55b6cdaa4e1890d11ffd5bda44c22bfad1ac4'

const mockSigner = {
  signPersonalMessage: vi.fn().mockResolvedValue(FAKE_SIGNATURE),
}

const mockSessionKey = {
  getPersonalMessage:          mockGetMsg,
  setPersonalMessageSignature: mockSetSig,
}

// ─── beforeEach ───────────────────────────────────────────────────────────────

beforeEach(() => {
  vi.clearAllMocks()

  // Default SessionKey.create return value
  mockCreate.mockResolvedValue(mockSessionKey)
  mockGetMsg.mockReturnValue(FAKE_PERSONAL_MSG)
  mockSetSig.mockResolvedValue(undefined)

  // Default Seal client return values
  mockDecrypt.mockResolvedValue(FAKE_PLAINTEXT)
  mockEncrypt.mockResolvedValue({ encryptedObject: FAKE_ENCRYPTED_OBJECT })

  // Default tx.build return value
  mockBuild.mockResolvedValue(FAKE_TX_BYTES)

  // Default zkLogin signer
  mockSigner.signPersonalMessage.mockResolvedValue(FAKE_SIGNATURE)
})

// ─── dynamic import of sealService (after mocks are active) ──────────────────

let encryptFile, decryptFile
beforeAll(async () => {
  const svc = await import('../sealService.js')
  encryptFile = svc.encryptFile
  decryptFile  = svc.decryptFile
})

// ─── getSealClient ────────────────────────────────────────────────────────────

describe('getSealClient', () => {
  it('exports encryptFile and decryptFile functions', () => {
    expect(typeof encryptFile).toBe('function')
    expect(typeof decryptFile).toBe('function')
  })
})

// ─── encryptFile ──────────────────────────────────────────────────────────────

describe('encryptFile', () => {
  it('calls client.encrypt with correct packageId, id, and data', async () => {
    const buffer = new ArrayBuffer(4)
    new Uint8Array(buffer).set([1, 2, 3, 4])

    await encryptFile(buffer, USER_ADDR)

    expect(mockEncrypt).toHaveBeenCalledOnce()
    const [args] = mockEncrypt.mock.calls
    expect(args[0].packageId).toBe(PACKAGE_ID)
    expect(args[0].id).toBe(USER_ADDR)
    expect(args[0].data).toBeInstanceOf(Uint8Array)
  })

  it('returns the encryptedObject from client.encrypt', async () => {
    const result = await encryptFile(new ArrayBuffer(2), USER_ADDR)
    expect(result).toEqual(FAKE_ENCRYPTED_OBJECT)
  })
})

// ─── decryptFile ──────────────────────────────────────────────────────────────

describe('decryptFile', () => {
  it('creates a SessionKey with the user address and package ID', async () => {
    await decryptFile(FAKE_ENCRYPTED, USER_ADDR, mockSigner)
    expect(mockCreate).toHaveBeenCalledOnce()
    const [opts] = mockCreate.mock.calls
    expect(opts[0].address).toBe(USER_ADDR)
    expect(opts[0].packageId).toBe(PACKAGE_ID)
  })

  it('signs the personal message with the zkLogin signer', async () => {
    await decryptFile(FAKE_ENCRYPTED, USER_ADDR, mockSigner)
    expect(mockSigner.signPersonalMessage).toHaveBeenCalledWith(FAKE_PERSONAL_MSG)
  })

  it('sets the personal message signature on the session key', async () => {
    await decryptFile(FAKE_ENCRYPTED, USER_ADDR, mockSigner)
    expect(mockSetSig).toHaveBeenCalledWith(FAKE_SIGNATURE)
  })

  it('calls tx.pure.vector to build the id argument', async () => {
    await decryptFile(FAKE_ENCRYPTED, USER_ADDR, mockSigner)
    expect(mockPureVector).toHaveBeenCalledOnce()
    const [type] = mockPureVector.mock.calls[0]
    expect(type).toBe('u8')
  })

  it('builds the transaction with onlyTransactionKind: true', async () => {
    await decryptFile(FAKE_ENCRYPTED, USER_ADDR, mockSigner)
    expect(mockBuild).toHaveBeenCalledOnce()
    const [buildOpts] = mockBuild.mock.calls[0]
    expect(buildOpts.onlyTransactionKind).toBe(true)
  })

  it('calls client.decrypt with the encrypted data', async () => {
    await decryptFile(FAKE_ENCRYPTED, USER_ADDR, mockSigner)
    expect(mockDecrypt).toHaveBeenCalledOnce()
    const [args] = mockDecrypt.mock.calls
    expect(args[0].data).toEqual(FAKE_ENCRYPTED)
  })

  it('passes the sessionKey to client.decrypt', async () => {
    await decryptFile(FAKE_ENCRYPTED, USER_ADDR, mockSigner)
    const [args] = mockDecrypt.mock.calls
    expect(args[0].sessionKey).toBe(mockSessionKey)
  })

  it('returns the decrypted plaintext from client.decrypt', async () => {
    const result = await decryptFile(FAKE_ENCRYPTED, USER_ADDR, mockSigner)
    expect(result).toEqual(FAKE_PLAINTEXT)
  })

  // ── txBytes format ─────────────────────────────────────────────────────────

  describe('txBytes passed to client.decrypt', () => {
    let passedTxBytes

    beforeEach(async () => {
      await decryptFile(FAKE_ENCRYPTED, USER_ADDR, mockSigner)
      passedTxBytes = mockDecrypt.mock.calls[0][0].txBytes
    })

    it('txBytes is a Uint8Array', () => {
      expect(passedTxBytes).toBeInstanceOf(Uint8Array)
    })

    it('txBytes is the raw tx.build() output — no prefix added', () => {
      expect(passedTxBytes).toEqual(FAKE_TX_BYTES)
    })

    it('txBytes length equals tx.build() length (no extra bytes prepended)', () => {
      expect(passedTxBytes.length).toBe(FAKE_TX_BYTES.length)
    })

    it('txBytes[0] = 0x00 (TransactionKind::ProgrammableTransaction enum tag)', () => {
      expect(passedTxBytes[0]).toBe(0x00)
    })

    it('txBytes[1] = 0x01 (inputs Vec length = 1)', () => {
      // After SDK slice(1), key server receives ProgrammableTransaction bytes:
      // [0x01 (1 input), 0x00 (Pure tag), ...]
      expect(passedTxBytes[1]).toBe(0x01)
    })

    it('txBytes.slice(1)[0] = 0x01 (ProgrammableTransaction: 1 input — correct)', () => {
      // SDK strips byte 0 (PTx kind tag) before sending to key server.
      // Key server receives [0x01 (inputs_count=1), Pure_arg_bytes...]
      // and parses as ProgrammableTransaction → correct!
      expect(passedTxBytes.slice(1)[0]).toBe(0x01)
    })
  })
})
