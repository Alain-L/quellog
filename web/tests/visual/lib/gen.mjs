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
  // The CLI prints "Report saved to <name>" with the exact filename it
  // chose (generateHTMLFilename in cmd/execute.go only strips a handful of
  // known extensions — .log/.csv/.gz/.zst/.tar/.tgz — so a fixture with any
  // other extension, e.g. a bare console-output capture named "*.out",
  // keeps it: "foo.out" -> "foo.out.html", not "foo.html". Reading the
  // line back is more robust than reimplementing that rule here.
  const match = stdout.match(/^Report saved to (\S+)/m);
  const produced = match ? join(tmpDir, match[1]) : join(tmpDir, `${fixturePath.split('/').pop().replace(/\.[^.]+$/, '')}.html`);
  if (!existsSync(produced)) {
    throw new Error(`expected report ${produced} was not produced.\nCLI output: ${stdout}`);
  }
  const outPath = join(tmpDir, outName);
  renameSync(produced, outPath);
  return outPath;
}
