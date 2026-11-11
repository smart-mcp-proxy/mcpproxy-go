package scanner

import (
	"context"
	"reflect"
	"testing"
	"time"
)

func TestAggregateReportsPreservesScanTimestamp(t *testing.T) {
	old := time.Date(2026, 11, 1, 8, 0, 0, 0, time.UTC)
	newer := old.Add(time.Hour)
	reports := []*ScanReport{
		{ID: "pass2", ScannedAt: newer},
		{ID: "pass1", ScannedAt: old},
	}
	first := AggregateReports("job", "server", reports)
	second := AggregateReports("job", "server", reports)
	if !first.ScannedAt.Equal(newer) || !reflect.DeepEqual(first, second) {
		t.Fatalf("re-reading must preserve the latest stored scan time: first=%v second=%v want=%v", first.ScannedAt, second.ScannedAt, newer)
	}
	if got := AggregateReports("job", "server", nil).ScannedAt; !got.IsZero() {
		t.Fatalf("missing scan times must not be replaced with read time: %v", got)
	}
}

func TestServiceScanTimestampIncludesCompletedCompanion(t *testing.T) {
	for _, legacyReport := range []bool{false, true} {
		t.Run(map[bool]string{false: "no pass2 report", true: "legacy undated pass2 report"}[legacyReport], func(t *testing.T) {
			svc, store, _ := newTestService(t)
			start := time.Date(2026, 11, 1, 8, 0, 0, 0, time.UTC)
			finished := start.Add(10 * time.Minute)
			for _, job := range []*ScanJob{
				{ID: "p1", ServerName: "server", ScanPass: ScanPassSecurityScan, Status: ScanJobStatusCompleted, StartedAt: start, CompletedAt: start.Add(time.Minute)},
				{ID: "p2", ServerName: "server", ScanPass: ScanPassSupplyChainAudit, Status: ScanJobStatusCompleted, StartedAt: start.Add(2 * time.Minute), CompletedAt: finished},
			} {
				if err := store.SaveScanJob(job); err != nil {
					t.Fatal(err)
				}
			}
			if err := store.SaveScanReport(&ScanReport{ID: "r1", JobID: "p1", ServerName: "server", ScannerID: "tpa-descriptions", ScannedAt: start}); err != nil {
				t.Fatal(err)
			}
			if legacyReport {
				if err := store.SaveScanReport(&ScanReport{ID: "r2", JobID: "p2", ServerName: "server", ScannerID: "trivy"}); err != nil {
					t.Fatal(err)
				}
			}
			for _, byID := range []bool{false, true} {
				var report *AggregatedReport
				var err error
				if byID {
					report, err = svc.GetScanReportByJobID(context.Background(), "p1")
				} else {
					report, err = svc.GetScanReport(context.Background(), "server")
				}
				if err != nil {
					t.Fatal(err)
				}
				if !report.Pass2Complete || !report.ScannedAt.Equal(finished) {
					t.Fatalf("byID=%v: timestamp=%v pass2_complete=%v, want %v and true", byID, report.ScannedAt, report.Pass2Complete, finished)
				}
			}
		})
	}
}

func TestAggregateReportsTimestampWithJobStatus(t *testing.T) {
	start := time.Date(2026, 11, 1, 8, 0, 0, 0, time.UTC)
	completed := start.Add(time.Minute)
	for _, tc := range []struct {
		name       string
		reportTime time.Time
		job        *ScanJob
		want       time.Time
	}{
		{"completed job", start, &ScanJob{StartedAt: start, CompletedAt: completed}, completed},
		{"later companion report", completed.Add(time.Hour), &ScanJob{StartedAt: start, CompletedAt: completed}, completed.Add(time.Hour)},
		{"no report timestamp", time.Time{}, &ScanJob{StartedAt: start, CompletedAt: completed}, completed},
		{"running job fallback", time.Time{}, &ScanJob{StartedAt: start}, start},
		{"missing timestamps", time.Time{}, &ScanJob{}, time.Time{}},
		{"no job", completed, nil, completed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := AggregateReportsWithJobStatus("job", "server", []*ScanReport{{ScannedAt: tc.reportTime}}, tc.job)
			if !got.ScannedAt.Equal(tc.want) {
				t.Fatalf("ScannedAt=%v, want persisted time %v", got.ScannedAt, tc.want)
			}
		})
	}
}
