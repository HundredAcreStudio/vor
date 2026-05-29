import { type ReactNode, useEffect, useState } from "react";

export type Async<T> =
  | { status: "loading" }
  | { status: "error"; message: string }
  | { status: "ready"; data: T };

// useAsync runs an async producer and tracks loading/error/ready. Re-runs
// when deps change; ignores resolution after unmount or a deps change.
export function useAsync<T>(fn: () => Promise<T>, deps: unknown[]): Async<T> {
  const [state, setState] = useState<Async<T>>({ status: "loading" });
  useEffect(() => {
    let cancelled = false;
    setState({ status: "loading" });
    fn()
      .then((data) => {
        if (!cancelled) setState({ status: "ready", data });
      })
      .catch((err: unknown) => {
        if (!cancelled)
          setState({ status: "error", message: err instanceof Error ? err.message : String(err) });
      });
    return () => {
      cancelled = true;
    };
    // fn is intentionally excluded; callers pass the real inputs via deps.
  }, deps);
  return state;
}

// AsyncView renders the standard loading/error states, delegating the ready
// state to children. Keeps every page from re-implementing the same markup.
export function AsyncView<T>({
  state,
  children,
}: {
  state: Async<T>;
  children: (data: T) => ReactNode;
}) {
  if (state.status === "loading") return <p className="muted">Loading…</p>;
  if (state.status === "error") return <p className="error">Failed to load: {state.message}</p>;
  return <>{children(state.data)}</>;
}
