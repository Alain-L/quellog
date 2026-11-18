package output

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Alain-L/quellog/analysis"
)

// TestExportJSONStringWithMeta_PgBackRest pins the WASM demo's payload:
// "meta" and "pgbackrest" together when pbr is non-empty, no
// "pgbackrest" key when it is empty, as for the CLI.
func TestExportJSONStringWithMeta_PgBackRest(t *testing.T) {
	meta := &MetaInfo{Format: "stderr", Entries: 3}
	got, err := ExportJSONStringWithMeta(analysis.AggregatedMetrics{}, []analysis.PgBackRestMetrics{{Stanza: "demo", LinesRead: 5}}, []string{"all"}, true, meta, true)
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Meta       map[string]any   `json:"meta"`
		PgBackRest []map[string]any `json:"pgbackrest"`
	}
	if err := json.Unmarshal([]byte(got), &doc); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, got)
	}
	if doc.Meta["format"] != "stderr" || len(doc.PgBackRest) != 1 || doc.PgBackRest[0]["stanza"] != "demo" {
		t.Errorf("meta = %v, pgbackrest = %v; want format stderr and the demo stanza", doc.Meta, doc.PgBackRest)
	}

	got, err = ExportJSONStringWithMeta(analysis.AggregatedMetrics{}, nil, []string{"all"}, true, meta, true)
	if err != nil || strings.Contains(got, `"pgbackrest"`) || !strings.Contains(got, `"meta"`) {
		t.Errorf("empty pbr: err %v, got %s; want meta and no pgbackrest key", err, got)
	}
}

// TestBuildPgBackRestJSON_Keys pins the keys the outputs read: archive
// logged flags, the --begin/--end window, a manual expire's --set label,
// a backup's size in bytes, a crashed backup's signal and the dropped WAL.
func TestBuildPgBackRestJSON_Keys(t *testing.T) {
	at := time.Date(2026, 10, 10, 13, 35, 24, 0, time.UTC)
	m := analysis.PgBackRestMetrics{
		Stanza: "main",
		Window: &analysis.PgBackRestWindow{Begin: at, CutAfter: true},
		Backups: []analysis.PgBackRestBackup{
			{Type: "full", SizeRaw: "29.4MB", SizeBytes: 30828134, HasSize: true},
			{Crashed: true, Signal: "SIGTERM"},
		},
		ArchivePush: analysis.PgBackRestArchiveStats{
			Logged: true, AsyncLogged: true,
			DroppedEvents: []analysis.PgBackRestDropEvent{{Segment: "000000010000000000000013", Time: at}},
		},
		ExpireRuns: []analysis.PgBackRestExpireRun{{Time: at, SetLabel: "20261010-134255F", Succeeded: true}},
	}
	b, err := json.Marshal(buildPgBackRestJSON(m))
	if err != nil {
		t.Fatal(err)
	}
	got := string(b)
	for _, want := range []string{
		`"window":{"begin":"2026-10-10 13:35:24","cut_before":false,"cut_after":true}`,
		`"size_bytes":30828134`,
		`"signal":"SIGTERM"`,
		`"archive_push":{"logged":true,"async_logged":true,`,
		`"archive_get":{"logged":false,"async_logged":false,`,
		`"dropped_wal":[{"segment":"000000010000000000000013","date":"2026-10-10 13:35:24"}]`,
		`"set_label":"20261010-134255F"`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %s in %s", want, got)
		}
	}
}
