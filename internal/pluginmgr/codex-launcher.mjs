import { spawn } from "node:child_process";
import { lstatSync, readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const root = dirname(fileURLToPath(import.meta.url));
const [registration, mode] = process.argv.slice(2);

try {
  const state = JSON.parse(readFileSync(join(root, "state.json"), "utf8"));
  const item = state.items?.codex;
  if (!/^atm-registration-[A-Z2-7]{26}$/.test(registration) || !["start", "stop"].includes(mode) ||
      state.pending?.id === "codex" || item?.enabled !== true ||
      !item.entry?.endsWith(" " + registration) || !/^pkg-[0-9]{8}-[0-9]+$/.test(item.dir) ||
      item.manifest?.id !== "codex" || item.manifest.host !== "codex" || item.manifest.entry !== "index.mjs") {
    process.stdout.write("{}\n");
  } else {
    const packageDir = join(root, "packages", item.dir);
    const entry = join(packageDir, "index.mjs");
    if (!lstatSync(packageDir).isDirectory() || !lstatSync(entry).isFile()) {
      throw new Error("Codex Hook 包不可用");
    }
    const runtime = JSON.parse(readFileSync(join(packageDir, "atm-runtime.json"), "utf8"));
    const env = { ...process.env };
    delete env.ATM_DATA_DIR;
    if (runtime.atmDataDir === dirname(root)) env.ATM_DATA_DIR = runtime.atmDataDir;
    else if (runtime.atmLocalAppData === dirname(dirname(root))) env.LOCALAPPDATA = runtime.atmLocalAppData;
    else throw new Error("Codex Hook 数据目录无效");
    delete env.NODE_OPTIONS;
    delete env.NODE_PATH;
    const child = spawn(process.execPath, [entry, registration, mode], {
      stdio: "inherit", windowsHide: true, env,
    });
    await new Promise((resolve, reject) => {
      child.once("error", reject);
      child.once("exit", code => code === 0 ? resolve() : reject(new Error("Codex Hook 退出异常")));
    });
  }
} catch {
  process.stderr.write("标题管理器无法启动 Codex Hook，请检查插件状态。\n");
  process.stdout.write("{}\n");
}
