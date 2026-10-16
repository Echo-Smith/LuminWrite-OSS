import type { OpError } from "@luminwrite/contracts";

export type ProposeOutcome =
  | {
      ok: true;
      already: boolean;
      status: "applied" | "proposed";
      versionId?: string;
      proposalId?: string;
    }
  | { ok: false; errors: OpError[] };
