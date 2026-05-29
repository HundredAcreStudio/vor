import type { ReactNode } from "react";

// Stub renders a "coming soon" placeholder for repo sections whose backend
// HTTP route doesn't exist yet (the data may live in the DB and/or be
// reachable via MCP, but isn't wired to the dashboard). One component so
// every placeholder reads consistently.
export function Stub({ title, children }: { title: string; children: ReactNode }) {
  return (
    <>
      <header className="page-header">
        <h1>{title}</h1>
      </header>
      <div className="placeholder">
        {children}
        <p className="muted">Backend route coming next.</p>
      </div>
    </>
  );
}
