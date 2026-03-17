import { useState, useEffect } from "react";
import { suiClient } from "../sealService.js";

export function useBalance(address) {
  const [balance, setBalance] = useState(null);

  useEffect(() => {
    if (!address) return;
    const fetchBalance = () =>
      suiClient
        .getBalance({ owner: address })
        .then((b) => setBalance(b.totalBalance))
        .catch(() => {});
    fetchBalance();
    const id = setInterval(fetchBalance, 30_000);
    return () => clearInterval(id);
  }, [address]);

  return balance;
}
