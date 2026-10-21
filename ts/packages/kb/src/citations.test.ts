import { openMemoryDatabase } from "@luminwrite/db";
import { fauxResearchPlan, readStoredPlan } from "@luminwrite/research";
import { describe, expect, it } from "vitest";

import { createKnowledgeBase } from "./kb.js";

describe("引用校验", () => {
  it("RSS 条目改稿后，旧引用判 mismatch，通过的证据补上计划缺口", async () => {
    const db = await openMemoryDatabase();
    try {
      const kb = createKnowledgeBase({ db });
      const created = await db.createDocument({ title: "引用稿", ownerId: "user_1" });
      const quote = "甲乙丙丁戊己庚辛壬癸，时租仍按小时计。";
      const saved = await kb.saveText({
        ownerId: "user_1",
        kind: "rss_item",
        title: "时租条目",
        body: `前文。${quote}后文。`,
        feedId: "feed_1",
        feedGuid: "guid_1",
      });
      const accepted = fauxResearchPlan("查时租");
      const plan = await db.saveResearchPlan({
        turnId: "trn_gap",
        sessionId: created.sessionId,
        plan: accepted,
        gaps: ["q1: 尚未有通过校验的证据"],
      });
      const cited = await kb.recordEvidence({
        ownerId: "user_1",
        citingDocumentId: created.id,
        sourceId: saved.sourceId,
        quote,
        planId: plan.id,
        questionId: "q1",
      });
      expect(cited.citationStatus).toBe("verified");
      const filledRow = (await db.listResearchPlans(created.id))[0];
      if (!filledRow) throw new Error("没有研究计划");
      const filled = readStoredPlan(filledRow);
      expect(filled.gaps).toEqual([]);

      const updated = await kb.saveText({
        ownerId: "user_1",
        kind: "rss_item",
        title: "时租条目",
        body: "这条已经改成完全不同的句子，原来的时租原文不在了。",
        feedId: "feed_1",
        feedGuid: "guid_1",
      });
      expect(updated).toEqual({ sourceId: saved.sourceId, status: "updated" });
      const resolved = await kb.resolveEvidence("user_1", cited.id);
      expect(resolved.citationStatus).toBe("mismatch");
      expect(resolved.note).toBe("来源已修改");
      const reopenedRow = (await db.listResearchPlans(created.id))[0];
      if (!reopenedRow) throw new Error("没有研究计划");
      const reopened = readStoredPlan(reopenedRow);
      expect(reopened.gaps).toEqual(["q1: 尚未有通过校验的证据"]);
    } finally {
      await db.close();
    }
  });
});
