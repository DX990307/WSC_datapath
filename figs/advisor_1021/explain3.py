# Advisor figures, version 3 (10-20 night): mechanisms grouped by hardware location.
#   M1 = RDMA endpoint (both 64-entry tables): merge (requester in-flight table + owner merge table),
#        pack (same-page bitmap batches + prefetch piggyback, from the HPCA m2), allocate (64 entries by distance).
#   M2 = network: in-network multicast of merged responses (needs M1's owner merge).
#   M3 = retention: keep remote lines in the requester L2 (HPCA m3, not the focus).
# RDMA tables stay at 64 entries on both ends.
import sys, cairosvg
from explain import (S, mesh, yx, shorten, retile, factbox, fig1, fig6,
                     INK, MUT, LINE, FILL, PANEL, M1, M1L, M2, M2L, M3, M3L, RED, REDL, W, H, E)
from explain2 import fig2 as fig2_v2, figB, figC, grid64, NEAR, NEARL, FAR, FARL

MID, MIDL = '#8a96a3', '#e9ecef'


def retext(fn, pairs):
    def g():
        s = fn()
        out = []
        for x in s.b:
            for a, b in pairs:
                x = x.replace('>' + E(a) + '<', '>' + E(b) + '<')
            out.append(x)
        s.b = out
        return s
    return g


# ------------------------------------------------------------------ fig 2
fig2 = retext(fig2_v2, [
    ('64 这个数不改（用户决定，当作硬件约束）。M1 的三部分都在 64 项以内想办法：', '64 这个数不改（当作硬件约束）。三个机制都在 64 项以内想办法：'),
    ('(a) 请求表按远近分区：近的请求总有位置，不被远的占满', 'M1 RDMA 端：合并（同 line 两端各只占一项）、打包（同页一个包）、分配（64 项按距离分）'),
    ('(b) owner 端合并：L2 读完之前到达的同 line 请求并成一项，只占 owner 一项、只读一次 L2', 'M2 网络：合并后的一份响应在 switch 里复制给每个请求方（网内多播）'),
    ('(c) 网内多播：合并后 owner 只发一份响应，在路由树的 switch 里复制给每个请求方', 'M3 保留：远程数据看到复用才留在请求方 L2（非重点）'),
])


# ------------------------------------------------------------------ fig 3: overview
def figO():
    s = S('three mechanisms by location')
    s.title('图 3  三个机制按硬件位置分：RDMA 端、网络、L2',
            '都对着同一个瓶颈：两端 RDMA 各 64 项的表（链路利用率 < 3%）')
    # requester GPM
    s.rect(50, 150, 560, 445, fill=PANEL, stroke=LINE, sw=1.5, rx=14)
    s.text(75, 188, '请求方 GPM', size=22, weight='bold')
    s.rect(75, 215, 200, 60, fill='#fff', stroke=INK, sw=1.5, rx=8)
    s.text(175, 252, 'CU / L1V 缺失', size=18, anchor='middle')
    s.line([(175, 275), (175, 318)], stroke=INK, sw=2, arrow='k')
    s.rect(75, 320, 200, 120, fill=M3L, stroke=M3, sw=2, rx=10)
    s.text(175, 354, '请求方 L2', size=19, anchor='middle', weight='bold', fill=M3)
    s.text(175, 388, 'M3 保留', size=21, anchor='middle', weight='bold', fill=M3)
    s.text(175, 418, '复用的远程 line 留下', size=15, anchor='middle', fill=M3)
    s.line([(275, 380), (308, 380)], stroke=INK, sw=2, arrow='k')
    s.text(175, 470, '命中就不出 GPM', size=15, anchor='middle', fill=MUT)
    s.rect(310, 215, 280, 340, fill=M1L, stroke=M1, sw=2, rx=10)
    s.text(450, 246, 'R 的 RDMA（请求表 64 项）', size=17, anchor='middle', weight='bold', fill=M1)
    s.text(450, 278, 'M1', size=24, anchor='middle', weight='bold', fill=M1)
    for i, (a, b) in enumerate([('合并', '同 line 在途或排队就挂上去'), ('打包', '同页的 line 打成一个包'), ('分配', '64 项按距离分，有保底')]):
        y = 296 + i * 84
        s.rect(328, y, 244, 68, fill='#fff', stroke=M1, sw=1.5, rx=8)
        s.text(344, y + 29, a, size=20, weight='bold', fill=M1)
        s.text(344, y + 55, b, size=15, fill=MUT)
    # mesh
    s.rect(640, 215, 320, 340, fill=FILL, stroke=LINE, sw=1.5, rx=10)
    s.text(800, 246, 'mesh（每跳只过 switch）', size=18, anchor='middle', weight='bold', fill=MUT)
    for x in (675, 775, 875):
        s.rect(x, 268, 50, 38, fill='#fff', stroke=MUT, sw=1.5, rx=6)
    s.line([(725, 287), (775, 287)], stroke=MUT, sw=1.5); s.line([(825, 287), (875, 287)], stroke=MUT, sw=1.5)
    s.text(800, 330, '请求（单条或位图包）', size=15, anchor='middle', fill=M1, weight='bold')
    s.rect(660, 372, 280, 150, fill=M2L, stroke=M2, sw=2, rx=10)
    s.text(800, 406, 'M2 网络', size=22, anchor='middle', weight='bold', fill=M2)
    s.text(800, 440, '合并后的一份响应', size=17, anchor='middle')
    s.text(800, 468, '在 switch 里复制给', size=17, anchor='middle')
    s.text(800, 496, '每个请求方（多播）', size=17, anchor='middle')
    s.text(800, 590, '响应', size=15, anchor='middle', fill=M2, weight='bold')
    # owner GPM
    s.rect(990, 150, 560, 445, fill=PANEL, stroke=LINE, sw=1.5, rx=14)
    s.text(1015, 188, 'owner GPM', size=22, weight='bold')
    s.rect(1010, 215, 300, 340, fill=M1L, stroke=M1, sw=2, rx=10)
    s.text(1160, 246, 'O 的 RDMA（owner 表 64 项）', size=17, anchor='middle', weight='bold', fill=M1)
    s.text(1160, 278, 'M1', size=24, anchor='middle', weight='bold', fill=M1)
    s.rect(1028, 296, 264, 100, fill='#fff', stroke=M1, sw=1.5, rx=8)
    s.text(1044, 325, '合并（owner 端）', size=20, weight='bold', fill=M1)
    s.text(1044, 353, 'L2 读完前到的同 line 请求', size=15, fill=MUT)
    s.text(1044, 377, '并成一项，只读一次 L2', size=15, fill=MUT)
    s.rect(1028, 412, 264, 68, fill='#fff', stroke=M1, sw=1.5, rx=8)
    s.text(1044, 441, '打包（owner 端）', size=20, weight='bold', fill=M1)
    s.text(1044, 467, '一个位图包只占一项', size=15, fill=MUT)
    s.rect(1340, 296, 180, 184, fill=FILL, stroke=INK, sw=1.5, rx=8)
    s.text(1430, 380, 'L2 / HBM', size=20, anchor='middle')
    s.line([(1294, 346), (1338, 346)], stroke=INK, sw=2, arrow='k')
    s.line([(592, 340), (1008, 340)], stroke=M1, sw=3, arrow='1')
    s.line([(1160, 557), (1160, 572), (450, 572), (450, 559)], stroke=M2, sw=3, dash='8 6', arrow='2')
    # table
    s.rect(50, 620, 1500, 255, fill='#fff', stroke=LINE, sw=1.5, rx=12)
    cols = [70, 250, 450, 820, 1270]
    for x, t in zip(cols, ['机制', '硬件位置', '做什么', '对应的 wafer 特点', '来源']):
        s.text(x, 655, t, size=18, weight='bold', fill=MUT)
    s.line([(65, 668), (1535, 668)], stroke=LINE, sw=1.2)
    rows = [
        ('M1 RDMA 端', M1, '两端各 64 项的表', ['合并 · 打包 · 分配'], ['读者多：一条 line 最多 47 个（MCM 3 个）', '距离差：往返 155–941 ns，6 倍（MCM 1.5 倍）'], ['合并、分配：新（T18）', '打包：HPCA 版']),
        ('M2 网络', M2, 'switch', ['网内多播', '（建立在 M1 的 owner 合并上）'], ['读者多、平均 4.3 跳'], ['新（T18）']),
        ('M3 保留', M3, '请求方 L2', ['看到复用才留下'], ['远程数据会被同一 GPM 再读'], ['HPCA 版，非重点']),
    ]
    for i, (m, c, loc, what, why, src) in enumerate(rows):
        y = 700 + i * 62 + (0 if i == 0 else 18)
        s.text(cols[0], y, m, size=19, weight='bold', fill=c)
        s.text(cols[1], y, loc, size=17)
        for k, t in enumerate(what): s.text(cols[2], y + k * 24, t, size=17 if k == 0 else 15, fill=INK if k == 0 else MUT)
        for k, t in enumerate(why): s.text(cols[3], y + k * 24, t, size=17)
        for k, t in enumerate(src): s.text(cols[4], y + k * 24, t, size=17)
    return s


# ------------------------------------------------------------------ fig 4: M1 merge
def figMerge():
    s = S('M1 merge on both ends')
    s.title('图 4  M1 合并：同一条 line 在两端各只占一项',
            '请求方：在途表里已有这条 line 就挂上去，不再发；owner：L2 读完之前到的同 line 请求并成一项（T18 的实现）')
    # requester panel
    s.rect(50, 150, 740, 480, fill=PANEL, stroke=LINE, sw=1.5, rx=14)
    s.text(75, 188, '请求方 RDMA：在途表（cuckoo 索引，128 槽）', size=21, weight='bold', fill=M1)
    for i, (cu, y) in enumerate([('CU0', 225), ('CU2', 290), ('CU5', 355)]):
        s.rect(75, y, 90, 46, fill='#fff', stroke=INK, sw=1.5, rx=8); s.text(120, y + 31, cu, size=18, anchor='middle')
        s.line([(165, y + 23), (228, 300)], stroke=M1, sw=2, arrow='1')
    s.text(75, 432, '都读 line A', size=16, fill=M1, weight='bold')
    s.rect(232, 262, 150, 76, fill='#fff', stroke=INK, sw=1.5, rx=8)
    s.text(307, 293, '查 2 个桶', size=17, anchor='middle'); s.text(307, 320, '核对完整地址', size=15, anchor='middle', fill=MUT)
    s.line([(382, 300), (408, 300)], stroke=INK, sw=2, arrow='k')
    rows = [('line A', '已发出', True, ['CU0', 'CU2', 'CU5'], '挂到已发出的项'),
            ('line B', '排队中', False, ['CU1', 'CU7'], '挂到还在排队的项'),
            ('line C', '已发出', True, ['CU3'], '')]
    for i, (l, st, sent, ws, note) in enumerate(rows):
        y = 230 + i * 92
        s.rect(410, y, 360, 70, fill=M1L if i == 0 else '#fff', stroke=M1, sw=1.5, rx=8, dash=None if sent else '6 4')
        s.text(425, y + 30, l, size=18, weight='bold', fill=M1 if i < 2 else MUT)
        s.text(425, y + 56, st, size=15, fill=M1 if sent else MUT)
        for k, w in enumerate(ws):
            s.rect(510 + k * 66, y + 14, 58, 30, fill=M1 if k == 0 else M1L, stroke=M1, sw=1.2, rx=15)
            s.text(539 + k * 66, y + 35, w, size=15, anchor='middle', fill='#fff' if k == 0 else M1, weight='bold')
        if note: s.text(510, y + 63, note, size=14, fill=MUT)
    s.text(410, 520, '实心：发出去的那个；浅色：挂上去的等待者', size=15, fill=MUT)
    s.text(75, 560, '网络上同时在途仍 ≤ 64；另外 64 个槽给还在排队的请求，排队时也能合并', size=17)
    s.text(75, 592, '写会关闭这条 line 的项：写之后的读不会并到写之前的读上', size=16, fill=MUT)
    # owner panel
    s.rect(810, 150, 740, 480, fill=PANEL, stroke=LINE, sw=1.5, rx=14)
    s.text(835, 188, 'owner RDMA：合并表（cuckoo 索引，128 槽）', size=21, weight='bold', fill=M1)
    ay = 290
    s.rect(880, ay - 46, 320, 46, fill=M1L, stroke=M1, sw=1.5, rx=4)
    s.text(1040, ay - 17, '合并窗口：line A 的 L2 读还没完成', size=16, anchor='middle', fill=M1, weight='bold')
    s.line([(845, ay), (1525, ay)], stroke=INK, sw=2, arrow='k')
    for l, x, inw in [('R1', 890, True), ('R2', 990, True), ('R3', 1120, True), ('R4', 1330, False)]:
        c = M1 if inw else MUT
        s.line([(x, ay - 6), (x, ay + 12)], stroke=c, sw=3)
        s.text(x, ay + 36, l, size=17, anchor='middle', weight='bold', fill=c)
    s.text(1520, ay + 36, '时间', size=15, anchor='end', fill=MUT)
    s.text(1330, ay + 62, 'R4 来晚了：另开一项', size=15, anchor='middle', fill=MUT)
    s.rect(835, 390, 450, 64, fill=M1L, stroke=M1, sw=1.5, rx=8)
    s.text(850, 429, 'line A', size=18, weight='bold', fill=M1)
    for k, r in enumerate(['R1', 'R2', 'R3']):
        s.rect(930 + k * 60, 406, 52, 30, fill=M1, stroke=M1, rx=4); s.text(956 + k * 60, 427, r, size=15, anchor='middle', fill='#fff', weight='bold')
    s.text(1115, 429, '48 位请求者位图', size=15, fill=MUT)
    s.line([(1287, 422), (1325, 422)], stroke=INK, sw=2, arrow='k')
    s.rect(1330, 380, 190, 84, fill=FILL, stroke=INK, sw=1.5, rx=8)
    s.text(1425, 414, 'L2 / HBM', size=17, anchor='middle'); s.text(1425, 446, '只读 1 次', size=18, anchor='middle', weight='bold', fill=M1)
    s.text(835, 500, '3 个请求只占 owner 表 1 项、只读 1 次 L2', size=18, weight='bold', fill=M1)
    s.text(835, 534, '回给 R1–R3：M2 多播一份；不开 M2 就逐个单播', size=17)
    s.text(835, 566, '表满、同一请求者重复、不是整行读：照常单独处理', size=16, fill=MUT)
    s.text(835, 596, '没有等待计时器，响应不会为了等更多请求而推迟', size=16, fill=MUT)
    factbox(s, 50, 660, 740, 210, '现有数据（FW 冒烟，64 项）', ['只开请求方合并：1.82×', '只开 owner 合并：1.01×（几乎没用）', 'T18 @64（两端合并 + 分配 + M2 多播）：2.13×', '旧 m2（去重 + 打包）：2.51×'], col=RED, light=REDL)
    factbox(s, 810, 660, 740, 210, '为什么对 wafer 有用（P12）', ['同一 GPM 内同 line 已在途：FW 49%，R-GAT 34%', '跨 GPM 同时在等同一 line：PR 79%，R-GAT 24%', '一条 line 最多 47 个读者（MCM 最多 3 个）', 'PagedAttention 几乎没有（< 1%）'], col=M1, light=M1L)
    return s


# ------------------------------------------------------------------ fig 5: M1 pack
def figPack():
    s = S('M1 pack')
    s.title('图 5  M1 打包：同页的 line 打成一个包，一个表项带多条 line',
            'HPCA 版已有（旧 m2 的同页打包 + 预取捎带）；包在等表项时顺便收 line，所以表项越紧，包越满')
    s.rect(50, 150, 1060, 470, fill=PANEL, stroke=LINE, sw=1.5, rx=14)
    s.text(75, 188, '请求方 RDMA', size=22, weight='bold', fill=M1)
    for i, (cu, y, t) in enumerate([('CU1', 230, 'line 3'), ('CU4', 310, 'line 5'), ('CU6', 390, 'line 6')]):
        s.rect(75, y, 90, 50, fill='#fff', stroke=INK, sw=1.5, rx=8); s.text(120, y + 33, cu, size=18, anchor='middle')
        s.text(180, y + 22, '读页 P 的', size=14, fill=MUT); s.text(180, y + 42, t, size=15, fill=M1, weight='bold')
        s.line([(260, y + 25), (330, 330)], stroke=M1, sw=2, arrow='1')
    s.text(340, 222, '页 P（4 KB = 64 条 line，同一个 owner）：收集中的包', size=17, fill=MUT)
    for i in range(64):
        x, y = 340 + (i % 16) * 30, 240 + (i // 16) * 30
        if i in (3, 5, 6): s.rect(x, y, 26, 26, fill=M1, stroke=M1, sw=1, rx=3)
        elif i == 7: s.rect(x, y, 26, 26, fill=M1L, stroke=M1, sw=2, rx=3, dash='3 2')
        else: s.rect(x, y, 26, 26, fill='#fff', stroke=LINE, sw=1, rx=3)
    s.line([(340 + 7 * 30 + 13, 390), (340 + 7 * 30 + 13, 368)], stroke=M1, sw=2, arrow='1')
    s.text(340, 414, '虚线：步长预测的下一条（line 7），顺路捎上，不单独发', size=16, fill=M1, weight='bold')
    s.text(340, 446, '包在等请求表项时继续收；后来同页的 line 也进同一个包', size=16, fill=MUT)
    s.rect(340, 480, 470, 110, fill=M1L, stroke=M1, sw=2, rx=10)
    s.text(575, 518, '1 个位图包 = 1 个请求表项', size=20, anchor='middle', weight='bold', fill=M1)
    s.text(575, 550, '4 条 line（3、5、6 + 预取 7），最多 8 条', size=16, anchor='middle')
    s.text(575, 576, '只有 1 条时就是普通读', size=15, anchor='middle', fill=MUT)
    s.line([(812, 535), (1255, 400)], stroke=M1, sw=3, arrow='1')
    s.rect(1260, 300, 290, 320, fill=M2L, stroke=M2, sw=2, rx=14)
    s.text(1405, 342, 'owner O 的 RDMA', size=21, anchor='middle', weight='bold', fill=M2)
    s.text(1405, 392, '位图包也只占 1 项', size=19, anchor='middle', weight='bold', fill=M1)
    s.text(1405, 440, '拆开位图', size=18, anchor='middle')
    s.text(1405, 468, '→ 按 line 读 L2', size=18, anchor='middle')
    s.text(1405, 520, '先读完的 line', size=18, anchor='middle')
    s.text(1405, 548, '先返回', size=18, anchor='middle')
    factbox(s, 50, 650, 740, 220, '规则', ['同一 owner、同一 4 KB 页；每包最多 8 条，最多 64 个包在收集', '不设等待计时器：包在等表项或发射宽度时顺便收', '预取：步长预测器 256 项，只在同 owner 同页里预测，', '只捎在已有的包上'], col=INK, light=FILL)
    factbox(s, 810, 650, 740, 220, '数据：表项越紧，包越满', ['64 项（HPCA 版 Complete）：平均每包 1.1–4.7 条', '（allreduce 4.67、ivfflat 3.41、fft2d 3.10）', 'W = 1024（新 Complete）：只剩 1.0–2.1 条', '→ 回到 64 项后打包又重要；P15 会确认'], col=M1, light=M1L)
    return s


# ------------------------------------------------------------------ fig 6: M1 allocate
def _credits(W, hops):
    rtt = [84 + 71.4 * h for h in hops]; sm = sum(rtt)
    ex = [W * r / sm for r in rtt]; c = [int(e) for e in ex]
    order = sorted(range(len(hops)), key=lambda i: -(ex[i] - c[i]))
    k = 0
    while sum(c) < W:
        c[order[k % len(order)]] += 1; k += 1
    return c


def figAlloc():
    s = S('M1 allocate by distance')
    s.title('图 6  M1 分配：64 项按距离分，每个目的地有保底，空闲的可以借',
            '份额怎么定是参数：按往返时间分（T18 已有）或近处多留（新，要实现），用实验来选')
    tiles = [(x, y) for y in range(7) for x in range(7) if (x, y) != (3, 3)]
    me = (0, 0)
    others = [t for t in tiles if t != me]
    hops = [abs(t[0] - me[0]) + abs(t[1] - me[1]) for t in others]
    c = _credits(64, hops)
    near = sum(v for h, v in zip(hops, c) if h <= 3); nd = sum(1 for h in hops if h <= 3)
    mid = sum(v for h, v in zip(hops, c) if 4 <= h <= 6); md = sum(1 for h in hops if 4 <= h <= 6)
    far = 64 - near - mid; fd = len(hops) - nd - md
    zero1 = sum(1 for h, v in zip(hops, c) if h == 1 and v == 0)
    # panel A
    s.rect(50, 150, 740, 470, fill=PANEL, stroke=LINE, sw=1.5, rx=14)
    s.text(75, 188, '按往返时间分（T18 的距离 credit，已有）', size=21, weight='bold', fill=M1)
    s.text(75, 218, f'例子：角上的 GPM，47 个目的地分 64 项', size=16, fill=MUT)
    cells = [(NEAR, NEAR)] * near + [(MID, MID)] * mid + [(FAR, FAR)] * far
    grid64(s, 80, 240, cells, size=34, gap=6)
    lx = 420
    for i, (col, lab, n, d) in enumerate([(NEAR, '近（≤ 3 跳）', near, nd), (MID, '中（4–6 跳）', mid, md), (FAR, '远（≥ 7 跳）', far, fd)]):
        y = 262 + i * 60
        s.rect(lx, y - 18, 26, 22, fill=col, stroke=col, rx=4)
        s.text(lx + 38, y, f'{lab}：{n} 项', size=18, weight='bold', fill=col)
        s.text(lx + 38, y + 24, f'{d} 个目的地', size=15, fill=MUT)
    s.text(lx, 470, f'1 跳的 {zero1} 个邻居保底是 0 项，', size=16)
    s.text(lx, 496, '只能借别人空着的', size=16)
    s.text(80, 580, '目标：每个目的地拿到一样的带宽（Little 定律：远的每项占得久，所以多给）', size=16)
    # panel B
    s.rect(810, 150, 740, 470, fill=PANEL, stroke=LINE, sw=1.5, rx=14)
    s.text(835, 188, '近处多留（新，比例是示意）', size=21, weight='bold', fill=M1)
    s.text(835, 218, '近区单独留出来，远的不能借走', size=16, fill=MUT)
    cellsB = [(NEAR, NEAR)] * 16 + [(MID, MID)] * 16 + [(FAR, FAR)] * 32
    grid64(s, 840, 240, cellsB, size=34, gap=6)
    s.line([(1180, 240), (1195, 240), (1195, 314), (1180, 314)], stroke=NEAR, sw=2.5)
    s.text(1210, 270, '近区 16 项', size=18, weight='bold', fill=NEAR)
    s.text(1210, 296, '只给 ≤ 3 跳的', size=15, fill=NEAR)
    s.line([(1180, 320), (1195, 320), (1195, 554), (1180, 554)], stroke=FAR, sw=2.5)
    s.text(1210, 420, '其余 48 项', size=18, weight='bold', fill=FAR)
    s.text(1210, 446, '按往返时间分', size=15, fill=FAR)
    s.text(840, 580, '目标：近的请求（155 ns 就能完成）不被远的挤在门口', size=16)
    factbox(s, 50, 650, 490, 220, '两种份额共用的规则', ['有请求在等的目的地，', '没用完的保底给它留着', '空闲的可以借；借走的要等', '响应回来才还（不抢回）'], col=INK, light=FILL)
    factbox(s, 560, 650, 490, 220, '为什么是 wafer 才有的问题', ['往返 155–941 ns，差 6 倍', '2×2 MCM 最多 2 跳，只差 1.5 倍', '远的借一次要占约 800 ns', '共享 64 项时远的占大头'], col=M2, light=M2L)
    factbox(s, 1070, 650, 480, 220, '要先测', ['按距离分组的表占用（统计 ①）', 'Complete@64：credit 开 / 关', '近处多留：在 t18_credits.go', '加一种份额（Claude Code）'], col=RED, light=REDL)
    return s


# ------------------------------------------------------------------ fig 7: M2 network
figM2 = retext(figC, [
    ('图 5  M1 (c)：网内多播，合并后的一份响应在路由树上分叉', '图 7  M2 网络：网内多播，合并后的一份响应在路由树上分叉'),
    ('owner 只发一份响应；switch 按 YX 路由复制给每个请求方；没合并的照常单播（T18 的实现）', 'M1 在 owner 端合并后只发一份响应；switch 按 YX 路由复制给每个请求方；没合并的照常单播'),
    ('(a) 没有多播：4 份响应各走各的', '没有多播：4 份响应各走各的'),
    ('(b) 多播：一份响应沿路由树分叉', '多播：一份响应沿路由树分叉'),
    ('1. 包头带目的地位图（来自 (b) 的 owner 合并表）', '1. 包头带目的地位图（来自 M1 的 owner 合并表）'),
    ('只有 (b) 合并了的才能多播', '只有 M1 合并了的才能多播'),
])

_figM2_text = figM2


def figM2():
    s = _figM2_text()
    out = []
    for x in s.b:
        if '>' + E('多播：一份响应沿路由树分叉') + '<' in x or '>' + E('owner 只发 1 份') + '<' in x:
            x = x.replace('fill="' + M1 + '"', 'fill="' + M2 + '"')
        out.append(x)
    s.b = out
    return s


# ------------------------------------------------------------------ fig 8: M3
figM3 = retext(fig6, [
    ('图 6  M3（HPCA 版 Remote Reuse，已有，非重点）：看到复用才留在本地 L2', '图 8  M3 保留（HPCA 版 Remote Reuse，非重点）：看到复用才留在本地 L2'),
])


# ------------------------------------------------------------------ fig 9: Complete
def figD3():
    s = S('Complete = M1 + M2 + M3')
    s.title('图 9  Complete = M1 + M2 + M3：一次远程读经过的每一步（RDMA 两端的表都是 64 项）',
            '颜色表示这一步由哪个机制负责')
    col = {'M1': (M1, M1L), 'M2': (M2, M2L), 'M3': (M3, M3L), '': (INK, '#fff')}

    def box(x, y, n, t, m, w=560):
        c, l = col[m]
        s.rect(x, y, w, 62, fill=l, stroke=c, sw=2, rx=10)
        s.badge(x + 30, y + 31, n, fill=c)
        s.text(x + 58, y + 39, t, size=19)
        if m: s.text(x + w - 16, y + 39, m, size=19, anchor='end', weight='bold', fill=c)
    req = [(1, '远程 line，L1V 缺失', ''), (2, '先查请求方 L2：命中就直接返回', 'M3'),
           (3, '合并：同 line 在途或排队就挂上去', 'M1'), (4, '打包：同页打成一个包，预测的下一条捎上', 'M1'),
           (5, '分配：按距离份额拿一个请求表项', 'M1')]
    ys = [185 + i * 80 for i in range(5)]
    s.text(60, 165, '请求方 GPM', size=22, weight='bold'); s.text(1035, 165, 'owner GPM', size=22, weight='bold')
    for (n, t, m), y in zip(req, ys): box(60, y, n, t, m)
    for i in range(4): s.line([(200, ys[i] + 62), (200, ys[i + 1])], stroke=INK, sw=2, arrow='k')
    for (n, t, m), y in zip([(9, '认领、唤醒等待者', 'M1'), (10, '准入的 line 回填请求方 L2', 'M3')], [640, 720]): box(60, y, n, t, m)
    s.line([(200, 702), (200, 720)], stroke=INK, sw=2, arrow='k')
    own = [(6, '合并：L2 读完前到的同 line 并成一项', 'M1'), (7, '读一次 L2 / HBM', ''), (8, '返回：多播，没合并的单播', 'M2')]
    for (n, t, m), y in zip(own, [265, 400, 535]): box(1000, y, n, t, m, w=550)
    s.line([(1275, 327), (1275, 400)], stroke=INK, sw=2, arrow='k'); s.line([(1275, 462), (1275, 535)], stroke=INK, sw=2, arrow='k')
    s.rect(680, 250, 280, 400, fill=FILL, stroke=LINE, sw=1.5, rx=14)
    s.text(820, 290, 'mesh', size=24, anchor='middle', weight='bold', fill=MUT)
    s.line([(620, 536), (700, 536), (700, 300), (990, 300)], stroke=M1, sw=3, arrow='1')
    s.text(820, 335, '请求（单条或位图包）', size=17, anchor='middle', fill=M1)
    s.line([(995, 566), (900, 566), (900, 671), (630, 671)], stroke=M2, sw=3, dash='8 6', arrow='2')
    s.rect(710, 420, 230, 90, fill=M2L, stroke=M2, sw=1.5, rx=10)
    s.text(825, 455, '路上：switch 按路由树', size=17, anchor='middle', fill=M2, weight='bold')
    s.text(825, 482, '复制响应（M2）', size=17, anchor='middle', fill=M2, weight='bold')
    s.text(820, 610, '响应（多播或单播）', size=17, anchor='middle', fill=M2)
    lx = 1000
    for i, (m, t) in enumerate([('M1', 'RDMA 端：合并、打包、分配'), ('M2', '网络：多播'), ('M3', '保留：本地复用，非重点')]):
        c, l = col[m]
        y = 650 + i * 40
        s.rect(lx, y - 20, 30, 26, fill=l, stroke=c, sw=2, rx=5); s.text(lx + 42, y, m + ' ' + t, size=18, fill=c, weight='bold')
    s.text(60, 860, '打包（第 4 步）和 M3（第 2、10 步）来自 HPCA 版；合并、分配、多播是新的（T18）；Local Pairing 不进论文', size=18, fill=MUT)
    return s


# ------------------------------------------------------------------ fig 10: data and next runs
def figE3():
    s = S('data so far')
    s.title('图 10  现有数据（表 64 项）和接下来要跑的',
            '放大表项的结果只当"64 项这个限制值多少"的上限参考，不作为机制')
    x0, y0, hsc = 120, 560, 330 / 10
    s.text(60, 175, '(a) 表 64 项下的现有结果（冒烟）', size=22, weight='bold')
    for k in range(0, 11, 2):
        y = y0 - k * hsc
        s.line([(x0, y), (740, y)], stroke=LINE, sw=1)
        s.text(x0 - 12, y + 6, f'{k}×', size=16, anchor='end', fill=MUT)
    bars = [('FW', '旧 m2', 2.51, M1, M1L), ('FW', 'T18 @64', 2.13, M1, M1L), ('R-GAT', 'T18 @64', 1.36, M1, M1L),
            ('PagedAttn', 'T18 @64', 1.01, M1, M1L), ('11 个新 bench', 'HPCA 版 Complete', 1.88, M3, M3L)]
    for i, (w_, c_, v, c, l) in enumerate(bars):
        x = x0 + 22 + i * 122
        s.rect(x, y0 - v * hsc, 86, v * hsc, fill=l, stroke=c, sw=2, rx=4)
        s.text(x + 43, y0 - v * hsc - 10, f'{v:.2f}×', size=19, anchor='middle', weight='bold', fill=c)
        s.text(x + 43, y0 + 28, w_, size=16, anchor='middle')
        s.text(x + 43, y0 + 52, c_, size=14, anchor='middle', fill=MUT)
    s.text(60, 640, '旧 m2 = 去重 + 打包（现在属于 M1）；T18 @64 = M1 合并 + 分配 + M2 多播，不含打包', size=16, fill=MUT)
    s.line([(790, 160), (790, 680)], stroke=LINE, sw=1.5)
    s.text(830, 175, '(b) 上限参考：表放大到 1024 项（不作为机制）', size=22, weight='bold', fill=MUT)
    x1 = 870
    for k in range(0, 11, 2):
        y = y0 - k * hsc
        s.line([(x1, y), (1530, y)], stroke=LINE, sw=1)
        s.text(x1 - 12, y + 6, f'{k}×', size=16, anchor='end', fill=MUT)
    for i, (w_, v) in enumerate([('FW', 8.56), ('PagedAttn', 3.34), ('R-GAT', 2.49), ('11 个新 bench', 2.98)]):
        x = x1 + 40 + i * 160
        s.rect(x, y0 - v * hsc, 100, v * hsc, fill=FILL, stroke=MUT, sw=1.5, rx=4, dash='6 4')
        s.text(x + 50, y0 - v * hsc - 10, f'{v:.2f}×', size=19, anchor='middle', weight='bold', fill=MUT)
        s.text(x + 50, y0 + 28, w_, size=16, anchor='middle')
    s.text(830, 640, '差距说明 64 项的限制很贵；三个机制要在不改 64 的前提下拿回一部分', size=17, fill=MUT)
    s.rect(50, 690, 1500, 180, fill=REDL, stroke=RED, sw=1.5, rx=12)
    s.text(75, 728, '接下来（表都保持 64 项）：', size=21, weight='bold', fill=RED)
    for i, t in enumerate(['① P15（已写好）：Complete@64 vs baseline，15 个新 benchmark、30 个运行，和 W = 1024 的 2.82× 对比',
                           '② 消融：baseline、M1、M1 + M2、M1 + M3、Complete；M1 内部去掉合并 / 打包 / 分配各一次',
                           '③ 统计：按距离分组的表占用（分配）、owner 合并数和表满时间（合并）、多播目的地数和退回单播比例（M2）']):
        s.text(95, 768 + i * 34, t, size=18)
    return s


FIGS = [fig1, fig2, figO, figMerge, figPack, figAlloc, figM2, figM3, figD3, figE3]
if __name__ == '__main__':
    which = [int(a) for a in sys.argv[1:]] or range(1, len(FIGS) + 1)
    for i in which:
        svg = FIGS[i - 1]().svg()
        base = f'adv3_{i}'
        open(base + '.svg', 'w').write(svg)
        cairosvg.svg2png(bytestring=svg.encode(), write_to=base + '.png', output_width=W * 2, output_height=H * 2)
        cairosvg.svg2pdf(bytestring=svg.encode(), write_to=base + '.pdf')
    print('ok')
