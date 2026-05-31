import { Stub } from "./Stub.tsx";

/** Costs renders a placeholder for the LLM-spend view (tokens and dollar cost
 * per provider/model and pipeline phase), pending a costs HTTP route. */
export function Costs() {
  return (
    <Stub title="Costs">
      <p>
        LLM spend recorded for this repository — tokens and dollar cost per
        provider/model and per pipeline phase (doc generation, embeddings).
        Needs a costs HTTP route over the recorded spend.
      </p>
    </Stub>
  );
}
