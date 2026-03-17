import { useState } from "react";

export default function SendModal({ file, onSend, onClose, sending, error }) {
  const [recipient, setRecipient] = useState("");
  const isValid = /^0x[0-9a-fA-F]{64}$/.test(recipient);

  return (
    <div className="modal-overlay" onClick={onClose}>
      <div className="modal" onClick={(e) => e.stopPropagation()}>
        <div className="modal-header">
          <h3 className="modal-title">Send file to wallet</h3>
          <button className="modal-close" onClick={onClose}>✕</button>
        </div>
        <p className="modal-filename">{file.filename}</p>
        <p className="modal-description">
          The file will be decrypted and re-encrypted for the recipient's wallet.
          Only they will be able to open it.
        </p>
        <input
          className="modal-input"
          type="text"
          placeholder="Recipient Sui address (0x…)"
          value={recipient}
          onChange={(e) => setRecipient(e.target.value.trim())}
          disabled={sending}
        />
        {error && <p className="modal-error">⚠ {error}</p>}
        <div className="modal-actions">
          <button className="modal-cancel-btn" onClick={onClose} disabled={sending}>
            Cancel
          </button>
          <button
            className="modal-send-btn"
            onClick={() => onSend(recipient)}
            disabled={!isValid || sending}
          >
            {sending ? (
              <><div className="spinner-sm" /> Sending…</>
            ) : "Send"}
          </button>
        </div>
      </div>
    </div>
  );
}
