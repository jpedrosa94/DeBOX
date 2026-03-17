export function formatBytes(bytes) {
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`;
  return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
}

export function fileTypeIcon(mimeType = "") {
  if (mimeType.startsWith("image/")) return "🖼️";
  if (mimeType === "application/pdf") return "📕";
  if (mimeType.startsWith("video/")) return "🎬";
  if (mimeType.startsWith("audio/")) return "🎵";
  if (mimeType.includes("word") || mimeType.includes("document")) return "📝";
  if (mimeType.includes("spreadsheet") || mimeType.includes("excel")) return "📊";
  if (mimeType.includes("zip") || mimeType.includes("compressed")) return "📦";
  return "📄";
}

export function shortAddress(addr) {
  if (!addr) return "";
  return `${addr.slice(0, 6)}…${addr.slice(-4)}`;
}
