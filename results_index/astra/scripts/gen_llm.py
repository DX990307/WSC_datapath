"""Chakra ET for Llama-7B inference on the 7x7 wafer (P18, ASTRA-sim).

TP x PP over the 48 GPM tiles (tile 24 = driver, idle). M sequences: prefill
of S tokens each, then D decode steps per sequence. Per layer: QKV GEMM,
attention, O-proj, TP all-reduce, MLP up/gate, MLP down, TP all-reduce.
Stages pass activations rank-to-rank (SEND/RECV); the last stage runs the
LM head and sends the new token back to stage 0. Compute nodes use the
roofline (num_ops, tensor_size); 1 tick = 1 ns.

usage: python3 gen_llm.py out_dir placement(snake|random) [TP PP M S D]
writes out_dir/et/wl.<npu>.et, out_dir/comm_groups.json, out_dir/layout.json
"""
import json, os, random, sys
HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, os.environ.get("CHAKRA_PB_DIR", HERE))
from et_def_pb2 import (ALL_REDUCE, COMM_COLL_NODE, COMM_RECV_NODE, COMM_SEND_NODE, COMP_NODE,
                        AttributeProto as A, GlobalMetadata, Node)
from google.protobuf.internal.encoder import _VarintBytes

L, D_MODEL, FFN, VOCAB, B = 32, 4096, 11008, 32000, 2  # Llama-7B, fp16
W7, CENTER = 7, 24


def tiles(placement, seed=1):
    order = []
    for y in range(W7):
        xs = range(W7) if y % 2 == 0 else range(W7 - 1, -1, -1)
        order += [x + W7 * y for x in xs if x + W7 * y != CENTER]
    if placement == "random":
        random.Random(seed).shuffle(order)
    return order


def write(f, msg):
    b = msg.SerializeToString()
    f.write(_VarintBytes(len(b)))
    f.write(b)


class ET:
    def __init__(self, npus, prefix):
        self.f = {p: open(f"{prefix}.{p}.et", "wb") for p in range(npus)}
        self.nid = {p: 0 for p in range(npus)}
        self.last = {p: None for p in range(npus)}
        for f in self.f.values():
            write(f, GlobalMetadata(version="0.0.4"))

    def node(self, p, kind, name, attrs, extra_deps=()):
        n = Node(id=self.nid[p], name=name, type=kind)
        self.nid[p] += 1
        deps = [d for d in [self.last[p], *extra_deps] if d is not None]
        n.data_deps.extend(deps)
        n.attr.append(A(name="is_cpu_op", bool_val=False))
        n.attr.extend(attrs)
        write(self.f[p], n)
        self.last[p] = n.id
        return n.id

    def comp(self, p, name, flops, nbytes):
        return self.node(p, COMP_NODE, name, [A(name="num_ops", uint64_val=int(max(1, flops))),
                                              A(name="tensor_size", uint64_val=int(max(1, nbytes)))])

    def allreduce(self, p, name, nbytes, pg):
        return self.node(p, COMM_COLL_NODE, name, [A(name="comm_type", int64_val=ALL_REDUCE),
                                                   A(name="comm_size", uint64_val=int(nbytes)),
                                                   A(name="pg_name", string_val=str(pg))])

    def send(self, p, dst, nbytes, tag):
        return self.node(p, COMM_SEND_NODE, "send", [A(name="comm_src", int32_val=p), A(name="comm_dst", int32_val=dst),
                                                     A(name="comm_size", uint64_val=int(nbytes)),
                                                     A(name="comm_tag", int32_val=tag)])

    def recv(self, p, src, nbytes, tag):
        return self.node(p, COMM_RECV_NODE, "recv", [A(name="comm_src", int32_val=src), A(name="comm_dst", int32_val=p),
                                                     A(name="comm_size", uint64_val=int(nbytes)),
                                                     A(name="comm_tag", int32_val=tag)])

    def close(self):
        for p, f in self.f.items():
            if self.nid[p] == 0:  # idle NPU: one 1-tick compute node
                n = Node(id=0, name="idle", type=COMP_NODE, duration_micros=0)
                n.attr.extend([A(name="is_cpu_op", bool_val=False), A(name="num_ops", uint64_val=24000),  # 1 ns at 24 TFLOPS
                               A(name="tensor_size", uint64_val=1)])
                write(f, n)
            f.close()


def layer(et, p, tp, pg, n, ctx, tag):
    """One decoder layer on TP rank p for n new tokens attending to ctx tokens."""
    d, f = D_MODEL, FFN
    act = n * d * B
    et.comp(p, f"qkv{tag}", 2 * n * d * 3 * d / tp, (3 * d * d * B) / tp + act)
    kv = 2 * ctx * d * B / tp  # K and V cache read
    et.comp(p, f"attn{tag}", 2 * 2 * n * ctx * d / tp, kv + 2 * act / tp)
    et.comp(p, f"oproj{tag}", 2 * n * d * d / tp, d * d * B / tp + act / tp)
    et.allreduce(p, f"ar_attn{tag}", act, pg)
    et.comp(p, f"mlp_up{tag}", 2 * n * d * 2 * f / tp, 2 * d * f * B / tp + act)
    et.comp(p, f"mlp_down{tag}", 2 * n * f * d / tp, f * d * B / tp + n * f * B / tp)
    et.allreduce(p, f"ar_mlp{tag}", act, pg)


def main(out, placement, tp=8, pp=6, m=6, s=512, dec=8):
    tp, pp, m, s, dec = map(int, (tp, pp, m, s, dec))
    order = tiles(placement)
    assert tp * pp == len(order)
    stage = [order[i * tp:(i + 1) * tp] for i in range(pp)]
    layers = [L // pp + (1 if i < L % pp else 0) for i in range(pp)]
    os.makedirs(f"{out}/et", exist_ok=True)
    json.dump({str(i + 1): stage[i] for i in range(pp)}, open(f"{out}/comm_groups.json", "w"))
    json.dump(dict(placement=placement, tp=tp, pp=pp, m=m, s=s, decode=dec, stages=stage, layers=layers),
              open(f"{out}/layout.json", "w"), indent=1)
    et = ET(W7 * W7, f"{out}/et/wl")
    steps = [(-1, mb) for mb in range(m)] + [(t, mb) for t in range(dec) for mb in range(m)]

    def act_tag(k, st, r):  # activation stage st-1 -> st at step k
        return k * 100 + st * 10 + r

    def tok_tag(t, mb, r):  # token consumed by decode step t of sequence mb
        return 100_000_000 + t * 100_000 + mb * 100 + r

    for k, (t, mb) in enumerate(steps):
        n = s if t < 0 else 1
        ctx = s if t < 0 else s + t + 1
        act = n * D_MODEL * B
        for st in range(pp):
            for r, p in enumerate(stage[st]):
                if st > 0:
                    et.recv(p, stage[st - 1][r], act, act_tag(k, st, r))
                elif t >= 0:  # token from the last stage
                    et.recv(p, stage[pp - 1][r], 64, tok_tag(t, mb, r))
                for li in range(layers[st]):
                    layer(et, p, tp, st + 1, n, ctx, f"_s{st}_l{li}_t{t}_m{mb}")
                if st < pp - 1:
                    et.send(p, stage[st + 1][r], act, act_tag(k, st + 1, r))
                else:
                    et.comp(p, "lm_head", 2 * D_MODEL * VOCAB / tp, D_MODEL * VOCAB * B / tp)
                    if t + 1 < dec:
                        et.send(p, stage[0][r], 64, tok_tag(t + 1, mb, r))
    et.close()
    print("nodes per npu:", max(et.nid.values()))


if __name__ == "__main__":
    main(*sys.argv[1:])
