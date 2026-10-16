"""命令行：python -m lweval <子命令> ...

  aitone   FILE...                         说人话/AI 腔检测（--json 输出全部命中）
  inject   FILE --k 0,1,2,4,8              注入 AI 腔短语的灵敏度曲线
  style    FILE... --ref REF...            风格指纹距离（相对参考范文）
  stylepack FILE... --voice yinyue --genre commentary   文风遵循 + 文体合规（两项分开报告）
  drift    FILE [--ref REF...] [--window 300 --step 150]   滑动窗口风格漂移 + 变点
  upcr     BEFORE AFTER [--target i] [--anchor 子串] [--must-contain s] [--must-not-contain s]
  shuffle  FILE [--metric adjacency_advantage|connective_fit] [--n 200]
  bt       JUDGMENTS.jsonl                 Bradley-Terry / Elo + bootstrap CI（只用 status=ok 的成对判定）
  power                                    样本量表
  cost     [--tasks 120 --arms 7 --gen M --judge M]   成本估算（元）
"""
import argparse
import json
import sys

from . import aitone, stylometry, upcr as upcr_mod, logic_probe, stats, cost, stylepack


def _read(p):
    with open(p, encoding="utf-8") as f:
        return f.read()


def main(argv=None):
    ap = argparse.ArgumentParser(prog="lweval")
    sub = ap.add_subparsers(dest="cmd", required=True)
    a = sub.add_parser("aitone"); a.add_argument("files", nargs="+"); a.add_argument("--json", action="store_true")
    i = sub.add_parser("inject"); i.add_argument("file"); i.add_argument("--k", default="0,1,2,4,8"); i.add_argument("--seeds", type=int, default=5)
    s = sub.add_parser("style"); s.add_argument("files", nargs="+"); s.add_argument("--ref", nargs="+", required=True)
    spk = sub.add_parser("stylepack"); spk.add_argument("files", nargs="+"); spk.add_argument("--voice", default="yinyue", choices=sorted(stylepack.VOICE_PACKS)); spk.add_argument("--genre", default="commentary", choices=sorted(stylepack.GENRE_PACKS))
    d = sub.add_parser("drift"); d.add_argument("file"); d.add_argument("--ref", nargs="*"); d.add_argument("--window", type=int, default=300); d.add_argument("--step", type=int, default=150); d.add_argument("--self-anchor", type=int, default=2)
    u = sub.add_parser("upcr"); u.add_argument("before"); u.add_argument("after"); u.add_argument("--target", type=int, action="append"); u.add_argument("--anchor", action="append"); u.add_argument("--must-contain", action="append"); u.add_argument("--must-not-contain", action="append"); u.add_argument("--rows", action="store_true")
    sh = sub.add_parser("shuffle"); sh.add_argument("file"); sh.add_argument("--metric", default="adjacency_advantage"); sh.add_argument("--n", type=int, default=200)
    b = sub.add_parser("bt"); b.add_argument("judgments")
    sub.add_parser("power")
    c = sub.add_parser("cost"); c.add_argument("--tasks", type=int, default=120); c.add_argument("--arms", type=int, default=7); c.add_argument("--gen", default="deepseek-v4-flash@idle"); c.add_argument("--judge", default="deepseek-v4-flash@idle")
    args = ap.parse_args(argv)
    out = None
    if args.cmd == "aitone":
        det = aitone.AIToneDetector()
        out = {}
        for f in args.files:
            r = det.analyze(_read(f)).to_dict()
            if not args.json:
                r.pop("hits")
            out[f] = r
    elif args.cmd == "inject":
        det = aitone.AIToneDetector()
        text = _read(args.file)
        out = {}
        for k in [int(x) for x in args.k.split(",")]:
            vals = [det.analyze(aitone.inject(text, k, seed)).aitone_score for seed in range(args.seeds)]
            out[k] = round(sum(vals) / len(vals), 1)
    elif args.cmd == "style":
        ref = stylometry.reference([stylometry.fingerprint(w) for r in args.ref for w in stylometry.windows(_read(r))])
        out = {f: stylometry.distance(stylometry.fingerprint(_read(f)), ref) for f in args.files}
    elif args.cmd == "stylepack":
        out = {}
        for f in args.files:
            r = stylepack.score(_read(f), args.voice, args.genre)
            out[f] = {"voice_adherence": r["voice_adherence"], "genre_conformance": r["genre_conformance"],
                      "voice_checks": r["voice_detail"]["checks"], "per1k": r["voice_detail"]["per1k"],
                      "optional_devices": r["voice_detail"]["optional_devices"], "genre_checks": r["genre_detail"]["checks"]}
    elif args.cmd == "drift":
        refs = [_read(r) for r in args.ref] if args.ref else None
        out = stylometry.drift(_read(args.file), refs, args.self_anchor, args.window, args.step)
    elif args.cmd == "upcr":
        out = upcr_mod.upcr(_read(args.before), _read(args.after), args.target, args.anchor, args.must_contain, args.must_not_contain)
        if not args.rows:
            out.pop("rows")
    elif args.cmd == "shuffle":
        out = logic_probe.shuffle_sensitivity(_read(args.file), args.metric, args.n)
    elif args.cmd == "bt":
        comps = []
        for line in open(args.judgments, encoding="utf-8"):
            j = json.loads(line)
            if j.get("status") != "ok" or j.get("kind") != "pairwise":
                continue
            o = {"A": 1.0, "B": 0.0, "tie": 0.5}[j["winner"]]
            comps.append((j["system_a"], j["system_b"], o))
        elo, ci = stats.bt_bootstrap(comps)
        out = {k: {"elo": v, "ci95": ci[k]} for k, v in sorted(elo.items(), key=lambda x: -x[1])}
    elif args.cmd == "power":
        out = {"pairwise_winrate": {p: stats.n_pairs_for_winrate(p) for p in (0.55, 0.60, 0.65, 0.70)},
               "paired_cohens_d": {d_: stats.n_for_paired_d(d_) for d_ in (0.2, 0.3, 0.5, 0.8)}}
    elif args.cmd == "cost":
        rows = cost.plan(n_tasks=args.tasks, n_arms=args.arms)
        out = {"volume": rows, "cny": cost.price(rows, args.gen, args.judge)}
    json.dump(out, sys.stdout, ensure_ascii=False, indent=2)
    print()


if __name__ == "__main__":
    main()
