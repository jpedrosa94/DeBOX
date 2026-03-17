import { useState } from "react";
import { shortAddress } from "../utils.js";

export default function Header({ session, balance, onLogout }) {
  const [copied, setCopied] = useState(false);

  function copyAddress() {
    navigator.clipboard.writeText(session.address).then(() => {
      setCopied(true);
      setTimeout(() => setCopied(false), 1500);
    });
  }

  return (
    <>
      <header className="header">
        <div className="header-left">
          <div className="header-logo">🦭</div>
          <div>
            <h1 className="header-title">DeBOX</h1>
            <p className="header-subtitle">Encrypted · Decentralized · Yours</p>
          </div>
        </div>

        <div className="header-user">
          {session.picture && (
            <img
              src={session.picture}
              alt={session.name}
              className="user-avatar"
              referrerPolicy="no-referrer"
            />
          )}
          <div className="user-info">
            <span className="user-name">{session.name}</span>
            <span className="user-address-row">
              <span className="user-address" title={session.address}>
                {shortAddress(session.address)}
              </span>
              <button
                className="copy-btn"
                onClick={copyAddress}
                title="Copy full address"
              >
                {copied ? "✓" : "⎘"}
              </button>
            </span>
            <span className="user-balance" title="SUI balance">
              {balance === null
                ? "…"
                : `${(Number(balance) / 1e9).toFixed(4)} SUI`}
            </span>
          </div>
          <button className="logout-btn" onClick={onLogout} title="Sign out">
            ↩
          </button>
        </div>
      </header>

      {balance !== null && Number(balance) < 10_000_000 && (
        <div className="gas-warning">
          ⚠️ Your wallet has insufficient gas (
          {(Number(balance) / 1e9).toFixed(6)} SUI). File preview and download
          require gas to verify access.{" "}
          <a
            href={`https://faucet.sui.io/?address=${session.address}`}
            target="_blank"
            rel="noopener noreferrer"
          >
            Get testnet SUI →
          </a>
        </div>
      )}
    </>
  );
}
