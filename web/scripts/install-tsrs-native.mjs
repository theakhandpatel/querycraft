import { execFileSync } from "node:child_process";
import { existsSync, mkdirSync, mkdtempSync, readFileSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import path from "node:path";
import process from "node:process";

// The native tsrs binary is linked against glibc. On older Linux hosts the
// static musl build runs without a glibc upgrade. npm refuses to install a
// musl package on a glibc host because of its libc field, so fetch the tarball
// directly and unpack it next to the gnu build.
const tsrsPackage = path.join(process.cwd(), "node_modules", "tsrs", "package.json");
if (!existsSync(tsrsPackage)) {
  process.exit(0);
}

const { version } = JSON.parse(readFileSync(tsrsPackage, "utf8"));
if (typeof version !== "string") {
  throw new Error("tsrs: cannot read the installed version");
}

const arch = process.arch === "x64" || process.arch === "arm64" ? process.arch : null;
const glibc = process.report?.getReport?.()?.header?.glibcVersionRuntime;

if (process.platform !== "linux" || !arch || !glibc) {
  process.exit(0);
}

const match = /^(\d+)\.(\d+)/.exec(glibc);
if (!match) {
  process.exit(0);
}
const [major, minor] = [Number(match[1]), Number(match[2])];
if (major > 2 || (major === 2 && minor >= 34)) {
  process.exit(0);
}

const packageName = `@tsrs/linux-${arch}-musl`;
const target = path.join(process.cwd(), "node_modules", packageName);

if (existsSync(path.join(target, "tsrs"))) {
  process.exit(0);
}

const temp = mkdtempSync(path.join(tmpdir(), "tsrs-musl-"));
try {
  const packed = execFileSync(
    "npm",
    ["pack", `${packageName}@${version}`, "--pack-destination", temp, "--silent"],
    { cwd: process.cwd(), encoding: "utf8" },
  );
  const archive = packed.trim().split(/\r?\n/).at(-1);
  if (!archive) {
    throw new Error("tsrs: npm pack did not return a tarball");
  }

  mkdirSync(target, { recursive: true });
  execFileSync(
    "tar",
    ["-xzf", path.join(temp, archive), "-C", target, "--strip-components=1"],
    { stdio: "inherit" },
  );
} finally {
  rmSync(temp, { recursive: true, force: true });
}

if (!existsSync(path.join(target, "tsrs"))) {
  throw new Error(`tsrs: the static ${arch} musl binary was not installed`);
}
