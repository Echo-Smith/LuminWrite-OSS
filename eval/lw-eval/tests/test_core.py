import os
import random
import sys
import unittest

sys.path.insert(0, os.path.dirname(os.path.dirname(__file__)))
from lweval import aitone, stylometry, upcr, logic_probe, stats, judges  # noqa: E402

S = os.path.join(os.path.dirname(os.path.dirname(__file__)), "samples")


def rd(name):
    with open(os.path.join(S, name), encoding="utf-8") as f:
        return f.read()


class TestAITone(unittest.TestCase):
    def setUp(self):
        self.d = aitone.AIToneDetector()

    def test_clean_text_zero(self):
        self.assertEqual(self.d.analyze("我昨天去菜市场买了两斤西红柿，花了六块钱。").aitone_score, 0.0)

    def test_buzzword_hit(self):
        r = self.d.analyze("我们要全方位赋能，打造闭环生态体系。")
        cats = {h["category"] for h in r.hits}
        self.assertIn("buzzword", cats)
        self.assertGreater(r.aitone_score, 50)

    def test_ordinal_requires_three(self):
        two = self.d.analyze("首先，带上身份证。其次，提前十分钟到。")
        three = self.d.analyze("首先，带上身份证。其次，提前十分钟到。最后，别忘了雨伞。")
        self.assertNotIn("ordinal_sequence", {h["category"] for h in two.hits})
        self.assertIn("ordinal_sequence", {h["category"] for h in three.hits})

    def test_ordinal_not_triggered_by_tiers(self):
        r = self.d.analyze("电量分三档：第一档按原价，第二档加五分钱，第三档加三毛。")
        self.assertNotIn("ordinal_sequence", {h["category"] for h in r.hits})

    def test_injection_monotone(self):
        base = rd("nat_report_ev_county.md")
        scores = [self.d.analyze(aitone.inject(base, k, 0)).aitone_score for k in (0, 2, 4, 8)]
        self.assertEqual(scores, sorted(scores))

    def test_negatives_above_naturals_on_samples(self):
        neg = [self.d.analyze(rd(f)).aitone_score for f in os.listdir(S) if f.startswith(("hollow_", "control_filler"))]
        nat = [self.d.analyze(rd(f)).aitone_score for f in os.listdir(S) if f.startswith("nat_")]
        self.assertGreater(min(neg), max(nat))


class TestRhetoricCalibration(unittest.TestCase):
    """有意修辞 ≠ 空话：同样的排比/设问/首在-重在-贵在，承载具体内容时应大幅折减。"""

    def test_gating_unit(self):
        d = aitone.AIToneDetector()
        hollow = "推进工作，首在夯实基础，重在激活人才，贵在久久为功。"
        concrete = "推进工作，首在摸清谁在用：全村412人，APP只装了37个。"
        h = d.analyze(hollow)
        c = d.analyze(concrete)
        self.assertGreater(h.rhetoric_gated, c.rhetoric_gated)
        self.assertEqual(c.hollow_raw, 0.0)

    def test_good_yinyue_vs_hollow_imitation(self):
        d = aitone.AIToneDetector()
        good = d.analyze(rd("style_yinyue_good_village.md"))
        bad = d.analyze(rd("hollow_yinyue_imitation.md"))
        self.assertLess(good.aitone_score, 30)
        self.assertGreater(bad.aitone_score - good.aitone_score, 50)
        self.assertGreater(good.rhetoric_raw, 20)  # 修辞浓度高（风格特征），但不被当作空话

    def test_v01_over_penalized(self):
        v01 = aitone.AIToneDetector(gate_rhetoric=False).analyze(rd("style_yinyue_good_village.md"))
        v02 = aitone.AIToneDetector().analyze(rd("style_yinyue_good_village.md"))
        self.assertGreater(v01.aitone_score, v02.aitone_score + 30)

    def test_filler_control_ranks_below_content(self):
        d = aitone.AIToneDetector()
        filler = d.analyze(rd("control_filler_village.md")).aitone_score
        content = [d.analyze(rd(f)).aitone_score for f in os.listdir(S) if f.startswith(("nat_", "style_"))]
        self.assertGreater(filler, max(content))

    def test_stylepack_two_axes(self):
        # 文风遵循只回答"像不像"：空壳仿写的修辞外壳也像，好坏交给说人话轴
        from lweval import stylepack
        self.assertEqual(stylepack.check(rd("style_yinyue_good_village.md"))["adherence"], 1.0)
        self.assertGreaterEqual(stylepack.check(rd("hollow_yinyue_imitation.md"))["adherence"], 0.9)
        self.assertEqual(stylepack.check(rd("nat_commentary_village.md"))["adherence"], 0.0)

    def test_filler_deterministic(self):
        from lweval import filler
        self.assertEqual(filler.generate("测试主题", 5, 3), filler.generate("测试主题", 5, 3))


class TestVoiceGenre(unittest.TestCase):
    """v0.3：文风 × 文体分离；首在-重在-贵在是可选手法。"""

    def setUp(self):
        from lweval import stylepack
        self.sp = stylepack

    def test_progression_optional(self):
        # 去掉递进格（换成中性说法），文风遵循不变；可选手法只记录，不计分
        good = rd("style_yinyue_good_village.md")
        neutral = good.replace("首在", "先要").replace("重在", "关键是").replace("贵在", "难在")
        a, b = self.sp.check_voice(good, "yinyue"), self.sp.check_voice(neutral, "yinyue")
        self.assertTrue(a["optional_devices"]["progression"])
        self.assertFalse(b["optional_devices"]["progression"])
        self.assertEqual(a["voice_adherence"], b["voice_adherence"])
        self.assertNotIn("progression", a["checks"])

    def test_progression_alone_not_enough(self):
        # 只有递进格、没有比喻/排比/设问的平实文，文风遵循仍然低
        t = "推进这件事，首在摸清谁在用。全村412人，APP装了37个。\n\n重在算清省了谁的事。老人少跑一趟县城。\n\n贵在耐得住不好看。上门教老人用手机。"
        self.assertLess(self.sp.check_voice(t, "yinyue")["voice_adherence"], 0.5)

    def test_frequency_range(self):
        self.assertEqual(self.sp._range_score(3.0, 2.0, 15.0), 1.0)
        self.assertEqual(self.sp._range_score(1.0, 2.0, 15.0), 0.5)
        self.assertEqual(self.sp._range_score(30.0, 2.0, 15.0), 0.5)  # 过密同样扣分

    def test_yinyue_voice_on_report_genre(self):
        # 印月文风写调研报告：文风遵循仍高，文体按报告判也合规
        r = self.sp.score(rd("style_yinyue_report_ev_county.md"), "yinyue", "report")
        self.assertGreaterEqual(r["voice_adherence"], 0.9)
        self.assertGreaterEqual(r["genre_conformance"], 0.9)

    def test_voice_genre_independence_2x2(self):
        # 2 文风 × 2 文体：文风分随文风变、不随文体变；文体分随文体变、不随文风变
        grid = {("yinyue", "commentary"): "style_yinyue_good_village.md",
                ("yinyue", "report"): "style_yinyue_report_ev_county.md",
                ("plain_professional", "commentary"): "nat_commentary_village.md",
                ("plain_professional", "report"): "nat_report_ev_county.md"}
        v = {k: self.sp.check_voice(rd(f), "yinyue")["voice_adherence"] for k, f in grid.items()}
        g = {k: {gn: self.sp.check_genre(rd(f), gn)["genre_conformance"] for gn in ("commentary", "report")} for k, f in grid.items()}
        for genre in ("commentary", "report"):
            self.assertGreater(v[("yinyue", genre)] - v[("plain_professional", genre)], 0.7)
        self.assertLessEqual(abs(v[("yinyue", "commentary")] - v[("yinyue", "report")]), 0.1)
        for voice in ("yinyue", "plain_professional"):
            self.assertGreaterEqual(g[(voice, "commentary")]["commentary"], 0.8)
            self.assertGreaterEqual(g[(voice, "report")]["report"], 0.8)
            self.assertLessEqual(g[(voice, "commentary")]["report"], 0.5)
            self.assertLessEqual(g[(voice, "report")]["commentary"], 0.5)
        # 交互效应：同一文体下，两种文风的文体分相差不大
        for genre in ("commentary", "report"):
            self.assertLessEqual(abs(g[("yinyue", genre)][genre] - g[("plain_professional", genre)][genre]), 0.2)

    def test_genre_rules_not_in_voice(self):
        # 篇幅/结构类检查只在文体包里出现
        for pack in self.sp.VOICE_PACKS.values():
            self.assertFalse((set(pack["devices"]) | set(pack["flags"])) & {"length", "three_part", "title_len"})


class TestUPCR(unittest.TestCase):
    def test_identical(self):
        t = rd("rev_before.md")
        r = upcr.upcr(t, t, targets=[1])
        self.assertEqual(r["UPCR"], 0.0)
        self.assertFalse(r["anchored_success"])  # 目标段未改 → 失败

    def test_good_vs_drift(self):
        b = rd("rev_before.md")
        g = upcr.upcr(b, rd("rev_after_good.md"), anchors=["售后是第二个"])
        d = upcr.upcr(b, rd("rev_after_drift.md"), anchors=["售后是第二个"])
        self.assertEqual(g["UPCR"], 0.0)
        self.assertTrue(g["anchored_success"])
        self.assertGreater(d["UPCR"], 0.4)

    def test_insertion(self):
        b = "甲段内容比较长一些。\n\n乙段内容也比较长。\n\n丙段内容同样很长。"
        a = "甲段内容比较长一些。\n\n乙段内容也比较长。\n\n丙段内容同样很长。\n\n新加的一段完全无关的话。"
        r = upcr.upcr(b, a, targets=[0])
        self.assertEqual(r["UPCR"], 0.0)
        self.assertGreater(r["insert_rate"], 0)


class TestStylometry(unittest.TestCase):
    def test_self_distance_small(self):
        t = rd("nat_essay_remote_work.md")
        ref = stylometry.reference([stylometry.fingerprint(t)])
        self.assertLess(stylometry.distance(stylometry.fingerprint(t), ref)["distance"], 1e-6)

    def test_change_point(self):
        idx, stat, _, _ = stylometry.change_point([0.1, 0.12, 0.11, 0.1, 1.0, 1.1, 0.9])
        self.assertEqual(idx, 4)


class TestStats(unittest.TestCase):
    def test_kappa_known(self):
        # 50 条：A 判 yes 25 次，B 判 yes 30 次，一致 35 次 → po=0.7, pe=0.5, κ=0.4
        a = ["y"] * 20 + ["y"] * 5 + ["n"] * 10 + ["n"] * 15
        b = ["y"] * 20 + ["n"] * 5 + ["y"] * 10 + ["n"] * 15
        self.assertAlmostEqual(stats.cohen_kappa(a, b), 0.4, places=6)

    def test_alpha_perfect(self):
        self.assertAlmostEqual(stats.krippendorff_alpha([[1, 2, 3, 1], [1, 2, 3, 1]]), 1.0)

    def test_alpha_interval_with_missing(self):
        a = stats.krippendorff_alpha([[1, 2, 3, None], [1, 2, 4, 2], [None, 2, 3, 2]], level="interval")
        self.assertTrue(0.5 < a <= 1.0)

    def test_bt_recovers_order(self):
        # 合成数据：真实强度 C>B>A，检查 BT 能恢复排序（仅验证算法，非评测结果）
        rnd = random.Random(1)
        true = {"A": 1.0, "B": 2.0, "C": 4.0}
        comps = []
        for _ in range(600):
            x, y = rnd.sample(list(true), 2)
            p = true[x] / (true[x] + true[y])
            comps.append((x, y, 1.0 if rnd.random() < p else 0.0))
        _, elo = stats.bradley_terry(comps)
        self.assertEqual(sorted(elo, key=elo.get), ["A", "B", "C"])

    def test_power_monotone(self):
        self.assertGreater(stats.n_pairs_for_winrate(0.55), stats.n_pairs_for_winrate(0.65))

    def test_sign_test(self):
        self.assertAlmostEqual(stats.sign_test_p(5, 5), 1.0)
        self.assertLess(stats.sign_test_p(18, 2), 0.01)


class TestLogicHarness(unittest.TestCase):
    def test_runs(self):
        r = logic_probe.shuffle_sensitivity(rd("nat_report_ev_county.md"), n_perm=30)
        self.assertIn("sensitivity", r)

    def test_custom_metric_perfect_sensitivity(self):
        # 一个"知道正确顺序"的理想指标应得 sensitivity≈1：验证 harness 本身
        text = rd("nat_report_ev_county.md")
        order = logic_probe._content_paras(text)

        def oracle(paras):
            return sum(1 for i, p in enumerate(paras) if p == order[i])
        r = logic_probe.shuffle_sensitivity(text, oracle, n_perm=50)
        self.assertGreaterEqual(r["sensitivity"], 0.99)


class TestJudges(unittest.TestCase):
    def test_render_and_swap(self):
        out = judges.StubJudge().pairwise("pairwise_naturalness", "任务", "甲文", "乙文")
        self.assertEqual(out["status"], "not_run")
        self.assertIn("甲文", out["prompt"])
        self.assertEqual(judges.swap_consistent("A", "B"), "A")
        self.assertEqual(judges.swap_consistent("A", "A"), "tie")


if __name__ == "__main__":
    unittest.main()


class TestAlphaReference(unittest.TestCase):
    def test_krippendorff_canonical_example(self):
        # Krippendorff (2011) 教程中的 4 评审 × 12 单元示例：nominal α=0.743，interval α=0.849
        N = None
        d = [[1, 2, 3, 3, 2, 1, 4, 1, 2, N, N, N], [1, 2, 3, 3, 2, 2, 4, 1, 2, 5, N, 3],
             [N, 3, 3, 3, 2, 3, 4, 2, 2, 5, 1, N], [1, 2, 3, 3, 2, 4, 4, 1, 2, 5, 1, N]]
        self.assertAlmostEqual(stats.krippendorff_alpha(d), 0.743, places=3)
        self.assertAlmostEqual(stats.krippendorff_alpha(d, "interval"), 0.849, places=3)


class TestRetrievalAndGraph(unittest.TestCase):
    def test_retrieval(self):
        from lweval import retrieval_metrics as rm
        pc = rm.plan_coverage({"G1": "covered", "G2": "partial", "G3": "missed"}, {"G1": True, "G2": True, "G3": False})
        self.assertEqual(pc, {"plan_coverage": 0.5, "critical_coverage": 0.75})
        v = [{"verdict": "supported", "cited": ["S1"], "best_source": "S1"},
             {"verdict": "unsupported", "cited": ["S2"], "best_source": None},
             {"verdict": "partial", "cited": [], "best_source": "S3"},
             {"verdict": "supported", "cited": ["S1"], "best_source": "S1", "type": "opinion"}]
        cm = rm.citation_metrics(v)
        self.assertEqual(cm["citation_precision"], 0.5)
        self.assertAlmostEqual(cm["unsupported_rate"], 0.333, places=3)
        self.assertEqual(rm.planning_efficiency(12, 4), 3.0)
        self.assertEqual(rm.ablation_sensitivity(0.8, 0.5, True), "honest_gap")

    def test_graph(self):
        from lweval import logic_graph as lg
        g = {"claims": [{"id": "C1", "type": "main"}, {"id": "C2", "type": "sub"}, {"id": "C3", "type": "fact"}, {"id": "C4", "type": "sub"}],
             "supports": [{"from": "C2", "to": "C1", "warrant_stated": True}, {"from": "C3", "to": "C2", "warrant_stated": False}],
             "orphans": ["C4"], "leaps": [{"to": "C1"}], "attacks": []}
        m = lg.graph_metrics(g, 2000)
        self.assertEqual(m["depth"], 3)
        self.assertAlmostEqual(m["support_ratio"], 0.667, places=3)
        self.assertEqual(m["leap_per_kchar"], 0.5)
