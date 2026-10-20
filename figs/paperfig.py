# Paper-style figures for T18 (two-sided merge) and T19 (filter-guided neighbor stealing).
import math, html
FONT="Helvetica, Arial, 'Liberation Sans', Inter, sans-serif"
INK="#1d2329"; MUT="#6b7480"; LINE="#c3cad2"; FILL="#f2f4f6"
ACC="#1f5a8a"; ACC_L="#dce8f2"; ORG="#d9822b"; ORG_L="#fbe9d6"; RED="#b8463c"
E=html.escape

class S:
    def __init__(s,W,H,label):
        s.W,s.H,s.label=W,H,label; s.b=[]
    def add(s,x): s.b.append(x)
    def text(s,x,y,t,size=15,anchor='start',weight='normal',fill=INK,style='',family=None):
        fam=family or FONT
        s.add(f'<text x="{x:.1f}" y="{y:.1f}" font-family="{fam}" font-size="{size}" font-weight="{weight}" fill="{fill}" text-anchor="{anchor}"{(" font-style=" + chr(34)+style+chr(34)) if style else ""}>{t}</text>')
    def rect(s,x,y,w,h,fill=FILL,stroke=INK,sw=1.2,rx=4,dash=None,op=None):
        d=f' stroke-dasharray="{dash}"' if dash else ''
        o=f' opacity="{op}"' if op else ''
        s.add(f'<rect x="{x:.1f}" y="{y:.1f}" width="{w:.1f}" height="{h:.1f}" rx="{rx}" fill="{fill}" stroke="{stroke}" stroke-width="{sw}"{d}{o}/>')
    def line(s,pts,stroke=INK,sw=1.4,arrow=None,dash=None,cap='round'):
        d=f' stroke-dasharray="{dash}"' if dash else ''
        m=f' marker-end="url(#ah-{arrow})"' if arrow else ''
        P=' '.join(f'{x:.1f},{y:.1f}' for x,y in pts)
        s.add(f'<polyline points="{P}" fill="none" stroke="{stroke}" stroke-width="{sw}" stroke-linecap="{cap}" stroke-linejoin="round"{d}{m}/>')
    def circle(s,x,y,r,fill=INK,stroke='none',sw=1):
        s.add(f'<circle cx="{x:.1f}" cy="{y:.1f}" r="{r}" fill="{fill}" stroke="{stroke}" stroke-width="{sw}"/>')
    def badge(s,x,y,n,fill=INK):
        s.circle(x,y,9.5,fill=fill); s.text(x,y+4.6,str(n),size=12.5,anchor='middle',weight='bold',fill='#fff')
    def svg(s):
        defs='<defs>'
        for k,c in [('k',INK),('m',MUT),('a',ACC),('o',ORG),('r',RED)]:
            defs+=f'<marker id="ah-{k}" viewBox="0 0 10 10" refX="9" refY="5" markerUnits="userSpaceOnUse" markerWidth="11" markerHeight="11" orient="auto-start-reverse"><path d="M0,1 L10,5 L0,9 z" fill="{c}"/></marker>'
        defs+='</defs>'
        return (f'<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 {s.W} {s.H}" width="{s.W}" height="{s.H}" role="img" aria-label="{E(s.label)}">'
                f'<rect width="{s.W}" height="{s.H}" fill="#ffffff"/>{defs}'+''.join(s.b)+'</svg>')

# ---------------------------------------------------------------- mesh helper
def mesh(s,ox,oy,cell=26,gap=5,roles=None,lab_size=10):
    roles=roles or {}
    P=lambda x,y:(ox+x*(cell+gap)+cell/2, oy+y*(cell+gap)+cell/2)
    for y in range(7):
        for x in range(7):
            cx,cy=P(x,y)
            if (x,y)==(3,3):
                s.rect(cx-cell/2,cy-cell/2,cell,cell,fill='#ffffff',stroke=MUT,sw=1,dash='3 2',rx=3); s.text(cx,cy+3.5,'IO',size=lab_size,anchor='middle',fill=MUT); continue
            s.rect(cx-cell/2,cy-cell/2,cell,cell,fill=FILL,stroke=LINE,sw=1,rx=3)
    def top():
        for (x,y),r in roles.items():
            cx,cy=P(x,y)
            if r=='owner': s.rect(cx-cell/2,cy-cell/2,cell,cell,fill=ACC,stroke=ACC,rx=3); s.text(cx,cy+4,'O',size=12,anchor='middle',weight='bold',fill='#fff')
            elif r=='T': s.rect(cx-cell/2,cy-cell/2,cell,cell,fill=ORG,stroke=ORG,rx=3); s.text(cx,cy+4.5,'T',size=13,anchor='middle',weight='bold',fill='#fff')
            else: s.rect(cx-cell/2,cy-cell/2,cell,cell,fill=ORG_L,stroke=ORG,sw=1.6,rx=3); s.text(cx,cy+4,r,size=11,anchor='middle',weight='bold',fill=INK)
    P.top=None
    return P,top
def xy(a,b):
    (x1,y1),(x2,y2)=a,b; pts=[(x1,y1)]
    if x1!=x2: pts.append((x2,y1))
    if y1!=y2: pts.append((x2,y2))
    return pts

# ================================================================ T18 figure
def fig_t18():
    W,H=1000,672; s=S(W,H,'Two-sided request merging: requester in-flight cuckoo table with distance-aware credits, owner merge table with mesh multicast')
    owner=(5,1); reqs={'R1':(1,4),'R2':(1,6),'R3':(4,5)}
    roles={owner:'owner',**{v:k for k,v in reqs.items()}}
    # ---- (a) baseline
    s.text(20,26,'(a) Baseline',size=15,weight='bold')
    P,top=mesh(s,30,44,roles=roles)
    def shorten(pts,d=15):
        (x1,y1),(x2,y2)=pts[-2],pts[-1]; L=math.hypot(x2-x1,y2-y1); return pts[:-1]+[(x2-(x2-x1)/L*d,y2-(y2-y1)/L*d)]
    for i,(k,r) in enumerate(reqs.items()):
        pts=[P(*q) for q in xy(owner,r)]
        off=(i-1)*3.4
        s.line(shorten([(x+off,y+off) for x,y in pts]),stroke=MUT,sw=1.6,arrow='m')
    top()
    s.text(30+7*31/2-2,44+7*31+22,'owner reads the line 3 times,',size=13,anchor='middle',fill=MUT)
    s.text(30+7*31/2-2,44+7*31+40,'sends 3 separate responses',size=13,anchor='middle',fill=MUT)
    # ---- (b) merged
    bx=290
    s.text(bx-10,26,'(b) Owner-side cross-GPM merge',size=15,weight='bold')
    P2,top2=mesh(s,bx,44,roles=roles)
    segs=set()
    for r in reqs.values():
        q=xy(owner,r)
        for a,b in zip(q,q[1:]):
            (x1,y1),(x2,y2)=a,b
            if y1==y2:
                for x in range(min(x1,x2),max(x1,x2)): segs.add(((x,y1),(x+1,y1)))
            else:
                for y in range(min(y1,y2),max(y1,y2)): segs.add(((x1,y),(x1,y+1)))
    for a,b in segs:
        endp=b in reqs.values() or a in reqs.values()
        A,B=P2(*a),P2(*b)
        if b in reqs.values():
            L=math.hypot(B[0]-A[0],B[1]-A[1]); B=(B[0]-(B[0]-A[0])/L*15,B[1]-(B[1]-A[1])/L*15)
            s.line([A,B],stroke=ORG,sw=3.2,arrow='o')
        else: s.line([A,B],stroke=ORG,sw=3.2)
    for f in [(4,1),(1,1)]:
        cx,cy=P2(*f); s.circle(cx,cy,4.6,fill=ORG)
    top2()
    s.text(bx+7*31/2-2,44+7*31+22,'owner reads once; one response',size=13,anchor='middle',fill=ORG,weight='bold')
    s.text(bx+7*31/2-2,44+7*31+40,'forks along the routing tree (dots)',size=13,anchor='middle',fill=ORG,weight='bold')
    # ---- (e) entry formats (top right)
    ex=560
    s.text(ex,26,'(c) Table entries (cuckoo-indexed)',size=15,weight='bold')
    def entry(y,title,fields,col):
        s.text(ex,y,title,size=13.5,fill=INK,weight='bold')
        x=ex;
        for name,w,f in fields:
            s.rect(x,y+8,w,30,fill=f,stroke=col,sw=1.2,rx=0); s.text(x+w/2,y+28,name,size=12.5,anchor='middle'); x+=w
    entry(58,'Remote request tracker (RRT), per GPM: 64 → 100s–1000s',[('fingerprint',96,ACC_L),('dst GPM',70,'#fff'),('waiter list ptr',118,'#fff'),('V',30,'#fff')],ACC)
    s.text(ex,120,'+ full line tag kept in a side array: checked before any merge',size=12.5,fill=MUT)
    entry(150,'Owner merge table (OMT), per GPM  [new]',[('fingerprint',96,ACC_L),('requester bitmap (48 b)',178,ORG_L),('state',60,'#fff')],ACC)
    # credits
    s.text(ex,232,'Why the tracker must grow on a wafer',size=13.5,weight='bold')
    s.text(ex,250,'entries needed for 100 B/ns remote = 100 × RTT / 64 B',size=12,fill=MUT)
    rows=[('1 hop',155),('4.3 hops (avg)',391),('6 hops',514),('12 hops',945)]
    for i,(n,rtt) in enumerate(rows):
        y=258+i*24; need=100*rtt/64; s.text(ex,y+14,n,size=12.5,fill=MUT)
        wbar=need/1480*270; s.rect(ex+104,y+2,wbar,16,fill=ACC_L,stroke=ACC,sw=1,rx=2)
        s.text(ex+104+wbar+6,y+15,f'{need:,.0f}',size=12,fill=INK)
    x64=ex+104+64/1480*270
    s.line([(x64,254),(x64,356)],stroke=RED,sw=1.6,dash='4 3'); s.text(x64+4,368,'today: 64 entries (≈ MI250X-class endpoint)',size=12,fill=RED)
    # ---- (d) requester datapath (bottom)
    y0=392
    s.line([(14,y0-14),(W-14,y0-14)],stroke=LINE,sw=1)
    s.text(20,y0+8,'(d) Requester side (network interface of each GPM)',size=15,weight='bold')
    s.text(690,y0+8,'(e) Owner side  [new]',size=15,weight='bold')
    by=y0+40
    s.rect(20,by+40,110,56,fill=FILL); s.text(75,by+64,'L1V miss',size=14,anchor='middle'); s.text(75,by+82,'(remote line)',size=12,anchor='middle',fill=MUT)
    s.rect(170,by+20,150,96,fill=ACC_L,stroke=ACC,sw=1.6); s.text(245,by+48,'RRT lookup',size=14,anchor='middle',weight='bold',fill=ACC); s.text(245,by+68,'cuckoo, 2 hashes',size=12,anchor='middle',fill=MUT); s.text(245,by+86,'+ full-tag check',size=12,anchor='middle',fill=MUT)
    s.line([(130,by+68),(168,by+68)],arrow='k')
    s.badge(150,by+52,1)
    s.rect(178,by+4,124,18,fill='#e6e8eb',stroke='none',rx=3); s.text(240,by+17,'exists in CuPath (HPCA)',size=11,anchor='middle',fill=MUT)
    # hit path
    s.rect(370,by-10,170,50,fill=ORG_L,stroke=ORG,sw=1.4); s.text(455,by+12,'merge: append waiter',size=13.5,anchor='middle'); s.text(455,by+30,'no new request',size=12,anchor='middle',fill=MUT)
    s.line([(320,by+40),(345,by+40),(345,by+15),(368,by+15)],stroke=ORG,arrow='o'); s.text(350,by+56,'hit',size=12,fill=ORG,weight='bold')
    s.badge(357,by+2,2,fill=ORG)
    # miss path
    s.rect(370,by+80,170,50,fill='#fff',stroke=INK); s.text(455,by+102,'allocate entry',size=13.5,anchor='middle'); s.text(455,by+120,'100s–1000s of entries, no CAM',size=11,anchor='middle',fill=MUT)
    s.line([(320,by+96),(345,by+96),(345,by+105),(368,by+105)],arrow='k'); s.text(326,by+120,'miss',size=12,fill=INK,weight='bold')
    s.badge(357,by+132,3)
    s.rect(372,by+134,166,18,fill=ACC,stroke='none',rx=3); s.text(455,by+147,'new: scaled tracker',size=11.5,anchor='middle',fill='#fff',weight='bold')
    s.line([(540,by+105),(588,by+105)],arrow='k'); s.text(548,by+96,'request',size=12,fill=MUT)
    # mesh cloud
    s.rect(590,by+64,64,82,fill='#fff',stroke=MUT,dash='4 3'); s.text(622,by+102,'mesh',size=13,anchor='middle',fill=MUT); s.text(622,by+118,'DOR',size=11.5,anchor='middle',fill=MUT)
    # owner side
    s.line([(654,by+90),(690,by+90)],arrow='k')
    s.rect(692,by+40,150,96,fill=ACC_L,stroke=ACC,sw=1.6); s.text(767,by+68,'OMT lookup',size=14,anchor='middle',weight='bold',fill=ACC); s.text(767,by+88,'hit: set requester bit',size=12,anchor='middle',fill=MUT); s.text(767,by+106,'miss: allocate, read once',size=12,anchor='middle',fill=MUT)
    s.badge(672,by+74,4)
    s.rect(880,by+40,100,96,fill=FILL); s.text(930,by+82,'L2 / HBM',size=14,anchor='middle'); s.text(930,by+100,'1 access',size=12,anchor='middle',fill=MUT)
    s.line([(842,by+88),(878,by+88)],arrow='k')
    # multicast return
    s.line([(930,by+136),(930,by+196),(622,by+196),(622,by+148)],stroke=ORG,sw=2.6,arrow='o')
    s.text(776,by+188,'multicast to all bits in the bitmap (routing tree)',size=12.5,anchor='middle',fill=ORG,weight='bold')
    s.badge(940,by+166,5,fill=ORG)
    # response to requester: wake & delete
    s.line([(600,by+180),(245,by+180),(245,by+118)],stroke=ORG,sw=2.0,arrow='o')
    s.text(40,by+204,'response carries only the line address: each waiting GPM',size=12.5,fill=INK); s.text(40,by+220,'claims it in its RRT, wakes the waiters, frees the entry',size=12.5,fill=INK)
    s.badge(258,by+156,6,fill=ORG)
    return s

# ================================================================ T19 figure
def fig_t19():
    W,H=1000,650; s=S(W,H,'Filter-guided neighbor work-group stealing: a GPM that finishes early steals not-yet-started work-groups from 1-2 hop neighbours, picking the group whose footprint overlaps data it already holds')
    # ---- (a) timeline
    s.text(20,26,'(a) Per-GPM busy time: static split vs. stealing',size=15,weight='bold')
    busy=[1.00,0.78,0.62,0.55,0.70,0.48]
    names=['GPM A','GPM B','GPM C','GPM D','GPM E','GPM F']
    tgt=sum(busy)/len(busy)*1.04
    x0=92; sc=360; y=56
    s.text(x0,y-8,'static split',size=12.5,fill=MUT)
    for i,(n,b) in enumerate(zip(names,busy)):
        yy=y+i*22; s.text(x0-8,yy+13,n,size=12,anchor='end',fill=MUT)
        s.rect(x0,yy+2,min(b,tgt)*sc,15,fill=FILL,stroke=INK,sw=1,rx=2)
        if b>tgt: s.rect(x0+tgt*sc,yy+2,(b-tgt)*sc,15,fill='#ecd0cb',stroke=RED,sw=1,rx=2)
    s.line([(x0+sc,y-4),(x0+sc,y+6*22+2)],stroke=RED,sw=1.4,dash='4 3'); s.text(x0+sc+5,y+12,'kernel end',size=12,fill=RED)
    y2=y+6*22+34
    s.text(x0,y2-8,'with neighbor stealing',size=12.5,fill=MUT)
    for i,(n,b) in enumerate(zip(names,busy)):
        yy=y2+i*22; s.text(x0-8,yy+13,n,size=12,anchor='end',fill=MUT)
        s.rect(x0,yy+2,min(b,tgt)*sc,15,fill=FILL,stroke=INK,sw=1,rx=2)
        if b<tgt: s.rect(x0+b*sc,yy+2,(tgt-b)*sc,15,fill=ORG_L,stroke=ORG,sw=1,rx=2)
    s.line([(x0+tgt*sc,y2-4),(x0+tgt*sc,y2+6*22+2)],stroke=ACC,sw=1.4,dash='4 3'); s.text(x0+tgt*sc+5,y2+12,'kernel end',size=12,fill=ACC)
    ly=y2+6*22+22
    s.rect(x0,ly,14,10,fill='#ecd0cb',stroke=RED,sw=1,rx=1); s.text(x0+20,ly+9,'not-yet-started WGs taken away',size=12,fill=MUT)
    s.rect(x0+215,ly,14,10,fill=ORG_L,stroke=ORG,sw=1,rx=1); s.text(x0+235,ly+9,'stolen WGs executed',size=12,fill=MUT)
    # ---- (b) mesh neighborhood
    mx=540
    s.text(mx,26,'(b) Steal only from neighbors within 2 hops',size=15,weight='bold')
    thief=(2,4)
    P,top=mesh(s,mx+30,48,cell=30,gap=6,roles={thief:'T'})
    for yy in range(7):
        for xx in range(7):
            d=abs(xx-thief[0])+abs(yy-thief[1])
            if (xx,yy)==(3,3) or d==0 or d>2: continue
            cx,cy=P(xx,yy); s.rect(cx-15,cy-15,30,30,fill=ACC_L,stroke=ACC,sw=1,rx=3)
    for v,ok in [((2,2),True),((4,4),True),((1,5),True),((6,6),False)]:
        a_=P(*v); b_=P(*thief)
        dx,dy=b_[0]-a_[0],b_[1]-a_[1]; L=math.hypot(dx,dy); ux,uy=dx/L,dy/L
        s.line([(a_[0]+ux*12,a_[1]+uy*12),(b_[0]-ux*18,b_[1]-uy*18)],stroke=ORG if ok else RED,sw=2.2,arrow='o' if ok else 'r',dash=None if ok else '5 4')
        if not ok:
            mxp,myp=(a_[0]+b_[0])/2,(a_[1]+b_[1])/2; s.circle(mxp,myp,9,fill='#fff',stroke=RED,sw=1.5); s.text(mxp,myp+5,'×',size=15,anchor='middle',fill=RED,weight='bold')
    top()
    cxm=mx+30+7*36/2-3
    s.text(cxm,48+7*36+20,'T = GPM that finished early;  shaded = within 2 hops',size=12.5,anchor='middle',fill=MUT)
    s.text(cxm,48+7*36+38,'a far victim\'s WGs would pay 5–10 extra hops per remote access',size=12.5,anchor='middle',fill=RED)
    # ---- (c) affinity selection
    y0=418
    s.line([(14,y0-16),(W-14,y0-16)],stroke=LINE,sw=1)
    s.text(20,y0+8,'(c) Which pending WG group to steal: footprint filters',size=15,weight='bold')
    by=y0+34
    cands=[('group g1',0.82,True),('group g2',0.35,False),('group g3',0.10,False)]
    s.text(20,by+4,"victim's pending WG groups",size=12.5,fill=MUT)
    for i,(n,ov,best) in enumerate(cands):
        yy=by+14+i*44
        s.rect(20,yy,92,34,fill='#fff',stroke=INK); s.text(66,yy+22,n,size=13,anchor='middle')
        s.rect(122,yy,130,34,fill=ACC_L,stroke=ACC,sw=1.2); s.text(187,yy+15,'footprint filter',size=12,anchor='middle',fill=ACC,weight='bold'); s.text(187,yy+29,'pages read last launch',size=11,anchor='middle',fill=MUT)
        s.line([(252,yy+17),(368,by+81)],stroke=MUT,sw=1.1)
    s.rect(370,by+48,150,66,fill='#fff',stroke=INK,sw=1.4); s.text(445,by+76,'overlap score',size=14,anchor='middle',weight='bold'); s.text(445,by+96,'per candidate group',size=12,anchor='middle',fill=MUT)
    s.rect(570,by+8,170,42,fill=ACC_L,stroke=ACC,sw=1.2); s.text(655,by+26,"thief's RRT (T18)",size=13,anchor='middle',fill=ACC,weight='bold'); s.text(655,by+42,'remote lines in flight',size=11.5,anchor='middle',fill=MUT)
    s.rect(570,by+112,170,42,fill=FILL,stroke=INK,sw=1.2); s.text(655,by+130,"thief's L2",size=13,anchor='middle'); s.text(655,by+146,'remote lines resident',size=11.5,anchor='middle',fill=MUT)
    s.line([(570,by+29),(546,by+29),(546,by+66),(522,by+66)],arrow='k'); s.line([(570,by+133),(546,by+133),(546,by+96),(522,by+96)],arrow='k')
    rx=790; s.text(rx,by+4,'score → decision',size=12.5,fill=MUT)
    for i,(n,ov,best) in enumerate(cands):
        yy=by+14+i*44
        s.text(rx,yy+22,n,size=12.5)
        s.rect(rx+64,yy+8,ov*80,18,fill=ORG if best else LINE,stroke='none',rx=2)
        if best: s.text(rx+64+ov*80+6,yy+22,'steal',size=12.5,fill=ORG,weight='bold')
    s.line([(520,by+81),(770,by+81)],stroke=MUT,sw=1.1,arrow='m',dash='4 3')
    s.text(20,by+176,"Footprint filters are cuckoo filters, so stale pages can be deleted when a group's footprint changes. Without history,",size=12.5,fill=MUT)
    s.text(20,by+194,'fall back to the adjacent / same-group WGs. The thief\'s RRT is the same table that T18 uses for merging.',size=12.5,fill=MUT)
    return s

if __name__=='__main__':
    import cairosvg
    for name,f in [('fig_t18_two_sided_merge',fig_t18),('fig_t19_neighbor_stealing',fig_t19)]:
        sv=f().svg(); open(name+'.svg','w').write(sv)
        cairosvg.svg2pdf(bytestring=sv.encode(),write_to=name+'.pdf')
        cairosvg.svg2png(bytestring=sv.encode(),write_to=name+'.png',scale=2)
    print('ok')
