//go:build integration

package ytdlp

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"downloader/internal/job"
)

func getLiveTestEnv(t *testing.T) (string, string, string, string) {
	ytdlpPath := os.Getenv("YTDLP_PATH")
	ffmpegPath := os.Getenv("FFMPEG_PATH")
	ffprobePath := os.Getenv("FFPROBE_PATH")
	testURL := os.Getenv("YTDLP_TEST_URL")

	if ytdlpPath == "" || ffmpegPath == "" || ffprobePath == "" || testURL == "" {
		t.Skip("skipping integration test: YTDLP_PATH, FFMPEG_PATH, FFPROBE_PATH, and YTDLP_TEST_URL must all be set")
	}

	if _, err := os.Stat(ytdlpPath); err != nil {
		t.Skipf("skipping integration test: ytdlp binary not found at %s", ytdlpPath)
	}
	if _, err := os.Stat(ffmpegPath); err != nil {
		t.Skipf("skipping integration test: ffmpeg binary not found at %s", ffmpegPath)
	}
	if _, err := os.Stat(ffprobePath); err != nil {
		t.Skipf("skipping integration test: ffprobe binary not found at %s", ffprobePath)
	}

	return ytdlpPath, ffmpegPath, ffprobePath, testURL
}

func TestLiveYTDLP_VideoOnlyMerge(t *testing.T) {
	ytdlpPath, ffmpegPath, ffprobePath, testURL := getLiveTestEnv(t)

	eng := NewEngine(ytdlpPath, ffmpegPath)
	tmpDir := t.TempDir()

	j := &job.Job{
		ID:     "live_test_job_video_only",
		Source: testURL,
		Type:   job.TypeMedia,
		MediaInfo: &job.MediaInfo{
			SelectedFmt: "18",
			Formats: []job.MediaFormat{
				{FormatID: "18", VCodec: "avc1.42001E", ACodec: "none"},
			},
		},
	}

	engineID, err := eng.Start(context.Background(), j, tmpDir)
	if err != nil {
		t.Fatalf("failed to start live ytdlp: %v", err)
	}
	t.Logf("started live ytdlp download with engine ID %s", engineID)

	start := time.Now()
	var finalStatus *job.EngineStatus
	for time.Since(start) < 2*time.Minute {
		st, err := eng.Status(context.Background(), j)
		if err != nil {
			t.Fatalf("status error: %v", err)
		}
		if st.Status == job.StatusCompleted || st.Status == job.StatusFailed || st.Status == job.StatusCancelled {
			finalStatus = st
			break
		}
		time.Sleep(500 * time.Millisecond)
	}

	if finalStatus == nil {
		t.Fatalf("download timed out")
	}
	if finalStatus.Status != job.StatusCompleted {
		t.Fatalf("expected completed status, got %s (err: %s)", finalStatus.Status, finalStatus.Error)
	}

	t.Logf("completed with OutputPath: %s, TotalBytes: %d", finalStatus.OutputPath, finalStatus.TotalBytes)

	fi, err := os.Stat(finalStatus.OutputPath)
	if err != nil || fi.Size() == 0 {
		t.Fatalf("final output path %s does not exist or is empty: %v", finalStatus.OutputPath, err)
	}

	cmd := exec.Command(ffprobePath,
		"-v", "error",
		"-show_entries", "stream=index,codec_type,codec_name",
		"-of", "default=noprint_wrappers=1",
		finalStatus.OutputPath,
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("ffprobe failed: %v, out: %s", err, string(out))
	}
	t.Logf("ffprobe output:\n%s", string(out))

	if !strings.Contains(string(out), "codec_type=video") || !strings.Contains(string(out), "codec_type=audio") {
		t.Fatalf("expected both video and audio streams in merged file, got:\n%s", string(out))
	}
}

func TestLiveYTDLP_AudioOnly(t *testing.T) {
	ytdlpPath, ffmpegPath, _, testURL := getLiveTestEnv(t)

	eng := NewEngine(ytdlpPath, ffmpegPath)
	tmpDir := t.TempDir()

	j := &job.Job{
		ID:     "live_test_job_audio_only",
		Source: testURL,
		Type:   job.TypeMedia,
		MediaInfo: &job.MediaInfo{
			SelectedFmt: "140",
			Formats: []job.MediaFormat{
				{FormatID: "140", VCodec: "none", ACodec: "mp4a.40.2"},
			},
		},
	}

	_, err := eng.Start(context.Background(), j, tmpDir)
	if err != nil {
		t.Fatalf("failed to start live ytdlp audio: %v", err)
	}

	start := time.Now()
	var finalStatus *job.EngineStatus
	for time.Since(start) < 2*time.Minute {
		st, err := eng.Status(context.Background(), j)
		if err != nil {
			t.Fatalf("status error: %v", err)
		}
		if st.Status == job.StatusCompleted || st.Status == job.StatusFailed || st.Status == job.StatusCancelled {
			finalStatus = st
			break
		}
		time.Sleep(500 * time.Millisecond)
	}

	if finalStatus == nil {
		t.Fatalf("download timed out")
	}
	if finalStatus.Status != job.StatusCompleted {
		t.Fatalf("expected completed status, got %s (err: %s)", finalStatus.Status, finalStatus.Error)
	}

	t.Logf("audio completed with OutputPath: %s, TotalBytes: %d", finalStatus.OutputPath, finalStatus.TotalBytes)

	fi, err := os.Stat(finalStatus.OutputPath)
	if err != nil || fi.Size() == 0 {
		t.Fatalf("final audio output path %s does not exist or is empty: %v", finalStatus.OutputPath, err)
	}
}

func TestLiveYTDLP_ThrottledProgress(t *testing.T) {
	ytdlpPath, ffmpegPath, _, testURL := getLiveTestEnv(t)

	eng := NewEngine(ytdlpPath, ffmpegPath)
	tmpDir := t.TempDir()

	j := &job.Job{
		ID:     "live_test_job_throttled",
		Source: testURL,
		Type:   job.TypeMedia,
		MediaInfo: &job.MediaInfo{
			SelectedFmt: "18",
			Formats: []job.MediaFormat{
				{FormatID: "18", VCodec: "avc1.42001E", ACodec: "none"},
			},
		},
	}

	_, err := eng.Start(context.Background(), j, tmpDir)
	if err != nil {
		t.Fatalf("failed to start throttled download: %v", err)
	}

	var observedPercents []float64

	start := time.Now()
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()

	var finalStatus *job.EngineStatus
	for time.Since(start) < 2*time.Minute {
		<-ticker.C
		st, err := eng.Status(context.Background(), j)
		if err != nil {
			continue
		}

		if st.Progress > 0 {
			if len(observedPercents) == 0 || observedPercents[len(observedPercents)-1] != st.Progress {
				observedPercents = append(observedPercents, st.Progress)
			}
		}

		if st.Status == job.StatusCompleted || st.Status == job.StatusFailed || st.Status == job.StatusCancelled {
			finalStatus = st
			break
		}
	}

	if finalStatus == nil || finalStatus.Status != job.StatusCompleted {
		t.Fatalf("expected completed status, got %+v", finalStatus)
	}

	t.Logf("Observed intermediate progress percentages: %v", observedPercents)
	if len(observedPercents) < 2 {
		t.Errorf("expected multiple intermediate progress samples, got %v", observedPercents)
	}
}
