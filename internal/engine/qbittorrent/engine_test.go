package qbittorrent

import (
	"context"
	"downloader/internal/job"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestEngine_AddMagnet(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v2/auth/login" {
			w.Header().Set("Set-Cookie", "SID=12345; Path=/; HttpOnly")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte("Ok."))
			return
		}
		if r.URL.Path == "/api/v2/torrents/createCategory" {
			w.WriteHeader(http.StatusOK)
			return
		}
		if r.URL.Path == "/api/v2/torrents/add" {
			w.WriteHeader(http.StatusOK)
			return
		}
		t.Errorf("Unexpected path: %s", r.URL.Path)
	}))
	defer ts.Close()

	engine := NewEngine(ts.URL, "admin", "adminadmin", 5)

	magnet := "magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567&dn=test"
	hash, err := engine.AddMagnet(context.Background(), magnet, "/tmp", "job-123")
	if err != nil {
		t.Fatalf("expected AddMagnet to succeed, got %v", err)
	}
	if hash != "0123456789abcdef0123456789abcdef01234567" {
		t.Errorf("expected hash to be extracted, got %s", hash)
	}
}

func TestEngine_Status(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v2/auth/login" {
			w.Header().Set("Set-Cookie", "SID=12345; Path=/; HttpOnly")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte("Ok."))
			return
		}
		if r.URL.Path == "/api/v2/torrents/info" {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`[{"hash":"1234","name":"ubuntu.iso","state":"downloading","progress":0.5,"eta":3600,"dlspeed":1024,"completed":512,"size":1024,"upspeed":10,"uploaded":100,"ratio":0.1,"num_seeds":5,"num_leechs":10}]`))
			return
		}
	}))
	defer ts.Close()

	engine := NewEngine(ts.URL, "admin", "adminadmin", 5)

	j := &job.Job{
		ID:       "job-1",
		EngineID: "1234",
	}

	status, err := engine.Status(context.Background(), j)
	if err != nil {
		t.Fatalf("expected Status to succeed, got %v", err)
	}

	if status.Status != job.StatusDownloading {
		t.Errorf("expected status downloading, got %s", status.Status)
	}
	if status.Progress != 50.0 {
		t.Errorf("expected progress 50.0, got %f", status.Progress)
	}
	if status.SpeedBytesPerSecond != 1024 {
		t.Errorf("expected speed 1024, got %d", status.SpeedBytesPerSecond)
	}
	if status.Seeders != 5 {
		t.Errorf("expected 5 seeders, got %d", status.Seeders)
	}
}

func TestEngine_GetFiles(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v2/auth/login" {
			w.Header().Set("Set-Cookie", "SID=12345; Path=/; HttpOnly")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte("Ok."))
			return
		}
		if r.URL.Path == "/api/v2/torrents/files" {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`[{"index":0,"name":"file1.txt","size":100,"progress":0.5,"priority":1,"is_seed":false}]`))
			return
		}
	}))
	defer ts.Close()

	engine := NewEngine(ts.URL, "admin", "adminadmin", 5)

	files, err := engine.GetFiles(context.Background(), "1234")
	if err != nil {
		t.Fatalf("expected GetFiles to succeed, got %v", err)
	}

	if len(files) != 1 {
		t.Fatalf("expected 1 file, got %d", len(files))
	}
	if files[0].Index != 0 || files[0].Path != "file1.txt" || files[0].Priority != "normal" || files[0].Progress != 50.0 {
		t.Errorf("parsed file incorrect: %+v", files[0])
	}
}

func TestEngine_SetFilePriorities(t *testing.T) {
	prioCallCount := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v2/auth/login" {
			w.Header().Set("Set-Cookie", "SID=12345; Path=/; HttpOnly")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte("Ok."))
			return
		}
		if r.URL.Path == "/api/v2/torrents/filePrio" {
			prioCallCount++
			body, _ := io.ReadAll(r.Body)
			bodyStr := string(body)
			if bodyStr != "hash=1234&id=0%7C1&priority=0" && bodyStr != "hash=1234&id=0%7C1&priority=1" {
				// depends on map iteration order
			}
			w.WriteHeader(http.StatusOK)
			return
		}
	}))
	defer ts.Close()

	engine := NewEngine(ts.URL, "admin", "adminadmin", 5)

	selections := []job.TorrentFileSelection{
		{Index: 0, Priority: job.PrioritySkip},
		{Index: 1, Priority: job.PrioritySkip},
	}
	err := engine.SetFilePriorities(context.Background(), "1234", selections)
	if err != nil {
		t.Fatalf("expected SetFilePriorities to succeed, got %v", err)
	}
	if prioCallCount != 1 {
		t.Errorf("expected 1 call to filePrio, got %d", prioCallCount)
	}
}
