package observability

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestClearLogsPreservesOtherObservationData(t *testing.T) {
	hub, err := NewHub(HubOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 4; i++ {
		hub.recordLog(LogEntry{Timestamp: time.Now().UTC(), Message: fmt.Sprint("entry-", i)})
	}
	hub.RecordTrace(TraceEntry{TraceID: "trace-one", Name: "request", Attributes: map[string]string{"url.path": "/api/example"}})
	hub.alerts["custom"] = Alert{ID: "custom", Title: "Alert remains", Status: "firing", LastSeen: time.Now()}
	hub.lastWriteError = "trace storage error"
	n, err := hub.ClearLogs()
	if err != nil || n != 4 {
		t.Fatalf("clear=%d err=%v", n, err)
	}
	if hub.QueryLogs(LogQuery{}).Total != 0 || hub.QueryTraces(TraceQuery{}).Total != 1 || len(hub.alerts) != 1 || hub.lastWriteError != "trace storage error" {
		t.Fatal("cleanup changed unrelated data")
	}
	if n, err = hub.ClearLogs(); err != nil || n != 0 {
		t.Fatal("empty cleanup is not idempotent")
	}
	hub.recordLog(LogEntry{Timestamp: time.Now().UTC(), Message: "new entry"})
	if hub.QueryLogs(LogQuery{}).Total != 1 {
		t.Fatal("new logs are not recorded")
	}
}

func TestClearLogsRemovesAllRotationsAndStaysEmptyAfterRestart(t *testing.T) {
	options := HubOptions{Directory: t.TempDir(), MaxFileBytes: 1}
	hub, err := NewHub(options)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 6; i++ {
		hub.recordLog(LogEntry{Timestamp: time.Now().UTC(), Message: fmt.Sprint("old-", i)})
	}
	hub.RecordTrace(TraceEntry{TraceID: "keep-trace", StartedAt: time.Now().UTC(), Name: "request", Attributes: map[string]string{"url.path": "/api/example"}})
	for _, suffix := range []string{".1", ".2"} {
		info, err := os.Stat(filepath.Join(options.Directory, "logs.jsonl"+suffix))
		if err != nil || info.Size() == 0 {
			t.Fatal("test did not produce rotated logs")
		}
	}
	n, err := hub.ClearLogs()
	if err != nil || n != 6 {
		t.Fatalf("clear=%d err=%v", n, err)
	}
	for _, suffix := range []string{".1", ".2"} {
		if _, err = os.Stat(filepath.Join(options.Directory, "logs.jsonl"+suffix)); !os.IsNotExist(err) {
			t.Fatalf("archive survived: %s", suffix)
		}
	}
	info, err := os.Stat(filepath.Join(options.Directory, "logs.jsonl"))
	if err != nil || info.Size() != 0 || info.Mode().Perm() != 0o600 {
		t.Fatal("active journal is not empty/private")
	}
	entries, _ := os.ReadDir(options.Directory)
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".application-log-cleanup-") {
			t.Fatal("cleanup left journal staging files")
		}
	}
	if err = hub.Close(); err != nil {
		t.Fatal(err)
	}
	restarted, err := NewHub(options)
	if err != nil {
		t.Fatal(err)
	}
	if restarted.QueryLogs(LogQuery{}).Total != 0 || restarted.QueryTraces(TraceQuery{}).Total != 1 {
		t.Fatal("old logs returned or traces disappeared after restart")
	}
	restarted.recordLog(LogEntry{Timestamp: time.Now().UTC(), Message: "after cleanup"})
	if err = restarted.Close(); err != nil {
		t.Fatal(err)
	}
	final, err := NewHub(options)
	if err != nil {
		t.Fatal(err)
	}
	defer final.Close()
	logs := final.QueryLogs(LogQuery{})
	if logs.Total != 1 || logs.Items[0].Message != "after cleanup" {
		t.Fatal("new logs were not persisted")
	}
}

func TestClearLogsRejectsUnexpectedArchiveBeforeDeletingAnything(t *testing.T) {
	hub, err := NewHub(HubOptions{Directory: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer hub.Close()
	hub.recordLog(LogEntry{Timestamp: time.Now().UTC(), Message: "retain on failure"})
	original, _ := os.ReadFile(hub.logJournal.path)
	if err = os.Mkdir(hub.logJournal.path+".1", 0o700); err != nil {
		t.Fatal(err)
	}
	if n, err := hub.ClearLogs(); err == nil || n != 0 {
		t.Fatal("invalid archive was not rejected")
	}
	current, _ := os.ReadFile(hub.logJournal.path)
	if string(current) != string(original) || hub.QueryLogs(LogQuery{}).Total != 1 {
		t.Fatal("failed validation discarded logs")
	}
	if err = os.Remove(hub.logJournal.path + ".1"); err != nil {
		t.Fatal(err)
	}
	if _, err = hub.ClearLogs(); err != nil || hub.lastWriteError != "" {
		t.Fatal("cleanup cannot recover from a previous cleanup failure")
	}
}

func TestClearLogsDoesNotFollowSymlinksOrTruncateHardlinks(t *testing.T) {
	for _, kind := range []string{"symlink", "hardlink"} {
		t.Run(kind, func(t *testing.T) {
			root := t.TempDir()
			directory := filepath.Join(root, "observability")
			if err := os.Mkdir(directory, 0o700); err != nil {
				t.Fatal(err)
			}
			outside := filepath.Join(root, "outside.log")
			contents := []byte("{\"level\":\"INFO\",\"msg\":\"external content\"}\n")
			if err := os.WriteFile(outside, contents, 0o600); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(directory, "logs.jsonl")
			var err error
			if kind == "hardlink" {
				err = os.Link(outside, path)
			} else {
				err = os.Symlink(outside, path)
			}
			if err != nil {
				t.Skipf("links unavailable: %v", err)
			}
			hub, err := NewHub(HubOptions{Directory: directory})
			if err != nil {
				t.Fatal(err)
			}
			defer hub.Close()
			_, err = hub.ClearLogs()
			if kind == "symlink" && err == nil {
				t.Fatal("symlink path accepted")
			}
			if kind == "hardlink" && err != nil {
				t.Fatal(err)
			}
			actual, _ := os.ReadFile(outside)
			if string(actual) != string(contents) {
				t.Fatal("external link target was modified")
			}
			if kind == "hardlink" {
				outsideInfo, _ := os.Stat(outside)
				activeInfo, _ := os.Stat(path)
				if os.SameFile(outsideInfo, activeInfo) {
					t.Fatal("active writer still uses external hardlink")
				}
			}
		})
	}
}

func TestClearLogsSerializesWithConcurrentWriters(t *testing.T) {
	hub, err := NewHub(HubOptions{Directory: t.TempDir(), MaxFileBytes: 4096})
	if err != nil {
		t.Fatal(err)
	}
	defer hub.Close()
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 80; j++ {
				hub.recordLog(LogEntry{Timestamp: time.Now().UTC(), Message: "concurrent"})
				hub.QueryLogs(LogQuery{})
			}
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 12; i++ {
			if _, err := hub.ClearLogs(); err != nil {
				t.Errorf("cleanup: %v", err)
			}
		}
	}()
	wg.Wait()
	if _, err = hub.ClearLogs(); err != nil {
		t.Fatal(err)
	}
	hub.recordLog(LogEntry{Timestamp: time.Now().UTC(), Message: "final log"})
	logs := hub.QueryLogs(LogQuery{})
	if logs.Total != 1 || logs.Items[0].Message != "final log" {
		t.Fatal("writer did not recover after concurrent cleanup")
	}
}
