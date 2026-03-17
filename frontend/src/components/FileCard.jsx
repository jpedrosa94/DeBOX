import { formatBytes } from "../utils.js";
import FilePreview from "./FilePreview.jsx";

export default function FileCard({ file, onDownload, onSend, onDelete, downloading, getZkLoginSigner }) {
  return (
    <div className="upload-card">
      <FilePreview file={file} getZkLoginSigner={getZkLoginSigner} />
      <div className="card-info">
        <div className="card-filename-row">
          <p className="card-filename" title={file.filename}>
            {file.filename}
          </p>
          {file.isEncrypted && (
            <span className="lock-badge" title="Seal encrypted">🔒</span>
          )}
        </div>
        <p className="card-meta">
          {formatBytes(file.size)}
          <span className={`status-badge ${file.status}`}>
            {file.status === "newly_created" ? "New" : "Cached"}
          </span>
        </p>
        <div className="card-blob-id">
          <span className="blob-label">Blob ID</span>
          <a
            href={`https://aggregator.walrus-testnet.walrus.space/v1/blobs/${file.blobId}`}
            target="_blank"
            rel="noopener noreferrer"
            className="blob-id"
            title="Open raw blob on Walrus"
          >
            {file.blobId}
          </a>
        </div>
        <div className="card-actions">
          <button
            className="action-btn download-btn"
            onClick={onDownload}
            disabled={downloading}
            title="Download"
          >
            {downloading ? (
              <><div className="spinner-sm" /> Decrypting…</>
            ) : "⬇ Download"}
          </button>
          <button
            className="action-btn send-btn"
            onClick={onSend}
            title="Send to another wallet"
          >
            ↗ Send
          </button>
          <button
            className="action-btn delete-btn"
            onClick={() => window.confirm(`Delete "${file.filename}"?`) && onDelete()}
            title="Remove from your list"
          >
            🗑
          </button>
        </div>
      </div>
    </div>
  );
}
