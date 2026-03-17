/**
 * PTB byte-format unit tests
 *
 * These tests document the correct way to build txBytes for the Seal SDK:
 *
 *   tx.build({ client: suiClient, onlyTransactionKind: true })
 *
 * With onlyTransactionKind: true, tx.build() returns TransactionKind BCS bytes:
 *   [0x00 (ProgrammableTransaction tag), ProgrammableTransaction struct...]
 *
 * The Seal SDK internally calls txBytes.slice(1) before sending the PTB to each
 * key server. After slice(1), the server receives raw ProgrammableTransaction bytes
 * (inputs_count, inputs[], commands_count, commands[]) and validates that
 * seal_approve is called with the correct id.
 *
 * Contrast: tx.build() WITHOUT onlyTransactionKind wraps the PTx in a full
 * TransactionData envelope. slice(1) then strips only the TransactionData::V1 tag,
 * leaving a byte stream that starts with the TransactionKind tag — which lands in
 * the wrong position when the server parses ProgrammableTransaction → "Invalid BCS".
 *
 * Integration tests (marked skipOffline) make real @mysten/sui calls and are
 * skipped in CI environments where process.env.CI is truthy.
 */
import { describe, it, expect } from 'vitest'
import { Transaction } from '@mysten/sui/transactions'
import { bcs } from '@mysten/sui/bcs'
import { fromHex } from '@mysten/bcs'
import { SuiJsonRpcClient, getJsonRpcFullnodeUrl } from '@mysten/sui/jsonRpc'

const PACKAGE_ID = '0x1d1bc0019d623cc5d1c0e67e3f024a531197378c3ea32d34a36fb2f49541ebe9'
const USER_ADDR  = '0xf0cd89c152a8f3d37e90ec81b7e55b6cdaa4e1890d11ffd5bda44c22bfad1ac4'

// ─── pure unit tests (no network, no mocks) ───────────────────────────────────

describe('onlyTransactionKind — pure logic', () => {
  /**
   * Minimal fake of what tx.build({ onlyTransactionKind: true }) returns:
   *   byte 0 : 0x00  → TransactionKind::ProgrammableTransaction tag
   *   byte 1 : 0x01  → inputs Vec length = 1
   *   byte 2 : 0x00  → CallArg::Pure tag
   */
  function fakeKindBytes() {
    const b = new Uint8Array(50)
    b[0] = 0x00 // TransactionKind::ProgrammableTransaction
    b[1] = 0x01 // 1 input
    b[2] = 0x00 // Pure
    return b
  }

  it('slice(1) strips the TransactionKind tag, exposing ProgrammableTransaction bytes', () => {
    const kb = fakeKindBytes()
    const afterSlice = kb.slice(1)
    // First byte after slice is the inputs_count = 1 (correct for ProgrammableTransaction)
    expect(afterSlice[0]).toBe(0x01)
  })

  it('kind bytes[0] = 0x00 (ProgrammableTransaction kind tag)', () => {
    expect(fakeKindBytes()[0]).toBe(0x00)
  })

  it('kind bytes[1] = 0x01 (1 input in ProgrammableTransaction)', () => {
    expect(fakeKindBytes()[1]).toBe(0x01)
  })

  it('full TransactionData bytes: slice(1) gives wrong result for key server', () => {
    // tx.build() WITHOUT onlyTransactionKind returns:
    //   [0x00 (V1 tag), 0x00 (PTx kind), 0x01 (inputs), ...]
    const fullTxData = new Uint8Array([0x00, 0x00, 0x01, 0x00])
    const afterSlice = fullTxData.slice(1)
    // Server tries to parse as ProgrammableTransaction:
    // byte 0 = 0x00 → inputs_count = 0  (WRONG, should be 1)
    // → "Invalid BCS"
    expect(afterSlice[0]).toBe(0x00) // misread as inputs_count=0, not 1
    expect(afterSlice[0]).not.toBe(0x01)
  })
})

// ─── integration tests (real tx.build() with both modes) ─────────────────────

const skipOffline = !!process.env.CI

describe('tx.build() byte format — real SDK', { skip: skipOffline }, () => {
  let kindBytes     // from onlyTransactionKind: true
  let fullTxBytes   // from default (onlyTransactionKind: false)
  let suiClient

  beforeAll(async () => {
    suiClient = new SuiJsonRpcClient({ url: getJsonRpcFullnodeUrl('testnet') })

    // ── kind-only tx (correct Seal approach) ──────────────────────────────────
    const tx = new Transaction()
    tx.moveCall({
      target: `${PACKAGE_ID}::identity_allowlist::seal_approve`,
      arguments: [tx.pure.vector('u8', fromHex(USER_ADDR))],
    })
    kindBytes = await tx.build({ client: suiClient, onlyTransactionKind: true })

    // ── full TransactionData tx (wrong Seal approach — needs gas) ─────────────
    const tx2 = new Transaction()
    tx2.moveCall({
      target: `${PACKAGE_ID}::identity_allowlist::seal_approve`,
      arguments: [tx2.pure.vector('u8', fromHex(USER_ADDR))],
    })
    tx2.setSender(USER_ADDR)
    tx2.setGasOwner(USER_ADDR)
    tx2.setGasPrice(1000)
    tx2.setGasBudget(10_000_000)
    tx2.setGasPayment([{
      objectId: '0x0000000000000000000000000000000000000000000000000000000000000000',
      version: 0,
      digest: '11111111111111111111111111111111',
    }])
    fullTxBytes = await tx2.build({ client: suiClient })
  })

  // ── kindBytes correctness ──────────────────────────────────────────────────

  it('onlyTransactionKind: kind bytes are a non-empty Uint8Array', () => {
    expect(kindBytes).toBeInstanceOf(Uint8Array)
    expect(kindBytes.length).toBeGreaterThan(0)
  })

  it('onlyTransactionKind: byte[0] = 0x00 (ProgrammableTransaction kind tag)', () => {
    expect(kindBytes[0]).toBe(0x00)
  })

  it('onlyTransactionKind: byte[1] = 0x01 (1 input in the PTB)', () => {
    expect(kindBytes[1]).toBe(0x01)
  })

  it('onlyTransactionKind: slice(1) → inputs_count = 1 (what key server receives)', () => {
    expect(kindBytes.slice(1)[0]).toBe(0x01)
  })

  it('onlyTransactionKind: kind bytes do NOT parse as TransactionData (no wrapper)', () => {
    // This is expected — kindBytes are just TransactionKind, not the full TransactionData
    expect(() => bcs.TransactionData.parse(kindBytes)).toThrow()
  })

  it('onlyTransactionKind: kind bytes parse as TransactionKind', () => {
    const parsed = bcs.TransactionKind.parse(kindBytes)
    expect(parsed.ProgrammableTransaction).toBeDefined()
  })

  it('onlyTransactionKind: parsed PTx has 1 input (Pure address bytes)', () => {
    const { ProgrammableTransaction: ptx } = bcs.TransactionKind.parse(kindBytes)
    expect(ptx.inputs).toHaveLength(1)
    expect(ptx.inputs[0].Pure).toBeDefined()
  })

  it('onlyTransactionKind: parsed PTx has 1 command (MoveCall)', () => {
    const { ProgrammableTransaction: ptx } = bcs.TransactionKind.parse(kindBytes)
    expect(ptx.commands).toHaveLength(1)
    expect(ptx.commands[0].MoveCall).toBeDefined()
  })

  it('onlyTransactionKind: MoveCall targets identity_allowlist::seal_approve', () => {
    const { ProgrammableTransaction: ptx } = bcs.TransactionKind.parse(kindBytes)
    const call = ptx.commands[0].MoveCall
    expect(call.module).toBe('identity_allowlist')
    expect(call.function).toBe('seal_approve')
  })

  // ── full TransactionData contrast ──────────────────────────────────────────

  it('full tx: fullTxBytes[0] = 0x00 (TransactionData::V1 tag)', () => {
    expect(fullTxBytes[0]).toBe(0x00)
  })

  it('full tx: fullTxBytes[1] = 0x00 (TransactionKind::ProgrammableTransaction)', () => {
    expect(fullTxBytes[1]).toBe(0x00)
  })

  it('full tx: slice(1)[0] = 0x00 — server misreads as inputs_count=0 → Invalid BCS', () => {
    // This is WHY the full-tx approach fails with Seal key servers:
    // After SDK's slice(1), the server sees byte 0 = 0x00 = inputs_count=0
    // but we actually have 1 input → BCS parse error.
    expect(fullTxBytes.slice(1)[0]).toBe(0x00)
  })
})
