// 只有桌面服务持有这对管道。工作台标识来自当前数据库记录，而非 Hook 请求。
export function parentRPC(input, output, diagnostic) {
  let sequence = 0;
  const pending = new Map();
  input.on("line", line => {
    try {
      const { id, result, error } = JSON.parse(line);
      pending.get(id)?.(error ? new Error(error) : null, result);
    } catch (error) { diagnostic("observer.reply.invalid", error); }
  });
  input.once("close", () => {
    for (const settle of pending.values()) settle(new Error("Observer parent disconnected"));
  });
  return (row, method, params, signal) => new Promise((resolve, reject) => {
    if (input.closed || signal?.aborted) return reject(new Error("Observer stopped"));
    const id = ++sequence;
    const line = JSON.stringify({ id, orcaSessionId: row?.orca, method, params }) + "\n";
    const abort = () => settle(new Error("Observer stopped"));
    const settle = (error, result) => {
      if (!pending.delete(id)) return;
      signal?.removeEventListener("abort", abort);
      if (error) reject(error);
      else resolve(result);
    };
    pending.set(id, settle);
    signal?.addEventListener("abort", abort, { once: true });
    try {
      output.write(line, error => { if (error) settle(error); });
    } catch (error) {
      settle(error);
    }
  });
}
