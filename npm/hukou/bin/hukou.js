#!/usr/bin/env node
'use strict';

// npm wrapper for hukou: execs the platform binary shipped in the matching
// optional dependency (the esbuild-style layout). Exit codes and signals of
// the real hukou process are forwarded 1:1.

const { spawn } = require('child_process');

const platform = { darwin: 'darwin', linux: 'linux' }[process.platform];
const arch = { x64: 'amd64', arm64: 'arm64' }[process.arch];
if (!platform || !arch) {
  console.error(`hukou: unsupported platform ${process.platform}/${process.arch}`);
  process.exit(1);
}

const pkg = `hukou-${platform}-${arch}`;
let bin;
try {
  bin = require.resolve(`${pkg}/bin/hukou`, { paths: [__dirname] });
} catch {
  console.error(
    `hukou: platform package ${pkg} is not installed (it is an optional dependency); ` +
    `run "npm install ${pkg}" first`
  );
  process.exit(1);
}

const child = spawn(bin, process.argv.slice(2), { stdio: 'inherit' });
for (const sig of ['SIGINT', 'SIGTERM']) {
  process.on(sig, () => {
    child.kill(sig);
  });
}
child.on('exit', (code, signal) => {
  if (signal) {
    // Re-raising the signal at ourselves only reproduces the child's death if
    // no listener intercepts it — our own forwarding handlers above would
    // swallow it and turn a signal death into exit code 0. Remove them first
    // so the default action (die by the same signal) applies. SIGKILL and
    // SIGSTOP can never be caught, so they were never registered and need no
    // removal.
    process.removeAllListeners(signal);
    process.kill(process.pid, signal);
    return;
  }
  process.exit(code === null ? 1 : code);
});
