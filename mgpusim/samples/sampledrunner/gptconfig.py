def _cmd(op, **kwargs):
    parts = ["./llmop", "-op=%s" % op]
    for key in sorted(kwargs):
        value = kwargs[key]
        if value is not None:
            parts.append("-%s=%s" % (key.replace("_", "-"), value))
    return " ".join(parts)


def _linear(rows, input_dim, output_dim):
    return _cmd("linear", rows=rows, input_dim=input_dim, output_dim=output_dim)


def _gpt_commands(
    batch=1,
    seq_len=64,
    hidden=512,
    heads=8,
    layers=2,
    intermediate=2048,
):
    rows = batch * seq_len

    commands = []
    commands.append(_cmd("embedding", rows=rows, hidden=hidden))

    for _ in range(layers):
        commands.append(_cmd("layernorm", rows=rows, hidden=hidden))
        commands.append(_linear(rows, hidden, hidden))  # q
        commands.append(_linear(rows, hidden, hidden))  # k
        commands.append(_linear(rows, hidden, hidden))  # v
        commands.append(_linear(rows, hidden, rows))  # q * k^T
        commands.append(_cmd(
            "causal-mask", rows=rows, seq_len=seq_len, batch_size=batch))
        commands.append(_cmd("row-softmax", rows=rows, cols=rows))
        commands.append(_linear(rows, rows, hidden))  # probs * v
        commands.append(_linear(rows, hidden, hidden))  # attention out
        commands.append(_cmd("residual-add", elements=rows * hidden))

        commands.append(_cmd("layernorm", rows=rows, hidden=hidden))
        commands.append(_linear(rows, hidden, intermediate))
        commands.append(_cmd("gelu", elements=rows * intermediate))
        commands.append(_linear(rows, intermediate, hidden))
        commands.append(_cmd("residual-add", elements=rows * hidden))

    return commands


def init_gpt():
    return {"llmop": _gpt_commands()}


def run_gpt(benchmarks):
    return [("llmop", cmd) for cmd in benchmarks["llmop"]]
