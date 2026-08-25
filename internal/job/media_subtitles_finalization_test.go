package job

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestMediaSubtitlesFinalization(t *testing.T) {
	t.Run("1. No subtitle regression (SubtitleOptions absent)", func(t *testing.T) {
		mgr, jobRepo, _, storageSrv, downloadDir, dataDir := setupStorageTestEnv(t)
		ctx := context.Background()

		workDir := filepath.Join(dataDir, "temp_media_nosub")
		_ = os.MkdirAll(workDir, 0755)
		_ = storageSrv.PrepareWorkDir(ctx, "job_nosub", workDir)

		mediaFile := filepath.Join(workDir, "movie.mkv")
		_ = os.WriteFile(mediaFile, []byte("media content"), 0644)

		j := &Job{
			ID:             "job_nosub",
			Source:         "https://example.com/watch?v=nosub",
			Name:           "No Subtitle Test",
			Status:         StatusDownloading,
			Engine:         "ytdlp",
			EngineID:       "ytdlp_nosub_gid",
			Type:           TypeMedia,
			DestinationDir: downloadDir,
			WorkDir:        workDir,
			MediaInfo: &MediaInfo{
				SelectedFmt: "18",
			},
			CreatedAt: time.Now(),
			UpdatedAt: time.Now(),
		}
		_ = jobRepo.Create(ctx, j)

		mgr.UpdateJobFromEngine(ctx, j, &EngineStatus{
			Status:     StatusCompleted,
			Progress:   100,
			OutputPath: mediaFile,
		}, true)

		updated, _ := jobRepo.GetByID(ctx, j.ID)
		if updated.Status != StatusCompleted {
			t.Fatalf("expected StatusCompleted, got %s (err: %s)", updated.Status, updated.Error)
		}
		if updated.FinalPath != filepath.Join(downloadDir, "movie.mkv") {
			t.Errorf("expected FinalPath %s, got %s", filepath.Join(downloadDir, "movie.mkv"), updated.FinalPath)
		}

		// Verify media exists in DestinationDir
		if _, err := os.Stat(updated.FinalPath); err != nil {
			t.Errorf("expected media file in destination, got err: %v", err)
		}

		// Verify WorkDir cleaned
		if _, err := os.Stat(workDir); !os.IsNotExist(err) {
			t.Errorf("expected WorkDir to be removed, but still exists")
		}
	})

	t.Run("2. Separate + manual SRT", func(t *testing.T) {
		mgr, jobRepo, _, storageSrv, downloadDir, dataDir := setupStorageTestEnv(t)
		ctx := context.Background()

		workDir := filepath.Join(dataDir, "temp_media_sep_srt")
		_ = os.MkdirAll(workDir, 0755)
		_ = storageSrv.PrepareWorkDir(ctx, "job_sep_srt", workDir)

		mediaFile := filepath.Join(workDir, "movie.mkv")
		subFile := filepath.Join(workDir, "movie.en.srt")
		_ = os.WriteFile(mediaFile, []byte("media content"), 0644)
		_ = os.WriteFile(subFile, []byte("1\n00:00:01,000 --> 00:00:02,000\nHello"), 0644)

		j := &Job{
			ID:             "job_sep_srt",
			Source:         "https://example.com/watch?v=sep_srt",
			Name:           "Separate SRT Test",
			Status:         StatusDownloading,
			Engine:         "ytdlp",
			EngineID:       "ytdlp_sep_srt_gid",
			Type:           TypeMedia,
			DestinationDir: downloadDir,
			WorkDir:        workDir,
			MediaInfo: &MediaInfo{
				SelectedFmt: "18",
				SubtitleOptions: &SubtitleOptions{
					Languages: []string{"en"},
					Mode:      SubtitleModeSeparate,
					Format:    SubtitleFormatSRT,
				},
			},
			CreatedAt: time.Now(),
			UpdatedAt: time.Now(),
		}
		_ = jobRepo.Create(ctx, j)

		mgr.UpdateJobFromEngine(ctx, j, &EngineStatus{
			Status:     StatusCompleted,
			Progress:   100,
			OutputPath: mediaFile,
		}, true)

		updated, _ := jobRepo.GetByID(ctx, j.ID)
		if updated.Status != StatusCompleted {
			t.Fatalf("expected StatusCompleted, got %s (err: %s)", updated.Status, updated.Error)
		}

		// FinalPath must point to media file
		if updated.FinalPath != filepath.Join(downloadDir, "movie.mkv") {
			t.Errorf("expected FinalPath %s, got %s", filepath.Join(downloadDir, "movie.mkv"), updated.FinalPath)
		}

		// Verify media and subtitle both exist in DestinationDir
		if _, err := os.Stat(filepath.Join(downloadDir, "movie.mkv")); err != nil {
			t.Errorf("media file missing in destination: %v", err)
		}
		if _, err := os.Stat(filepath.Join(downloadDir, "movie.en.srt")); err != nil {
			t.Errorf("subtitle file missing in destination: %v", err)
		}

		// Verify WorkDir cleaned
		if _, err := os.Stat(workDir); !os.IsNotExist(err) {
			t.Errorf("expected WorkDir to be cleaned")
		}
	})

	t.Run("3. Separate + VTT", func(t *testing.T) {
		mgr, jobRepo, _, storageSrv, downloadDir, dataDir := setupStorageTestEnv(t)
		ctx := context.Background()

		workDir := filepath.Join(dataDir, "temp_media_sep_vtt")
		_ = os.MkdirAll(workDir, 0755)
		_ = storageSrv.PrepareWorkDir(ctx, "job_sep_vtt", workDir)

		mediaFile := filepath.Join(workDir, "movie.mkv")
		subFile := filepath.Join(workDir, "movie.en.vtt")
		_ = os.WriteFile(mediaFile, []byte("media content"), 0644)
		_ = os.WriteFile(subFile, []byte("WEBVTT\n\n00:01.000 --> 00:02.000\nHello"), 0644)

		j := &Job{
			ID:             "job_sep_vtt",
			Source:         "https://example.com/watch?v=sep_vtt",
			Name:           "Separate VTT Test",
			Status:         StatusDownloading,
			Engine:         "ytdlp",
			EngineID:       "ytdlp_sep_vtt_gid",
			Type:           TypeMedia,
			DestinationDir: downloadDir,
			WorkDir:        workDir,
			MediaInfo: &MediaInfo{
				SelectedFmt: "18",
				SubtitleOptions: &SubtitleOptions{
					Languages: []string{"en"},
					Mode:      SubtitleModeSeparate,
					Format:    SubtitleFormatVTT,
				},
			},
			CreatedAt: time.Now(),
			UpdatedAt: time.Now(),
		}
		_ = jobRepo.Create(ctx, j)

		mgr.UpdateJobFromEngine(ctx, j, &EngineStatus{
			Status:     StatusCompleted,
			Progress:   100,
			OutputPath: mediaFile,
		}, true)

		updated, _ := jobRepo.GetByID(ctx, j.ID)
		if updated.Status != StatusCompleted {
			t.Fatalf("expected StatusCompleted, got %s (err: %s)", updated.Status, updated.Error)
		}

		if _, err := os.Stat(filepath.Join(downloadDir, "movie.mkv")); err != nil {
			t.Errorf("media file missing in destination: %v", err)
		}
		if _, err := os.Stat(filepath.Join(downloadDir, "movie.en.vtt")); err != nil {
			t.Errorf("vtt subtitle file missing in destination: %v", err)
		}
	})

	t.Run("4. Separate + multiple languages", func(t *testing.T) {
		mgr, jobRepo, _, storageSrv, downloadDir, dataDir := setupStorageTestEnv(t)
		ctx := context.Background()

		workDir := filepath.Join(dataDir, "temp_media_multi")
		_ = os.MkdirAll(workDir, 0755)
		_ = storageSrv.PrepareWorkDir(ctx, "job_multi", workDir)

		mediaFile := filepath.Join(workDir, "movie.mkv")
		subEn := filepath.Join(workDir, "movie.en.srt")
		subJa := filepath.Join(workDir, "movie.ja.srt")
		_ = os.WriteFile(mediaFile, []byte("media content"), 0644)
		_ = os.WriteFile(subEn, []byte("sub en"), 0644)
		_ = os.WriteFile(subJa, []byte("sub ja"), 0644)

		j := &Job{
			ID:             "job_multi",
			Source:         "https://example.com/watch?v=multi",
			Name:           "Multi Subtitle Test",
			Status:         StatusDownloading,
			Engine:         "ytdlp",
			EngineID:       "ytdlp_multi_gid",
			Type:           TypeMedia,
			DestinationDir: downloadDir,
			WorkDir:        workDir,
			MediaInfo: &MediaInfo{
				SelectedFmt: "18",
				SubtitleOptions: &SubtitleOptions{
					Languages: []string{"en", "ja"},
					Mode:      SubtitleModeSeparate,
					Format:    SubtitleFormatSRT,
				},
			},
			CreatedAt: time.Now(),
			UpdatedAt: time.Now(),
		}
		_ = jobRepo.Create(ctx, j)

		mgr.UpdateJobFromEngine(ctx, j, &EngineStatus{
			Status:     StatusCompleted,
			Progress:   100,
			OutputPath: mediaFile,
		}, true)

		updated, _ := jobRepo.GetByID(ctx, j.ID)
		if updated.Status != StatusCompleted {
			t.Fatalf("expected StatusCompleted, got %s (err: %s)", updated.Status, updated.Error)
		}

		if _, err := os.Stat(filepath.Join(downloadDir, "movie.mkv")); err != nil {
			t.Errorf("media missing: %v", err)
		}
		if _, err := os.Stat(filepath.Join(downloadDir, "movie.en.srt")); err != nil {
			t.Errorf("en subtitle missing: %v", err)
		}
		if _, err := os.Stat(filepath.Join(downloadDir, "movie.ja.srt")); err != nil {
			t.Errorf("ja subtitle missing: %v", err)
		}
	})

	t.Run("5. Both + manual subtitle", func(t *testing.T) {
		mgr, jobRepo, _, storageSrv, downloadDir, dataDir := setupStorageTestEnv(t)
		ctx := context.Background()

		workDir := filepath.Join(dataDir, "temp_media_both_man")
		_ = os.MkdirAll(workDir, 0755)
		_ = storageSrv.PrepareWorkDir(ctx, "job_both_man", workDir)

		mediaFile := filepath.Join(workDir, "movie.mkv")
		subFile := filepath.Join(workDir, "movie.ja.srt")
		_ = os.WriteFile(mediaFile, []byte("media content with embedded sub"), 0644)
		_ = os.WriteFile(subFile, []byte("sub ja"), 0644)

		j := &Job{
			ID:             "job_both_man",
			Source:         "https://example.com/watch?v=both_man",
			Name:           "Both Manual Test",
			Status:         StatusDownloading,
			Engine:         "ytdlp",
			EngineID:       "ytdlp_both_man_gid",
			Type:           TypeMedia,
			DestinationDir: downloadDir,
			WorkDir:        workDir,
			MediaInfo: &MediaInfo{
				SelectedFmt: "18",
				SubtitleOptions: &SubtitleOptions{
					Languages: []string{"ja"},
					Mode:      SubtitleModeBoth,
					Format:    SubtitleFormatSRT,
				},
			},
			CreatedAt: time.Now(),
			UpdatedAt: time.Now(),
		}
		_ = jobRepo.Create(ctx, j)

		mgr.UpdateJobFromEngine(ctx, j, &EngineStatus{
			Status:     StatusCompleted,
			Progress:   100,
			OutputPath: mediaFile,
		}, true)

		updated, _ := jobRepo.GetByID(ctx, j.ID)
		if updated.Status != StatusCompleted {
			t.Fatalf("expected StatusCompleted, got %s (err: %s)", updated.Status, updated.Error)
		}

		if _, err := os.Stat(filepath.Join(downloadDir, "movie.mkv")); err != nil {
			t.Errorf("media missing: %v", err)
		}
		if _, err := os.Stat(filepath.Join(downloadDir, "movie.ja.srt")); err != nil {
			t.Errorf("retained standalone subtitle missing: %v", err)
		}
	})

	t.Run("6. Both + auto-generated subtitle", func(t *testing.T) {
		mgr, jobRepo, _, storageSrv, downloadDir, dataDir := setupStorageTestEnv(t)
		ctx := context.Background()

		workDir := filepath.Join(dataDir, "temp_media_both_auto")
		_ = os.MkdirAll(workDir, 0755)
		_ = storageSrv.PrepareWorkDir(ctx, "job_both_auto", workDir)

		mediaFile := filepath.Join(workDir, "movie.mkv")
		subFile := filepath.Join(workDir, "movie.fr.vtt")
		_ = os.WriteFile(mediaFile, []byte("media content"), 0644)
		_ = os.WriteFile(subFile, []byte("auto sub fr"), 0644)

		j := &Job{
			ID:             "job_both_auto",
			Source:         "https://example.com/watch?v=both_auto",
			Name:           "Both Auto Test",
			Status:         StatusDownloading,
			Engine:         "ytdlp",
			EngineID:       "ytdlp_both_auto_gid",
			Type:           TypeMedia,
			DestinationDir: downloadDir,
			WorkDir:        workDir,
			MediaInfo: &MediaInfo{
				SelectedFmt: "18",
				SubtitleOptions: &SubtitleOptions{
					Languages:   []string{"fr"},
					IncludeAuto: true,
					Mode:        SubtitleModeBoth,
					Format:      SubtitleFormatVTT,
				},
			},
			CreatedAt: time.Now(),
			UpdatedAt: time.Now(),
		}
		_ = jobRepo.Create(ctx, j)

		mgr.UpdateJobFromEngine(ctx, j, &EngineStatus{
			Status:     StatusCompleted,
			Progress:   100,
			OutputPath: mediaFile,
		}, true)

		updated, _ := jobRepo.GetByID(ctx, j.ID)
		if updated.Status != StatusCompleted {
			t.Fatalf("expected StatusCompleted, got %s (err: %s)", updated.Status, updated.Error)
		}

		if _, err := os.Stat(filepath.Join(downloadDir, "movie.mkv")); err != nil {
			t.Errorf("media missing: %v", err)
		}
		if _, err := os.Stat(filepath.Join(downloadDir, "movie.fr.vtt")); err != nil {
			t.Errorf("retained auto subtitle missing: %v", err)
		}
	})

	t.Run("7. Both + English translation", func(t *testing.T) {
		mgr, jobRepo, _, storageSrv, downloadDir, dataDir := setupStorageTestEnv(t)
		ctx := context.Background()

		workDir := filepath.Join(dataDir, "temp_media_both_trans")
		_ = os.MkdirAll(workDir, 0755)
		_ = storageSrv.PrepareWorkDir(ctx, "job_both_trans", workDir)

		mediaFile := filepath.Join(workDir, "foreign_movie.mkv")
		subFile := filepath.Join(workDir, "foreign_movie.en.srt")
		_ = os.WriteFile(mediaFile, []byte("media content"), 0644)
		_ = os.WriteFile(subFile, []byte("translated english sub"), 0644)

		j := &Job{
			ID:             "job_both_trans",
			Source:         "https://example.com/watch?v=both_trans",
			Name:           "Both Translation Test",
			Status:         StatusDownloading,
			Engine:         "ytdlp",
			EngineID:       "ytdlp_both_trans_gid",
			Type:           TypeMedia,
			DestinationDir: downloadDir,
			WorkDir:        workDir,
			MediaInfo: &MediaInfo{
				SelectedFmt: "18",
				SubtitleOptions: &SubtitleOptions{
					Languages:          []string{},
					EnglishTranslation: true,
					Mode:               SubtitleModeBoth,
					Format:             SubtitleFormatSRT,
				},
			},
			CreatedAt: time.Now(),
			UpdatedAt: time.Now(),
		}
		_ = jobRepo.Create(ctx, j)

		mgr.UpdateJobFromEngine(ctx, j, &EngineStatus{
			Status:     StatusCompleted,
			Progress:   100,
			OutputPath: mediaFile,
		}, true)

		updated, _ := jobRepo.GetByID(ctx, j.ID)
		if updated.Status != StatusCompleted {
			t.Fatalf("expected StatusCompleted, got %s (err: %s)", updated.Status, updated.Error)
		}

		if _, err := os.Stat(filepath.Join(downloadDir, "foreign_movie.mkv")); err != nil {
			t.Errorf("media missing: %v", err)
		}
		if _, err := os.Stat(filepath.Join(downloadDir, "foreign_movie.en.srt")); err != nil {
			t.Errorf("translated english subtitle missing: %v", err)
		}
	})

	t.Run("8. Embed mode does NOT promote standalone subtitle file", func(t *testing.T) {
		mgr, jobRepo, _, storageSrv, downloadDir, dataDir := setupStorageTestEnv(t)
		ctx := context.Background()

		workDir := filepath.Join(dataDir, "temp_media_embed_only")
		_ = os.MkdirAll(workDir, 0755)
		_ = storageSrv.PrepareWorkDir(ctx, "job_embed_only", workDir)

		mediaFile := filepath.Join(workDir, "movie.mkv")
		tempSubFile := filepath.Join(workDir, "movie.en.srt")
		_ = os.WriteFile(mediaFile, []byte("embedded media content"), 0644)
		_ = os.WriteFile(tempSubFile, []byte("temp subtitle that was used for embedding"), 0644)

		j := &Job{
			ID:             "job_embed_only",
			Source:         "https://example.com/watch?v=embed_only",
			Name:           "Embed Only Test",
			Status:         StatusDownloading,
			Engine:         "ytdlp",
			EngineID:       "ytdlp_embed_only_gid",
			Type:           TypeMedia,
			DestinationDir: downloadDir,
			WorkDir:        workDir,
			MediaInfo: &MediaInfo{
				SelectedFmt: "18",
				SubtitleOptions: &SubtitleOptions{
					Languages: []string{"en"},
					Mode:      SubtitleModeEmbed,
					Format:    SubtitleFormatSRT,
				},
			},
			CreatedAt: time.Now(),
			UpdatedAt: time.Now(),
		}
		_ = jobRepo.Create(ctx, j)

		mgr.UpdateJobFromEngine(ctx, j, &EngineStatus{
			Status:     StatusCompleted,
			Progress:   100,
			OutputPath: mediaFile,
		}, true)

		updated, _ := jobRepo.GetByID(ctx, j.ID)
		if updated.Status != StatusCompleted {
			t.Fatalf("expected StatusCompleted, got %s (err: %s)", updated.Status, updated.Error)
		}

		// Primary media is promoted
		if _, err := os.Stat(filepath.Join(downloadDir, "movie.mkv")); err != nil {
			t.Errorf("media file missing: %v", err)
		}

		// Standalone subtitle MUST NOT be promoted in embed mode
		if _, err := os.Stat(filepath.Join(downloadDir, "movie.en.srt")); !os.IsNotExist(err) {
			t.Errorf("standalone subtitle unexpectedly promoted in embed mode")
		}

		// WorkDir is cleaned
		if _, err := os.Stat(workDir); !os.IsNotExist(err) {
			t.Errorf("expected WorkDir to be cleaned")
		}
	})

	t.Run("9. Wrong subtitle extension filtering", func(t *testing.T) {
		mgr, jobRepo, _, storageSrv, downloadDir, dataDir := setupStorageTestEnv(t)
		ctx := context.Background()

		workDir := filepath.Join(dataDir, "temp_media_ext_filter")
		_ = os.MkdirAll(workDir, 0755)
		_ = storageSrv.PrepareWorkDir(ctx, "job_ext_filter", workDir)

		mediaFile := filepath.Join(workDir, "movie.mkv")
		srtFile := filepath.Join(workDir, "movie.en.srt")
		unrelatedVtt := filepath.Join(workDir, "unrelated.vtt")
		_ = os.WriteFile(mediaFile, []byte("media content"), 0644)
		_ = os.WriteFile(srtFile, []byte("valid srt"), 0644)
		_ = os.WriteFile(unrelatedVtt, []byte("unrelated vtt"), 0644)

		j := &Job{
			ID:             "job_ext_filter",
			Source:         "https://example.com/watch?v=ext_filter",
			Name:           "Ext Filter Test",
			Status:         StatusDownloading,
			Engine:         "ytdlp",
			EngineID:       "ytdlp_ext_filter_gid",
			Type:           TypeMedia,
			DestinationDir: downloadDir,
			WorkDir:        workDir,
			MediaInfo: &MediaInfo{
				SelectedFmt: "18",
				SubtitleOptions: &SubtitleOptions{
					Languages: []string{"en"},
					Mode:      SubtitleModeSeparate,
					Format:    SubtitleFormatSRT,
				},
			},
			CreatedAt: time.Now(),
			UpdatedAt: time.Now(),
		}
		_ = jobRepo.Create(ctx, j)

		mgr.UpdateJobFromEngine(ctx, j, &EngineStatus{
			Status:     StatusCompleted,
			Progress:   100,
			OutputPath: mediaFile,
		}, true)

		// .srt promoted
		if _, err := os.Stat(filepath.Join(downloadDir, "movie.en.srt")); err != nil {
			t.Errorf("movie.en.srt missing: %v", err)
		}
		// .vtt NOT promoted
		if _, err := os.Stat(filepath.Join(downloadDir, "unrelated.vtt")); !os.IsNotExist(err) {
			t.Errorf("unrelated.vtt unexpectedly promoted when SRT was requested")
		}
	})

	t.Run("10. Temporary/unrelated files in WorkDir are NOT promoted", func(t *testing.T) {
		mgr, jobRepo, _, storageSrv, downloadDir, dataDir := setupStorageTestEnv(t)
		ctx := context.Background()

		workDir := filepath.Join(dataDir, "temp_media_unrelated")
		_ = os.MkdirAll(workDir, 0755)
		_ = storageSrv.PrepareWorkDir(ctx, "job_unrelated", workDir)

		mediaFile := filepath.Join(workDir, "movie.mkv")
		srtFile := filepath.Join(workDir, "movie.en.srt")
		_ = os.WriteFile(mediaFile, []byte("media content"), 0644)
		_ = os.WriteFile(srtFile, []byte("valid srt"), 0644)
		_ = os.WriteFile(filepath.Join(workDir, "thumbnail.jpg"), []byte("jpg"), 0644)
		_ = os.WriteFile(filepath.Join(workDir, "metadata.info.json"), []byte("{}"), 0644)
		_ = os.WriteFile(filepath.Join(workDir, "partial.part"), []byte("part"), 0644)
		_ = os.WriteFile(filepath.Join(workDir, "temp.tmp"), []byte("tmp"), 0644)
		_ = os.WriteFile(filepath.Join(workDir, "video.ytdl"), []byte("ytdl"), 0644)

		j := &Job{
			ID:             "job_unrelated",
			Source:         "https://example.com/watch?v=unrelated",
			Name:           "Unrelated Files Test",
			Status:         StatusDownloading,
			Engine:         "ytdlp",
			EngineID:       "ytdlp_unrelated_gid",
			Type:           TypeMedia,
			DestinationDir: downloadDir,
			WorkDir:        workDir,
			MediaInfo: &MediaInfo{
				SelectedFmt: "18",
				SubtitleOptions: &SubtitleOptions{
					Languages: []string{"en"},
					Mode:      SubtitleModeSeparate,
					Format:    SubtitleFormatSRT,
				},
			},
			CreatedAt: time.Now(),
			UpdatedAt: time.Now(),
		}
		_ = jobRepo.Create(ctx, j)

		mgr.UpdateJobFromEngine(ctx, j, &EngineStatus{
			Status:     StatusCompleted,
			Progress:   100,
			OutputPath: mediaFile,
		}, true)

		// Only media and srt should be promoted
		if _, err := os.Stat(filepath.Join(downloadDir, "movie.mkv")); err != nil {
			t.Errorf("media file missing: %v", err)
		}
		if _, err := os.Stat(filepath.Join(downloadDir, "movie.en.srt")); err != nil {
			t.Errorf("srt file missing: %v", err)
		}
		if _, err := os.Stat(filepath.Join(downloadDir, "thumbnail.jpg")); !os.IsNotExist(err) {
			t.Errorf("thumbnail.jpg unexpectedly promoted")
		}
		if _, err := os.Stat(filepath.Join(downloadDir, "metadata.info.json")); !os.IsNotExist(err) {
			t.Errorf("metadata.info.json unexpectedly promoted")
		}
		if _, err := os.Stat(filepath.Join(downloadDir, "partial.part")); !os.IsNotExist(err) {
			t.Errorf("partial.part unexpectedly promoted")
		}
	})

	t.Run("12. Non-regular file (directory with .srt extension) is NOT promoted", func(t *testing.T) {
		mgr, jobRepo, _, storageSrv, downloadDir, dataDir := setupStorageTestEnv(t)
		ctx := context.Background()

		workDir := filepath.Join(dataDir, "temp_media_dir_sub")
		_ = os.MkdirAll(workDir, 0755)
		_ = storageSrv.PrepareWorkDir(ctx, "job_dir_sub", workDir)

		mediaFile := filepath.Join(workDir, "movie.mkv")
		dirAsSub := filepath.Join(workDir, "fake_sub.srt")
		_ = os.WriteFile(mediaFile, []byte("media content"), 0644)
		_ = os.MkdirAll(dirAsSub, 0755) // directory, not regular file

		j := &Job{
			ID:             "job_dir_sub",
			Source:         "https://example.com/watch?v=dir_sub",
			Name:           "Directory Sub Test",
			Status:         StatusDownloading,
			Engine:         "ytdlp",
			EngineID:       "ytdlp_dir_sub_gid",
			Type:           TypeMedia,
			DestinationDir: downloadDir,
			WorkDir:        workDir,
			MediaInfo: &MediaInfo{
				SelectedFmt: "18",
				SubtitleOptions: &SubtitleOptions{
					Languages: []string{"en"},
					Mode:      SubtitleModeSeparate,
					Format:    SubtitleFormatSRT,
				},
			},
			CreatedAt: time.Now(),
			UpdatedAt: time.Now(),
		}
		_ = jobRepo.Create(ctx, j)

		mgr.UpdateJobFromEngine(ctx, j, &EngineStatus{
			Status:     StatusCompleted,
			Progress:   100,
			OutputPath: mediaFile,
		}, true)

		updated, _ := jobRepo.GetByID(ctx, j.ID)
		if updated.Status != StatusCompleted {
			t.Fatalf("expected StatusCompleted, got %s (err: %s)", updated.Status, updated.Error)
		}

		if _, err := os.Stat(filepath.Join(downloadDir, "fake_sub.srt")); !os.IsNotExist(err) {
			t.Errorf("directory fake_sub.srt was unexpectedly promoted")
		}
	})

	t.Run("13. Conflict rename policy on existing destination subtitle", func(t *testing.T) {
		mgr, jobRepo, _, storageSrv, downloadDir, dataDir := setupStorageTestEnv(t)
		ctx := context.Background()

		workDir := filepath.Join(dataDir, "temp_media_conflict_ren")
		_ = os.MkdirAll(workDir, 0755)
		_ = storageSrv.PrepareWorkDir(ctx, "job_conflict_ren", workDir)

		// Pre-create existing subtitle file in destination
		existingSub := filepath.Join(downloadDir, "movie.en.srt")
		_ = os.WriteFile(existingSub, []byte("existing sub"), 0644)

		mediaFile := filepath.Join(workDir, "movie.mkv")
		newSub := filepath.Join(workDir, "movie.en.srt")
		_ = os.WriteFile(mediaFile, []byte("media content"), 0644)
		_ = os.WriteFile(newSub, []byte("new subtitle version"), 0644)

		j := &Job{
			ID:             "job_conflict_ren",
			Source:         "https://example.com/watch?v=conflict_ren",
			Name:           "Conflict Rename Test",
			Status:         StatusDownloading,
			Engine:         "ytdlp",
			EngineID:       "ytdlp_conflict_ren_gid",
			Type:           TypeMedia,
			DestinationDir: downloadDir,
			ConflictPolicy: ConflictPolicyRename,
			WorkDir:        workDir,
			MediaInfo: &MediaInfo{
				SelectedFmt: "18",
				SubtitleOptions: &SubtitleOptions{
					Languages: []string{"en"},
					Mode:      SubtitleModeSeparate,
					Format:    SubtitleFormatSRT,
				},
			},
			CreatedAt: time.Now(),
			UpdatedAt: time.Now(),
		}
		_ = jobRepo.Create(ctx, j)

		mgr.UpdateJobFromEngine(ctx, j, &EngineStatus{
			Status:     StatusCompleted,
			Progress:   100,
			OutputPath: mediaFile,
		}, true)

		updated, _ := jobRepo.GetByID(ctx, j.ID)
		if updated.Status != StatusCompleted {
			t.Fatalf("expected StatusCompleted, got %s (err: %s)", updated.Status, updated.Error)
		}

		// Original existing sub must remain unchanged
		origContent, _ := os.ReadFile(existingSub)
		if string(origContent) != "existing sub" {
			t.Errorf("original subtitle was overwritten")
		}

		// New sub should be renamed to movie.en (1).srt
		renamedSub := filepath.Join(downloadDir, "movie.en (1).srt")
		newContent, err := os.ReadFile(renamedSub)
		if err != nil {
			t.Fatalf("renamed subtitle movie.en (1).srt missing: %v", err)
		}
		if string(newContent) != "new subtitle version" {
			t.Errorf("expected new subtitle version in renamed file, got: %s", string(newContent))
		}
	})

	t.Run("14. Subtitle finalization failure sets job to StatusFailed", func(t *testing.T) {
		mgr, jobRepo, _, storageSrv, downloadDir, dataDir := setupStorageTestEnv(t)
		ctx := context.Background()

		workDir := filepath.Join(dataDir, "temp_media_sub_fail")
		_ = os.MkdirAll(workDir, 0755)
		_ = storageSrv.PrepareWorkDir(ctx, "job_sub_fail", workDir)

		mediaFile := filepath.Join(workDir, "movie.mkv")
		subFile := filepath.Join(workDir, "movie.en.srt")
		_ = os.WriteFile(mediaFile, []byte("media content"), 0644)
		_ = os.WriteFile(subFile, []byte("sub content"), 0644)

		// Create a file named movie.en.srt in destination so FinalizeFile fails when policy is Fail
		existingConflict := filepath.Join(downloadDir, "movie.en.srt")
		_ = os.WriteFile(existingConflict, []byte("already exists"), 0644)

		j := &Job{
			ID:             "job_sub_fail",
			Source:         "https://example.com/watch?v=sub_fail",
			Name:           "Sub Fail Test",
			Status:         StatusDownloading,
			Engine:         "ytdlp",
			EngineID:       "ytdlp_sub_fail_gid",
			Type:           TypeMedia,
			DestinationDir: downloadDir,
			ConflictPolicy: ConflictPolicyFail, // will fail when destination file exists
			WorkDir:        workDir,
			MediaInfo: &MediaInfo{
				SelectedFmt: "18",
				SubtitleOptions: &SubtitleOptions{
					Languages: []string{"en"},
					Mode:      SubtitleModeSeparate,
					Format:    SubtitleFormatSRT,
				},
			},
			CreatedAt: time.Now(),
			UpdatedAt: time.Now(),
		}
		_ = jobRepo.Create(ctx, j)

		mgr.UpdateJobFromEngine(ctx, j, &EngineStatus{
			Status:     StatusCompleted,
			Progress:   100,
			OutputPath: mediaFile,
		}, true)

		updated, _ := jobRepo.GetByID(ctx, j.ID)
		if updated.Status != StatusFailed {
			t.Fatalf("expected StatusFailed when subtitle finalization fails, got %s", updated.Status)
		}
		if !strings.Contains(updated.Error, "subtitle finalization failed") {
			t.Errorf("expected error message to mention subtitle finalization, got: %s", updated.Error)
		}
	})

	t.Run("15 & 16. Existing media final path preserved & sidecars finalized before WorkDir cleanup", func(t *testing.T) {
		mgr, jobRepo, _, storageSrv, downloadDir, dataDir := setupStorageTestEnv(t)
		ctx := context.Background()

		workDir := filepath.Join(dataDir, "temp_media_ordering")
		_ = os.MkdirAll(workDir, 0755)
		_ = storageSrv.PrepareWorkDir(ctx, "job_ordering", workDir)

		mediaFile := filepath.Join(workDir, "feature_film.mkv")
		subFile := filepath.Join(workDir, "feature_film.en.srt")
		_ = os.WriteFile(mediaFile, []byte("feature film media payload 12345"), 0644)
		_ = os.WriteFile(subFile, []byte("subtitle payload"), 0644)

		j := &Job{
			ID:             "job_ordering",
			Source:         "https://example.com/watch?v=ordering",
			Name:           "Ordering Test",
			Status:         StatusDownloading,
			Engine:         "ytdlp",
			EngineID:       "ytdlp_ordering_gid",
			Type:           TypeMedia,
			DestinationDir: downloadDir,
			WorkDir:        workDir,
			MediaInfo: &MediaInfo{
				SelectedFmt: "18",
				SubtitleOptions: &SubtitleOptions{
					Languages: []string{"en"},
					Mode:      SubtitleModeSeparate,
					Format:    SubtitleFormatSRT,
				},
			},
			CreatedAt: time.Now(),
			UpdatedAt: time.Now(),
		}
		_ = jobRepo.Create(ctx, j)

		mgr.UpdateJobFromEngine(ctx, j, &EngineStatus{
			Status:     StatusCompleted,
			Progress:   100,
			OutputPath: mediaFile,
		}, true)

		updated, _ := jobRepo.GetByID(ctx, j.ID)
		if updated.Status != StatusCompleted {
			t.Fatalf("expected StatusCompleted, got %s (err: %s)", updated.Status, updated.Error)
		}

		// 15. Job.FinalPath must point to media, not subtitle
		expectedFinalPath := filepath.Join(downloadDir, "feature_film.mkv")
		if updated.FinalPath != expectedFinalPath {
			t.Errorf("FinalPath must point to primary media, expected %s, got %s", expectedFinalPath, updated.FinalPath)
		}
		if updated.Name != "feature_film.mkv" {
			t.Errorf("Job Name must be primary media basename, expected feature_film.mkv, got %s", updated.Name)
		}

		// 16. Both artifacts must exist in destination, and WorkDir must be cleaned
		if _, err := os.Stat(expectedFinalPath); err != nil {
			t.Errorf("primary media missing from destination: %v", err)
		}
		if _, err := os.Stat(filepath.Join(downloadDir, "feature_film.en.srt")); err != nil {
			t.Errorf("subtitle missing from destination: %v", err)
		}
		if _, err := os.Stat(workDir); !os.IsNotExist(err) {
			t.Errorf("WorkDir was not cleaned after successful finalization")
		}
	})
}
