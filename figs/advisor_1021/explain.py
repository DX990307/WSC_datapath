# Explanation figures (Chinese) for presenting CuPath ISCA'27 M1/M2/M3 to the advisors.
import html, random, sys, cairosvg
E = html.escape
F = "'Noto Sans CJK SC','Noto Sans CJK JP','Noto Sans CJK HK',sans-serif"
INK = "#1d2329"; MUT = "#6b7480"; LINE = "#c3cad2"; FILL = "#f2f4f6"; PANEL = "#fafbfc"
M1 = "#d9822b"; M1L = "#fbe9d6"     # new (orange)
M2 = "#1f5a8a"; M2L = "#dce8f2"     # HPCA, already there (blue)
M3 = "#2e8b7a"; M3L = "#d6efe9"     # HPCA, not the focus (teal)
RED = "#b8463c"; REDL = "#f6e1de"
W, H = 1600, 900

class S:
    def __init__(s, label): s.b = []; s.label = label
    def add(s, x): s.b.append(x)
    def text(s, x, y, t, size=20, anchor='start', weight='normal', fill=INK, mono=False):
        fam = "'DejaVu Sans Mono',monospace" if mono else F
        s.add(f'<text x="{x:.1f}" y="{y:.1f}" font-family="{fam}" font-size="{size}" font-weight="{weight}" fill="{fill}" text-anchor="{anchor}">{E(t)}</text>')
    def rect(s, x, y, w, h, fill=FILL, stroke=INK, sw=1.5, rx=6, dash=None):
        d = f' stroke-dasharray="{dash}"' if dash else ''
        s.add(f'<rect x="{x:.1f}" y="{y:.1f}" width="{w:.1f}" height="{h:.1f}" rx="{rx}" fill="{fill}" stroke="{stroke}" stroke-width="{sw}"{d}/>')
    def line(s, pts, stroke=INK, sw=2, arrow=None, dash=None):
        d = f' stroke-dasharray="{dash}"' if dash else ''
        m = f' marker-end="url(#ah-{arrow})"' if arrow else ''
        P = ' '.join(f'{x:.1f},{y:.1f}' for x, y in pts)
        s.add(f'<polyline points="{P}" fill="none" stroke="{stroke}" stroke-width="{sw}" stroke-linecap="round" stroke-linejoin="round"{d}{m}/>')
    def circle(s, x, y, r, fill=INK, stroke='none', sw=1.5, dash=None):
        d = f' stroke-dasharray="{dash}"' if dash else ''
        s.add(f'<circle cx="{x:.1f}" cy="{y:.1f}" r="{r}" fill="{fill}" stroke="{stroke}" stroke-width="{sw}"{d}/>')
    def badge(s, x, y, n, fill=INK, r=15):
        s.circle(x, y, r, fill=fill); s.text(x, y + 7, str(n), size=19, anchor='middle', weight='bold', fill='#fff')
    def chip(s, x, y, t, col, light, size=18):
        w = 14 + size * 0.62 * sum(2 if ord(c) > 255 else 1 for c in t) / 1.0 * 0.92
        s.rect(x, y - size - 4, w, size + 14, fill=light, stroke=col, sw=1.5, rx=(size + 14) / 2)
        s.text(x + w / 2, y + 1, t, size=size, anchor='middle', weight='bold', fill=col)
        return w
    def title(s, t, sub=None):
        s.text(50, 66, t, size=36, weight='bold')
        if sub: s.text(50, 108, sub, size=22, fill=MUT)
    def svg(s):
        defs = '<defs>'
        for k, c in [('k', INK), ('m', MUT), ('1', M1), ('2', M2), ('3', M3), ('r', RED)]:
            defs += (f'<marker id="ah-{k}" viewBox="0 0 10 10" refX="9" refY="5" markerUnits="userSpaceOnUse" '
                     f'markerWidth="16" markerHeight="16" orient="auto-start-reverse"><path d="M0,1 L10,5 L0,9 z" fill="{c}"/></marker>')
        defs += '</defs>'
        return (f'<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 {W} {H}" width="{W}" height="{H}" role="img" aria-label="{E(s.label)}">'
                f'<rect width="{W}" height="{H}" fill="#ffffff"/>{defs}' + ''.join(s.b) + '</svg>')

def mesh(s, ox, oy, cell, gap, roles=None, lab=None):
    """7x7 mesh; roles: {(x,y): (label, kind)} kind in r/o/g. Returns P(x,y)->centre."""
    roles = roles or {}
    st = cell + gap
    P = lambda x, y: (ox + x * st + cell / 2, oy + y * st + cell / 2)
    for y in range(7):
        for x in range(7):
            cx, cy = P(x, y)
            if x < 6: s.line([(cx + cell / 2, cy), (cx + st - cell / 2, cy)], stroke=LINE, sw=2)
            if y < 6: s.line([(cx, cy + cell / 2), (cx, cy + st - cell / 2)], stroke=LINE, sw=2)
    for y in range(7):
        for x in range(7):
            cx, cy = P(x, y)
            if (x, y) == (3, 3):
                s.rect(cx - cell / 2, cy - cell / 2, cell, cell, fill='#fff', stroke=MUT, sw=1.2, dash='4 3', rx=5)
                s.text(cx, cy + 6, 'IO', size=max(13, cell * 0.32), anchor='middle', fill=MUT); continue
            if (x, y) in roles:
                l, k = roles[(x, y)]
                col, light = {'r': (M1, M1L), 'o': (M2, M2L), 'g': (M3, M3L), 'x': (RED, REDL)}[k]
                s.rect(cx - cell / 2, cy - cell / 2, cell, cell, fill=light, stroke=col, sw=2.5, rx=5)
                s.text(cx, cy + cell * 0.16, l, size=max(14, cell * 0.38), anchor='middle', weight='bold', fill=col)
            else:
                s.rect(cx - cell / 2, cy - cell / 2, cell, cell, fill=FILL, stroke=LINE, sw=1.2, rx=5)
    return P

def yx(P, a, b):
    """dimension-order route, Y first then X (as in the T18 implementation)"""
    (x1, y1), (x2, y2) = a, b
    pts = [P(x1, y1)]
    if y1 != y2: pts.append(P(x1, y2))
    if x1 != x2: pts.append(P(x2, y2))
    return pts

def shorten(pts, d):
    (x1, y1), (x2, y2) = pts[-2], pts[-1]
    L = ((x2 - x1) ** 2 + (y2 - y1) ** 2) ** 0.5 or 1
    return pts[:-1] + [(x2 - (x2 - x1) / L * d, y2 - (y2 - y1) / L * d)]

def retile(s, P, roles, cell):
    for (x, y), (l, k) in roles.items():
        cx, cy = P(x, y)
        col, light = {'r': (M1, M1L), 'o': (M2, M2L), 'g': (M3, M3L), 'x': (RED, REDL)}[k]
        s.rect(cx - cell / 2, cy - cell / 2, cell, cell, fill=light, stroke=col, sw=2.5, rx=5)
        s.text(cx, cy + cell * 0.16, l, size=max(14, cell * 0.38), anchor='middle', weight='bold', fill=col)

def factbox(s, x, y, w, h, head, lines, col=RED, light=REDL):
    s.rect(x, y, w, h, fill=light, stroke=col, sw=1.5, rx=10)
    s.text(x + 20, y + 38, head, size=23, weight='bold', fill=col)
    for i, l in enumerate(lines): s.text(x + 20, y + 76 + i * 32, l, size=19)

# ------------------------------------------------------------------ fig 1
def fig1():
    s = S('baseline bottleneck')
    s.title('图 1  基线的瓶颈：请求方的 RDMA 引擎只能同时挂 64 个远程请求',
            'RDMA 引擎是每个 GPM 连到 mesh 的网络接口，两端各有一张有限的表；中间每一跳只过 switch，不碰 RDMA')
    roles1 = {(1, 4): ('R', 'r'), (5, 1): ('O', 'o')}
    P = mesh(s, 60, 175, 50, 12, roles1)
    s.line(shorten(yx(P, (1, 4), (5, 1)), 28), stroke=M1, sw=5, arrow='1')
    s.line(shorten([(p[0] + 8, p[1] + 8) for p in yx(P, (5, 1), (1, 4))], 36), stroke=M2, sw=3, dash='8 6', arrow='2')
    retile(s, P, roles1, 50)
    s.text(P(1, 2)[0] - 34, P(1, 2)[1], '请求', size=19, anchor='end', fill=M1, weight='bold')
    s.text(P(3, 4)[0], P(3, 4)[1] + 44, '响应', size=19, anchor='middle', fill=M2, weight='bold')
    s.text(271, 642, '这一次 7 跳：只有 R、O 两端经过 RDMA，', size=18, anchor='middle', fill=MUT)
    s.text(271, 668, '中间每跳只过 switch（约 72 ns / 跳）', size=18, anchor='middle', fill=MUT)
    s.rect(560, 160, 980, 500, fill=PANEL, stroke=LINE, sw=1.5, rx=14)
    s.text(585, 198, '两端的 RDMA 引擎各有一张有限的表（每个方向最多 64 项）', size=23, weight='bold')
    # requester RDMA: queue + req table
    s.rect(580, 220, 360, 330, fill='#fff', stroke=M1, sw=2, rx=12)
    s.text(600, 252, 'R 的 RDMA（请求方）', size=20, weight='bold', fill=M1)
    s.text(600, 288, '等空位', size=17, fill=M1)
    for i in range(32):
        s.circle(612 + (i % 4) * 22, 308 + (i // 4) * 24, 7.5, fill=M1L, stroke=M1, sw=1.3)
    s.line([(692, 400), (712, 400)], stroke=INK, sw=2, arrow='k')
    s.text(718, 288, '请求表 64 项（全满）', size=17, weight='bold', fill=M1)
    for i in range(64):
        s.rect(718 + (i % 8) * 26, 302 + (i // 8) * 26, 22, 22, fill=M1, stroke=M1, sw=1, rx=3)
    s.text(600, 532, '发出去、在等响应的请求', size=16, fill=MUT)
    # mesh
    s.text(1055, 284, 'mesh', size=20, anchor='middle', weight='bold', fill=MUT)
    s.rect(965, 300, 180, 170, fill=FILL, stroke=LINE, sw=1.5, rx=10)
    for x in (985, 1040, 1095):
        s.rect(x, 368, 30, 30, fill='#fff', stroke=MUT, sw=1.5, rx=5)
    s.line([(1015, 383), (1040, 383)], stroke=MUT, sw=1.5); s.line([(1070, 383), (1095, 383)], stroke=MUT, sw=1.5)
    s.text(1055, 425, 'switch，不碰 RDMA', size=15, anchor='middle', fill=MUT)
    s.text(1055, 505, '链路利用率 < 3%', size=19, anchor='middle', weight='bold', fill=RED)
    # owner RDMA: owner table + L2
    s.rect(1170, 220, 350, 330, fill='#fff', stroke=M2, sw=2, rx=12)
    s.text(1190, 252, 'O 的 RDMA（owner）', size=20, weight='bold', fill=M2)
    s.text(1190, 288, 'owner 表 64 项', size=17, weight='bold', fill=M2)
    for i in range(64):
        on = i < 26
        s.rect(1190 + (i % 8) * 26, 302 + (i // 8) * 26, 22, 22, fill=M2 if on else '#fff', stroke=M2 if on else LINE, sw=1, rx=3)
    s.rect(1415, 330, 90, 140, fill=FILL, stroke=INK, sw=1.5, rx=8)
    s.text(1460, 392, 'L2 /', size=18, anchor='middle'); s.text(1460, 420, 'HBM', size=18, anchor='middle')
    s.text(1190, 532, '别的 GPM 发来、在等本地 L2 的请求', size=16, fill=MUT)
    # request / response between the two ends
    s.line([(930, 335), (962, 335)], stroke=M1, sw=3, arrow='1'); s.line([(1148, 335), (1182, 335)], stroke=M1, sw=3, arrow='1')
    s.text(1055, 330, '请求', size=16, anchor='middle', fill=M1, weight='bold')
    s.line([(1182, 450), (1150, 450)], stroke=M2, sw=3, dash='7 5', arrow='2'); s.line([(962, 450), (932, 450)], stroke=M2, sw=3, dash='7 5', arrow='2')
    s.text(1055, 460, '响应', size=16, anchor='middle', fill=M2, weight='bold')
    s.text(585, 598, '请求表的一项：请求离开 R 时占上，响应回到 R 才释放，占满整个往返 → 跳数越多，占得越久', size=19, weight='bold', fill=M1)
    s.text(585, 634, 'owner 表的一项：只在 owner 读 L2 / HBM 期间占用，和跳数无关；同一条 line 的请求在这里也是各占一项', size=17, fill=M2)
    factbox(s, 50, 690, 480, 170, '带宽被表项卡住', ['跳数越多，往返越长，每项占得越久：', '远程带宽 = 64 × 64 B ÷ 往返时间', '1 跳 26.4 B/周期，6 跳只剩 8.0'])
    factbox(s, 560, 690, 480, 170, '时间花在排队上', ['按闭环模型，排队占远程延迟 80–86%', '（T17 的三个 workload）', '排队就发生在请求表门口'])
    factbox(s, 1070, 690, 480, 170, '链路是闲的', ['链路利用率 < 3%', '受限的是两端 RDMA 的表，', '不是链路和 switch'])
    return s

# ------------------------------------------------------------------ fig 2
def fig2():
    s = S('why the tracker must grow on a wafer')
    s.title('图 2  为什么 wafer 上才严重：需要的跟踪表项随距离增长',
            'RDMA 的一项从请求发出占到响应回来；中间每跳不碰 RDMA，但跳数越多往返越长，同样的带宽就要更多项')
    x0, sc = 330, 680 / 1500
    rows = [('1 跳', 242), ('平均 4.3 跳', 611), ('6 跳', 803), ('12 跳（对角）', 1477)]
    for k, v in (0, 0), (500, 0), (1000, 0), (1500, 0):
        X = x0 + k * sc
        s.line([(X, 190), (X, 560)], stroke=LINE, sw=1)
        s.text(X, 588, f'{k:,}', size=18, anchor='middle', fill=MUT)
    for i, (lab, v) in enumerate(rows):
        y = 205 + i * 88
        s.text(x0 - 18, y + 36, lab, size=22, anchor='end')
        s.rect(x0, y, v * sc, 54, fill=M2L, stroke=M2, sw=1.5, rx=4)
        s.text(x0 + v * sc + 14, y + 36, f'{v:,} 项', size=22, weight='bold', fill=M2)
    X64 = x0 + 64 * sc
    s.line([(X64, 185), (X64, 565)], stroke=RED, sw=3, dash='9 6')
    s.text(X64 - 6, 628, '今天：64 项', size=22, weight='bold', fill=RED)
    s.text(X64 - 6, 656, '≈ MI250X 级端点；AMD NoC2 的 NMU 也是 64 个在途读', size=18, fill=RED)
    s.rect(1130, 185, 420, 400, fill=PANEL, stroke=LINE, sw=1.5, rx=14)
    L = [('需要的在途数', 24, 'bold', INK), ('= 远程带宽 × 往返时间 ÷ 64 B', 21, 'normal', INK),
         ('按每 GPM 100 B/ns 远程带宽', 19, 'normal', MUT), ('往返 ≈ 84 + 71.4 ns × 跳数', 19, 'normal', MUT),
         ('', 10, 'normal', INK), ('一个 GPM 的链路 768 GB/s，', 21, 'normal', INK), ('往返 155–945 ns', 21, 'normal', INK),
         ('→ 填满链路要 120–730 KB 在途', 21, 'bold', M2), ('64 项 × 64 B 只有 4 KB，', 21, 'normal', INK), ('差 30–180 倍', 21, 'bold', RED)]
    y = 228
    for t, sz, wt, c in L:
        s.text(1152, y, t, size=sz, weight=wt, fill=c); y += sz + 16
    s.rect(50, 700, 1500, 160, fill=M1L, stroke=M1, sw=1.5, rx=12)
    s.text(75, 745, 'wafer 的链路更宽、距离更远，需要的表项比多芯片 GPU 大一个数量级；全相联 CAM 做不到几千项 → M1 的出发点', size=21, weight='bold', fill=INK)
    s.text(75, 790, '还要做：3×3 / 5×5 / 7×7 的规模实验，证明收益随 GPM 数增长（回答"为什么对 wafer 独特"）', size=20, fill=INK)
    s.text(75, 830, '注：MGPUSim 上游没有这个上限，64 是我们 fork 加的；论文里写成"沿用今天多芯片 GPU 的端点设计"，并扫 64 / 256 / 1024 / 4096', size=18, fill=MUT)
    return s

# ------------------------------------------------------------------ fig 3
def fig3():
    s = S('M2 remote aggregation')
    s.title('图 3  M2（HPCA 版 Remote Aggregation，已有）：在请求方合并、打包',
            '同一个 GPM 发出的远程读，在进入 mesh 之前先去重，再把同一页的 line 打成一个包')
    s.rect(50, 150, 1010, 620, fill=PANEL, stroke=LINE, sw=1.5, rx=14)
    s.text(75, 188, '请求方 GPM', size=22, weight='bold')
    s.rect(1260, 300, 290, 330, fill=M2L, stroke=M2, sw=2, rx=14)
    s.text(1405, 345, 'owner O', size=24, anchor='middle', weight='bold', fill=M2)
    s.text(1405, 400, '拆开位图', size=20, anchor='middle')
    s.text(1405, 430, '→ 按 line 读 L2', size=20, anchor='middle')
    s.text(1405, 500, '每条 line', size=20, anchor='middle')
    s.text(1405, 530, '单独返回', size=20, anchor='middle')
    # (a) same-line merge
    s.text(75, 238, '(a) 同 line 合并', size=23, weight='bold', fill=M2)
    for i, (cu, y) in enumerate([('CU0', 262), ('CU2', 332), ('CU3', 402)]):
        s.rect(80, y, 110, 52, fill='#fff', stroke=INK, sw=1.5, rx=8); s.text(135, y + 34, cu, size=20, anchor='middle')
        s.line([(190, y + 26), (420, 330)], stroke=M2, sw=2, arrow='2')
    s.text(235, 262, '都读 line A', size=17, fill=M2, weight='bold')
    s.rect(425, 285, 400, 110, fill='#fff', stroke=M2, sw=2, rx=10)
    s.text(445, 318, 'line', size=17, fill=MUT); s.text(560, 318, '等待者', size=17, fill=MUT)
    s.text(445, 365, 'A', size=26, weight='bold', fill=M2)
    for i, cu in enumerate(['CU0', 'CU2', 'CU3']):
        s.rect(560 + i * 82, 340, 72, 36, fill=M2L, stroke=M2, sw=1.2, rx=18); s.text(596 + i * 82, 365, cu, size=17, anchor='middle', fill=M2)
    s.text(430, 422, '第一个请求发出去，后来的挂在后面等同一个响应', size=17, fill=MUT)
    s.line([(825, 340), (1250, 400)], stroke=M2, sw=3, arrow='2')
    s.text(1000, 350, '只发 1 个请求', size=19, weight='bold', fill=M2)
    # (b) same-page batching + prefetch piggyback
    s.text(75, 488, '(b) 同页打包 + 预取捎带', size=23, weight='bold', fill=M2)
    s.rect(80, 512, 110, 52, fill='#fff', stroke=INK, sw=1.5, rx=8); s.text(135, 546, 'CU1', size=20, anchor='middle')
    s.text(80, 600, '读页 P 的', size=17, fill=MUT); s.text(80, 624, 'line 3、5、6', size=17, fill=MUT)
    s.line([(190, 538), (418, 590)], stroke=M2, sw=2, arrow='2')
    s.text(425, 520, '4 KB 页 P = 64 条 line（同一个 owner）', size=17, fill=MUT)
    for i in range(64):
        x, y = 425 + (i % 16) * 24, 535 + (i // 16) * 24
        if i in (3, 5, 6): s.rect(x, y, 20, 20, fill=M2, stroke=M2, sw=1, rx=3)
        elif i == 7: s.rect(x, y, 20, 20, fill=M1L, stroke=M1, sw=2, rx=3, dash='3 2')
        else: s.rect(x, y, 20, 20, fill='#fff', stroke=LINE, sw=1, rx=3)
    s.line([(425 + 7 * 24 + 10, 660), (425 + 7 * 24 + 10, 640)], stroke=M1, sw=2, arrow='1')
    s.text(430, 690, '步长预测的下一条（line 7），顺路捎上', size=17, fill=M1, weight='bold')
    s.line([(810, 585), (1250, 480)], stroke=M2, sw=3, arrow='2')
    s.text(840, 632, '1 个位图包（最多 8 条 line）', size=19, weight='bold', fill=M2)
    s.line([(1405, 632), (1405, 735), (1068, 735)], stroke=M2, sw=2.5, dash='8 6', arrow='2')
    s.text(1240, 762, '响应（每条 line 一个）', size=18, anchor='middle', fill=M2)
    s.text(50, 818, '实现：每个包最多 8 条 line，最多 64 个正在收集的页；预测器只在同一 owner、同一页里预测，不多发包。', size=20)
    s.text(50, 856, '在 Complete 里，同 line 合并交给 M1 的跟踪表（M2 自己的 filter 去重关掉），M2 保留打包和预取。', size=20, fill=M1, weight='bold')
    return s

# ------------------------------------------------------------------ fig 4
def fig4():
    s = S('M1 part 1: scaled tracker')
    s.title('图 4  M1 新 1：把 RDMA 的请求表从 64 项做到 1024 项，用 cuckoo 索引代替 CAM',
            '项数够了，请求不用在门口排队；查表只读两个桶，再用完整地址核对')
    s.text(60, 170, '之前：64 项', size=22, weight='bold', fill=RED)
    for i in range(64):
        x, y = 62 + (i % 8) * 26, 190 + (i // 8) * 26
        s.rect(x, y, 22, 22, fill=M1, stroke=M1, sw=1, rx=3)
    for i in range(24):
        s.circle(300 + (i % 4) * 26, 202 + (i // 4) * 30, 9, fill=M1L, stroke=M1, sw=1.2)
    s.text(285, 395, '门口排队', size=18, fill=RED)
    s.text(60, 450, '之后：1024 项', size=22, weight='bold', fill=M1)
    rnd = random.Random(5)
    for i in range(1024):
        x, y = 62 + (i % 32) * 8, 460 + (i // 32) * 8
        on = rnd.random() < 0.42
        s.rect(x, y, 6.5, 6.5, fill=M1 if on else '#fff', stroke=M1 if on else LINE, sw=0.6, rx=1.5)
    s.text(340, 560, '不再排队，', size=18, fill=M1, weight='bold')
    s.text(340, 590, '链路忙起来', size=18, fill=M1)
    # lookup
    s.rect(560, 150, 990, 560, fill=PANEL, stroke=LINE, sw=1.5, rx=14)
    s.text(585, 190, '一次查表（每个远程读都要查）', size=22, weight='bold')
    s.rect(590, 230, 190, 70, fill='#fff', stroke=INK, sw=1.5, rx=10)
    s.text(685, 273, 'line 地址', size=21, anchor='middle')
    s.line([(780, 255), (880, 255)], stroke=M1, sw=2.5, arrow='1'); s.text(830, 245, 'h1', size=18, anchor='middle', fill=M1)
    s.line([(780, 285), (880, 425)], stroke=M1, sw=2.5, arrow='1'); s.text(815, 370, 'h2', size=18, fill=M1)
    s.text(885, 222, 'cuckoo 表：每个桶 4 个槽，槽里放 12 位指纹', size=17, fill=MUT)
    for b in range(5):
        y = 232 + b * 48
        hot = b in (0, 4)
        for k in range(4):
            x = 885 + k * 92
            match = (b == 0 and k == 3)
            s.rect(x, y, 86, 40, fill=M1 if match else (M1L if hot else '#fff'), stroke=M1 if hot else LINE, sw=1.5 if hot else 1, rx=5)
            s.text(x + 43, y + 27, '0x' + format(rnd.randrange(4096), '03x'), size=15, anchor='middle', fill='#fff' if match else MUT, mono=True)
    s.line([(1249, 252), (1292, 252)], stroke=M1, sw=2.5, arrow='1')
    s.rect(1295, 222, 240, 80, fill='#fff', stroke=INK, sw=1.5, rx=10)
    s.text(1415, 256, '旁路阵列：核对完整地址', size=18, anchor='middle')
    s.text(1415, 286, '（指纹相同时）', size=16, anchor='middle', fill=M1)
    s.line([(1340, 302), (1340, 500), (1030, 500), (1030, 538)], stroke=M1, sw=2.5, arrow='1')
    s.text(1185, 490, '一致', size=17, anchor='middle', fill=M1, weight='bold')
    s.line([(1470, 302), (1470, 538)], stroke=INK, sw=2.5, arrow='k')
    s.text(1480, 420, '不一致', size=17, weight='bold')
    s.rect(885, 540, 300, 80, fill=M1L, stroke=M1, sw=1.5, rx=10)
    s.text(1035, 574, '挂到等待链', size=19, anchor='middle', weight='bold', fill=M1)
    s.text(1035, 604, '（合并，不发新请求）', size=17, anchor='middle', fill=M1)
    s.rect(1215, 540, 315, 80, fill='#fff', stroke=INK, sw=1.5, rx=10)
    s.text(1372, 574, '新分配一项', size=19, anchor='middle', weight='bold')
    s.text(1372, 604, '→ 拿窗口份额 → 发出', size=17, anchor='middle')
    s.text(590, 650, '指纹只做快速筛选；合并前一定核对完整地址，所以指纹撞了也不会读错数据', size=18, fill=MUT)
    s.text(590, 682, '冒烟：W = 1024 和 4096 的结果相同；owner 端的表也跟着放大到 W', size=18, fill=MUT)
    factbox(s, 50, 735, 740, 135, '为什么不用全相联 CAM', ['CAM 每次要和每一项比较，做到上千项代价很高；', 'cuckoo 每次只读 2 个桶（面积用 CACTI 对比，还没做）'], col=M1, light=M1L)
    factbox(s, 810, 735, 740, 135, '按距离分 credit（细节，可不讲）', ['每个目的地的份额按空载往返 84 + 71.4 ns × 跳数；', '空闲份额可以借；W ≥ 1024 时和共享池结果相同'], col=MUT, light=FILL)
    return s

# ------------------------------------------------------------------ fig 5
def fig5():
    s = S('M1 part 2: owner merge and multicast')
    s.title('图 5  M1 新 2：owner 端跨 GPM 合并 + 网内多播',
            '只放大跟踪表，很多 GPM 会同时读同一条 line，瓶颈转到 owner；owner 合并后只读一次，响应沿路分叉')
    roles = {(1, 4): ('R1', 'r'), (1, 6): ('R2', 'r'), (4, 5): ('R3', 'r'), (3, 6): ('R4', 'r'), (5, 1): ('O', 'o')}
    s.text(60, 175, '(a) 只放大跟踪表', size=24, weight='bold', fill=RED)
    P = mesh(s, 90, 205, 44, 10, roles)
    for r in [(1, 4), (1, 6), (4, 5), (3, 6)]:
        pts = shorten(yx(P, r, (5, 1)), 26)
        s.line(pts, stroke=M1, sw=3, arrow='1')
    retile(s, P, roles, 44)
    ox, oy = P(5, 1)
    for i in range(4):
        s.rect(ox + 36 + i * 30, oy - 12, 24, 24, fill=REDL, stroke=RED, sw=1.5, rx=4)
    s.text(ox + 36, oy - 26, '排队', size=17, fill=RED, weight='bold')
    s.text(60, 620, '同一条 line：读 4 次 L2、回 4 份响应', size=20)
    s.text(60, 652, 'incast：owner 封顶约 113 B/周期，往返停在 3–4 µs', size=20)
    s.text(60, 694, 'FW 冒烟：只放大跟踪表 2.26×', size=22, weight='bold', fill=RED)
    s.line([(790, 160), (790, 720)], stroke=LINE, sw=1.5)
    s.text(830, 175, '(b) + owner 合并 + 多播', size=24, weight='bold', fill=M1)
    Q = mesh(s, 860, 205, 44, 10, roles)
    tree = [[Q(5, 1), Q(5, 6), Q(1, 6)], [Q(5, 4), Q(1, 4)], [Q(5, 5), Q(4, 5)]]
    for seg_ in tree: s.line(seg_, stroke=M2, sw=5)
    retile(s, Q, roles, 44)
    for p in [Q(5, 4), Q(5, 5), (Q(3, 6)[0] + 27, Q(3, 6)[1])]:
        s.circle(p[0], p[1], 9, fill='#fff', stroke=M2, sw=3)
    a, b = Q(5, 1)
    s.rect(1290, 205, 260, 190, fill=M1L, stroke=M1, sw=2, rx=12)
    s.text(1420, 240, 'owner 合并表', size=20, anchor='middle', weight='bold', fill=M1)
    s.text(1310, 280, 'line A', size=18, weight='bold')
    for i, r in enumerate(['R1', 'R2', 'R3', 'R4']):
        s.rect(1310 + i * 58, 296, 50, 30, fill=M1, stroke=M1, sw=1, rx=4); s.text(1335 + i * 58, 318, r, size=15, anchor='middle', fill='#fff', weight='bold')
    s.text(1310, 352, '48 位请求者位图', size=16, fill=MUT)
    s.text(1310, 380, 'L2 只读 1 次', size=19, weight='bold', fill=M1)
    s.line([(1290, 300), (a + 26, b)], stroke=M1, sw=1.5, dash='5 4')
    s.text(830, 620, '一份响应在路由树的 switch 里复制（圆点处分叉）', size=20)
    s.text(830, 652, '链路占用：分别回 28 段 → 树 14 段', size=20)
    s.text(830, 694, 'FW 冒烟：两部分一起 8.56×（全本地上限 11.1× 的 77%）', size=22, weight='bold', fill=M1)
    s.rect(50, 740, 1500, 130, fill=FILL, stroke=LINE, sw=1.5, rx=12)
    s.text(75, 782, '合并窗口：该 line 的 L2 读完成之前到达的同 line 请求都能并进来；表满就退回原来的单播。', size=20)
    s.text(75, 818, '为什么对 wafer 有用：同一条 line 最多 47 个 GPM 在读（4 个 GPM 的 MCM 最多 3 个）；读者之间最近只隔 1.5–2.6 跳。', size=20)
    s.text(75, 852, '只开 owner 端几乎没用（FW 88.3 → 87.8 µs）：表项不够时请求到不了一起，两部分要一起开。', size=20, fill=M1, weight='bold')
    return s

# ------------------------------------------------------------------ fig 6
def fig6():
    s = S('M3 remote reuse')
    s.title('图 6  M3（HPCA 版 Remote Reuse，已有，非重点）：看到复用才留在本地 L2',
            '同一个 GPM 再次读到同一条远程 line 时，用本地 L2 里的副本，不再穿过晶圆')
    s.rect(50, 150, 700, 600, fill=PANEL, stroke=LINE, sw=1.5, rx=14)
    s.text(75, 190, '请求方 GPM', size=22, weight='bold')
    s.rect(1180, 205, 370, 265, fill=M2L, stroke=M2, sw=2, rx=14)
    s.text(1365, 280, 'mesh → owner', size=24, anchor='middle', weight='bold', fill=M2)
    s.text(1365, 320, '往返几百 ns 到几 µs', size=19, anchor='middle', fill=M2)
    s.text(470, 205, '请求方 L2（一组的 8 个 way）', size=17, fill=MUT)
    steps = [(250, '①', '第一次读 line B', '走远程；只记下"见过"，不留', False),
             (430, '②', '第二次读 B', '走远程；回来后留在本地 L2', True),
             (610, '③', '之后再读 B', '本地 L2 命中，不过 mesh', None)]
    for y, n, a, b, keep in steps:
        s.text(80, y, n + ' ' + a, size=22, weight='bold', fill=M3 if keep is not False else INK)
        s.text(110, y + 34, b, size=19, fill=MUT)
        for k in range(8):
            fill = '#fff'
            if k in (1, 4): fill = FILL
            if keep is True and k == 6: fill = M3
            if keep is None and k == 6: fill = M3
            s.rect(470 + k * 32, y - 22, 28, 28, fill=fill, stroke=M3 if (keep is not False and k == 6) else LINE, sw=1.5, rx=4)
        if keep is None:
            s.line([(420, y - 8), (660, y - 8)], stroke=M3, sw=3, arrow='3')
        else:
            s.line([(740, y - 12), (1170, y - 12)], stroke=M1, sw=3, arrow='1')
            s.line([(1170, y + 10), (740 if keep is False else 680, y + 10)], stroke=M2, sw=3, dash='8 6', arrow='2')
    s.text(470, 704, '灰色：本地数据；绿色：留下的远程副本', size=16, fill=MUT)
    s.rect(1180, 500, 370, 130, fill=M3L, stroke=M3, sw=1.5, rx=10)
    s.text(1200, 538, '什么时候留', size=20, weight='bold', fill=M3)
    s.text(1200, 572, '第二次读到；或 ≥2 个请求在等它；', size=17)
    s.text(1200, 600, '或预取被用上', size=17)
    s.rect(50, 775, 1500, 100, fill=FILL, stroke=LINE, sw=1.5, rx=12)
    s.text(75, 812, '只占无效的 way 或替换别的远程副本，不挤掉本地数据；写照常发给 owner，本地副本一起更新。', size=19)
    s.text(75, 850, '还要补：kernel 边界的失效（论文写了，实现还没有）；模拟器的 L2 命中偏快（129 ns），M3 的收益可能偏高。', size=19, fill=RED)
    return s

# ------------------------------------------------------------------ fig 7
def fig7():
    s = S('Complete = M1 + M2 + M3')
    s.title('图 7  Complete = M1 + M2 + M3：一次远程读经过的每一步',
            '颜色表示这一步由哪个机制负责')
    col = {'M1': (M1, M1L), 'M2': (M2, M2L), 'M3': (M3, M3L), '': (INK, '#fff')}
    req = [(1, '远程 line，L1V 缺失', ''), (2, '先查请求方 L2：命中就直接返回', 'M3'),
           (3, '跟踪表查重：同 line 在途就挂上去', 'M1'), (4, '同页打包，预测的下一条顺路捎上', 'M2'),
           (5, '拿窗口份额（W = 1024，按距离分 credit）', 'M1')]
    back = [(9, '各请求方用跟踪表认领，唤醒等待者', 'M1'), (10, '准入的 line 回填请求方 L2', 'M3')]
    own = [(6, 'owner 合并表：同 line 只留一项', 'M1'), (7, '读一次 L2 / HBM', ''),
           (8, '沿路由树多播（没人合并的照常单播）', 'M1')]
    s.text(60, 165, '请求方 GPM', size=22, weight='bold'); s.text(1035, 165, 'owner GPM', size=22, weight='bold')
    def box(x, y, n, t, m, w=560):
        c, l = col[m]
        s.rect(x, y, w, 62, fill=l, stroke=c, sw=2, rx=10)
        s.badge(x + 30, y + 31, n, fill=c)
        s.text(x + 58, y + 39, t, size=20)
        if m: s.text(x + w - 16, y + 39, m, size=20, anchor='end', weight='bold', fill=c)
    ys = [185 + i * 80 for i in range(5)]
    for (n, t, m), y in zip(req, ys): box(60, y, n, t, m)
    for i in range(4): s.line([(200, ys[i] + 62), (200, ys[i + 1])], stroke=INK, sw=2, arrow='k')
    for (n, t, m), y in zip(back, [640, 720]): box(60, y, n, t, m)
    s.line([(200, 702), (200, 720)], stroke=INK, sw=2, arrow='k')
    for (n, t, m), y in zip(own, [265, 400, 535]): box(1035, y, n, t, m, w=510)
    s.line([(1290, 327), (1290, 400)], stroke=INK, sw=2, arrow='k'); s.line([(1290, 462), (1290, 535)], stroke=INK, sw=2, arrow='k')
    s.rect(700, 250, 250, 380, fill=FILL, stroke=LINE, sw=1.5, rx=14)
    s.text(825, 290, 'mesh', size=24, anchor='middle', weight='bold', fill=MUT)
    s.line([(620, 536), (720, 536), (720, 300), (1025, 300)], stroke=M1, sw=3, arrow='1')
    s.text(825, 340, '请求（单条或位图包）', size=17, anchor='middle', fill=M1)
    s.line([(1030, 566), (900, 566), (900, 671), (630, 671)], stroke=M2, sw=3, dash='8 6', arrow='2')
    s.text(825, 610, '多播响应', size=17, anchor='middle', fill=M2)
    lx = 1035
    for i, (m, t) in enumerate([('M1', '新：跟踪表规模化 + owner 合并多播'), ('M2', 'HPCA 已有：打包、预取'), ('M3', 'HPCA 已有：本地复用，非重点')]):
        c, l = col[m]
        y = 650 + i * 40
        s.rect(lx, y - 20, 30, 26, fill=l, stroke=c, sw=2, rx=5); s.text(lx + 42, y, m + ' ' + t, size=18, fill=c, weight='bold')
    s.text(60, 860, '代码：isca27/m1m2 的 complete_new = T18 全开（W = 1024 + 按距离分 credit）+ 旧 m2 + 旧 m3；去重归 M1；Local Pairing 不进论文', size=18, fill=MUT)
    return s

# ------------------------------------------------------------------ fig 8
def fig8():
    s = S('preliminary results')
    s.title('图 8  初步结果（冒烟和中小规模运行，正式评估在跑）',
            '左：M1 单独；右：Complete 在 16 个新 benchmark 上')
    # left: vertical bars
    x0, y0, hsc = 120, 600, 380 / 12
    for k in range(0, 13, 2):
        y = y0 - k * hsc
        s.line([(x0, y), (720, y)], stroke=LINE, sw=1)
        s.text(x0 - 12, y + 6, f'{k}×', size=17, anchor='end', fill=MUT)
    bars = [('FW', '只放大跟踪表', 2.26, MUT, FILL), ('FW', 'M1 全开', 8.56, M1, M1L), ('PagedAttention', 'M1 全开', 3.34, M1, M1L), ('R-GAT', 'M1 全开', 2.49, M1, M1L)]
    for i, (w_, c_, v, c, l) in enumerate(bars):
        x = x0 + 30 + i * 145
        s.rect(x, y0 - v * hsc, 100, v * hsc, fill=l, stroke=c, sw=2, rx=4)
        s.text(x + 50, y0 - v * hsc - 12, f'{v:.2f}×', size=21, anchor='middle', weight='bold', fill=c)
        s.text(x + 50, y0 + 30, w_, size=18, anchor='middle')
        s.text(x + 50, y0 + 56, c_, size=15, anchor='middle', fill=MUT)
    yO = y0 - 11.1 * hsc
    s.line([(x0 + 175, yO), (x0 + 275, yO)], stroke=RED, sw=3, dash='7 5')
    s.text(x0 + 285, yO + 6, '全本地上限 11.1×', size=17, fill=RED)
    s.text(x0, 180, 'M1（T18）单独，冒烟，W = 1024', size=22, weight='bold')
    s.line([(790, 160), (790, 700)], stroke=LINE, sw=1.5)
    # right: horizontal bars
    data = [('altis_cfd', 6.60), ('npbcg', 6.30), ('gups', 5.33), ('allreduce', 4.28), ('sssp', 4.12), ('npbis', 3.83), ('npb_mg', 3.62),
            ('ivfflat', 2.95), ('atax', 2.88), ('fft2d', 2.33), ('bfs', 1.77), ('shocsort', 1.62), ('rodinia_lud', 1.52), ('lonestar_bh', 1.21), ('embbag', 1.02)]
    s.text(830, 180, 'Complete，16 个新 benchmark（15 个有效）', size=22, weight='bold')
    bx, sc = 990, 520 / 7
    for k in range(0, 8):
        X = bx + k * sc
        s.line([(X, 205), (X, 655)], stroke=LINE, sw=1)
        s.text(X, 680, f'{k}×', size=16, anchor='middle', fill=MUT)
    for i, (n, v) in enumerate(data):
        y = 210 + i * 30
        s.text(bx - 10, y + 19, n, size=16, anchor='end', mono=True)
        s.rect(bx, y + 3, v * sc, 22, fill=M1L, stroke=M1, sw=1.2, rx=3)
        s.text(bx + v * sc + 8, y + 20, f'{v:.2f}', size=15, fill=M1)
    Xg = bx + 2.82 * sc
    s.line([(Xg, 200), (Xg, 660)], stroke=INK, sw=2.5, dash='8 6')
    s.text(Xg + 8, 702, 'GMEAN 2.82×', size=20, weight='bold')
    s.rect(50, 730, 1500, 140, fill=REDL, stroke=RED, sw=1.5, rx=12)
    s.text(75, 770, '同样 11 个 benchmark：HPCA 版 Complete 1.88×，新 Complete 2.98×。', size=21, weight='bold')
    s.text(75, 808, '都是初步数字：FW 是截断运行；16 个新 benchmark 的数据量 2–267 MB，多数小于全晶圆 L2（96 MB），加速比可能偏高；', size=19, fill=RED)
    s.text(75, 840, 'graphtc 三个配置都崩溃，没有计入。正式评估（更大的 footprint）还在跑。', size=19, fill=RED)
    return s

FIGS = [fig1, fig2, fig3, fig4, fig5, fig6, fig7, fig8]
if __name__ == '__main__':
    which = [int(a) for a in sys.argv[1:]] or range(1, 9)
    for i in which:
        svg = FIGS[i - 1]().svg()
        base = f'explain_{i}'
        open(base + '.svg', 'w').write(svg)
        cairosvg.svg2png(bytestring=svg.encode(), write_to=base + '.png', output_width=W * 2, output_height=H * 2)
        cairosvg.svg2pdf(bytestring=svg.encode(), write_to=base + '.pdf')
    print('ok')
