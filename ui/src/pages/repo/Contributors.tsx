import { Stub } from "./Stub.tsx";

/** Contributors renders a placeholder for the per-author contributor breakdown
 * (commits, ownership, bus factor), pending a git-author aggregation endpoint. */
export function Contributors() {
  return (
    <Stub title="Contributors">
      <p>
        Contributor breakdown — commits, ownership, and bus-factor per author —
        will appear here once a git-author aggregation endpoint is added.
      </p>
    </Stub>
  );
}
