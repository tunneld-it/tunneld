#!/usr/bin/env node
// Launcher for the tunneld npm package.
//
// The Go binary is the whole program; this file finds the build for the host
// and hands it the process. Two places to look, in order:
//
//   1. A platform package, @tunnel-pizza/tunneld-<platform>-<arch>: one per
//      platform, each holding a single binary and declaring os/cpu so npm
//      installs only the one that matches the host. They are listed as
//      optionalDependencies of this package, pinned to the same version.
//   2. dist/tunneld-<platform>-<arch> beside this package: what `make
//      binaries` writes, so a checkout runs with `npx .`, and what a package
//      that ships every binary in dist/ carries.
//
// The key is process.platform + "-" + process.arch, which is also how the
// Makefile names its targets; win32 adds .exe. An x64 Node under Rosetta 2
// reports x64 and gets the darwin-x64 binary, which is fine: the Go build has
// no AVX requirement, so it runs translated.
//
// The binary is spawned asynchronously rather than with spawnSync so that this
// process outlives a Ctrl-C: the terminal delivers SIGINT to both, the tunnel
// tears down, and only then does the prompt come back carrying the binary's
// exit status. A signal aimed at this process alone (kill, a supervisor's
// SIGTERM) is forwarded, so the tunnel never outlives its launcher. The
// duplicate a Ctrl-C produces is harmless: tunneld's signal context swallows
// repeats until it exits.
//
// Beyond that, the launcher does what the binary does not:
//
//   - It refuses a command line that could mean two things (misread, below),
//     before the run starts, and names the quoting for each.
//   - `-k`, as the only word, ends every run on the machine, detached or
//     not, the way Ctrl-C would, so the teardown runs; `-k<pid>` ends the one
//     run with that pid. The binary registers each run as a file it holds
//     open beside its cached spec (v1alpha1/cache), and -k ends whatever
//     holds one.
//
// -d and -l are the binary's own flags, so they reach it as typed. -k is
// read only as the only word; anything else, -kd included, is the binary's,
// and it refuses them as unknown flags, so a brew user and an npx user never
// mean different things by the same flag.

const { spawn, execFileSync } = require("child_process");
const fs = require("fs");
const { constants } = require("os");
const path = require("path");

const PACKAGE_PREFIX = "@tunnel-pizza/tunneld";
const BINARY_NAME = "tunneld";
const WRAPPER_NAME = require("../package.json").name;

// Keep in step with PLATFORMS in the Makefile.
const PLATFORMS = [
  "darwin-arm64",
  "darwin-x64",
  "linux-arm64",
  "linux-x64",
  "win32-arm64",
  "win32-x64",
];

// How long -k waits for a run it has signalled to finish tearing down, and
// how often it looks.
const STOP_MS = 30000;
const POLL_MS = 50;


// The status the binary exits with when another run holds this one's
// lock. Keep in step with RunningCode in v1/v1.go.
const RUNNING = 75;


function fail(message) {
  console.error(`${WRAPPER_NAME}: ${message}`);
  process.exit(1);
}

function binaryPath() {
  const key = `${process.platform}-${process.arch}`;
  if (!PLATFORMS.includes(key)) {
    fail(
      `unsupported platform ${process.platform} ${process.arch}; supported: ${PLATFORMS.join(", ")}`,
    );
  }
  const exe = process.platform === "win32" ? ".exe" : "";
  const pkg = `${PACKAGE_PREFIX}-${key}`;

  try {
    const dir = path.dirname(require.resolve(`${pkg}/package.json`));
    return path.join(dir, `${BINARY_NAME}${exe}`);
  } catch {
    // Not installed: --omit=optional, a lockfile written on another platform,
    // or a checkout. Fall through to the local build.
  }

  const local = path.join(__dirname, "..", "dist", `${BINARY_NAME}-${key}${exe}`);
  if (fs.existsSync(local)) {
    return local;
  }

  fail(
    `no binary for ${key}: ${pkg} is not installed and ${local} does not exist.\n` +
      "  Reinstall without --omit=optional. In a checkout, npm run dev -- <args> builds it and runs,\n" +
      "  or make host builds it for npx .",
  );
}

// isOrigin mirrors the Go parser's: a word that can only be an origin, a bare
// port (":3000") or a URL with a scheme. It is what ends a program's
// arguments.
function isOrigin(word) {
  if (word.startsWith(":")) {
    return /^:\d+$/.test(word);
  }
  return word.includes("://");
}

// onPath reports whether a word names a program on $PATH, the way the Go
// parser decides a bare word is a program: $PATH and the executable bit on
// Unix, PATHEXT on Windows. A relative $PATH entry (an empty one is the
// working directory) is skipped, as Go's exec.LookPath refuses what it finds
// there.
function onPath(word) {
  if (!word || word.includes("/") || word.includes(path.sep)) {
    return false;
  }
  const win = process.platform === "win32";
  let exts = [""];
  if (win) {
    exts = (process.env.PATHEXT || ".COM;.EXE;.BAT;.CMD").split(";").filter(Boolean);
    if (exts.some((ext) => word.toLowerCase().endsWith(ext.toLowerCase()))) {
      exts = [""];
    }
  }
  for (const dir of (process.env.PATH || "").split(path.delimiter)) {
    if (!path.isAbsolute(dir)) {
      continue;
    }
    for (const ext of exts) {
      try {
        const st = fs.statSync(path.join(dir, word + ext));
        if (st.isFile() && (win || st.mode & 0o111)) {
          return true;
        }
      } catch {
        // Not here; the next directory may have it.
      }
    }
  }
  return false;
}

// quote spells a word so that pasting it back into a shell gives the same
// word: as it is when nothing in it is special, single-quoted otherwise, or
// double-quoted for cmd.exe.
function quote(word) {
  if (/^[\w@%+=:,./-]+$/.test(word)) {
    return word;
  }
  if (process.platform === "win32") {
    return `"${word.replace(/"/g, '\\"')}"`;
  }
  return `'${word.replace(/'/g, "'\\''")}'`;
}

// misread looks for the command line that could mean two things:
//
//   npx tunneld claude "next dev"
//
// A bare program is greedy: it takes every word after it up to one that can
// only be an origin, so as written that is one program, claude, with "next
// dev" as its argument. Whoever typed it more likely meant two, and quoted
// "next dev" because a quoted group is complete. The quotes are gone before
// anything here runs, but what they left is visible: a word with whitespace
// inside it, whose first word is itself a program. That word, in a bare
// program's arguments and not the value of a flag before it (`sh -c "npm run
// dev"` is one program on purpose), is the case.
//
// The launcher refuses such a line rather than guessing, since a guess either
// way starts somebody a program they did not ask for, in public. It returns
// null, or the two lines that each say one thing: `two`, the bare program
// moved last so the quoted word is an origin of its own, and `one`, the
// program quoted together with its arguments, which is a word with
// whitespace in it and so never a bare program. Neither is itself misread,
// so whichever gets pasted back runs. A prompt (`claude "fix the bug"`) is
// not refused, because fix is not a program.
//
// Words starting with - before the first origin are the binary's flags and
// are skipped; the value of one given as a separate word is looked at like
// any other, which only matters if it names a program.
function misread(words) {
  const found = locate(words);
  if (!found) {
    return null;
  }
  const { first, i, j, end } = found;
  const word = words[i];
  const arg = words[j];

  // Each reading goes where the program stood. If what comes before it is a
  // run of the same program — `bash bash "next dev"` — that run would swallow
  // the reading in turn, so there it goes ahead of every origin instead: a
  // quoted group is complete, so nothing it precedes is affected. Only there,
  // since this cannot tell `--log-level debug`'s value from an origin, and
  // moving ahead of one would split a flag.
  const place = (reading, rest) => {
    const here = [...words.slice(0, i), ...reading, ...words.slice(end), ...rest];
    if (locate(here) === null) {
      return here;
    }
    return [...words.slice(0, first), ...reading, ...words.slice(first, i), ...words.slice(end), ...rest];
  };
  return {
    program: word,
    arg,
    two: place([arg], [word, ...words.slice(i + 1, j), ...words.slice(j + 1, end)]),
    one: place([group(words.slice(i, end))], []),
  };
}

// locate finds what misread describes: the first positional word, the bare
// program at i, the argument at j that could be an origin, and the end of the
// program's run. Null when there is none.
function locate(words) {
  let first = 0;
  while (first < words.length && words[first].startsWith("-")) {
    first++;
  }
  for (let i = first; i < words.length; i++) {
    const word = words[i];
    if (isOrigin(word) || /\s/.test(word) || !onPath(word)) {
      continue;
    }
    let end = i + 1;
    while (end < words.length && !isOrigin(words[end]) && words[end] !== word) {
      end++;
    }
    for (let j = i + 1; j < end; j++) {
      const arg = words[j];
      if (!/\s/.test(arg) || words[j - 1].startsWith("-")) {
        continue;
      }
      if (onPath(arg.trim().split(/\s+/)[0])) {
        return { first, i, j, end };
      }
    }
    i = end - 1;
  }
  return null;
}

// group spells a program and its arguments as the one word the Go parser
// splits back into them: each word with anything special in it quoted with
// the quote quote() will not wrap the whole in, so the two nest.
function group(words) {
  return words
    .map((w) => {
      if (/^[\w@%+=:,./-]+$/.test(w)) {
        return w;
      }
      if (process.platform === "win32") {
        return `'${w}'`;
      }
      return `"${w.replace(/["\\$`]/g, "\\$&")}"`;
    })
    .join(" ");
}

// ambiguous is the refusal for a line misread found: both readings, each
// ready to paste back.
function ambiguous(reading, prefix) {
  const line = (words) => [prefix, ...words.map(quote)].join(" ");
  const two = line(reading.two);
  const one = line(reading.one);
  const width = Math.max(two.length, one.length);
  return (
    `ambiguous: ${quote(reading.arg)} could be ${reading.program}'s argument or an origin of its own.\n` +
    `  Say which with quotes:\n` +
    `    ${two.padEnd(width)}  two origins\n` +
    `    ${one.padEnd(width)}  one origin`
  );
}

// cacheDir is the binary's own: $TUNNELD_CACHE_DIR when set, else
// <user cache dir>/tunneld, Go's os.UserCacheDir spelled out. Each run
// registers there as <key>.pid, beside its spec, and logs to <key>.log, which
// the binary writes and this only names.
function cacheDir() {
  const env = process.env;
  if (env.TUNNELD_CACHE_DIR) {
    return path.resolve(env.TUNNELD_CACHE_DIR);
  }
  let base;
  switch (process.platform) {
    case "darwin":
      base = env.HOME && path.join(env.HOME, "Library", "Caches");
      break;
    case "win32":
      base = env.LocalAppData;
      break;
    default:
      base = env.XDG_CACHE_HOME || (env.HOME && path.join(env.HOME, ".cache"));
  }
  if (!base || !path.isAbsolute(base)) {
    fail("no cache directory: $HOME is not set");
  }
  return path.join(base, BINARY_NAME);
}

// Windows has no way for this process to deliver a Ctrl-C to one that is not
// on its console: process.kill there is TerminateProcess, which skips the
// teardown -k exists to run. A run -k could only cut short is not one to
// leave behind, so neither flag is offered there.
function refuseOnWindows(flag) {
  if (process.platform === "win32") {
    fail(`${flag} is not supported on Windows yet`);
  }
}

function read(file) {
  try {
    return fs.readFileSync(file, "utf8");
  } catch {
    return "";
  }
}

function signum(signal) {
  return 128 + (constants.signals[signal] ?? 0);
}

// start runs the binary on this process's own streams and waits for it,
// passing on its exit status. Listening keeps this process alive until the
// child is done. On POSIX a signal is also forwarded, for the case where only
// this pid was targeted. On Windows the console has already delivered Ctrl-C
// to the child, and child.kill() there is TerminateProcess, which would cut
// teardown short, so it only waits. -d is the binary's: it starts the run
// again in the background itself and exits once it is up, so this does too.
function start(bin, args) {
  const signals = ["SIGINT", "SIGTERM", "SIGHUP"];
  let child = null;

  // Leaves by letting the event loop run dry rather than process.exit, which
  // can cut short a write to a pipe that is still draining.
  const finish = (status) => {
    for (const signal of signals) {
      process.removeAllListeners(signal);
    }
    process.exitCode = status;
  };

  for (const signal of signals) {
    process.on(signal, () => {
      if (process.platform !== "win32") {
        child.kill(signal);
      }
    });
  }

  child = spawn(bin, args, { stdio: "inherit" });
  child.on("error", (err) => fail(`cannot run ${bin}: ${err.message}`));
  child.on("close", (code, signal) => {
    if (code === RUNNING) {
      console.error(
        `${WRAPPER_NAME}: it is running in the background or another terminal: npx ${WRAPPER_NAME} -k ends it`,
      );
    }
    // Node ignores some signals itself (SIGPIPE), so re-raising is
    // unreliable; report a signal death the way a shell does, 128+signum.
    finish(signal ? signum(signal) : (code ?? 1));
  });
}

// paint wraps text in an ANSI style when stderr is a terminal that wants
// one: not when it is a file or a pipe, not under NO_COLOR, not on a dumb
// terminal.

// holders is every run holding file open. A run keeps its <key>.pid open
// for as long as it runs, and the kernel closes it however the process ends,
// so this is the live runs registered there and nothing else — not a process
// that has since been given a dead run's pid. Two runs of the same thing at
// once share the file, so it can be more than one.
//
// A child the run forks holds the file too, from the fork until its exec
// closes it. That child is not a run, and its parent is one: a holder whose
// parent also holds the file is left out. Linux answers from /proc, where
// only this user's processes can be read, which are the only ones this could
// signal anyway; everywhere else lsof does, which macOS always has.
function holders(file) {
  const found = new Map(); // pid -> parent pid
  try {
    if (process.platform === "linux") {
      const target = fs.realpathSync(file);
      for (const pid of fs.readdirSync("/proc").filter((n) => /^\d+$/.test(n))) {
        let fds;
        try {
          fds = fs.readdirSync(`/proc/${pid}/fd`);
        } catch {
          continue; // Not ours, or gone.
        }
        for (const fd of fds) {
          try {
            if (fs.readlinkSync(`/proc/${pid}/fd/${fd}`) === target) {
              // stat is "pid (comm) state ppid ...", and comm may hold spaces
              // or parentheses of its own.
              const stat = fs.readFileSync(`/proc/${pid}/stat`, "utf8");
              found.set(Number(pid), Number(stat.slice(stat.lastIndexOf(")") + 2).split(" ")[1]));
              break;
            }
          } catch {
            // Closed or gone while being looked at.
          }
        }
      }
    } else {
      // -F pR: a p line per process, then its R line, the parent pid.
      const out = execFileSync("lsof", ["-F", "pR", "--", file], {
        encoding: "utf8",
        stdio: ["ignore", "pipe", "ignore"],
      });
      let pid;
      for (const line of out.split("\n")) {
        if (line.startsWith("p")) {
          pid = Number(line.slice(1));
          found.set(pid, 0);
        } else if (line.startsWith("R") && pid !== undefined) {
          found.set(pid, Number(line.slice(1)));
        }
      }
    }
  } catch {
    return [];
  }
  return [...found].filter(([, parent]) => !found.has(parent)).map(([pid]) => pid);
}

function alive(pid) {
  try {
    process.kill(pid, 0);
    return true;
  } catch (err) {
    return err.code === "EPERM";
  }
}

// runs is every registered run, as { pid, key }, sorted by key: whatever
// holds a <key>.pid in cacheDir, detached or not, npx or brew. A file nobody
// holds is a run that ended without removing it — kill -9, a crash — and is
// cleared without a word.
function runs() {
  const dir = cacheDir();
  let names = [];
  try {
    names = fs.readdirSync(dir);
  } catch (err) {
    if (err.code !== "ENOENT") {
      fail(`cannot read ${dir}: ${err.message}`);
    }
  }

  const found = [];
  for (const name of names.filter((n) => n.endsWith(".pid")).sort()) {
    const file = path.join(dir, name);
    const pids = holders(file);
    if (pids.length === 0) {
      fs.rmSync(file, { force: true });
      continue;
    }
    for (const pid of pids) {
      if (!found.some((r) => r.pid === pid)) {
        found.push({ pid, key: path.basename(name, ".pid") });
      }
    }
  }
  return found;
}

// kill ends every run, or only the one whose pid is only, with SIGINT, which
// is what Ctrl-C sends a foreground run, so each one tears down the way it
// would there: the programs its origins started, the attach servers, the
// tunnel. It signals runs and nothing else, so a pid that is no run is
// refused rather than signalled. It waits for them to finish, and names each
// one it stopped.
//
// One still going after STOP_MS is named and the exit status is 1: it was
// asked, and asking twice changes nothing, since the binary swallows repeats.
function kill(only) {
  const stopping = [];
  for (const run of runs()) {
    if (only && run.pid !== only) {
      continue;
    }
    try {
      process.kill(run.pid, "SIGINT");
    } catch (err) {
      console.error(`${WRAPPER_NAME}: cannot stop pid ${run.pid}: ${err.message}`);
      continue;
    }
    stopping.push(run);
  }

  if (stopping.length === 0) {
    if (only) {
      console.error(`${WRAPPER_NAME}: no run with pid ${only}`);
      process.exitCode = 1;
    } else {
      console.error(`${WRAPPER_NAME}: no runs`);
    }
    return;
  }

  const deadline = Date.now() + STOP_MS;
  const timer = setInterval(() => {
    for (const r of stopping.filter((r) => !r.done && !alive(r.pid))) {
      r.done = true;
      console.error(`${WRAPPER_NAME}: stopped pid ${r.pid} (${r.key})`);
    }
    const left = stopping.filter((r) => !r.done);
    if (left.length === 0) {
      clearInterval(timer);
      return;
    }
    if (Date.now() > deadline) {
      clearInterval(timer);
      for (const r of left) {
        console.error(`${WRAPPER_NAME}: pid ${r.pid} is still tearing down after ${STOP_MS / 1000}s`);
      }
      process.exitCode = 1;
    }
  }, POLL_MS);
}

// FLAG is the launcher's own first word: -k, or -k with a pid and no space
// between.
const FLAG = /^-k([1-9]\d*)?$/;

function main() {
  const argv = process.argv.slice(2);
  const match = FLAG.exec(argv[0] ?? "");
  if (match) {
    refuseOnWindows("-k");
    if (argv.length > 1) {
      fail("-k takes no arguments: it ends every run, or the one -k<pid> names");
    }
    kill(match[1] && Number(match[1]));
    return;
  }

  const reading = misread(argv);
  if (reading) {
    fail(ambiguous(reading, `npx ${WRAPPER_NAME}`));
  }

  start(binaryPath(), argv);
}

if (require.main === module) {
  main();
}

module.exports = { ambiguous, isOrigin, misread, onPath, quote };
