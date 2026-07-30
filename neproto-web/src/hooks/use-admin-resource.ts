"use client";

import * as React from "react";

import { adminFetch } from "@/lib/admin-api";

export function useAdminResource<T>(path: string, refreshInterval = 0) {
  const [data, setData] = React.useState<T | null>(null);
  const [error, setError] = React.useState<string | null>(null);
  const [loading, setLoading] = React.useState(true);

  const refresh = React.useCallback(async () => {
    try {
      const next = await adminFetch<T>(path);
      setData(next);
      setError(null);
      return next;
    } catch (caught) {
      setError(caught instanceof Error ? caught.message : "Request failed");
      throw caught;
    } finally {
      setLoading(false);
    }
  }, [path]);

  React.useEffect(() => {
    let active = true;
    const load = async () => {
      try {
        const next = await adminFetch<T>(path);
        if (active) {
          setData(next);
          setError(null);
        }
      } catch (caught) {
        if (active) {
          setError(caught instanceof Error ? caught.message : "Request failed");
        }
      } finally {
        if (active) {
          setLoading(false);
        }
      }
    };
    void load();
    const timer = refreshInterval > 0 ? window.setInterval(load, refreshInterval) : undefined;
    return () => {
      active = false;
      if (timer !== undefined) {
        window.clearInterval(timer);
      }
    };
  }, [path, refreshInterval]);

  return { data, error, loading, refresh };
}
