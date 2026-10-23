#!/usr/bin/env python3
"""P21 offline C: can the migration target of a page / 1 KB block be predicted from its index
in the buffer, and how much can be pre-placed before the page is first touched?

usage: offline_pattern.py <workload>  -> JSON line on stdout

Data: W=128 baseline exact aggregation (all pages, no sampling): per (page, reader) reads and
the 8 x 512 B sub-block reads; page first access = pages.csv first_ns (blocks inherit it).
Buffer = driver allocation (alloc 'block'); index i = page (or 1 KB block) number in the buffer.
Target = reader with >= 0.6 of the unit's L1V reads (owner's local reads included), else none.
A unit "migrates" if it has a target != owner. Devices 1..48 (tile 24 = CPU skipped).

Rules (device numbers d = dev - 1 in 0..47):
  const   adjacent same: d_i = d of the neighbouring migrating unit (index order)
  lin     d = (c + floor(i * sigma + phi)) mod 48   (linear split, n = 1 / sigma pages per device,
          real-valued; wraps around the 48 devices)
  step    d = (c + s * i) mod 48, s integer
  owner   d = (owner_dev + delta) mod 48
  best    best of the four per buffer
Global fit (C1): each rule fitted on all migrating units of the buffer; hit rate weighted by
  the units' remote reads.
Online (C2): units in first-access order; every migrating unit is an observation. Each rule
  predicts it from the observations since its last miss (const: nearest observed index; owner:
  mode of the offset; step: best s; lin: least-squares slope + best phase); a hit extends the
  rule's streak. The first rule with m consecutive hits makes the buffer confident: all its
  units not yet accessed are pre-placed by that rule (once per buffer).
  hit_online     observations predicted correctly / observations (remote-read weighted)
  prec           pre-placed units (with a main reader) whose prediction equals their main reader
                 (the owner if it should not move) -- units and remote-read weighted;
                 notarget = pre-placed units without a main reader (placement neutral);
                 harm = units that should stay at the owner but were moved / all units (reads)
  cover_bytes    correctly pre-placed migrating units / all migrating units (= migrated bytes)
  cover_reads    same, remote-read weighted
  unacc_at_conf  units not yet accessed at confirmation / units of the confident buffers
Optimistic: the observation is available at the unit's first access (no counting delay).
"""
import sys, json, collections, gzip
import numpy as np
from offline_common import *

SHARE = 0.6
MS = [2, 3, 4, 8]
HIST = 32
META = []        # (page, 1 KB sub-block or -1) per unit of the last units() call
GIVEUP = 512     # observations without confidence after which a buffer is left alone
DUMP_M = 2       # placement of the rollback learner dumped for this m (offline_ft.py)
RULES = ["const", "lin", "step", "owner"]


def wmode(x, w, n=48):
    b = np.bincount(np.asarray(x) % n, weights=w, minlength=n)
    return int(np.argmax(b))


def fit_lin(I, D, Wt, refine=False):
    """returns (sigma, phi, c) or None"""
    if len(I) < 2:
        return None
    o = np.argsort(I, kind="stable")
    I, D, Wt = I[o], D[o], Wt[o]
    if I[-1] == I[0]:
        return None
    dd = np.diff(D) % 48
    dd = np.where(dd >= 24, dd - 48, dd)
    Du = np.concatenate([[D[0]], D[0] + np.cumsum(dd)])
    x = I - I.mean()
    sig = float((x * (Du - Du.mean())).sum() / (x * x).sum())
    cands = {round(sig, 12)}
    if abs(sig) > 1e-9:
        n = 1.0 / sig
        for q in (1, 2, 3, 4):
            r = round(n * q) / q
            if r != 0:
                cands.add(round(1.0 / r, 12))
    if refine:
        for e in np.arange(-0.02, 0.0201, 0.0025):
            cands.add(round(sig * (1 + e), 12))
    phis = np.arange(16 if not refine else 64) / (16.0 if not refine else 64.0)
    best = None
    for s in cands:
        v = I * s
        fl = np.floor(v)
        fr = v - fl
        for ph in phis:
            pred = (fl + (fr >= 1 - ph - 1e-12)).astype(np.int64)
            c = wmode(D - pred, Wt)
            h = Wt[((pred + c) % 48) == D].sum()
            if best is None or h > best[0] + 1e-9:
                best = (h, s, ph, c)
    return best[1:]


def fit_lin_online(I, D, Wt):
    """fit on the recent observation window: candidate slopes from observation pairs that are
    1-3 apart in index order (device difference mod 48 in (-24, 24], +-48 alternatives for
    adjacent pairs), each also snapped to n = p/q pages per device (q <= 4); offset and phase
    chosen to maximise weighted hits on the window."""
    if len(I) < 2:
        return None
    o = np.argsort(I, kind="stable")
    I, D, Wt = I[o], D[o], Wt[o]
    cands = set()
    for gap in (1, 2, 3):
        if len(I) <= gap:
            break
        di = I[gap:] - I[:-gap]
        dd = (D[gap:] - D[:-gap]) % 48
        dd = np.where(dd > 24, dd - 48, dd)
        for a, b in zip(dd.tolist(), di.tolist()):
            if b <= 0:
                continue
            for k in ((0, -48, 48) if gap == 1 and b > 1 else (0,)):
                sg = (a + k) / b
                cands.add(round(sg, 9))
                if abs(sg) > 1e-9:
                    n = 1.0 / sg
                    for q in (1, 2, 3, 4):
                        r = round(n * q) / q
                        if r != 0:
                            cands.add(round(1.0 / r, 9))
    if not cands:
        return None
    S = np.array(sorted(cands))
    P = np.arange(16) / 16.0
    pred = np.floor(I[None, None, :] * S[:, None, None] + P[None, :, None] + 1e-9).astype(np.int64)
    off = (D[None, None, :] - pred) % 48
    nr = len(S) * len(P)
    idx = (np.arange(nr)[:, None] * 48 + off.reshape(nr, -1)).reshape(-1)
    hist = np.bincount(idx, weights=np.tile(Wt, nr), minlength=nr * 48).reshape(nr, 48)
    b = int(np.argmax(hist.max(axis=1)))
    return (float(S[b // len(P)]), float(P[b % len(P)]), int(np.argmax(hist[b])))


def pred_lin(p, i):
    s, ph, c = p
    return int((np.floor(i * s + ph + 1e-9) + c) % 48)


def fit_step(I, D, Wt):
    if len(I) < 2:
        return None
    best = None
    for s in range(48):
        c = wmode(D - s * I, Wt)
        h = Wt[((c + s * I) % 48) == D].sum()
        if best is None or h > best[0] + 1e-9:
            best = (h, s, c)
    return best[1:]


def units(w, gran):
    """per unit: buffer, index, owner dev0, target dev0 (-1 none), remote reads, first_ns"""
    info, bl = load_alloc(w)
    pa = pages_agg(w)
    per = collections.defaultdict(lambda: collections.defaultdict(lambda: [0] * 4))
    for r in readers(w):
        if int(r["reads"]) == 0:
            continue
        s = [int(x) for x in r["sub_reads"].split(";")]
        per[int(r["page"])][int(r["reader"])] = [s[0] + s[1], s[2] + s[3], s[4] + s[5], s[6] + s[7]]
    U = []
    META.clear()
    for blk, pl in bl.items():
        for i, p in enumerate(pl):
            if p not in per:
                continue
            od = info[p][1] - 1
            ot = dev2tile(od + 1)
            t0 = float(pa[p]["first_ns"])
            R = per[p]
            subs = [None] if gran == "page" else [0, 1, 2, 3]
            for b in subs:
                cnt = {g: (sum(v) if b is None else v[b]) for g, v in R.items()}
                tot = sum(cnt.values())
                if tot == 0:
                    continue
                g, v = max(cnt.items(), key=lambda x: (x[1], -x[0]))
                tgt = tile2dev(g) - 1 if v >= SHARE * tot else -1
                rr = sum(x for gg, x in cnt.items() if gg != ot)
                idx = i if b is None else 4 * i + b
                U.append((blk, idx, od, tgt, rr, t0))
                META.append((p, -1 if b is None else b))
    return U


def analyse(U):
    out = {}
    byb = collections.defaultdict(list)
    for u in U:
        byb[u[0]].append(u)
    mig_n = sum(1 for u in U if u[3] >= 0 and u[3] != u[2])
    mig_r = sum(u[4] for u in U if u[3] >= 0 and u[3] != u[2])
    out["units"] = len(U)
    out["mig_units"] = mig_n
    out["mig_reads"] = mig_r
    out["mig_frac_reads"] = mig_r / max(1, sum(u[4] for u in U))
    # ---------------- C1 global fit
    hits = collections.Counter()
    for blk, L in byb.items():
        M = sorted([u for u in L if u[3] >= 0 and u[3] != u[2]], key=lambda u: u[1])
        if not M:
            continue
        I = np.array([u[1] for u in M], dtype=np.int64)
        D = np.array([u[3] for u in M], dtype=np.int64)
        O = np.array([u[2] for u in M], dtype=np.int64)
        Wt = np.array([u[4] for u in M], dtype=np.float64) + 1e-6
        h = {}
        same = np.zeros(len(M), dtype=bool)
        if len(M) > 1:
            same[1:] = D[1:] == D[:-1]
            same[0] = D[0] == D[1]
        h["const"] = Wt[same].sum()
        p = fit_lin(I, D, Wt, refine=True)
        h["lin"] = sum(Wt[k] for k in range(len(M)) if pred_lin(p, I[k]) == D[k]) if p else (Wt.sum() if len(M) == 1 else 0)
        p = fit_step(I, D, Wt)
        h["step"] = Wt[((p[1] + p[0] * I) % 48) == D].sum() if p else Wt.sum()
        dl = wmode(D - O, Wt)
        h["owner"] = Wt[((O + dl) % 48) == D].sum()
        h["best"] = max(h.values())
        for k, v in h.items():
            hits[k] += v
    out["global_hit"] = {k: v / max(1, mig_r) for k, v in hits.items()}
    # ---------------- C2 online
    order = sorted(range(len(U)), key=lambda k: (U[k][5], U[k][0], U[k][1]))

    def fit(r, H):
        I = np.array([x[0] for x in H]); D = np.array([x[1] for x in H])
        O = np.array([x[2] for x in H]); Wt = np.array([x[3] for x in H], dtype=float) + 1e-6
        if r == "const":
            return (I, D)
        if r == "owner":
            return wmode(D - O, Wt)
        if r == "step":
            return fit_step(I, D, Wt) if len(H) >= 2 else None
        return fit_lin_online(I, D, Wt) if len(H) >= 2 else None

    def predict(r, p, idx, od):
        if p is None:
            return None
        if r == "const":
            I, D = p
            return int(D[np.argmin(np.abs(I - idx))])
        if r == "owner":
            return (od + p) % 48
        if r == "step":
            return (p[1] + p[0] * idx) % 48
        return pred_lin(p, idx)

    for m in MS:
        st = {}
        res = collections.Counter()
        obs_hit = collections.Counter()
        for k in order:
            blk, idx, od, tgt, rr, t0 = U[k]
            S = st.get(blk)
            if S is None:
                S = st[blk] = dict(hist=[], par={r: None for r in RULES}, streak={r: 0 for r in RULES},
                                   conf=None, n=0)
            if S["conf"] is not None or S["n"] >= GIVEUP:
                continue
            if not (tgt >= 0 and tgt != od):
                continue
            S["n"] += 1
            S["hist"].append((idx, tgt, od, rr))
            if len(S["hist"]) > HIST:
                S["hist"].pop(0)
            for r in RULES:
                pr = predict(r, S["par"][r], idx, od)
                obs_hit[(r, "n")] += rr
                if pr == tgt:
                    obs_hit[(r, "hit")] += rr
                    S["streak"][r] += 1
                    if r == "const":
                        S["par"][r] = fit(r, S["hist"])
                else:
                    S["streak"][r] = 0
                    S["par"][r] = fit(r, S["hist"])
            for r in ("owner", "const", "step", "lin"):
                if S["streak"][r] >= m:
                    S["conf"] = (r, t0, S["par"][r])
                    break
        for blk, S in st.items():
            if S["conf"] is None:
                continue
            r, tc, p = S["conf"]
            L = byb[blk]
            res["conf_buffers"] += 1
            res["conf_units"] += len(L)
            res["conf_obs"] += S["n"]
            for u in L:
                if u[5] <= tc:
                    continue
                _, idx, od, tgt, rr, _ = u
                pr = predict(r, p, idx, od)
                res["unacc"] += 1
                res["pre_units"] += 1
                res["pre_reads"] += rr
                if tgt >= 0:
                    res["pre_t_units"] += 1
                    res["pre_t_reads"] += rr
                    if pr != tgt and tgt == od:
                        res["harm_units"] += 1
                        res["harm_reads"] += rr
                if (tgt >= 0 and pr == tgt):
                    res["ok_units"] += 1
                    res["ok_reads"] += rr
                    if tgt != od:
                        res["cov_units"] += 1
                        res["cov_reads"] += rr
                res["rule_" + r] += 1
        # ---- rollback variant: confidence is lost on a mispredicted observation and regained
        # after m new consecutive hits; every unit is placed by the rule that is confident at
        # its first access (re-placing untouched units is free), otherwise left to migration.
        st2 = {}
        rb = collections.Counter()
        if m == DUMP_M:
            placed = {}
            out["_placed"] = placed
        for k in order:
            blk, idx, od, tgt, rr, t0 = U[k]
            S = st2.get(blk)
            if S is None:
                S = st2[blk] = dict(hist=[], par={r: None for r in RULES}, streak={r: 0 for r in RULES},
                                    conf=None, n=0, ever=False)
            mig = tgt >= 0 and tgt != od
            if S["conf"] is not None:
                r, p = S["conf"]
                pr = predict(r, p, idx, od)
                if m == DUMP_M:
                    placed[k] = pr
                rb["pre_units"] += 1
                rb["pre_reads"] += rr
                if tgt >= 0:
                    rb["pre_t_units"] += 1
                    rb["pre_t_reads"] += rr
                if tgt >= 0 and pr == tgt:
                    rb["ok_units"] += 1
                    rb["ok_reads"] += rr
                    if mig:
                        rb["cov_units"] += 1
                        rb["cov_reads"] += rr
                elif tgt >= 0:
                    # the unit's real main reader (owner included) differs: detectable -> rollback
                    rb["rollbacks"] += 1
                    if not mig and pr != od:
                        rb["harm_units"] += 1
                        rb["harm_reads"] += rr
                    S["conf"] = None
                    for q in RULES:
                        S["streak"][q] = 0
                if not mig:
                    continue
                S["hist"].append((idx, tgt, od, rr))
                if len(S["hist"]) > HIST:
                    S["hist"].pop(0)
                if S["conf"] is None:
                    for q in RULES:
                        S["par"][q] = fit(q, S["hist"])
                continue
            if not mig or (not S["ever"] and S["n"] >= GIVEUP):
                continue
            S["n"] += 1
            S["hist"].append((idx, tgt, od, rr))
            if len(S["hist"]) > HIST:
                S["hist"].pop(0)
            for q in RULES:
                pr = predict(q, S["par"][q], idx, od)
                if pr == tgt:
                    S["streak"][q] += 1
                    if q == "const":
                        S["par"][q] = fit(q, S["hist"])
                else:
                    S["streak"][q] = 0
                    S["par"][q] = fit(q, S["hist"])
            for q in ("owner", "const", "step", "lin"):
                if S["streak"][q] >= m:
                    S["conf"] = (q, S["par"][q])
                    S["ever"] = True
                    break
        out_rb = dict(prec_units=rb["ok_units"] / rb["pre_t_units"] if rb["pre_t_units"] else None,
                      prec_reads=rb["ok_reads"] / rb["pre_t_reads"] if rb["pre_t_reads"] else None,
                      notarget_units=1 - rb["pre_t_units"] / rb["pre_units"] if rb["pre_units"] else None,
                      harm_units=rb["harm_units"] / max(1, len(U)), harm_reads=rb["harm_reads"] / max(1, sum(u[4] for u in U)),
                      cover_bytes=rb["cov_units"] / max(1, mig_n), cover_reads=rb["cov_reads"] / max(1, mig_r),
                      rollbacks=rb["rollbacks"])
        out["m%d" % m] = dict(rb=out_rb, 
            conf_buffers=res["conf_buffers"], buffers=len(byb),
            hit_online={r: obs_hit[(r, "hit")] / obs_hit[(r, "n")] if obs_hit[(r, "n")] else None for r in RULES},
            prec_units=res["ok_units"] / res["pre_t_units"] if res["pre_t_units"] else None,
            prec_reads=res["ok_reads"] / res["pre_t_reads"] if res["pre_t_reads"] else None,
            notarget_units=1 - res["pre_t_units"] / res["pre_units"] if res["pre_units"] else None,
            harm_units=res["harm_units"] / max(1, len(U)), harm_reads=res["harm_reads"] / max(1, sum(u[4] for u in U)),
            cover_bytes=res["cov_units"] / max(1, mig_n), cover_reads=res["cov_reads"] / max(1, mig_r),
            unacc_at_conf=res["unacc"] / res["conf_units"] if res["conf_units"] else None,
            obs_to_conf=res["conf_obs"] / res["conf_buffers"] if res["conf_buffers"] else None,
            rules={r: res["rule_" + r] for r in RULES if res["rule_" + r]})
    return out


def main(w, dump=None):
    res = dict(w=w)
    for gran in ("page", "blk1k"):
        res[gran] = analyse(units(w, gran))
        placed = res[gran].pop("_placed", {})
        if dump:
            with gzip.open("%s_%s.csv.gz" % (dump, gran), "wt") as fo:
                fo.write("page,sub,dev0\n")
                for k, d in placed.items():
                    fo.write("%d,%d,%d\n" % (META[k][0], META[k][1], d))
    print(json.dumps(res, ensure_ascii=False))


if __name__ == "__main__":
    main(sys.argv[1], sys.argv[2] if len(sys.argv) > 2 else None)
