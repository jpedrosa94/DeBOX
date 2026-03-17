import { useState } from "react";

export default function ImportForm({ onImport }) {
  const [blobId, setBlobId] = useState("");
  const [filename, setFilename] = useState("");
  const [isEncrypted, setIsEncrypted] = useState(false);
  const [loading, setLoading] = useState(false);

  async function handleSubmit(e) {
    e.preventDefault();
    const id = blobId.trim();
    if (!id) return;
    setLoading(true);
    try {
      await onImport(id, filename.trim() || id, isEncrypted);
      setBlobId("");
      setFilename("");
      setIsEncrypted(false);
    } finally {
      setLoading(false);
    }
  }

  return (
    <form className="import-form" onSubmit={handleSubmit}>
      <span className="import-label">Import by Blob ID</span>
      <input
        className="import-input"
        placeholder="Blob ID"
        value={blobId}
        onChange={(e) => setBlobId(e.target.value)}
        spellCheck={false}
      />
      <input
        className="import-input import-input-filename"
        placeholder="Filename (optional)"
        value={filename}
        onChange={(e) => setFilename(e.target.value)}
      />
      <label className="import-encrypted-toggle" title="Is this blob Seal-encrypted?">
        <input
          type="checkbox"
          checked={isEncrypted}
          onChange={(e) => setIsEncrypted(e.target.checked)}
        />
        <span>{isEncrypted ? "🔒" : "🌐"}</span>
      </label>
      <button className="import-btn" type="submit" disabled={!blobId.trim() || loading}>
        {loading ? "…" : "Import"}
      </button>
    </form>
  );
}
