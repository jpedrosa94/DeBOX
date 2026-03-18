import { useState, useEffect } from "react";
import { useZkLogin } from "./hooks/useZkLogin.js";
import { useBalance } from "./hooks/useBalance.js";
import { useFiles } from "./hooks/useFiles.js";

import LoginPage from "./components/LoginPage.jsx";
import Header from "./components/Header.jsx";
import DropZone from "./components/DropZone.jsx";
import FileCard from "./components/FileCard.jsx";
import SendModal from "./components/SendModal.jsx";
import ImportForm from "./components/ImportForm.jsx";

export default function App() {
  const { session, loading: authLoading, error: authError, initLogin, handleCallback, logout, getZkLoginSigner } =
    useZkLogin();
  const balance = useBalance(session?.address);
  const { uploads, uploading, uploadError, downloading, sending, sendError, uploadFile, downloadFile, sendFile, importFile, deleteFile } =
    useFiles(session, getZkLoginSigner);
  const [sendModal, setSendModal] = useState(null); // { file } | null
  const [encrypt, setEncrypt] = useState(true);

  // Handle OAuth redirect — clear hash immediately to prevent token replay,
  // then let Enoki process the saved hash string
  useEffect(() => {
    const hash = window.location.hash;
    if (hash.includes("id_token=")) {
      window.history.replaceState(null, "", window.location.pathname);
      handleCallback(hash).catch(console.error);
    }
  }, [handleCallback]);


  if (!session) {
    return <LoginPage onLogin={initLogin} loading={authLoading} error={authError} />;
  }

  return (
    <div className="app">
      <Header session={session} balance={balance} onLogout={logout} />

      <main className="main">
        <DropZone
          onFiles={(files) => files.forEach((f) => uploadFile(f, encrypt))}
          uploading={uploading}
          encrypt={encrypt}
          onEncryptChange={setEncrypt}
        />

        <ImportForm onImport={importFile} />

        {uploadError && (
          <div className="upload-error-banner">⚠ {uploadError}</div>
        )}

        {uploads.length > 0 && (
          <section className="uploads-section">
            <h2 className="section-title">Your Files ({uploads.length})</h2>
            <div className="uploads-grid">
              {uploads.map((file) => (
                <FileCard
                  key={file.blobId}
                  file={file}
                  onDownload={() => downloadFile(file)}
                  onSend={() => { setSendModal({ file }); }}
                  onDelete={() => deleteFile(file)}
                  downloading={downloading[file.blobId]}
                  getZkLoginSigner={getZkLoginSigner}
                />
              ))}
            </div>
          </section>
        )}
      </main>

      {sendModal && (
        <SendModal
          file={sendModal.file}
          onSend={(recipient) => sendFile(sendModal.file, recipient, () => setSendModal(null))}
          onClose={() => setSendModal(null)}
          sending={sending}
          error={sendError}
        />
      )}
    </div>
  );
}
