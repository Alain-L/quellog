// pgBouncer report charts. Vanilla JS reading the embedded telemetry
// time-series (#pgb-chart-data) and drawing three uPlot line charts:
// request throughput, network I/O, and query/wait timing. No framework,
// no module system — this script is inlined into the standalone report
// alongside the uPlot UMD global.
(function () {
  "use strict";

  var el = document.getElementById("pgb-chart-data");
  if (!el || typeof uPlot === "undefined") return;

  var d;
  try {
    d = JSON.parse(el.textContent || "{}");
  } catch (e) {
    return;
  }
  var t = d.t || [];
  if (t.length === 0) return;

  function cssVar(name, fallback) {
    var v = getComputedStyle(document.documentElement).getPropertyValue(name);
    return (v && v.trim()) || fallback;
  }

  var textCol = cssVar("--text-muted", "#888");
  var gridCol = "rgba(128,128,128,0.15)";
  var accent = cssVar("--accent", "#4a90d9");
  var purple = cssVar("--purple", "#9b59b6");
  var danger = cssVar("--danger", "#e06c5e");
  var success = cssVar("--success", "#4a9d6a");

  // Shared axis styling.
  function axes(yfmt) {
    return [
      { stroke: textCol, grid: { stroke: gridCol }, ticks: { stroke: gridCol } },
      {
        stroke: textCol,
        grid: { stroke: gridCol },
        ticks: { stroke: gridCol },
        size: 74,
        values: yfmt ? function (u, splits) { return splits.map(yfmt); } : undefined,
      },
    ];
  }

  function mk(containerId, series, data, yfmt) {
    var c = document.getElementById(containerId);
    if (!c) return;
    function make() {
      c.innerHTML = "";
      var opts = {
        width: c.clientWidth || 600,
        height: 220,
        scales: { x: { time: true } },
        legend: { show: true },
        cursor: { focus: { prox: 16 } },
        axes: axes(yfmt),
        series: series,
      };
      var u = new uPlot(opts, data, c);
      return u;
    }
    var u = make();
    var ro = new ResizeObserver(function () {
      if (u) u.setSize({ width: c.clientWidth || 600, height: 220 });
    });
    ro.observe(c);
  }

  function siBytes(v) {
    if (v == null) return "";
    var u = ["B", "kB", "MB", "GB"], i = 0;
    while (v >= 1024 && i < u.length - 1) { v /= 1024; i++; }
    return (i === 0 ? v : v.toFixed(1)) + " " + u[i] + "/s";
  }
  function micros(v) {
    if (v == null) return "";
    if (v < 1000) return v.toFixed(0) + " µs";
    if (v < 1e6) return (v / 1000).toFixed(1) + " ms";
    return (v / 1e6).toFixed(2) + " s";
  }

  // Throughput: transactions/s and queries/s.
  mk(
    "chart-throughput",
    [
      {},
      { label: "xacts/s", stroke: accent, width: 2, points: { show: false } },
      { label: "queries/s", stroke: purple, width: 2, points: { show: false } },
    ],
    [t, d.xacts || [], d.queries || []]
  );

  // Network I/O: bytes in/out per second.
  mk(
    "chart-io",
    [
      {},
      { label: "in", stroke: accent, width: 2, points: { show: false }, value: function (u, v) { return siBytes(v); } },
      { label: "out", stroke: success, width: 2, points: { show: false }, value: function (u, v) { return siBytes(v); } },
    ],
    [t, d.in || [], d.out || []],
    siBytes
  );

  // Timing: xact / query / wait, microseconds.
  mk(
    "chart-timing",
    [
      {},
      { label: "xact", stroke: accent, width: 2, points: { show: false }, value: function (u, v) { return micros(v); } },
      { label: "query", stroke: purple, width: 2, points: { show: false }, value: function (u, v) { return micros(v); } },
      { label: "wait", stroke: danger, width: 2, points: { show: false }, value: function (u, v) { return micros(v); } },
    ],
    [t, d.xact_us || [], d.query_us || [], d.wait_us || []],
    micros
  );
})();
