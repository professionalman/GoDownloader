package ytdlp

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"downloader/internal/job"
)

func TestEngine_Start_Subtitles_RealChild(t *testing.T) {
	binPath := buildFakeYtDlp(t)
	tmpDir := t.TempDir()
	argsFile := filepath.Join(tmpDir, "args.txt")
	t.Setenv("FAKE_YTDLP_RECORD_ARGS", argsFile)

	eng := NewEngine(binPath, tmpDir)

	j := &job.Job{
		ID:     "sub_job_1",
		Source: "https://example.com/watch?v=12345",
		Type:   job.TypeMedia,
		MediaInfo: &job.MediaInfo{
			SelectedFmt: "18",
			Formats: []job.MediaFormat{
				{FormatID: "18", VCodec: "avc1", ACodec: "mp4a"},
			},
			Subtitles: &job.SubtitleCapabilities{
				Tracks: []job.SubtitleTrack{
					{Language: "ja", Name: "Japanese", Manual: true, Formats: []string{"vtt"}},
				},
			},
			SubtitleOptions: &job.SubtitleOptions{
				Languages: []string{"ja"},
				Mode:      job.SubtitleModeSeparate,
				Format:    job.SubtitleFormatSRT,
			},
		},
	}

	engineID, err := eng.Start(context.Background(), j, tmpDir)
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	if engineID != j.ID {
		t.Fatalf("expected engineID %s, got %s", j.ID, engineID)
	}

	// Wait for process completion
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if _, statErr := os.Stat(argsFile); statErr == nil {
			st, _ := eng.Status(context.Background(), j)
			if st != nil && (st.Status == job.StatusCompleted || st.Status == job.StatusFailed) {
				break
			}
		}
		time.Sleep(50 * time.Millisecond)
	}

	data, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatalf("failed to read recorded args: %v", err)
	}
	args := strings.Split(string(data), "\n")

	// Verify exact argv flags
	if !slices.Contains(args, "--sub-langs") {
		t.Errorf("missing --sub-langs in argv: %v", args)
	}
	subLangsIdx := slices.Index(args, "--sub-langs")
	if subLangsIdx < 0 || subLangsIdx+1 >= len(args) || args[subLangsIdx+1] != "ja" {
		t.Errorf("expected --sub-langs ja, got args: %v", args)
	}

	if !slices.Contains(args, "--write-subs") {
		t.Errorf("missing --write-subs in argv: %v", args)
	}
	if !slices.Contains(args, "--convert-subs") {
		t.Errorf("missing --convert-subs in argv: %v", args)
	}
	convertIdx := slices.Index(args, "--convert-subs")
	if convertIdx < 0 || convertIdx+1 >= len(args) || args[convertIdx+1] != "srt" {
		t.Errorf("expected --convert-subs srt, got args: %v", args)
	}

	// Verify format and source are present
	if !slices.Contains(args, "-f") || !slices.Contains(args, "18") {
		t.Errorf("format flags missing from argv: %v", args)
	}
	if !slices.Contains(args, j.Source) {
		t.Errorf("source URL missing from argv: %v", args)
	}
}

func TestEngine_Start_AuthAndSubtitles_Composition_RealChild(t *testing.T) {
	binPath := buildFakeYtDlp(t)
	tmpDir := t.TempDir()
	argsFile := filepath.Join(tmpDir, "args.txt")
	cookieCheckFile := filepath.Join(tmpDir, "cookie_checked.txt")
	cookieFile := filepath.Join(tmpDir, "imported_cookies.txt")

	if err := os.WriteFile(cookieFile, []byte("# Netscape HTTP Cookie File\n.example.com\tTRUE\t/\tTRUE\t2147483647\tsession\tvalid\n"), 0600); err != nil {
		t.Fatal(err)
	}

	t.Setenv("FAKE_YTDLP_RECORD_ARGS", argsFile)
	t.Setenv("FAKE_YTDLP_COOKIE_CHECK", cookieCheckFile)

	mockAuth := &mockAuthProvider{
		args:     []string{"--cookies", cookieFile},
		tempFile: cookieFile,
	}

	eng := NewEngine(binPath, tmpDir)
	eng.SetAuthProvider(mockAuth)

	j := &job.Job{
		ID:     "auth_sub_job",
		Source: "https://example.com/protected/video",
		Type:   job.TypeMedia,
		MediaInfo: &job.MediaInfo{
			SelectedFmt: "137",
			Formats: []job.MediaFormat{
				{FormatID: "137", VCodec: "avc1", ACodec: "none"},
				{FormatID: "140", VCodec: "none", ACodec: "mp4a"},
			},
			Subtitles: &job.SubtitleCapabilities{
				Tracks: []job.SubtitleTrack{
					{Language: "ja", Name: "Japanese", Manual: true, Formats: []string{"vtt"}},
					{Language: "en", Name: "English", Manual: false, Auto: true, Formats: []string{"vtt"}},
				},
				EnglishTranslationAvailable: true,
				TranslationLanguageKey:      "en",
			},
			SubtitleOptions: &job.SubtitleOptions{
				Languages:          []string{"ja"},
				IncludeAuto:        true,
				EnglishTranslation: true,
				Mode:               job.SubtitleModeBoth,
				Format:             job.SubtitleFormatSRT,
			},
		},
	}

	_, err := eng.Start(context.Background(), j, tmpDir)
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	// Wait for process completion
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if _, statErr := os.Stat(argsFile); statErr == nil {
			st, _ := eng.Status(context.Background(), j)
			if st != nil && (st.Status == job.StatusCompleted || st.Status == job.StatusFailed) {
				break
			}
		}
		time.Sleep(50 * time.Millisecond)
	}

	// 1. Verify cookie check occurred during execution
	if _, err := os.Stat(cookieCheckFile); err != nil {
		t.Fatalf("child process did not verify cookie file existence during execution: %v", err)
	}

	// 2. Verify argv captured contains all expected flags
	data, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatalf("failed to read recorded args: %v", err)
	}
	args := strings.Split(string(data), "\n")

	// Check auth flags
	if !slices.Contains(args, "--cookies") || !slices.Contains(args, cookieFile) {
		t.Errorf("cookie auth arguments missing from argv: %v", args)
	}

	// Check subtitle flags
	if !slices.Contains(args, "--sub-langs") || !slices.Contains(args, "ja,en") {
		t.Errorf("expected --sub-langs ja,en, got args: %v", args)
	}
	if !slices.Contains(args, "--write-subs") {
		t.Errorf("missing --write-subs in argv: %v", args)
	}
	if !slices.Contains(args, "--embed-subs") {
		t.Errorf("missing --embed-subs in argv: %v", args)
	}
	if !slices.Contains(args, "--write-auto-subs") {
		t.Errorf("missing --write-auto-subs in argv: %v", args)
	}
	if !slices.Contains(args, "--convert-subs") || !slices.Contains(args, "srt") {
		t.Errorf("missing --convert-subs srt in argv: %v", args)
	}

	// Check format flags
	if !slices.Contains(args, "-f") || !slices.Contains(args, "137+bestaudio/best") {
		t.Errorf("missing format selector 137+bestaudio/best in argv: %v", args)
	}

	// Check source URL
	if !slices.Contains(args, j.Source) {
		t.Errorf("missing source URL in argv: %v", args)
	}

	// 3. Verify auth cleanup ran
	if !mockAuth.cleanupRan.Load() {
		t.Errorf("expected auth cleanup to run after download completes")
	}
}

func TestEngine_Start_EnglishTranslationOnly_RealChild(t *testing.T) {
	binPath := buildFakeYtDlp(t)
	tmpDir := t.TempDir()
	argsFile := filepath.Join(tmpDir, "args_trans_only.txt")
	t.Setenv("FAKE_YTDLP_RECORD_ARGS", argsFile)

	eng := NewEngine(binPath, tmpDir)

	j := &job.Job{
		ID:     "trans_only_job",
		Source: "https://example.com/watch?v=foreign_video",
		Type:   job.TypeMedia,
		MediaInfo: &job.MediaInfo{
			SelectedFmt: "18",
			Formats: []job.MediaFormat{
				{FormatID: "18", VCodec: "avc1", ACodec: "mp4a"},
			},
			Subtitles: &job.SubtitleCapabilities{
				Tracks: []job.SubtitleTrack{
					{Language: "ja", Name: "Japanese", Manual: true, Formats: []string{"vtt"}},
				},
				EnglishTranslationAvailable: true,
				TranslationLanguageKey:      "en",
			},
			SubtitleOptions: &job.SubtitleOptions{
				Languages:          []string{},
				IncludeAuto:        false,
				EnglishTranslation: true,
				Mode:               job.SubtitleModeSeparate,
				Format:             job.SubtitleFormatSRT,
			},
		},
	}

	_, err := eng.Start(context.Background(), j, tmpDir)
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	// Wait for process completion
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if _, statErr := os.Stat(argsFile); statErr == nil {
			st, _ := eng.Status(context.Background(), j)
			if st != nil && (st.Status == job.StatusCompleted || st.Status == job.StatusFailed) {
				break
			}
		}
		time.Sleep(50 * time.Millisecond)
	}

	data, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatalf("failed to read recorded args: %v", err)
	}
	args := strings.Split(string(data), "\n")

	// Verify that translation only passes --write-auto-subs and NOT --write-subs
	if !slices.Contains(args, "--sub-langs") || !slices.Contains(args, "en") {
		t.Errorf("expected --sub-langs en in argv: %v", args)
	}
	if !slices.Contains(args, "--write-auto-subs") {
		t.Errorf("missing --write-auto-subs in argv for English translation: %v", args)
	}
	if slices.Contains(args, "--write-subs") {
		t.Errorf("unexpected --write-subs in argv when only English translation is selected: %v", args)
	}
	if !slices.Contains(args, "--convert-subs") || !slices.Contains(args, "srt") {
		t.Errorf("missing --convert-subs srt in argv: %v", args)
	}
}

func TestEngine_Start_EnglishTranslationOnly_BothMode_RealChild(t *testing.T) {
	binPath := buildFakeYtDlp(t)
	tmpDir := t.TempDir()
	argsFile := filepath.Join(tmpDir, "args_trans_both.txt")
	t.Setenv("FAKE_YTDLP_RECORD_ARGS", argsFile)

	eng := NewEngine(binPath, tmpDir)

	j := &job.Job{
		ID:     "trans_both_job",
		Source: "https://example.com/watch?v=foreign_video",
		Type:   job.TypeMedia,
		MediaInfo: &job.MediaInfo{
			SelectedFmt: "18",
			Formats: []job.MediaFormat{
				{FormatID: "18", VCodec: "avc1", ACodec: "mp4a"},
			},
			Subtitles: &job.SubtitleCapabilities{
				Tracks: []job.SubtitleTrack{
					{Language: "ja", Name: "Japanese", Manual: true, Formats: []string{"vtt"}},
				},
				EnglishTranslationAvailable: true,
				TranslationLanguageKey:      "en",
			},
			SubtitleOptions: &job.SubtitleOptions{
				Languages:          []string{},
				IncludeAuto:        false,
				EnglishTranslation: true,
				Mode:               job.SubtitleModeBoth,
				Format:             job.SubtitleFormatSRT,
			},
		},
	}

	_, err := eng.Start(context.Background(), j, tmpDir)
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	deadline = time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if _, statErr := os.Stat(argsFile); statErr == nil {
			st, _ := eng.Status(context.Background(), j)
			if st != nil && (st.Status == job.StatusCompleted || st.Status == job.StatusFailed) {
				break
			}
		}
		time.Sleep(50 * time.Millisecond)
	}

	data, err = os.ReadFile(argsFile)
	if err != nil {
		t.Fatalf("failed to read recorded args: %v", err)
	}
	args = strings.Split(string(data), "\n")

	// Both mode must include --write-subs (for retention), --embed-subs, --write-auto-subs, and --convert-subs srt
	if !slices.Contains(args, "--sub-langs") || !slices.Contains(args, "en") {
		t.Errorf("expected --sub-langs en in argv: %v", args)
	}
	if !slices.Contains(args, "--write-subs") {
		t.Errorf("missing --write-subs in argv for Both mode file retention: %v", args)
	}
	if !slices.Contains(args, "--embed-subs") {
		t.Errorf("missing --embed-subs in argv for Both mode: %v", args)
	}
	if !slices.Contains(args, "--write-auto-subs") {
		t.Errorf("missing --write-auto-subs in argv for English translation: %v", args)
	}
	if !slices.Contains(args, "--convert-subs") || !slices.Contains(args, "srt") {
		t.Errorf("missing --convert-subs srt in argv: %v", args)
	}
}

func TestEngine_Start_EnglishTranslationOnly_EmbedMode_RealChild(t *testing.T) {
	binPath := buildFakeYtDlp(t)
	tmpDir := t.TempDir()
	argsFile := filepath.Join(tmpDir, "args_trans_embed.txt")
	t.Setenv("FAKE_YTDLP_RECORD_ARGS", argsFile)

	eng := NewEngine(binPath, tmpDir)

	j := &job.Job{
		ID:     "trans_embed_job",
		Source: "https://example.com/watch?v=foreign_video",
		Type:   job.TypeMedia,
		MediaInfo: &job.MediaInfo{
			SelectedFmt: "18",
			Formats: []job.MediaFormat{
				{FormatID: "18", VCodec: "avc1", ACodec: "mp4a"},
			},
			Subtitles: &job.SubtitleCapabilities{
				Tracks: []job.SubtitleTrack{
					{Language: "ja", Name: "Japanese", Manual: true, Formats: []string{"vtt"}},
				},
				EnglishTranslationAvailable: true,
				TranslationLanguageKey:      "en",
			},
			SubtitleOptions: &job.SubtitleOptions{
				Languages:          []string{},
				IncludeAuto:        false,
				EnglishTranslation: true,
				Mode:               job.SubtitleModeEmbed,
				Format:             job.SubtitleFormatSRT,
			},
		},
	}

	_, err := eng.Start(context.Background(), j, tmpDir)
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	deadline = time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if _, statErr := os.Stat(argsFile); statErr == nil {
			st, _ := eng.Status(context.Background(), j)
			if st != nil && (st.Status == job.StatusCompleted || st.Status == job.StatusFailed) {
				break
			}
		}
		time.Sleep(50 * time.Millisecond)
	}

	data, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatalf("failed to read recorded args: %v", err)
	}
	args := strings.Split(string(data), "\n")

	// Embed mode must include --embed-subs and --write-auto-subs, but NOT --write-subs
	if !slices.Contains(args, "--sub-langs") || !slices.Contains(args, "en") {
		t.Errorf("expected --sub-langs en in argv: %v", args)
	}
	if !slices.Contains(args, "--embed-subs") {
		t.Errorf("missing --embed-subs in argv for Embed mode: %v", args)
	}
	if !slices.Contains(args, "--write-auto-subs") {
		t.Errorf("missing --write-auto-subs in argv for English translation: %v", args)
	}
	if slices.Contains(args, "--write-subs") {
		t.Errorf("unexpected --write-subs in argv for Embed mode: %v", args)
	}
}
