import { Stub } from "./Stub.tsx";

/** Coverage renders a placeholder for the test-coverage view (per-file/package
 * coverage with untested-hotspot overlays), pending a coverage HTTP route. */
export function Coverage() {
  return (
    <Stub title="Coverage">
      <p>
        Test-coverage reports (LCOV / Cobertura) per file and package, with
        untested-hotspot overlays. Needs a coverage HTTP route over the
        imported coverage data.
      </p>
    </Stub>
  );
}
