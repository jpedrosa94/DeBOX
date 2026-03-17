import { useState, useCallback, useEffect } from "react";
import { EnokiFlow } from "@mysten/enoki";

const GOOGLE_CLIENT_ID = import.meta.env.VITE_GOOGLE_CLIENT_ID;
const ENOKI_API_KEY = import.meta.env.VITE_ENOKI_API_KEY;

// Persist sessions across page refreshes via localStorage
const enokiFlow = new EnokiFlow({
  apiKey: ENOKI_API_KEY,
  store: {
    get: (key) => localStorage.getItem(key),
    set: (key, value) => localStorage.setItem(key, value),
    delete: (key) => localStorage.removeItem(key),
  },
});

function extractProfile(jwt) {
  try {
    const base64 = jwt.split(".")[1].replace(/-/g, "+").replace(/_/g, "/");
    const bytes = Uint8Array.from(atob(base64), (c) => c.charCodeAt(0));
    const { email, name, picture } = JSON.parse(new TextDecoder().decode(bytes));
    return { email, name, picture };
  } catch {
    return {};
  }
}

export function useZkLogin() {
  const [session, setSession] = useState(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState(null);

  // Restore session on mount
  useEffect(() => {
    enokiFlow.getSession().then((zkpSession) => {
      const { address } = enokiFlow.$zkLoginState.get();
      if (address) {
        setSession({
          address,
          jwt: zkpSession?.jwt || null,
          ...(zkpSession?.jwt ? extractProfile(zkpSession.jwt) : {}),
        });
      }
    });
  }, []);

  // Step 1 — redirect to Google OAuth via Enoki
  const initLogin = useCallback(async () => {
    setError(null);
    try {
      const url = await enokiFlow.createAuthorizationURL({
        provider: "google",
        clientId: GOOGLE_CLIENT_ID,
        redirectUrl: window.location.origin,
        network: "testnet",
        extraParams: { scope: ["email", "profile"] },
      });
      window.location.href = url;
    } catch (err) {
      setError(err.message);
    }
  }, []);

  // Step 2 — called after Google redirects back with #id_token=…
  // Pass the raw hash string (window.location.hash) — Enoki reads id_token from it
  const handleCallback = useCallback(async (hash) => {
    setLoading(true);
    setError(null);
    try {
      await enokiFlow.handleAuthCallback(hash);
      const { address } = enokiFlow.$zkLoginState.get();
      const zkpSession = await enokiFlow.getSession();
      const newSession = {
        address,
        jwt: zkpSession?.jwt || null,
        ...(zkpSession?.jwt ? extractProfile(zkpSession.jwt) : {}),
      };
      setSession(newSession);
      return newSession;
    } catch (err) {
      setError(err.message);
      throw err;
    } finally {
      setLoading(false);
    }
  }, []);

  const logout = useCallback(async () => {
    await enokiFlow.logout();
    setSession(null);
  }, []);

  // Returns a signer compatible with @mysten/seal's SessionKey
  // signPersonalMessage / signTransaction return a plain base64 zkLogin signature string
  const getZkLoginSigner = useCallback(() => {
    if (!session) return null;
    return {
      toSuiAddress: () => session.address,
      signPersonalMessage: async (bytes) => {
        const keypair = await enokiFlow.getKeypair({ network: "testnet" });
        const { signature } = await keypair.signPersonalMessage(bytes);
        return signature;
      },
      signTransaction: async (bytes) => {
        const keypair = await enokiFlow.getKeypair({ network: "testnet" });
        const { signature } = await keypair.signTransaction(bytes);
        return signature;
      },
    };
  }, [session]);

  return { session, loading, error, initLogin, handleCallback, logout, getZkLoginSigner };
}
