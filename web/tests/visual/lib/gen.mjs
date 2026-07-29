// Report generation: always regenerate fresh HTML reports from the fixture.
// Never reuse an existing report — stale-report comparisons have burned us before.

import { execFileSync } from 'node:child_process';
import { existsSync, mkdirSync, renameSync, rmSync } from 'node:fs';
import { join } from 'node:path';

/**
 * Wipe and recreate the tmp working directory (tmp/ and tmp/diff/).
 * @returns {{tmpDir: string, diffDir: string}}
 */
export function resetTmpDir(visualDir) {
  const tmpDir = join(visualDir, 'tmp');
  const diffDir = join(tmpDir, 'diff');
  rmSync(tmpDir, { recursive: true, force: true });
  mkdirSync(diffDir, { recursive: true });
  return { tmpDir, diffDir };
}

/**
 * Run `bin/quellog <fixture> --html [extraArgs...]` with cwd=tmpDir, then
 * rename the produced `<fixture-basename>.html` to `outName`.
 * @returns {string} absolute path of the generated report
 */
export function generateReport(binPath, fixturePath, tmpDir, outName, extraArgs = []) {
  if (!existsSync(binPath)) {
    throw new Error(`CLI binary not found at ${binPath} — build it first (go build -o bin/quellog .)`);
  }
  if (!existsSync(fixturePath)) {
    throw new Error(`fixture not found: ${fixturePath}`);
  }
  const stdout = execFileSync(binPath, [fixturePath, '--html', ...extraArgs], {
    cwd: tmpDir,
    encoding: 'utf8',
    stdio: ['ignore', 'pipe', 'inherit'],
  });
  // The CLI writes <input-basename>.html into its cwd.
  const base = fixturePath.split('/').pop().replace(/\.[^.]+$/, '');
  const produced = join(tmpDir, `${base}.html`);
  if (!existsSync(produced)) {
    throw new Error(`expected report ${produced} was not produced.\nCLI output: ${stdout}`);
  }
  const outPath = join(tmpDir, outName);
  renameSync(produced, outPath);
  return outPath;
}
