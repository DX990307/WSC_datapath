# T1 检查 MGPUSim 的指令支持

> 🔒 **执行前必做**：按 `../CONFIG.md` 第 3 节做配置确认（检查文件有没有变 → 打印完整参数 → 列出和论文配置的差异 → **等用户确认**）。论文配置不可修改（用户 10-13）。

**状态**：`[已定]` · **周次**：W1 第 1 天 · **依赖**：无 · **被依赖**：T5a–T5d（所有新 workload）

> **项目背景（重复）**：CuPath（HPCA 2027 #1329 被拒）改投 ISCA 2027。审稿人 F 批评 benchmark 太旧，所以要移植 4 个现代 workload（T5a–T5d）到 MGPUSim。MGPUSim 模拟的是 AMD GCN3 ISA，新 kernel 需要编译成 GCN3 的 HSACO。
> 索引：`../README.md` · 背景：`../00_background.md` · 进度写入：`../STATUS.md`

## 为什么做

新 workload 需要用到一些现有 benchmark 很少用的指令：global atomic（DLRM 反向的 scatter-add、hash join 的 build 阶段）、`exp` 和 `rsqrt`（softmax、RMSNorm）。MGPUSim 的 GCN3 模拟器不一定全部实现了这些指令。**要在第一天就把缺什么查清楚**，免得移植到一半才卡住。

## 要做什么

1. 写一个最小的 OpenCL kernel（或 HIP kernel，看哪个能编译到 gfx803 并被 MGPUSim 加载），覆盖：
   - `atomic_add`（int 和 float；float 不支持的话，测试用 CAS 循环模拟）、`atomic_cmpxchg`
   - 32、64、128 位的 global load/store（`float`、`float2`、`float4`）
   - `exp`、`native_exp`、`rsqrt`、`fmax`
   - LDS 读写，加 `barrier`
   - 一个 grid-stride 循环
2. 参考现有 benchmark 的编译流程，编译成 GCN3 HSACO
3. 直接用 **timing 模式**运行（**不做 verify**，用户 10-12 的决定），确认不会因为不支持的指令而报错或崩溃，并且所有 kernel 都能跑完

## 验收标准

- [ ] 在 `STATUS.md` 里列一张表：每类指令是“能跑通 / 不支持（贴出报错信息）”
- [ ] 遇到不支持的指令，写明替代方案（比如用 CAS 循环模拟 float atomic add），**不要私自修改模拟器去实现它**，先报告用户

## 注意

- 所有新 kernel 一律用 fp32，不用矩阵核心或 MFMA（GCN3 本身也没有）。
- 把编译命令写进脚本，后面 T5a–T5d 都会复用。

## 调研补充（10-12，Cowork 侧）

- 上游 MGPUSim（sarchlab/mgpusim）的 `CLAUDE.md` 中写明：
  - benchmark 放在 `amd/benchmarks/` 下，用 `//go:embed kernels.hsaco` 嵌入，用 `kernels.LoadProgramFromMemory(data, "kernelName")` 加载
  - HIP→HSACO 推荐使用 Docker 镜像 `rocm/dev-ubuntu-24.04:7.2.4`
  - 支持两种 HSACO 格式：V2/V3（GCN3 使用，在 `.text` 中每个 kernel 有 256B 头）和 V5（在 `.rodata` 中有 64B 的 kernel descriptor）
  - 平台配置在 `samples/*/platform.go`、`r9nano.go`、`shaderarray.go`
- **先确认 CuPath 基于的 MGPUSim 版本是否支持 V5 和 HIP 这条编译路线。**
  - 如果支持：新 workload 优先用 HIP 写，这样可以直接改写 CUDA 代码（比如 T5d 的 Crystal），编译目标是 gfx803
  - 如果不支持：沿用现有的 OpenCL 流程
- 用两条路线各编译一次本任务的测试 kernel，把结果记进 `STATUS.md`。
