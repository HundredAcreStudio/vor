import { Stub } from "./Stub.tsx";

/** Activity renders a placeholder for the pipeline run-history view (per-phase
 * state and duration), pending wiring to the /pipeline API. */
export function Activity() {
  return (
    <Stub title="Activity">
      <p>
        Pipeline run history — each phase's state and duration per job, so you
        can see when the repo was last (re)indexed and what the daemon's
        auto-indexer has been doing. The <code>/pipeline</code> API exists;
        this view needs wiring.
      </p>
    </Stub>
  );
}
