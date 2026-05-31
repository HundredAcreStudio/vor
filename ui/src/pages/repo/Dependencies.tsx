import { Stub } from "./Stub.tsx";

/** Dependencies renders a placeholder for the declared third-party dependency
 * view (grouped by ecosystem, dev vs. runtime), pending wiring to /externals. */
export function Dependencies() {
  return (
    <Stub title="Dependencies">
      <p>
        Declared third-party dependencies, grouped by ecosystem, with dev vs.
        runtime split. The <code>/externals</code> API already exists — this
        view just needs to be wired to it.
      </p>
    </Stub>
  );
}
