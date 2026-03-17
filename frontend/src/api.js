let _authToken = null;

export function setAuthToken(token) {
  _authToken = token;
}

function authHeaders(extra = {}) {
  const headers = { ...extra };
  if (_authToken) {
    headers["Authorization"] = `Bearer ${_authToken}`;
  }
  return headers;
}

export async function fetchFiles(address) {
  const r = await fetch(`/api/files/${address}`, {
    headers: authHeaders(),
  });
  if (!r.ok) throw new Error(`Server responded ${r.status}`);
  return r.json();
}

export async function saveFileEntry(address, entry) {
  const r = await fetch(`/api/files/${address}`, {
    method: "POST",
    headers: authHeaders({ "Content-Type": "application/json" }),
    body: JSON.stringify(entry),
  });
  if (!r.ok) throw new Error(`Server responded ${r.status}`);
  return r.json();
}

export async function uploadBlob(encryptedBytes, { mimeType, filename, size }) {
  const form = new FormData();
  form.append(
    "file",
    new Blob([encryptedBytes], { type: "application/octet-stream" }),
    filename
  );
  form.append("mimeType", mimeType);
  form.append("filename", filename);
  form.append("originalSize", String(size));
  const r = await fetch("/api/upload", {
    method: "POST",
    headers: authHeaders(),
    body: form,
  });
  const data = await r.json();
  if (!r.ok) throw new Error(data.error || "Upload failed");
  return data;
}

export async function deleteFileEntry(address, blobId) {
  const r = await fetch(`/api/files/${address}/${blobId}`, {
    method: "DELETE",
    headers: authHeaders(),
  });
  if (!r.ok) throw new Error(`Server responded ${r.status}`);
  return r.json();
}

export async function fetchBlob(blobId) {
  const r = await fetch(`/api/blob/${blobId}`);
  if (!r.ok) throw new Error("Blob fetch failed");
  return r.arrayBuffer();
}
