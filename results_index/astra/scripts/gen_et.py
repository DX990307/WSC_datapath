"""Chakra ET generator for closed-loop remote reads on ASTRA-sim (P18).

Every requester r has a sequence of owners o_1..o_N. Request k is a SEND
r->o_k (req_bytes) that depends on the RECV of response k-W, so at most W
reads are in flight per requester. Each owner RECVs the request and SENDs
the response (rsp_bytes) back after send_delay ticks (owner service time).

With "open": true there are no window dependencies; every request is a
SEND that is dependency-free at t = 0 and carries send_delay = its injection
time, so requests enter the network exactly at the given times (open loop).

spec (JSON): {"npus": 49, "W": 64, "req_bytes": 16, "rsp_bytes": 96,
              "owner_delay": {owner: ticks} or ticks, "open": false,
              "seq": {requester: [owner | [owner, req_bytes, rsp_bytes, t_inject], ...]}}
usage: python3 gen_et.py spec.json out_dir/prefix
"""
import json, os, sys
HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, os.environ.get("CHAKRA_PB_DIR", HERE))
from et_def_pb2 import (COMM_RECV_NODE, COMM_SEND_NODE, COMP_NODE, AttributeProto as A,
                        GlobalMetadata, Node)
from google.protobuf.internal.encoder import _VarintBytes


def write(f, msg):
    b = msg.SerializeToString()
    f.write(_VarintBytes(len(b)))
    f.write(b)


def comm(nid, kind, src, dst, size, tag, deps=(), delay=0):
    n = Node(id=nid, name="", type=kind)
    n.data_deps.extend(deps)
    n.attr.extend([A(name="is_cpu_op", bool_val=False),
                   A(name="comm_src", int32_val=src), A(name="comm_dst", int32_val=dst),
                   A(name="comm_size", uint64_val=size), A(name="comm_tag", int32_val=tag)])
    if delay:
        n.attr.append(A(name="send_delay", uint64_val=int(delay)))
    return n


def main(spec_path, prefix):
    s = json.load(open(spec_path))
    npus, W = s["npus"], s["W"]
    req, rsp = s["req_bytes"], s["rsp_bytes"]
    od = s["owner_delay"]
    delay = (lambda o: od.get(str(o), 0)) if isinstance(od, dict) else (lambda o: od)
    os.makedirs(os.path.dirname(prefix) or ".", exist_ok=True)
    files = {p: open(f"{prefix}.{p}.et", "wb") for p in range(npus)}
    nid = {p: 1 for p in range(npus)}
    count = 0
    for p, f in files.items():
        write(f, GlobalMetadata(version="0.0.4"))
        # a 1-tick compute node (id 0) so that idle NPUs have a dependency-free node
        idle = Node(id=0, name="", type=COMP_NODE, duration_micros=0)
        idle.attr.append(A(name="is_cpu_op", bool_val=False))
        write(f, idle)
    tag = 0

    def new_id(p):
        nid[p] += 1
        return nid[p] - 1

    def emit(p, n):
        nonlocal count
        write(files[p], n)
        count += 1

    for r, owners in sorted(s["seq"].items(), key=lambda kv: int(kv[0])):
        r = int(r)
        recv_ids = []
        for k, e in enumerate(owners):
            o, q, a, t = (e, req, rsp, 0) if isinstance(e, int) else e
            if s.get("open"):
                deps, d = [], t
            else:
                deps, d = ([recv_ids[k - W]] if k >= W else []), 0
            emit(r, comm(new_id(r), COMM_SEND_NODE, r, o, q, tag, deps, d))
            rid = new_id(r)
            recv_ids.append(rid)
            emit(r, comm(rid, COMM_RECV_NODE, o, r, a, tag))
            oid = new_id(o)
            emit(o, comm(oid, COMM_RECV_NODE, r, o, q, tag))
            emit(o, comm(new_id(o), COMM_SEND_NODE, o, r, a, tag, [oid], delay(o)))
            tag += 1
    assert tag < 500_000_000
    for f in files.values():
        f.close()
    print(f"{tag} reads, {count} nodes")


if __name__ == "__main__":
    main(*sys.argv[1:3])
