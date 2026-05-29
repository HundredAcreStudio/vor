import { Stub } from "./Stub.tsx";

export function Security() {
  return (
    <Stub title="Security">
      <p>
        Security findings — secrets, weak crypto, and injection sinks — will
        surface here. The scan exists as a CLI/MCP capability; it needs an HTTP
        route before this view can render live data.
      </p>
    </Stub>
  );
}
