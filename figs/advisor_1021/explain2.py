# Advisor figures, version 2: RDMA tables stay at 64 entries; M1 = (a) near/far partition,
# (b) owner-side merge (T18 version: requests arriving before the L2 read completes),
# (c) in-network multicast along the YX routing tree (T18 version). M2 and M3 unchanged.
# 10-20 night: (b) without queued-request merge and (c) en-route pickup were reverted to T18 at the user's request.
import sys, cairosvg
from explain import (S, mesh, yx, shorten, retile, factbox, fig1, fig3, fig6,
                     INK, MUT, LINE, FILL, PANEL, M1, M1L, M2, M2L, M3, M3L, RED, REDL, W, H)

NEAR, NEARL = '#2f7fc1', '#d9e9f6'
FAR, FARL = '#4b5560', '#e4e7ea'

def renum(fn, old, new):
    def g():
        s = fn()
        s.b = [x.replace(old, new) for x in s.b]
        return s
    return g

def grid64(s, x0, y0, cells, size=26, gap=5):
    """cells: list of 64 (fill, stroke) tuples"""
    for i, (f, st) in enumerate(cells):
        s.rect(x0 + (i % 8) * (size + gap), y0 + (i // 8) * (size + gap), size, size, fill=f, stroke=st, sw=1.2, rx=4)

# ------------------------------------------------------------------ fig 2
def fig2():
    s = S('why wafer is worse')
    s.title('图 2  为什么 wafer 上更严重：64 项在远处撑不起带宽，远的请求还会占满表项',
            '一项从请求发出占到响应回来；跳数越多，每项占得越久')
    s.text(60, 175, '(a) 64 项能撑起的远程带宽', size=24, weight='bold')
    s.text(60, 207, '带宽 = 64 × 64 B ÷ 往返时间，往返 ≈ 84 + 71.4 ns × 跳数', size=18, fill=MUT)
    rows = [('1 跳', 155, 26.4), ('平均 4.3 跳', 391, 10.5), ('6 跳', 512, 8.0), ('12 跳（对角）', 941, 4.4)]
    x0, sc = 250, 340 / 30
    for k in (0, 10, 20, 30):
        X = x0 + k * sc
        s.line([(X, 225), (X, 575)], stroke=LINE, sw=1)
        s.text(X, 600, f'{k}', size=17, anchor='middle', fill=MUT)
    s.text(x0 + 170, 628, 'GB/s', size=17, anchor='middle', fill=MUT)
    for i, (lab, rtt, bw) in enumerate(rows):
        y = 240 + i * 84
        s.text(x0 - 16, y + 30, lab, size=20, anchor='end')
        s.text(x0 - 16, y + 54, f'往返 {rtt} ns', size=15, anchor='end', fill=MUT)
        s.rect(x0, y + 6, bw * sc, 44, fill=M2L, stroke=M2, sw=1.5, rx=4)
        s.text(x0 + bw * sc + 12, y + 36, f'{bw} GB/s · 链路 {bw / 768 * 100:.1f}%', size=18, weight='bold', fill=M2)
    s.line([(790, 160), (790, 650)], stroke=LINE, sw=1.5)
    s.text(830, 175, '(b) 共享 64 项时，远的请求占满表项', size=24, weight='bold')
    s.text(830, 207, '例子：一半请求去 1 跳外（往返 155 ns），一半去 10 跳外（往返 798 ns）', size=18, fill=MUT)
    cells = [(FAR, FAR)] * 54 + [(NEAR, NEAR)] * 10
    grid64(s, 840, 235, cells, size=34, gap=6)
    s.rect(1180, 250, 26, 22, fill=FAR, stroke=FAR, rx=4); s.text(1216, 268, '远的：54 项', size=20, weight='bold', fill=FAR)
    s.rect(1180, 290, 26, 22, fill=NEAR, stroke=NEAR, rx=4); s.text(1216, 308, '近的：只剩 10 项', size=20, weight='bold', fill=NEAR)
    s.text(1180, 360, '表项按"请求速率 × 往返时间"分，', size=18)
    s.text(1180, 388, '远的每项占得久，就占了 84%；', size=18)
    s.text(1180, 416, '近的 155 ns 就能完成，', size=18)
    s.text(1180, 444, '却要排在后面等', size=18)
    s.text(1180, 500, '2×2 的 MCM 最多 2 跳，往返最多差 1.5 倍；', size=18, fill=MUT)
    s.text(1180, 528, 'wafer 上差 6 倍（155–941 ns）', size=18, fill=MUT)
    s.rect(50, 680, 1500, 180, fill=M1L, stroke=M1, sw=1.5, rx=12)
    s.text(75, 722, '64 这个数不改（用户决定，当作硬件约束）。M1 的三部分都在 64 项以内想办法：', size=22, weight='bold')
    s.text(95, 764, '(a) 请求表按远近分区：近的请求总有位置，不被远的占满', size=20)
    s.text(95, 798, '(b) owner 端合并：L2 读完之前到达的同 line 请求并成一项，只占 owner 一项、只读一次 L2', size=20)
    s.text(95, 832, '(c) 网内多播：合并后 owner 只发一份响应，在路由树的 switch 里复制给每个请求方', size=20)
    return s

# ------------------------------------------------------------------ fig 3: M1 (a)
def figA():
    s = S('M1 (a) near/far partition')
    s.title('图 3  M1 (a)：请求表按远近分区（表还是 64 项）',
            '近的请求总有位置；空着的分区可以互借，不浪费表项')
    s.text(60, 175, '现在：共享 64 项', size=24, weight='bold', fill=RED)
    grid64(s, 70, 205, [(FAR, FAR)] * 54 + [(NEAR, NEAR)] * 10, size=34, gap=6)
    for i in range(10):
        s.circle(440 + (i % 2) * 30, 225 + (i // 2) * 34, 11, fill=NEARL, stroke=NEAR, sw=1.5)
    s.text(420, 420, '近的请求', size=18, fill=NEAR, weight='bold')
    s.text(420, 446, '在门口排队', size=18, fill=NEAR)
    s.text(70, 560, '远的占了 54 项；近的只有 10 项', size=19)
    s.line([(560, 360), (680, 360)], stroke=INK, sw=3, arrow='k')
    s.text(720, 175, 'M1 (a)：分区 + 可借用（比例是示意，实验来定）', size=24, weight='bold', fill=M1)
    cells = []
    for i in range(64):
        r = i // 8
        if r < 2: cells.append((NEAR, NEAR) if i < 13 else (NEARL, NEAR))
        elif r < 4: cells.append((FAR, FAR))
        else: cells.append((FAR, FAR) if i < 52 else (NEAR, NEAR))
    grid64(s, 730, 205, cells, size=34, gap=6)
    s.line([(1060, 205), (1075, 205), (1075, 279), (1060, 279)], stroke=NEAR, sw=2.5)
    s.text(1090, 236, '近区 16 项', size=20, weight='bold', fill=NEAR)
    s.text(1090, 262, '只给近的（例如 ≤ 3 跳）', size=17, fill=NEAR)
    s.line([(1060, 285), (1075, 285), (1075, 359), (1060, 359)], stroke=FAR, sw=2.5)
    s.text(1090, 316, '远区 16 项', size=20, weight='bold', fill=FAR)
    s.text(1090, 342, '只给远的', size=17, fill=FAR)
    s.line([(1060, 365), (1075, 365), (1075, 519), (1060, 519)], stroke=MUT, sw=2.5)
    s.text(1090, 430, '共享 32 项', size=20, weight='bold', fill=MUT)
    s.text(1090, 456, '谁先来谁用', size=17, fill=MUT)
    s.text(1090, 482, '空着的分区也可以借', size=17, fill=MUT)
    s.text(730, 560, '近的请求不再排队；远的最多占远区 + 共享区', size=19)
    s.rect(50, 610, 740, 250, fill=FILL, stroke=LINE, sw=1.5, rx=12)
    s.text(75, 650, '怎么做', size=22, weight='bold')
    for i, t in enumerate(['目的 GPM 由地址决定，跳数是已知的，不用预测',
                           '近的先用近区，满了用共享区；远的同理',
                           '按 Little 定律，近的往返短，同样的项每秒完成更多请求',
                           '不需要 cuckoo filter：这是同一张表的分配策略']):
        s.text(75, 690 + i * 40, f'{i + 1}. {t}', size=19)
    s.rect(810, 610, 740, 250, fill=REDL, stroke=RED, sw=1.5, rx=12)
    s.text(835, 650, '要说清楚的', size=22, weight='bold', fill=RED)
    for i, t in enumerate(['每个 WG 远近数据都要时，总时间仍由最远的决定；',
                           '收益主要来自近的数据先到、先算，重叠更多',
                           '分多少、按几跳分，要用占用统计和实验来定',
                           'T18 现有的"按距离分 credit"是给远的更多，方向相反']):
        s.text(835, 690 + i * 40, t, size=19)
    return s

# ------------------------------------------------------------------ fig 4: M1 (b)
def figB():
    s = S('M1 (b) owner-side merge')
    s.title('图 4  M1 (b)：owner 端合并，L2 读完之前到达的同 line 请求并成一项',
            'owner 表还是 64 项；合并窗口 = 这条 line 的 L2 读还没完成（T18 的实现）')
    roles = {(1, 4): ('R1', 'r'), (1, 6): ('R2', 'r'), (4, 5): ('R3', 'r'), (3, 6): ('R4', 'r'), (0, 2): ('R5', 'r'), (6, 4): ('R6', 'r'), (5, 1): ('O', 'o')}
    P = mesh(s, 60, 185, 44, 10, roles)
    for r in [(1, 4), (1, 6), (4, 5), (3, 6), (0, 2), (6, 4)]:
        s.line(shorten(yx(P, r, (5, 1)), 26), stroke=M1, sw=3, arrow='1')
    retile(s, P, roles, 44)
    s.text(60, 600, '6 个 GPM 先后读 O 上的 line A', size=19)
    s.text(60, 628, '一条 line 最多 47 个读者（MCM 最多 3 个）', size=17, fill=MUT)
    s.rect(500, 165, 1050, 470, fill=PANEL, stroke=LINE, sw=1.5, rx=14)
    s.text(525, 202, 'O 的 RDMA（owner 端）：line A 的请求先后到达', size=22, weight='bold', fill=M2)
    ay = 300
    s.rect(600, ay - 50, 400, 50, fill=M1L, stroke=M1, sw=1.5, rx=4)
    s.text(800, ay - 19, '合并窗口：line A 的 L2 读还没完成', size=17, anchor='middle', fill=M1, weight='bold')
    s.rect(1160, ay - 50, 330, 50, fill='#fff', stroke=MUT, sw=1.5, rx=4, dash='6 4')
    s.text(1325, ay - 19, '第二个窗口', size=17, anchor='middle', fill=MUT)
    s.line([(560, ay), (1520, ay)], stroke=INK, sw=2, arrow='k')
    s.text(1520, ay + 62, '时间', size=16, anchor='end', fill=MUT)
    for l, x, inw in [('R1', 612, True), ('R2', 700, True), ('R3', 785, True), ('R4', 905, True), ('R5', 1175, False), ('R6', 1330, False)]:
        c = M1 if inw else MUT
        s.line([(x, ay - 6), (x, ay + 12)], stroke=c, sw=3)
        s.text(x, ay + 36, l, size=17, anchor='middle', weight='bold', fill=c)
    s.text(600, ay + 64, 'R1 到：占一项、开始读 L2', size=15, fill=MUT)
    s.text(1000, ay + 64, 'L2 读完：一起返回', size=15, anchor='middle', fill=M1)
    s.text(1160, ay + 64, 'R5 来晚了：另开一项', size=15, fill=MUT)
    s.text(530, 425, 'owner 表（64 项）', size=19, weight='bold', fill=M2)
    s.rect(530, 440, 600, 170, fill='#fff', stroke=M2, sw=2, rx=10)
    s.rect(545, 455, 570, 50, fill=M1L, stroke=M1, sw=1.5, rx=6)
    s.text(560, 487, 'line A', size=18, weight='bold', fill=M1)
    for i, r in enumerate(['R1', 'R2', 'R3', 'R4']):
        s.rect(640 + i * 58, 465, 50, 30, fill=M1, stroke=M1, rx=4); s.text(665 + i * 58, 486, r, size=15, anchor='middle', fill='#fff', weight='bold')
    s.text(885, 487, '4 个请求只占 1 项', size=16, fill=M1)
    s.rect(545, 518, 570, 50, fill='#fff', stroke=MUT, sw=1.5, rx=6, dash='6 4')
    s.text(560, 550, 'line A', size=18, fill=MUT)
    for i, r in enumerate(['R5', 'R6']):
        s.rect(640 + i * 58, 528, 50, 30, fill=MUT, stroke=MUT, rx=4); s.text(665 + i * 58, 549, r, size=15, anchor='middle', fill='#fff', weight='bold')
    s.text(885, 550, '第二个窗口', size=16, fill=MUT)
    s.text(545, 596, '其余 62 项留给别的请求', size=16, fill=MUT)
    s.line([(1133, 500), (1178, 500)], stroke=INK, sw=2.5, arrow='k')
    s.rect(1185, 440, 150, 120, fill=FILL, stroke=INK, sw=1.5, rx=8)
    s.text(1260, 485, 'L2 / HBM', size=18, anchor='middle'); s.text(1260, 520, '读 2 次', size=19, anchor='middle', weight='bold', fill=M1)
    s.text(1260, 588, '不合并要读 6 次', size=15, anchor='middle', fill=MUT)
    s.text(1360, 470, '返回：', size=17)
    s.text(1360, 498, '多播（图 5）', size=17, weight='bold', fill=M2)
    s.text(1360, 534, '表满或只有', size=17, fill=MUT)
    s.text(1360, 560, '一个请求方：', size=17, fill=MUT)
    s.text(1360, 586, '照常单播', size=17, fill=MUT)
    factbox(s, 50, 665, 740, 195, '现有数据（64 项）', ['T18 @64（含 (b)(c)）：FW 2.13×，R-GAT 1.36×，PA 1.01×', '只开 owner 端：FW 88.3 → 87.8 µs，几乎没用', 'owner 端同时在途的同 line 请求：PA 0.1%，FW 19%', '→ 64 项时，同 line 请求很少能同时到 owner'], col=RED, light=REDL)
    factbox(s, 810, 665, 740, 195, '为什么对 wafer 有用', ['incast：owner 封顶约 113 B/周期（≥ 8 个读者）', '一条 line 最多 47 个读者，MCM 最多 3 个', '合并后 owner 表和 L2 都只为这条 line 忙一次'], col=M2, light=M2L)
    return s

# ------------------------------------------------------------------ fig 5: M1 (c)
def figC():
    s = S('M1 (c) in-network multicast')
    s.title('图 5  M1 (c)：网内多播，合并后的一份响应在路由树上分叉',
            'owner 只发一份响应；switch 按 YX 路由复制给每个请求方；没合并的照常单播（T18 的实现）')
    roles = {(1, 4): ('R1', 'r'), (1, 6): ('R2', 'r'), (4, 5): ('R3', 'r'), (3, 6): ('R4', 'r'), (5, 1): ('O', 'o')}
    s.text(60, 175, '(a) 没有多播：4 份响应各走各的', size=24, weight='bold', fill=RED)
    P = mesh(s, 90, 205, 44, 10, roles)
    for k, r in enumerate([(1, 4), (1, 6), (4, 5), (3, 6)]):
        d = (k - 1.5) * 5
        pts = [(x + d, y + d) for x, y in yx(P, (5, 1), r)]
        s.line(shorten(pts, 26), stroke=M2, sw=2.5, arrow='2')
    retile(s, P, roles, 44)
    s.text(60, 620, 'owner 的 RDMA 发 4 份，共 28 段链路', size=20)
    s.text(60, 652, '同一段链路上最多叠 4 份同样的数据', size=18, fill=MUT)
    s.line([(790, 160), (790, 670)], stroke=LINE, sw=1.5)
    s.text(830, 175, '(b) 多播：一份响应沿路由树分叉', size=24, weight='bold', fill=M1)
    Q = mesh(s, 860, 205, 44, 10, roles)
    for seg_ in [[Q(5, 1), Q(5, 6), Q(1, 6)], [Q(5, 4), Q(1, 4)], [Q(5, 5), Q(4, 5)]]:
        s.line(seg_, stroke=M2, sw=5)
    retile(s, Q, roles, 44)
    for p in [Q(5, 4), Q(5, 5), (Q(3, 6)[0] + 27, Q(3, 6)[1])]:
        s.circle(p[0], p[1], 9, fill='#fff', stroke=M2, sw=3)
    a, b = Q(5, 1)
    s.rect(1270, 205, 280, 200, fill=M2L, stroke=M2, sw=2, rx=12)
    s.text(1410, 240, 'owner 发出的响应', size=19, anchor='middle', weight='bold', fill=M2)
    s.text(1290, 274, '包头：line A + 目的地位图', size=16)
    for i, r in enumerate(['R1', 'R2', 'R3', 'R4']):
        s.rect(1290 + i * 60, 290, 52, 30, fill=M2, stroke=M2, rx=4); s.text(1316 + i * 60, 311, r, size=15, anchor='middle', fill='#fff', weight='bold')
    s.text(1290, 348, '48 位，一位一个 GPM', size=15, fill=MUT)
    s.text(1290, 385, 'owner 只发 1 份', size=19, weight='bold', fill=M1)
    s.line([(1268, 300), (a + 26, b)], stroke=M2, sw=1.5, dash='5 4')
    s.text(830, 620, '一份响应在 switch 里复制（圆点处分叉），共 14 段链路', size=20)
    s.text(830, 652, '到了请求方 GPM 交给它的 RDMA，认领、唤醒等待者', size=18, fill=MUT)
    s.rect(50, 690, 600, 180, fill=FILL, stroke=LINE, sw=1.5, rx=10)
    s.text(70, 728, 'switch 怎么做', size=23, weight='bold')
    for i, t in enumerate(['1. 包头带目的地位图（来自 (b) 的 owner 合并表）',
                           '2. 按 YX 路由把位图按输出端口拆开，各复制一份',
                           '3. 只有一个目的地，或表满没合并的：普通单播']):
        s.text(70, 766 + i * 32, t, size=19)
    factbox(s, 670, 690, 430, 180, '为什么对 wafer 有用', ['一条 line 最多 47 个读者', '读者之间最近只隔 1.5–2.6 跳', '共享 line 上省 25–52% 链路'], col=M2, light=M2L)
    factbox(s, 1120, 690, 430, 180, '要说清楚的', ['链路本来就闲（利用率 < 3%）', '收益主要是 owner 少发几份响应', '只有 (b) 合并了的才能多播'], col=RED, light=REDL)
    return s

# ------------------------------------------------------------------ fig 8: Complete
def figD():
    s = S('Complete = M1 + M2 + M3')
    s.title('图 8  Complete = M1 + M2 + M3：一次远程读经过的每一步（RDMA 两端的表都是 64 项）',
            '颜色表示这一步由哪个机制负责')
    col = {'M1': (M1, M1L), 'M2': (M2, M2L), 'M3': (M3, M3L), '': (INK, '#fff')}
    def box(x, y, n, t, m, w=560):
        c, l = col[m]
        s.rect(x, y, w, 62, fill=l, stroke=c, sw=2, rx=10)
        s.badge(x + 30, y + 31, n, fill=c)
        s.text(x + 58, y + 39, t, size=19)
        if m: s.text(x + w - 16, y + 39, m, size=19, anchor='end', weight='bold', fill=c)
    req = [(1, '远程 line，L1V 缺失', ''), (2, '先查请求方 L2：命中就直接返回', 'M3'),
           (3, '在途表查重：同 line 在途就挂上去', 'M1'), (4, '同页打包，预测的下一条顺路捎上', 'M2'),
           (5, '按远近分区拿一个请求表项（a）', 'M1')]
    ys = [185 + i * 80 for i in range(5)]
    s.text(60, 165, '请求方 GPM', size=22, weight='bold'); s.text(1035, 165, 'owner GPM', size=22, weight='bold')
    for (n, t, m), y in zip(req, ys): box(60, y, n, t, m)
    for i in range(4): s.line([(200, ys[i] + 62), (200, ys[i + 1])], stroke=INK, sw=2, arrow='k')
    for (n, t, m), y in zip([(9, '认领、唤醒等待者', 'M1'), (10, '准入的 line 回填请求方 L2', 'M3')], [640, 720]): box(60, y, n, t, m)
    s.line([(200, 702), (200, 720)], stroke=INK, sw=2, arrow='k')
    own = [(6, '合并：L2 读完前到的同 line 请求并成一项（b）', 'M1'), (7, '读一次 L2 / HBM', ''), (8, '返回：路由树多播（c），没合并的单播', 'M1')]
    for (n, t, m), y in zip(own, [265, 400, 535]): box(1000, y, n, t, m, w=550)
    s.line([(1275, 327), (1275, 400)], stroke=INK, sw=2, arrow='k'); s.line([(1275, 462), (1275, 535)], stroke=INK, sw=2, arrow='k')
    s.rect(680, 250, 280, 400, fill=FILL, stroke=LINE, sw=1.5, rx=14)
    s.text(820, 290, 'mesh', size=24, anchor='middle', weight='bold', fill=MUT)
    s.line([(620, 536), (700, 536), (700, 300), (990, 300)], stroke=M1, sw=3, arrow='1')
    s.text(820, 335, '请求（单条或位图包）', size=17, anchor='middle', fill=M1)
    s.line([(995, 566), (900, 566), (900, 671), (630, 671)], stroke=M2, sw=3, dash='8 6', arrow='2')
    s.rect(710, 420, 230, 90, fill=M1L, stroke=M1, sw=1.5, rx=10)
    s.text(825, 455, '路上：switch 按路由树', size=17, anchor='middle', fill=M1, weight='bold')
    s.text(825, 482, '复制响应（c）M1', size=17, anchor='middle', fill=M1, weight='bold')
    s.text(820, 610, '响应（多播或单播）', size=17, anchor='middle', fill=M2)
    lx = 1000
    for i, (m, t) in enumerate([('M1', '新：(a) 远近分区 (b) owner 合并 (c) 网内多播'), ('M2', 'HPCA 已有：打包、预取'), ('M3', 'HPCA 已有：本地复用，非重点')]):
        c, l = col[m]
        y = 650 + i * 40
        s.rect(lx, y - 20, 30, 26, fill=l, stroke=c, sw=2, rx=5); s.text(lx + 42, y, m + ' ' + t, size=18, fill=c, weight='bold')
    s.text(60, 860, '第 3 步用的是请求方 64 项在途表（cuckoo 索引）；第 2、10 步来自 M3；Local Pairing 不进论文', size=18, fill=MUT)
    return s

# ------------------------------------------------------------------ fig 9: data so far
def figE():
    s = S('data so far')
    s.title('图 9  现有数据（表 64 项）和还缺的数据',
            '放大表项的结果只当"64 项这个限制值多少"的上限参考，不作为机制')
    x0, y0, hsc = 120, 560, 330 / 10
    s.text(60, 175, '(a) 表 64 项下的现有结果', size=22, weight='bold')
    for k in range(0, 11, 2):
        y = y0 - k * hsc
        s.line([(x0, y), (740, y)], stroke=LINE, sw=1)
        s.text(x0 - 12, y + 6, f'{k}×', size=16, anchor='end', fill=MUT)
    bars = [('FW', '旧 m2（M2）', 2.51, M2, M2L), ('FW', 'T18 @64', 2.13, M1, M1L), ('R-GAT', 'T18 @64', 1.36, M1, M1L),
            ('PagedAttn', 'T18 @64', 1.01, M1, M1L), ('11 个新 bench', 'HPCA 版 Complete', 1.88, M3, M3L)]
    for i, (w_, c_, v, c, l) in enumerate(bars):
        x = x0 + 22 + i * 122
        s.rect(x, y0 - v * hsc, 86, v * hsc, fill=l, stroke=c, sw=2, rx=4)
        s.text(x + 43, y0 - v * hsc - 10, f'{v:.2f}×', size=19, anchor='middle', weight='bold', fill=c)
        s.text(x + 43, y0 + 28, w_, size=16, anchor='middle')
        s.text(x + 43, y0 + 52, c_, size=14, anchor='middle', fill=MUT)
    s.text(60, 650, 'T18 @64 = 请求方合并 + (b) owner 合并 + (c) 网内多播；还没有 (a) 远近分区', size=16, fill=MUT)
    s.line([(790, 160), (790, 680)], stroke=LINE, sw=1.5)
    s.text(830, 175, '(b) 上限参考：表放大到 1024 项（不作为机制）', size=22, weight='bold', fill=MUT)
    x1 = 870
    for k in range(0, 11, 2):
        y = y0 - k * hsc
        s.line([(x1, y), (1530, y)], stroke=LINE, sw=1)
        s.text(x1 - 12, y + 6, f'{k}×', size=16, anchor='end', fill=MUT)
    up = [('FW', 8.56), ('PagedAttn', 3.34), ('R-GAT', 2.49), ('11 个新 bench', 2.98)]
    for i, (w_, v) in enumerate(up):
        x = x1 + 40 + i * 160
        s.rect(x, y0 - v * hsc, 100, v * hsc, fill=FILL, stroke=MUT, sw=1.5, rx=4, dash='6 4')
        s.text(x + 50, y0 - v * hsc - 10, f'{v:.2f}×', size=19, anchor='middle', weight='bold', fill=MUT)
        s.text(x + 50, y0 + 28, w_, size=16, anchor='middle')
    s.text(830, 650, '差距说明 64 项的限制很贵：M1 要在不改 64 的前提下拿回其中一部分', size=17, fill=MUT)
    s.rect(50, 690, 1500, 175, fill=REDL, stroke=RED, sw=1.5, rx=12)
    s.text(75, 728, '还缺（先测，只加统计、表保持 64 项）：', size=21, weight='bold', fill=RED)
    for i, t in enumerate(['① 按距离分组的请求表占用：多少时间表满、被远的占着，同时近的在排队 → (a)',
                           '② owner 端实际合并了多少请求（合并窗口内到达的同 line 请求）、owner 表满的时间 → (b)',
                           '③ 多播响应的目的地个数、owner 少发的响应数、退回单播的比例 → (c)']):
        s.text(95, 766 + i * 32, t, size=18)
    return s

FIGS = [fig1, fig2, figA, figB, figC, renum(fig3, '图 3  ', '图 6  '), renum(fig6, '图 6  ', '图 7  '), figD, figE]
if __name__ == '__main__':
    which = [int(a) for a in sys.argv[1:]] or range(1, len(FIGS) + 1)
    for i in which:
        svg = FIGS[i - 1]().svg()
        base = f'adv2_{i}'
        open(base + '.svg', 'w').write(svg)
        cairosvg.svg2png(bytestring=svg.encode(), write_to=base + '.png', output_width=W * 2, output_height=H * 2)
        cairosvg.svg2pdf(bytestring=svg.encode(), write_to=base + '.pdf')
    print('ok')
