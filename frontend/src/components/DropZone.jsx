import { useState, useRef } from "react";

export default function DropZone({ onFiles, uploading, encrypt, onEncryptChange }) {
  const [dragOver, setDragOver] = useState(false);
  const inputRef = useRef(null);

  function handleDrop(e) {
    e.preventDefault();
    setDragOver(false);
    if (e.dataTransfer.files?.length) onFiles(Array.from(e.dataTransfer.files));
  }

  function handleChange(e) {
    if (e.target.files?.length) {
      onFiles(Array.from(e.target.files));
      e.target.value = "";
    }
  }

  return (
    <div
      className={`drop-zone ${dragOver ? "drag-over" : ""} ${uploading ? "uploading" : ""}`}
      onClick={() => !uploading && inputRef.current?.click()}
      onDragOver={(e) => { e.preventDefault(); setDragOver(true); }}
      onDragLeave={() => setDragOver(false)}
      onDrop={handleDrop}
    >
      <input
        ref={inputRef}
        type="file"
        multiple
        className="hidden-input"
        onChange={handleChange}
      />
      {uploading ? (
        <div className="upload-status">
          <div className="spinner" />
          <span>{encrypt ? "Encrypting & uploading to Walrus…" : "Uploading to Walrus…"}</span>
        </div>
      ) : (
        <div className="upload-prompt">
          <span className="upload-icon">☁️</span>
          <span className="upload-text">Click or drag &amp; drop files here</span>
          <span className="upload-hint">
            {encrypt
              ? "Files are encrypted with Seal before upload — only you can view them"
              : "Files will be uploaded as-is — anyone with the link can view them"}
          </span>
          <label className="encrypt-toggle" onClick={(e) => e.stopPropagation()}>
            <span className="encrypt-toggle-label">
              {encrypt ? "🔒 Encrypted" : "🌐 Public"}
            </span>
            <span className="toggle-switch">
              <input
                type="checkbox"
                checked={encrypt}
                onChange={(e) => onEncryptChange(e.target.checked)}
              />
              <span className="toggle-slider" />
            </span>
          </label>
        </div>
      )}
    </div>
  );
}
