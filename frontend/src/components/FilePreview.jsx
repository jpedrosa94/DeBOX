import { useState, useEffect } from "react";
import { decryptFile } from "../sealService.js";
import { fetchBlob } from "../api.js";
import { fileTypeIcon } from "../utils.js";

export default function FilePreview({ file, getZkLoginSigner }) {
  const [objectUrl, setObjectUrl] = useState(null);
  const [loadState, setLoadState] = useState("loading"); // loading | ready | error

  const isImage = file.mimeType?.startsWith("image/");
  const isPdf = file.mimeType === "application/pdf";
  const isVideo = file.mimeType?.startsWith("video/");
  const isAudio = file.mimeType?.startsWith("audio/");
  const hasPreview = isImage || isPdf || isVideo || isAudio;

  useEffect(() => {
    if (!hasPreview) {
      setLoadState("ready");
      return;
    }

    let url;
    (async () => {
      try {
        const raw = await fetchBlob(file.blobId);

        let bytes;
        if (file.isEncrypted) {
          const signer = getZkLoginSigner();
          if (!signer) throw new Error("Not authenticated");
          bytes = await decryptFile(new Uint8Array(raw), file.userAddress, signer);
        } else {
          bytes = new Uint8Array(raw);
        }

        const blob = new Blob([bytes], { type: file.mimeType });
        url = URL.createObjectURL(blob);
        setObjectUrl(url);
        setLoadState("ready");
      } catch (err) {
        console.error("Preview error:", err);
        setLoadState("error");
      }
    })();

    return () => {
      if (url) URL.revokeObjectURL(url);
    };
  }, [file.blobId, file.mimeType, file.isEncrypted, file.userAddress, hasPreview, getZkLoginSigner]);

  if (loadState === "loading") {
    return (
      <div className="preview-box preview-loading">
        <div className="spinner" />
        <span className="preview-loading-text">
          {file.isEncrypted ? "Decrypting…" : "Loading preview…"}
        </span>
      </div>
    );
  }

  if (loadState === "error" || (!objectUrl && hasPreview)) {
    return (
      <div className="preview-box preview-generic">
        <span className="file-icon">{fileTypeIcon(file.mimeType)}</span>
        <span className="preview-label">Preview unavailable</span>
      </div>
    );
  }

  if (!hasPreview) {
    return (
      <div className="preview-box preview-generic">
        <span className="file-icon">{fileTypeIcon(file.mimeType)}</span>
        <span className="preview-label">No preview for this file type</span>
      </div>
    );
  }

  return (
    <div className="preview-box">
      {isImage && (
        <img src={objectUrl} alt={file.filename} className="preview-image" />
      )}
      {isPdf && (
        <object data={objectUrl} type="application/pdf" className="preview-iframe">
          <div className="preview-generic">
            <span className="file-icon">📕</span>
            <span className="preview-label">PDF preview unavailable</span>
          </div>
        </object>
      )}
      {isVideo && <video src={objectUrl} controls className="preview-video" />}
      {isAudio && (
        <div className="preview-audio-wrapper">
          <span className="file-icon" style={{ fontSize: "2rem" }}>🎵</span>
          <audio src={objectUrl} controls className="preview-audio" />
        </div>
      )}
    </div>
  );
}
