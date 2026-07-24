package qbittorrent

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestClient_Login(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v2/auth/login" {
			t.Errorf("expected /api/v2/auth/login, got %s", r.URL.Path)
		}
		if r.Method != "POST" {
			t.Errorf("expected POST, got %s", r.Method)
		}

		body, _ := io.ReadAll(r.Body)
		if string(body) == "password=adminadmin&username=admin" {
			w.Header().Set("Set-Cookie", "SID=12345; Path=/; HttpOnly")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte("Ok."))
		} else {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte("Fails."))
		}
	}))
	defer ts.Close()

	client := NewClient(ts.URL, "admin", "adminadmin", 5*time.Second)
	err := client.Login(context.Background())
	if err != nil {
		t.Fatalf("expected login to succeed, got %v", err)
	}
	if !client.authenticated {
		t.Errorf("expected client to be authenticated")
	}

	clientFail := NewClient(ts.URL, "wrong", "wrong", 5*time.Second)
	err = clientFail.Login(context.Background())
	if err == nil {
		t.Fatalf("expected login to fail")
	}
}

func TestClient_AddMagnet(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v2/torrents/add" {
			t.Errorf("expected /api/v2/torrents/add, got %s", r.URL.Path)
		}
		if r.Method != "POST" {
			t.Errorf("expected POST, got %s", r.Method)
		}

		r.ParseForm()
		if r.FormValue("urls") != "magnet:?xt=urn:btih:1234" {
			t.Errorf("expected url to be magnet:?xt=urn:btih:1234, got %s", r.FormValue("urls"))
		}
		if r.FormValue("savepath") != "/downloads" {
			t.Errorf("expected savepath to be /downloads, got %s", r.FormValue("savepath"))
		}
		if r.FormValue("category") != "test" {
			t.Errorf("expected category to be test, got %s", r.FormValue("category"))
		}
		if r.FormValue("tags") != "tag1,tag2" {
			t.Errorf("expected tags to be tag1,tag2, got %s", r.FormValue("tags"))
		}
		if r.FormValue("stopped") != "true" {
			t.Errorf("expected stopped to be true, got %s", r.FormValue("stopped"))
		}

		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	client := NewClient(ts.URL, "admin", "adminadmin", 5*time.Second)
	client.authenticated = true

	err := client.AddMagnet(context.Background(), "magnet:?xt=urn:btih:1234", "/downloads", "test", []string{"tag1", "tag2"}, true)
	if err != nil {
		t.Fatalf("expected add magnet to succeed, got %v", err)
	}
}

func TestClient_GetTorrentInfo(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v2/torrents/info" {
			t.Errorf("expected /api/v2/torrents/info, got %s", r.URL.Path)
		}
		hash := r.URL.Query().Get("hashes")
		if hash != "1234" {
			t.Errorf("expected hashes to be 1234, got %s", hash)
		}

		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`[{"hash":"1234","name":"ubuntu.iso","state":"downloading","progress":0.5,"eta":3600}]`))
	}))
	defer ts.Close()

	client := NewClient(ts.URL, "admin", "adminadmin", 5*time.Second)
	client.authenticated = true

	info, err := client.GetTorrentInfo(context.Background(), "1234")
	if err != nil {
		t.Fatalf("expected to get torrent info, got %v", err)
	}

	if info.Hash != "1234" || info.Name != "ubuntu.iso" || info.State != "downloading" || info.Progress != 0.5 || info.ETA != 3600 {
		t.Errorf("parsed info incorrect: %+v", info)
	}
}

func TestClient_GetTorrentFiles(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v2/torrents/files" {
			t.Errorf("expected /api/v2/torrents/files, got %s", r.URL.Path)
		}
		hash := r.URL.Query().Get("hash")
		if hash != "1234" {
			t.Errorf("expected hash to be 1234, got %s", hash)
		}

		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`[{"id":0,"name":"file1.txt","size":100,"progress":0.5,"priority":1,"is_seed":false}]`))
	}))
	defer ts.Close()

	client := NewClient(ts.URL, "admin", "adminadmin", 5*time.Second)
	client.authenticated = true

	files, err := client.GetTorrentFiles(context.Background(), "1234")
	if err != nil {
		t.Fatalf("expected to get torrent files, got %v", err)
	}

	if len(files) != 1 || files[0].Index != 0 || files[0].Name != "file1.txt" || files[0].Size != 100 || files[0].Priority != 1 {
		t.Errorf("parsed files incorrect: %+v", files)
	}
}

func TestClient_SetFilePriority(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v2/torrents/filePrio" {
			t.Errorf("expected /api/v2/torrents/filePrio, got %s", r.URL.Path)
		}
		if r.Method != "POST" {
			t.Errorf("expected POST, got %s", r.Method)
		}

		r.ParseForm()
		if r.FormValue("hash") != "1234" {
			t.Errorf("expected hash to be 1234, got %s", r.FormValue("hash"))
		}
		if r.FormValue("id") != "0|1" {
			t.Errorf("expected id to be 0|1, got %s", r.FormValue("id"))
		}
		if r.FormValue("priority") != "1" {
			t.Errorf("expected priority to be 1, got %s", r.FormValue("priority"))
		}

		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	client := NewClient(ts.URL, "admin", "adminadmin", 5*time.Second)
	client.authenticated = true

	err := client.SetFilePriority(context.Background(), "1234", []int{0, 1}, 1)
	if err != nil {
		t.Fatalf("expected set file priority to succeed, got %v", err)
	}
}

func TestClient_403_Reauth(t *testing.T) {
	authCount := 0
	requestCount := 0

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v2/auth/login" {
			authCount++
			w.WriteHeader(http.StatusOK)
			w.Write([]byte("Ok."))
			return
		}

		requestCount++
		if requestCount == 1 {
			w.WriteHeader(http.StatusForbidden)
			return
		}

		if r.URL.Path == "/api/v2/app/version" {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte("4.3.9"))
			return
		}
	}))
	defer ts.Close()

	client := NewClient(ts.URL, "admin", "adminadmin", 5*time.Second)
	client.authenticated = true

	ver, err := client.GetVersion(context.Background())
	if err != nil {
		t.Fatalf("expected success, got err %v", err)
	}
	if ver != "4.3.9" {
		t.Errorf("expected version 4.3.9, got %s", ver)
	}

	if authCount != 1 {
		t.Errorf("expected 1 auth call due to 403, got %d", authCount)
	}
	if requestCount != 2 {
		t.Errorf("expected 2 request calls (1st failed, 2nd success), got %d", requestCount)
	}
}

func TestClient_ValidateCompatibility(t *testing.T) {
	tests := []struct {
		name        string
		version     string
		apiVersion  string
		authFail    bool
		expectError bool
	}{
		{
			name:        "4.x version rejected",
			version:     "v4.6.3",
			apiVersion:  "2.9.3",
			expectError: true,
		},
		{
			name:        "5.0.0 version accepted",
			version:     "v5.0.0",
			apiVersion:  "2.9.3",
			expectError: false,
		},
		{
			name:        "5.x version accepted",
			version:     "5.1.2",
			apiVersion:  "2.10.0",
			expectError: false,
		},
		{
			name:        "Malformed version rejected",
			version:     "invalid_ver",
			apiVersion:  "2.9.3",
			expectError: true,
		},
		{
			name:        "API version retrieval failure rejected",
			version:     "5.0.0",
			apiVersion:  "",
			expectError: true,
		},
		{
			name:        "Authentication failure rejected",
			authFail:    true,
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if tt.authFail {
					w.WriteHeader(http.StatusForbidden)
					return
				}
				if r.URL.Path == "/api/v2/auth/login" {
					w.WriteHeader(http.StatusOK)
					w.Write([]byte("Ok."))
					return
				}
				if r.URL.Path == "/api/v2/app/version" {
					w.WriteHeader(http.StatusOK)
					w.Write([]byte(tt.version))
					return
				}
				if r.URL.Path == "/api/v2/app/webapiVersion" {
					if tt.apiVersion == "" {
						w.WriteHeader(http.StatusInternalServerError)
						return
					}
					w.WriteHeader(http.StatusOK)
					w.Write([]byte(tt.apiVersion))
					return
				}
			}))
			defer ts.Close()

			client := NewClient(ts.URL, "admin", "adminadmin", 5*time.Second)
			err := client.ValidateCompatibility(context.Background())

			if tt.expectError && err == nil {
				t.Errorf("expected error, got nil")
			}
			if !tt.expectError && err != nil {
				t.Errorf("expected no error, got %v", err)
			}
		})
	}
}
