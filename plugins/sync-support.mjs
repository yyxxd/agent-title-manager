import { readFileSync, writeFileSync } from "node:fs";

// 每个发布目录都可以独立安装。共享源码只在 shared/ 维护。
const check = process.argv.includes("--check");
const sets = [
  ["client.mjs", ["codex", "claude", "pi", "antigravity", "example"]],
];
for (const [file, agents] of sets) {
  const canonical = readFileSync(new URL("./shared/" + file, import.meta.url), "utf8");
  for (const agent of agents) {
    const path = new URL("./" + agent + "/" + file, import.meta.url);
    if (!check) writeFileSync(path, canonical);
    else if (readFileSync(path, "utf8") !== canonical) {
      process.stderr.write(agent + "/" + file + " 与共享源码不一致\n");
      process.exitCode = 1;
    }
  }
}
