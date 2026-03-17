import { useState, useEffect, useCallback } from "react";
import { encryptFile, decryptFile } from "../sealService.js";
import { fetchFiles, saveFileEntry, uploadBlob, fetchBlob, deleteFileEntry } from "../api.js";

export function useFiles(session, getZkLoginSigner) {
  const [uploads, setUploads] = useState([]);
  const [uploading, setUploading] = useState(false);
  const [uploadError, setUploadError] = useState(null);
  const [downloading, setDownloading] = useState({});
  const [sending, setSending] = useState(false);
  const [sendError, setSendError] = useState(null);

  // Load file list when the user logs in
  useEffect(() => {
    if (!session?.address) return;
    fetchFiles(session.address)
      .then((files) =>
        setUploads(
          files.map((f) => ({ ...f, isEncrypted: f.isEncrypted ?? true, userAddress: session.address }))
        )
      )
      .catch(console.error);
  }, [session?.address]);

  const uploadFile = useCallback(
    async (file, encrypt = true) => {
      if (!session) return;
      setUploading(true);
      setUploadError(null);
      try {
        const buffer = await file.arrayBuffer();
        const bytes = encrypt
          ? await encryptFile(buffer, session.address)
          : new Uint8Array(buffer);
        const data = await uploadBlob(bytes, {
          mimeType: file.type || "application/octet-stream",
          filename: file.name,
          size: file.size,
        });
        const entry = {
          ...data,
          mimeType: file.type || "application/octet-stream",
          isEncrypted: encrypt,
          userAddress: session.address,
        };
        await saveFileEntry(session.address, entry);
        setUploads((prev) => [entry, ...prev]);
      } catch (err) {
        console.error("Upload error:", err);
        setUploadError(err.message);
      } finally {
        setUploading(false);
      }
    },
    [session]
  );

  const downloadFile = useCallback(
    async (file) => {
      setDownloading((prev) => ({ ...prev, [file.blobId]: true }));
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
        const blob = new Blob([bytes], { type: file.mimeType || "application/octet-stream" });
        const url = URL.createObjectURL(blob);
        const a = document.createElement("a");
        a.href = url;
        a.download = file.filename;
        a.click();
        URL.revokeObjectURL(url);
      } catch (err) {
        console.error("Download error:", err);
      } finally {
        setDownloading((prev) => ({ ...prev, [file.blobId]: false }));
      }
    },
    [getZkLoginSigner]
  );

  // onSuccess is called after a successful send, allowing the caller to close
  // the modal without the hook needing to know about modal state.
  const sendFile = useCallback(
    async (file, recipientAddress, onSuccess) => {
      setSending(true);
      setSendError(null);
      try {
        const raw = await fetchBlob(file.blobId);
        const signer = getZkLoginSigner();
        if (!signer) throw new Error("Not authenticated");
        const plainBytes = await decryptFile(new Uint8Array(raw), file.userAddress, signer);
        const encryptedBytes = await encryptFile(plainBytes, recipientAddress);
        const data = await uploadBlob(encryptedBytes, {
          mimeType: file.mimeType,
          filename: file.filename,
          size: file.size,
        });
        const entry = {
          ...data,
          mimeType: file.mimeType,
          isEncrypted: true,
          userAddress: recipientAddress,
        };
        await saveFileEntry(recipientAddress, entry);
        onSuccess?.();
      } catch (err) {
        setSendError(err.message);
      } finally {
        setSending(false);
      }
    },
    [getZkLoginSigner]
  );

  const importFile = useCallback(
    async (blobId, filename, isEncrypted) => {
      if (!session) return;
      const entry = {
        blobId,
        filename: filename || blobId,
        mimeType: "application/octet-stream",
        size: 0,
        status: "imported",
        isEncrypted,
        userAddress: session.address,
      };
      await saveFileEntry(session.address, entry);
      setUploads((prev) => [entry, ...prev]);
    },
    [session]
  );

  const deleteFile = useCallback(
    async (file) => {
      if (!session) return;
      await deleteFileEntry(session.address, file.blobId);
      setUploads((prev) => prev.filter((f) => f.blobId !== file.blobId));
    },
    [session]
  );

  return {
    uploads,
    uploading,
    uploadError,
    downloading,
    sending,
    sendError,
    uploadFile,
    downloadFile,
    sendFile,
    importFile,
    deleteFile,
  };
}
