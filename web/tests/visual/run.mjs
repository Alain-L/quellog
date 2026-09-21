#!/usr/bin/env node
// Headless visual regression harness for quellog HTML reports (Gate 2).
//
// Usage (from repo root):
//   node web/tests/visual/run.mjs                     # check against baselines
//   node web/tests/visual/run.mjs --update-baselines  # (re)write baselines + structure contract
//
// Flow: regenerate reports fresh from the fixtures -> load headless
// (playwright-core: the system Chrome when there is one, else the browser
// Playwright has downloaded) -> structural contract assertions -> per-section
// + full-page pixel baselines (pixelmatch) -> split report smoke test.
// One browser instance, sequential pages (memory-fragile host).
//
// A baseline is a photograph of one UI under one browser build: it has to
// be re-recorded when either changes, and a machine that renders under a
// different build than the one on record will see every dimension differ.

import { existsSync, mkdirSync, readdirSync, readFileSync, writeFileSync } from 'node:fs';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';
import { chromium } from 'playwright-core';
import { resetTmpDir, generateReport } from './lib/gen.mjs';
import { comparePng } from './lib/compare.mjs';

const VISUAL_DIR = dirname(fileURLToPath(import.meta.url));
const REPO_ROOT = resolve(VISUAL_DIR, '../../..');
const BIN = join(REPO_ROOT, 'bin', 'quellog');
const FIXTURE = join(REPO_ROOT, 'test', 'testdata', 'comprehensive', 'stderr.log');
// The fixture spans ~22 minutes, so 10m yields 3 periods.
const SPLIT_INTERVAL = '10m';

// pgBouncer reports. The section is rendered by its own module and none
// of the PostgreSQL fixtures reaches it, so without these the pooler
// report has no visual contract at all. Three captures, chosen for the
// three shapes the section takes and no more (each one is a page load):
//   nominal      every block filled -- cards, three charts, tabbed
//                breakdowns, session histogram and error table;
//   placeholders log_connections / log_disconnections off, so the two
//                empty blocks fall back to their "which setting" notes
//                and the error table takes the closing row on its own;
//   one-snapshot a single "stats:" line -- the coverage caveat, and the
//                charts replaced by the reason they are not drawn.
const PGB_DIR = join(REPO_ROOT, 'test', 'testdata', 'regressions', 'pgbouncer');
const PGB_REPORTS = [
  { name: 'pgb-nominal', fixture: join(PGB_DIR, 'pgbouncer.log') },
  { name: 'pgb-placeholders', fixture: join(PGB_DIR, 'pgbouncer_no_conn_log.log') },
  { name: 'pgb-one-snapshot', fixture: join(PGB_DIR, 'pgbouncer_oldversion.log'), charts: false },
];
const BASELINES_DIR = join(VISUAL_DIR, 'baselines');
const STRUCTURE_PATH = join(VISUAL_DIR, 'expected-structure.json');

const PIXEL_THRESHOLD = 0.1; // pixelmatch per-pixel color threshold
const MAX_DIFF_RATIO = 0.005; // fail if > 0.5% of pixels differ
const RENDER_POLL_MS = 250;
const RENDER_SETTLE_MS = 300;
const RENDER_DEADLINE_MS = 30000;

const updateBaselines = process.argv.includes('--update-baselines');
const failures = [];
const warnings = [];
// Every screenshot name taken this run, across all reports.
const recorded = new Set();

function fail(msg) {
  failures.push(msg);
  console.error(`  FAIL: ${msg}`);
}

function warn(msg) {
  warnings.push(msg);
  console.warn(`  warn: ${msg}`);
}

/** Attach console-error / pageerror collectors to a page. */
function collectErrors(page) {
  const errors = [];
  page.on('console', (msg) => {
    if (msg.type() === 'error') errors.push(`console error: ${msg.text()}`);
  });
  page.on('pageerror', (err) => {
    errors.push(`page error: ${err.message}`);
  });
  return errors;
}

/**
 * Wait for render completion: poll until the `.uplot canvas` count is stable
 * (same non-zero value) across two consecutive 250ms polls, then settle 300ms.
 * `allowZero` accepts a stable zero as well, for a report that legitimately
 * draws no chart — otherwise it would burn the whole deadline before saying so.
 * @returns {number} final canvas count
 */
async function waitForRender(page, allowZero = false) {
  const deadline = Date.now() + RENDER_DEADLINE_MS;
  let prev = -1;
  let stablePolls = 0;
  while (Date.now() < deadline) {
    const count = await page.locator('.uplot canvas').count();
    if (count === prev && (count > 0 || allowZero)) {
      stablePolls++;
      if (stablePolls >= 2) break;
    } else {
      stablePolls = 0;
    }
    prev = count;
    await page.waitForTimeout(RENDER_POLL_MS);
  }
  await page.waitForTimeout(RENDER_SETTLE_MS);
  return page.locator('.uplot canvas').count();
}

/**
 * Neutralize the only run-varying rendered value: `.summary-parsetime` shows
 * the embedded parse_time_ms, which changes on every report generation.
 */
async function normalizeVolatileText(page) {
  await page.evaluate(() => {
    document.querySelectorAll('.summary-parsetime').forEach((el) => {
      el.textContent = 'parsed in 0ms';
    });
  });
}

/** Discover the rendered structure of the report (sections, canvases, cards). */
async function discoverStructure(page) {
  return page.evaluate(() => {
    const visible = (el) => !!(el.offsetWidth || el.offsetHeight || el.getClientRects().length);
    const sections = [...document.querySelectorAll('.section[id]')]
      .filter(visible)
      .map((el) => el.id);
    return {
      sections,
      canvasCount: document.querySelectorAll('.uplot canvas').length,
      statCards: document.querySelectorAll('.stat-card').length,
      tables: document.querySelectorAll('table').length,
      cards: {
        summary: sections.includes('summary'),
        sql: sections.includes('sql_performance'),
        checkpoints: sections.includes('checkpoints'),
        connections: sections.includes('connections'),
        tempfiles: sections.includes('temp_files'),
        events: sections.includes('events'),
      },
    };
  });
}

/** Assert the discovered structure against the committed contract. */
function checkStructure(actual, expected, errors) {
  for (const e of errors) fail(e);
  for (const id of expected.sections) {
    if (!actual.sections.includes(id)) fail(`missing section: #${id}`);
  }
  for (const id of actual.sections) {
    if (!expected.sections.includes(id)) warn(`new section not in contract: #${id} (update baselines to adopt)`);
  }
  if (actual.canvasCount < expected.canvasCount) {
    fail(`canvas count dropped: expected >= ${expected.canvasCount}, got ${actual.canvasCount}`);
  } else if (actual.canvasCount > expected.canvasCount) {
    warn(`canvas count grew: ${expected.canvasCount} -> ${actual.canvasCount} (update baselines to adopt)`);
  }
}

/**
 * Screenshot the full page and each visible top-level section. `prefix`
 * namespaces the files so several reports can share one baseline directory;
 * it is empty for the main report, whose baselines predate the others.
 */
async function takeScreenshots(page, sections, outDir, prefix = '') {
  const shots = [];
  const fullPath = join(outDir, `${prefix}full-page.png`);
  await page.screenshot({ path: fullPath, fullPage: true });
  shots.push({ name: `${prefix}full-page.png`, path: fullPath });
  for (const id of sections) {
    const name = `${prefix}section-${id}.png`;
    const path = join(outDir, name);
    await page.locator(`.section[id="${id}"]`).screenshot({ path });
    shots.push({ name, path });
  }
  return shots;
}

/**
 * Load one report, check its structure against its own contract, then
 * compare (or record) its screenshots.
 * @param {{label: string, path: string, structurePath: string,
 *          prefix?: string, charts?: boolean}} report
 */
async function testReport(context, report) {
  const prefix = report.prefix || '';
  const before = failures.length;
  console.log(`\n== ${report.label} ==`);
  const page = await context.newPage();
  const errors = collectErrors(page);
  await page.goto(pathToFileURL(report.path).href, { waitUntil: 'load' });
  const canvasCount = await waitForRender(page, report.charts === false);
  console.log(`  rendered: ${canvasCount} uPlot canvases`);

  await normalizeVolatileText(page);
  const structure = await discoverStructure(page);
  console.log(`  sections: ${structure.sections.join(', ')}`);

  if (updateBaselines || !existsSync(report.structurePath)) {
    // Never bake a contract from an erroring page -- this page: an earlier
    // report's failure is not a reason to leave this one without a contract.
    for (const e of errors) fail(e);
    if (errors.length === 0) {
      writeFileSync(report.structurePath, JSON.stringify(structure, null, 2) + '\n');
      console.log(`  wrote structure contract: ${report.structurePath}`);
    }
  } else {
    const expected = JSON.parse(readFileSync(report.structurePath, 'utf8'));
    checkStructure(structure, expected, errors);
  }

  if (failures.length > before) {
    await page.close();
    return; // structural failure: pixel comparison would only add noise
  }

  if (updateBaselines) {
    mkdirSync(BASELINES_DIR, { recursive: true });
    const shots = await takeScreenshots(page, structure.sections, BASELINES_DIR, prefix);
    shots.forEach((s) => recorded.add(s.name));
    console.log(`  wrote ${shots.length} baseline PNGs to ${BASELINES_DIR}`);
  } else {
    const tmpDir = dirname(report.path);
    const diffDir = join(tmpDir, 'diff');
    const shots = await takeScreenshots(page, structure.sections, tmpDir, prefix);
    shots.forEach((s) => recorded.add(s.name));
    for (const shot of shots) {
      const baselinePath = join(BASELINES_DIR, shot.name);
      if (!existsSync(baselinePath)) {
        fail(`missing baseline ${shot.name} — run: node web/tests/visual/run.mjs --update-baselines`);
        continue;
      }
      const diffPath = join(diffDir, shot.name);
      const res = comparePng(baselinePath, shot.path, diffPath, {
        threshold: PIXEL_THRESHOLD,
        maxDiffRatio: MAX_DIFF_RATIO,
      });
      if (res.pass) {
        console.log(`  ok: ${shot.name} (${res.diffPixels} px diff)`);
      } else {
        fail(`pixel mismatch ${shot.name}: ${res.reason} — diff: ${diffPath}`);
      }
    }
  }
  await page.close();
}

/**
 * Flag orphaned baselines so they get pruned deliberately. Run once, over
 * the screenshots every report produced — a per-report sweep would call
 * each report's baselines stale on behalf of all the others.
 */
function checkStaleBaselines() {
  if (!existsSync(BASELINES_DIR)) return;
  for (const f of readdirSync(BASELINES_DIR)) {
    if (f.endsWith('.png') && !recorded.has(f)) {
      warn(`stale baseline with no current screenshot: ${f}`);
    }
  }
}

async function testSplitReport(context, reportPath) {
  console.log('\n== Split report (smoke) ==');
  const page = await context.newPage();
  const errors = collectErrors(page);
  await page.goto(pathToFileURL(reportPath).href, { waitUntil: 'load' });
  const canvasCount = await waitForRender(page);

  for (const e of errors) fail(e);
  const hasPeriodNav = (await page.locator('.summary-periods').count()) > 0;
  if (!hasPeriodNav) {
    fail('split report: period selector (.summary-periods) not found');
  } else {
    console.log('  period selector present');
  }
  if (canvasCount === 0) {
    fail('split report: first period rendered no charts');
  } else {
    console.log(`  first period rendered ${canvasCount} canvases`);
  }
  await page.close();
}

/**
 * Launch headless: the system Chrome when one is installed, else whatever
 * browser Playwright has downloaded. Chrome stays the first choice so a
 * machine that has it keeps rendering exactly as before; the fallback is
 * what makes the harness runnable on a machine that does not.
 */
async function launchBrowser() {
  const opts = { headless: true, args: ['--force-color-profile=srgb'] };
  try {
    const browser = await chromium.launch({ channel: 'chrome', ...opts });
    console.log('browser: system Chrome');
    return browser;
  } catch (err) {
    console.log(`browser: bundled Chromium (system Chrome unavailable: ${String(err.message).split('\n')[0]})`);
    return chromium.launch(opts);
  }
}

async function main() {
  const t0 = Date.now();
  console.log(`mode: ${updateBaselines ? 'update baselines' : 'check against baselines'}`);

  // 1. Always regenerate reports fresh (never reuse a stale HTML).
  const { tmpDir } = resetTmpDir(VISUAL_DIR);
  const mainReport = generateReport(BIN, FIXTURE, tmpDir, 'report.html');
  const splitReport = generateReport(BIN, FIXTURE, tmpDir, 'report-split.html', ['--split', SPLIT_INTERVAL]);
  const reports = [{
    label: 'Main report',
    path: mainReport,
    structurePath: STRUCTURE_PATH,
  }];
  for (const r of PGB_REPORTS) {
    reports.push({
      label: `pgBouncer report (${r.name})`,
      path: generateReport(BIN, r.fixture, tmpDir, `${r.name}.html`),
      structurePath: join(VISUAL_DIR, `expected-structure.${r.name}.json`),
      prefix: `${r.name}-`,
      charts: r.charts,
    });
  }
  console.log(`reports regenerated in ${tmpDir}`);

  // 2. One browser instance for everything; sequential pages.
  const browser = await launchBrowser();
  try {
    const context = await browser.newContext({
      viewport: { width: 1440, height: 900 },
      deviceScaleFactor: 1,
    });
    for (const report of reports) {
      await testReport(context, report);
    }
    await testSplitReport(context, splitReport);
    await context.close();
  } finally {
    await browser.close();
  }
  if (!updateBaselines) checkStaleBaselines();

  const secs = ((Date.now() - t0) / 1000).toFixed(1);
  console.log('');
  if (failures.length > 0) {
    console.error(`visual harness FAILED in ${secs}s — ${failures.length} failure(s):`);
    for (const f of failures) console.error(`  - ${f}`);
    process.exit(1);
  }
  const suffix = warnings.length > 0 ? ` (${warnings.length} warning(s))` : '';
  console.log(`visual harness OK in ${secs}s${suffix}`);
}

main().catch((err) => {
  console.error(`visual harness crashed: ${err.stack || err}`);
  process.exit(1);
});
