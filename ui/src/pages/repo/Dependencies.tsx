import { Stub } from "./Stub.tsx";

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
