/// Identity-based access control for Seal.
///
/// seal_approve grants decryption to the exact Sui address that was used
/// as the encryption identity. This means only the file owner (the address
/// used at upload time) can decrypt and view their files.
module identity_allowlist::identity_allowlist {
    use sui::bcs;

    const ENotAuthorized: u64 = 0;

    /// Called by Seal key servers (via dry-run) to decide whether to release
    /// key shares for decryption.
    ///
    /// `id`  — the raw 32 bytes of the owner's Sui address, set at upload time.
    /// `ctx` — provides the transaction sender (the user requesting decryption).
    ///
    /// Aborts with ENotAuthorized if the caller's address does not match `id`.
    public fun seal_approve(id: vector<u8>, ctx: &TxContext) {
        let sender_bytes = bcs::to_bytes(&ctx.sender());
        assert!(id == sender_bytes, ENotAuthorized);
    }
}
