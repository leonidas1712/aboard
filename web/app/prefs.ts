"use client";

// Small per-browser conveniences: what is collapsed, how wide the panels are, which
// filters are set. They live in this browser's storage when it allows it; the page
// works the same without them.

import { useCallback, useState } from "react";

export function readStored(key: string): string | null {
  try {
    return localStorage.getItem(key);
  } catch {
    return null;
  }
}

export function store(key: string, value: string | null) {
  try {
    if (value === null) localStorage.removeItem(key);
    else localStorage.setItem(key, value);
  } catch {
    // Not kept; the page works the same without it.
  }
}

/** usePref is state kept in this browser's storage under key, as JSON. */
export function usePref<T>(key: string, initial: T): [T, (v: T) => void] {
  const [value, setValue] = useState<T>(() => {
    const raw = readStored(key);
    if (raw === null) return initial;
    try {
      return JSON.parse(raw) as T;
    } catch {
      return initial;
    }
  });
  const set = useCallback(
    (v: T) => {
      setValue(v);
      store(key, JSON.stringify(v));
    },
    [key],
  );
  return [value, set];
}
