# T17 datasets

Downloaded on 2026-10-16 (22:14 UTC, yss2 clock) into
`yss2:~/akita-exp-test/datasets/` with `fetch.sh` in that directory (curl).
Nothing but the small derived file below is in the git repository. The user
approved all three downloads; the parent checked the licences.

| Dataset | Used by | Source | Files (size, md5) | Licence |
|---|---|---|---|---|
| Azure LLM inference trace 2023, conversation (+ code) | W-A `pagedattn` lengths | github.com/Azure/AzurePublicDataset, `data/AzureLLMInferenceTrace_conv.csv`, `..._code.csv` | conv: 719,188 B, 19,366 requests, md5 `2ec51bd013f310fa72cac08de1331368`; code: 320,117 B, 8,819 requests, md5 `5429a38bc2999f29bf67c1ae0f98be57` | CC-BY 4.0 (`licenses/AzurePublicDataset_LICENSE`). Attribution: Patel et al., "Splitwise: Efficient generative LLM inference using phase splitting", ISCA 2024 |
| IGBH tiny (IGB heterogeneous) | W-C `rgat` | igb-public-awsopen.s3.amazonaws.com/igb-heterogeneous/igb_heterogeneous_tiny.tar.gz (the URL of `igb.download.download_dataset(path, 'heterogeneous', 'tiny')`, IGB-Datasets repo, `licenses/IGB-Datasets_download.py`) | 2,073,557,891 B, md5 `83fbc1091497ff92cf20afe82fae0ade` (equals the md5 in the IGB downloader); extracted `igbh/tiny/`, 2.33 GB | data: ODC-By 1.0 / CC-BY 4.0 (checked by the parent); repository code MIT (`licenses/IGB-Datasets_LICENSE`) |
| ogbn-mag (fallback only, not used) | documentation | snap.stanford.edu/ogb/data/nodeproppred/mag.zip | 431,762,160 B, md5 `c90bfb7cced43ddd2dc32684cf43ad6b`; extracted `ogbn_mag/mag/`, 434 MB | ODC-BY (MAG); OGB code MIT (`licenses/ogb_LICENSE`) |

## Azure conversation trace (W-A)

19,366 requests (TIMESTAMP, ContextTokens, GeneratedTokens). ContextTokens:
mean 1,155, median 1,020, p99 4,142, max 14,050; GeneratedTokens: mean 211,
max 1,000. Requests longer than 8,192 tokens: 0.01 %.

`pagedattn/azure_conv_tokens.csv` (158 KB, committed, embedded in the
binary) holds the (ContextTokens, GeneratedTokens) pairs of every conv
request in trace order, nothing else. Mapping to a decode batch
(`pagedattn.TraceDist`): each sequence is a request drawn uniformly, caught
in the middle of its decode phase: context length = ContextTokens + g with g
uniform in 1..GeneratedTokens, clipped to [16, 8192] (Llama-3-8B context);
its ContextTokens are allocated at prefill and the rest block by block during
decode. Mean decode-time length is about 1,260 tokens. The log-normal option
remains (`-pa-len-source=lognormal`).

## IGBH tiny (W-C)

`processed/` holds `.npy` files (all node types have 1024-dim float32
features):

| Node type | Nodes |
|---|---|
| paper | 100,000 |
| author | 357,041 |
| institute | 8,738 |
| fos | 84,220 |
| journal | 8,101 |
| conference | 398 |
| total | 558,498 (2.29 GB of features) |

| Edge file (src -> dst) | Edges |
|---|---|
| paper__cites__paper | 447,416 |
| paper__written_by__author | 471,443 |
| author__affiliated_to__institute | 325,410 |
| paper__topic__fos | 718,445 |
| paper__published__journal | 85,765 |
| paper__venue__conference | 1,512 |

`rgat.LoadIGBH` reads these directly (no derived files): `cites` is
symmetrised and the other five are also used reversed (`rev_*`), 11
relations and 4,099,982 directed edges, following the MLPerf RGAT / IGB
heterogeneous DGL loader. Paper labels are not used (inference only, random
weights).

## ogbn-mag (not used)

1,939,743 nodes (paper 736,389, author 1,134,649, institution 8,740,
field_of_study 59,965), 21,111,007 edges in 4 relations (affiliated_with
1,043,998; writes 7,145,660; cites 5,416,271; has_topic 7,505,078); only
papers have features (128-dim). It would need feature generation for the
other types, so IGBH tiny is used instead.
