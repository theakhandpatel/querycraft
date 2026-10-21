import { spawnSync } from "node:child_process";
import { existsSync } from "node:fs";
import path from "node:path";
import process from "node:process";

const root = path.resolve(import.meta.dirname, "..");
const arch = process.arch === "x64" || process.arch === "arm64" ? process.arch : null;
const musl = arch
  ? path.join(root, "node_modules", `@tsrs/linux-${arch}-musl`, "tsrs")
  : null;
const env = { ...process.env };

if (
  process.platform === "linux" &&
  musl &&
  existsSync(musl) &&
  !env.TSRS_BINARY
) {
  env.TSRS_BINARY = musl;
}

const bin = path.join(root, "node_modules", "tsrs", "bin", "tsrs");
const result = spawnSync(process.execPath, [bin, ...process.argv.slice(2)], {
  env,
  stdio: "inherit",
});

if (result.error) {
  throw result.error;
}

process.exit(result.status ?? 1);
